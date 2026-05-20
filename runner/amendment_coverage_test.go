package runner

import "testing"

// Tier 3 coverage push.

func TestScenario_AmendmentQueue_DrainedCount_TracksDrains(t *testing.T) {
	q := NewAmendmentQueue()
	if q.DrainedCount() != 0 {
		t.Error("empty queue DrainedCount != 0")
	}
	for _, id := range []string{"amend-1", "amend-2"} {
		if err := q.Enqueue(AmendmentRequest{
			ID: id, Reason: AmendmentReasonMissingCrossContextSpec,
			SourceArrow: "A", TargetRole: "architect",
			Contexts:    []string{"ctxA", "ctxB"},
			FindingIDs:  []string{"F1"},
			Description: "r",
		}); err != nil {
			t.Fatalf("Enqueue %s: %v", id, err)
		}
	}
	if drained := q.Drain(); len(drained) != 2 {
		t.Errorf("Drain returned %d; want 2", len(drained))
	}
	if got := q.DrainedCount(); got != 2 {
		t.Errorf("DrainedCount = %d; want 2", got)
	}
}

func TestScenario_AmendmentQueue_LoadDrained_BoundsRedraining(t *testing.T) {
	q := NewAmendmentQueue()
	q.LoadDrained("amend-pre-loaded")
	if got := q.DrainedCount(); got != 1 {
		t.Errorf("LoadDrained DrainedCount = %d; want 1", got)
	}
	// Empty id is a no-op.
	q.LoadDrained("")
	if got := q.DrainedCount(); got != 1 {
		t.Errorf("after empty load: %d; want 1", got)
	}
}
