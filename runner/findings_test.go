package runner

import (
	"context"
	"errors"
	"testing"
)

func TestFindingsStore_RaiseAndGet(t *testing.T) {
	s := NewFindingsStore()
	if err := s.Raise(FindingRecord{
		ID:           "F1",
		ArrowID:      "A1",
		Type:         FindingTypeLocalBug,
		Severity:     SeverityHigh,
		Status:       FindingStatusOpen,
		Description:  "off-by-one in pagination",
		RaisedByRole: "integrator",
	}); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Get("F1")
	if !ok {
		t.Fatal("Get returned !ok")
	}
	if got.Type != FindingTypeLocalBug {
		t.Errorf("Type = %v; want local-bug", got.Type)
	}
	if got.Severity != SeverityHigh {
		t.Errorf("Severity = %d; want %d", got.Severity, SeverityHigh)
	}
}

func TestFindingsStore_DuplicateID(t *testing.T) {
	s := NewFindingsStore()
	_ = s.Raise(FindingRecord{ID: "F1", ArrowID: "A1", Type: FindingTypeLocalBug})
	err := s.Raise(FindingRecord{ID: "F1", ArrowID: "A1", Type: FindingTypeLocalBug})
	if !errors.Is(err, ErrFindingDuplicateID) {
		t.Errorf("err = %v; want ErrFindingDuplicateID", err)
	}
}

func TestFindingsStore_EmptyFields(t *testing.T) {
	s := NewFindingsStore()
	if err := s.Raise(FindingRecord{ArrowID: "A", Type: FindingTypeLocalBug}); !errors.Is(err, ErrFindingIDEmpty) {
		t.Errorf("empty ID: got %v", err)
	}
	if err := s.Raise(FindingRecord{ID: "F", Type: FindingTypeLocalBug}); !errors.Is(err, ErrFindingArrowIDEmpty) {
		t.Errorf("empty ArrowID: got %v", err)
	}
	if err := s.Raise(FindingRecord{ID: "F", ArrowID: "A"}); !errors.Is(err, ErrFindingTypeEmpty) {
		t.Errorf("empty Type: got %v", err)
	}
}

func TestFindingsStore_Transition(t *testing.T) {
	s := NewFindingsStore()
	_ = s.Raise(FindingRecord{
		ID: "F1", ArrowID: "A1", Type: FindingTypeLocalBug, Status: FindingStatusOpen,
	})
	if err := s.Transition("F1", FindingStatusResolved); err != nil {
		t.Errorf("open → resolved should be legal: %v", err)
	}
	got, _ := s.Get("F1")
	if got.Status != FindingStatusResolved {
		t.Errorf("status = %v; want resolved", got.Status)
	}
	// Reopen is allowed.
	if err := s.Transition("F1", FindingStatusOpen); err != nil {
		t.Errorf("resolved → open (reopen): %v", err)
	}
}

func TestFindingsStore_InvalidTransition(t *testing.T) {
	s := NewFindingsStore()
	_ = s.Raise(FindingRecord{
		ID: "F1", ArrowID: "A1", Type: FindingTypeLocalBug, Status: FindingStatusOpen,
	})
	// open → unevaluated is not in the legal set (per validFindingTransition).
	err := s.Transition("F1", FindingStatusUnevaluated)
	if !errors.Is(err, ErrFindingInvalidStatus) {
		t.Errorf("open → unevaluated should be invalid: %v", err)
	}
}

func TestFindingsStore_UnknownID(t *testing.T) {
	s := NewFindingsStore()
	err := s.Transition("nope", FindingStatusResolved)
	if !errors.Is(err, ErrFindingUnknownID) {
		t.Errorf("unknown ID: got %v", err)
	}
}

func TestFindingsStore_ForArrow(t *testing.T) {
	s := NewFindingsStore()
	for _, id := range []string{"F3", "F1", "F2"} {
		_ = s.Raise(FindingRecord{ID: id, ArrowID: "A1", Type: FindingTypeLocalBug})
	}
	got := s.ForArrow("A1")
	if len(got) != 3 {
		t.Fatalf("len = %d; want 3", len(got))
	}
	// Stable sort by ID.
	want := []string{"F1", "F2", "F3"}
	for i, r := range got {
		if r.ID != want[i] {
			t.Errorf("[%d] = %q; want %q", i, r.ID, want[i])
		}
	}
}

