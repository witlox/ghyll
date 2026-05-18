package catalogue

import (
	"strings"
	"testing"
)

func TestValidate_UnknownConcept(t *testing.T) {
	cat := loadShipped(t)
	_, err := cat.Validate("does-not-exist", map[string]any{})
	if err == nil {
		t.Fatal("Validate of unknown concept should return error")
	}
	if !strings.Contains(err.Error(), "unknown concept") {
		t.Errorf("error should mention unknown concept; got: %v", err)
	}
}

func TestValidate_CompilesValid(t *testing.T) {
	cat := loadShipped(t)
	args := map[string]any{
		"scope":    "src/**",
		"language": "go",
	}
	out, err := cat.Validate("compiles", args)
	if err != nil {
		t.Fatalf("Validate(compiles, valid args) = err %v; want nil", err)
	}
	if out["scope"] != "src/**" {
		t.Errorf("normalized scope = %v; want \"src/**\"", out["scope"])
	}
	if out["language"] != "go" {
		t.Errorf("normalized language = %v; want \"go\"", out["language"])
	}
}

func TestValidate_CompilesMissingRequired(t *testing.T) {
	cat := loadShipped(t)
	args := map[string]any{
		"scope": "src/**",
		// missing required `language`
	}
	_, err := cat.Validate("compiles", args)
	if err == nil {
		t.Fatal("Validate should fail when required arg is missing")
	}
	if !strings.Contains(err.Error(), "missing required argument") {
		t.Errorf("error should mention missing required argument; got: %v", err)
	}
	if !strings.Contains(err.Error(), "language") {
		t.Errorf("error should name the missing argument; got: %v", err)
	}
}

func TestValidate_UnknownArgument(t *testing.T) {
	cat := loadShipped(t)
	args := map[string]any{
		"scope":    "src/**",
		"language": "go",
		"bogus":    "value",
	}
	_, err := cat.Validate("compiles", args)
	if err == nil {
		t.Fatal("Validate should fail for unknown argument")
	}
	if !strings.Contains(err.Error(), "unknown argument") {
		t.Errorf("error should mention unknown argument; got: %v", err)
	}
}

func TestValidate_TypeMismatch(t *testing.T) {
	cat := loadShipped(t)
	args := map[string]any{
		"scope":    123, // should be string/path-glob
		"language": "go",
	}
	_, err := cat.Validate("compiles", args)
	if err == nil {
		t.Fatal("Validate should fail when arg type is wrong")
	}
	if !strings.Contains(err.Error(), "requires string") {
		t.Errorf("error should describe type mismatch; got: %v", err)
	}
}

func TestValidate_MutationScoreInRange(t *testing.T) {
	cat := loadShipped(t)
	args := map[string]any{
		"scope":      "src/**",
		"test-scope": "tests/**",
		"threshold":  0.7,
		"language":   "go",
	}
	out, err := cat.Validate("mutation-score", args)
	if err != nil {
		t.Fatalf("Validate(mutation-score, threshold=0.7) = err %v; want nil", err)
	}
	if got := out["threshold"].(float64); got != 0.7 {
		t.Errorf("threshold normalized to %v; want 0.7", got)
	}
}

func TestValidate_MutationScoreOutOfRange(t *testing.T) {
	cat := loadShipped(t)
	args := map[string]any{
		"scope":      "src/**",
		"test-scope": "tests/**",
		"threshold":  1.5, // exceeds [0.0, 1.0]
		"language":   "go",
	}
	_, err := cat.Validate("mutation-score", args)
	if err == nil {
		t.Fatal("Validate should fail when threshold is out of declared range")
	}
	if !strings.Contains(err.Error(), "outside range") {
		t.Errorf("error should mention range violation; got: %v", err)
	}
}

func TestValidate_MutationScoreThresholdWrongType(t *testing.T) {
	cat := loadShipped(t)
	args := map[string]any{
		"scope":      "src/**",
		"test-scope": "tests/**",
		"threshold":  "high", // should be a number, not a string
		"language":   "go",
	}
	_, err := cat.Validate("mutation-score", args)
	if err == nil {
		t.Fatal("Validate should fail when threshold has wrong type")
	}
	if !strings.Contains(err.Error(), "requires numeric") {
		t.Errorf("error should mention numeric type requirement; got: %v", err)
	}
}

func TestValidate_DefaultApplied(t *testing.T) {
	// validation-pass-1 finding #15: assert normalized output content,
	// not just key presence.
	cat := loadShipped(t)
	// no-todo-marker has optional `markers` with a declared default.
	args := map[string]any{
		"scope": "src/**",
	}
	out, err := cat.Validate("no-todo-marker", args)
	if err != nil {
		t.Fatalf("Validate(no-todo-marker, minimal args) = err %v; want nil", err)
	}
	got, ok := out["markers"]
	if !ok {
		t.Fatal("default for `markers` should be applied")
	}
	// The default in the schema is [TODO, TBD, ???, FIXME, XXX].
	// Verify content, not just presence.
	wantMarkers := map[string]struct{}{
		"TODO":  {},
		"TBD":   {},
		"???":   {},
		"FIXME": {},
		"XXX":   {},
	}
	list, ok := got.([]any)
	if !ok {
		t.Fatalf("default markers should be a list; got %T", got)
	}
	if len(list) != len(wantMarkers) {
		t.Errorf("default markers length = %d; want %d", len(list), len(wantMarkers))
	}
	for _, v := range list {
		s, ok := v.(string)
		if !ok {
			t.Errorf("marker entry is not string: %T", v)
			continue
		}
		if _, expected := wantMarkers[s]; !expected {
			t.Errorf("unexpected default marker %q", s)
		}
	}
}

