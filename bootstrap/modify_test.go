package bootstrap

import (
	"errors"
	"testing"

	"github.com/witlox/ghyll/catalogue"
)

// loadCatalogue loads the shipped concept set for use in modify tests.
func loadCatalogue(t *testing.T) *catalogue.Catalogue {
	t.Helper()
	cat, err := catalogue.Load("../gates/concepts")
	if err != nil {
		t.Fatalf("load catalogue: %v", err)
	}
	return cat
}

func TestCheckModification_NumericRaiseAllowed(t *testing.T) {
	cat := loadCatalogue(t)
	original := map[string]any{
		"threshold": 0.7,
	}
	proposed := map[string]any{
		"threshold": 0.85,
	}
	if err := CheckModification("mutation-score", original, proposed, cat); err != nil {
		t.Errorf("raise 0.7→0.85 should be allowed; got %v", err)
	}
}

func TestCheckModification_NumericLowerRefused(t *testing.T) {
	cat := loadCatalogue(t)
	original := map[string]any{
		"threshold": 0.7,
	}
	proposed := map[string]any{
		"threshold": 0.5,
	}
	err := CheckModification("mutation-score", original, proposed, cat)
	if !errors.Is(err, ErrModifyWeakening) {
		t.Errorf("lower 0.7→0.5 should return ErrModifyWeakening; got %v", err)
	}
}

func TestCheckModification_NumericEqualAllowed(t *testing.T) {
	cat := loadCatalogue(t)
	original := map[string]any{
		"threshold": 0.7,
	}
	proposed := map[string]any{
		"threshold": 0.7,
	}
	if err := CheckModification("mutation-score", original, proposed, cat); err != nil {
		t.Errorf("equal 0.7=0.7 should be allowed; got %v", err)
	}
}

func TestCheckModification_NumericIntegerCompatibility(t *testing.T) {
	cat := loadCatalogue(t)
	// Integers and floats should compare correctly.
	original := map[string]any{"threshold": 1}
	proposed := map[string]any{"threshold": 0.99}
	err := CheckModification("mutation-score", original, proposed, cat)
	if !errors.Is(err, ErrModifyWeakening) {
		t.Errorf("integer 1 to float 0.99 should weaken; got %v", err)
	}
}

func TestCheckModification_SeverityRaiseAllowed(t *testing.T) {
	cat := loadCatalogue(t)
	original := map[string]any{
		"scope":              "src/**",
		"language":           "go",
		"severity-threshold": "medium",
	}
	proposed := map[string]any{
		"severity-threshold": "high",
	}
	if err := CheckModification("lint-clean", original, proposed, cat); err != nil {
		t.Errorf("severity medium→high should be allowed (stricter); got %v", err)
	}
}

func TestCheckModification_SeverityLowerRefused(t *testing.T) {
	cat := loadCatalogue(t)
	original := map[string]any{
		"scope":              "src/**",
		"language":           "go",
		"severity-threshold": "high",
	}
	proposed := map[string]any{
		"severity-threshold": "medium",
	}
	err := CheckModification("lint-clean", original, proposed, cat)
	if !errors.Is(err, ErrModifyWeakening) {
		t.Errorf("severity high→medium should weaken; got %v", err)
	}
}

func TestCheckModification_SeverityOutsideEnum(t *testing.T) {
	cat := loadCatalogue(t)
	original := map[string]any{"severity-threshold": "medium"}
	proposed := map[string]any{"severity-threshold": "supercritical"}
	err := CheckModification("lint-clean", original, proposed, cat)
	if err == nil {
		t.Error("severity outside enum should fail validation")
	}
	if errors.Is(err, ErrModifyWeakening) {
		t.Errorf("error should be about enum membership, not weakening; got %v", err)
	}
}

func TestCheckModification_UnknownConcept(t *testing.T) {
	cat := loadCatalogue(t)
	original := map[string]any{}
	proposed := map[string]any{"x": 1}
	err := CheckModification("not-a-concept", original, proposed, cat)
	if err == nil {
		t.Fatal("unknown concept should fail")
	}
}

func TestCheckModification_UnknownArgument(t *testing.T) {
	cat := loadCatalogue(t)
	original := map[string]any{
		"threshold": 0.7,
	}
	proposed := map[string]any{
		"threshold": 0.8,
		"bogus":     "x",
	}
	err := CheckModification("mutation-score", original, proposed, cat)
	if err == nil {
		t.Fatal("unknown argument should fail")
	}
}

func TestCheckModification_NewArgumentSkipsCheck(t *testing.T) {
	cat := loadCatalogue(t)
	// Operator provides an argument that wasn't in the original; this
	// is "extend" semantics (adding a new constraint). Modify-rule
	// should not flag the new arg as weakening.
	original := map[string]any{
		"threshold": 0.7,
	}
	proposed := map[string]any{
		"threshold":            0.85,
		"timeout-per-mutation": "60s",
	}
	if err := CheckModification("mutation-score", original, proposed, cat); err != nil {
		t.Errorf("adding a new arg + raising threshold should be allowed; got %v", err)
	}
}

func TestCheckModification_StringEqualityForUnsupportedType(t *testing.T) {
	cat := loadCatalogue(t)
	// scope is path-glob; for v1, equality is required.
	original := map[string]any{
		"scope":    "src/**",
		"language": "go",
	}
	proposed := map[string]any{
		"scope": "src/main.go", // different value; type=path-glob → unsupported monotonicity
	}
	err := CheckModification("compiles", original, proposed, cat)
	if !errors.Is(err, ErrModifyUnsupportedType) {
		t.Errorf("path-glob change should return ErrModifyUnsupportedType; got %v", err)
	}
}

func TestCheckModification_NilCatalogue(t *testing.T) {
	err := CheckModification("anything", nil, nil, nil)
	if err == nil {
		t.Error("nil catalogue should fail")
	}
}