func TestFindingStatus_String(t *testing.T) {
	cases := map[FindingStatus]string{
		FindingStatusOpen:         "open",
		FindingStatusRunning:      "running",
		FindingStatusResolved:     "resolved",
		FindingStatusAcceptedRisk: "accepted-risk",
		FindingStatusUnevaluated:  "unevaluated",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("%v.String() = %q; want %q", s, got, want)
		}
	}
}

func TestParseFindingStatus(t *testing.T) {
	cases := map[string]FindingStatus{
		"open":          FindingStatusOpen,
		"running":       FindingStatusRunning,
		"resolved":      FindingStatusResolved,
		"accepted-risk": FindingStatusAcceptedRisk,
		"unevaluated":   FindingStatusUnevaluated,
	}
	for in, want := range cases {
		got, err := ParseFindingStatus(in)
		if err != nil {
			t.Errorf("ParseFindingStatus(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("ParseFindingStatus(%q) = %v; want %v", in, got, want)
		}
	}
	if _, err := ParseFindingStatus("bogus"); err == nil {
		t.Error("bogus status should error")
	}
}

func TestNoOpenFinding_Pass(t *testing.T) {
	store := NewFindingsStore()
	_ = store.Raise(FindingRecord{
		ID: "F1", ArrowID: "A1", Type: FindingTypeLocalBug,
		Severity: SeverityHigh, Status: FindingStatusResolved,
	})
	ctx := WithFindingsStore(context.Background(), store)
	res, err := EvaluateNoOpenFinding(ctx, Clause{
		Args: map[string]any{"arrow-id": "A1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pass {
		t.Errorf("expected pass (only resolved finding); got %+v", res.Details)
	}
}

func TestNoOpenFinding_BlockingHigh(t *testing.T) {
	store := NewFindingsStore()
	_ = store.Raise(FindingRecord{
		ID: "F1", ArrowID: "A1", Type: FindingTypeLocalBug,
		Severity: SeverityHigh, Status: FindingStatusOpen,
	})
	ctx := WithFindingsStore(context.Background(), store)
	res, _ := EvaluateNoOpenFinding(ctx, Clause{
		Args: map[string]any{"arrow-id": "A1"},
	})
	if res.Pass {
		t.Errorf("expected fail; got %+v", res.Details)
	}
	blocking, _ := res.Details["blocking-findings"].([]map[string]any)
	if len(blocking) != 1 {
		t.Errorf("blocking len = %d; want 1", len(blocking))
	}
}

func TestNoOpenFinding_BelowThresholdAllowed(t *testing.T) {
	store := NewFindingsStore()
	_ = store.Raise(FindingRecord{
		ID: "F1", ArrowID: "A1", Type: FindingTypeLocalBug,
		Severity: SeverityLow, Status: FindingStatusOpen,
	})
	ctx := WithFindingsStore(context.Background(), store)
	res, _ := EvaluateNoOpenFinding(ctx, Clause{
		Args: map[string]any{
			"arrow-id":           "A1",
			"severity-threshold": "high",
		},
	})
	if !res.Pass {
		t.Errorf("low finding with high threshold should pass; got %+v", res.Details)
	}
}

func TestNoOpenFinding_UnevaluatedAlwaysBlocks(t *testing.T) {
	// validation-pass-3 F25: unevaluated severity always blocks
	// regardless of rank.
	store := NewFindingsStore()
	_ = store.Raise(FindingRecord{
		ID: "F1", ArrowID: "A1", Type: FindingTypeLocalBug,
		Severity: SeverityLow, // would be below medium threshold
		Status:   FindingStatusUnevaluated,
	})
	ctx := WithFindingsStore(context.Background(), store)
	res, _ := EvaluateNoOpenFinding(ctx, Clause{
		Args: map[string]any{"arrow-id": "A1"},
	})
	if res.Pass {
		t.Errorf("unevaluated should block regardless of severity; got %+v", res.Details)
	}
}

func TestNoOpenFinding_NoStoreUnevaluated(t *testing.T) {
	// Missing FindingsStore in ctx → Unevaluated (can't decide).
	res, err := EvaluateNoOpenFinding(context.Background(), Clause{
		Args: map[string]any{"arrow-id": "A1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Unevaluated {
		t.Errorf("missing store should produce Unevaluated; got %+v", res)
	}
	if res.Reason == "" {
		t.Error("Unevaluated requires Reason (F11)")
	}
}

func TestNoOpenFinding_MissingArrowID(t *testing.T) {
	_, err := EvaluateNoOpenFinding(context.Background(), Clause{
		Args: map[string]any{},
	})
	if err == nil {
		t.Error("missing arrow-id should error")
	}
}
