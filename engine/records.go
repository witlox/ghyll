package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// FindingRecord is the persistence shape of a runner.FindingRecord.
// Mirrors the runner type but with explicit columnar fields so
// queries can filter without unmarshaling.
type FindingRecord struct {
	ID              string
	ArrowID         string
	Type            string
	Severity        int
	Status          string
	Description     string
	RaisedAt        string
	RaisedByRole    string
	TransitionCount int
	GridVersion     uint64
	StoreVersion    uint64
	UpdatedAt       string
}

// UpsertFinding inserts or updates a finding. The runner's
// FindingsStore.Raise produces a new row; Transition updates
// status + transition_count.
func (s *Store) UpsertFinding(ctx context.Context, f FindingRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO findings (
			id, arrow_id, type, severity, status,
			description, raised_at, raised_by_role,
			transition_count, grid_version, store_version, updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			arrow_id          = excluded.arrow_id,
			type              = excluded.type,
			severity          = excluded.severity,
			status            = excluded.status,
			description       = excluded.description,
			raised_at         = excluded.raised_at,
			raised_by_role    = excluded.raised_by_role,
			transition_count  = excluded.transition_count,
			grid_version      = excluded.grid_version,
			store_version     = excluded.store_version,
			updated_at        = excluded.updated_at
	`,
		f.ID, f.ArrowID, f.Type, f.Severity, f.Status,
		f.Description, f.RaisedAt, f.RaisedByRole,
		f.TransitionCount, int64(f.GridVersion), int64(f.StoreVersion), f.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("UpsertFinding %s: %w", f.ID, err)
	}
	return nil
}

// DeleteFinding removes a finding (via runner.FindingsStore.Forget).
// Cascades to finding_transitions via the FK ON DELETE CASCADE.
func (s *Store) DeleteFinding(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM findings WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("DeleteFinding %s: %w", id, err)
	}
	return nil
}

// InsertTransition appends a transition audit-log entry.
type TransitionRecord struct {
	FindingID    string
	FromStatus   string
	ToStatus     string
	Role         string
	Reason       string
	StoreVersion uint64
	At           string
}

func (s *Store) InsertTransition(ctx context.Context, t TransitionRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO finding_transitions (
			finding_id, from_status, to_status, role, reason, store_version, at
		) VALUES (?,?,?,?,?,?,?)
	`, t.FindingID, t.FromStatus, t.ToStatus, t.Role, t.Reason, int64(t.StoreVersion), t.At)
	if err != nil {
		return fmt.Errorf("InsertTransition %s: %w", t.FindingID, err)
	}
	return nil
}

// RequirementRecord is the persistence shape of a runner.Requirement.
type RequirementRecord struct {
	ArrowID      string
	ReqID        string
	MinDepth     int
	Description  string
	StoreVersion uint64
	DeclaredAt   string
}

func (s *Store) UpsertRequirement(ctx context.Context, r RequirementRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO requirements (
			arrow_id, req_id, min_depth, description, store_version, declared_at
		) VALUES (?,?,?,?,?,?)
		ON CONFLICT(arrow_id, req_id) DO UPDATE SET
			min_depth     = excluded.min_depth,
			description   = excluded.description,
			store_version = excluded.store_version,
			declared_at   = excluded.declared_at
	`, r.ArrowID, r.ReqID, r.MinDepth, r.Description, int64(r.StoreVersion), r.DeclaredAt)
	if err != nil {
		return fmt.Errorf("UpsertRequirement %s/%s: %w", r.ArrowID, r.ReqID, err)
	}
	return nil
}

// ClassificationRecord is the persistence shape of a runner.Classification.
type ClassificationRecord struct {
	ArrowID        string
	ReqID          string
	Observed       int
	Evidence       string
	OverwriteCount int
	StoreVersion   uint64
	ClassifiedAt   string
}

func (s *Store) UpsertClassification(ctx context.Context, c ClassificationRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO classifications (
			arrow_id, req_id, observed, evidence,
			overwrite_count, store_version, classified_at
		) VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(arrow_id, req_id) DO UPDATE SET
			observed        = excluded.observed,
			evidence        = excluded.evidence,
			overwrite_count = excluded.overwrite_count,
			store_version   = excluded.store_version,
			classified_at   = excluded.classified_at
	`, c.ArrowID, c.ReqID, c.Observed, c.Evidence,
		c.OverwriteCount, int64(c.StoreVersion), c.ClassifiedAt)
	if err != nil {
		return fmt.Errorf("UpsertClassification %s/%s: %w", c.ArrowID, c.ReqID, err)
	}
	return nil
}

