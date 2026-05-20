package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/witlox/ghyll/cmd/ghyll/modal"
	"github.com/witlox/ghyll/runner"
)

// fixedOpID returns a constant op-id provider; the most common
// driver-test scaffold.
func fixedOpID(id string) func() string { return func() string { return id } }

// stubResolver returns a constant arrowResolved for any ArrowID.
// Used by tests so AttestationRecord.SourceRole/TargetRole are
// non-empty (matches the /attest CLI behavior).
func stubResolver(src, tgt string) arrowResolverFn {
	return func(_ string) (arrowResolved, bool) {
		return arrowResolved{
			SourceRole:  src,
			TargetRole:  tgt,
			Context:     "ctx-A",
			Stratum:     "S1",
			GridVersion: 7,
		}, true
	}
}

// driverFixture bundles the constructed driver + its dependencies
// + a captured-events slice so tests can assert on bus output.
type driverFixture struct {
	driver  *modalDriver
	store   *runner.AttestationStore
	bus     *runner.OperatorBus
	stub    *modal.StubModal
	ib      *runner.InsufficientBasisTracker
	events  []runner.OperatorEvent
	eventMu sync.Mutex
}

func newDriverFixture(t *testing.T, stub *modal.StubModal) *driverFixture {
	t.Helper()
	fx := &driverFixture{
		store: runner.NewAttestationStore(),
		bus:   runner.NewOperatorBus(),
		stub:  stub,
		ib:    runner.NewInsufficientBasisTracker(3, nil),
	}
	fx.bus.Subscribe(func(ev runner.OperatorEvent) {
		fx.eventMu.Lock()
		fx.events = append(fx.events, ev)
		fx.eventMu.Unlock()
	})
	fx.driver = newModalDriver(
		stub,
		fx.store,
		runner.NewPassRegistry(),
		fx.bus,
		fx.ib,
		fixedOpID("op-1"),
		stubResolver("analyst", "architect"),
		0,
	)
	return fx
}

func (fx *driverFixture) snapshotEvents() []runner.OperatorEvent {
	fx.eventMu.Lock()
	defer fx.eventMu.Unlock()
	out := make([]runner.OperatorEvent, len(fx.events))
	copy(out, fx.events)
	return out
}

func (fx *driverFixture) countByKind(k runner.OperatorEventKind) int {
	n := 0
	for _, e := range fx.snapshotEvents() {
		if e.Kind == k {
			n++
		}
	}
	return n
}

// --- happy-path verdict ----------------------------------------

func TestScenario_ModalDriver_OnEvent_VerdictPath_RecordsAttestation(t *testing.T) {
	stub := &modal.StubModal{
		Verdicts: []modal.VerdictSubmission{
			{Verdict: runner.AttestationPass, Unit: runner.VerdictUnitConfirm},
		},
	}
	fx := newDriverFixture(t, stub)
	hint := runner.Hint{
		ArrowID:        "arrow-X",
		ClauseID:       "c-1",
		AttestationRef: "att-ref-1",
	}
	hintJSON, _ := json.Marshal(hint)
	fx.bus.Publish(runner.OperatorEvent{
		Kind:     runner.OpEventAttestationRequested,
		ArrowID:  "arrow-X",
		ClauseID: "c-1",
		PassID:   "pass-1",
		Detail:   string(hintJSON),
	})
	if got := fx.driver.PendingLen(); got != 1 {
		t.Fatalf("PendingLen = %d; want 1", got)
	}
	if err := fx.driver.DrainPending(context.Background()); err != nil {
		t.Fatalf("DrainPending: %v", err)
	}
	if fx.store.Len() != 1 {
		t.Errorf("store.Len = %d; want 1", fx.store.Len())
	}
	rec, ok := fx.store.Lookup("att-ref-1")
	if !ok {
		t.Fatal("Lookup failed")
	}
	if rec.Verdict != runner.AttestationPass || rec.Unit != runner.VerdictUnitConfirm {
		t.Errorf("verdict/unit mismatch: %+v", rec)
	}
	if rec.OpID != "op-1" {
		t.Errorf("OpID = %q; want op-1", rec.OpID)
	}
	if rec.SourceRole != "analyst" || rec.TargetRole != "architect" {
		t.Errorf("roles = %q/%q; want analyst/architect", rec.SourceRole, rec.TargetRole)
	}
}

