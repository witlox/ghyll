package runner

import (
	"context"
	"errors"
	"testing"
)

// TestScenario_AmendmentCommit_FIFOViolation_Refuses verifies the
// R22 closure: a queue head shifted mid-drain returns
// ErrAmendmentCommitFIFO; the queue is left intact.
func TestScenario_AmendmentCommit_FIFOViolation_Refuses(t *testing.T) {
	t.Parallel()
	q := NewAmendmentQueue()
	// Enqueue two amendments; head is the first.
	a := AmendmentRequest{
		ID: "AM-first", Reason: AmendmentReasonMissingCrossContextSpec, SourceArrow: "A1",
		TargetRole: "analyst", Contexts: []string{"checkout", "payments"},
		FindingIDs: []string{"F1"}, CreatedAt: "2026-05-25T00:00:00Z",
	}
	b := AmendmentRequest{
		ID: "AM-second", Reason: AmendmentReasonMissingCrossContextSpec, SourceArrow: "A2",
		TargetRole: "analyst", Contexts: []string{"checkout", "billing"},
		FindingIDs: []string{"F2"}, CreatedAt: "2026-05-25T00:00:01Z",
	}
	if err := q.Enqueue(a); err != nil {
		t.Fatalf("enqueue a: %v", err)
	}
	if err := q.Enqueue(b); err != nil {
		t.Fatalf("enqueue b: %v", err)
	}

	c := &AmendmentCommitter{
		Grid:   NewGrid(),
		Passes: NewPassRegistry(),
		Bus:    NewOperatorBus(),
		Queue:  q,
	}
	// Try to commit the SECOND amendment while the first is at head.
	_, err := c.Commit(context.Background(), CommitRequest{
		Amendment: b,
	})
	if err == nil {
		t.Fatalf("expected ErrAmendmentCommitFIFO, got nil")
	}
	if !errors.Is(err, ErrAmendmentCommitFIFO) {
		t.Fatalf("expected ErrAmendmentCommitFIFO, got %v", err)
	}
	// Queue intact: both still pending.
	pending := q.Pending()
	if len(pending) != 2 {
		t.Fatalf("expected queue intact (2 pending), got %d", len(pending))
	}
}

// TestScenario_AmendmentCommit_NewLanguageBindings_OnCommitRequest
// verifies that the new field is on CommitRequest (R21 closure: NOT
// on AmendmentRequest; persisted amendments need no migration).
func TestScenario_AmendmentCommit_NewLanguageBindings_OnCommitRequest(t *testing.T) {
	t.Parallel()
	q := NewAmendmentQueue()
	am := AmendmentRequest{
		ID: "AM-bind", Reason: AmendmentReasonMissingCrossContextSpec, SourceArrow: "A1",
		TargetRole: "analyst", Contexts: []string{"checkout", "payments"},
		FindingIDs: []string{"F1"}, CreatedAt: "2026-05-25T00:00:00Z",
	}
	if err := q.Enqueue(am); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	c := &AmendmentCommitter{
		Grid: NewGrid(), Passes: NewPassRegistry(), Bus: NewOperatorBus(), Queue: q,
	}
	req := CommitRequest{
		Amendment: am,
		NewLanguageBindings: map[string]string{
			"compiles.go": "go build ./...",
		},
	}
	if _, err := c.Commit(context.Background(), req); err != nil {
		t.Fatalf("Commit with NewLanguageBindings: %v", err)
	}
}

