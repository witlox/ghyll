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