// --- dedup -----------------------------------------------------

func TestScenario_ModalDriver_OnEvent_DedupsByAttestRef(t *testing.T) {
	stub := &modal.StubModal{
		Verdicts: []modal.VerdictSubmission{
			{Verdict: runner.AttestationPass, Unit: runner.VerdictUnitConfirm},
		},
	}
	fx := newDriverFixture(t, stub)
	hint := runner.Hint{ArrowID: "arrow-X", ClauseID: "c-1", AttestationRef: "att-ref-1"}
	hj, _ := json.Marshal(hint)
	for i := 0; i < 3; i++ {
		fx.bus.Publish(runner.OperatorEvent{
			Kind:     runner.OpEventAttestationRequested,
			ArrowID:  "arrow-X",
			ClauseID: "c-1",
			PassID:   "pass-1",
			Detail:   string(hj),
		})
	}
	if got := fx.driver.PendingLen(); got != 1 {
		t.Errorf("PendingLen = %d; dedup should hold to 1", got)
	}
}

// --- skip -------------------------------------------------------

func TestScenario_ModalDriver_VerdictSkip_EmitsModalSkippedNoRecord(t *testing.T) {
	stub := &modal.StubModal{
		Verdicts:    []modal.VerdictSubmission{{}},
		VerdictErrs: []error{modal.ErrModalSkipped},
	}
	fx := newDriverFixture(t, stub)
	hint := runner.Hint{ArrowID: "A", ClauseID: "c", AttestationRef: "att-1"}
	hj, _ := json.Marshal(hint)
	fx.bus.Publish(runner.OperatorEvent{
		Kind: runner.OpEventAttestationRequested, ArrowID: "A", ClauseID: "c", PassID: "p", Detail: string(hj),
	})
	if err := fx.driver.DrainPending(context.Background()); err != nil {
		t.Fatalf("DrainPending: %v", err)
	}
	if fx.store.Len() != 0 {
		t.Errorf("store.Len = %d; skip should not record", fx.store.Len())
	}
	if fx.countByKind(runner.OpEventModalSkipped) != 1 {
		t.Errorf("OpEventModalSkipped count = %d; want 1", fx.countByKind(runner.OpEventModalSkipped))
	}
}

// --- backpressure ----------------------------------------------

func TestScenario_ModalDriver_BackpressureDropsAndEmits(t *testing.T) {
	stub := &modal.StubModal{}
	fx := &driverFixture{
		store: runner.NewAttestationStore(),
		bus:   runner.NewOperatorBus(),
		stub:  stub,
		ib:    runner.NewInsufficientBasisTracker(3, nil),
	}
	fx.bus.Subscribe(func(ev runner.OperatorEvent) {
		fx.eventMu.Lock()
		fx.events = append(fx.events, ev)
		fx.eventMu.Unlock()
	})
	// Tiny cap so we overflow with 3 events.
	fx.driver = newModalDriver(
		stub, fx.store, runner.NewPassRegistry(), fx.bus, fx.ib,
		fixedOpID("op-1"), stubResolver("a", "b"), 2,
	)
	for i := 0; i < 5; i++ {
		hint := runner.Hint{ArrowID: "A", ClauseID: "c", AttestationRef: "ref-" + string(rune('A'+i))}
		hj, _ := json.Marshal(hint)
		fx.bus.Publish(runner.OperatorEvent{
			Kind: runner.OpEventAttestationRequested, ArrowID: "A", ClauseID: "c", PassID: "p", Detail: string(hj),
		})
	}
	if fx.driver.PendingLen() != 2 {
		t.Errorf("PendingLen = %d; want 2 (cap)", fx.driver.PendingLen())
	}
	if fx.countByKind(runner.OpEventModalBackpressure) == 0 {
		t.Errorf("backpressure event not emitted")
	}
}

// --- escalation: option 1 -------------------------------------

