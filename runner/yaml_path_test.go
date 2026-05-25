// Tests for the proper yaml-path evaluator wired into uniquedef.go
// and predicateform.go (diamond v4 H-2 closure: TODOs resolved).

package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestScenario_UniqueDefinition_YAMLPath_DetectsDuplicates verifies
// the unique-definition evaluator's yaml-path locator surfaces
// duplicates in a yaml document under a dotted path.
func TestScenario_UniqueDefinition_YAMLPath_DetectsDuplicates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	yamlSrc := `arrows:
  - id: a1
    description: first
  - id: a2
    description: second
  - id: a1
    description: dup-of-first
`
	if err := os.WriteFile(filepath.Join(dir, "grid.yaml"), []byte(yamlSrc), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	clause := Clause{
		Concept:    "unique-definition",
		ClauseID:   "C1",
		ProjectDir: dir,
		Args: map[string]any{
			"scope":              "**/*.yaml",
			"field":              "id",
			"field-locator-rule": "yaml-path:arrows[]",
		},
	}
	res, err := EvaluateUniqueDefinition(context.Background(), clause)
	if err != nil {
		t.Fatalf("EvaluateUniqueDefinition: %v", err)
	}
	if res.Pass {
		t.Fatal("expected Pass=false (duplicate a1 detected)")
	}
	dupes, _ := res.Details["duplicates"].([]map[string]any)
	if len(dupes) != 1 {
		t.Fatalf("expected 1 duplicate group, got %d", len(dupes))
	}
}

// TestScenario_UniqueDefinition_YAMLPath_NoDuplicates verifies the
// pass path: all ids unique.
func TestScenario_UniqueDefinition_YAMLPath_NoDuplicates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	yamlSrc := `arrows:
  - id: a1
  - id: a2
  - id: a3
`
	if err := os.WriteFile(filepath.Join(dir, "grid.yaml"), []byte(yamlSrc), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	clause := Clause{
		Concept:    "unique-definition",
		ClauseID:   "C1",
		ProjectDir: dir,
		Args: map[string]any{
			"scope":              "**/*.yaml",
			"field":              "id",
			"field-locator-rule": "yaml-path:arrows[]",
		},
	}
	res, err := EvaluateUniqueDefinition(context.Background(), clause)
	if err != nil {
		t.Fatalf("EvaluateUniqueDefinition: %v", err)
	}
	if !res.Pass {
		t.Fatalf("expected Pass=true, got Pass=false details=%v", res.Details)
	}
}

// TestScenario_PredicateForm_YAMLPath_DetectsNonPredicates verifies
// the predicate-form evaluator's yaml-path locator flags entries
// that don't carry a comparison operator.
func TestScenario_PredicateForm_YAMLPath_DetectsNonPredicates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	yamlSrc := `requirements:
  - x >= 1
  - assert(y < 100)
  - "narrative prose without an operator"
`
	if err := os.WriteFile(filepath.Join(dir, "req.yaml"), []byte(yamlSrc), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	clause := Clause{
		Concept:    "predicate-form",
		ClauseID:   "C1",
		ProjectDir: dir,
		Args: map[string]any{
			"scope":              "**/*.yaml",
			"collection-locator": "yaml-path:requirements[]",
		},
	}
	res, err := EvaluatePredicateForm(context.Background(), clause)
	if err != nil {
		t.Fatalf("EvaluatePredicateForm: %v", err)
	}
	if res.Pass {
		t.Fatal("expected Pass=false (third entry not a predicate)")
	}
	non, _ := res.Details["non-predicates"].([]map[string]any)
	if len(non) != 1 {
		t.Fatalf("expected 1 non-predicate, got %d", len(non))
	}
}
