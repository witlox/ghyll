package runner

import (
	"sync"
)

// InsufficientBasisTracker counts consecutive `insufficient-basis`
// operator verdicts per clause. When the count for a clause reaches
// the configured Max, an OpEventInsufficientBasisRoundsExceeded
// event is published on the OperatorBus so the operator surface
// can escalate (typically: route the clause to a deeper-tier model
// or to manual review).
//
// The tracker is per-clause; the Max threshold is loaded at init
// time from the grid's `insufficient-basis-rounds-max` setting
// (bootstrap.GridFile, default 3). Counts reset on any non-
// insufficient-basis verdict (pass / fail) for the same clause —
// the spec measures *consecutive* insufficient-basis rounds, not
// cumulative.
type InsufficientBasisTracker struct {
	mu     sync.Mutex
	counts map[string]int
	// crossedClauses is the Tier 2 sticky-crossed set (gate-1
	// F-7): once a clause's consecutive count crosses max, every
	// subsequent insufficient-basis Record re-emits the
	// escalation event regardless of count value. Cleared via
	// Reset(clauseID).
	crossedClauses map[string]struct{}
	max            int
	bus            *OperatorBus
}

// NewInsufficientBasisTracker constructs a tracker bound to a
// max-rounds threshold. max <= 0 disables escalation (the tracker
// still counts but never publishes). The bus is optional; nil =
// silent.
func NewInsufficientBasisTracker(max int, bus *OperatorBus) *InsufficientBasisTracker {
	return &InsufficientBasisTracker{
		counts:         make(map[string]int),
		crossedClauses: make(map[string]struct{}),
		max:            max,
		bus:            bus,
	}
}

// Max returns the configured threshold.
func (t *InsufficientBasisTracker) Max() int { return t.max }

// Record records one operator verdict for a clause. Returns the
// new round count and whether the threshold was crossed by this
// call. If the threshold was crossed, the tracker publishes
// OpEventInsufficientBasisRoundsExceeded.
//
// verdict semantics:
//
//   - AttestationInsufficientBasis: increment counter; if new count
//     equals max, fire escalation.
//   - Any other verdict (pass / fail): reset counter to 0 (the
//     consecutive streak is broken).
//
// Empty clauseID is treated as a no-op (round count zero, no
// event); callers should pass valid IDs.
func (t *InsufficientBasisTracker) Record(arrowID, clauseID string, verdict AttestationVerdict) (rounds int, crossed bool) {
	if clauseID == "" {
		return 0, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if verdict != AttestationInsufficientBasis {
		// Reset on any non-insufficient-basis verdict.
		delete(t.counts, clauseID)
		delete(t.crossedClauses, clauseID)
		return 0, false
	}
	t.counts[clauseID]++
	rounds = t.counts[clauseID]

	// Gate-1 F-7 (Tier 2): the crossed state is sticky. Once
	// rounds reaches max, every subsequent IB Record on this
	// clause re-emits the escalation event until Reset clears
	// it. The modal driver's inFlight set dedups so the operator
	// gets one prompt at a time.
	_, wasCrossed := t.crossedClauses[clauseID]
	if t.max > 0 && (rounds >= t.max || wasCrossed) {
		crossed = true
		if !wasCrossed {
			t.crossedClauses[clauseID] = struct{}{}
		}
		if t.bus != nil {
			t.bus.Publish(OperatorEvent{
				Kind:     OpEventInsufficientBasisRoundsExceeded,
				ArrowID:  arrowID,
				ClauseID: clauseID,
				Detail:   "max-rounds-reached",
			})
		}
	}
	return rounds, crossed
}

// Rounds returns the current consecutive count for a clause.
// Zero if no record exists (or the streak was reset).
func (t *InsufficientBasisTracker) Rounds(clauseID string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.counts[clauseID]
}

// Reset clears the counter AND the sticky-crossed flag for one
// clause. Called by the modal driver after the operator resolves
// the escalation (option 1 accepted-risk or option 2
// route-upstream — both dispose the clause).
func (t *InsufficientBasisTracker) Reset(clauseID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.counts, clauseID)
	delete(t.crossedClauses, clauseID)
}

// IsCrossed reports whether the clause has crossed max-rounds at
// some point and hasn't been Reset. Used by the modal driver to
// pick between PresentVerdict and PresentEscalation.
func (t *InsufficientBasisTracker) IsCrossed(clauseID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.crossedClauses[clauseID]
	return ok
}
