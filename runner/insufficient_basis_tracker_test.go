package runner

import (
	"testing"
)

func TestScenario_InsufficientBasisTracker_IncrementOnInsufficientBasis(t *testing.T) {
	tr := NewInsufficientBasisTracker(3, nil)
	rounds, crossed := tr.Record("A1", "C5", AttestationInsufficientBasis)
	if rounds != 1 || crossed {
		t.Fatalf("first record: rounds=%d crossed=%v; want 1 false", rounds, crossed)
	}
	rounds, crossed = tr.Record("A1", "C5", AttestationInsufficientBasis)
	if rounds != 2 || crossed {
		t.Fatalf("second record: rounds=%d crossed=%v; want 2 false", rounds, crossed)
	}
}

func TestScenario_InsufficientBasisTracker_CrossesAtMax(t *testing.T) {
	bus := NewOperatorBus()
	var events []OperatorEvent
	bus.Subscribe(func(e OperatorEvent) { events = append(events, e) })

	tr := NewInsufficientBasisTracker(3, bus)
	_, _ = tr.Record("A1", "C5", AttestationInsufficientBasis)             // 1
	_, _ = tr.Record("A1", "C5", AttestationInsufficientBasis)             // 2
	rounds, crossed := tr.Record("A1", "C5", AttestationInsufficientBasis) // 3 — crosses
	if rounds != 3 {
		t.Fatalf("rounds = %d; want 3", rounds)
	}
	if !crossed {
		t.Fatal("crossed should be true at max")
	}
	if len(events) != 1 {
		t.Fatalf("events = %d; want 1", len(events))
	}
	if events[0].Kind != OpEventInsufficientBasisRoundsExceeded {
		t.Fatalf("event kind = %q; want insufficient-basis-rounds-exceeded", events[0].Kind)
	}
	if events[0].ClauseID != "C5" {
		t.Fatalf("event ClauseID = %q; want C5", events[0].ClauseID)
	}
}

func TestScenario_InsufficientBasisTracker_PassResetsCounter(t *testing.T) {
	tr := NewInsufficientBasisTracker(3, nil)
	_, _ = tr.Record("A1", "C5", AttestationInsufficientBasis) // 1
	_, _ = tr.Record("A1", "C5", AttestationInsufficientBasis) // 2
	rounds, _ := tr.Record("A1", "C5", AttestationPass)        // reset
	if rounds != 0 {
		t.Fatalf("after pass: rounds = %d; want 0", rounds)
	}
	if tr.Rounds("C5") != 0 {
		t.Fatalf("Rounds(C5) = %d; want 0", tr.Rounds("C5"))
	}
}

func TestScenario_InsufficientBasisTracker_FailResetsCounter(t *testing.T) {
	tr := NewInsufficientBasisTracker(3, nil)
	_, _ = tr.Record("A1", "C5", AttestationInsufficientBasis)
	rounds, _ := tr.Record("A1", "C5", AttestationFail)
	if rounds != 0 {
		t.Fatalf("fail should reset; rounds = %d", rounds)
	}
}

func TestScenario_InsufficientBasisTracker_DisabledByZeroMax_NeverEscalates(t *testing.T) {
	bus := NewOperatorBus()
	var events []OperatorEvent
	bus.Subscribe(func(e OperatorEvent) { events = append(events, e) })

	tr := NewInsufficientBasisTracker(0, bus)
	for i := 0; i < 10; i++ {
		_, crossed := tr.Record("A1", "C5", AttestationInsufficientBasis)
		if crossed {
			t.Fatalf("max=0 should never fire; crossed at round %d", i+1)
		}
	}
	if len(events) != 0 {
		t.Fatalf("max=0 should never publish; got %d events", len(events))
	}
}

func TestScenario_InsufficientBasisTracker_ManualResetClearsCounter(t *testing.T) {
	tr := NewInsufficientBasisTracker(3, nil)
	_, _ = tr.Record("A1", "C5", AttestationInsufficientBasis)
	_, _ = tr.Record("A1", "C5", AttestationInsufficientBasis)
	tr.Reset("C5")
	if tr.Rounds("C5") != 0 {
		t.Fatal("Reset should clear counter")
	}
}

func TestScenario_InsufficientBasisTracker_EmptyClauseID_NoOp(t *testing.T) {
	tr := NewInsufficientBasisTracker(3, nil)
	rounds, crossed := tr.Record("A1", "", AttestationInsufficientBasis)
	if rounds != 0 || crossed {
		t.Fatalf("empty clauseID should no-op; got rounds=%d crossed=%v", rounds, crossed)
	}
}

func TestScenario_InsufficientBasisTracker_PerClauseIsolation(t *testing.T) {
	tr := NewInsufficientBasisTracker(3, nil)
	_, _ = tr.Record("A1", "C5", AttestationInsufficientBasis)
	_, _ = tr.Record("A1", "C5", AttestationInsufficientBasis)
	_, _ = tr.Record("A1", "C6", AttestationInsufficientBasis)
	if tr.Rounds("C5") != 2 {
		t.Errorf("C5 = %d; want 2", tr.Rounds("C5"))
	}
	if tr.Rounds("C6") != 1 {
		t.Errorf("C6 = %d; want 1", tr.Rounds("C6"))
	}
}
