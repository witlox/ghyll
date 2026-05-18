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
//   1. Any clause status fail OR any open finding at-or-above
//      severity threshold (validation-pass-3 F6 pins inclusive
//      semantics) → ArrowStatusBlocked.
//   2. Any clause status unevaluated → ArrowStatusUnevaluated.
//      (Unevaluated trumps provisional: an undecidable clause means
//      the arrow's gate state is unknowable, not "tentatively
//      pass".)
//   3. All non-attested clauses pass AND at least one attested
//      clause is awaiting-attestation or insufficient-basis
//      → ArrowStatusProvisional. (Validation-pass-3 F4: spec demands
//      the all-evaluated-clauses-pass precondition. F5: insufficient-
//      basis is a peer of awaiting-attestation per gates.md §7.2 + §10.)
//   4. Any clause status pending or running (not attested) →
//      ArrowStatusInProgress. (Internal-only state; not in the
//      spec's persisted 5-status set — validation-pass-3 F24.)
//   5. All clauses pass → ArrowStatusComplete.
//
// THREAD SAFETY: DeriveArrowStatus does NOT copy its inputs. Callers
// must not mutate the clauses or findings slices concurrently with
// the call (validation-pass-3 F23).

// ArrowStatus is the derived state of an arrow's exit gate.
type ArrowStatus int

const (
	// ArrowStatusUnset is the zero value — a defensive sentinel
	// signalling "no status assigned" (e.g., uninitialized struct
	// field). DeriveArrowStatus never returns this; if a caller
	// observes it in a record, the record is corrupt
	// (validation-pass-3 F24).
	ArrowStatusUnset ArrowStatus = iota

	// ArrowStatusInProgress: at least one clause is still pending
	// or running (and not awaiting attestation). Runner-INTERNAL
	// state — not in gates.md §7.2's persisted status set. Should
	// not appear in attestation records; the runner sees it
	// transiently before clauses reach a terminal state.
	ArrowStatusInProgress

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

	// ArrowStatusProvisional: every evaluated clause is pass AND
	// at least one attested clause is awaiting-attestation or
	// insufficient-basis. Operator verdict pending; the next
	// role's transition is refused until resolution.
	ArrowStatusProvisional

	// ArrowStatusInvalidated: the grid amendment component has
	// invalidated this arrow. Set externally (not derived from
	// clause status); DeriveArrowStatus never returns this — it
	// is a sticky state the runner reads from durable state.
	ArrowStatusInvalidated
)

// IsPersisted reports whether the status is one of gates.md §7.2's
// persistable values. Used by attestation writers to refuse to log
// the runner-internal Unset/InProgress states.
func (s ArrowStatus) IsPersisted() bool {
	switch s {
	case ArrowStatusComplete, ArrowStatusBlocked, ArrowStatusUnevaluated,
		ArrowStatusProvisional, ArrowStatusInvalidated:
		return true
	}
	return false
}

