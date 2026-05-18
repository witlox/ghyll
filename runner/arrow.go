package runner

import (
	"fmt"
)

// Arrow status derivation. Per gates.md §7.2: an arrow's status is
// DERIVED from its clauses and findings, never set directly. The
// runner's DeriveArrowStatus is the canonical implementation;
// callers must not assign ArrowStatus to records — they read it
// from this function's output.
//
// Precedence (highest-blocking first):
//
//   1. Any clause status fail OR any open finding above severity
//      threshold → ArrowStatusBlocked.
//   2. Any clause status unevaluated → ArrowStatusUnevaluated.
//      (Unevaluated trumps provisional: an undecidable clause means
//      the arrow's gate state is unknowable, not "tentatively pass".)
//   3. Any clause status awaiting-attestation → ArrowStatusProvisional.
//      (Some clauses pending operator verdict; not yet decidable but
//      no failure observed.)
//   4. Any clause status pending or running → ArrowStatusInProgress.
//   5. All clauses pass → ArrowStatusComplete.

// ArrowStatus is the derived state of an arrow's exit gate.
type ArrowStatus int

const (
	// ArrowStatusInProgress: at least one clause is still pending
	// or running. Not yet decidable.
	ArrowStatusInProgress ArrowStatus = iota

	// ArrowStatusComplete: every clause is pass; no blocking
	// findings. The arrow satisfies the next role's input.
	ArrowStatusComplete

	// ArrowStatusBlocked: at least one clause failed OR there's an
	// open finding above the severity threshold. The arrow does
	// NOT satisfy the next role's input until remediation.
	ArrowStatusBlocked

	// ArrowStatusUnevaluated: at least one clause is unevaluated
	// (depth-below-required, no-rule-selectable-locations,
	// evaluator-timeout, etc.). The arrow's gate state is
	// unknowable; the next role's transition is refused.
	ArrowStatusUnevaluated

	// ArrowStatusProvisional: some clauses await attestation but
	// none has failed. Operator verdict pending; the next role's
	// transition is refused until resolution.
	ArrowStatusProvisional

	// ArrowStatusInvalidated: the grid amendment component has
	// invalidated this arrow. Set externally (not derived from
	// clause status); DeriveArrowStatus never returns this — it
	// is a sticky state the runner reads from durable state.
	ArrowStatusInvalidated
)

// String returns the wire form used in attestation records.
func (s ArrowStatus) String() string {
	switch s {
	case ArrowStatusInProgress:
		return "in-progress"
	case ArrowStatusComplete:
		return "complete"
	case ArrowStatusBlocked:
		return "blocked"
	case ArrowStatusUnevaluated:
		return "unevaluated"
	case ArrowStatusProvisional:
		return "provisional"
	case ArrowStatusInvalidated:
		return "invalidated"
	default:
		return fmt.Sprintf("invalid-arrow-status(%d)", int(s))
	}
}

// SatisfiesNextRole reports whether downstream roles can transition
// past this arrow. Only ArrowStatusComplete satisfies; every other
// state (including Provisional and Unevaluated) refuses transition.
func (s ArrowStatus) SatisfiesNextRole() bool {
	return s == ArrowStatusComplete
}

// FindingStatus is the lifecycle state of a per-arrow finding.
// Mirrors gates.md §7.3's finding-status enum.
type FindingStatus int

const (
	FindingStatusOpen FindingStatus = iota
	FindingStatusRunning
	FindingStatusResolved
	FindingStatusAcceptedRisk
	FindingStatusUnevaluated
)

// Finding is the minimal shape DeriveArrowStatus needs: status and
// severity rank. The full finding record (id, raised-by-clause,
// remediation-history, etc.) lives in the state-machine engine;
// this struct is just the derive-status input.
type Finding struct {
	Status       FindingStatus
	SeverityRank int // higher = stricter; 0 = info, 5 = critical
}

// ClauseDeriveInput is the per-clause input to DeriveArrowStatus.
// AwaitingAttestation is the "running, pending operator verdict"
// state for attested clauses — modeled here as a flag rather than a
// new ClauseStatus value so the runner's clause-state-machine
// transition table (validTransitions in runner.go) stays focused
// on machine-evaluator lifecycles.
type ClauseDeriveInput struct {
	Status              ClauseStatus
	AwaitingAttestation bool
}

// DeriveArrowStatus computes the arrow's status from per-clause
// derive inputs and per-finding status/severity. severityThreshold
// is the operator-declared cutoff (gates.md §7.2 + no-open-finding
// concept); findings above or equal to this rank with status open
// or running are blocking.
//
// Returns (status, blockingClauseCount). Callers attach the count
// to transition-refusal errors so the operator sees "blocked by N
// clauses" at a glance.
func DeriveArrowStatus(clauses []ClauseDeriveInput, findings []Finding, severityThreshold int) (ArrowStatus, int) {
	if len(clauses) == 0 {
		// An arrow with no clauses is degenerate; treat as
		// in-progress so the runner doesn't claim "complete" on
		// nothing. The catalogue's universal-base set (compiles,
		// lint-clean, no-todo-marker, every-step-bound) ensures
		// real arrows always have clauses; this path is defensive.
		return ArrowStatusInProgress, 0
	}

	// Precedence step 1: any clause-fail or open finding above
	// threshold → blocked.
	failCount := 0
	for _, c := range clauses {
		if c.Status == StatusFail {
			failCount++
		}
	}
	openBlockingFindings := 0
	for _, f := range findings {
		if (f.Status == FindingStatusOpen || f.Status == FindingStatusRunning) &&
			f.SeverityRank >= severityThreshold {
			openBlockingFindings++
		}
	}
	if failCount > 0 || openBlockingFindings > 0 {
		return ArrowStatusBlocked, failCount + openBlockingFindings
	}

	// Precedence step 2: any unevaluated clause → unevaluated.
	unevalCount := 0
	for _, c := range clauses {
		if c.Status == StatusUnevaluated {
			unevalCount++
		}
	}
	// Unevaluated findings also push the arrow to unevaluated (a
	// finding that the harness couldn't decide is itself a depth
	// signal).
	for _, f := range findings {
		if f.Status == FindingStatusUnevaluated {
			unevalCount++
		}
	}
	if unevalCount > 0 {
		return ArrowStatusUnevaluated, unevalCount
	}

	// Precedence step 3: any awaiting-attestation → provisional.
	awaitingCount := 0
	for _, c := range clauses {
		if c.AwaitingAttestation {
			awaitingCount++
		}
	}
	if awaitingCount > 0 {
		return ArrowStatusProvisional, awaitingCount
	}

	// Precedence step 4: any pending or running → in-progress.
	inProgressCount := 0
	for _, c := range clauses {
		if c.Status == StatusPending || c.Status == StatusRunning {
			inProgressCount++
		}
	}
	if inProgressCount > 0 {
		return ArrowStatusInProgress, inProgressCount
	}

	// Precedence step 5: all pass → complete.
	return ArrowStatusComplete, 0
}
