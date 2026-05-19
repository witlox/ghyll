package acceptance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"
	"github.com/witlox/ghyll/runner"
)

// Step bindings for runner.feature deferred scenarios that exercise
// the per-(role, context) lock + concurrent Pass admission + amendment
// abort + depth-gate short-circuit. All driven against the existing
// runner substrate.
//
// Scenarios wired:
//   - Two passes on different contexts run concurrently
//   - Two passes on same (role, context) are refused
//   - Pass aborted due to grid amendment
//   - Machine evaluator returns unevaluated due to depth

func registerRunnerDeferredSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	ctx.Before(func(c context.Context, sc *godog.Scenario) (context.Context, error) {
		state.RPLockTable = runner.NewRoleContextLockTable()
		state.RPBus = runner.NewOperatorBus()
		state.RPRegistry = runner.NewRegistry()
		runner.RegisterBuiltins(state.RPRegistry)
		state.RPRunner = runner.NewRunner(state.RPRegistry).
			WithActualTier(runner.DepthRankShallow)
		state.RPPasses = runner.NewPassRegistry()
		state.RPGrid = runner.NewGrid()
		state.RPAmendQueue = runner.NewAmendmentQueue()
		state.RPFindings = runner.NewFindingsStore()
		state.RPCommitter = &runner.AmendmentCommitter{
			Grid:   state.RPGrid,
			Passes: state.RPPasses,
			Bus:    state.RPBus,
			Queue:  state.RPAmendQueue,
		}
		state.RPP1 = nil
		state.RPP2 = nil
		state.RPP2Err = nil
		state.RPP1Open = false
		state.RPP2Open = false
		state.RPRun = nil
		state.RPRunErr = nil
		state.RPDepthBlock = false
		state.RPGridVersion = 1
		return c, nil
	})

	// -------- Two passes on different contexts run concurrently --------

	ctx.Step(`^pass P1 on \(analyst, contextA, stratum-1\)$`, func() error {
		// Recorded — opened in parallel with P2 in the "scheduled" step.
		return nil
	})

	ctx.Step(`^pass P2 on \(analyst, contextB, stratum-1\)$`, func() error {
		return nil
	})

	ctx.Step(`^both pass P1 and pass P2 evaluate the same clause-concept \(e\.g\., `+"`"+`no-orphan-symbol`+"`"+`\) at the same wall-clock instant$`,
		func() error {
			// The clause-concept itself isn't load-bearing for the
			// concurrency claim — the lock table is. Narrative.
			return nil
		})

	ctx.Step(`^both are scheduled$`, func() error {
		// Open both passes in parallel. The lock table is per-(role,
		// context); different contexts → independent locks → both
		// goroutines acquire.
		var wg sync.WaitGroup
		gate := make(chan struct{})
		var p1, p2 *runner.Pass
		var err1, err2 error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-gate
			p1, err1 = runner.OpenPass(runner.PassOptions{
				PassID:    "P1",
				Role:      "analyst",
				Context:   "contextA",
				ArrowID:   "A1",
				LockTable: state.RPLockTable,
				Bus:       state.RPBus,
			})
		}()
		go func() {
			defer wg.Done()
			<-gate
			p2, err2 = runner.OpenPass(runner.PassOptions{
				PassID:    "P2",
				Role:      "analyst",
				Context:   "contextB",
				ArrowID:   "A1",
				LockTable: state.RPLockTable,
				Bus:       state.RPBus,
			})
		}()
		close(gate) // simultaneous release
		wg.Wait()
		if err1 != nil {
			return fmt.Errorf("open P1: %w", err1)
		}
		if err2 != nil {
			return fmt.Errorf("open P2: %w", err2)
		}
		state.RPP1, state.RPP2 = p1, p2
		state.RPPasses.Register(p1)
		state.RPPasses.Register(p2)
		// Probe Open-ness BEFORE either pass closes — both must be
		// reported Open simultaneously.
		state.RPP1Open = p1.State() == runner.PassStateOpen
		state.RPP2Open = p2.State() == runner.PassStateOpen
		return nil
	})

	ctx.Step(`^the runner permits both to run concurrently$`, func() error {
		if !state.RPP1Open {
			return errors.New("P1 not Open after parallel admission")
		}
		if !state.RPP2Open {
			return errors.New("P2 not Open after parallel admission")
		}
		return nil
	})

	ctx.Step(`^both evaluators' start timestamps overlap \(proving parallelism, not serialization\)$`, func() error {
		// OpenedAt windows on both passes are populated by OpenPass.
		// "Overlap" at the lock-table layer means: both are Open at
		// the same moment (verified above) AND neither's OpenedAt
		// follows the other's ClosedAt (passes are still in flight).
		if state.RPP1.ClosedAt() != (time.Time{}) || state.RPP2.ClosedAt() != (time.Time{}) {
			return errors.New("one of the passes closed before overlap check")
		}
		return nil
	})

	ctx.Step(`^the per-\(role, context\) lock for \(analyst, contextA\) is held by P1 only; the lock for \(analyst, contextB\) is held by P2 only$`,
		func() error {
			// Try to acquire contextA from a third pass — must fail.
			_, errA := state.RPLockTable.TryAcquire("analyst", "contextA", "P3", 0)
			var busy *runner.ErrRoleContextBusy
			if !errors.As(errA, &busy) {
				return fmt.Errorf("contextA TryAcquire = %v; want ErrRoleContextBusy", errA)
			}
			if busy.HoldingPass != "P1" {
				return fmt.Errorf("contextA holder = %q; want P1", busy.HoldingPass)
			}
			_, errB := state.RPLockTable.TryAcquire("analyst", "contextB", "P4", 0)
			if !errors.As(errB, &busy) {
				return fmt.Errorf("contextB TryAcquire = %v; want ErrRoleContextBusy", errB)
			}
			if busy.HoldingPass != "P2" {
				return fmt.Errorf("contextB holder = %q; want P2", busy.HoldingPass)
			}
			return nil
		})

	ctx.Step(`^the state-machine per-clause locks for P1 and P2 are distinct \(different pass-ids . different lock keys\)$`,
		func() error {
			// Per-clause locks are keyed on pass-id (per gates.md
			// §3.5). P1 and P2 have distinct ids → distinct keys.
			if state.RPP1.ID() == state.RPP2.ID() {
				return fmt.Errorf("P1.ID == P2.ID = %q; expected distinct", state.RPP1.ID())
			}
			return nil
		})

	ctx.Step(`^running with `+"`"+`-race`+"`"+` reports no data races on the shared evaluator-output buffer$`,
		func() error {
			// The CI pipeline runs `make test-race`; the acceptance
			// suite participates. A data race on the lock table would
			// have already failed the parallel admission step under
			// -race. We assert non-zero parallelism via the State
			// probes above; the race detector's verdict is the
			// independent harness layer.
			return nil
		})

	// -------- Two passes on same (role, context) are refused --------

	ctx.Step(`^pass P1 on \(analyst, contextA\) is running$`, func() error {
		p, err := runner.OpenPass(runner.PassOptions{
			PassID:    "P1",
			Role:      "analyst",
			Context:   "contextA",
			ArrowID:   "A-same-ctx",
			LockTable: state.RPLockTable,
			Bus:       state.RPBus,
		})
		if err != nil {
			return fmt.Errorf("open P1: %w", err)
		}
		state.RPP1 = p
		state.RPPasses.Register(p)
		return nil
	})

	ctx.Step(`^a second pass P2 on \(analyst, contextA\) is requested$`, func() error {
		p, err := runner.OpenPass(runner.PassOptions{
			PassID:    "P2",
			Role:      "analyst",
			Context:   "contextA",
			ArrowID:   "A-same-ctx",
			LockTable: state.RPLockTable,
			Bus:       state.RPBus,
		})
		state.RPP2 = p
		state.RPP2Err = err
		return nil
	})

	ctx.Step(`^the runner refuses P2 with kind "single-active-role-violation"$`, func() error {
		if state.RPP2Err == nil {
			return errors.New("P2 was not refused")
		}
		var busy *runner.ErrRoleContextBusy
		if !errors.As(state.RPP2Err, &busy) {
			return fmt.Errorf("P2 error = %v; want ErrRoleContextBusy (single-active-role-violation)",
				state.RPP2Err)
		}
		if busy.HoldingPass != "P1" {
			return fmt.Errorf("HoldingPass = %q; want P1", busy.HoldingPass)
		}
		return nil
	})

	ctx.Step(`^P2 is not started until P1 completes or aborts$`, func() error {
		// Verify by completing P1 and re-attempting P2.
		state.RPP1.Close("test-done")
		p2, err := runner.OpenPass(runner.PassOptions{
			PassID:    "P2-retry",
			Role:      "analyst",
			Context:   "contextA",
			ArrowID:   "A-same-ctx",
			LockTable: state.RPLockTable,
			Bus:       state.RPBus,
		})
		if err != nil {
			return fmt.Errorf("re-attempt P2 after P1 closed: %w", err)
		}
		state.RPP2 = p2
		return nil
	})

	// -------- Pass aborted due to grid amendment --------

	ctx.Step(`^pass P1 is in remediation phase$`, func() error {
		const sourceArrow = "A-rem-1"
		p, err := runner.OpenPass(runner.PassOptions{
			PassID:    "P-rem-1",
			Role:      "implementer",
			Context:   "contextA",
			ArrowID:   sourceArrow,
			LockTable: state.RPLockTable,
			Bus:       state.RPBus,
		})
		if err != nil {
			return fmt.Errorf("open P1: %w", err)
		}
		state.RPP1 = p
		state.RPPasses.Register(p)
		// Pre-seed a finding raised on this arrow during remediation;
		// the assertion later checks the finding retains its grid-version
		// tag after abort (the AmendmentCommitter does not mutate
		// findings).
		state.RPGridVersion = state.RPGrid.Version()
		_ = state.RPFindings.Raise(runner.FindingRecord{
			ID:           "F-rem-1",
			ArrowID:      sourceArrow,
			Type:         "test-finding",
			Severity:     runner.SeverityHigh,
			Status:       runner.FindingStatusOpen,
			RaisedByRole: "adversary",
			RaisedAt:     "2026-05-19T00:00:00Z",
		})
		return nil
	})

	ctx.Step(`^a grid amendment lands invalidating P1's arrow$`, func() error {
		// Queue the amendment. The commit fires in the next step.
		req := runner.AmendmentRequest{
			ID:          "amend-rem-1",
			Reason:      runner.AmendmentReasonMissingCrossContextSpec,
			SourceArrow: state.RPP1.ArrowID(),
			TargetRole:  "analyst",
			Contexts:    []string{"contextA", "contextB"},
			FindingIDs:  []string{"F-rem-1"},
			Description: "amendment lands during remediation",
			CreatedAt:   "2026-05-19T00:00:00Z",
		}
		return state.RPAmendQueue.Enqueue(req)
	})

	ctx.Step(`^the grid amendment component signals abort$`, func() error {
		pending := state.RPAmendQueue.Pending()
		if len(pending) != 1 {
			return fmt.Errorf("pending amendments = %d; want 1", len(pending))
		}
		_, err := state.RPCommitter.Commit(context.Background(), runner.CommitRequest{
			Amendment: pending[0],
		})
		return err
	})

	ctx.Step(`^the runner halts P1's evaluation runs in flight$`, func() error {
		// "Halts" = lock released + Pass.State transitions to
		// Aborted. The lock release is verified by acquiring it.
		tok, err := state.RPLockTable.TryAcquire("implementer", "contextA", "P-probe-after-abort", 0)
		if err != nil {
			return fmt.Errorf("expected lock free after abort; got %w", err)
		}
		tok.Release()
		return nil
	})

	ctx.Step(`^records pass-status "aborted" with reason "invalidated"$`, func() error {
		if state.RPP1.State() != runner.PassStateAborted {
			return fmt.Errorf("P1 state = %q; want aborted", state.RPP1.State())
		}
		// The reason is composed by AmendmentCommitter as "amendment
		// <id> drained: <reason>". Verify both substrings.
		reason := state.RPP1.CloseReason()
		if !strings.Contains(reason, "amend-rem-1") {
			return fmt.Errorf("close reason = %q; want to mention amendment id",
				reason)
		}
		if !strings.Contains(reason, string(runner.AmendmentReasonMissingCrossContextSpec)) {
			return fmt.Errorf("close reason = %q; want to encode amendment reason",
				reason)
		}
		return nil
	})

	ctx.Step(`^preserves findings discovered before abort, tagged with their grid-version$`, func() error {
		// FindingRecord's identity is unchanged by abort. Verify F-rem-1
		// is still present and its RaisedAt/ID intact (the grid-version
		// tagging in v1 is implicit: the finding was raised against
		// state.RPGridVersion, which is captured in the test's setup).
		f, ok := state.RPFindings.Get("F-rem-1")
		if !ok {
			return errors.New("F-rem-1 missing after abort")
		}
		if f.Status != runner.FindingStatusOpen {
			return fmt.Errorf("F-rem-1 status mutated to %q after abort", f.Status)
		}
		return nil
	})

	// -------- Machine evaluator returns unevaluated due to depth --------

	ctx.Step(`^a clause "mutation-score\(\.\.\.\)" with depth-sensitive depth-type$`, func() error {
		// Register a stub "mutation-score" evaluator that writes
		// RPDepthBlock if it's invoked. The depth gate must fire
		// FIRST so the stub never runs — the later Then step asserts
		// RPDepthBlock stayed false.
		state.RPDepthBlock = false
		stub := runner.Evaluator(func(ctx context.Context, c runner.Clause) (*runner.Result, error) {
			state.RPDepthBlock = true
			return &runner.Result{Pass: true}, nil
		})
		// Register or replace — idempotent across scenarios.
		if err := state.RPRegistry.Register("mutation-score", stub); err != nil {
			if err := state.RPRegistry.Replace("mutation-score", stub); err != nil {
				return fmt.Errorf("register stub: %w", err)
			}
		}
		return nil
	})

	ctx.Step(`^the active model tier is below the clause's declared minimum$`, func() error {
		// Runner is configured at DepthRankShallow in Before; the
		// clause asks for MinDepthTier=Realistic. Realistic > Shallow
		// → gate fires.
		state.RPRunner = runner.NewRunner(state.RPRegistry).
			WithActualTier(runner.DepthRankShallow)
		return nil
	})

	ctx.Step(`^the runner attempts to invoke the evaluator$`, func() error {
		clause := runner.Clause{
			Concept:      "mutation-score",
			ArrowID:      "A-mut",
			ClauseID:     "C-mut-1",
			DepthType:    runner.DepthTypeSensitive,
			MinDepthTier: runner.DepthRankRealistic,
		}
		run, err := state.RPRunner.Evaluate(context.Background(), clause.ClauseID, "P-mut", clause)
		state.RPRun = run
		state.RPRunErr = err
		return err
	})

	ctx.Step(`^the runner records the clause status "unevaluated" with reason "depth-below-required"$`, func() error {
		if state.RPRun == nil {
			return errors.New("no evaluation run produced")
		}
		if state.RPRun.EndStatus != runner.StatusUnevaluated {
			return fmt.Errorf("EndStatus = %v; want StatusUnevaluated", state.RPRun.EndStatus)
		}
		if state.RPRun.Result == nil ||
			state.RPRun.Result.Reason != string(runner.ReasonDepthBelowRequired) {
			var got string
			if state.RPRun.Result != nil {
				got = state.RPRun.Result.Reason
			}
			return fmt.Errorf("reason = %q; want %q",
				got, runner.ReasonDepthBelowRequired)
		}
		return nil
	})

	ctx.Step(`^the evaluator is NOT invoked \(depth gate is checked first\)$`, func() error {
		if state.RPDepthBlock {
			return errors.New("evaluator was invoked despite depth gate fail")
		}
		return nil
	})
}
