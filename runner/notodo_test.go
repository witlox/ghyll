package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoTodoMarker_PassesOnCleanScope(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/foo.go", "package src\nfunc Foo() {}\n")
	writeFile(t, dir, "src/bar.go", "package src\nfunc Bar() { return }\n")
	res, err := EvaluateNoTodoMarker(context.Background(), Clause{
		Concept:    "no-todo-marker",
		ProjectDir: dir,
		Args:       map[string]any{"scope": "src/**"},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !res.Pass {
		t.Errorf("expected pass on clean scope; got %+v", res.Details)
	}
}

func TestNoTodoMarker_FailsOnHit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/foo.go", "package src\n// TODO: implement retries\nfunc Foo() {}\n")
	res, err := EvaluateNoTodoMarker(context.Background(), Clause{
		Concept:    "no-todo-marker",
		ProjectDir: dir,
		Args:       map[string]any{"scope": "src/**"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pass {
		t.Errorf("expected fail; got pass")
	}
	hits, ok := res.Details["hits"].([]map[string]any)
	if !ok || len(hits) != 1 {
		t.Fatalf("hits = %v", res.Details["hits"])
	}
	hit := hits[0]
	if hit["file"] != "src/foo.go" {
		t.Errorf("hit.file = %v; want src/foo.go", hit["file"])
	}
	if hit["line"].(int) != 2 {
		t.Errorf("hit.line = %v; want 2", hit["line"])
	}
	if !strings.Contains(hit["surrounding-text"].(string), "TODO:") {
		t.Errorf("hit.surrounding-text = %v", hit["surrounding-text"])
	}
}

func TestNoTodoMarker_DetectsAllDefaultMarkers(t *testing.T) {
	dir := t.TempDir()
	for _, marker := range defaultTodoMarkers {
		writeFile(t, dir, "src/"+marker+".go",
			"package src\n// "+marker+" remember to implement\n")
	}
	res, err := EvaluateNoTodoMarker(context.Background(), Clause{
		ProjectDir: dir,
		Args:       map[string]any{"scope": "src/**"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pass {
		t.Error("expected fail (all default markers present)")
	}
	hits := res.Details["hits"].([]map[string]any)
	if len(hits) < len(defaultTodoMarkers) {
		t.Errorf("hit count = %d; want >= %d (one per default marker)",
			len(hits), len(defaultTodoMarkers))
	}
}

func TestNoTodoMarker_CaseInsensitiveByDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/foo.go", "package src\n// todo: lower-cased\n")
	res, _ := EvaluateNoTodoMarker(context.Background(), Clause{
		ProjectDir: dir,
		Args:       map[string]any{"scope": "src/**"},
	})
	if res.Pass {
		t.Error("case-insensitive default should match lowercase 'todo'")
	}
}

func TestNoTodoMarker_CaseSensitiveOptOut(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/foo.go", "package src\n// todo: lower-cased\n")
	res, _ := EvaluateNoTodoMarker(context.Background(), Clause{
		ProjectDir: dir,
		Args: map[string]any{
			"scope":          "src/**",
			"case-sensitive": true,
		},
	})
	if !res.Pass {
		t.Error("case-sensitive=true should not match lowercase 'todo'")
	}
}

func TestNoTodoMarker_CustomMarkers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/foo.go", "package src\n// HACK: this is rough\n")
	// Default markers don't include HACK; with default we'd pass.
	resDefault, _ := EvaluateNoTodoMarker(context.Background(), Clause{
		ProjectDir: dir,
		Args:       map[string]any{"scope": "src/**"},
	})
	if !resDefault.Pass {
		t.Error("default markers should NOT match HACK")
	}
	// With custom markers including HACK, fail.
	resCustom, _ := EvaluateNoTodoMarker(context.Background(), Clause{
		ProjectDir: dir,
		Args: map[string]any{
			"scope":   "src/**",
			"markers": []any{"HACK"},
		},
	})
	if resCustom.Pass {
		t.Error("custom markers=[HACK] should match HACK")
	}
}

func TestNoTodoMarker_RespectsScope(t *testing.T) {
	dir := t.TempDir()
	// TODO in scope (src/), should fail.
	writeFile(t, dir, "src/in_scope.go", "// TODO: in scope\n")
	// TODO out of scope (docs/), should be ignored.
	writeFile(t, dir, "docs/out_of_scope.md", "TODO: in docs\n")
	res, _ := EvaluateNoTodoMarker(context.Background(), Clause{
		ProjectDir: dir,
		Args:       map[string]any{"scope": "src/**"},
	})
	if res.Pass {
		t.Error("expected fail (in-scope TODO present)")
	}
	hits := res.Details["hits"].([]map[string]any)
	for _, h := range hits {
		if !strings.HasPrefix(h["file"].(string), "src/") {
			t.Errorf("out-of-scope hit: %v", h["file"])
		}
	}
}

func TestNoTodoMarker_SkipsBuildDirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/main.go", "package src\n")
	writeFile(t, dir, "vendor/lib/foo.go", "// TODO: vendored\n")
	writeFile(t, dir, "node_modules/pkg/index.js", "// TODO: dep code\n")
	res, _ := EvaluateNoTodoMarker(context.Background(), Clause{
		ProjectDir: dir,
		Args:       map[string]any{"scope": "**"},
	})
	if !res.Pass {
		hits := res.Details["hits"].([]map[string]any)
		t.Errorf("expected pass (build dirs skipped); got hits: %v", hits)
	}
}

func TestNoTodoMarker_MissingScopeArgErrors(t *testing.T) {
	_, err := EvaluateNoTodoMarker(context.Background(), Clause{
		Args: map[string]any{},
	})
	if err == nil {
		t.Error("missing scope should error")
	}
}

func TestNoTodoMarker_EmptyMarkersErrors(t *testing.T) {
	_, err := EvaluateNoTodoMarker(context.Background(), Clause{
		Args: map[string]any{
			"scope":   "**",
			"markers": []any{},
		},
	})
	if err == nil {
		t.Error("empty markers list should error")
	}
}

func TestNoTodoMarker_RespectsContext(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/foo.go", "// TODO: x\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancel
	_, err := EvaluateNoTodoMarker(ctx, Clause{
		ProjectDir: dir,
		Args:       map[string]any{"scope": "**"},
	})
	if err == nil {
		t.Error("cancelled context should propagate error")
	}
	if !errors.Is(err, context.Canceled) {
		// WalkDir wraps the cancel error.
		if !strings.Contains(err.Error(), "context canceled") {
			t.Errorf("err = %v; want context.Canceled", err)
		}
	}
}

func TestRegisterBuiltins(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)
	if _, _, ok := r.Lookup("no-todo-marker"); !ok {
		t.Error("no-todo-marker not registered by RegisterBuiltins")
	}
	// Second call goes through Replace (Generation bumps).
	RegisterBuiltins(r)
	_, id, _ := r.Lookup("no-todo-marker")
	if id.Generation != 2 {
		t.Errorf("Generation after second RegisterBuiltins = %d; want 2", id.Generation)
	}
}

// writeFile is a test helper that creates a file under dir with the
// given relative path and content.
func writeFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
