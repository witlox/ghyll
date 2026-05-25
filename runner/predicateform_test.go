package runner

import (
	"context"
	"path/filepath"
	"testing"
)

func TestScenario_PredicateForm_AssertablePredicates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "doc.md"), `
# Invariants

- uptime >= 99.9
- assert(thing-is-true)
- p99 < 200ms
`)
	res, err := EvaluatePredicateForm(context.Background(), Clause{
		Concept:    "predicate-form",
		ProjectDir: dir,
		Args: map[string]any{
			"scope":              "*.md",
			"collection-locator": "markdown-section:Invariants",
		},
	})
	if err != nil {
		t.Fatalf("EvaluatePredicateForm: %v", err)
	}
	if !res.Pass {
		t.Fatalf("expected Pass=true, got %+v", res)
	}
}

func TestScenario_PredicateForm_ProseEntry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "doc.md"), `
# Invariants

- uptime is critical
`)
	res, err := EvaluatePredicateForm(context.Background(), Clause{
		Concept:    "predicate-form",
		ProjectDir: dir,
		Args: map[string]any{
			"scope":              "*.md",
			"collection-locator": "markdown-section:Invariants",
		},
	})
	if err != nil {
		t.Fatalf("EvaluatePredicateForm: %v", err)
	}
	if res.Pass {
		t.Fatalf("expected Pass=false for prose entry, got Pass=true")
	}
}

// TestScenario_PredicateForm_ArgsMatchYAML feeds the evaluator the
// canonical YAML-declared args and asserts no schema-shape error.
func TestScenario_PredicateForm_ArgsMatchYAML(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "doc.md"), "# Invariants\n\n- a > b\n")
	res, err := EvaluatePredicateForm(context.Background(), Clause{
		Concept:    "predicate-form",
		ProjectDir: dir,
		Args: map[string]any{
			"scope":              "*.md",
			"collection-locator": "markdown-section:Invariants",
			"predicate-grammar":  "contains at least one comparison operator OR is in assert(...) form",
		},
	})
	if err != nil {
		t.Fatalf("EvaluatePredicateForm with YAML-declared args: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil Result")
	}
}
