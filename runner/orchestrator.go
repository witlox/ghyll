package runner

import (
	"context"
	"errors"
	"fmt"
)

// AdversaryFactory builds a fresh Adversary for one round. The
// existing Adversary is SINGLE-SHOT (used atomic.Bool flips on
// first Attack); the orchestrator therefore constructs one Adversary
// per round via this factory. The factory MUST configure OpenSweep
// + Classify before returning.
type AdversaryFactory func(round int) *Adversary

// AdversarialOrchestrator runs the bounded multi-round adversarial
// cycle described in gates.md §11. Each round:
//
//  1. Factory builds a fresh Adversary (single-shot rule).
//  2. Adversary.Attack runs (falsification, open sweep,
//     classification).
//  3. Findings raised this round trigger a producer-fix signal.
//  4. ProducerRemediate (if set) gets a chance to transition
//     findings (Resolved / AcceptedRisk / Invalidated).
//  5. If remediation converged (no open or running findings
//     remain at threshold severity), the orchestrator exits with
//     OutcomeRemediationConverged.
//  6. After MaxRounds rounds without convergence, the
//     orchestrator escalates and exits with
//     OutcomeRemediationEscalated.
//
// One Run per arrow per invocation. Concurrent runs against the
// same arrow are prevented by the per-(role, context) lock
// (ADR-011) at the pass level.
type AdversarialOrchestrator struct {
	// Factory constructs a fresh Adversary for each round.
	Factory AdversaryFactory

	// Findings is the shared store the Adversary writes into and
	// the orchestrator reads to detect convergence. The factory's
	// Adversary instances MUST share this store.
	Findings *FindingsStore

	// Bus optional — nil disables event publication.
	Bus *OperatorBus

	// MaxRounds bounds the loop. 0 → default 5.
	MaxRounds int

	// SeverityThreshold (0..4). Findings below this severity are
	// tracked but do not prevent convergence.
	SeverityThreshold int

	// ProducerRemediate is the hook the orchestrator calls
	// between rounds. Receives the open findings; should
	// transition them (or return an error). nil = no remediation
	// step (useful for tests).
	ProducerRemediate ProducerRemediateFn
}

// ProducerRemediateFn is the producer-fix hook. It receives the
// findings raised in the previous round; it should attempt to
// remediate by calling FindingsStore.Transition or returning an
// error to abort the cycle.
type ProducerRemediateFn func(ctx context.Context, openFindings []FindingRecord) error

// OrchestratorOutcome names the terminal state.
type OrchestratorOutcome string

const (
	OutcomeRemediationConverged OrchestratorOutcome = "converged"
	OutcomeRemediationEscalated OrchestratorOutcome = "escalated-after-max-rounds"
	OutcomeProducerError        OrchestratorOutcome = "producer-error"
	OutcomeCanceled             OrchestratorOutcome = "canceled"
)

// OrchestratorResult bundles the per-run summary.
type OrchestratorResult struct {
	Outcome   OrchestratorOutcome
	RoundsRun int
	FinalOpen []FindingRecord // findings still open at exit
	FinalErr  error
}

// Orchestrator-specific errors.
var (
	ErrOrchestratorNoFactory  = errors.New("orchestrator: nil Factory")
	ErrOrchestratorNoFindings = errors.New("orchestrator: nil Findings")
)

// Run executes the multi-round cycle for one arrow. The base
// attack fixture is reused on every round with Round incremented;
// the factory builds a fresh Adversary per round.
func (o *AdversarialOrchestrator) Run(ctx context.Context, base AdversaryAttack) (*OrchestratorResult, error) {
	if o.Factory == nil {
		return nil, ErrOrchestratorNoFactory
	}
	if o.Findings == nil {
		return nil, ErrOrchestratorNoFindings
	}
	if o.MaxRounds <= 0 {
		o.MaxRounds = 5 // defensive default
	}
	res := &OrchestratorResult{}

	for round := 1; round <= o.MaxRounds; round++ {
		if err := ctx.Err(); err != nil {
			res.Outcome = OutcomeCanceled
			res.FinalErr = err
			return res, nil
		}
		o.publish(OperatorEvent{
			Kind:    OpEventAdversarialRoundStart,
			ArrowID: base.ArrowID,
			Detail:  fmt.Sprintf("round=%d/%d", round, o.MaxRounds),
		})

		adv := o.Factory(round)
		if adv == nil {
			res.Outcome = OutcomeProducerError
			res.FinalErr = errors.New("orchestrator: Factory returned nil Adversary")
			return res, nil
		}
		attack := base
		attack.Round = round
		report, err := adv.Attack(ctx, attack)
		if err != nil {
			res.Outcome = OutcomeProducerError
			res.FinalErr = err
			return res, nil
		}
		res.RoundsRun = round

		openFindings := o.openAboveThreshold(base.ArrowID)
		if len(openFindings) == 0 && !report.RaisedThisRound() {
			res.Outcome = OutcomeRemediationConverged
			o.publish(OperatorEvent{
				Kind:    OpEventRemediationConverged,
				ArrowID: base.ArrowID,
				Detail:  fmt.Sprintf("after-rounds=%d", round),
			})
			return res, nil
		}

		o.publish(OperatorEvent{
			Kind:    OpEventProducerFixSignal,
			ArrowID: base.ArrowID,
			Detail:  fmt.Sprintf("round=%d open=%d", round, len(openFindings)),
		})

		if o.ProducerRemediate != nil {
			if err := o.ProducerRemediate(ctx, openFindings); err != nil {
				res.Outcome = OutcomeProducerError
				res.FinalErr = err
				return res, nil
			}
		}

		if remaining := o.openAboveThreshold(base.ArrowID); len(remaining) == 0 {
			res.Outcome = OutcomeRemediationConverged
			o.publish(OperatorEvent{
				Kind:    OpEventRemediationConverged,
				ArrowID: base.ArrowID,
				Detail:  fmt.Sprintf("after-rounds=%d-mid", round),
			})
			return res, nil
		}
	}

	res.Outcome = OutcomeRemediationEscalated
	res.FinalOpen = o.openAboveThreshold(base.ArrowID)
	o.publish(OperatorEvent{
		Kind:    OpEventRemediationEscalated,
		ArrowID: base.ArrowID,
		Detail:  fmt.Sprintf("max-rounds=%d open=%d", o.MaxRounds, len(res.FinalOpen)),
	})
	return res, nil
}

// openAboveThreshold returns findings on arrowID with status
// Open or Running and severity >= SeverityThreshold.
func (o *AdversarialOrchestrator) openAboveThreshold(arrowID string) []FindingRecord {
	if o.Findings == nil {
		return nil
	}
	var out []FindingRecord
	for _, f := range o.Findings.ForArrow(arrowID) {
		if (f.Status == FindingStatusOpen || f.Status == FindingStatusRunning) && int(f.Severity) >= o.SeverityThreshold {
			out = append(out, f)
		}
	}
	return out
}

// publish emits an OperatorEvent if the orchestrator has a bus
// wired up.
func (o *AdversarialOrchestrator) publish(e OperatorEvent) {
	if o.Bus != nil {
		o.Bus.Publish(e)
	}
}