func TestScenario_ModalDriver_EscalationOption1_RecordsPassWithResidue(t *testing.T) {
	stub := &modal.StubModal{
		Escalations: []modal.EscalationChoice{
			{Option: 1, Residue: "accept the risk because X"},
		},
	}
	fx := newDriverFixture(t, stub)
	fx.bus.Publish(runner.OperatorEvent{
		Kind:     runner.OpEventInsufficientBasisRoundsExceeded,
		ArrowID:  "A",
		ClauseID: "c-1",
		PassID:   "p-1",
	})
	if err := fx.driver.DrainPending(context.Background()); err != nil {
		t.Fatalf("DrainPending: %v", err)
	}
	all := fx.store.All()
	if len(all) != 1 {
		t.Fatalf("All() = %d records; want 1", len(all))
	}
	rec := all[0]
	if rec.Verdict != runner.AttestationPass {
		t.Errorf("verdict = %q; want pass for option 1", rec.Verdict)
	}
	if rec.Unit != runner.VerdictUnitWriteResidueNote {
		t.Errorf("unit = %q", rec.Unit)
	}
	if rec.UnitPayload.Residue != "accept the risk because X" {
		t.Errorf("residue = %q", rec.UnitPayload.Residue)
	}
	if fx.countByKind(runner.OpEventEscalationPresented) != 1 ||
		fx.countByKind(runner.OpEventEscalationResolved) != 1 {
		t.Errorf("escalation events: presented=%d resolved=%d",
			fx.countByKind(runner.OpEventEscalationPresented),
			fx.countByKind(runner.OpEventEscalationResolved))
	}
}

// --- escalation: option 2 -------------------------------------

func TestScenario_ModalDriver_EscalationOption2_RecordsFailWithRationale(t *testing.T) {
	stub := &modal.StubModal{
		Escalations: []modal.EscalationChoice{
			{Option: 2, Residue: "route upstream because Y"},
		},
	}
	fx := newDriverFixture(t, stub)
	fx.bus.Publish(runner.OperatorEvent{
		Kind: runner.OpEventInsufficientBasisRoundsExceeded, ArrowID: "A", ClauseID: "c", PassID: "p",
	})
	if err := fx.driver.DrainPending(context.Background()); err != nil {
		t.Fatalf("DrainPending: %v", err)
	}
	rec := fx.store.All()[0]
	if rec.Verdict != runner.AttestationFail {
		t.Errorf("verdict = %q; want fail for option 2", rec.Verdict)
	}
}

// --- IB-crossed gating ----------------------------------------

func TestScenario_ModalDriver_CrossedClause_PresentsEscalation(t *testing.T) {
	// Setup: stub returns escalation choice but has NO verdict
	// queued, so if the driver mistakenly calls PresentVerdict
	// we'd get ErrModalSkipped.
	stub := &modal.StubModal{
		Escalations: []modal.EscalationChoice{
			{Option: 1, Residue: "ok"},
		},
	}
	fx := newDriverFixture(t, stub)
	// Mark clause as crossed.
	for i := 0; i < 3; i++ {
		fx.ib.Record("A", "c-x", runner.AttestationInsufficientBasis)
	}
	if !fx.ib.IsCrossed("c-x") {
		t.Fatal("precondition: clause not crossed after 3 records")
	}
	hint := runner.Hint{ArrowID: "A", ClauseID: "c-x", AttestationRef: "ref-x"}
	hj, _ := json.Marshal(hint)
	fx.bus.Publish(runner.OperatorEvent{
		Kind: runner.OpEventAttestationRequested, ArrowID: "A", ClauseID: "c-x", PassID: "p", Detail: string(hj),
	})
	if err := fx.driver.DrainPending(context.Background()); err != nil {
		t.Fatalf("DrainPending: %v", err)
	}
	// Should have recorded via the escalation path (pass +
	// residue).
	if fx.store.Len() != 1 {
		t.Fatalf("store.Len = %d; want 1", fx.store.Len())
	}
	if fx.countByKind(runner.OpEventEscalationPresented) != 1 {
		t.Errorf("expected escalation-presented event")
	}
}

// --- drain cap exceeded ---------------------------------------

