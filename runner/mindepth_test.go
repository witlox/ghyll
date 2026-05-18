package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestEveryRequirementMeetsMinDepth_NoStoreUnevaluated(t *testing.T) {
	res, err := EvaluateEveryRequirementMeetsMinDepth(context.Background(), Clause{
		Args: map[string]any{"arrow-id": "A1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Unevaluated {
		t.Errorf("expected Unevaluated when no store attached; got %+v", res)
	}
	if res.Reason == "" {
		t.Error("Unevaluated must carry a Reason")
	}
}

func TestEveryRequirementMeetsMinDepth_NoRequirementsIsUnevaluated(t *testing.T) {
	// F13: empty requirements set is operator misconfiguration; the
	// evaluator must surface it, not silently pass.
	store := NewClassificationsStore()
	ctx := WithClassificationsStore(context.Background(), store)
	res, _ := EvaluateEveryRequirementMeetsMinDepth(ctx, Clause{
		Args: map[string]any{"arrow-id": "A1"},
	})
	if !res.Unevaluated {
		t.Errorf("no requirements should produce Unevaluated; got %+v", res)
	}
}

func TestEveryRequirementMeetsMinDepth_NoRequirementsAllowEmpty(t *testing.T) {
	// F13: operator can opt into trivial-pass via allow-empty=true.
	store := NewClassificationsStore()
	ctx := WithClassificationsStore(context.Background(), store)
	res, _ := EvaluateEveryRequirementMeetsMinDepth(ctx, Clause{
		Args: map[string]any{"arrow-id": "A1", "allow-empty": true},
	})
	if !res.Pass {
		t.Errorf("allow-empty=true should produce pass; got %+v", res)
	}
}

func TestEveryRequirementMeetsMinDepth_EmptyArrowIDErrors(t *testing.T) {
	// F25: writers reject empty arrowID; reader should too.
	_, err := EvaluateEveryRequirementMeetsMinDepth(context.Background(), Clause{
		Args: map[string]any{"arrow-id": "   "},
	})
	if err == nil {
		t.Error("whitespace-only arrow-id should error")
	}
}

func TestEveryRequirementMeetsMinDepth_SanitizesOperatorFreeText(t *testing.T) {
	// F11: Description and Evidence with embedded newlines must be
	// escaped before flowing into Result.Details.
	store := NewClassificationsStore()
	_ = store.DeclareRequirement("A1", Requirement{
		ID: "R1", MinDepth: DepthRankRealistic,
		Description: "real-line\n[CRITICAL] forged",
	})
	_ = store.RecordClassification("A1", Classification{
		RequirementID: "R1", Observed: DepthRankMocked,
		Evidence: "evidence\n[INFO] forged",
	})
	ctx := WithClassificationsStore(context.Background(), store)
	res, _ := EvaluateEveryRequirementMeetsMinDepth(ctx, Clause{
		Args: map[string]any{"arrow-id": "A1"},
	})
	below, _ := res.Details["below-min"].([]map[string]any)
	if len(below) != 1 {
		t.Fatalf("expected 1 below-min; got %d", len(below))
	}
	evidence, _ := below[0]["evidence"].(string)
	if !strings.Contains(evidence, `\n`) {
		t.Errorf("evidence should have escaped newline; got %q", evidence)
	}
	if strings.Contains(evidence, "\n") {
		t.Errorf("evidence should NOT contain raw newline; got %q", evidence)
	}
}

func TestEveryRequirementMeetsMinDepth_AllMeetMin(t *testing.T) {
	store := NewClassificationsStore()
	_ = store.DeclareRequirement("A1", Requirement{ID: "R1", MinDepth: DepthRankShallow, Description: "test req R1"})
	_ = store.DeclareRequirement("A1", Requirement{ID: "R2", MinDepth: DepthRankMocked, Description: "test req R2"})
	_ = store.RecordClassification("A1", Classification{RequirementID: "R1", Observed: DepthRankRealistic})
	_ = store.RecordClassification("A1", Classification{RequirementID: "R2", Observed: DepthRankMocked})
	ctx := WithClassificationsStore(context.Background(), store)
	res, _ := EvaluateEveryRequirementMeetsMinDepth(ctx, Clause{
		Args: map[string]any{"arrow-id": "A1"},
	})
	if !res.Pass {
		t.Errorf("all-meet should pass; got %+v", res.Details)
	}
}

func TestEveryRequirementMeetsMinDepth_BelowMinFails(t *testing.T) {
	store := NewClassificationsStore()
	_ = store.DeclareRequirement("A1", Requirement{ID: "R1", MinDepth: DepthRankRealistic, Description: "checkout integration"})
	_ = store.RecordClassification("A1", Classification{
		RequirementID: "R1", Observed: DepthRankMocked, Evidence: "uses pg mock",
	})
	ctx := WithClassificationsStore(context.Background(), store)
	res, _ := EvaluateEveryRequirementMeetsMinDepth(ctx, Clause{
		Args: map[string]any{"arrow-id": "A1"},
	})
	if res.Pass {
		t.Errorf("below-min should fail; got %+v", res.Details)
	}
	below, _ := res.Details["below-min"].([]map[string]any)
	if len(below) != 1 {
		t.Errorf("below-min len = %d; want 1", len(below))
	}
}

func TestEveryRequirementMeetsMinDepth_UnclassifiedIsUnevaluated(t *testing.T) {
	store := NewClassificationsStore()
	_ = store.DeclareRequirement("A1", Requirement{ID: "R1", MinDepth: DepthRankShallow, Description: "test req R1"})
	_ = store.DeclareRequirement("A1", Requirement{ID: "R2", MinDepth: DepthRankShallow, Description: "test req R2"})
	// Classify only R1.
	_ = store.RecordClassification("A1", Classification{RequirementID: "R1", Observed: DepthRankShallow})
	ctx := WithClassificationsStore(context.Background(), store)
	res, _ := EvaluateEveryRequirementMeetsMinDepth(ctx, Clause{
		Args: map[string]any{"arrow-id": "A1"},
	})
	if !res.Unevaluated {
		t.Errorf("partial classification should produce Unevaluated; got %+v", res)
	}
	unc, _ := res.Details["unclassified"].([]string)
	if len(unc) != 1 || unc[0] != "R2" {
		t.Errorf("unclassified = %v; want [R2]", unc)
	}
}

func TestEveryRequirementMeetsMinDepth_MissingArrowID(t *testing.T) {
	_, err := EvaluateEveryRequirementMeetsMinDepth(context.Background(), Clause{
		Args: map[string]any{},
	})
	if err == nil {
		t.Error("missing arrow-id should error")
	}
}

func TestClassificationsStore_RecordRequiresDeclare(t *testing.T) {
	store := NewClassificationsStore()
	err := store.RecordClassification("A1", Classification{
		RequirementID: "R1", Observed: DepthRankShallow,
	})
	if !errors.Is(err, ErrRequirementUnknown) {
		t.Errorf("undeclared req should error; got %v", err)
	}
}

func TestClassificationsStore_DuplicateRequirementRefused(t *testing.T) {
	store := NewClassificationsStore()
	_ = store.DeclareRequirement("A1", Requirement{ID: "R1", MinDepth: DepthRankShallow, Description: "test req R1"})
	err := store.DeclareRequirement("A1", Requirement{ID: "R1", MinDepth: DepthRankShallow, Description: "test req R1"})
	if !errors.Is(err, ErrRequirementDuplicateID) {
		t.Errorf("dup requirement should error; got %v", err)
	}
}

func TestClassificationsStore_RecordOverwrites(t *testing.T) {
	// Re-classification on remediation re-run overwrites the prior
	// observation.
	store := NewClassificationsStore()
	_ = store.DeclareRequirement("A1", Requirement{ID: "R1", MinDepth: DepthRankShallow, Description: "test req R1"})
	_ = store.RecordClassification("A1", Classification{RequirementID: "R1", Observed: DepthRankShallow})
	if err := store.RecordClassification("A1", Classification{
		RequirementID: "R1", Observed: DepthRankRealistic,
	}); err != nil {
		t.Errorf("re-record should overwrite, not error: %v", err)
	}
	cl := store.ClassificationsForArrow("A1")
	if len(cl) != 1 || cl[0].Observed != DepthRankRealistic {
		t.Errorf("re-record didn't overwrite: %+v", cl)
	}
}

func TestWithClassificationsStore_NestedPanics(t *testing.T) {
	ctx := WithClassificationsStore(context.Background(), NewClassificationsStore())
	defer func() {
		if r := recover(); r == nil {
			t.Error("nested WithClassificationsStore should panic")
		}
	}()
	_ = WithClassificationsStore(ctx, NewClassificationsStore())
}
