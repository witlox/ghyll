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
	cat := loadShipped(t)
	// no-todo-marker has optional `markers` with a declared default.
	args := map[string]any{
		"scope": "src/**",
	}
	out, err := cat.Validate("no-todo-marker", args)
	if err != nil {
		t.Fatalf("Validate(no-todo-marker, minimal args) = err %v; want nil", err)
	}
	if _, ok := out["markers"]; !ok {
		t.Error("default for `markers` should be applied")
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
	cat := loadShipped(t)
	// int-or-range type accepts both shapes.
	base := map[string]any{
		"query":        "$.contexts[*]",
		"query-target": "project-state",
	}

	base["expected"] = 1
	if _, err := cat.Validate("cardinality-check", base); err != nil {
		t.Errorf("expected=1 (int) should validate; got %v", err)
	}

	base["expected"] = []any{0, 3}
	if _, err := cat.Validate("cardinality-check", base); err != nil {
		t.Errorf("expected=[0,3] (range) should validate; got %v", err)
	}

	base["expected"] = "high" // neither int nor range
	if _, err := cat.Validate("cardinality-check", base); err == nil {
		t.Error("expected=string should fail")
	}
}
