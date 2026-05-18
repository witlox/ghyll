package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Validation pattern (validation-pass-9 E2): every write boundary
// validates the record so a caller bypassing the runner's
// FindingsStore.Raise can't persist corruption. Mirrors the
// runner's own validation policy (validation-pass-4 F9, F22, F23).

// Writer-validation errors.
var (
	ErrEngineInvalidSeverity = errors.New("engine: severity out of 0..4")
	ErrEngineInvalidType     = errors.New("engine: type does not match [a-z][a-z0-9-]*")
	ErrEngineInvalidStatus   = errors.New("engine: status not in known enum")
	ErrEngineEmptyID         = errors.New("engine: id required")
	ErrEngineInvalidJSON     = errors.New("engine: invalid JSON blob")
)

// findingTypePattern mirrors runner/findings.go.
var findingTypePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// knownFindingStatus is the canonical set of FindingStatus wire
// values. Matches runner/findings.go's FindingStatus.String().
var knownFindingStatus = map[string]struct{}{
	"open":          {},
	"running":       {},
	"resolved":      {},
	"accepted-risk": {},
	"unevaluated":   {},
}

// safeID truncates and strips control bytes from an operator-
// supplied identifier before it lands in an error message
// (validation-pass-9 E5).
func safeID(id string) string {
	const max = 64
	if len(id) > max {
		id = id[:max] + "…"
	}
	var b strings.Builder
	b.Grow(len(id))
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			b.WriteRune('?')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// validJSON returns true if s is empty or valid JSON.
func validJSON(s string) bool {
	if s == "" {
		return true
	}
	return json.Valid([]byte(s))
}

// FindingRecord is the persistence shape of a runner.FindingRecord.
// Mirrors the runner type but with explicit columnar fields so
// queries can filter without unmarshaling.
//
// Uint64 fields ship as JSON strings (V6) so JavaScript clients
// don't lose precision past 2^53.
type FindingRecord struct {
	ID              string `json:"id"`
	ArrowID         string `json:"arrow_id"`
	Type            string `json:"type"`
	Severity        int    `json:"severity"`
	Status          string `json:"status"`
	Description     string `json:"description"`
	RaisedAt        string `json:"raised_at"`
	RaisedByRole    string `json:"raised_by_role"`
	TransitionCount int    `json:"transition_count"`
	GridVersion     uint64 `json:"grid_version,string"`
	StoreVersion    uint64 `json:"store_version,string"`
	UpdatedAt       string `json:"updated_at"`
}

// UpsertFinding inserts or updates a finding. Validation-pass-9
// E2: severity/status/type/ID validated at writer boundary so a
// caller bypassing the runner can't persist corruption. E3:
// newer-wins on conflict via `WHERE excluded.store_version >
// findings.store_version`.
func (s *Store) UpsertFinding(ctx context.Context, f FindingRecord) error {
	if strings.TrimSpace(f.ID) == "" {
		return ErrEngineEmptyID
	}
	if f.Severity < 0 || f.Severity > 4 {
		return fmt.Errorf("%w: %s = %d", ErrEngineInvalidSeverity, safeID(f.ID), f.Severity)
	}
	if !findingTypePattern.MatchString(f.Type) {
		return fmt.Errorf("%w: %s type=%q", ErrEngineInvalidType, safeID(f.ID), safeID(f.Type))
	}
	if _, ok := knownFindingStatus[f.Status]; !ok {
		return fmt.Errorf("%w: %s status=%q", ErrEngineInvalidStatus, safeID(f.ID), safeID(f.Status))
	}
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
		WHERE excluded.store_version >= findings.store_version
	`,
		f.ID, f.ArrowID, f.Type, f.Severity, f.Status,
		f.Description, f.RaisedAt, f.RaisedByRole,
		f.TransitionCount, int64(f.GridVersion), int64(f.StoreVersion), f.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("UpsertFinding %s: %w", safeID(f.ID), err)
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
	FindingID    string `json:"finding_id"`
	FromStatus   string `json:"from_status"`
	ToStatus     string `json:"to_status"`
	Role         string `json:"role"`
	Reason       string `json:"reason"`
	StoreVersion uint64 `json:"store_version,string"`
	At           string `json:"at"`
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
	ArrowID      string `json:"arrow_id"`
	ReqID        string `json:"req_id"`
	MinDepth     int    `json:"min_depth"`
	Description  string `json:"description"`
	StoreVersion uint64 `json:"store_version,string"`
	DeclaredAt   string `json:"declared_at"`
}

func (s *Store) UpsertRequirement(ctx context.Context, r RequirementRecord) error {
	if strings.TrimSpace(r.ArrowID) == "" || strings.TrimSpace(r.ReqID) == "" {
		return ErrEngineEmptyID
	}
	if r.MinDepth < 0 || r.MinDepth > 3 {
		return fmt.Errorf("engine: requirement %s/%s min_depth=%d out of 0..3",
			safeID(r.ArrowID), safeID(r.ReqID), r.MinDepth)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO requirements (
			arrow_id, req_id, min_depth, description, store_version, declared_at
		) VALUES (?,?,?,?,?,?)
		ON CONFLICT(arrow_id, req_id) DO UPDATE SET
			min_depth     = excluded.min_depth,
			description   = excluded.description,
			store_version = excluded.store_version,
			declared_at   = excluded.declared_at
		WHERE excluded.store_version >= requirements.store_version
	`, r.ArrowID, r.ReqID, r.MinDepth, r.Description, int64(r.StoreVersion), r.DeclaredAt)
	if err != nil {
		return fmt.Errorf("UpsertRequirement %s/%s: %w", safeID(r.ArrowID), safeID(r.ReqID), err)
	}
	return nil
}

