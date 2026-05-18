package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// FindingFilter narrows a ListFindings query. Empty fields are
// ignored. Limit defaults to 100 when zero; cap 1000.
type FindingFilter struct {
	ArrowID     string
	Status      string
	MinSeverity int // -1 means no floor
	Type        string
	Limit       int
	Offset      int
}

// applyFindingFilter returns the WHERE clause + args for filter.
// The clause omits an empty WHERE — caller composes onto the base
// query.
func applyFindingFilter(f FindingFilter) (string, []any) {
	var clauses []string
	var args []any
	if f.ArrowID != "" {
		clauses = append(clauses, "arrow_id = ?")
		args = append(args, f.ArrowID)
	}
	if f.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, f.Status)
	}
	if f.MinSeverity >= 0 {
		clauses = append(clauses, "severity >= ?")
		args = append(args, f.MinSeverity)
	}
	if f.Type != "" {
		clauses = append(clauses, "type = ?")
		args = append(args, f.Type)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// normalizePaging clamps Limit / Offset to safe bounds.
func normalizePaging(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// ListFindings returns findings matching the filter, ordered by
// arrow_id ASC, severity DESC, id ASC (stable for paging).
func (s *Store) ListFindings(ctx context.Context, f FindingFilter) ([]FindingRecord, error) {
	limit, offset := normalizePaging(f.Limit, f.Offset)
	where, args := applyFindingFilter(f)
	// #nosec G202 -- `where` is built from a closed in-package set
	// of column-name fragments; never operator-supplied SQL.
	q := `
		SELECT id, arrow_id, type, severity, status,
		       description, raised_at, raised_by_role,
		       transition_count, grid_version, store_version, updated_at
		FROM findings` + where + `
		ORDER BY arrow_id ASC, severity DESC, id ASC
		LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("ListFindings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []FindingRecord
	for rows.Next() {
		f, err := scanFinding(rows)
		if err != nil {
			return nil, fmt.Errorf("ListFindings scan: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// GetFinding returns one finding by ID; (zero, false, nil) if absent.
func (s *Store) GetFinding(ctx context.Context, id string) (FindingRecord, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, arrow_id, type, severity, status,
		       description, raised_at, raised_by_role,
		       transition_count, grid_version, store_version, updated_at
		FROM findings WHERE id = ?
	`, id)
	f, err := scanFinding(row)
	if errors.Is(err, sql.ErrNoRows) {
		return FindingRecord{}, false, nil
	}
	if err != nil {
		return FindingRecord{}, false, fmt.Errorf("GetFinding %s: %w", id, err)
	}
	return f, true, nil
}

// ListTransitions returns the audit log for a finding, oldest first.
// Offset paging per V15.
func (s *Store) ListTransitions(ctx context.Context, findingID string, limit, offset int) ([]TransitionRecord, error) {
	limit, offset = normalizePaging(limit, offset)
	rows, err := s.db.QueryContext(ctx, `
		SELECT finding_id, from_status, to_status, role, reason, store_version, at
		FROM finding_transitions
		WHERE finding_id = ?
		ORDER BY seq ASC
		LIMIT ? OFFSET ?
	`, findingID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("ListTransitions %s: %w", findingID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []TransitionRecord
	for rows.Next() {
		var t TransitionRecord
		var sv int64
		if err := rows.Scan(&t.FindingID, &t.FromStatus, &t.ToStatus,
			&t.Role, &t.Reason, &sv, &t.At); err != nil {
			return nil, fmt.Errorf("ListTransitions scan: %w", err)
		}
		t.StoreVersion = uint64(sv)
		out = append(out, t)
	}
	return out, rows.Err()
}

// RequirementFilter narrows ListRequirements.
type RequirementFilter struct {
	ArrowID string
	Limit   int
	Offset  int
}

// ListRequirements returns requirements matching the filter.
func (s *Store) ListRequirements(ctx context.Context, f RequirementFilter) ([]RequirementRecord, error) {
	limit, offset := normalizePaging(f.Limit, f.Offset)
	var (
		where string
		args  []any
	)
	if f.ArrowID != "" {
		where = " WHERE arrow_id = ?"
		args = append(args, f.ArrowID)
	}
	args = append(args, limit, offset)
	// #nosec G202 -- closed in-package WHERE fragment.
	rows, err := s.db.QueryContext(ctx, `
		SELECT arrow_id, req_id, min_depth, description, store_version, declared_at
		FROM requirements`+where+`
		ORDER BY arrow_id ASC, req_id ASC
		LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("ListRequirements: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []RequirementRecord
	for rows.Next() {
		var r RequirementRecord
		var sv int64
		if err := rows.Scan(&r.ArrowID, &r.ReqID, &r.MinDepth,
			&r.Description, &sv, &r.DeclaredAt); err != nil {
			return nil, fmt.Errorf("ListRequirements scan: %w", err)
		}
		r.StoreVersion = uint64(sv)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ClassificationFilter narrows ListClassifications. Per V9 it is a
// distinct type from RequirementFilter so future per-endpoint
// extensions (e.g., MinObserved) don't accidentally apply to both.
type ClassificationFilter struct {
	ArrowID string
	Limit   int
	Offset  int
}

// ListClassifications returns classifications matching the filter.
func (s *Store) ListClassifications(ctx context.Context, f ClassificationFilter) ([]ClassificationRecord, error) {
	limit, offset := normalizePaging(f.Limit, f.Offset)
	var (
		where string
		args  []any
	)
	if f.ArrowID != "" {
		where = " WHERE arrow_id = ?"
		args = append(args, f.ArrowID)
	}
	args = append(args, limit, offset)
	// #nosec G202 -- closed in-package WHERE fragment.
	rows, err := s.db.QueryContext(ctx, `
		SELECT arrow_id, req_id, observed, evidence,
		       overwrite_count, store_version, classified_at
		FROM classifications`+where+`
		ORDER BY arrow_id ASC, req_id ASC
		LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("ListClassifications: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ClassificationRecord
	for rows.Next() {
		var c ClassificationRecord
		var sv int64
		if err := rows.Scan(&c.ArrowID, &c.ReqID, &c.Observed,
			&c.Evidence, &c.OverwriteCount, &sv, &c.ClassifiedAt); err != nil {
			return nil, fmt.Errorf("ListClassifications scan: %w", err)
		}
		c.StoreVersion = uint64(sv)
		out = append(out, c)
	}
	return out, rows.Err()
}

// ArrowFilter narrows ListArrows.
type ArrowFilter struct {
	Kind       string // "append" | "on-the-spot" | ""
	MinGridVer uint64
	Limit      int
	Offset     int
}

// ListArrows returns grid_arrow rows matching the filter, newest
// version first per arrow.
func (s *Store) ListArrows(ctx context.Context, f ArrowFilter) ([]GridArrowRecord, error) {
	limit, offset := normalizePaging(f.Limit, f.Offset)
	var (
		where []string
		args  []any
	)
	if f.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, f.Kind)
	}
	if f.MinGridVer > 0 {
		where = append(where, "grid_version >= ?")
		args = append(args, int64(f.MinGridVer))
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, grid_version, source_role, target_role,
		       stratum, context, clauses_json, requirements_json,
		       kind, declared_at
		FROM grid_arrows`+whereSQL+`
		ORDER BY id ASC, grid_version DESC
		LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("ListArrows: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []GridArrowRecord
	for rows.Next() {
		var a GridArrowRecord
		var gv int64
		if err := rows.Scan(&a.ID, &gv, &a.SourceRole, &a.TargetRole,
			&a.Stratum, &a.Context, &a.ClausesJSON, &a.RequirementsJSON,
			&a.Kind, &a.DeclaredAt); err != nil {
			return nil, fmt.Errorf("ListArrows scan: %w", err)
		}
		a.GridVersion = uint64(gv)
		out = append(out, a)
	}
	return out, rows.Err()
}

// AmendmentFilter narrows ListAmendments.
type AmendmentFilter struct {
	SourceArrow string
	Drained     *bool // nil = either; true = drained_at is set; false = pending
	Limit       int
	Offset      int
}

// ListAmendments returns amendments matching the filter, newest first.
func (s *Store) ListAmendments(ctx context.Context, f AmendmentFilter) ([]AmendmentRecord, error) {
	limit, offset := normalizePaging(f.Limit, f.Offset)
	var (
		where []string
		args  []any
	)
	if f.SourceArrow != "" {
		where = append(where, "source_arrow = ?")
		args = append(args, f.SourceArrow)
	}
	if f.Drained != nil {
		if *f.Drained {
			where = append(where, "drained_at IS NOT NULL")
		} else {
			where = append(where, "drained_at IS NULL")
		}
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, reason, source_arrow, target_role,
		       contexts_json, description, finding_ids_json,
		       created_at, drained_at
		FROM amendments`+whereSQL+`
		ORDER BY created_at DESC, id ASC
		LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("ListAmendments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AmendmentRecord
	for rows.Next() {
		var a AmendmentRecord
		if err := rows.Scan(&a.ID, &a.Reason, &a.SourceArrow,
			&a.TargetRole, &a.ContextsJSON, &a.Description,
			&a.FindingIDsJSON, &a.CreatedAt, &a.DrainedAt); err != nil {
			return nil, fmt.Errorf("ListAmendments scan: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// RunFilter narrows ListEvaluationRuns.
type RunFilter struct {
	ClauseID string
	PassID   string
	ArrowID  string
	Limit    int
	Offset   int
}

// ListEvaluationRuns returns evaluation_run rows matching filter,
// newest first.
func (s *Store) ListEvaluationRuns(ctx context.Context, f RunFilter) ([]EvaluationRunRecord, error) {
	limit, offset := normalizePaging(f.Limit, f.Offset)
	var (
		where []string
		args  []any
	)
	if f.ClauseID != "" {
		where = append(where, "clause_id = ?")
		args = append(args, f.ClauseID)
	}
	if f.PassID != "" {
		where = append(where, "pass_id = ?")
		args = append(args, f.PassID)
	}
	if f.ArrowID != "" {
		where = append(where, "arrow_id = ?")
		args = append(args, f.ArrowID)
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, clause_id, pass_id, arrow_id, grid_version,
		       depth_type_attestation_ref, actual_tier, min_depth_tier,
		       evaluator_concept, evaluator_generation,
		       started_at, completed_at, start_status, end_status,
		       result_json, run_error
		FROM evaluation_runs`+whereSQL+`
		ORDER BY completed_at DESC, id ASC
		LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("ListEvaluationRuns: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []EvaluationRunRecord
	for rows.Next() {
		var r EvaluationRunRecord
		var gv int64
		if err := rows.Scan(&r.ID, &r.ClauseID, &r.PassID, &r.ArrowID, &gv,
			&r.DepthTypeAttestationRef, &r.ActualTier, &r.MinDepthTier,
			&r.EvaluatorConcept, &r.EvaluatorGeneration,
			&r.StartedAt, &r.CompletedAt, &r.StartStatus, &r.EndStatus,
			&r.ResultJSON, &r.RunError); err != nil {
			return nil, fmt.Errorf("ListEvaluationRuns scan: %w", err)
		}
		r.GridVersion = uint64(gv)
		out = append(out, r)
	}
	return out, rows.Err()
}
