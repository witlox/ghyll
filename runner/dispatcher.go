package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Hint carries the dispatcher-synthesized payload presented to
// the operator inside the verdict modal. Tier 2 minimal shape
// (ADR-016 Part G). Tier 3 may add Locations / Basis / Residue
// from a producer-side hook.
type Hint struct {
	ArrowID        string `json:"arrow_id"`
	ClauseID       string `json:"clause_id"`
	Concept        string `json:"concept"`
	AttestationRef string `json:"attestation_ref"`
}

// SynthesizeHint returns the Tier 2 minimal hint for a clause
// (ADR-016 Part G). The dispatcher embeds the JSON serialization
// in OpEventAttestationRequested's Detail field; the modal driver
// deserializes when presenting.
func SynthesizeHint(c Clause) Hint {
	return Hint{
		ArrowID:        c.ArrowID,
		ClauseID:       c.ClauseID,
		Concept:        c.Concept,
		AttestationRef: c.DepthTypeAttestationRef,
	}
}

// PassDispatcher drives the end-to-end execution of one arrow's
// clauses inside a Pass. It is the production caller of
// Runner.Evaluate — the only path through which clauses fire in a
// live ghyll session.
//
// The dispatcher's job per ADR-011:
//
//  1. Open a Pass via OpenPass — acquires the per-(role, context)
//     lock from the RoleContextLockTable. Returns
//     *ErrRoleContextBusy if another pass already holds the tuple.
//  2. Register the Pass with the PassRegistry so the engine
//     status CLI surfaces it.
//  3. Construct a Runner at the right depth tier (the arrow's
//     max-tier requirement per gates.md §8).
//  4. Iterate the arrow's clauses, calling Runner.Evaluate per
//     clause with a STABLE passID. The runner records each
//     EvaluationRun; the journal observer persists it.
//  5. Derive the arrow's status via DeriveArrowStatus.
//  6. Close (or Abort) the Pass — releases the lock and
//     unregisters.
//
// Concurrency: one Dispatch call per arrow per session. Two
// concurrent Dispatch calls on the same (role, context) tuple are
// serialized by the lock table; the second receives
// *ErrRoleContextBusy.
type PassDispatcher struct {
	// LockTable is required. The dispatcher acquires the
	// per-(role, context) lock at Open and releases at Close.
	LockTable *RoleContextLockTable

	// Passes is the live-pass registry. The dispatcher Registers
	// the Pass at Open and Unregisters at Close.
	Passes *PassRegistry

	// Bus is the operator event bus. Optional; nil disables event
	// publication.
	Bus *OperatorBus

	// RunnerFactory builds a Runner at the requested depth tier.
	// Required. The engine.Runtime's NewRunner method satisfies
	// this signature.
	RunnerFactory func(tier DepthRank) *Runner

	// SeverityThreshold is the minimum finding severity that
	// blocks arrow convergence. Passed to DeriveArrowStatus.
	SeverityThreshold int

	// DefaultLockTTL bounds how long a Pass may hold its lock.
	// Zero = no auto-expire (interactive sessions). Recommended:
	// the session's max-turn wall-clock budget.
	DefaultLockTTL time.Duration

	// Now is the dispatcher's clock; abstracted for tests.
	// Defaults to time.Now.
	Now func() time.Time

	// PassIDGen produces a unique pass identifier per Dispatch
	// call. Required.
	PassIDGen func() string

	// AttestationStore (optional) resolves a clause's
	// DepthTypeAttestationRef into an AttestationRecord at
	// arrow-status derivation time. If a clause carries a non-
	// empty ref and the looked-up record has verdict !=
	// AttestationPass (or is missing entirely), the dispatcher
	// marks the clause as attestation-pending so
	// DeriveArrowStatus produces ArrowStatusProvisional. nil =
	// dispatcher does not inspect attestations; clauses derive
	// purely from their EndStatus.
	AttestationStore *AttestationStore

	// Hooks holds the active AdversarialHooks bundle (diamond v4 /
	// Gap 1). Nil = the dispatcher refuses any depth-sensitive
	// arrow with ErrAdversaryHooksNotWired. The atomic pointer
	// admits race-clean swap when the operator types /adversary
	// enable|disable (ADR-v4-002).
	Hooks *AtomicAdversarialHooks

	// MaxRecursiveDispatch caps nested Dispatch depth. Zero ->
	// DefaultMaxRecursiveDispatch (4). Defense-in-depth per R11.
	MaxRecursiveDispatch int

	// AdversarialPhase is the optional helper that runs the
	// adversarial + remediation cycle on depth-sensitive clauses.
	// When non-nil and Hooks is wired, Dispatch invokes it BEFORE
	// running verification clauses. Returns (report, verifyClauses,
	// err): a non-converged outcome leads Dispatch to early-return
	// with ArrowStatusAbortedRemediation per R23 closure. When nil
	// (test path / disabled), Dispatch runs vanilla verification
	// over every clause unchanged.
	AdversarialPhase AdversarialPhaseFn
}

