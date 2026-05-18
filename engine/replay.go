package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/witlox/ghyll/runner"
)

// Replay-on-startup. After a process restart, the in-memory
// runner-layer stores (FindingsStore, ClassificationsStore, Grid,
// AmendmentQueue) start empty. The Replay function reads every
// persisted record from sqlite and applies it through the regular
// runner-layer mutators (Raise, Declare, Append, Enqueue).
//
// CRITICAL ordering invariant: the caller MUST replay BEFORE
// attaching the Journal. Otherwise the replayed mutations write
// back to the same sqlite rows they came from — recursive journaling.
// The engine package's New flow handles this ordering; tests that
// build the stores manually MUST mirror it.

// ReplayInto reads every v2 entity from store and replays it into
// the runner-layer caches. The caches MUST be freshly constructed
// (no journal observers attached). Returns the count of entities
// replayed per category for diagnostics.
type ReplayCounts struct {
	Findings          int
	Requirements      int
	Classifications   int
	Arrows            int
	AmendmentsActive  int
	AmendmentsDrained int
}

// ErrReplayCachesNotEmpty is returned if the caller tries to replay
// into pre-populated caches. Replay must run on fresh stores.
var ErrReplayCachesNotEmpty = errors.New("replay: target caches not empty")

// ReplayTargets bundles the runner-layer caches the replay populates.
type ReplayTargets struct {
	Findings        *runner.FindingsStore
	Classifications *runner.ClassificationsStore
	Grid            *runner.Grid
	Amendments      *runner.AmendmentQueue
}

// Replay loads every persisted v2 entity from store into targets.
//
// Order matters: requirements before classifications (foreign-key-
// style invariant in the runner layer); findings before transitions
// (already implicit because findings carry their final status —
// transitions are audit-only and not replayed back through the
// in-memory Transition state machine).
//
// ArrowStatus is never cached per the architect's preflight verdict;
// the runner re-derives it from the replayed inputs.
func Replay(ctx context.Context, store *Store, targets ReplayTargets) (ReplayCounts, error) {
	var c ReplayCounts
	if store == nil {
		return c, errors.New("replay: nil store")
	}
	if err := ensureEmpty(targets); err != nil {
		return c, err
	}

	// 1. Grid arrows. Replay newest version of each arrow ID;
	// older versions stay in the persistence table but aren't
	// loaded back into the in-memory grid (they're history).
	arrows, err := store.allLatestArrows(ctx)
	if err != nil {
		return c, fmt.Errorf("replay arrows: %w", err)
	}
	for _, a := range arrows {
		def, err := arrowFromRecord(a)
		if err != nil {
			return c, fmt.Errorf("replay arrow %s/v%d: %w", a.ID, a.GridVersion, err)
		}
		if a.Kind == "on-the-spot" {
			if _, err := targets.Grid.AppendOnTheSpot(def); err != nil {
				return c, fmt.Errorf("replay AppendOnTheSpot %s: %w", a.ID, err)
			}
		} else {
			if _, err := targets.Grid.Append(def); err != nil {
				return c, fmt.Errorf("replay Append %s: %w", a.ID, err)
			}
		}
		c.Arrows++
	}

	// 2. Requirements then Classifications. Requirements MUST land
	// first; ClassificationsStore.RecordClassification rejects an
	// undeclared requirement.
	reqs, err := store.allRequirements(ctx)
	if err != nil {
		return c, fmt.Errorf("replay requirements: %w", err)
	}
	for _, r := range reqs {
		if err := targets.Classifications.DeclareRequirement(r.ArrowID, runner.Requirement{
			ID:          r.ReqID,
			MinDepth:    runner.DepthRank(r.MinDepth),
			Description: r.Description,
		}); err != nil {
			return c, fmt.Errorf("replay DeclareRequirement %s/%s: %w", r.ArrowID, r.ReqID, err)
		}
		c.Requirements++
	}

	cls, err := store.allClassifications(ctx)
	if err != nil {
		return c, fmt.Errorf("replay classifications: %w", err)
	}
	for _, cl := range cls {
		if err := targets.Classifications.RecordClassification(cl.ArrowID, runner.Classification{
			RequirementID: cl.ReqID,
			Observed:      runner.DepthRank(cl.Observed),
			Evidence:      cl.Evidence,
		}); err != nil {
			return c, fmt.Errorf("replay RecordClassification %s/%s: %w", cl.ArrowID, cl.ReqID, err)
		}
		c.Classifications++
	}

	// 3. Findings. Carry the final Status verbatim; the runner's
	// Raise validates the status enum but accepts any valid value
	// (not just Open), so a Resolved finding replays directly.
	findings, err := store.ListFindings(ctx, FindingFilter{MinSeverity: -1, Limit: 1000000})
	if err != nil {
		return c, fmt.Errorf("replay findings: %w", err)
	}
	for _, f := range findings {
		status, err := runner.ParseFindingStatus(f.Status)
		if err != nil {
			return c, fmt.Errorf("replay finding %s status: %w", f.ID, err)
		}
		rec := runner.FindingRecord{
			ID:              f.ID,
			ArrowID:         f.ArrowID,
			Type:            runner.FindingType(f.Type),
			Severity:        f.Severity,
			Status:          status,
			Description:     f.Description,
			RaisedAt:        f.RaisedAt,
			RaisedByRole:    f.RaisedByRole,
			TransitionCount: f.TransitionCount,
		}
		if err := targets.Findings.Raise(rec); err != nil {
			return c, fmt.Errorf("replay Raise %s: %w", f.ID, err)
		}
		c.Findings++
	}

	// 4. Amendments. Pending ones (drained_at NULL) re-enqueue;
	// drained ones populate seenIDs only so re-emission stays
	// idempotent across process restarts (validation-pass-4 F44).
	pending := false
	pendingRows, err := store.ListAmendments(ctx, AmendmentFilter{Drained: &pending, Limit: 1000000})
	if err != nil {
		return c, fmt.Errorf("replay pending amendments: %w", err)
	}
	for _, a := range pendingRows {
		req, err := amendmentFromRecord(a)
		if err != nil {
			return c, fmt.Errorf("replay amendment %s: %w", a.ID, err)
		}
		if err := targets.Amendments.Enqueue(req); err != nil {
			return c, fmt.Errorf("replay Enqueue %s: %w", a.ID, err)
		}
		c.AmendmentsActive++
	}
	drained := true
	drainedRows, err := store.ListAmendments(ctx, AmendmentFilter{Drained: &drained, Limit: 1000000})
	if err != nil {
		return c, fmt.Errorf("replay drained amendments: %w", err)
	}
	for _, a := range drainedRows {
		targets.Amendments.LoadDrained(a.ID)
		c.AmendmentsDrained++
	}

	return c, nil
}