// String returns the wire form used in attestation records.
func (s ArrowStatus) String() string {
	switch s {
	case ArrowStatusUnset:
		return "unset"
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
type FindingStatus int

const (
	FindingStatusOpen FindingStatus = iota
	FindingStatusRunning
	FindingStatusResolved
	FindingStatusAcceptedRisk
	FindingStatusUnevaluated
)

// Severity rank constants per gates.md §7.3 (5-value enum, ranks
// 0..4). Used by ThresholdRank checks. Validation-pass-3 F17.
const (
	SeverityInfo     = 0
	SeverityLow      = 1
	SeverityMedium   = 2
	SeverityHigh     = 3
	SeverityCritical = 4
)

// Finding is the minimal shape DeriveArrowStatus needs.
//
// SeverityRank is meaningful only when Status is NOT
// FindingStatusUnevaluated (a finding whose severity itself is
// unevaluated propagates regardless of rank per gates.md §7.3 —
// validation-pass-3 F25).
type Finding struct {
	Status       FindingStatus
	SeverityRank int // 0=info..4=critical per the constants above.
}

// ClauseDeriveInput is the per-clause input to DeriveArrowStatus.
//
// AwaitingAttestation and InsufficientBasis are the two peer markers
// that drive an attested clause toward ArrowStatusProvisional. Only
// valid when Status == StatusRunning (gates.md §7.1: attested-
// awaiting transitions from running). Per validation-pass-3 F7.
type ClauseDeriveInput struct {
	Status              ClauseStatus
	AwaitingAttestation bool // operator verdict pending
	InsufficientBasis   bool // operator returned insufficient-basis; awaiting more depth
}

// IsAttestedPending reports whether the clause is currently
// awaiting attestation OR insufficient-basis. Both flag a clause
// as in-flight on the attestation pathway.
func (c ClauseDeriveInput) IsAttestedPending() bool {
	return c.AwaitingAttestation || c.InsufficientBasis
}

// DeriveArrowStatus computes the arrow's status from per-clause
// derive inputs and per-finding status/severity.
//
// severityThreshold is the operator-declared cutoff (per
// gates.md §7.3 + no-open-finding concept). Threshold semantics:
// INCLUSIVE (≥) — a finding at exactly the threshold blocks
// (validation-pass-3 F6).
//
// Returns (status, blockingClauses, blockingFindings). The split
// (validation-pass-3 F22) clarifies which axis is blocking — a
// fail-count of 2 with 3 open findings produces (2, 3) rather than
// a conflated 5.
//
// Out-of-range ClauseStatus values are treated as unevaluated
// (validation-pass-3 F8) — corruption should not silently pass.
//
// AwaitingAttestation / InsufficientBasis are honored only when
// Status == StatusRunning (F7); other combinations are
// reinterpreted as if the flag were false.
func DeriveArrowStatus(clauses []ClauseDeriveInput, findings []Finding, severityThreshold int) (ArrowStatus, int, int) {
	if len(clauses) == 0 {
		// An arrow with no clauses is degenerate; treat as
		// in-progress so the runner doesn't claim "complete" on
		// nothing. The catalogue's universal-base set ensures
		// real arrows always have clauses; this path is defensive.
		return ArrowStatusInProgress, 0, 0
	}

	// Normalize: treat out-of-range Status as unevaluated, and
	// invalid (AwaitingAttestation|InsufficientBasis with Status
	// != StatusRunning) combinations as plain Status.
	normalized := make([]ClauseDeriveInput, len(clauses))
	for i, c := range clauses {
		if !isKnownClauseStatus(c.Status) {
			c.Status = StatusUnevaluated
			c.AwaitingAttestation = false
			c.InsufficientBasis = false
		} else if c.Status != StatusRunning {
			c.AwaitingAttestation = false
			c.InsufficientBasis = false
		}
		normalized[i] = c
	}

	// Precedence step 1: any clause-fail or open finding at-or-above
	// threshold → blocked.
	failCount := 0
	for _, c := range normalized {
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
		return ArrowStatusBlocked, failCount, openBlockingFindings
	}

	// Precedence step 2: any unevaluated clause OR unevaluated
	// finding → unevaluated.
	unevalClauses := 0
	for _, c := range normalized {
		if c.Status == StatusUnevaluated {
			unevalClauses++
		}
	}
	unevalFindings := 0
	for _, f := range findings {
		if f.Status == FindingStatusUnevaluated {
			unevalFindings++
		}
	}
	if unevalClauses > 0 || unevalFindings > 0 {
		return ArrowStatusUnevaluated, unevalClauses, unevalFindings
	}

	// Precedence step 3: provisional requires
	//   (a) every non-attested clause is StatusPass, AND
	//   (b) at least one attested clause is awaiting/insufficient.
	// Validation-pass-3 F4.
	awaitingCount := 0
	nonAttestedAllPass := true
	for _, c := range normalized {
		if c.IsAttestedPending() {
			awaitingCount++
			continue
		}
		if c.Status != StatusPass {
			nonAttestedAllPass = false
		}
	}
	if awaitingCount > 0 && nonAttestedAllPass {
		return ArrowStatusProvisional, awaitingCount, 0
	}

	// Precedence step 4: any pending/running (not awaiting) →
	// in-progress.
	inProgressCount := 0
	for _, c := range normalized {
		if c.IsAttestedPending() {
			continue
		}
		if c.Status == StatusPending || c.Status == StatusRunning {
			inProgressCount++
		}
	}
	if inProgressCount > 0 {
		return ArrowStatusInProgress, inProgressCount, 0
	}

	// Precedence step 5: all pass → complete.
	return ArrowStatusComplete, 0, 0
}

// isKnownClauseStatus reports whether s is a recognized
// ClauseStatus value. Out-of-range values are corruption signals
// per validation-pass-3 F8.
func isKnownClauseStatus(s ClauseStatus) bool {
	switch s {
	case StatusPending, StatusRunning, StatusPass, StatusFail, StatusUnevaluated:
		return true
	}
	return false
}
