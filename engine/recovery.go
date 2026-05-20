// Package engine — crash recovery component (ADR-015 Part D, Tier 1).
//
// Recovery runs once at session start, AFTER Replay returns and
// BEFORE the live Journal observers attach. It reconciles
// split-brain conditions left by a crashed previous run:
//
//   1. orphan scan: passes whose engine row is `state=open`.
//   2. attestation-pending scan: subset of orphans with an
//      unanswered depth-type attestation. These are preserved
//      (PassRegistry.Resume re-acquires the lock; recovered_at
//      stamped).
//   3. orphan abort: remaining orphans → `aborted:crash`.
//   4. evaluation_runs reconcile: clauses with `end_status=running`
//      AND a verdict in the JSONL attestation store get the new
//      end_status + `recovery_source` provenance.
//
// All four scans run inside ONE BeginTx/Commit (F-10) so a
// concurrent read-only CLI sees pre- or post-recovery atomically.
// Idempotent per F-12: passes with `recovered_at != ''` skip; the
// second invocation returns an empty RecoveryReport.
//
// Refuses to run on dirty replay (F-13): if
// ReplayCounts.Errors is non-empty, returns ErrRecoveryReplayDirty.

package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/witlox/ghyll/runner"
)

// RecoveryDeps bundles the runner stores + injected deps Recovery
// needs. Distinct from ReplayTargets so the Recovery signature
// carries its own surface (F-9).
type RecoveryDeps struct {
	Store        *Store
	Passes       *runner.PassRegistry
	Attestations *runner.AttestationStore
	LockTable    *runner.RoleContextLockTable
	IBTracker    *runner.InsufficientBasisTracker // optional
	JSONLPath    string                           // for forensic detail; recovery itself doesn't re-read
	Now          func() time.Time                 // F-12 idempotence injection
}

// RecoveryReport summarizes one Recovery invocation. Counts are
// for telemetry; Events is the audit trail (one entry per
// reconciliation action). session.Open surfaces these to the
// operator on chat-loop startup — Recovery does NOT publish to
// the OperatorBus (F-18).
type RecoveryReport struct {
	OrphansAborted        int
	OrphansPreserved      int
	AttestationsReplayed  int
	EvaluationRunsFlipped int
	Events                []runner.OperatorEvent
}

// ErrRecoveryReplayDirty signals that Replay finished with
// per-row errors. Recovery refuses to proceed because half-loaded
// state would be reconciled incorrectly (F-13).
var ErrRecoveryReplayDirty = errors.New(
	"recovery: ReplayCounts.Errors non-empty; refuse to proceed")

// recoverySource is the provenance string written to
// evaluation_runs.recovery_source for F-4 reconciliations.
const recoverySource = "recovery-attestation-replay"

// Recovery scans the engine + JSONL state and reconciles split-
// brain conditions. See file header for invariants.
//
// MUST be called AFTER engine.Replay and BEFORE the live Journal
// observers attach. Returns RecoveryReport with per-step counts +
// the audit-trail events for session.Open to surface.
func Recovery(
	ctx context.Context,
	deps RecoveryDeps,
	replayCounts ReplayCounts,
) (RecoveryReport, error) {
	if len(replayCounts.Errors) > 0 {
		return RecoveryReport{}, fmt.Errorf("%w: %d row errors", ErrRecoveryReplayDirty, len(replayCounts.Errors))
	}
	if deps.Store == nil {
		return RecoveryReport{}, errors.New("recovery: nil Store")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}

	run := &recoveryRun{deps: deps}

	// F-10: single transaction wrap so concurrent read-only CLIs
	// see pre- or post-recovery atomically.
	tx, err := deps.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return RecoveryReport{}, fmt.Errorf("recovery: BeginTx: %w", err)
	}
	defer func() {
		// On error / panic the deferred Rollback runs; on commit
		// Rollback is a no-op (sql package contract).
		_ = tx.Rollback()
	}()
	run.tx = tx

	orphans, err := run.orphanScan(ctx)
	if err != nil {
		return RecoveryReport{}, err
	}
	preserved, remaining, err := run.attestationPendingScan(ctx, orphans)
	if err != nil {
		return RecoveryReport{}, err
	}
	if err := run.preserveOpen(ctx, preserved); err != nil {
		return RecoveryReport{}, err
	}
	if err := run.orphanAbort(ctx, remaining); err != nil {
		return RecoveryReport{}, err
	}
	if err := run.evaluationRunReconcile(ctx); err != nil {
		return RecoveryReport{}, err
	}

	if err := tx.Commit(); err != nil {
		return RecoveryReport{}, fmt.Errorf("recovery: Commit: %w", err)
	}
	return run.report, nil
}

// recoveryRun groups the in-flight transaction + accumulated
// report for the per-scan methods.
type recoveryRun struct {
	tx     *sql.Tx
	deps   RecoveryDeps
	report RecoveryReport
}

