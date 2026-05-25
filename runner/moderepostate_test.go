package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScenario_ModeDeterminableFromRepo_ValidEnum(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ghyll"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustWrite(t, filepath.Join(dir, ".ghyll", "mode.yaml"), "greenfield-vs-brownfield: greenfield\n")
	res, err := EvaluateModeDeterminableFromRepo(context.Background(), Clause{
		Concept:    "mode-determinable-from-repo",
		ProjectDir: dir,
		Args: map[string]any{
			"discriminator": "greenfield-vs-brownfield",
			"rule":          "cat .ghyll/mode.yaml",
			"valid-modes":   []any{"greenfield", "brownfield"},
		},
	})
	if err != nil {
		t.Fatalf("EvaluateModeDeterminableFromRepo: %v", err)
	}
	if !res.Pass {
		t.Fatalf("expected Pass=true, got %+v", res)
	}
}

func TestScenario_ModeDeterminableFromRepo_MissingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	res, err := EvaluateModeDeterminableFromRepo(context.Background(), Clause{
		Concept:    "mode-determinable-from-repo",
		ProjectDir: dir,
		Args: map[string]any{
			"discriminator": "greenfield-vs-brownfield",
			"rule":          "cat .ghyll/mode.yaml",
			"valid-modes":   []any{"greenfield", "brownfield"},
		},
	})
	if err != nil {
		t.Fatalf("EvaluateModeDeterminableFromRepo: %v", err)
	}
	if res.Pass {
		t.Fatalf("expected Pass=false on missing file, got Pass=true")
	}
}

// TestScenario_ModeDeterminableFromRepo_PathEscape_Refused asserts the
// evaluator rejects a discriminator path that escapes ProjectDir.
func TestScenario_ModeDeterminableFromRepo_PathEscape_Refused(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := EvaluateModeDeterminableFromRepo(context.Background(), Clause{
		Concept:    "mode-determinable-from-repo",
		ProjectDir: dir,
		Args: map[string]any{
			"discriminator":           "x",
			"rule":                    "cat",
			"valid-modes":             []any{"a"},
			"mode-discriminator-path": "../etc/passwd",
		},
	})
	if err == nil {
		t.Fatalf("expected path-escape refusal, got nil error")
	}
}

// TestScenario_ModeDeterminableFromRepo_ArgsMatchYAML feeds the
// authoritative YAML args (discriminator, rule, valid-modes).
func TestScenario_ModeDeterminableFromRepo_ArgsMatchYAML(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ghyll"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustWrite(t, filepath.Join(dir, ".ghyll", "mode.yaml"), "mode: brownfield\n")
	res, err := EvaluateModeDeterminableFromRepo(context.Background(), Clause{
		Concept:    "mode-determinable-from-repo",
		ProjectDir: dir,
		Args: map[string]any{
			"discriminator": "mode",
			"rule":          "cat .ghyll/mode.yaml",
			"valid-modes":   []any{"greenfield", "brownfield"},
		},
	})
	if err != nil {
		t.Fatalf("EvaluateModeDeterminableFromRepo with YAML-declared args: %v", err)
	}
	if !res.Pass {
		t.Fatalf("expected Pass=true, got %+v", res)
	}
}
