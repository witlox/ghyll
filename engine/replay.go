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
// runner-layer mutators.
//
// CRITICAL ordering invariant: the caller MUST replay BEFORE
// attaching the Journal. Otherwise the replayed mutations write
// back to the same sqlite rows they came from — recursive journaling.

// ReplayCounts reports how many entities replay loaded per category.
type ReplayCounts struct {
	Findings          int
	Requirements      int
	Classifications   int
	Arrows            int
	AmendmentsActive  int
	AmendmentsDrained int

	// Errors collects per-row failures so a single corrupt row
	// doesn't stop the whole replay (J9). Empty when clean.
	Errors []string
}

// ErrReplayCachesNotEmpty is returned if the caller tries to replay
// into pre-populated caches.
var ErrReplayCachesNotEmpty = errors.New("replay: target caches not empty")

// ReplayTargets bundles the runner-layer caches the replay populates.
type ReplayTargets struct {
	Findings        *runner.FindingsStore
	Classifications *runner.ClassificationsStore
	Grid            *runner.Grid
	Amendments      *runner.AmendmentQueue
}

// replayPageSize is the per-batch read size for paging through
// large persisted tables (J1). Tunable; the bottleneck is the
// runner-side Raise/Append cost, not sqlite read cost.
const replayPageSize = 1000

// Replay loads every persisted v2 entity from store into targets.
//
// Per validation-pass-9:
//   - J1: paging loop instead of single-shot 1M-limit query.
//   - J9: per-row errors accumulate into ReplayCounts.Errors;
//     replay continues past malformed rows so amendments and
//     other categories aren't blocked by a corrupt finding.
//   - J10: findings ordered by raised_at ASC so the in-memory
//     slice order post-replay matches raise sequence.
func Replay(ctx context.Context, store *Store, targets ReplayTargets) (ReplayCounts, error) {
	var c ReplayCounts
	if store == nil {
		return c, errors.New("replay: nil store")
	}
	if err := ensureEmpty(targets); err != nil {
		return c, err
	}

	// 1. Grid arrows (latest version per ID).
	arrows, err := store.allLatestArrows(ctx)
	if err != nil {
		return c, fmt.Errorf("replay arrows: %w", err)
	}
	for _, a := range arrows {
		def, err := arrowFromRecord(a)
		if err != nil {
			c.Errors = append(c.Errors, fmt.Sprintf("arrow %s/v%d: %v", safeID(a.ID), a.GridVersion, err))
			continue
		}
		var appendErr error
		if a.Kind == "on-the-spot" {
			_, appendErr = targets.Grid.AppendOnTheSpot(def)
		} else {
			_, appendErr = targets.Grid.Append(def)
		}
		if appendErr != nil {
			c.Errors = append(c.Errors, fmt.Sprintf("arrow %s: %v", safeID(a.ID), appendErr))
			continue
		}
		c.Arrows++
	}

	// 2. Requirements before classifications.
	if err := store.iterRequirements(ctx, func(r RequirementRecord) {
		if err := targets.Classifications.DeclareRequirement(r.ArrowID, runner.Requirement{
			ID:          r.ReqID,
			MinDepth:    runner.DepthRank(r.MinDepth),
			Description: r.Description,
		}); err != nil {
			c.Errors = append(c.Errors, fmt.Sprintf("requirement %s/%s: %v",
				safeID(r.ArrowID), safeID(r.ReqID), err))
			return
		}
		c.Requirements++
	}); err != nil {
		return c, fmt.Errorf("replay requirements: %w", err)
	}

	if err := store.iterClassifications(ctx, func(cl ClassificationRecord) {
		if err := targets.Classifications.RecordClassification(cl.ArrowID, runner.Classification{
			RequirementID: cl.ReqID,
			Observed:      runner.DepthRank(cl.Observed),
			Evidence:      cl.Evidence,
		}); err != nil {
			c.Errors = append(c.Errors, fmt.Sprintf("classification %s/%s: %v",
				safeID(cl.ArrowID), safeID(cl.ReqID), err))
			return
		}
		c.Classifications++
	}); err != nil {
		return c, fmt.Errorf("replay classifications: %w", err)
	}

	// 3. Findings, ordered by raised_at so the in-memory slice
	// order matches raise sequence (J10).
	if err := store.iterFindingsByRaiseTime(ctx, func(f FindingRecord) {
		status, err := runner.ParseFindingStatus(f.Status)
		if err != nil {
			c.Errors = append(c.Errors, fmt.Sprintf("finding %s status: %v", safeID(f.ID), err))
			return
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
			c.Errors = append(c.Errors, fmt.Sprintf("finding %s raise: %v", safeID(f.ID), err))
			return
		}
		c.Findings++
	}); err != nil {
		return c, fmt.Errorf("replay findings: %w", err)
	}

	// 4. Amendments — single query, classify in-process so the
	// pending/drained split is consistent (J13).
	if err := store.iterAmendments(ctx, func(a AmendmentRecord) {
		req, err := amendmentFromRecord(a)
		if err != nil {
			c.Errors = append(c.Errors, fmt.Sprintf("amendment %s: %v", safeID(a.ID), err))
			return
		}
		if a.DrainedAt.Valid {
			targets.Amendments.LoadDrained(req.ID)
			c.AmendmentsDrained++
			return
		}
		if err := targets.Amendments.Enqueue(req); err != nil {
			c.Errors = append(c.Errors, fmt.Sprintf("enqueue %s: %v", safeID(a.ID), err))
			return
		}
		c.AmendmentsActive++
	}); err != nil {
		return c, fmt.Errorf("replay amendments: %w", err)
	}

	return c, nil
}

