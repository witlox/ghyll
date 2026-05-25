// Adversarial-cycle driver wired into PassDispatcher.AdversarialPhase
// (diamond v4 / Gap 1, ADR-v4-007).
//
// Per the v2 implementation contract (specs/v4/diamond-load-bearing-
// revised-v2.md §"Dispatcher integration pseudocode"), the runner's
// PassDispatcher consults a swap-clean hook bundle to drive the §11
// adversarial cycle on depth-sensitive arrows. The dispatcher itself
// stays in `runner/`; the cycle's outer driver lives in `cmd/ghyll`
// because it threads together the OperatorBus (for round events) +
// the runtime FindingsStore + the harness (ProducerFixHarness).
//
// runDispatcherAdversarialPhase is the method PassDispatcher invokes
// via the AdversarialPhaseFn field. Returns (report, verifyClauses,
// err). A non-converged report tells the dispatcher to early-return
// with ArrowStatusAbortedRemediation (R23 closure). A converged
// report carries the robust + auto-inserted clauses the dispatcher
// then verifies (M7 closure).

package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/witlox/ghyll/runner"
)

// runDispatcherAdversarialPhase is the dispatcher-injected helper
// that drives the §11 cycle for one depth-sensitive arrow.
//
// Inputs:
//   - ctx       — already incremented by the dispatcher's recursion budget.
//   - req       — pointer so the AdversaryRole stamp lands on req.AdversaryRole (R13).
//   - passID    — the dispatcher's freshly-minted pass identifier.
//   - sensitive — the depth-sensitive partition (PartitionClauses output).
//
// Outputs:
//   - report        — *RemediationReport (Outcome ∈ converged | escalated-* | context-cancelled).
//   - verifyClauses — clauses for the dispatcher's post-cycle verification loop.
//     On converged: robust + verification auto-inserts.
//     On non-converged: returned unchanged; the dispatcher early-returns.
//   - err           — non-nil only on hook plumbing failure (e.g., builder returns nil).
func (r *engineRuntime) runDispatcherAdversarialPhase(
	ctx context.Context,
	req *runner.DispatchRequest,
	passID string,
	sensitive []runner.Clause,
	hooks *runner.AdversarialHooks,
) (*runner.RemediationReport, []runner.Clause, error) {
	if r == nil {
		return nil, nil, errors.New("dispatcher-adversarial: nil runtime")
	}
	// Integrator-pass I-H-3 closure: the dispatcher passes its
	// already-loaded hooks snapshot rather than have us re-load
	// from r.adversarialHooks. A concurrent `/adversary disable`
	// between the dispatcher gate and this call no longer races —
	// we drive the cycle through the validated snapshot regardless.
	// Defensive fallback: if the caller passed nil, fall back to a
	// fresh Load so direct test paths still work.
	if hooks == nil {
		hooks = r.adversarialHooks.Load()
	}
	if hooks == nil || !hooks.Validate() {
		return nil, nil, runner.ErrAdversaryHooksNotWired
	}

	// Diamond v4 / W-H-4 closure: early Factory probe. The
	// AdversaryBuilder closure below assumes the Factory returns a
	// non-nil Adversary AND that this codepath fills in the required
	// pointers (FindingsStore / ClassificationsStore / Runner). A
	// future refactor that moves the per-round init pattern to a
	// different lifecycle, or an operator-supplied bundle whose
	// Factory pre-assigns these to wrong values, must surface here
	// rather than silently inside RunRemediationLoop. Validate this
	// invariant by probing Factory(0) + checking the wrapper's
	// post-condition holds.
	if err := r.validateAdversaryFactoryContract(hooks, req); err != nil {
		return nil, nil, err
	}

	// Surface a round-start event so the modal driver + operator
	// UI can render adversarial progress inline. M9 closure: the
	// tier_label carries the cycle's depth context.
	if r.bus != nil {
		r.bus.Publish(runner.OperatorEvent{
			Kind:    runner.OpEventAdversarialRoundStart,
			ArrowID: req.Arrow.ID,
			PassID:  passID,
			Detail:  fmt.Sprintf("sensitive_clauses=%d tier=%d", len(sensitive), int(req.ActualTier)),
			Payload: map[string]string{
				"tier_label":        fmt.Sprintf("%d", int(req.ActualTier)),
				"sensitive_clauses": fmt.Sprintf("%d", len(sensitive)),
			},
		})
	}

	harness := &runner.ProducerFixHarness{
		Producer: hooks.ProducerFix,
		Bus:      r.bus,
		ArrowID:  req.Arrow.ID,
	}
	cfg := hooks.RemediationConfigDefaults
	cfg.AdversaryBuilder = func(round int) *runner.Adversary {
		a := hooks.Factory(round)
		if a == nil {
			return nil
		}
		// Inject the runtime stores so per-round Attack reports
		// land on the live runtime (M3 closure). Also build a
		// fresh Runner per round so the clause-falsification step
		// has an evaluator (Attack refuses on nil Runner).
		a.FindingsStore = r.findings
		a.ClassificationsStore = r.classifications
		if a.Runner == nil {
			a.Runner = r.NewRunner(req.ActualTier)
		}
		a.OpenSweep = hooks.OpenSweep
		a.Classify = hooks.Classify
		// R13 stamp — propagates the adversary role onto
		// OpEventAttestationRequested via DispatchRequest.
		req.AdversaryRole = a.AdversaryRole
		return a
	}
	cfg.AttackBuilder = func(round int) runner.AdversaryAttack {
		return runner.AdversaryAttack{
			ArrowID:      req.Arrow.ID,
			PassID:       passID,
			ProjectDir:   req.ProjectDir,
			DepthClauses: sensitive,
			Requirements: req.Arrow.Requirements,
			Round:        round,
		}
	}
	cfg.FixAttempt = func(ctx context.Context, openFindings []runner.FindingRecord) (bool, error) {
		// Pump the harness; loop-bomb surfaces as ProducerLoopBomb
		// (loop terminates with RemediationEscalatedHookError after
		// MaxFixErrors). Returning err == nil + madeProgress == true
		// for the legit-fix path is what the loop expects.
		err := harness.ProducerRemediate()(ctx, openFindings)
		if err != nil {
			if errors.Is(err, runner.ErrProducerLoopBomb) {
				return false, err
			}
			return false, err
		}
		return true, nil
	}

	report, runErr := runner.RunRemediationLoop(ctx, cfg)
	if runErr != nil && report == nil {
		return nil, nil, runErr
	}
	if report == nil {
		// Defensive: loop returned (nil, nil) means a builder
		// produced an unrecoverable error inside the loop.
		return nil, nil, errors.New("dispatcher-adversarial: nil remediation report")
	}

	// Surface the terminal outcome so subscribers (modal driver,
	// operator UI) render the cycle conclusion.
	if r.bus != nil {
		kind := runner.OpEventRemediationEscalated
		if remediationConvergedDriver(report.Outcome) {
			kind = runner.OpEventRemediationConverged
		}
		r.bus.Publish(runner.OperatorEvent{
			Kind:    kind,
			ArrowID: req.Arrow.ID,
			PassID:  passID,
			Detail: fmt.Sprintf("outcome=%s rounds_executed=%d",
				string(report.Outcome), report.RoundsExecuted),
			Payload: map[string]string{
				"outcome":         string(report.Outcome),
				"rounds_executed": fmt.Sprintf("%d", report.RoundsExecuted),
			},
		})
	}

	if !remediationConvergedDriver(report.Outcome) {
		return report, nil, nil
	}

	// Converged: hand the dispatcher the verification clause set
	// (robust + auto-inserts per M7 closure).
	_, robust := runner.PartitionClauses(req.Arrow.Clauses)
	verifyClauses := runner.VerificationAutoInsert(req.Arrow.ID, robust)
	return report, verifyClauses, nil
}

