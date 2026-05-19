package runner

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestScenario_OperatorBus_PublishFansOutToSubscribers(t *testing.T) {
	bus := NewOperatorBus()
	var got []OperatorEvent
	var mu sync.Mutex
	bus.Subscribe(func(e OperatorEvent) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, e)
	})

	bus.Publish(OperatorEvent{
		Kind:    OpEventAttestationRecorded,
		ArrowID: "A1",
		OpID:    "op-alice",
		Detail:  "verified",
	})

	if len(got) != 1 {
		t.Fatalf("subscriber received %d events; want 1", len(got))
	}
	if got[0].Kind != OpEventAttestationRecorded {
		t.Fatalf("event kind = %q; want %q", got[0].Kind, OpEventAttestationRecorded)
	}
	if got[0].Timestamp.IsZero() {
		t.Fatal("Publish should stamp Timestamp if zero")
	}
}

func TestScenario_OperatorBus_PublishPreservesNonZeroTimestamp(t *testing.T) {
	bus := NewOperatorBus()
	var got OperatorEvent
	bus.Subscribe(func(e OperatorEvent) { got = e })
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	bus.Publish(OperatorEvent{Kind: OpEventPassOpened, Timestamp: ts})
	if !got.Timestamp.Equal(ts) {
		t.Fatalf("Timestamp = %v; want %v (caller-supplied timestamps must be preserved)", got.Timestamp, ts)
	}
}

func TestScenario_OperatorBus_MultipleSubscribers_EachReceives(t *testing.T) {
	bus := NewOperatorBus()
	var a, b atomic.Int32
	bus.Subscribe(func(_ OperatorEvent) { a.Add(1) })
	bus.Subscribe(func(_ OperatorEvent) { b.Add(1) })
	if bus.SubscriberCount() != 2 {
		t.Fatalf("SubscriberCount = %d; want 2", bus.SubscriberCount())
	}
	bus.Publish(OperatorEvent{Kind: OpEventPassOpened})
	bus.Publish(OperatorEvent{Kind: OpEventPassClosed})
	if a.Load() != 2 || b.Load() != 2 {
		t.Fatalf("subscribers received (a=%d, b=%d); want (2, 2)", a.Load(), b.Load())
	}
}

func TestScenario_OperatorBus_SubscribeDuringPublish_DoesNotDeadlock(t *testing.T) {
	bus := NewOperatorBus()
	// First subscriber adds a second subscriber from within its
	// callback. The bus uses fan-out-outside-the-lock so this
	// should not deadlock.
	added := false
	bus.Subscribe(func(_ OperatorEvent) {
		if added {
			return
		}
		added = true
		bus.Subscribe(func(_ OperatorEvent) {})
	})
	done := make(chan struct{})
	go func() {
		bus.Publish(OperatorEvent{Kind: OpEventPassOpened})
		close(done)
	}()
	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("Publish deadlocked when subscriber called Subscribe")
	}
}

func TestScenario_OperatorBus_WithClock_OverridesStamp(t *testing.T) {
	pinned := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	bus := NewOperatorBus().WithClock(func() time.Time { return pinned })
	var got OperatorEvent
	bus.Subscribe(func(e OperatorEvent) { got = e })
	bus.Publish(OperatorEvent{Kind: OpEventPassOpened}) // Timestamp zero
	if !got.Timestamp.Equal(pinned) {
		t.Fatalf("Timestamp = %v; want pinned %v", got.Timestamp, pinned)
	}
}

func TestScenario_OperatorBus_PublishNoSubscribers_NoOp(t *testing.T) {
	bus := NewOperatorBus()
	// Just verify no panic.
	bus.Publish(OperatorEvent{Kind: OpEventPassOpened})
}

func TestScenario_OperatorBus_ConcurrentPublish_AllSeenByAllSubscribers(t *testing.T) {
	bus := NewOperatorBus()
	const n = 100
	var count atomic.Int32
	bus.Subscribe(func(_ OperatorEvent) { count.Add(1) })

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bus.Publish(OperatorEvent{Kind: OpEventPassOpened})
		}()
	}
	wg.Wait()
	if count.Load() != n {
		t.Fatalf("subscriber received %d events; want %d", count.Load(), n)
	}
}