// ensureEmpty checks that the replay targets are freshly constructed.
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
// arrow ID.
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

// iterRequirements pages through every requirement row and calls
// fn per row. Bypasses normalizePaging's 1000 cap (J1).
func (s *Store) iterRequirements(ctx context.Context, fn func(RequirementRecord)) error {
	offset := 0
	for {
		// #nosec G202 -- closed in-package query string.
		rows, err := s.db.QueryContext(ctx, `
			SELECT arrow_id, req_id, min_depth, description, store_version, declared_at
			FROM requirements
			ORDER BY arrow_id ASC, req_id ASC
			LIMIT ? OFFSET ?
		`, replayPageSize, offset)
		if err != nil {
			return err
		}
		count := 0
		for rows.Next() {
			var r RequirementRecord
			var sv int64
			if err := rows.Scan(&r.ArrowID, &r.ReqID, &r.MinDepth,
				&r.Description, &sv, &r.DeclaredAt); err != nil {
				_ = rows.Close()
				return err
			}
			r.StoreVersion = uint64(sv)
			fn(r)
			count++
		}
		_ = rows.Close()
		if count < replayPageSize {
			return nil
		}
		offset += replayPageSize
	}
}

// iterClassifications pages through classifications.
func (s *Store) iterClassifications(ctx context.Context, fn func(ClassificationRecord)) error {
	offset := 0
	for {
		rows, err := s.db.QueryContext(ctx, `
			SELECT arrow_id, req_id, observed, evidence,
			       overwrite_count, store_version, classified_at
			FROM classifications
			ORDER BY arrow_id ASC, req_id ASC
			LIMIT ? OFFSET ?
		`, replayPageSize, offset)
		if err != nil {
			return err
		}
		count := 0
		for rows.Next() {
			var cl ClassificationRecord
			var sv int64
			if err := rows.Scan(&cl.ArrowID, &cl.ReqID, &cl.Observed,
				&cl.Evidence, &cl.OverwriteCount, &sv, &cl.ClassifiedAt); err != nil {
				_ = rows.Close()
				return err
			}
			cl.StoreVersion = uint64(sv)
			fn(cl)
			count++
		}
		_ = rows.Close()
		if count < replayPageSize {
			return nil
		}
		offset += replayPageSize
	}
}

// iterFindingsByRaiseTime pages through findings ordered by
// raised_at so the in-memory slice order post-replay matches raise
// sequence (J10).
func (s *Store) iterFindingsByRaiseTime(ctx context.Context, fn func(FindingRecord)) error {
	offset := 0
	for {
		rows, err := s.db.QueryContext(ctx, `
			SELECT id, arrow_id, type, severity, status,
			       description, raised_at, raised_by_role,
			       transition_count, grid_version, store_version, updated_at
			FROM findings
			ORDER BY raised_at ASC, id ASC
			LIMIT ? OFFSET ?
		`, replayPageSize, offset)
		if err != nil {
			return err
		}
		count := 0
		for rows.Next() {
			f, err := scanFinding(rows)
			if err != nil {
				_ = rows.Close()
				return err
			}
			fn(f)
			count++
		}
		_ = rows.Close()
		if count < replayPageSize {
			return nil
		}
		offset += replayPageSize
	}
}

// iterAmendments pages through amendments, returning each row
// regardless of drained state. Caller classifies via DrainedAt.Valid.
func (s *Store) iterAmendments(ctx context.Context, fn func(AmendmentRecord)) error {
	offset := 0
	for {
		rows, err := s.db.QueryContext(ctx, `
			SELECT id, reason, source_arrow, target_role,
			       contexts_json, description, finding_ids_json,
			       created_at, drained_at
			FROM amendments
			ORDER BY created_at ASC, id ASC
			LIMIT ? OFFSET ?
		`, replayPageSize, offset)
		if err != nil {
			return err
		}
		count := 0
		for rows.Next() {
			var a AmendmentRecord
			if err := rows.Scan(&a.ID, &a.Reason, &a.SourceArrow,
				&a.TargetRole, &a.ContextsJSON, &a.Description,
				&a.FindingIDsJSON, &a.CreatedAt, &a.DrainedAt); err != nil {
				_ = rows.Close()
				return err
			}
			fn(a)
			count++
		}
		_ = rows.Close()
		if count < replayPageSize {
			return nil
		}
		offset += replayPageSize
	}
}

// arrowFromRecord re-hydrates a runner.ArrowDefinition.
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

// amendmentFromRecord re-hydrates a runner.AmendmentRequest.
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
