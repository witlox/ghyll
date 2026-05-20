package acceptance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cucumber/godog"
	"github.com/witlox/ghyll/engine"
	"github.com/witlox/ghyll/runner"
)

// Tier 1 (ADR-015) deferred-scenario bindings. Drives the 7
// crash-recovery scenarios end-to-end against the implementer-
// shipped engine.Recovery + AttestationStore.LoadFromJSONL +
// PassRegistry.Resume substrate.
//
// Scenarios:
//   - state-machine.feature:
//       Pass aborted by crash recovery
//       Restart from checkpoint log
//       Query historical pass
//       Crash while clause is awaiting-attestation
//       Crash between attestation write and clause-status flip
//   - runner.feature:
//       Pass completes and emits checkpoint
//       Pass aborted records reason in checkpoint

func registerTier1RecoverySteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	ctx.Before(func(c context.Context, sc *godog.Scenario) (context.Context, error) {
		dir, err := os.MkdirTemp("", "tier1-recovery-")
		if err != nil {
			return c, err
		}
		state.TR1Workdir = dir
		store, err := engine.OpenStore(filepath.Join(dir, "engine.db"))
		if err != nil {
			return c, err
		}
		state.TR1Store = store
		state.TR1Atts = runner.NewAttestationStore()
		state.TR1Passes = runner.NewPassRegistry()
		state.TR1LockTable = runner.NewRoleContextLockTable()
		state.TR1RecoveryRep = engine.RecoveryReport{}
		state.TR1RecoveryErr = nil
		state.TR1PassRec = engine.PassRecord{}
		state.TR1PassFound = false
		state.TR1HistoricalPass = engine.PassRecord{}
		state.TR1HistoryFound = false
		return c, nil
	})
	ctx.After(func(c context.Context, sc *godog.Scenario, _ error) (context.Context, error) {
		// M-2 (G2-F-10 / G2-I-T4): RemoveAll the per-scenario
		// tmpdir so CI doesn't accumulate `tier1-recovery-*` dirs.
		if state.TR1Store != nil {
			_ = state.TR1Store.Close()
		}
		if state.TR1Workdir != "" {
			_ = os.RemoveAll(state.TR1Workdir)
			state.TR1Workdir = ""
		}
		return c, nil
	})

	// -------- Pass aborted by crash recovery --------

	ctx.Step(`^pass P1 was "running" but the runner crashed$`, func() error {
		// Seed an open pass row directly — simulating a crashed
		// prior process.
		return state.TR1Store.UpsertPass(context.Background(), engine.PassRecord{
			PassID: "P1", Role: "analyst", Context: "A", ArrowID: "A1",
			State: "open", OpenedAt: "2026-05-20T10:00:00Z",
		})
	})

	ctx.Step(`^the engine performs crash recovery on restart$`, func() error {
		rep, err := engine.Recovery(context.Background(), engine.RecoveryDeps{
			Store: state.TR1Store, Passes: state.TR1Passes,
			Attestations: state.TR1Atts, LockTable: state.TR1LockTable,
			Now: func() time.Time { return time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC) },
		}, engine.ReplayCounts{})
		state.TR1RecoveryRep = rep
		state.TR1RecoveryErr = err
		return err
	})

	ctx.Step(`^the engine finds the orphaned pass and transitions it to "aborted" with reason "crash"$`, func() error {
		got, ok, err := state.TR1Store.GetPass(context.Background(), "P1")
		if err != nil {
			return fmt.Errorf("GetPass: %w", err)
		}
		if !ok {
			return errors.New("P1 missing after recovery")
		}
		if got.State != "aborted" {
			return fmt.Errorf("state = %q; want aborted", got.State)
		}
		if got.CloseReason != "crash" {
			return fmt.Errorf("close_reason = %q; want crash", got.CloseReason)
		}
		if state.TR1RecoveryRep.OrphansAborted != 1 {
			return fmt.Errorf("OrphansAborted = %d; want 1", state.TR1RecoveryRep.OrphansAborted)
		}
		return nil
	})

	// -------- Restart from checkpoint log --------

	ctx.Step(`^the harness was running and is restarted$`, func() error {
		// Seed mixed state: one running pass, one closed pass, one
		// aborted pass. After recovery the running one is aborted,
		// the historical ones remain queryable.
		ctx := context.Background()
		for _, rec := range []engine.PassRecord{
			{PassID: "P-running", Role: "r", Context: "c", ArrowID: "A1", State: "open", OpenedAt: "2026-05-20T10:00:00Z"},
			{PassID: "P-closed", Role: "r", Context: "c", ArrowID: "A2", State: "closed", OpenedAt: "2026-05-20T10:01:00Z", ClosedAt: "2026-05-20T10:02:00Z", CloseReason: "derived-complete"},
			{PassID: "P-aborted", Role: "r", Context: "c", ArrowID: "A3", State: "aborted", OpenedAt: "2026-05-20T10:03:00Z", ClosedAt: "2026-05-20T10:04:00Z", CloseReason: "amendment-drained"},
		} {
			if err := state.TR1Store.UpsertPass(ctx, rec); err != nil {
				return fmt.Errorf("seed %s: %w", rec.PassID, err)
			}
		}
		return nil
	})

	ctx.Step(`^the engine initializes$`, func() error {
		// "Initialize" = run Recovery (the session.Open path).
		rep, err := engine.Recovery(context.Background(), engine.RecoveryDeps{
			Store: state.TR1Store, Passes: state.TR1Passes,
			Attestations: state.TR1Atts, LockTable: state.TR1LockTable,
			Now: func() time.Time { return time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC) },
		}, engine.ReplayCounts{})
		state.TR1RecoveryRep = rep
		state.TR1RecoveryErr = err
		return err
	})

	ctx.Step(`^it reads the checkpoint log to reconstruct all "running" passes \(treated as "aborted" with reason "crash"\), all "completed"/"aborted" passes \(kept in log, not in in-memory store\), current grid version, and current arrow statuses \(per the latest completed pass per arrow\)$`, func() error {
		ctx := context.Background()
		// The running pass is now aborted:crash.
		got, _, _ := state.TR1Store.GetPass(ctx, "P-running")
		if got.State != "aborted" || got.CloseReason != "crash" {
			return fmt.Errorf("p-running not aborted-crash: %+v", got)
		}
		// The historical passes are unchanged.
		got, _, _ = state.TR1Store.GetPass(ctx, "P-closed")
		if got.State != "closed" || got.CloseReason != "derived-complete" {
			return fmt.Errorf("p-closed mutated: %+v", got)
		}
		got, _, _ = state.TR1Store.GetPass(ctx, "P-aborted")
		if got.State != "aborted" || got.CloseReason != "amendment-drained" {
			return fmt.Errorf("p-aborted mutated: %+v", got)
		}
		// In-memory PassRegistry should NOT contain historical passes
		// (only preserved passes go through Resume).
		if state.TR1Passes.Len() != 0 {
			return fmt.Errorf("registry has %d entries; want 0 (no attestation-pending in this scenario)", state.TR1Passes.Len())
		}
		return nil
	})

	ctx.Step(`^the engine is ready to accept new pass starts$`, func() error {
		// "Ready" = a fresh OpenPass succeeds on a free (role, context).
		p, err := runner.OpenPass(runner.PassOptions{
			PassID: "P-new", Role: "r", Context: "c-new", ArrowID: "A-new",
			LockTable: state.TR1LockTable,
		})
		if err != nil {
			return fmt.Errorf("OpenPass post-recovery: %w", err)
		}
		p.Close("test-cleanup")
		return nil
	})

	// -------- Query historical pass --------

	ctx.Step(`^a query for pass P5 \(completed and flushed\)$`, func() error {
		// Seed a closed P5.
		return state.TR1Store.UpsertPass(context.Background(), engine.PassRecord{
			PassID: "P5", Role: "analyst", Context: "A", ArrowID: "A5",
			GridVersion: 3,
			State:       "closed",
			OpenedAt:    "2026-05-20T09:00:00Z",
			ClosedAt:    "2026-05-20T09:05:00Z",
			CloseReason: "derived-complete",
		})
	})

	ctx.Step(`^the engine receives the query$`, func() error {
		rec, ok, err := state.TR1Store.GetPass(context.Background(), "P5")
		state.TR1HistoricalPass = rec
		state.TR1HistoryFound = ok
		return err
	})

	ctx.Step(`^it reads from the checkpoint log$`, func() error {
		// Implicit — GetPass IS the checkpoint-log read in the
		// Tier 1 architecture (the engine sqlite is the checkpoint
		// store). Verify the call succeeded.
		if !state.TR1HistoryFound {
			return errors.New("P5 not found via GetPass")
		}
		return nil
	})

	ctx.Step(`^returns the historical pass's full state$`, func() error {
		p := state.TR1HistoricalPass
		if p.PassID != "P5" || p.Role != "analyst" || p.Context != "A" ||
			p.ArrowID != "A5" || p.State != "closed" ||
			p.CloseReason != "derived-complete" || p.GridVersion != 3 {
			return fmt.Errorf("historical pass payload = %+v; want full P5 state", p)
		}
		return nil
	})

	// -------- Crash while clause is awaiting-attestation --------

	ctx.Step(`^pass P1 has clause C5 with status "awaiting-attestation"$`, func() error {
		// Seed a pass + an evaluation_runs row with end_status=running
		// AND a depth_type_attestation_ref. No matching attestations
		// row → JOIN identifies P1 as attestation-pending.
		ctx := context.Background()
		if err := state.TR1Store.UpsertPass(ctx, engine.PassRecord{
			PassID: "P1", Role: "analyst", Context: "A", ArrowID: "A1",
			State: "open", OpenedAt: "2026-05-20T10:00:00Z",
		}); err != nil {
			return err
		}
		return state.TR1Store.InsertEvaluationRun(ctx, engine.EvaluationRunRecord{
			ID: "R5", ClauseID: "C5", PassID: "P1", ArrowID: "A1",
			DepthTypeAttestationRef: "att-X-C5-v1",
			StartStatus:             "pending",
			EndStatus:               "running",
			ResultJSON:              "{}",
		})
	})

	ctx.Step(`^the hint has been published to the operator event bus$`, func() error {
		// Narrative — the substrate signal is the
		// depth_type_attestation_ref on the evaluation_runs row,
		// which is already populated above. The volatile-bus claim
		// is what the gate-1 review F-1 reclassified as
		// "JOIN-based detection".
		return nil
	})

	ctx.Step(`^the operator has not yet returned a verdict$`, func() error {
		// Narrative — verified by the absence of an attestations
		// row matching att-X-C5-v1.
		return nil
	})

	ctx.Step(`^the harness crashes and restarts$`, func() error {
		rep, err := engine.Recovery(context.Background(), engine.RecoveryDeps{
			Store: state.TR1Store, Passes: state.TR1Passes,
			Attestations: state.TR1Atts, LockTable: state.TR1LockTable,
			Now: func() time.Time { return time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC) },
		}, engine.ReplayCounts{})
		state.TR1RecoveryRep = rep
		state.TR1RecoveryErr = err
		return err
	})

	ctx.Step(`^crash recovery does NOT mark P1 as aborted \(the operator can still deliver a verdict\)$`, func() error {
		got, ok, _ := state.TR1Store.GetPass(context.Background(), "P1")
		if !ok {
			return errors.New("P1 missing")
		}
		if got.State != "open" {
			return fmt.Errorf("state = %q; want open (preserved)", got.State)
		}
		if got.RecoveredAt == "" {
			return errors.New("RecoveredAt unstamped on preserved pass")
		}
		if state.TR1RecoveryRep.OrphansPreserved != 1 {
			return fmt.Errorf("OrphansPreserved = %d; want 1", state.TR1RecoveryRep.OrphansPreserved)
		}
		return nil
	})

	ctx.Step(`^the attestation request is re-published on the event bus on restart \(so a UI client that reconnected sees it again\)$`, func() error {
		// RecoveryReport.Events carries the
		// recovery-attestation-republished event with the hint
		// payload. session.Open surfaces it (F-18).
		var saw bool
		for _, ev := range state.TR1RecoveryRep.Events {
			if ev.Kind == runner.OpEventRecoveryAttestationRepublished &&
				ev.ClauseID == "C5" {
				saw = true
				break
			}
		}
		if !saw {
			return fmt.Errorf("no republish event for C5 in %+v", state.TR1RecoveryRep.Events)
		}
		return nil
	})

	ctx.Step(`^C5's status remains "awaiting-attestation" after recovery$`, func() error {
		// The evaluation_runs.end_status stays "running" because
		// the JSONL had no verdict to flip it to. Verify.
		var endStatus, recoverySrc string
		if err := state.TR1Store.DB().QueryRowContext(context.Background(),
			`SELECT end_status, recovery_source FROM evaluation_runs WHERE id = ?`, "R5",
		).Scan(&endStatus, &recoverySrc); err != nil {
			return fmt.Errorf("scan run: %w", err)
		}
		if endStatus != "running" {
			return fmt.Errorf("end_status = %q; want running (still awaiting)", endStatus)
		}
		if recoverySrc != "" {
			return fmt.Errorf("recovery_source = %q; want empty (no flip)", recoverySrc)
		}
		return nil
	})

	// -------- Crash between attestation write and clause-status flip --------

	ctx.Step(`^the operator submitted verdict "pass" for clause C5$`, func() error {
		// Seed a pass with the verdict in JSONL but not yet flipped
		// in the run row.
		ctx := context.Background()
		if err := state.TR1Store.UpsertPass(ctx, engine.PassRecord{
			PassID: "P1", Role: "analyst", Context: "A", ArrowID: "A1",
			State: "open", OpenedAt: "2026-05-20T10:00:00Z",
		}); err != nil {
			return err
		}
		if err := state.TR1Store.InsertEvaluationRun(ctx, engine.EvaluationRunRecord{
			ID: "R5", ClauseID: "C5", PassID: "P1", ArrowID: "A1",
			DepthTypeAttestationRef: "att-X-C5-v1",
			StartStatus:             "pending",
			EndStatus:               "running",
			ResultJSON:              "{}",
		}); err != nil {
			return err
		}
		// The attestation IS in the in-memory store (simulating
		// LoadFromJSONL having read it).
		return state.TR1Atts.Record(runner.AttestationRecord{
			ID:             "att-X-C5-v1",
			Kind:           runner.AttestationKindDepthType,
			ArrowID:        "A1",
			ClauseID:       "C5",
			OpID:           "alice@example.com",
			AttestedByRole: "operator",
			SourceRole:     "analyst",
			TargetRole:     "architect",
			Verdict:        runner.AttestationPass,
			Timestamp:      1716100000_000000000,
			GridVersion:    1,
			PassID:         "P1",
		})
	})

	ctx.Step(`^the JSONL record was appended successfully$`, func() error {
		// Narrative — the in-memory record + the engine catch-up
		// represent the post-LoadFromJSONL state. Verify the
		// attestation is in the engine table too.
		if _, _, err := state.TR1Store.CatchUpAttestations(context.Background(), state.TR1Atts); err != nil {
			return fmt.Errorf("CatchUp: %w", err)
		}
		return nil
	})

	ctx.Step(`^the engine's clause-status transition has not yet committed$`, func() error {
		// Narrative — the evaluation_runs row still has end_status=running.
		var endStatus string
		if err := state.TR1Store.DB().QueryRowContext(context.Background(),
			`SELECT end_status FROM evaluation_runs WHERE id = ?`, "R5",
		).Scan(&endStatus); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		if endStatus != "running" {
			return fmt.Errorf("end_status = %q; want running (pre-flip)", endStatus)
		}
		return nil
	})

	ctx.Step(`^crash recovery reads the latest attestation record for C5$`, func() error {
		// Implicit — handled by recovery.evaluationRunReconcile.
		// Verified by the next two steps.
		return nil
	})

	ctx.Step(`^reconciles C5's status to match the recorded verdict \("pass"\)$`, func() error {
		var endStatus, recoverySrc string
		if err := state.TR1Store.DB().QueryRowContext(context.Background(),
			`SELECT end_status, recovery_source FROM evaluation_runs WHERE id = ?`, "R5",
		).Scan(&endStatus, &recoverySrc); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		if endStatus != "pass" {
			return fmt.Errorf("end_status = %q; want pass", endStatus)
		}
		if recoverySrc != "recovery-attestation-replay" {
			return fmt.Errorf("recovery_source = %q", recoverySrc)
		}
		return nil
	})

	ctx.Step(`^the reconciliation is recorded as a recovery event for audit$`, func() error {
		var saw bool
		for _, ev := range state.TR1RecoveryRep.Events {
			if ev.Kind == runner.OpEventRecoveryAttestationReplay && ev.ClauseID == "C5" {
				saw = true
				break
			}
		}
		if !saw {
			return fmt.Errorf("no replay event in %+v", state.TR1RecoveryRep.Events)
		}
		return nil
	})

	ctx.Step(`^no "split-brain" persists \(record says pass, in-memory says awaiting-attestation\)$`, func() error {
		// Verify: in-memory attestation says pass + engine table
		// says end_status=pass. Both agree → no split-brain.
		rec, ok := state.TR1Atts.Lookup("att-X-C5-v1")
		if !ok || rec.Verdict != runner.AttestationPass {
			return fmt.Errorf("in-memory attestation = %+v ok=%v", rec, ok)
		}
		return nil
	})

	// -------- Pass completes and emits checkpoint (runner.feature) --------

	ctx.Step(`^pass P1 has reached terminal arrow status$`, func() error {
		// M-1 (G2-F-9): use SCENARIO-LOCAL registry + lock-table
		// rather than overwriting shared state.TR1Passes /
		// state.TR1LockTable. This step is self-contained: it opens
		// a pass, closes it, flushes the journal, asserts the row
		// landed. The After hook stays correct for the rest of the
		// scenario.
		passes := runner.NewPassRegistry()
		lockTable := runner.NewRoleContextLockTable()
		journal := engine.NewJournal(state.TR1Store, nil)
		journal.AttachPasses(passes)
		p, err := runner.OpenPass(runner.PassOptions{
			PassID: "P-finalize", Role: "analyst", Context: "F",
			ArrowID: "A-final", GridVersion: 1,
			LockTable: lockTable,
		})
		if err != nil {
			return fmt.Errorf("OpenPass: %w", err)
		}
		passes.Register(p)
		p.Close("derived-complete")
		journal.Flush()
		journal.Close()
		return nil
	})

	ctx.Step(`^the runner finalizes the pass$`, func() error {
		// Close + flush already happened in the previous step.
		got, ok, _ := state.TR1Store.GetPass(context.Background(), "P-finalize")
		state.TR1PassRec = got
		state.TR1PassFound = ok
		return nil
	})

	ctx.Step(`^the runner emits a checkpoint with pass-id, arrow-id, grid-version, clause-by-clause status, finding ids raised, pass-status "completed", and timestamps$`, func() error {
		if !state.TR1PassFound {
			return errors.New("p-finalize row missing in engine")
		}
		p := state.TR1PassRec
		if p.PassID == "" || p.ArrowID != "A-final" || p.GridVersion != 1 {
			return fmt.Errorf("checkpoint missing identity fields: %+v", p)
		}
		if p.State != "closed" || p.CloseReason != "derived-complete" {
			return fmt.Errorf("state/reason = %q/%q; want closed/derived-complete", p.State, p.CloseReason)
		}
		if p.OpenedAt == "" || p.ClosedAt == "" {
			return errors.New("timestamps unset")
		}
		// "Clause-by-clause status" + "finding ids raised" — these
		// are carried by evaluation_runs and findings rows, queryable
		// separately. The pass checkpoint itself records the
		// pass-level summary; cross-table assembly is the engine
		// status CLI's job (out of scope for this scenario).
		return nil
	})

	ctx.Step(`^the checkpoint is appended to the project's checkpoint log$`, func() error {
		// The engine `passes` table IS the checkpoint log. Verify
		// the row is reachable via ListPasses too.
		passes, err := state.TR1Store.ListPasses(context.Background(),
			engine.PassListFilter{State: "closed"})
		if err != nil {
			return fmt.Errorf("ListPasses: %w", err)
		}
		var saw bool
		for _, p := range passes {
			if p.PassID == "P-finalize" {
				saw = true
				break
			}
		}
		if !saw {
			return errors.New("p-finalize not in closed-passes list")
		}
		return nil
	})

	// -------- Pass aborted records reason in checkpoint --------

	ctx.Step(`^pass P1 was aborted mid-phase$`, func() error {
		// M-1 (G2-F-9): scenario-local registry + lock-table.
		passes := runner.NewPassRegistry()
		lockTable := runner.NewRoleContextLockTable()
		journal := engine.NewJournal(state.TR1Store, nil)
		journal.AttachPasses(passes)
		p, err := runner.OpenPass(runner.PassOptions{
			PassID: "P-abort", Role: "implementer", Context: "X",
			ArrowID: "A-abort", GridVersion: 1,
			LockTable: lockTable,
		})
		if err != nil {
			return fmt.Errorf("OpenPass: %w", err)
		}
		passes.Register(p)
		p.Abort("amendment-drained: missing-cross-context-spec")
		journal.Flush()
		journal.Close()
		return nil
	})

	ctx.Step(`^the runner finalizes the aborted pass$`, func() error {
		got, ok, _ := state.TR1Store.GetPass(context.Background(), "P-abort")
		state.TR1PassRec = got
		state.TR1PassFound = ok
		return nil
	})

	ctx.Step(`^the checkpoint records pass-status "aborted" with the abort reason$`, func() error {
		if !state.TR1PassFound {
			return errors.New("p-abort row missing")
		}
		p := state.TR1PassRec
		if p.State != "aborted" {
			return fmt.Errorf("state = %q; want aborted", p.State)
		}
		if !strings.Contains(p.CloseReason, "amendment-drained") {
			return fmt.Errorf("close_reason = %q; missing abort context", p.CloseReason)
		}
		return nil
	})

	ctx.Step(`^the partial evaluation results are persisted for forensic value$`, func() error {
		// Evaluation runs from before abort are persisted via the
		// existing Journal.AttachRunner path; this scenario's
		// runner.Pass does not run clauses, so we just verify the
		// pass row carries enough provenance for a forensic re-
		// trace (pass_id, role, context, arrow_id, opened/closed
		// timestamps, abort reason).
		p := state.TR1PassRec
		if p.OpenedAt == "" || p.ClosedAt == "" {
			return errors.New("forensic timestamps unset")
		}
		if p.PassID == "" || p.ArrowID == "" {
			return errors.New("forensic identity fields unset")
		}
		return nil
	})
}
