package runner

import (
	"context"
	"errors"
	"testing"
	"time"
)

func sampleAmendment() AmendmentRequest {
	return AmendmentRequest{
		ID:          "amend-1",
		Reason:      AmendmentReasonMissingCrossContextSpec,
		SourceArrow: "A1",
		TargetRole:  "analyst",
		Contexts:    []string{"checkout", "payments"},
		Description: "checkout-payments cross-context contract missing",
		FindingIDs:  []string{"F1"},
		CreatedAt:   "2026-05-19T12:00:00Z",
	}
}

func sampleNewArrow(id string) ArrowDefinition {
	return ArrowDefinition{
		ID:         id,
		SourceRole: "integrator",
		TargetRole: "analyst",
		Stratum:    "L1",
		Context:    "checkout",
		Clauses:    []Clause{{Concept: "no-todo-marker", ClauseID: "C1"}},
	}
}

func newCommitterFixture(t *testing.T) *AmendmentCommitter {
	t.Helper()
	return &AmendmentCommitter{
		Grid:   NewGrid(),
		Passes: NewPassRegistry(),
		Bus:    NewOperatorBus(),
		Queue:  NewAmendmentQueue(),
	}
}

func TestScenario_AmendmentCommit_AppendsArrowAndBumpsVersion(t *testing.T) {
	c := newCommitterFixture(t)
	v0 := c.Grid.Version()

	res, err := c.Commit(context.Background(), CommitRequest{
		Amendment: sampleAmendment(),
		NewArrows: []ArrowDefinition{sampleNewArrow("A2-new")},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if res.GridVersionBefore != v0 {
		t.Errorf("VersionBefore = %d; want %d", res.GridVersionBefore, v0)
	}
	if res.GridVersionAfter != v0+1 {
		t.Errorf("VersionAfter = %d; want %d", res.GridVersionAfter, v0+1)
	}
	if len(res.AppendedArrows) != 1 || res.AppendedArrows[0] != "A2-new" {
		t.Errorf("AppendedArrows = %v; want [A2-new]", res.AppendedArrows)
	}
}

func TestScenario_AmendmentCommit_AbortsInFlightPassOnSourceArrow(t *testing.T) {
	c := newCommitterFixture(t)
	locks := NewRoleContextLockTable()
	pass, err := OpenPass(PassOptions{
		PassID: "P-affected", Role: "implementer", Context: "checkout",
		ArrowID: "A1", LockTable: locks, Bus: c.Bus,
	})
	if err != nil {
		t.Fatal(err)
	}
	c.Passes.Register(pass)
	defer c.Passes.Unregister(pass.ID())

	_, err = c.Commit(context.Background(), CommitRequest{
		Amendment: sampleAmendment(), // SourceArrow == "A1"
		NewArrows: []ArrowDefinition{sampleNewArrow("A2-new")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if pass.State() != PassStateAborted {
		t.Fatalf("pass state = %q; want aborted", pass.State())
	}
	if _, held := locks.InspectHolder("implementer", "checkout"); held {
		t.Fatal("pass abort should have released the lock")
	}
}

func TestScenario_AmendmentCommit_DoesNotAbortDisjointPasses(t *testing.T) {
	c := newCommitterFixture(t)
	locks := NewRoleContextLockTable()
	// A pass on a different arrow stays running.
	pass, _ := OpenPass(PassOptions{
		PassID: "P-other", Role: "implementer", Context: "payments",
		ArrowID: "A99-different", LockTable: locks,
	})
	defer pass.Close("done")
	c.Passes.Register(pass)
	defer c.Passes.Unregister(pass.ID())

	_, err := c.Commit(context.Background(), CommitRequest{
		Amendment: sampleAmendment(),
		NewArrows: []ArrowDefinition{sampleNewArrow("A2-new")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if pass.State() != PassStateOpen {
		t.Fatalf("disjoint pass should still be open; state = %q", pass.State())
	}
}

func TestScenario_AmendmentCommit_PublishesAmendmentDrainedEvent(t *testing.T) {
	c := newCommitterFixture(t)
	var seen []OperatorEvent
	c.Bus.Subscribe(func(e OperatorEvent) { seen = append(seen, e) })

	_, err := c.Commit(context.Background(), CommitRequest{
		Amendment: sampleAmendment(),
		NewArrows: []ArrowDefinition{sampleNewArrow("A2-new")},
	})
	if err != nil {
		t.Fatal(err)
	}
	var sawDrained bool
	for _, e := range seen {
		if e.Kind == OpEventAmendmentDrained {
			sawDrained = true
		}
	}
	if !sawDrained {
		t.Fatal("expected amendment-drained OperatorEvent")
	}
}

func TestScenario_AmendmentCommit_InvalidAmendmentRejected(t *testing.T) {
	c := newCommitterFixture(t)
	bad := sampleAmendment()
	bad.ID = "" // invalid
	_, err := c.Commit(context.Background(), CommitRequest{Amendment: bad})
	if !errors.Is(err, ErrAmendmentCommitInvalid) {
		t.Fatalf("got %v; want ErrAmendmentCommitInvalid", err)
	}
}

func TestScenario_AmendmentCommit_NoArrowsValid_AbortsPassesOnly(t *testing.T) {
	// gates.md §3.7 allows an amendment that doesn't introduce
	// new arrows — the analyst may decide the original arrow is
	// valid and just wants in-flight passes to restart. The grid
	// version stays put; affected passes abort.
	c := newCommitterFixture(t)
	locks := NewRoleContextLockTable()
	pass, _ := OpenPass(PassOptions{
		PassID: "P-affected", Role: "implementer", Context: "checkout",
		ArrowID: "A1", LockTable: locks,
	})
	c.Passes.Register(pass)
	defer c.Passes.Unregister(pass.ID())

	res, err := c.Commit(context.Background(), CommitRequest{
		Amendment: sampleAmendment(),
		NewArrows: nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.GridVersionAfter != res.GridVersionBefore {
		t.Errorf("grid version drifted with no new arrows: %d -> %d",
			res.GridVersionBefore, res.GridVersionAfter)
	}
	if pass.State() != PassStateAborted {
		t.Errorf("pass should be aborted; state = %q", pass.State())
	}
}

func TestScenario_AmendmentCommit_PartialAppendSurfacedInResult(t *testing.T) {
	c := newCommitterFixture(t)
	// First arrow appends; second collides with the first's ID
	// (Grid.Append rejects duplicates).
	dup := sampleNewArrow("A2-new")
	_, err := c.Commit(context.Background(), CommitRequest{
		Amendment: sampleAmendment(),
		NewArrows: []ArrowDefinition{sampleNewArrow("A2-new"), dup},
	})
	if err == nil {
		t.Fatal("expected duplicate-id error from Grid.Append")
	}
	// Grid version reflects the partial commit (one arrow appended).
	if c.Grid.Version() != 1 {
		t.Errorf("grid version = %d; want 1 (partial commit)", c.Grid.Version())
	}
}

// TestScenario_AmendmentCommit_PartialAppend_StillAbortsPasses pins
// the adversary-fix: pass abort runs BEFORE append, so a partial
// append still leaves the affected passes aborted (they can't run
// against the half-amended grid).
func TestScenario_AmendmentCommit_PartialAppend_StillAbortsPasses(t *testing.T) {
	c := newCommitterFixture(t)
	locks := NewRoleContextLockTable()
	pass, _ := OpenPass(PassOptions{
		PassID: "P-affected", Role: "implementer", Context: "checkout",
		ArrowID: "A1", LockTable: locks,
	})
	c.Passes.Register(pass)
	defer c.Passes.Unregister(pass.ID())

	dup := sampleNewArrow("A2-new")
	_, err := c.Commit(context.Background(), CommitRequest{
		Amendment: sampleAmendment(),
		NewArrows: []ArrowDefinition{sampleNewArrow("A2-new"), dup},
	})
	if err == nil {
		t.Fatal("expected partial-append error")
	}
	if pass.State() != PassStateAborted {
		t.Fatalf("pass should be aborted even on partial-commit; state = %q", pass.State())
	}
}

// TestScenario_AmendmentCommit_MarkDrained_FiresAmendmentEvent pins
// the persistence-fix: committing fires AmendmentEventDrain through
// the queue so the journal's handleAmendment writes drained_at.
func TestScenario_AmendmentCommit_MarkDrained_FiresAmendmentEvent(t *testing.T) {
	c := newCommitterFixture(t)
	// Pre-enqueue the amendment so MarkDrained has something to act on.
	if err := c.Queue.Enqueue(sampleAmendment()); err != nil {
		t.Fatal(err)
	}
	var observed []AmendmentEvent
	c.Queue.Observe(func(e AmendmentEvent) { observed = append(observed, e) })

	_, err := c.Commit(context.Background(), CommitRequest{
		Amendment: sampleAmendment(),
		NewArrows: []ArrowDefinition{sampleNewArrow("A2-new")},
	})
	if err != nil {
		t.Fatal(err)
	}
	var sawDrain bool
	for _, e := range observed {
		if e.Kind == AmendmentEventDrain && len(e.Drained) == 1 && e.Drained[0].ID == "amend-1" {
			sawDrain = true
		}
	}
	if !sawDrain {
		t.Fatal("committer must fire AmendmentEventDrain through the queue so journal persists drained_at")
	}
	if c.Queue.Len() != 0 {
		t.Fatalf("queue len after commit = %d; want 0 (amendment drained)", c.Queue.Len())
	}
}

// TestScenario_AmendmentCommit_MarkDrained_AbsentAmendmentSilent pins
// the idempotency: committing an amendment that was never enqueued
// (or already drained) is a silent no-op on the queue path; the
// grid + abort + bus event still fire.
func TestScenario_AmendmentCommit_MarkDrained_AbsentAmendmentSilent(t *testing.T) {
	c := newCommitterFixture(t)
	// Queue is empty.
	_, err := c.Commit(context.Background(), CommitRequest{
		Amendment: sampleAmendment(),
		NewArrows: []ArrowDefinition{sampleNewArrow("A2-new")},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestScenario_AmendmentCommit_ContextCancelAborts(t *testing.T) {
	c := newCommitterFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Commit(ctx, CommitRequest{
		Amendment: sampleAmendment(),
		NewArrows: []ArrowDefinition{sampleNewArrow("A2-new")},
	})
	if err == nil {
		t.Fatal("expected context-cancel error")
	}
}

func TestScenario_AmendmentCommit_NilGridErrors(t *testing.T) {
	c := &AmendmentCommitter{Passes: NewPassRegistry()}
	_, err := c.Commit(context.Background(), CommitRequest{Amendment: sampleAmendment()})
	if !errors.Is(err, ErrAmendmentCommitNoGrid) {
		t.Fatalf("got %v; want ErrAmendmentCommitNoGrid", err)
	}
}

func TestScenario_AmendmentCommit_NilPassesErrors(t *testing.T) {
	c := &AmendmentCommitter{Grid: NewGrid()}
	_, err := c.Commit(context.Background(), CommitRequest{Amendment: sampleAmendment()})
	if !errors.Is(err, ErrAmendmentCommitNoPasses) {
		t.Fatalf("got %v; want ErrAmendmentCommitNoPasses", err)
	}
}

func TestScenario_AmendmentCommit_NowClockOverride(t *testing.T) {
	pinned := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	c := newCommitterFixture(t)
	c.Now = func() time.Time { return pinned }
	res, err := c.Commit(context.Background(), CommitRequest{
		Amendment: sampleAmendment(),
		NewArrows: []ArrowDefinition{sampleNewArrow("A2-new")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.CommittedAt.Equal(pinned) {
		t.Fatalf("CommittedAt = %v; want %v", res.CommittedAt, pinned)
	}
}
