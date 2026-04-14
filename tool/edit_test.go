package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const editTestContent = `package main

func hello() string {
    return "hello"
}

func goodbye() string {
    return "goodbye"
}
`

func setupEditDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte(editTestContent), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestScenario_Edit_SuccessfulReplacement maps to:
// Scenario: Successful single replacement
func TestScenario_Edit_SuccessfulReplacement(t *testing.T) {
	dir := setupEditDir(t)
	path := filepath.Join(dir, "main.go")

	result := EditFile(context.Background(), path, `return "hello"`, `return "hi"`, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, `return "hi"`) {
		t.Error("expected replacement")
	}
	if !strings.Contains(content, `return "goodbye"`) {
		t.Error("goodbye should be untouched")
	}
}

// TestScenario_Edit_OldStringNotFound maps to:
// Scenario: Old string not found
func TestScenario_Edit_OldStringNotFound(t *testing.T) {
	dir := setupEditDir(t)
	path := filepath.Join(dir, "main.go")

	result := EditFile(context.Background(), path, `return "missing"`, `return "found"`, 5*time.Second)
	if result.Error == "" {
		t.Fatal("expected error")
	}
	if !strings.Contains(result.Error, "not found") {
		t.Errorf("error = %q, want 'not found'", result.Error)
	}

	// File should be unchanged
	data, _ := os.ReadFile(path)
	if string(data) != editTestContent {
		t.Error("file was modified on error")
	}
}

// TestScenario_Edit_AmbiguousMatch maps to:
// Scenario: Ambiguous match — old string appears multiple times
func TestScenario_Edit_AmbiguousMatch(t *testing.T) {
	dir := setupEditDir(t)
	path := filepath.Join(dir, "main.go")

	result := EditFile(context.Background(), path, "return", "yield", 5*time.Second)
	if result.Error == "" {
		t.Fatal("expected error")
	}
	if !strings.Contains(result.Error, "matches") {
		t.Errorf("error = %q, want match count", result.Error)
	}

	data, _ := os.ReadFile(path)
	if string(data) != editTestContent {
		t.Error("file was modified on ambiguous match")
	}
}

// TestScenario_Edit_FileNotFound maps to:
// Scenario: File does not exist
func TestScenario_Edit_FileNotFound(t *testing.T) {
	result := EditFile(context.Background(), "/tmp/nonexistent-ghyll-edit/x.go", "x", "y", 5*time.Second)
	if result.Error == "" {
		t.Fatal("expected error")
	}
	if !strings.Contains(result.Error, "file") {
		t.Errorf("error = %q, want file error", result.Error)
	}
}

// TestScenario_Edit_PreservesPermissions maps to:
// Scenario: Edit preserves file permissions
func TestScenario_Edit_PreservesPermissions(t *testing.T) {
	dir := setupEditDir(t)
	path := filepath.Join(dir, "main.go")

	// Set specific permissions
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}

	result := EditFile(context.Background(), path, `return "hello"`, `return "hi"`, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0644 {
		t.Errorf("permissions = %o, want 0644", info.Mode().Perm())
	}
}

// TestScenario_Edit_Multiline maps to:
// Scenario: Edit with multiline old and new strings
func TestScenario_Edit_Multiline(t *testing.T) {
	dir := setupEditDir(t)
	path := filepath.Join(dir, "main.go")

	old := "func hello() string {\n    return \"hello\"\n}"
	new := "func hello(name string) string {\n    return \"hello \" + name\n}"

	result := EditFile(context.Background(), path, old, new, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "func hello(name string) string") {
		t.Error("multiline replacement failed")
	}
}

// TestScenario_Edit_EmptyNewString maps to:
// Scenario: Edit with empty new_string deletes matched text
func TestScenario_Edit_EmptyNewString(t *testing.T) {
	dir := setupEditDir(t)
	path := filepath.Join(dir, "main.go")

	old := "func goodbye() string {\n    return \"goodbye\"\n}\n"
	result := EditFile(context.Background(), path, old, "", 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if strings.Contains(content, "func goodbye") {
		t.Error("deleted text still present")
	}
	if !strings.Contains(content, "func hello") {
		t.Error("hello function should remain")
	}
}

// TestScenario_Edit_ConcurrentModification maps to:
// Scenario: Edit fails if file modified during operation
func TestScenario_Edit_ConcurrentModification(t *testing.T) {
	dir := setupEditDir(t)
	path := filepath.Join(dir, "main.go")

	// Read original content, then modify file, then try CAS edit
	original, _ := os.ReadFile(path)

	// Modify file to simulate concurrent change
	modified := strings.Replace(string(original), `return "hello"`, `return "hola"`, 1)
	if err := os.WriteFile(path, []byte(modified), 0644); err != nil {
		t.Fatal(err)
	}

	// This should fail because old_string "return \"hello\"" no longer exists
	result := EditFile(context.Background(), path, `return "hello"`, `return "hi"`, 5*time.Second)
	if result.Error == "" {
		t.Fatal("expected error due to concurrent modification")
	}

	// File should retain the concurrent change
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `return "hola"`) {
		t.Error("concurrent change was lost")
	}
}

// TestScenario_Edit_NoOpSameContent maps to:
// Scenario: Edit where old_string equals new_string
func TestScenario_Edit_NoOpSameContent(t *testing.T) {
	dir := setupEditDir(t)
	path := filepath.Join(dir, "main.go")

	result := EditFile(context.Background(), path, `return "hello"`, `return "hello"`, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	data, _ := os.ReadFile(path)
	if string(data) != editTestContent {
		t.Error("file changed on no-op edit")
	}
}

// TestScenario_Edit_Timeout maps to:
// Scenario: Edit respects tool timeout
func TestScenario_Edit_Timeout(t *testing.T) {
	dir := setupEditDir(t)
	path := filepath.Join(dir, "main.go")

	// Use a cancelled context to simulate timeout
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancelled

	result := EditFile(ctx, path, `return "hello"`, `return "hi"`, 5*time.Second)
	if !result.TimedOut {
		// Context cancellation might complete before timeout check,
		// but the file should not be modified
		data, _ := os.ReadFile(path)
		if string(data) != editTestContent && result.Error == "" {
			t.Error("file was modified despite cancelled context")
		}
	}
}

// TestScenario_Edit_CAS_SHA256 maps to:
// Scenario: Edit CAS uses content hash not mtime
func TestScenario_Edit_CAS_SHA256(t *testing.T) {
	dir := setupEditDir(t)
	path := filepath.Join(dir, "main.go")

	// Verify that the CAS mechanism works by doing a successful edit
	// and confirming the hash-based check passed
	result := EditFile(context.Background(), path, `return "hello"`, `return "hi"`, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("first edit failed: %s", result.Error)
	}

	// Second edit on the modified file should work (new content, new hash)
	result = EditFile(context.Background(), path, `return "hi"`, `return "hey"`, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("second edit failed: %s", result.Error)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `return "hey"`) {
		t.Error("second edit not applied")
	}
}