func TestValidate_LintCleanSeverityThreshold(t *testing.T) {
	cat := loadShipped(t)
	args := map[string]any{
		"scope":              "src/**",
		"language":           "go",
		"severity-threshold": "high",
	}
	if _, err := cat.Validate("lint-clean", args); err != nil {
		t.Fatalf("Validate(lint-clean, valid severity) = err %v; want nil", err)
	}

	args["severity-threshold"] = "supercritical" // not in canonical enum
	if _, err := cat.Validate("lint-clean", args); err == nil {
		t.Fatal("Validate should reject non-canonical severity")
	}
}

func TestValidate_KillServerEnumArgument(t *testing.T) {
	cat := loadShipped(t)
	args := map[string]any{
		"test-suite":    "tests/integration/**",
		"critical-deps": []any{"postgres", "redis"},
		"kill-strategy": "stop-process",
	}
	if _, err := cat.Validate("kill-server-fails-integration", args); err != nil {
		t.Fatalf("Validate(kill-server, stop-process) = err %v; want nil", err)
	}

	args["kill-strategy"] = "bogus-strategy"
	_, err := cat.Validate("kill-server-fails-integration", args)
	if err == nil {
		t.Fatal("Validate should reject value outside enum")
	}
	if !strings.Contains(err.Error(), "not in enum") {
		t.Errorf("error should mention enum violation; got: %v", err)
	}
}

func TestValidate_CardinalityCheckIntOrRange(t *testing.T) {
	// validation-pass-1 finding #15: assert normalized output content;
	// finding #14: range with min > max is rejected.
	cat := loadShipped(t)
	base := map[string]any{
		"query":        "$.contexts[*]",
		"query-target": "project-state",
	}

	base["expected"] = 1
	out, err := cat.Validate("cardinality-check", base)
	if err != nil {
		t.Errorf("expected=1 (int) should validate; got %v", err)
	}
	if got := out["expected"]; got != 1 {
		t.Errorf("normalized expected = %v; want 1", got)
	}

	base["expected"] = []any{0, 3}
	out, err = cat.Validate("cardinality-check", base)
	if err != nil {
		t.Errorf("expected=[0,3] (range) should validate; got %v", err)
	}
	if list, ok := out["expected"].([]any); !ok || len(list) != 2 || list[0] != 0 || list[1] != 3 {
		t.Errorf("normalized expected = %v; want [0, 3]", out["expected"])
	}

	base["expected"] = "high"
	if _, err := cat.Validate("cardinality-check", base); err == nil {
		t.Error("expected=string should fail")
	}

	base["expected"] = []any{5, 2} // inverted range
	_, err = cat.Validate("cardinality-check", base)
	if err == nil {
		t.Error("expected=[5,2] (inverted) should fail")
	}
	if !strings.Contains(err.Error(), "inverted") {
		t.Errorf("error should mention inverted bounds; got: %v", err)
	}
}

func TestValidate_AllNamedTypesRejectWrongInput(t *testing.T) {
	// validation-pass-1 finding #16: table-driven type-mismatch across
	// the full catalogue type vocabulary. Each named type should reject
	// at least one obviously-wrong input.
	cat := loadShipped(t)

	cases := []struct {
		concept string
		args    map[string]any
		want    string // substring of error
	}{
		{
			// path-glob requires string
			concept: "compiles",
			args:    map[string]any{"scope": 123, "language": "go"},
			want:    "requires string",
		},
		{
			// language-id requires string
			concept: "compiles",
			args:    map[string]any{"scope": "src/**", "language": 42},
			want:    "requires string",
		},
		{
			// severity must be in canonical enum
			concept: "lint-clean",
			args:    map[string]any{"scope": "x", "language": "go", "severity-threshold": "blegh"},
			want:    "not in canonical enum",
		},
		{
			// number requires numeric
			concept: "mutation-score",
			args: map[string]any{
				"scope":      "src",
				"test-scope": "tests",
				"threshold":  "0.7", // string, not number
				"language":   "go",
			},
			want: "requires numeric",
		},
		{
			// list requires array
			concept: "kill-server-fails-integration",
			args: map[string]any{
				"test-suite":    "tests/**",
				"critical-deps": "postgres", // should be a list
			},
			want: "requires array",
		},
		{
			// enum value not in declared values
			concept: "kill-server-fails-integration",
			args: map[string]any{
				"test-suite":    "tests/**",
				"critical-deps": []any{"postgres"},
				"kill-strategy": "nuke-it-from-orbit", // not in enum
			},
			want: "not in enum",
		},
	}

	for _, tc := range cases {
		_, err := cat.Validate(tc.concept, tc.args)
		if err == nil {
			t.Errorf("%s: expected error containing %q; got nil", tc.concept, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v; want substring %q", tc.concept, err, tc.want)
		}
	}
}

func TestValidate_UnknownTypeNameRejected(t *testing.T) {
	// validation-pass-1 finding #17: a schema declaring a type not in
	// the catalogue's closed vocabulary should be rejected, not silently
	// permitted.
	//
	// The Concept type's Arguments map is unexported but we construct
	// a Concept value directly to exercise the unknown-type branch.
	cat := &Catalogue{concepts: map[string]Concept{
		"test-concept": {
			Name: "test-concept",
			Arguments: map[string]ArgumentSchema{
				"x": {Type: "totally-made-up-type", Required: true},
			},
			Evaluator:   EvaluatorContract{Contract: "machine"},
			DefaultCost: 0,
		},
	}}
	_, err := cat.Validate("test-concept", map[string]any{"x": "anything"})
	if err == nil {
		t.Fatal("Validate should reject a schema with an unknown type name")
	}
	if !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("error should mention unknown type; got: %v", err)
	}
}
