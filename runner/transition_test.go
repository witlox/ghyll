package runner

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckTransition_CompletePermits(t *testing.T) {
	err := CheckTransition("A1", "B1", ArrowStatusComplete, 0, 0)
	if err != nil {
		t.Errorf("Complete upstream should permit transition; got %v", err)
	}
}

func TestCheckTransition_BlockedRefuses(t *testing.T) {
	err := CheckTransition("A1", "B1", ArrowStatusBlocked, 3, 0)
	if err == nil {
		t.Fatal("Blocked upstream should refuse")
	}
	if !errors.Is(err, ErrTransitionRefused) {
		t.Errorf("errors.Is ErrTransitionRefused = false; want true")
	}
	tr := AsTransitionRefusal(err)
	if tr == nil {
		t.Fatal("AsTransitionRefusal returned nil")
	}
	if tr.Kind != KindTransitionRefused {
		t.Errorf("Kind = %q; want %q", tr.Kind, KindTransitionRefused)
	}
	if tr.UpstreamArrowID != "A1" {
		t.Errorf("UpstreamArrowID = %q; want A1", tr.UpstreamArrowID)
	}
	if tr.DownstreamArrowID != "B1" {
		t.Errorf("DownstreamArrowID = %q; want B1", tr.DownstreamArrowID)
	}
	if tr.UpstreamStatus != ArrowStatusBlocked {
		t.Errorf("UpstreamStatus = %s; want blocked", tr.UpstreamStatus)
	}
	if tr.BlockingClauses != 3 {
		t.Errorf("BlockingClauses = %d; want 3", tr.BlockingClauses)
	}
}

func TestCheckTransition_ProvisionalRefuses(t *testing.T) {
	// Scenario: Downstream attempts to start before upstream
	// complete — provisional upstream refuses.
	err := CheckTransition("A1", "B1", ArrowStatusProvisional, 2, 0)
	tr := AsTransitionRefusal(err)
	if tr == nil || tr.Kind != KindTransitionRefused {
		t.Fatalf("expected transition-refused; got %v", err)
	}
	if tr.UpstreamStatus != ArrowStatusProvisional {
		t.Errorf("UpstreamStatus = %s; want provisional", tr.UpstreamStatus)
	}
}

func TestCheckTransition_UnevaluatedRefuses(t *testing.T) {
	err := CheckTransition("A1", "B1", ArrowStatusUnevaluated, 1, 0)
	tr := AsTransitionRefusal(err)
	if tr == nil || tr.UpstreamStatus != ArrowStatusUnevaluated {
		t.Errorf("expected unevaluated refusal; got %v", err)
	}
}

func TestCheckTransition_InProgressRefuses(t *testing.T) {
	err := CheckTransition("A1", "B1", ArrowStatusInProgress, 5, 0)
	tr := AsTransitionRefusal(err)
	if tr == nil || tr.UpstreamStatus != ArrowStatusInProgress {
		t.Errorf("expected in-progress refusal; got %v", err)
	}
}

func TestCheckTransition_InvalidatedRefuses(t *testing.T) {
	// Scenario: Invalidated arrow refuses transitions.
	err := CheckTransition("A1", "B1", ArrowStatusInvalidated, 0, 5)
	if err == nil {
		t.Fatal("invalidated should refuse")
	}
	tr := AsTransitionRefusal(err)
	if tr == nil {
		t.Fatal("AsTransitionRefusal returned nil")
	}
	if tr.Kind != KindTransitionRefusedInvalidated {
		t.Errorf("Kind = %q; want %q", tr.Kind, KindTransitionRefusedInvalidated)
	}
	if tr.InvalidatingGridVersion != 5 {
		t.Errorf("InvalidatingGridVersion = %d; want 5", tr.InvalidatingGridVersion)
	}
	if !strings.Contains(err.Error(), "v5") {
		t.Errorf("error %q should mention the grid version", err)
	}
}

func TestCheckTransition_InvalidatedTrumpsComplete(t *testing.T) {
	// Defensive: an invalidated arrow should refuse even if its
	// status pre-invalidation was complete. The status field is
	// what the runner sees post-invalidation; that's
	// ArrowStatusInvalidated.
	err := CheckTransition("A1", "B1", ArrowStatusInvalidated, 0, 7)
	tr := AsTransitionRefusal(err)
	if tr == nil || tr.Kind != KindTransitionRefusedInvalidated {
		t.Errorf("invalidated must refuse with the invalidated-specific kind; got %v", err)
	}
}

func TestTransitionRefusal_ErrorString(t *testing.T) {
	tr := &TransitionRefusal{
		Kind:              KindTransitionRefused,
		UpstreamArrowID:   "analyst→architect/contextA",
		DownstreamArrowID: "architect→implementer/contextA",
		UpstreamStatus:    ArrowStatusBlocked,
		BlockingClauses:   2,
	}
	s := tr.Error()
	for _, want := range []string{
		"transition-refused",
		"analyst→architect/contextA",
		"architect→implementer/contextA",
		"blocked",
		"2 blocking",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("error %q missing %q", s, want)
		}
	}
}

func TestTransitionRefusal_InvalidatedErrorString(t *testing.T) {
	tr := &TransitionRefusal{
		Kind:                    KindTransitionRefusedInvalidated,
		UpstreamArrowID:         "A1",
		DownstreamArrowID:       "B1",
		UpstreamStatus:          ArrowStatusInvalidated,
		InvalidatingGridVersion: 3,
	}
	s := tr.Error()
	for _, want := range []string{
		"transition-refused-invalidated",
		"A1",
		"B1",
		"v3",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("error %q missing %q", s, want)
		}
	}
}

func TestAsTransitionRefusal_NotMatching(t *testing.T) {
	if got := AsTransitionRefusal(nil); got != nil {
		t.Error("AsTransitionRefusal(nil) should be nil")
	}
	if got := AsTransitionRefusal(errors.New("other")); got != nil {
		t.Error("AsTransitionRefusal of unrelated error should be nil")
	}
}
