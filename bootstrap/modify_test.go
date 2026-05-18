package bootstrap

import (
	"errors"
	"math"
	"strings"
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

func TestCheckModification_PathGlobNarrowingAccepted(t *testing.T) {
	// Per init.feature 184 outline: narrowing a path-glob scope
	// (fewer files allowed to fail) is a raise — accepted.
	cat := loadCatalogue(t)
	original := map[string]any{
		"scope":    "src/**",
		"language": "go",
	}
	proposed := map[string]any{
		"scope": "src/main.go", // literal; src/** matches it → narrowing
	}
	if err := CheckModification("compiles", original, proposed, cat); err != nil {
		t.Errorf("path-glob narrowing should be accepted; got %v", err)
	}
}

func TestCheckModification_PathGlobWideningRefused(t *testing.T) {
	cat := loadCatalogue(t)
	original := map[string]any{
		"scope":    "src/main.go",
		"language": "go",
	}
	proposed := map[string]any{
		"scope": "src/**", // wider; refused
	}
	err := CheckModification("compiles", original, proposed, cat)
	if !errors.Is(err, ErrModifyWeakening) {
		t.Errorf("path-glob widening should refuse with ErrModifyWeakening; got %v", err)
	}
}

func TestCheckModification_NilCatalogue(t *testing.T) {
	// validation-pass-1 finding #29: assert error message content.
	err := CheckModification("anything", nil, nil, nil)
	if err == nil {
		t.Fatal("nil catalogue should fail")
	}
	if !strings.Contains(err.Error(), "catalogue is nil") {
		t.Errorf("error should mention nil catalogue; got: %v", err)
	}
}

func TestCheckModification_SeverityRaiseToUnevaluatedIsWeakening(t *testing.T) {
	// validation-pass-1 finding #1: severity unevaluated has rank 0,
	// so medium → unevaluated is weakening (lowering rank).
	cat := loadCatalogue(t)
	original := map[string]any{
		"scope":              "src/**",
		"language":           "go",
		"severity-threshold": "medium",
	}
	proposed := map[string]any{
		"severity-threshold": "unevaluated",
	}
	err := CheckModification("lint-clean", original, proposed, cat)
	if !errors.Is(err, ErrModifyWeakening) {
		t.Errorf("severity medium→unevaluated should weaken; got %v", err)
	}
}

func TestCheckModification_SeverityRaiseFromUnevaluatedAllowed(t *testing.T) {
	// Conversely, unevaluated → medium raises rank 0 → 3, strictly stricter.
	cat := loadCatalogue(t)
	original := map[string]any{
		"scope":              "src/**",
		"language":           "go",
		"severity-threshold": "unevaluated",
	}
	proposed := map[string]any{
		"severity-threshold": "medium",
	}
	if err := CheckModification("lint-clean", original, proposed, cat); err != nil {
		t.Errorf("severity unevaluated→medium should be allowed; got %v", err)
	}
}

func TestCheckModification_NaNProposedRejected(t *testing.T) {
	// validation-pass-1 finding #2: NaN bypasses raise-only silently
	// (NaN compares false to everything). Must be rejected.
	cat := loadCatalogue(t)
	original := map[string]any{"threshold": 0.7}
	proposed := map[string]any{"threshold": math.NaN()}
	err := CheckModification("mutation-score", original, proposed, cat)
	if !errors.Is(err, ErrModifyNonFinite) {
		t.Errorf("NaN proposed should return ErrModifyNonFinite; got %v", err)
	}
}

func TestCheckModification_PosInfProposedRejected(t *testing.T) {
	cat := loadCatalogue(t)
	original := map[string]any{"threshold": 0.7}
	proposed := map[string]any{"threshold": math.Inf(+1)}
	err := CheckModification("mutation-score", original, proposed, cat)
	if !errors.Is(err, ErrModifyNonFinite) {
		t.Errorf("+Inf proposed should return ErrModifyNonFinite; got %v", err)
	}
}

func TestCheckModification_NegInfProposedRejected(t *testing.T) {
	cat := loadCatalogue(t)
	original := map[string]any{"threshold": 0.7}
	proposed := map[string]any{"threshold": math.Inf(-1)}
	err := CheckModification("mutation-score", original, proposed, cat)
	if !errors.Is(err, ErrModifyNonFinite) {
		t.Errorf("-Inf proposed should return ErrModifyNonFinite; got %v", err)
	}
}

func TestCheckModification_NaNOriginalRejected(t *testing.T) {
	cat := loadCatalogue(t)
	original := map[string]any{"threshold": math.NaN()}
	proposed := map[string]any{"threshold": 0.5}
	err := CheckModification("mutation-score", original, proposed, cat)
	if !errors.Is(err, ErrModifyNonFinite) {
		t.Errorf("NaN original should return ErrModifyNonFinite; got %v", err)
	}
}

func TestCheckModification_IdenticalListArgsAllowed(t *testing.T) {
	// validation-pass-1 finding #18: identical list args should
	// round-trip without falling into ErrModifyUnsupportedType.
	cat := loadCatalogue(t)
	original := map[string]any{
		"scope":   "src/**",
		"markers": []any{"TODO", "FIXME"},
	}
	proposed := map[string]any{
		"markers": []any{"TODO", "FIXME"},
	}
	if err := CheckModification("no-todo-marker", original, proposed, cat); err != nil {
		t.Errorf("identical list args should be allowed; got %v", err)
	}
}

func TestCheckModification_DifferentListArgsRefused(t *testing.T) {
	cat := loadCatalogue(t)
	original := map[string]any{
		"scope":   "src/**",
		"markers": []any{"TODO", "FIXME"},
	}
	proposed := map[string]any{
		"markers": []any{"TODO"}, // removed FIXME
	}
	err := CheckModification("no-todo-marker", original, proposed, cat)
	if !errors.Is(err, ErrModifyUnsupportedType) {
		t.Errorf("list change should return ErrModifyUnsupportedType; got %v", err)
	}
}

func TestIsPathGlobNarrowing(t *testing.T) {
	cases := []struct {
		orig, proposed string
		want           bool
	}{
		{"src/**", "src/main.go", true},  // literal under glob → narrowing
		{"src/main.go", "src/**", false}, // glob over literal → widening
		{"src/**", "src/**", true},       // equal
		{"src/**", "src/sub/foo.go", true},
		{"src/main.go", "src/main.go", true},
		{"src/**", "lib/main.go", false}, // proposed not under original
		{"src/*.go", "src/main.go", true},
		{"src/*.go", "src/sub/main.go", false}, // single-* doesn't cross /
	}
	for _, c := range cases {
		t.Run(c.orig+"->"+c.proposed, func(t *testing.T) {
			got := isPathGlobNarrowing(c.orig, c.proposed)
			if got != c.want {
				t.Errorf("isPathGlobNarrowing(%q, %q) = %v; want %v", c.orig, c.proposed, got, c.want)
			}
		})
	}
}

func TestIsRegexWidening(t *testing.T) {
	cases := []struct {
		orig, proposed string
		want           bool
	}{
		{"^TODO", "^TODO|^XXX", true},      // more alternations → widening
		{"^TODO|^XXX", "^TODO", false},     // fewer alternations → narrowing
		{"^TODO", "^TODO", true},           // equal
		{"^TODO|^XXX", "^TODO|^XXX", true}, // equal
		{"^A|^B|^C", "^A|^B|^C|^D", true},  // strict superset
		{"^A|^B", "^A|^C", false},          // different alternations (B replaced by C)
		{"^A", "^B", false},                // completely different
	}
	for _, c := range cases {
		t.Run(c.orig+"->"+c.proposed, func(t *testing.T) {
			got := isRegexWidening(c.orig, c.proposed)
			if got != c.want {
				t.Errorf("isRegexWidening(%q, %q) = %v; want %v", c.orig, c.proposed, got, c.want)
			}
		})
	}
}

func TestSplitTrim(t *testing.T) {
	got := splitTrim(" a | b |c", "|")
	want := []string{"a", "b", "c"}
	if len(got) != 3 {
		t.Fatalf("got len %d; want 3", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q; want %q", i, got[i], want[i])
		}
	}
	// Empty pieces dropped.
	got = splitTrim("|", "|")
	if len(got) != 0 {
		t.Errorf("|→%v; want []", got)
	}
}
