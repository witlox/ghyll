package runner

import (
	"sync/atomic"
	"testing"
)

// TestScenario_OperatorBus_Subscribe_ReturnsCloser verifies gate-2
// CONC-H-4: the closer returned by Subscribe drops the
// subscriber from the bus.
func TestScenario_OperatorBus_Subscribe_ReturnsCloser(t *testing.T) {
	bus := NewOperatorBus()
	var calls int32
	cancel := bus.Subscribe(func(_ OperatorEvent) {
		atomic.AddInt32(&calls, 1)
	})
	bus.Publish(OperatorEvent{Kind: OpEventPassOpened})
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d; want 1", got)
	}
	cancel()
	bus.Publish(OperatorEvent{Kind: OpEventPassOpened})
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d after cancel; want still 1", got)
	}
	// Idempotent close.
	cancel()
}

func TestScenario_OperatorBus_Subscribe_MultipleCancellersDistinct(t *testing.T) {
	bus := NewOperatorBus()
	var a, b int32
	cancelA := bus.Subscribe(func(_ OperatorEvent) { atomic.AddInt32(&a, 1) })
	_ = bus.Subscribe(func(_ OperatorEvent) { atomic.AddInt32(&b, 1) })

	bus.Publish(OperatorEvent{Kind: OpEventPassOpened})
	if atomic.LoadInt32(&a) != 1 || atomic.LoadInt32(&b) != 1 {
		t.Fatalf("a=%d b=%d; both want 1", a, b)
	}
	cancelA()
	bus.Publish(OperatorEvent{Kind: OpEventPassOpened})
	if atomic.LoadInt32(&a) != 1 {
		t.Errorf("a = %d after cancel; want 1", a)
	}
	if atomic.LoadInt32(&b) != 2 {
		t.Errorf("b = %d; want 2 (still subscribed)", b)
	}
}