// AdversarialPhaseFn is the dispatcher-injected adversarial cycle
// runner. cmd/ghyll provides the concrete implementation
// (runDispatcherAdversarialPhase) per ADR-v4-007. Returning a
// non-converged outcome means Dispatch must early-return with
// ArrowStatusAbortedRemediation (R23 closure).
//
// Integrator-pass I-H-3 closure: the dispatcher's snapshot of the
// AdversarialHooks bundle (loaded ONCE at the gate-check) flows
// through as `hooks` so the phase fn never re-loads from the atomic
// pointer. A concurrent `/adversary disable` between the dispatcher's
// gate and the phase invocation cannot now cause the cycle to
// observe a nil bundle mid-dispatch (the prior shape did a second
// Load() inside the phase and aborted the already-open pass with
// adversary-hooks-not-wired).
type AdversarialPhaseFn func(ctx context.Context, req *DispatchRequest, passID string, sensitive []Clause, hooks *AdversarialHooks) (report *RemediationReport, verifyClauses []Clause, err error)

// DispatchRequest is the single-arrow dispatch payload.
type DispatchRequest struct {
	// Role is the role this pass runs as (analyst / architect /
	// implementer / integrator, or a synthetic role-id like
	// `init` / `adversary`).
	Role string

	// Context is the bounded-context-id. (role, context) is the
	// single-active-role-instance key.
	Context string

	// Arrow is the arrow definition this pass traverses. Its
	// clauses are evaluated in declared order.
	Arrow ArrowDefinition

	// ActualTier is the depth tier the runner runs at. Per
	// gates.md §8: the dispatcher resolves the arrow's max
	// MinDepthTier across clauses and picks a tier at or above
	// it. Passing DepthRankNone disables the §7.1 short-circuit
	// (test path).
	ActualTier DepthRank

	// GridVersion stamps every clause's GridVersion so persisted
	// EvaluationRun records are self-keyed to a grid generation.
	GridVersion uint64

	// ProjectDir scopes filesystem-bound evaluators
	// (no-todo-marker, etc.). Optional — falls back to the
	// clause's ProjectDir if set.
	ProjectDir string

	// AdversaryRole is non-empty when this dispatch runs inside an
	// adversary-phase pass (orchestrator's adversarial.go). The
	// dispatcher stamps it on OpEventAttestationRequested so the
	// modal driver can propagate to AttestationRecord.AdversaryRole
	// for the §12.2 3-role-chain encoding (gate-1 F-3 / gate-2
	// CORR-A-5). Empty for vanilla passes.
	AdversaryRole string
}

// DispatchResult bundles the per-pass summary.
type DispatchResult struct {
	PassID           string
	Runs             []*EvaluationRun
	ArrowStatus      ArrowStatus
	BlockingClauses  int
	BlockingFindings int
	ClosedAt         time.Time
	CloseReason      string
}

// Dispatcher errors.
var (
	ErrDispatcherNoLockTable = errors.New("dispatcher: nil LockTable")
	ErrDispatcherNoPasses    = errors.New("dispatcher: nil Passes")
	ErrDispatcherNoFactory   = errors.New("dispatcher: nil RunnerFactory")
	ErrDispatcherNoPassIDGen = errors.New("dispatcher: nil PassIDGen")
	ErrDispatcherClauseEval  = errors.New("dispatcher: clause evaluation failed")
)