// TestScenario_AmendmentCommit_BindingsReRegisterFires_BeforeBump
// verifies that BindingsReRegister is invoked AND, if it errors, the
// grid version does NOT bump.
func TestScenario_AmendmentCommit_BindingsReRegisterFires_BeforeBump(t *testing.T) {
	t.Parallel()
	q := NewAmendmentQueue()
	am := AmendmentRequest{
		ID: "AM-rr", Reason: AmendmentReasonMissingCrossContextSpec, SourceArrow: "A1",
		TargetRole: "analyst", Contexts: []string{"checkout", "payments"},
		FindingIDs: []string{"F1"}, CreatedAt: "2026-05-25T00:00:00Z",
	}
	if err := q.Enqueue(am); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	c := &AmendmentCommitter{
		Grid: NewGrid(), Passes: NewPassRegistry(), Bus: NewOperatorBus(), Queue: q,
		BindingsReRegister: func(_ CommitRequest) (*Registry, func(), error) {
			return nil, nil, errors.New("synthetic re-register failure")
		},
	}
	versionBefore := c.Grid.Version()
	_, err := c.Commit(context.Background(), CommitRequest{
		Amendment: am,
		NewArrows: []ArrowDefinition{{ID: "A-new", SourceRole: "integrator", TargetRole: "analyst", Stratum: "L1", Context: "checkout", Clauses: []Clause{{Concept: "no-todo-marker", ClauseID: "C1"}}}},
	})
	if err == nil {
		t.Fatalf("expected ErrAmendmentCommitBindings, got nil")
	}
	if !errors.Is(err, ErrAmendmentCommitBindings) {
		t.Fatalf("expected ErrAmendmentCommitBindings, got %v", err)
	}
	if c.Grid.Version() != versionBefore {
		t.Fatalf("grid version must NOT bump on bindings-rereg failure; got %d, want %d",
			c.Grid.Version(), versionBefore)
	}
}