// ensureEmpty checks that the replay targets are freshly constructed
// (no entities). Replay into a pre-populated store would duplicate.
func ensureEmpty(t ReplayTargets) error {
	if t.Findings == nil || t.Classifications == nil || t.Grid == nil || t.Amendments == nil {
		return errors.New("replay: every ReplayTargets field required")
	}
	if t.Grid.Version() != 0 {
		return fmt.Errorf("%w: grid", ErrReplayCachesNotEmpty)
	}
	if t.Findings.Version() != 0 {
		return fmt.Errorf("%w: findings", ErrReplayCachesNotEmpty)
	}
	if t.Classifications.Version() != 0 {
		return fmt.Errorf("%w: classifications", ErrReplayCachesNotEmpty)
	}
	if t.Amendments.Len() != 0 {
		return fmt.Errorf("%w: amendments", ErrReplayCachesNotEmpty)
	}
	return nil
}

// allLatestArrows returns the highest grid_version row for each
// arrow ID. Older versions stay in the table for history; the
// in-memory Grid only holds the latest state.
func (s *Store) allLatestArrows(ctx context.Context) ([]GridArrowRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.grid_version, a.source_role, a.target_role,
		       a.stratum, a.context, a.clauses_json, a.requirements_json,
		       a.kind, a.declared_at
		FROM grid_arrows a
		INNER JOIN (
			SELECT id, MAX(grid_version) AS max_ver
			FROM grid_arrows
			GROUP BY id
		) latest
		ON a.id = latest.id AND a.grid_version = latest.max_ver
		ORDER BY a.id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []GridArrowRecord
	for rows.Next() {
		var a GridArrowRecord
		var gv int64
		if err := rows.Scan(&a.ID, &gv, &a.SourceRole, &a.TargetRole,
			&a.Stratum, &a.Context, &a.ClausesJSON, &a.RequirementsJSON,
			&a.Kind, &a.DeclaredAt); err != nil {
			return nil, err
		}
		a.GridVersion = uint64(gv)
		out = append(out, a)
	}
	return out, rows.Err()
}

