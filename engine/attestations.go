package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/witlox/ghyll/runner"
)

// Engine-side persistence for runner.AttestationRecord per ADR-010.
//
// One row per attestation. Records are immutable; UPSERT exists
// only to make replay idempotent against the same ID arriving
// twice (e.g., journal redelivery after crash mid-write).

// ErrAttestationConflict is returned by insertAttestation when a
// row with the same attestation_id already exists with different
// content. Records are immutable; this enforces immutability at
// the persistence boundary so a buggy or out-of-band caller cannot
// silently overwrite a recorded verdict.
var ErrAttestationConflict = errors.New("engine: attestation conflict")

// insertAttestation writes one record. The CHECK constraints in the
// schema enforce kind/clause_id/verdict consistency; the application-
// layer §12.2 validation lives in runner.AttestationStore.Record.
//
// Immutability is enforced at this layer too: INSERT OR IGNORE
// followed by a content-equality probe. Identical re-insert (same
// ID + same content) succeeds silently (idempotent for journal
// redelivery). Re-insert with conflicting content returns
// ErrAttestationConflict.
func (s *Store) insertAttestation(ctx context.Context, rec runner.AttestationRecord) error {
	var clauseID any
	if rec.ClauseID == "" {
		clauseID = nil
	} else {
		clauseID = rec.ClauseID
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO attestations (
			attestation_id, kind, arrow_id, clause_id, op_id,
			attested_by_role, source_role, target_role, verdict,
			reason, timestamp, grid_version
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		rec.ID, string(rec.Kind), rec.ArrowID, clauseID, rec.OpID,
		rec.AttestedByRole, rec.SourceRole, rec.TargetRole, string(rec.Verdict),
		rec.Reason, rec.Timestamp, rec.GridVersion,
	)
	if err != nil {
		return fmt.Errorf("attestation insert %s: %w", rec.ID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("attestation insert %s rows-affected: %w", rec.ID, err)
	}
	if affected == 1 {
		return nil
	}
	// Row exists; verify content equality.
	existing, err := s.readAttestation(ctx, rec.ID)
	if err != nil {
		return fmt.Errorf("attestation conflict probe %s: %w", rec.ID, err)
	}
	if existing == rec {
		return nil // idempotent re-insert with identical content
	}
	return fmt.Errorf("%w: id=%s", ErrAttestationConflict, rec.ID)
}

// readAttestation fetches a single attestation by ID. Used by
// insertAttestation's conflict probe.
func (s *Store) readAttestation(ctx context.Context, id string) (runner.AttestationRecord, error) {
	var rec runner.AttestationRecord
	var kind, verdict string
	var clauseID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT attestation_id, kind, arrow_id, clause_id, op_id,
		       attested_by_role, source_role, target_role, verdict,
		       reason, timestamp, grid_version
		FROM attestations WHERE attestation_id = ?
	`, id).Scan(
		&rec.ID, &kind, &rec.ArrowID, &clauseID, &rec.OpID,
		&rec.AttestedByRole, &rec.SourceRole, &rec.TargetRole, &verdict,
		&rec.Reason, &rec.Timestamp, &rec.GridVersion,
	)
	if err != nil {
		return runner.AttestationRecord{}, err
	}
	rec.Kind = runner.AttestationKind(kind)
	rec.Verdict = runner.AttestationVerdict(verdict)
	if clauseID.Valid {
		rec.ClauseID = clauseID.String
	}
	return rec, nil
}

// listAttestations returns every persisted attestation, ordered by
// timestamp ASC then ID ASC for deterministic replay. Used by
// engine.Replay to populate runner.AttestationStore.
func (s *Store) listAttestations(ctx context.Context) ([]runner.AttestationRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT attestation_id, kind, arrow_id, clause_id, op_id,
		       attested_by_role, source_role, target_role, verdict,
		       reason, timestamp, grid_version
		FROM attestations
		ORDER BY timestamp ASC, attestation_id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("attestations query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []runner.AttestationRecord
	for rows.Next() {
		var rec runner.AttestationRecord
		var kind, verdict string
		var clauseID sql.NullString
		if err := rows.Scan(
			&rec.ID, &kind, &rec.ArrowID, &clauseID, &rec.OpID,
			&rec.AttestedByRole, &rec.SourceRole, &rec.TargetRole, &verdict,
			&rec.Reason, &rec.Timestamp, &rec.GridVersion,
		); err != nil {
			return nil, fmt.Errorf("attestation scan: %w", err)
		}
		rec.Kind = runner.AttestationKind(kind)
		rec.Verdict = runner.AttestationVerdict(verdict)
		if clauseID.Valid {
			rec.ClauseID = clauseID.String
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("attestations rows: %w", err)
	}
	return out, nil
}

// CountAttestations returns the total persisted attestation count,
// used by `ghyll engine status`.
//
// Tolerates a missing attestations table (a v1 schema DB) by
// returning (0, nil). The engine status CLI then reports
// "attestations: 0" — consistent with the user-visible view that
// no attestations exist on a pre-v2 schema. Upgrade is automatic
// the next time a v2 binary opens the store via OpenStore (NOT
// OpenStoreReadOnly), which runs the schema DDL.
// CatchUpAttestations writes every record from the in-memory
// AttestationStore into the engine `attestations` table. Per
// ADR-015 Part C the JSONL is the source of truth; this function
// keeps the derived engine cache in sync at session start AFTER
// AttestationStore.LoadFromJSONL has populated the in-memory
// state. Called by session.Open between Load and Recovery so
// Recovery's JOIN-based attestation-pending detection sees the
// authoritative state.
//
// H-3 (G2-F-7) hardening:
//   - The full catch-up runs inside ONE BeginTx. Partial failures
//     roll back so the engine and in-memory state stay consistent.
//   - The record list is sorted by ID for deterministic retry
//     behavior.
//   - On a content-conflict between an existing engine row and a
//     JSONL record (operator hand-edited the file), the JSONL
//     record WINS via UPDATE — per ADR-015 Part C JSONL is the
//     source of truth. A conflict no longer refuses session
//     start.
//
// Returns the count of rows written/updated and any per-row
// override events (caller surfaces these via RecoveryReport
// or the bus). The error return is non-nil only for transactional
// failures (BeginTx, Commit, sqlite errors); per-row conflicts
// are resolved silently with override + event.
func (s *Store) CatchUpAttestations(ctx context.Context, src *runner.AttestationStore) (int, []runner.OperatorEvent, error) {
	if s == nil || src == nil {
		return 0, nil, nil
	}
	recs := src.All()
	sort.Slice(recs, func(i, j int) bool { return recs[i].ID < recs[j].ID })

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("catch-up BeginTx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		count  int
		events []runner.OperatorEvent
	)
	for _, rec := range recs {
		written, override, err := s.upsertAttestationInTx(ctx, tx, rec)
		if err != nil {
			return count, events, fmt.Errorf("catch-up attestation %s: %w", rec.ID, err)
		}
		if written {
			count++
		}
		if override {
			events = append(events, runner.OperatorEvent{
				Kind:    runner.OpEventAttestationAuditDurabilityFailed,
				ArrowID: rec.ArrowID,
				Detail:  "engine row overridden by JSONL: " + rec.ID,
			})
		}
	}
	if err := tx.Commit(); err != nil {
		return count, events, fmt.Errorf("catch-up Commit: %w", err)
	}
	return count, events, nil
}

// upsertAttestationInTx writes a record inside an existing tx.
// On INSERT conflict where the existing row's content disagrees
// with rec, UPDATEs the row to match rec (JSONL wins) and returns
// override=true so the caller can emit an audit event.
func (s *Store) upsertAttestationInTx(
	ctx context.Context, tx *sql.Tx, rec runner.AttestationRecord,
) (written bool, override bool, err error) {
	var clauseID any
	if rec.ClauseID == "" {
		clauseID = nil
	} else {
		clauseID = rec.ClauseID
	}
	res, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO attestations (
			attestation_id, kind, arrow_id, clause_id, op_id,
			attested_by_role, source_role, target_role, verdict,
			reason, timestamp, grid_version
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		rec.ID, string(rec.Kind), rec.ArrowID, clauseID, rec.OpID,
		rec.AttestedByRole, rec.SourceRole, rec.TargetRole, string(rec.Verdict),
		rec.Reason, rec.Timestamp, rec.GridVersion,
	)
	if err != nil {
		return false, false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, false, err
	}
	if affected == 1 {
		return true, false, nil
	}
	// Row exists; probe equality.
	existing, err := s.readAttestationInTx(ctx, tx, rec.ID)
	if err != nil {
		return false, false, err
	}
	if existing == rec {
		return false, false, nil // idempotent — same content
	}
	// JSONL wins on conflict (H-3 / G2-F-7).
	_, err = tx.ExecContext(ctx, `
		UPDATE attestations
		SET    kind             = ?,
		       arrow_id         = ?,
		       clause_id        = ?,
		       op_id            = ?,
		       attested_by_role = ?,
		       source_role      = ?,
		       target_role      = ?,
		       verdict          = ?,
		       reason           = ?,
		       timestamp        = ?,
		       grid_version     = ?
		WHERE  attestation_id   = ?
	`,
		string(rec.Kind), rec.ArrowID, clauseID, rec.OpID,
		rec.AttestedByRole, rec.SourceRole, rec.TargetRole, string(rec.Verdict),
		rec.Reason, rec.Timestamp, rec.GridVersion,
		rec.ID,
	)
	if err != nil {
		return false, false, err
	}
	return true, true, nil
}

// readAttestationInTx is the tx-bound variant of readAttestation.
func (s *Store) readAttestationInTx(ctx context.Context, tx *sql.Tx, id string) (runner.AttestationRecord, error) {
	var rec runner.AttestationRecord
	var kind, verdict string
	var clauseID sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT attestation_id, kind, arrow_id, clause_id, op_id,
		       attested_by_role, source_role, target_role, verdict,
		       reason, timestamp, grid_version
		FROM   attestations
		WHERE  attestation_id = ?
	`, id).Scan(
		&rec.ID, &kind, &rec.ArrowID, &clauseID, &rec.OpID,
		&rec.AttestedByRole, &rec.SourceRole, &rec.TargetRole, &verdict,
		&rec.Reason, &rec.Timestamp, &rec.GridVersion,
	)
	if err != nil {
		return runner.AttestationRecord{}, err
	}
	rec.Kind = runner.AttestationKind(kind)
	rec.Verdict = runner.AttestationVerdict(verdict)
	if clauseID.Valid {
		rec.ClauseID = clauseID.String
	}
	return rec, nil
}

func (s *Store) CountAttestations(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attestations`).Scan(&n)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		// sqlite returns "no such table: attestations" for v1
		// schemas. Surface as zero so status displays cleanly.
		if strings.Contains(err.Error(), "no such table") {
			return 0, nil
		}
		return 0, fmt.Errorf("count attestations: %w", err)
	}
	return n, nil
}