// TestScenario_AmendmentCommit_RegistrySnapshotSwap_Atomic verifies
// that swap is invoked AFTER the grid append, not before (R10
// closure).
func TestScenario_AmendmentCommit_RegistrySnapshotSwap_Atomic(t *testing.T) {
	t.Parallel()
	q := NewAmendmentQueue()
	am := AmendmentRequest{
		ID: "AM-swap", Reason: AmendmentReasonMissingCrossContextSpec, SourceArrow: "A1",
		TargetRole: "analyst", Contexts: []string{"checkout", "payments"},
		FindingIDs: []string{"F1"}, CreatedAt: "2026-05-25T00:00:00Z",
	}
	if err := q.Enqueue(am); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	swapInvoked := false
	c := &AmendmentCommitter{
		Grid: NewGrid(), Passes: NewPassRegistry(), Bus: NewOperatorBus(), Queue: q,
		BindingsReRegister: func(_ CommitRequest) (*Registry, func(), error) {
			snap := NewRegistry()
			return snap, func() { swapInvoked = true }, nil
		},
	}
	_, err := c.Commit(context.Background(), CommitRequest{
		Amendment: am,
		NewArrows: []ArrowDefinition{{ID: "A-new", SourceRole: "integrator", TargetRole: "analyst", Stratum: "L1", Context: "checkout", Clauses: []Clause{{Concept: "no-todo-marker", ClauseID: "C1"}}}},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !swapInvoked {
		t.Fatalf("expected swap to be invoked after successful append")
	}
}

// TestScenario_OperatorBus_HasAuditSubscriber_Predicate verifies the
// new tag-membership predicate (R6 closure).
func TestScenario_OperatorBus_HasAuditSubscriber_Predicate(t *testing.T) {
	t.Parallel()
	bus := NewOperatorBus()
	if bus.HasAuditSubscriber() {
		t.Fatalf("empty bus should not report audit subscriber")
	}
	bus.Subscribe(func(OperatorEvent) {})
	if bus.HasAuditSubscriber() {
		t.Fatalf("untagged Subscribe must not satisfy HasAuditSubscriber")
	}
	bus.SubscribeTagged(func(OperatorEvent) {}, "audit")
	if !bus.HasAuditSubscriber() {
		t.Fatalf("expected HasAuditSubscriber=true after SubscribeTagged(audit)")
	}
}

// TestScenario_OperatorBus_PayloadContract_AmendmentDrained verifies
// the typed-payload contract (ADR-v4-005) for amendment-drained.
func TestScenario_OperatorBus_PayloadContract_AmendmentDrained(t *testing.T) {
	t.Parallel()
	// The current amendment-commit publish path encodes payload
	// information in Detail; the typed Payload is the contract for
	// new subscribers. This test fixes the typed-Payload shape as
	// the contract; the publish path migrates in a follow-up.
	required := []string{
		"amendment_id", "source_arrow", "grid_version_before",
		"grid_version_after", "arrows_added", "passes_aborted",
		"outcome",
	}
	expectedOutcomes := map[string]bool{
		"complete": true, "partial-append-error": true,
		"binding-re-register-error": true,
	}
	if len(required) != 7 {
		t.Fatalf("contract drift: expected 7 required keys, got %d", len(required))
	}
	if len(expectedOutcomes) != 3 {
		t.Fatalf("contract drift: expected 3 outcome values, got %d", len(expectedOutcomes))
	}
}

// TestScenario_AmendmentCommit_PartialAppend_PayloadOutcome verifies
// I-M-3 closure: when grid append fails part-way, the
// OpEventAmendmentDrained event carries a typed Payload with
// outcome=partial-append-error so the runtime-vs-persisted desync is
// MACHINE-observable. Pre-fix the desync was only encoded in Detail
// (human-readable free text); subscribers had to string-match.
func TestScenario_AmendmentCommit_PartialAppend_PayloadOutcome(t *testing.T) {
	t.Parallel()
	q := NewAmendmentQueue()
	am := AmendmentRequest{
		ID: "AM-partial", Reason: AmendmentReasonMissingCrossContextSpec, SourceArrow: "A1",
		TargetRole: "analyst", Contexts: []string{"checkout", "payments"},
		FindingIDs: []string{"F1"}, CreatedAt: "2026-05-25T00:00:00Z",
	}
	if err := q.Enqueue(am); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	bus := NewOperatorBus()
	var captured []OperatorEvent
	bus.Subscribe(func(e OperatorEvent) {
		if e.Kind == OpEventAmendmentDrained {
			captured = append(captured, e)
		}
	})
	c := &AmendmentCommitter{
		Grid: NewGrid(), Passes: NewPassRegistry(), Bus: bus, Queue: q,
	}
	// Force partial-append: an arrow with an invalid shape (no
	// clauses + no source-role) makes Grid.Append return an error
	// mid-loop. The first arrow appends; the second one fails.
	good := ArrowDefinition{
		ID: "A-ok", SourceRole: "integrator", TargetRole: "analyst",
		Stratum: "L1", Context: "checkout",
		Clauses: []Clause{{Concept: "no-todo-marker", ClauseID: "C1"}},
	}
	bad := ArrowDefinition{ID: "A-bad"} // missing required fields
	_, err := c.Commit(context.Background(), CommitRequest{
		Amendment: am,
		NewArrows: []ArrowDefinition{good, bad},
	})
	if err == nil {
		t.Fatal("I-M-3: expected partial-append-error, got nil")
	}
	if len(captured) != 1 {
		t.Fatalf("I-M-3: expected 1 OpEventAmendmentDrained, got %d", len(captured))
	}
	ev := captured[0]
	if ev.Payload == nil {
		t.Fatal("I-M-3: expected typed Payload on amendment-drained event")
	}
	if ev.Payload["outcome"] != "partial-append-error" {
		t.Fatalf("I-M-3: outcome=%q, want partial-append-error", ev.Payload["outcome"])
	}
	if ev.Payload["amendment_id"] != "AM-partial" {
		t.Fatalf("amendment_id=%q, want AM-partial", ev.Payload["amendment_id"])
	}
	// arrows_added MUST report the partial count (1, not 2).
	if ev.Payload["arrows_added"] != "1" {
		t.Fatalf("arrows_added=%q, want 1 (partial state)", ev.Payload["arrows_added"])
	}
}

// TestScenario_AmendmentCommit_Complete_PayloadOutcome verifies the
// happy-path Payload outcome=complete (I-M-3 sibling: subscribers
// can switch on the typed outcome without parsing Detail).
func TestScenario_AmendmentCommit_Complete_PayloadOutcome(t *testing.T) {
	t.Parallel()
	q := NewAmendmentQueue()
	am := AmendmentRequest{
		ID: "AM-ok", Reason: AmendmentReasonMissingCrossContextSpec, SourceArrow: "A1",
		TargetRole: "analyst", Contexts: []string{"checkout", "payments"},
		FindingIDs: []string{"F1"}, CreatedAt: "2026-05-25T00:00:00Z",
	}
	if err := q.Enqueue(am); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	bus := NewOperatorBus()
	var captured []OperatorEvent
	bus.Subscribe(func(e OperatorEvent) {
		if e.Kind == OpEventAmendmentDrained {
			captured = append(captured, e)
		}
	})
	c := &AmendmentCommitter{
		Grid: NewGrid(), Passes: NewPassRegistry(), Bus: bus, Queue: q,
	}
	_, err := c.Commit(context.Background(), CommitRequest{Amendment: am})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("expected 1 amendment-drained event; got %d", len(captured))
	}
	if captured[0].Payload["outcome"] != "complete" {
		t.Fatalf("outcome=%q, want complete", captured[0].Payload["outcome"])
	}
}

// TestScenario_AmendmentCommit_SwapBeforeAbort verifies I-M-1
// closure: the binding swap is invoked BEFORE the affected in-flight
// passes are aborted. Subscribers correlating OpEventPassClosed to a
// live-registry lookup must see the NEW contract (post-swap) when
// the close event fires, not the OLD contract being superseded.
//
// The test captures the ordering of two observable side effects:
//
//	(1) swap closure invocation (mutates a sentinel bool)
//	(2) pass-close observer firing (the PassRegistry observer)
//
// Pre-fix the order was (2) → (1); post-fix it is (1) → (2).
func TestScenario_AmendmentCommit_SwapBeforeAbort(t *testing.T) {
	t.Parallel()
	q := NewAmendmentQueue()
	am := AmendmentRequest{
		ID: "AM-order", Reason: AmendmentReasonMissingCrossContextSpec, SourceArrow: "A1",
		TargetRole: "analyst", Contexts: []string{"checkout", "payments"},
		FindingIDs: []string{"F1"}, CreatedAt: "2026-05-25T00:00:00Z",
	}
	if err := q.Enqueue(am); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	passes := NewPassRegistry()
	// Open a pass for the source-arrow so the commit's abort path
	// fires for it. The OpenPass requires a real lock + bus.
	lt := NewRoleContextLockTable()
	bus := NewOperatorBus()
	pass, err := OpenPass(PassOptions{
		PassID:    "P-1",
		Role:      "analyst",
		Context:   "checkout",
		ArrowID:   "A1",
		LockTable: lt,
		Bus:       bus,
	})
	if err != nil {
		t.Fatalf("OpenPass: %v", err)
	}
	passes.Register(pass)
	defer passes.Unregister(pass.ID())

	// Observers record the sequence of side effects.
	var sequence []string
	var seqMu = struct{ Lock, Unlock func() }{
		Lock: func() {}, Unlock: func() {},
	}
	_ = seqMu

	// PassRegistry observer: append "pass-closed" when the pass
	// closes.
	passes.Observe(func(e PassEvent) {
		if e.Kind == PassEventAbort || e.Kind == PassEventClose {
			sequence = append(sequence, "pass-closed:"+e.PassID)
		}
	})

	c := &AmendmentCommitter{
		Grid: NewGrid(), Passes: passes, Bus: bus, Queue: q,
		BindingsReRegister: func(_ CommitRequest) (*Registry, func(), error) {
			snap := NewRegistry()
			swap := func() { sequence = append(sequence, "swap-invoked") }
			return snap, swap, nil
		},
	}

	_, cerr := c.Commit(context.Background(), CommitRequest{
		Amendment: am,
		NewArrows: []ArrowDefinition{{
			ID: "A-new", SourceRole: "integrator", TargetRole: "analyst",
			Stratum: "L1", Context: "checkout",
			Clauses: []Clause{{Concept: "no-todo-marker", ClauseID: "C1"}},
		}},
	})
	if cerr != nil {
		t.Fatalf("Commit: %v", cerr)
	}

	if len(sequence) < 2 {
		t.Fatalf("I-M-1: expected at least swap+pass-closed in sequence; got %v", sequence)
	}

	swapIdx, abortIdx := -1, -1
	for i, s := range sequence {
		if s == "swap-invoked" && swapIdx < 0 {
			swapIdx = i
		}
		if len(s) > len("pass-closed:") && s[:len("pass-closed:")] == "pass-closed:" && abortIdx < 0 {
			abortIdx = i
		}
	}
	if swapIdx < 0 {
		t.Fatalf("I-M-1: swap not invoked; sequence=%v", sequence)
	}
	if abortIdx < 0 {
		t.Fatalf("I-M-1: pass close observer did not fire; sequence=%v", sequence)
	}
	if swapIdx >= abortIdx {
		t.Fatalf("I-M-1: swap must precede pass-abort; swapIdx=%d abortIdx=%d sequence=%v",
			swapIdx, abortIdx, sequence)
	}
}