// OverwriteRecord is one entry in classification_overwrites.
type OverwriteRecord struct {
	ArrowID        string
	ReqID          string
	BeforeObserved int
	AfterObserved  int
	StoreVersion   uint64
	At             string
}

func (s *Store) InsertOverwrite(ctx context.Context, o OverwriteRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO classification_overwrites (
			arrow_id, req_id, before_observed, after_observed, store_version, at
		) VALUES (?,?,?,?,?,?)
	`, o.ArrowID, o.ReqID, o.BeforeObserved, o.AfterObserved, int64(o.StoreVersion), o.At)
	if err != nil {
		return fmt.Errorf("InsertOverwrite %s/%s: %w", o.ArrowID, o.ReqID, err)
	}
	return nil
}

// DeleteRequirement removes a (arrow_id, req_id) row from
// `requirements` AND its classification row + overwrite history.
func (s *Store) DeleteRequirement(ctx context.Context, arrowID, reqID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("DeleteRequirement begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`DELETE FROM requirements WHERE arrow_id = ? AND req_id = ?`,
		`DELETE FROM classifications WHERE arrow_id = ? AND req_id = ?`,
		`DELETE FROM classification_overwrites WHERE arrow_id = ? AND req_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, arrowID, reqID); err != nil {
			return fmt.Errorf("DeleteRequirement %s/%s: %w", arrowID, reqID, err)
		}
	}
	return tx.Commit()
}

// DeleteArrow removes everything for an arrow (requirements,
// classifications, overwrites). Called from
// ClassificationsStore.ForgetArrow.
func (s *Store) DeleteArrow(ctx context.Context, arrowID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("DeleteArrow begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`DELETE FROM requirements WHERE arrow_id = ?`,
		`DELETE FROM classifications WHERE arrow_id = ?`,
		`DELETE FROM classification_overwrites WHERE arrow_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, arrowID); err != nil {
			return fmt.Errorf("DeleteArrow %s: %w", arrowID, err)
		}
	}
	return tx.Commit()
}

// GridArrowRecord is the persistence shape of a runner.ArrowDefinition.
// Clauses and Requirements are JSON-encoded so the runtime structure
// roundtrips losslessly; structured fields are mirrored for query
// indexing.
type GridArrowRecord struct {
	ID               string
	GridVersion      uint64
	SourceRole       string
	TargetRole       string
	Stratum          string
	Context          string
	ClausesJSON      string
	RequirementsJSON string
	Kind             string // "append" or "on-the-spot"
	DeclaredAt       string
}

// InsertGridArrow records a new arrow at a specific grid version.
// Pair (id, grid_version) is the primary key — re-appending the
// same arrow at the same version is rejected.
func (s *Store) InsertGridArrow(ctx context.Context, a GridArrowRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO grid_arrows (
			id, grid_version, source_role, target_role,
			stratum, context, clauses_json, requirements_json,
			kind, declared_at
		) VALUES (?,?,?,?,?,?,?,?,?,?)
	`, a.ID, int64(a.GridVersion), a.SourceRole, a.TargetRole,
		a.Stratum, a.Context, a.ClausesJSON, a.RequirementsJSON,
		a.Kind, a.DeclaredAt)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return fmt.Errorf("%w: arrow %s at grid_version %d",
				ErrEngineDuplicate, a.ID, a.GridVersion)
		}
		return fmt.Errorf("InsertGridArrow %s: %w", a.ID, err)
	}
	return nil
}

