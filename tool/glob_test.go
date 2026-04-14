package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupGlobDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	files := []string{
		"src/main.go",
		"src/handler.go",
		"src/handler_test.go",
		"internal/store/store.go",
		"internal/store/store_test.go",
		"docs/README.md",
		".ghyll/instructions.md",
	}
	for _, f := range files {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("content"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestScenario_Glob_AllGoFiles maps to:
// Scenario: Match all Go files recursively
func TestScenario_Glob_AllGoFiles(t *testing.T) {
	dir := setupGlobDir(t)

	result := Glob(context.Background(), "**/*.go", dir, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	paths := strings.Split(strings.TrimSpace(result.Output), "\n")
	if len(paths) != 5 {
		t.Errorf("got %d paths, want 5: %v", len(paths), paths)
	}
}

// TestScenario_Glob_TestFilesOnly maps to:
// Scenario: Match test files only
func TestScenario_Glob_TestFilesOnly(t *testing.T) {
	dir := setupGlobDir(t)

	result := Glob(context.Background(), "**/*_test.go", dir, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	paths := strings.Split(strings.TrimSpace(result.Output), "\n")
	if len(paths) != 2 {
		t.Errorf("got %d paths, want 2: %v", len(paths), paths)
	}
}

// TestScenario_Glob_Subdirectory maps to:
// Scenario: Match in subdirectory
func TestScenario_Glob_Subdirectory(t *testing.T) {
	dir := setupGlobDir(t)
	srcDir := filepath.Join(dir, "src")

	result := Glob(context.Background(), "*.go", srcDir, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	paths := strings.Split(strings.TrimSpace(result.Output), "\n")
	if len(paths) != 3 {
		t.Errorf("got %d paths, want 3: %v", len(paths), paths)
	}
}

// TestScenario_Glob_NoMatches maps to:
// Scenario: No matches returns empty list
func TestScenario_Glob_NoMatches(t *testing.T) {
	dir := setupGlobDir(t)

	result := Glob(context.Background(), "**/*.rs", dir, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if strings.TrimSpace(result.Output) != "" {
		t.Errorf("expected empty output, got %q", result.Output)
	}
}

// TestScenario_Glob_DirectoryWildcard maps to:
// Scenario: Pattern with directory wildcard
func TestScenario_Glob_DirectoryWildcard(t *testing.T) {
	dir := setupGlobDir(t)

	result := Glob(context.Background(), "internal/**/*.go", dir, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	paths := strings.Split(strings.TrimSpace(result.Output), "\n")
	if len(paths) != 2 {
		t.Errorf("got %d paths, want 2: %v", len(paths), paths)
	}
}

// TestScenario_Glob_InvalidPath maps to:
// Scenario: Invalid path returns error
func TestScenario_Glob_InvalidPath(t *testing.T) {
	result := Glob(context.Background(), "**/*.go", "/tmp/nonexistent-ghyll-glob", 5*time.Second)
	if result.Error == "" {
		t.Fatal("expected error for nonexistent directory")
	}
	if !strings.Contains(result.Error, "not found") && !strings.Contains(result.Error, "no such") {
		t.Errorf("error = %q, want directory error", result.Error)
	}
}

// TestScenario_Glob_SortedByMtime maps to:
// Scenario: Results sorted by modification time
func TestScenario_Glob_SortedByMtime(t *testing.T) {
	dir := setupGlobDir(t)

	// Touch handler.go to make it more recent
	handlerPath := filepath.Join(dir, "src/handler.go")
	time.Sleep(10 * time.Millisecond) // Ensure different mtime
	if err := os.WriteFile(handlerPath, []byte("updated"), 0644); err != nil {
		t.Fatal(err)
	}

	result := Glob(context.Background(), "src/*.go", dir, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	paths := strings.Split(strings.TrimSpace(result.Output), "\n")
	if len(paths) < 2 {
		t.Fatalf("need at least 2 paths, got %d", len(paths))
	}
	// Most recently modified should be first
	if !strings.Contains(paths[0], "handler.go") {
		t.Errorf("first path should be handler.go (most recent), got %s", paths[0])
	}
}

// TestScenario_Glob_BrokenSymlink maps to:
// Scenario: Glob skips broken symlinks
func TestScenario_Glob_BrokenSymlink(t *testing.T) {
	dir := setupGlobDir(t)
	brokenLink := filepath.Join(dir, "src/broken")
	if err := os.Symlink("/tmp/nonexistent-target-ghyll", brokenLink); err != nil {
		t.Skip("symlinks not supported")
	}

	result := Glob(context.Background(), "**/*", dir, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if strings.Contains(result.Output, "broken") {
		t.Error("broken symlink should be excluded")
	}
}

// TestScenario_Glob_ExternalSymlink maps to:
// Scenario: Glob does not follow symlinks outside workspace
func TestScenario_Glob_ExternalSymlink(t *testing.T) {
	dir := setupGlobDir(t)
	extLink := filepath.Join(dir, "src/external")
	if err := os.Symlink("/etc/hosts", extLink); err != nil {
		t.Skip("symlinks not supported")
	}

	result := Glob(context.Background(), "**/*", dir, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if strings.Contains(result.Output, "external") {
		t.Error("external symlink should be excluded")
	}
}

// TestScenario_Glob_HiddenFiles maps to:
// Scenario: Glob includes hidden files when pattern matches
func TestScenario_Glob_HiddenFiles(t *testing.T) {
	dir := setupGlobDir(t)

	result := Glob(context.Background(), "**/*.md", dir, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if !strings.Contains(result.Output, ".ghyll/instructions.md") {
		t.Error("hidden directory files should be included")
	}
}

// TestScenario_Glob_EmptyPattern maps to:
// Scenario: Glob with empty pattern returns error
func TestScenario_Glob_EmptyPattern(t *testing.T) {
	dir := setupGlobDir(t)

	result := Glob(context.Background(), "", dir, 5*time.Second)
	if result.Error == "" {
		t.Fatal("expected error for empty pattern")
	}
	if !strings.Contains(result.Error, "empty pattern") {
		t.Errorf("error = %q, want 'empty pattern'", result.Error)
	}
}

// TestScenario_Glob_ValidWorkspaceSymlink maps to:
// Scenario: Glob follows valid symlinks within workspace
func TestScenario_Glob_ValidWorkspaceSymlink(t *testing.T) {
	dir := setupGlobDir(t)
	target := filepath.Join(dir, "src/main.go")
	link := filepath.Join(dir, "src/alias.go")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks not supported")
	}

	result := Glob(context.Background(), "**/*.go", dir, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "alias.go") {
		t.Error("valid workspace symlink should be included")
	}
}
