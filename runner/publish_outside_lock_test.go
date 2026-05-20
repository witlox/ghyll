package runner

import (
	"sync"
	"testing"
	"time"
)

// TestScenario_AttestationStore_Record_ObserverCanCallLookup verifies
// gate-2 CONC-H-3: an observer that calls back into the store
// (Lookup) must NOT deadlock. Previously the observer fanout ran
// under s.mu (write lock), so a Lookup-from-observer hit a
// sync.RWMutex re-entry deadlock.
func TestScenario_AttestationStore_Record_ObserverCanCallLookup(t *testing.T) {
	store := NewAttestationStore()
	var seen bool
	store.Observe(func(e AttestationEvent) {
		// Re-entry: observer calls back into the store.
		if _, ok := store.Lookup(e.Record.ID); ok {
			seen = true
		}
	})
	done := make(chan struct{})
	go func() {
		_ = store.Record(AttestationRecord{
			ID: "att-reenter", Kind: AttestationKindDepthType,
			ArrowID: "A", ClauseID: "C", OpID: "alice",
			AttestedByRole: "operator",
			SourceRole:     "analyst", TargetRole: "architect",
			Verdict:   AttestationPass,
			Timestamp: 1, GridVersion: 1,
			PassID: "P-1",
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deadlock: Record-observer-Lookup never returned")
	}
	if !seen {
		t.Error("observer did not see the just-recorded entry")
	}
}

// TestScenario_InsufficientBasisTracker_PublishOutsideLock verifies
// gate-2 CONC-H-1: a subscriber that calls back into the tracker
// must NOT deadlock.
func TestScenario_InsufficientBasisTracker_PublishOutsideLock(t *testing.T) {
	bus := NewOperatorBus()
	tr := NewInsufficientBasisTracker(3, bus)
	var subscriberSawCrossed bool
	bus.Subscribe(func(ev OperatorEvent) {
		if ev.Kind == OpEventInsufficientBasisRoundsExceeded {
			// Re-entry into the tracker.
			if tr.IsCrossed(ev.ClauseID) {
				subscriberSawCrossed = true
			}
		}
	})
	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				tr.Record("A1", "C1", AttestationInsufficientBasis)
			}()
		}
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deadlock: Record + IsCrossed re-entry")
	}
	if !subscriberSawCrossed {
		t.Error("subscriber didn't observe crossed state via re-entry")
	}
}