// AmendmentRecord is the persistence shape of a
// runner.AmendmentRequest.
type AmendmentRecord struct {
	ID             string
	Reason         string
	SourceArrow    string
	TargetRole     string
	ContextsJSON   string
	Description    string
	FindingIDsJSON string
	CreatedAt      string
	DrainedAt      sql.NullString
}

// UpsertAmendment inserts or updates an amendment row. Drain sets
// drained_at; pre-drain Enqueue sets it nil.
func (s *Store) UpsertAmendment(ctx context.Context, a AmendmentRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO amendments (
			id, reason, source_arrow, target_role,
			contexts_json, description, finding_ids_json,
			created_at, drained_at
		) VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			drained_at = excluded.drained_at
	`, a.ID, a.Reason, a.SourceArrow, a.TargetRole,
		a.ContextsJSON, a.Description, a.FindingIDsJSON,
		a.CreatedAt, a.DrainedAt)
	if err != nil {
		return fmt.Errorf("UpsertAmendment %s: %w", a.ID, err)
	}
	return nil
}

// EvaluationRunRecord is the persistence shape of a runner.EvaluationRun.
type EvaluationRunRecord struct {
	ID                      string
	ClauseID                string
	PassID                  string
	ArrowID                 string
	GridVersion             uint64
	DepthTypeAttestationRef string
	ActualTier              int
	MinDepthTier            int
	EvaluatorConcept        string
	EvaluatorGeneration     int64
	StartedAt               string
	CompletedAt             string
	StartStatus             string
	EndStatus               string
	ResultJSON              string
	RunError                string
}

// InsertEvaluationRun appends a run record. EvaluationRun is
// snapshot-by-design (runner.go); persistence is one-shot.
func (s *Store) InsertEvaluationRun(ctx context.Context, r EvaluationRunRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO evaluation_runs (
			id, clause_id, pass_id, arrow_id, grid_version,
			depth_type_attestation_ref, actual_tier, min_depth_tier,
			evaluator_concept, evaluator_generation,
			started_at, completed_at, start_status, end_status,
			result_json, run_error
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO NOTHING
	`,
		r.ID, r.ClauseID, r.PassID, r.ArrowID, int64(r.GridVersion),
		r.DepthTypeAttestationRef, r.ActualTier, r.MinDepthTier,
		r.EvaluatorConcept, r.EvaluatorGeneration,
		r.StartedAt, r.CompletedAt, r.StartStatus, r.EndStatus,
		r.ResultJSON, r.RunError,
	)
	if err != nil {
		return fmt.Errorf("InsertEvaluationRun %s: %w", r.ID, err)
	}
	return nil
}

// scanFinding reads one row into a FindingRecord. Used by reader
// methods.
func scanFinding(scanner interface {
	Scan(...any) error
}) (FindingRecord, error) {
	var f FindingRecord
	var grid, store int64
	err := scanner.Scan(
		&f.ID, &f.ArrowID, &f.Type, &f.Severity, &f.Status,
		&f.Description, &f.RaisedAt, &f.RaisedByRole,
		&f.TransitionCount, &grid, &store, &f.UpdatedAt,
	)
	if err != nil {
		return FindingRecord{}, err
	}
	f.GridVersion = uint64(grid)
	f.StoreVersion = uint64(store)
	return f, nil
}

// MustJSON marshals v to JSON or returns "{}" on error. Used by
// the journaling layer where a marshal failure on an in-memory
// struct is a programmer error — we don't want to abort the
// mutation, but we do want to log + persist a safe fallback.
func MustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// Compile-time check that errors are wrapped via standard package.
var _ = errors.New
