package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

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
}

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
	clauseInputs := make([]ClauseDeriveInput, 0, len(req.Arrow.Clauses))

	for i, clause := range req.Arrow.Clauses {
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
			if input.AwaitingAttestation && d.Bus != nil {
				d.Bus.Publish(OperatorEvent{
					Kind:     OpEventAttestationRequested,
					ArrowID:  req.Arrow.ID,
					PassID:   passID,
					ClauseID: clauseID,
					Detail:   "att-ref=" + clause.DepthTypeAttestationRef,
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
