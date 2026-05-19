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
	max    int
	bus    *OperatorBus
}

// NewInsufficientBasisTracker constructs a tracker bound to a
// max-rounds threshold. max <= 0 disables escalation (the tracker
// still counts but never publishes). The bus is optional; nil =
// silent.
func NewInsufficientBasisTracker(max int, bus *OperatorBus) *InsufficientBasisTracker {
	return &InsufficientBasisTracker{
		counts: make(map[string]int),
		max:    max,
		bus:    bus,
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
		return 0, false
	}
	t.counts[clauseID]++
	rounds = t.counts[clauseID]
	if t.max > 0 && rounds == t.max {
		crossed = true
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

// Reset clears the counter for one clause. Useful when the
// operator manually decides to retry the clause from scratch.
func (t *InsufficientBasisTracker) Reset(clauseID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.counts, clauseID)
}