func TestScenario_ModalDriver_DrainCapExceeded_ReturnsTypedError(t *testing.T) {
	// A stub that publishes ANOTHER event on every PresentVerdict.
	// We achieve this by interposing a custom prompt that re-publishes.
	fx := &driverFixture{
		store: runner.NewAttestationStore(),
		bus:   runner.NewOperatorBus(),
		ib:    runner.NewInsufficientBasisTracker(3, nil),
	}
	fx.bus.Subscribe(func(ev runner.OperatorEvent) {
		fx.eventMu.Lock()
		fx.events = append(fx.events, ev)
		fx.eventMu.Unlock()
	})
	counter := int32(0)
	prompt := &republishingPrompt{
		bus:     fx.bus,
		counter: &counter,
	}
	fx.driver = newModalDriver(
		prompt, fx.store, runner.NewPassRegistry(), fx.bus, fx.ib,
		fixedOpID("op-1"), stubResolver("a", "b"), 0,
	)
	// Seed the queue.
	hint := runner.Hint{ArrowID: "A", ClauseID: "c", AttestationRef: "ref-seed"}
	hj, _ := json.Marshal(hint)
	fx.bus.Publish(runner.OperatorEvent{
		Kind: runner.OpEventAttestationRequested, ArrowID: "A", ClauseID: "c", PassID: "p", Detail: string(hj),
	})
	err := fx.driver.DrainPending(context.Background())
	if !errors.Is(err, ErrModalDrainCapExceeded) {
		t.Errorf("err = %v; want ErrModalDrainCapExceeded", err)
	}
}

// republishingPrompt publishes a fresh attestation-requested event
// on every PresentVerdict, simulating a runaway publisher.
type republishingPrompt struct {
	bus     *runner.OperatorBus
	counter *int32
}

func (p *republishingPrompt) PresentVerdict(ctx context.Context, hint modal.Hint) (modal.VerdictSubmission, error) {
	n := atomic.AddInt32(p.counter, 1)
	// Publish another event for a unique ref.
	freshHint := runner.Hint{ArrowID: "A", ClauseID: "c", AttestationRef: "ref-republished-" + string(rune('A'+n))}
	hj, _ := json.Marshal(freshHint)
	p.bus.Publish(runner.OperatorEvent{
		Kind: runner.OpEventAttestationRequested, ArrowID: "A", ClauseID: "c", PassID: "p", Detail: string(hj),
	})
	return modal.VerdictSubmission{Verdict: runner.AttestationPass, Unit: runner.VerdictUnitConfirm}, nil
}

func (p *republishingPrompt) PresentEscalation(ctx context.Context, hint modal.Hint) (modal.EscalationChoice, error) {
	return modal.EscalationChoice{}, nil
}

// --- ctx-cancel rerequeues ------------------------------------