// Dispatch opens a Pass on req.Role / req.Context / req.Arrow.ID,
// runs every clause on req.Arrow.Clauses through a Runner at
// req.ActualTier, derives the arrow status, and closes the Pass.
//
// Returns:
//   - *ErrRoleContextBusy if the (role, context) tuple is held by
//     another pass.
//   - ErrDispatcher* sentinels if the dispatcher is misconfigured.
//   - ErrDispatcherClauseEval wrapping the underlying error if any
//     clause's Evaluate call returned an error. The Pass is Aborted
//     in that case; the lock is released.
//
// On success, all clauses returned without error (which does NOT
// mean they all passed — a clause may have returned a Result with
// Pass=false; that's normal). The derived ArrowStatus encodes the
// per-clause outcomes.
func (d *PassDispatcher) Dispatch(ctx context.Context, req DispatchRequest) (*DispatchResult, error) {
	if err := d.validate(); err != nil {
		return nil, err
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}

	// Diamond v4 / Gap 1 (R6 + R11): pre-flight checks BEFORE the
	// pass counter spins. A refused dispatch must not produce a
	// pass row, must not mutate the registry, must not appear in
	// the operator-facing pass history.
	//
	// Audit-floor check: production paths attach the JSONL writer
	// via SubscribeTagged(_, "audit"); if no such subscriber is
	// present, the dispatch's lifecycle would not be persisted.
	// Refuse rather than silently drop. Nil bus = skipped (test path).
	if err := RequireAuditSubscriber(d.Bus); err != nil {
		return nil, err
	}
	// Recursion-budget check (defense-in-depth per R11): refuses
	// re-entrant dispatch beyond MaxRecursiveDispatch hops.
	budget := d.MaxRecursiveDispatch
	if budget <= 0 {
		budget = DefaultMaxRecursiveDispatch
	}
	if err := CheckRecursionBudget(ctx, budget); err != nil {
		return nil, err
	}

	// Adversarial cycle gating: partition the clause set so we can
	// short-circuit the depth-robust-only path. R14 + R23 closure.
	sensitive, _ := PartitionClauses(req.Arrow.Clauses)
	runCycle := len(sensitive) > 0 && req.Role != "init" && req.Role != "adversary"
	var loadedHooks *AdversarialHooks
	if d.Hooks != nil {
		loadedHooks = d.Hooks.Load()
	}
	if runCycle && d.AdversarialPhase != nil {
		// Hooks bundle missing -> refuse BEFORE pass counter spins.
		if loadedHooks == nil || !loadedHooks.Validate() {
			return nil, ErrAdversaryHooksNotWired
		}
	}

	passID := d.PassIDGen()
	pass, err := OpenPass(PassOptions{
		PassID:      passID,
		Role:        req.Role,
		Context:     req.Context,
		ArrowID:     req.Arrow.ID,
		GridVersion: req.GridVersion, // H-5 (G2-I-4): stamp grid_version on the engine row
		LockTable:   d.LockTable,
		LockTTL:     d.DefaultLockTTL,
		Bus:         d.Bus,
		Now:         now,
	})
	if err != nil {
		// Pass through *ErrRoleContextBusy and Open-validation
		// errors untransformed — callers switch on them. Return
		// BEFORE Register/defer so a failed OpenPass doesn't leave
		// a nil pass in the registry or panic in defer Unregister.
		return nil, err
	}
	d.Passes.Register(pass)
	defer d.Passes.Unregister(pass.ID())

	rn := d.RunnerFactory(req.ActualTier)
	if rn == nil {
		pass.Abort("runner-factory-returned-nil")
		return nil, errors.New("dispatcher: RunnerFactory returned nil Runner")
	}

	res := &DispatchResult{PassID: passID}

	// Diamond v4 / Gap 1 (R23 + R13): run the adversarial cycle on
	// depth-sensitive clauses BEFORE verification. Returns
	// (report, verifyClauses, err). A non-converged outcome
	// early-returns with ArrowStatusAbortedRemediation, bypassing
	// DeriveArrowStatus BY DESIGN.
	verifyClauses := req.Arrow.Clauses
	if runCycle && d.AdversarialPhase != nil {
		ctx = IncrementRecursionDepth(ctx)
		// I-H-3 closure: pass the already-loaded hooks snapshot so
		// the phase fn never re-loads from the atomic pointer. The
		// snapshot was validated by the gate-check above; the phase
		// uses it directly.
		report, vc, advErr := d.AdversarialPhase(ctx, &req, passID, sensitive, loadedHooks)
		if advErr != nil {
			pass.Abort(fmt.Sprintf("adversarial-phase: %v", advErr))
			return res, advErr
		}
		if report != nil && !remediationConverged(report.Outcome) {
			reason := fmt.Sprintf("aborted-remediation:%s", string(report.Outcome))
			pass.Abort(reason)
			res.ArrowStatus = ArrowStatusAbortedRemediation
			res.ClosedAt = pass.ClosedAt()
			res.CloseReason = pass.CloseReason()
			return res, nil
		}
		if len(vc) > 0 {
			verifyClauses = vc
		}
	}

	clauseInputs := make([]ClauseDeriveInput, 0, len(verifyClauses))

	for i, clause := range verifyClauses {
		if err := ctx.Err(); err != nil {
			pass.Abort(fmt.Sprintf("context-cancelled: %v", err))
			return nil, err
		}
		// Stamp the clause with arrow metadata so persisted
		// EvaluationRun rows carry self-keyed identity.
		clause.ArrowID = req.Arrow.ID
		clause.GridVersion = req.GridVersion
		clause.PassID = passID
		if clause.ProjectDir == "" {
			clause.ProjectDir = req.ProjectDir
		}
		clauseID := clause.ClauseID
		if strings.TrimSpace(clauseID) == "" {
			clauseID = fmt.Sprintf("%s-c%d", req.Arrow.ID, i)
			clause.ClauseID = clauseID
		}

		run, err := rn.Evaluate(ctx, clauseID, passID, clause)
		if err != nil {
			pass.Abort(fmt.Sprintf("clause %s: %v", clauseID, err))
			return res, fmt.Errorf("%w: clause %s: %w",
				ErrDispatcherClauseEval, clauseID, err)
		}
		res.Runs = append(res.Runs, run)
		input := ClauseDeriveInput{Status: run.EndStatus}
		// If the clause references a depth-type attestation and the
		// store is wired, resolve the verdict. Per ADR-010 the
		// dispatcher (not the Lookup callee) decides what the
		// verdict means for the arrow's status.
		if d.AttestationStore != nil && clause.DepthTypeAttestationRef != "" && run.EndStatus == StatusRunning {
			rec, ok := d.AttestationStore.Lookup(clause.DepthTypeAttestationRef)
			switch {
			case !ok:
				// Record not yet recorded — operator hasn't acted.
				input.AwaitingAttestation = true
			case rec.Verdict == AttestationInsufficientBasis:
				input.InsufficientBasis = true
			case rec.Verdict != AttestationPass:
				// Fail / unknown — clause does not move forward;
				// leave Status as Running so DeriveArrowStatus
				// treats it as attestation-pending blocking.
				input.AwaitingAttestation = true
			}
			// M-3 (G2-F-11): publish OpEventAttestationRequested when
			// the dispatcher flips AwaitingAttestation on. Live
			// subscribers (operator UI, future status CLI) see the
			// hint. Crash recovery's republish uses a distinct kind
			// (OpEventRecoveryAttestationRepublished) so the two
			// surfaces are distinguishable.
			//
			// Tier 2 (ADR-016 Part G): the Detail field carries the
			// minimal hint as JSON so the modal driver can
			// deserialize without an out-of-band lookup.
			if input.AwaitingAttestation && d.Bus != nil {
				hint := SynthesizeHint(clause)
				hintJSON, _ := json.Marshal(hint)
				// Gate-2 CORR-A-5/A-13: stamp Role + Context +
				// AdversaryRole + GridVersion on the event payload
				// so the modal driver can construct the
				// AttestationRecord without re-resolving against
				// the live grid (which may have shifted since the
				// pass started).
				payload := map[string]string{
					"source_role":  req.Role,
					"target_role":  req.Arrow.TargetRole,
					"context":      req.Context,
					"stratum":      req.Arrow.Stratum,
					"grid_version": fmt.Sprintf("%d", req.GridVersion),
				}
				if req.AdversaryRole != "" {
					payload["adversary_role"] = req.AdversaryRole
				}
				d.Bus.Publish(OperatorEvent{
					Kind:     OpEventAttestationRequested,
					ArrowID:  req.Arrow.ID,
					PassID:   passID,
					ClauseID: clauseID,
					Role:     req.Role,
					Detail:   string(hintJSON),
					Payload:  payload,
				})
			}
		}
		clauseInputs = append(clauseInputs, input)
	}

	// Derive arrow status from per-clause endStatus values.
	arrowStatus, bClauses, bFindings := DeriveArrowStatus(
		clauseInputs, nil, d.SeverityThreshold,
	)
	res.ArrowStatus = arrowStatus
	res.BlockingClauses = bClauses
	res.BlockingFindings = bFindings

	pass.Close(fmt.Sprintf("derived-%s", arrowStatus.String()))
	res.ClosedAt = pass.ClosedAt()
	res.CloseReason = pass.CloseReason()
	return res, nil
}

// remediationConverged reports whether the adversarial cycle's
// terminal outcome is one of the "converged" family. Diamond v4 /
// R23: any other outcome means the dispatcher early-returns with
// ArrowStatusAbortedRemediation. Mirrors the loop's vocabulary in
// runner/remediation.go.
func remediationConverged(o RemediationOutcome) bool {
	return o == RemediationConverged || o == RemediationConvergedWithUnevaluated
}

func (d *PassDispatcher) validate() error {
	if d == nil {
		return errors.New("dispatcher: nil receiver")
	}
	if d.LockTable == nil {
		return ErrDispatcherNoLockTable
	}
	if d.Passes == nil {
		return ErrDispatcherNoPasses
	}
	if d.RunnerFactory == nil {
		return ErrDispatcherNoFactory
	}
	if d.PassIDGen == nil {
		return ErrDispatcherNoPassIDGen
	}
	return nil
}