// remediationConvergedDriver mirrors runner.remediationConverged
// (which is package-private). Kept private to cmd/ghyll so the wire
// here owns its own predicate; both are kept in sync as the
// converged-vocabulary expands.
func remediationConvergedDriver(o runner.RemediationOutcome) bool {
	return o == runner.RemediationConverged ||
		o == runner.RemediationConvergedWithUnevaluated
}

// ErrAdversaryFactoryContract is returned by validateAdversaryFactoryContract
// when the bundle's Factory contradicts the per-round wrapper's
// assumption (returns nil; doesn't accept the wrapper's pointer
// injection). Surfaces as a typed dispatch error so operators see
// "bundle malformed" rather than a panic deep inside Attack.
var ErrAdversaryFactoryContract = errors.New("dispatcher-adversarial: adversary-factory-contract-violation")

// validateAdversaryFactoryContract probes Factory(0), runs the per-
// round injection wrapper against the result, and asserts the
// Adversary's required pointers end up non-nil after injection. The
// probe Adversary is discarded — it has not been Attacked, so the
// single-shot used flag stays clean for the real per-round Adversary
// instances built by RunRemediationLoop.
func (r *engineRuntime) validateAdversaryFactoryContract(
	hooks *runner.AdversarialHooks, req *runner.DispatchRequest,
) error {
	probe := hooks.Factory(0)
	if probe == nil {
		return fmt.Errorf("%w: Factory(0) returned nil", ErrAdversaryFactoryContract)
	}
	// Apply the same wrapper logic runDispatcherAdversarialPhase
	// uses per-round. If after injection any required pointer is
	// still nil, the contract is broken.
	if probe.FindingsStore == nil {
		probe.FindingsStore = r.findings
	}
	if probe.ClassificationsStore == nil {
		probe.ClassificationsStore = r.classifications
	}
	if probe.Runner == nil {
		probe.Runner = r.NewRunner(req.ActualTier)
	}
	if probe.FindingsStore == nil {
		return fmt.Errorf("%w: FindingsStore still nil after wrapper injection",
			ErrAdversaryFactoryContract)
	}
	if probe.ClassificationsStore == nil {
		return fmt.Errorf("%w: ClassificationsStore still nil after wrapper injection",
			ErrAdversaryFactoryContract)
	}
	if probe.Runner == nil {
		return fmt.Errorf("%w: Runner still nil after wrapper injection",
			ErrAdversaryFactoryContract)
	}
	return nil
}