// orphanScan returns every open pass row not yet preserved. Per
// F-12 idempotence: rows with recovered_at != ” are excluded.
func (r *recoveryRun) orphanScan(ctx context.Context) ([]PassRecord, error) {
	rows, err := r.tx.QueryContext(ctx, `
		SELECT pass_id, role, context, arrow_id, grid_version,
		       state, opened_at, closed_at, close_reason, recovered_at
		FROM   passes
		WHERE  state = 'open' AND recovered_at = ''
		ORDER BY opened_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("orphanScan: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []PassRecord
	for rows.Next() {
		rec, err := scanPass(rows)
		if err != nil {
			return nil, fmt.Errorf("orphanScan scan: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("orphanScan rows: %w", err)
	}
	return out, nil
}

// attestationPendingScan partitions orphans into (preserved,
// remaining). A pass is preserved iff it has at least one
// evaluation_runs row matching the JOIN defined in ADR-015 Part E.
// Returns the matching hint info alongside each preserved pass
// for the OperatorEvent payload.
type preservedPass struct {
	Pass     PassRecord
	ClauseID string
	AttRef   string
	ArrowID  string
}

func (r *recoveryRun) attestationPendingScan(
	ctx context.Context, orphans []PassRecord,
) (preserved []preservedPass, remaining []PassRecord, err error) {
	for _, p := range orphans {
		rows, queryErr := r.tx.QueryContext(ctx, `
			SELECT e.clause_id, e.depth_type_attestation_ref, e.arrow_id
			FROM   evaluation_runs e
			LEFT JOIN attestations a
			   ON a.attestation_id = e.depth_type_attestation_ref
			WHERE  e.pass_id = ?
			   AND e.depth_type_attestation_ref != ''
			   AND e.end_status = 'running'
			   AND a.attestation_id IS NULL
			LIMIT 1
		`, p.PassID)
		if queryErr != nil {
			return nil, nil, fmt.Errorf("attestationPendingScan %s: %w", safeID(p.PassID), queryErr)
		}
		var found *preservedPass
		if rows.Next() {
			var clauseID, attRef, arrowID string
			if err := rows.Scan(&clauseID, &attRef, &arrowID); err != nil {
				_ = rows.Close()
				return nil, nil, fmt.Errorf("attestationPendingScan scan %s: %w", safeID(p.PassID), err)
			}
			found = &preservedPass{Pass: p, ClauseID: clauseID, AttRef: attRef, ArrowID: arrowID}
		}
		_ = rows.Close()
		if found != nil {
			preserved = append(preserved, *found)
		} else {
			remaining = append(remaining, p)
		}
	}
	return preserved, remaining, nil
}

// preserveOpen marks the preserved set with recovered_at and calls
// PassRegistry.Resume so the live registry shows them. Emits one
// recovery-attestation-republished event per preserved pass.
func (r *recoveryRun) preserveOpen(ctx context.Context, preserved []preservedPass) error {
	if len(preserved) == 0 {
		return nil
	}
	now := r.deps.Now()
	stamp := now.UTC().Format(time.RFC3339Nano)
	for _, item := range preserved {
		if _, err := r.tx.ExecContext(ctx, `
			UPDATE passes
			SET    recovered_at = ?
			WHERE  pass_id = ? AND recovered_at = ''
		`, stamp, item.Pass.PassID); err != nil {
			return fmt.Errorf("preserveOpen UPDATE %s: %w", safeID(item.Pass.PassID), err)
		}
		// PassRegistry.Resume rebuilds the in-memory *Pass + re-
		// acquires the lock token. Failure here is logged but the
		// engine row is still preserved (sqlite is the source of
		// truth for recovery decisions).
		if r.deps.Passes != nil && r.deps.LockTable != nil {
			openedAt, _ := time.Parse(time.RFC3339Nano, item.Pass.OpenedAt)
			if _, err := r.deps.Passes.Resume(runner.ResumeOptions{
				PassID:      item.Pass.PassID,
				Role:        item.Pass.Role,
				Context:     item.Pass.Context,
				ArrowID:     item.Pass.ArrowID,
				GridVersion: item.Pass.GridVersion,
				OpenedAt:    openedAt,
				Now:         func() time.Time { return now },
			}, r.deps.LockTable); err != nil {
				// Defensive: a process fresh after crash has an empty
				// lock table; Resume should not fail. Log via the
				// report's events slice so the operator sees it.
				r.report.Events = append(r.report.Events, runner.OperatorEvent{
					Kind:    runner.OpEventRecoveryAttestationRepublished,
					ArrowID: item.ArrowID,
					PassID:  item.Pass.PassID,
					Detail:  "WARNING resume failed: " + err.Error(),
				})
				continue
			}
		}
		r.report.OrphansPreserved++
		r.report.Events = append(r.report.Events, runner.OperatorEvent{
			Kind:     runner.OpEventRecoveryAttestationRepublished,
			ArrowID:  item.ArrowID,
			PassID:   item.Pass.PassID,
			ClauseID: item.ClauseID,
			Detail:   "att-ref=" + item.AttRef + " preserved at " + stamp,
		})
	}
	return nil
}

// orphanAbort UPDATEs the remaining open passes to aborted:crash.
// Emits one recovery-pass-aborted-crash event per pass.
func (r *recoveryRun) orphanAbort(ctx context.Context, remaining []PassRecord) error {
	if len(remaining) == 0 {
		return nil
	}
	now := r.deps.Now()
	stamp := now.UTC().Format(time.RFC3339Nano)
	for _, p := range remaining {
		if _, err := r.tx.ExecContext(ctx, `
			UPDATE passes
			SET    state        = 'aborted',
			       closed_at    = ?,
			       close_reason = 'crash',
			       recovered_at = ?
			WHERE  pass_id = ? AND state = 'open'
		`, stamp, stamp, p.PassID); err != nil {
			return fmt.Errorf("orphanAbort UPDATE %s: %w", safeID(p.PassID), err)
		}
		r.report.OrphansAborted++
		r.report.Events = append(r.report.Events, runner.OperatorEvent{
			Kind:    runner.OpEventRecoveryPassAbortedCrash,
			ArrowID: p.ArrowID,
			PassID:  p.PassID,
			Role:    p.Role,
			Detail:  "no live process at restart; closed_at=" + stamp,
		})
	}
	return nil
}

// evaluationRunReconcile flips end_status for clauses whose JSONL
// has a verdict but whose engine row still says running. Uses
// the in-memory attestation cache (already rebuilt via
// AttestationStore.LoadFromJSONL).
func (r *recoveryRun) evaluationRunReconcile(ctx context.Context) error {
	if r.deps.Attestations == nil {
		return nil
	}
	rows, err := r.tx.QueryContext(ctx, `
		SELECT id, clause_id, pass_id, arrow_id, depth_type_attestation_ref
		FROM   evaluation_runs
		WHERE  end_status = 'running'
		   AND depth_type_attestation_ref != ''
	`)
	if err != nil {
		return fmt.Errorf("evaluationRunReconcile: %w", err)
	}
	type runRow struct {
		ID, ClauseID, PassID, ArrowID, Ref string
	}
	var runs []runRow
	for rows.Next() {
		var rr runRow
		if err := rows.Scan(&rr.ID, &rr.ClauseID, &rr.PassID, &rr.ArrowID, &rr.Ref); err != nil {
			_ = rows.Close()
			return fmt.Errorf("evaluationRunReconcile scan: %w", err)
		}
		runs = append(runs, rr)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("evaluationRunReconcile rows: %w", err)
	}
	_ = rows.Close()

	now := r.deps.Now()
	stamp := now.UTC().Format(time.RFC3339Nano)
	for _, rr := range runs {
		rec, ok := r.deps.Attestations.Lookup(rr.Ref)
		if !ok {
			continue // still pending (the attestationPendingScan handled this)
		}
		mapped := verdictToClauseStatus(rec.Verdict)
		if _, err := r.tx.ExecContext(ctx, `
			UPDATE evaluation_runs
			SET    end_status      = ?,
			       recovery_source = ?,
			       completed_at    = CASE WHEN completed_at = '' THEN ? ELSE completed_at END
			WHERE  id = ?
		`, mapped, recoverySource, stamp, rr.ID); err != nil {
			return fmt.Errorf("evaluationRunReconcile UPDATE %s: %w", safeID(rr.ID), err)
		}
		r.report.EvaluationRunsFlipped++
		r.report.Events = append(r.report.Events, runner.OperatorEvent{
			Kind:     runner.OpEventRecoveryAttestationReplay,
			ArrowID:  rr.ArrowID,
			PassID:   rr.PassID,
			ClauseID: rr.ClauseID,
			Detail:   "att-ref=" + rr.Ref + " verdict=" + string(rec.Verdict) + " mapped=" + mapped,
		})
	}
	return nil
}

// verdictToClauseStatus maps an attestation verdict to a clause
// EndStatus string per ADR-015 Part D:
//
//	pass                → "pass"
//	fail                → "fail"
//	insufficient-basis  → "running" (process-local flag can't be
//	                      reconstructed; keep status pending so
//	                      the dispatcher re-emits the hint on
//	                      next traversal).
func verdictToClauseStatus(v runner.AttestationVerdict) string {
	switch v {
	case runner.AttestationPass:
		return "pass"
	case runner.AttestationFail:
		return "fail"
	case runner.AttestationInsufficientBasis:
		return "running"
	default:
		return "running"
	}
}