func TestScenario_ModalDriver_CtxCancel_RequeuesPending(t *testing.T) {
	// Cancelled ctx → StubModal returns ctx.Err() from
	// PresentVerdict; the driver should re-queue the item.
	stub := &modal.StubModal{
		Verdicts: []modal.VerdictSubmission{{Verdict: runner.AttestationPass, Unit: runner.VerdictUnitConfirm}},
	}
	fx := newDriverFixture(t, stub)
	hint := runner.Hint{ArrowID: "A", ClauseID: "c", AttestationRef: "ref-1"}
	hj, _ := json.Marshal(hint)
	fx.bus.Publish(runner.OperatorEvent{
		Kind: runner.OpEventAttestationRequested, ArrowID: "A", ClauseID: "c", PassID: "p", Detail: string(hj),
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := fx.driver.DrainPending(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v; want context.Canceled", err)
	}
	// Item should be back on the queue for the next turn.
	if got := fx.driver.PendingLen(); got != 1 {
		t.Errorf("PendingLen after cancel = %d; want 1 (re-queued)", got)
	}
}

// --- EnqueueFromRecovery --------------------------------------

func TestScenario_ModalDriver_EnqueueFromRecovery_FeedsQueue(t *testing.T) {
	fx := newDriverFixture(t, &modal.StubModal{})
	now := time.Now()
	events := []runner.OperatorEvent{
		{
			Kind:     runner.OpEventRecoveryAttestationRepublished,
			ArrowID:  "A",
			ClauseID: "c",
			PassID:   "p",
			Detail:   "att-ref=ref-recovered preserved at " + now.Format(time.RFC3339),
		},
		// Non-recovery events should be ignored.
		{Kind: runner.OpEventPassClosed, ArrowID: "Z"},
	}
	fx.driver.EnqueueFromRecovery(events)
	if got := fx.driver.PendingLen(); got != 1 {
		t.Errorf("PendingLen = %d; want 1 (only recovery republish)", got)
	}
}

func TestScenario_ModalDriver_ExtractAttRef(t *testing.T) {
	cases := map[string]string{
		"att-ref=foo preserved at 2026-05-19": "foo",
		"att-ref=bar":                         "bar",
		"no att-ref here":                     "",
	}
	for in, want := range cases {
		got := extractAttRef(in)
		if got != want {
			t.Errorf("extractAttRef(%q) = %q; want %q", in, got, want)
		}
	}
}

// --- opIDProvider absent --------------------------------------

func TestScenario_ModalDriver_NoOpID_RejectsRecord(t *testing.T) {
	stub := &modal.StubModal{
		Verdicts: []modal.VerdictSubmission{
			{Verdict: runner.AttestationPass, Unit: runner.VerdictUnitConfirm},
		},
	}
	fx := &driverFixture{
		store: runner.NewAttestationStore(),
		bus:   runner.NewOperatorBus(),
		stub:  stub,
		ib:    runner.NewInsufficientBasisTracker(3, nil),
	}
	fx.bus.Subscribe(func(ev runner.OperatorEvent) {
		fx.eventMu.Lock()
		fx.events = append(fx.events, ev)
		fx.eventMu.Unlock()
	})
	fx.driver = newModalDriver(
		stub, fx.store, runner.NewPassRegistry(), fx.bus, fx.ib,
		fixedOpID(""), stubResolver("a", "b"), 0,
	)
	hint := runner.Hint{ArrowID: "A", ClauseID: "c", AttestationRef: "ref-1"}
	hj, _ := json.Marshal(hint)
	fx.bus.Publish(runner.OperatorEvent{
		Kind: runner.OpEventAttestationRequested, ArrowID: "A", ClauseID: "c", PassID: "p", Detail: string(hj),
	})
	err := fx.driver.DrainPending(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no active op-id") {
		t.Errorf("err = %v; want 'no active op-id'", err)
	}
	if fx.store.Len() != 0 {
		t.Errorf("store.Len = %d; want 0 (record refused)", fx.store.Len())
	}
}

// --- residue note cap -----------------------------------------

func TestScenario_ModalDriver_ResidueOverCap_RejectedBeforeRecord(t *testing.T) {
	long := strings.Repeat("x", 65)
	stub := &modal.StubModal{
		Verdicts: []modal.VerdictSubmission{
			{
				Verdict: runner.AttestationInsufficientBasis,
				Unit:    runner.VerdictUnitWriteResidueNote,
				Payload: runner.VerdictUnitPayload{Residue: long},
			},
		},
	}
	fx := newDriverFixture(t, stub)
	fx.driver.residueNoteMaxBytes = 64 // tiny cap so 65-byte residue fails
	hint := runner.Hint{ArrowID: "A", ClauseID: "c", AttestationRef: "att-residue-cap"}
	hj, _ := json.Marshal(hint)
	fx.bus.Publish(runner.OperatorEvent{
		Kind: runner.OpEventAttestationRequested, ArrowID: "A", ClauseID: "c", PassID: "p", Detail: string(hj),
	})
	err := fx.driver.DrainPending(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unit payload") {
		t.Errorf("err = %v; want unit-payload error", err)
	}
	if fx.store.Len() != 0 {
		t.Errorf("store.Len = %d; want 0 (over-cap residue refused)", fx.store.Len())
	}
}

// --- subscriber filter -----------------------------------------

func TestScenario_ModalDriver_OnEvent_IgnoresIrrelevantKinds(t *testing.T) {
	fx := newDriverFixture(t, &modal.StubModal{})
	for _, k := range []runner.OperatorEventKind{
		runner.OpEventPassOpened,
		runner.OpEventAttestationRecorded,
		runner.OpEventPassClosed,
	} {
		fx.bus.Publish(runner.OperatorEvent{Kind: k, ArrowID: "Z"})
	}
	if got := fx.driver.PendingLen(); got != 0 {
		t.Errorf("PendingLen = %d; want 0 for unrelated kinds", got)
	}
}
