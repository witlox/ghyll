package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/witlox/ghyll/cmd/ghyll/modal"
	"github.com/witlox/ghyll/runner"
)

// TestScenario_ModalDriver_ConcurrentPublishAndDrain verifies the
// modal driver survives N concurrent Publish calls while
// DrainPending iterates. Gate-2 T-1: cover the race-detector
// surface that was uncovered when every previous test ran
// publish + drain on the same goroutine.
//
// Runs with `go test -race` automatically (no t.Skip on race).
func TestScenario_ModalDriver_ConcurrentPublishAndDrain(t *testing.T) {
	stub := &modal.StubModal{}
	// Pre-load enough verdicts so PresentVerdict never blocks.
	for i := 0; i < 100; i++ {
		stub.Verdicts = append(stub.Verdicts,
			modal.VerdictSubmission{Verdict: runner.AttestationPass, Unit: runner.VerdictUnitConfirm})
	}
	fx := newDriverFixture(t, stub)

	var wg sync.WaitGroup
	// 4 publisher goroutines each emit 25 events; 1 drainer
	// repeatedly drains.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for k := 0; k < 25; k++ {
				hint := runner.Hint{
					ArrowID:        "A",
					ClauseID:       "c",
					AttestationRef: ref(base*100 + k),
				}
				hj, _ := json.Marshal(hint)
				fx.bus.Publish(runner.OperatorEvent{
					Kind: runner.OpEventAttestationRequested, ArrowID: "A", ClauseID: "c",
					PassID: "p", Detail: string(hj),
				})
			}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			_ = fx.driver.DrainPending(context.Background())
			if fx.driver.PendingLen() == 0 && fx.store.Len() >= 100 {
				return
			}
		}
	}()
	wg.Wait()
}

func ref(n int) string {
	return "ref-" + string(rune('a'+n%26)) + string(rune('a'+(n/26)%26)) + string(rune('a'+(n/676)%26))
}

// TestScenario_Session_CloseDuringDrain_NoWriteAfterClose verifies
// gate-2 CONC-M-3 / T-7: Session.Close runs while DrainPending
// is mid-modal; no panic, no write-after-close error.
func TestScenario_Session_CloseDuringDrain_NoWriteAfterClose(t *testing.T) {
	s := newOperatorTestSession(t)
	s.opID = "alice"
	// Stub modal that blocks on a gate so we can race Close.
	gate := make(chan struct{})
	stub := &blockingStubModal{gate: gate}
	s.modalPrompt = stub
	s.modalDriver = newModalDriver(
		stub,
		s.engine.AttestationStore(),
		s.engine.Passes(),
		s.engine.Bus(),
		s.engine.InsufficientBasisTracker(),
		func() string { return s.opID },
		s.buildArrowResolver(s.engine),
		0,
	)
	hint := runner.Hint{ArrowID: "A1", ClauseID: "C1", AttestationRef: "att-close-race"}
	hj, _ := json.Marshal(hint)
	s.engine.Bus().Publish(runner.OperatorEvent{
		Kind: runner.OpEventAttestationRequested, ArrowID: "A1", ClauseID: "C1",
		PassID: "P-close-race", Detail: string(hj),
	})
	// Start the drain in a goroutine; it'll block at the modal.
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		s.DrainModalPending(s.SessionContext())
	}()
	// Give the goroutine a moment to enter the modal.
	time.Sleep(20 * time.Millisecond)
	// Close while drain is in-flight. Should not panic; should
	// cancel sessionCtx so the modal returns.
	s.Close()
	// Unblock the modal so the goroutine can exit.
	close(gate)
	select {
	case <-drainDone:
	case <-time.After(2 * time.Second):
		t.Fatal("drain goroutine did not exit after Close")
	}
}

// blockingStubModal blocks PresentVerdict until gate is closed.
type blockingStubModal struct {
	gate chan struct{}
}

func (b *blockingStubModal) PresentVerdict(ctx context.Context, _ modal.Hint) (modal.VerdictSubmission, error) {
	select {
	case <-b.gate:
		return modal.VerdictSubmission{Verdict: runner.AttestationPass, Unit: runner.VerdictUnitConfirm}, nil
	case <-ctx.Done():
		return modal.VerdictSubmission{}, ctx.Err()
	}
}

func (b *blockingStubModal) PresentEscalation(ctx context.Context, _ modal.Hint) (modal.EscalationChoice, error) {
	select {
	case <-b.gate:
		return modal.EscalationChoice{Option: 1, Residue: "ok"}, nil
	case <-ctx.Done():
		return modal.EscalationChoice{}, ctx.Err()
	}
}
