package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScenario_UniqueDefinition_NoDuplicates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.txt"), "id: alpha\n")
	mustWrite(t, filepath.Join(dir, "b.txt"), "id: beta\n")
	res, err := EvaluateUniqueDefinition(context.Background(), Clause{
		Concept:    "unique-definition",
		ProjectDir: dir,
		Args: map[string]any{
			"scope":              "*.txt",
			"field":              "id",
			"field-locator-rule": "yaml-path:id",
		},
	})
	if err != nil {
		t.Fatalf("EvaluateUniqueDefinition: %v", err)
	}
	if !res.Pass {
		t.Fatalf("expected Pass=true, got %+v", res)
	}
}

func TestScenario_UniqueDefinition_DuplicatesDetected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.txt"), "id: alpha\n")
	mustWrite(t, filepath.Join(dir, "b.txt"), "id: alpha\n")
	res, err := EvaluateUniqueDefinition(context.Background(), Clause{
		Concept:    "unique-definition",
		ProjectDir: dir,
		Args: map[string]any{
			"scope":              "*.txt",
			"field":              "id",
			"field-locator-rule": "yaml-path:id",
		},
	})
	if err != nil {
		t.Fatalf("EvaluateUniqueDefinition: %v", err)
	}
	if res.Pass {
		t.Fatalf("expected Pass=false, got %+v", res)
	}
	dupes, _ := res.Details["duplicates"].([]map[string]any)
	if len(dupes) == 0 {
		t.Fatalf("expected at least one duplicate entry, got %+v", res.Details)
	}
}

func TestScenario_UniqueDefinition_CaseInsensitive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.txt"), "id: Alpha\n")
	mustWrite(t, filepath.Join(dir, "b.txt"), "id: alpha\n")
	res, err := EvaluateUniqueDefinition(context.Background(), Clause{
		Concept:    "unique-definition",
		ProjectDir: dir,
		Args: map[string]any{
			"scope":              "*.txt",
			"field":              "id",
			"field-locator-rule": "yaml-path:id",
			"case-sensitive":     false,
		},
	})
	if err != nil {
		t.Fatalf("EvaluateUniqueDefinition: %v", err)
	}
	if res.Pass {
		t.Fatalf("expected case-insensitive duplicate detection to fail, got Pass=true")
	}
}

func TestScenario_UniqueDefinition_NoLocator_Unevaluated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.txt"), "hello world\n")
	res, err := EvaluateUniqueDefinition(context.Background(), Clause{
		Concept:    "unique-definition",
		ProjectDir: dir,
		Args: map[string]any{
			"scope":              "*.txt",
			"field":              "id",
			"field-locator-rule": "yaml-path:id",
		},
	})
	if err != nil {
		t.Fatalf("EvaluateUniqueDefinition: %v", err)
	}
	if !res.Unevaluated {
		t.Fatalf("expected Unevaluated=true, got %+v", res)
	}
	if res.Reason != "no-rule-selectable-locations" {
		t.Fatalf("expected reason=no-rule-selectable-locations, got %q", res.Reason)
	}
}

// TestScenario_UniqueDefinition_ArgsMatchYAML asserts that the args
// the evaluator requires line up with the YAML's arguments block.
func TestScenario_UniqueDefinition_ArgsMatchYAML(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"scope":              "*.txt",
		"field":              "id",
		"field-locator-rule": "yaml-path:id",
		"case-sensitive":     true,
	}
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.txt"), "id: alpha\n")
	res, err := EvaluateUniqueDefinition(context.Background(), Clause{
		Concept:    "unique-definition",
		ProjectDir: dir,
		Args:       args,
	})
	if err != nil {
		t.Fatalf("EvaluateUniqueDefinition with YAML-declared args: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil Result")
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
