package runner

import (
	"errors"
	"strings"
	"testing"
)

func TestAmendmentRequest_Validate(t *testing.T) {
	good := AmendmentRequest{
		ID:          "amend-1",
		Reason:      AmendmentReasonMissingCrossContextSpec,
		SourceArrow: "integrator→exit/contextA-contextB",
		TargetRole:  "analyst",
		Contexts:    []string{"contextA", "contextB"},
		FindingIDs:  []string{"F1"},
	}
	if err := good.Validate(); err != nil {
		t.Errorf("good amendment should validate: %v", err)
	}

	bad := []struct {
		name string
		mut  func(*AmendmentRequest)
	}{
		{"empty ID", func(r *AmendmentRequest) { r.ID = "" }},
		{"empty Reason", func(r *AmendmentRequest) { r.Reason = "" }},
		{"empty SourceArrow", func(r *AmendmentRequest) { r.SourceArrow = "" }},
		{"empty TargetRole", func(r *AmendmentRequest) { r.TargetRole = "" }},
		{"empty FindingIDs", func(r *AmendmentRequest) { r.FindingIDs = nil }},
		{"one context", func(r *AmendmentRequest) { r.Contexts = []string{"contextA"} }},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			r := good
			c.mut(&r)
			if err := r.Validate(); err == nil {
				t.Errorf("%s: should fail validation", c.name)
			}
		})
	}
}

func TestAmendmentQueue_Enqueue(t *testing.T) {
	q := NewAmendmentQueue()
	r := AmendmentRequest{
		ID: "amend-1", Reason: AmendmentReasonMissingCrossContextSpec,
		SourceArrow: "A", TargetRole: "analyst",
		Contexts: []string{"c1", "c2"}, FindingIDs: []string{"F1"},
	}
	if err := q.Enqueue(r); err != nil {
		t.Fatal(err)
	}
	if q.Len() != 1 {
		t.Errorf("Len = %d; want 1", q.Len())
	}
}

func TestAmendmentQueue_DuplicateIDRefused(t *testing.T) {
	q := NewAmendmentQueue()
	r := AmendmentRequest{
		ID: "amend-1", Reason: AmendmentReasonMissingCrossContextSpec,
		SourceArrow: "A", TargetRole: "analyst",
		Contexts: []string{"c1", "c2"}, FindingIDs: []string{"F1"},
	}
	_ = q.Enqueue(r)
	err := q.Enqueue(r)
	if !errors.Is(err, ErrAmendmentDuplicateID) {
		t.Errorf("got %v; want ErrAmendmentDuplicateID", err)
	}
}

func TestAmendmentQueue_DrainEmptiesAndReturnsAll(t *testing.T) {
	q := NewAmendmentQueue()
	for i, id := range []string{"amend-1", "amend-2", "amend-3"} {
		_ = q.Enqueue(AmendmentRequest{
			ID: id, Reason: AmendmentReasonMissingCrossContextSpec,
			SourceArrow: "A", TargetRole: "analyst",
			Contexts: []string{"c1", "c2"}, FindingIDs: []string{string(rune('a' + i))},
		})
	}
	drained := q.Drain()
	if len(drained) != 3 {
		t.Errorf("drained len = %d; want 3", len(drained))
	}
	if q.Len() != 0 {
		t.Error("queue should be empty after Drain")
	}
	// Re-enqueue with the same IDs works (Drain cleared the byID map).
	if err := q.Enqueue(drained[0]); err != nil {
		t.Errorf("re-enqueue after drain: %v", err)
	}
}

func TestAmendmentQueue_PendingDoesNotClear(t *testing.T) {
	q := NewAmendmentQueue()
	_ = q.Enqueue(AmendmentRequest{
		ID: "amend-1", Reason: AmendmentReasonMissingCrossContextSpec,
		SourceArrow: "A", TargetRole: "analyst",
		Contexts: []string{"c1", "c2"}, FindingIDs: []string{"F1"},
	})
	_ = q.Pending()
	if q.Len() != 1 {
		t.Errorf("Pending must not clear; Len = %d; want 1", q.Len())
	}
}

func TestPendingAmendments_FindsOpenMissingCrossContext(t *testing.T) {
	store := NewFindingsStore()
	_ = store.Raise(FindingRecord{
		ID: "F1", ArrowID: "A1",
		Type:        FindingTypeMissingCrossContextSpec,
		Status:      FindingStatusOpen,
		Description: "payment + identity contexts don't agree on user-id format",
	})
	_ = store.Raise(FindingRecord{
		ID: "F2", ArrowID: "A1",
		Type:   FindingTypeLocalBug, // not an amendment trigger
		Status: FindingStatusOpen,
	})
	_ = store.Raise(FindingRecord{
		ID: "F3", ArrowID: "A1",
		Type:   FindingTypeMissingCrossContextSpec,
		Status: FindingStatusResolved, // not open → no trigger
	})
	id := 0
	idGen := func() string {
		id++
		return "amend-test-" + string(rune('0'+id))
	}
	got := PendingAmendments(store, "A1", []string{"payment", "identity"}, idGen)
	if len(got) != 1 {
		t.Fatalf("got %d amendments; want 1", len(got))
	}
	if got[0].Reason != AmendmentReasonMissingCrossContextSpec {
		t.Errorf("Reason = %v; want missing-cross-context-spec", got[0].Reason)
	}
	if got[0].SourceArrow != "A1" {
		t.Errorf("SourceArrow = %q; want A1", got[0].SourceArrow)
	}
	if got[0].TargetRole != "analyst" {
		t.Errorf("TargetRole = %q; want analyst", got[0].TargetRole)
	}
	if len(got[0].Contexts) != 2 {
		t.Errorf("Contexts len = %d; want 2", len(got[0].Contexts))
	}
	if got[0].FindingIDs[0] != "F1" {
		t.Errorf("FindingIDs = %v; want [F1]", got[0].FindingIDs)
	}
}

func TestPendingAmendments_NilStoreReturnsNil(t *testing.T) {
	if got := PendingAmendments(nil, "A1", nil, nil); got != nil {
		t.Errorf("nil store should return nil; got %v", got)
	}
}

func TestPendingAmendments_EmptyArrowIDReturnsNil(t *testing.T) {
	store := NewFindingsStore()
	if got := PendingAmendments(store, "", nil, nil); got != nil {
		t.Errorf("empty arrow ID should return nil; got %v", got)
	}
}

func TestFormatAmendmentSummary(t *testing.T) {
	r := AmendmentRequest{
		ID: "amend-1", Reason: AmendmentReasonMissingCrossContextSpec,
		SourceArrow: "integrator→exit",
		TargetRole:  "analyst",
		Contexts:    []string{"payment", "identity"},
		FindingIDs:  []string{"F1"},
		Description: "user-id format mismatch",
		CreatedAt:   "2026-05-18T12:00:00Z",
	}
	s := FormatAmendmentSummary(r)
	for _, want := range []string{
		"amend-1",
		"missing-cross-context-spec",
		"integrator→exit",
		"analyst",
		"payment",
		"identity",
		"F1",
		"user-id format mismatch",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("summary %q missing %q", s, want)
		}
	}
}