// ClassificationRecord is the persistence shape of a runner.Classification.
type ClassificationRecord struct {
	ArrowID        string `json:"arrow_id"`
	ReqID          string `json:"req_id"`
	Observed       int    `json:"observed"`
	Evidence       string `json:"evidence"`
	OverwriteCount int    `json:"overwrite_count"`
	StoreVersion   uint64 `json:"store_version,string"`
	ClassifiedAt   string `json:"classified_at"`
}

func (s *Store) UpsertClassification(ctx context.Context, c ClassificationRecord) error {
	if strings.TrimSpace(c.ArrowID) == "" || strings.TrimSpace(c.ReqID) == "" {
		return ErrEngineEmptyID
	}
	if c.Observed < 0 || c.Observed > 3 {
		return fmt.Errorf("engine: classification %s/%s observed=%d out of 0..3",
			safeID(c.ArrowID), safeID(c.ReqID), c.Observed)
	}
	// Validation-pass-9 J14: increment overwrite_count on conflict
	// so the column matches the per-overwrite audit table.
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO classifications (
			arrow_id, req_id, observed, evidence,
			overwrite_count, store_version, classified_at
		) VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(arrow_id, req_id) DO UPDATE SET
			observed        = excluded.observed,
			evidence        = excluded.evidence,
			overwrite_count = classifications.overwrite_count + 1,
			store_version   = excluded.store_version,
			classified_at   = excluded.classified_at
		WHERE excluded.store_version >= classifications.store_version
	`, c.ArrowID, c.ReqID, c.Observed, c.Evidence,
		c.OverwriteCount, int64(c.StoreVersion), c.ClassifiedAt)
	if err != nil {
		return fmt.Errorf("UpsertClassification %s/%s: %w", safeID(c.ArrowID), safeID(c.ReqID), err)
	}
	return nil
}

// OverwriteRecord is one entry in classification_overwrites.
type OverwriteRecord struct {
	ArrowID        string `json:"arrow_id"`
	ReqID          string `json:"req_id"`
	BeforeObserved int    `json:"before_observed"`
	AfterObserved  int    `json:"after_observed"`
	StoreVersion   uint64 `json:"store_version,string"`
	At             string `json:"at"`
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
	ID               string `json:"id"`
	GridVersion      uint64 `json:"grid_version,string"`
	SourceRole       string `json:"source_role"`
	TargetRole       string `json:"target_role"`
	Stratum          string `json:"stratum"`
	Context          string `json:"context"`
	ClausesJSON      string `json:"clauses_json"`
	RequirementsJSON string `json:"requirements_json"`
	Kind             string `json:"kind"`
	DeclaredAt       string `json:"declared_at"`
}

// InsertGridArrow records a new arrow at a specific grid version.
// Pair (id, grid_version) is the primary key — re-appending the
// same arrow at the same version is rejected. JSON blob validated
// at write boundary (E4).
func (s *Store) InsertGridArrow(ctx context.Context, a GridArrowRecord) error {
	if strings.TrimSpace(a.ID) == "" {
		return ErrEngineEmptyID
	}
	if !validJSON(a.ClausesJSON) {
		return fmt.Errorf("%w: arrow %s clauses_json", ErrEngineInvalidJSON, safeID(a.ID))
	}
	if !validJSON(a.RequirementsJSON) {
		return fmt.Errorf("%w: arrow %s requirements_json", ErrEngineInvalidJSON, safeID(a.ID))
	}
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
//
// DrainedAt is sql.NullString for sqlite NULL handling, but
// MarshalJSON renders it as either a plain RFC3339Nano string or
// JSON null — clients see `"drained_at": "2026-…"` or
// `"drained_at": null`, never the default Go `{"String":"","Valid":false}`
// shape (validation-pass-9 V1).
type AmendmentRecord struct {
	ID             string         `json:"id"`
	Reason         string         `json:"reason"`
	SourceArrow    string         `json:"source_arrow"`
	TargetRole     string         `json:"target_role"`
	ContextsJSON   string         `json:"contexts_json"`
	Description    string         `json:"description"`
	FindingIDsJSON string         `json:"finding_ids_json"`
	CreatedAt      string         `json:"created_at"`
	DrainedAt      sql.NullString `json:"drained_at"`
}

// MarshalJSON renders AmendmentRecord with drained_at as plain
// string or null — see struct docstring.
func (a AmendmentRecord) MarshalJSON() ([]byte, error) {
	type alias AmendmentRecord
	view := struct {
		alias
		DrainedAt any `json:"drained_at"`
	}{alias: alias(a)}
	if a.DrainedAt.Valid {
		view.DrainedAt = a.DrainedAt.String
	} else {
		view.DrainedAt = nil
	}
	return json.Marshal(view)
}

// UpsertAmendment inserts or updates an amendment row. Drain sets
// drained_at; pre-drain Enqueue sets it nil. JSON blobs validated
// at write boundary (E4).
func (s *Store) UpsertAmendment(ctx context.Context, a AmendmentRecord) error {
	if strings.TrimSpace(a.ID) == "" {
		return ErrEngineEmptyID
	}
	if !validJSON(a.ContextsJSON) {
		return fmt.Errorf("%w: amendment %s contexts_json", ErrEngineInvalidJSON, safeID(a.ID))
	}
	if !validJSON(a.FindingIDsJSON) {
		return fmt.Errorf("%w: amendment %s finding_ids_json", ErrEngineInvalidJSON, safeID(a.ID))
	}
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
	ID                      string `json:"id"`
	ClauseID                string `json:"clause_id"`
	PassID                  string `json:"pass_id"`
	ArrowID                 string `json:"arrow_id"`
	GridVersion             uint64 `json:"grid_version,string"`
	DepthTypeAttestationRef string `json:"depth_type_attestation_ref"`
	ActualTier              int    `json:"actual_tier"`
	MinDepthTier            int    `json:"min_depth_tier"`
	EvaluatorConcept        string `json:"evaluator_concept"`
	EvaluatorGeneration     int64  `json:"evaluator_generation"`
	StartedAt               string `json:"started_at"`
	CompletedAt             string `json:"completed_at"`
	StartStatus             string `json:"start_status"`
	EndStatus               string `json:"end_status"`
	ResultJSON              string `json:"result_json"`
	RunError                string `json:"run_error"`
}

// InsertEvaluationRun appends a run record. EvaluationRun is
// snapshot-by-design (runner.go); persistence is one-shot. JSON
// blob validated at write boundary (E4).
func (s *Store) InsertEvaluationRun(ctx context.Context, r EvaluationRunRecord) error {
	if strings.TrimSpace(r.ID) == "" {
		return ErrEngineEmptyID
	}
	if !validJSON(r.ResultJSON) {
		return fmt.Errorf("%w: run %s result_json", ErrEngineInvalidJSON, safeID(r.ID))
	}
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

// MustJSON marshals v to JSON or returns fallback on error. Per
// validation-pass-9 J12: callers MUST choose `"[]"` for slice
// types and `"{}"` for map/object types so the replay path's
// unmarshal into the correct Go type doesn't fail.
//
// The marshal-error case is a programmer-supplied non-marshalable
// type (channels, functions inside map[string]any). Logging is
// the caller's responsibility — MustJSON can't reach a logger
// without an injected dependency.
func MustJSON(v any, fallback string) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fallback
	}
	return string(b)
}

// JSONSlice marshals v as a JSON array, falling back to "[]" on
// error. Convenience wrapper for the most common journal use.
func JSONSlice(v any) string { return MustJSON(v, "[]") }

// JSONObject marshals v as a JSON object, falling back to "{}" on
// error. Convenience wrapper.
func JSONObject(v any) string { return MustJSON(v, "{}") }

// Compile-time check that errors are wrapped via standard package.
var _ = errors.New
