package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/witlox/ghyll/cmd/ghyll/modal"
	"github.com/witlox/ghyll/runner"
)

// TestScenario_ModalDriver_DrainPending_NonCancelError_PreservesTail
// verifies gate-2 CONC-C-3: on a non-cancel error mid-snapshot, the
// unprocessed tail items get re-queued (previously they were
// silently dropped + their attestRefs stranded in inFlight).
func TestScenario_ModalDriver_DrainPending_NonCancelError_PreservesTail(t *testing.T) {
	// Stub with 3 verdicts: first errors (via an injected non-cancel
	// error), second + third would succeed if reached.
	customErr := errors.New("simulated record failure")
	stub := &modal.StubModal{
		Verdicts: []modal.VerdictSubmission{
			{Verdict: runner.AttestationPass, Unit: runner.VerdictUnitConfirm},
			{Verdict: runner.AttestationPass, Unit: runner.VerdictUnitConfirm},
			{Verdict: runner.AttestationPass, Unit: runner.VerdictUnitConfirm},
		},
		VerdictErrs: []error{customErr, nil, nil},
	}
	fx := newDriverFixture(t, stub)
	for i, ref := range []string{"ref-a", "ref-b", "ref-c"} {
		hint := runner.Hint{ArrowID: "A", ClauseID: "c", AttestationRef: ref}
		hj, _ := json.Marshal(hint)
		fx.bus.Publish(runner.OperatorEvent{
			Kind:     runner.OpEventAttestationRequested,
			ArrowID:  "A",
			ClauseID: "c",
			PassID:   "p",
			Detail:   string(hj),
		})
		_ = i
	}
	if fx.driver.PendingLen() != 3 {
		t.Fatalf("PendingLen before drain = %d; want 3", fx.driver.PendingLen())
	}
	err := fx.driver.DrainPending(context.Background())
	if err == nil {
		t.Fatal("DrainPending should have returned the simulated error")
	}
	// Items b + c MUST be requeued.
	if got := fx.driver.PendingLen(); got != 2 {
		t.Errorf("PendingLen after drain = %d; want 2 (b + c re-queued)", got)
	}
	// First item's attestRef should be cleared (non-transient drop).
	// The remaining refs (b, c) stay in inFlight because they're
	// queued.
	fx.driver.mu.Lock()
	_, aStillInFlight := fx.driver.inFlight["ref-a"]
	_, bStillInFlight := fx.driver.inFlight["ref-b"]
	_, cStillInFlight := fx.driver.inFlight["ref-c"]
	fx.driver.mu.Unlock()
	if aStillInFlight {
		t.Error("ref-a should be cleared from inFlight (dropped)")
	}
	if !bStillInFlight {
		t.Error("ref-b should remain in inFlight (re-queued)")
	}
	if !cStillInFlight {
		t.Error("ref-c should remain in inFlight (re-queued)")
	}
}

// TestScenario_ModalDriver_DrainPending_CtxCancel_PreservesEntireTail
// verifies gate-2 CONC-C-4: on ctx-cancel mid-snapshot, the
// failing item AND every unprocessed tail item gets re-queued.
func TestScenario_ModalDriver_DrainPending_CtxCancel_PreservesEntireTail(t *testing.T) {
	// 3 queued items; cancel before drain so PresentVerdict
	// returns ctx.Err() on the FIRST item.
	stub := &modal.StubModal{
		Verdicts: []modal.VerdictSubmission{
			{Verdict: runner.AttestationPass, Unit: runner.VerdictUnitConfirm},
			{Verdict: runner.AttestationPass, Unit: runner.VerdictUnitConfirm},
			{Verdict: runner.AttestationPass, Unit: runner.VerdictUnitConfirm},
		},
	}
	fx := newDriverFixture(t, stub)
	for _, ref := range []string{"ref-1", "ref-2", "ref-3"} {
		hint := runner.Hint{ArrowID: "A", ClauseID: "c", AttestationRef: ref}
		hj, _ := json.Marshal(hint)
		fx.bus.Publish(runner.OperatorEvent{
			Kind:     runner.OpEventAttestationRequested,
			ArrowID:  "A",
			ClauseID: "c",
			PassID:   "p",
			Detail:   string(hj),
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := fx.driver.DrainPending(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v; want context.Canceled", err)
	}
	// All 3 items must survive — none consumed.
	if got := fx.driver.PendingLen(); got != 3 {
		t.Errorf("PendingLen after cancel = %d; want 3 (all 3 re-queued)", got)
	}
}

// TestScenario_ModalDriver_DrainPending_NonCancelError_EmitsBackpressureEvent
// verifies the operator-visible signal when an item is dropped.
func TestScenario_ModalDriver_DrainPending_NonCancelError_EmitsBackpressureEvent(t *testing.T) {
	customErr := errors.New("simulated failure")
	stub := &modal.StubModal{
		Verdicts:    []modal.VerdictSubmission{{}},
		VerdictErrs: []error{customErr},
	}
	fx := newDriverFixture(t, stub)
	hint := runner.Hint{ArrowID: "A", ClauseID: "c", AttestationRef: "ref-drop"}
	hj, _ := json.Marshal(hint)
	fx.bus.Publish(runner.OperatorEvent{
		Kind: runner.OpEventAttestationRequested, ArrowID: "A", ClauseID: "c", PassID: "p", Detail: string(hj),
	})
	_ = fx.driver.DrainPending(context.Background())
	// We expect at least one OpEventModalBackpressure carrying the
	// drop diagnostic.
	if fx.countByKind(runner.OpEventModalBackpressure) < 1 {
		t.Errorf("expected OpEventModalBackpressure on non-transient drop; got events %+v", fx.snapshotEvents())
	}
}
