package tool

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestScenario_Tools_BashExecution maps to:
// Scenario: Bash command execution
func TestScenario_Tools_BashExecution(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("world"), 0644)

	result := Bash(context.Background(), "ls "+dir, 30*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.TimedOut {
		t.Fatal("unexpected timeout")
	}
	if result.Output == "" {
		t.Fatal("expected output from ls")
	}
}

// TestScenario_Tools_BashTimeout maps to:
// Scenario: Bash command timeout
func TestScenario_Tools_BashTimeout(t *testing.T) {
	result := Bash(context.Background(), "sleep 10", 100*time.Millisecond)
	if !result.TimedOut {
		t.Fatal("expected timeout")
	}
	if result.Error == "" {
		t.Fatal("expected error message on timeout")
	}
}

// TestScenario_Tools_FileRead maps to:
// Scenario: File read
func TestScenario_Tools_FileRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	_ = os.WriteFile(path, []byte("package main\n"), 0644)

	result := ReadFile(context.Background(), path, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Output != "package main\n" {
		t.Errorf("output = %q", result.Output)
	}
}

// TestScenario_Tools_FileWrite maps to:
// Scenario: File write
func TestScenario_Tools_FileWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.go")
	content := "package util\n"

	result := WriteFile(context.Background(), path, content, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("file content = %q", string(data))
	}
}

// TestScenario_Tools_GitOperation maps to:
// Scenario: Git operation
func TestScenario_Tools_GitOperation(t *testing.T) {
	dir := t.TempDir()
	// Init a git repo so we can run git commands
	Bash(context.Background(), "git init -b main "+dir, 5*time.Second)
	Bash(context.Background(), "git -C "+dir+" config user.email test@test.com", 5*time.Second)
	Bash(context.Background(), "git -C "+dir+" config user.name Test", 5*time.Second)
	Bash(context.Background(), "git -C "+dir+" commit --allow-empty -m init", 5*time.Second)

	result := Git(context.Background(), dir, []string{"log", "--oneline"}, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Output == "" {
		t.Fatal("expected git log output")
	}
}

// TestScenario_Tools_GrepRipgrep maps to:
// Scenario: Grep with ripgrep
func TestScenario_Tools_GrepRipgrep(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("rg may not be available")
	}

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("// TODO fix this\npackage main\n"), 0644)

	result := Grep(context.Background(), "TODO", dir, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Output == "" {
		t.Fatal("expected grep match")
	}
}

// TestScenario_Tools_GrepFallback maps to:
// Scenario: Grep fallback to standard grep
func TestScenario_Tools_GrepFallback(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello world\nfoo bar\n"), 0644)

	// Force fallback by using a PATH without rg
	result := GrepWithPath(context.Background(), "hello", dir, 5*time.Second, "/usr/bin")
	if result.Output == "" && result.Error == "" {
		t.Fatal("expected either output or error from grep fallback")
	}
	// If standard grep is available, we should get a match
	if result.Error == "" && !strings.Contains(result.Output, "hello") {
		t.Errorf("expected 'hello' in output: %q", result.Output)
	}
}

// TestScenario_Tools_FileReadNonexistent tests error path.
func TestScenario_Tools_FileReadNonexistent(t *testing.T) {
	result := ReadFile(context.Background(), "/nonexistent/file.txt", 5*time.Second)
	if result.Error == "" {
		t.Fatal("expected error for nonexistent file")
	}
}