// allRequirements returns every requirement row.
func (s *Store) allRequirements(ctx context.Context) ([]RequirementRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT arrow_id, req_id, min_depth, description, store_version, declared_at
		FROM requirements
		ORDER BY arrow_id ASC, req_id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []RequirementRecord
	for rows.Next() {
		var r RequirementRecord
		var sv int64
		if err := rows.Scan(&r.ArrowID, &r.ReqID, &r.MinDepth,
			&r.Description, &sv, &r.DeclaredAt); err != nil {
			return nil, err
		}
		r.StoreVersion = uint64(sv)
		out = append(out, r)
	}
	return out, rows.Err()
}

// allClassifications returns every classification row.
func (s *Store) allClassifications(ctx context.Context) ([]ClassificationRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT arrow_id, req_id, observed, evidence,
		       overwrite_count, store_version, classified_at
		FROM classifications
		ORDER BY arrow_id ASC, req_id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ClassificationRecord
	for rows.Next() {
		var c ClassificationRecord
		var sv int64
		if err := rows.Scan(&c.ArrowID, &c.ReqID, &c.Observed,
			&c.Evidence, &c.OverwriteCount, &sv, &c.ClassifiedAt); err != nil {
			return nil, err
		}
		c.StoreVersion = uint64(sv)
		out = append(out, c)
	}
	return out, rows.Err()
}

// arrowFromRecord re-hydrates a runner.ArrowDefinition from its
// persisted shape. Clauses and Requirements are JSON-decoded.
func arrowFromRecord(a GridArrowRecord) (runner.ArrowDefinition, error) {
	var clauses []runner.Clause
	if a.ClausesJSON != "" && a.ClausesJSON != "null" {
		if err := json.Unmarshal([]byte(a.ClausesJSON), &clauses); err != nil {
			return runner.ArrowDefinition{}, fmt.Errorf("clauses json: %w", err)
		}
	}
	var reqs []runner.Requirement
	if a.RequirementsJSON != "" && a.RequirementsJSON != "null" {
		if err := json.Unmarshal([]byte(a.RequirementsJSON), &reqs); err != nil {
			return runner.ArrowDefinition{}, fmt.Errorf("requirements json: %w", err)
		}
	}
	return runner.ArrowDefinition{
		ID:           a.ID,
		SourceRole:   a.SourceRole,
		TargetRole:   a.TargetRole,
		Stratum:      a.Stratum,
		Context:      a.Context,
		Clauses:      clauses,
		Requirements: reqs,
	}, nil
}

// amendmentFromRecord re-hydrates a runner.AmendmentRequest from its
// persisted shape.
func amendmentFromRecord(a AmendmentRecord) (runner.AmendmentRequest, error) {
	var contexts []string
	if a.ContextsJSON != "" && a.ContextsJSON != "null" {
		if err := json.Unmarshal([]byte(a.ContextsJSON), &contexts); err != nil {
			return runner.AmendmentRequest{}, fmt.Errorf("contexts json: %w", err)
		}
	}
	var findingIDs []string
	if a.FindingIDsJSON != "" && a.FindingIDsJSON != "null" {
		if err := json.Unmarshal([]byte(a.FindingIDsJSON), &findingIDs); err != nil {
			return runner.AmendmentRequest{}, fmt.Errorf("finding-ids json: %w", err)
		}
	}
	return runner.AmendmentRequest{
		ID:          a.ID,
		Reason:      runner.AmendmentReason(a.Reason),
		SourceArrow: a.SourceArrow,
		TargetRole:  a.TargetRole,
		Contexts:    contexts,
		Description: a.Description,
		FindingIDs:  findingIDs,
		CreatedAt:   a.CreatedAt,
	}, nil
}
