package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/witlox/ghyll/config"
)

// TestScenario_SeedUserHome_FirstRun_WritesEntireBundle verifies
// the embedded user-home tree lands under a fresh home dir with
// the right mode + the right content.
func TestScenario_SeedUserHome_FirstRun_WritesEntireBundle(t *testing.T) {
	home := t.TempDir()
	written, err := seedUserHome(home)
	if err != nil {
		t.Fatalf("seedUserHome: %v", err)
	}
	want := len(config.UserHomeFiles())
	if written != want {
		t.Fatalf("written = %d; want %d", written, want)
	}
	// Every file present + 0o600.
	for rel := range config.UserHomeFiles() {
		dst := filepath.Join(home, rel)
		fi, err := os.Stat(dst)
		if err != nil {
			t.Fatalf("expected file %s: %v", dst, err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("file %s perm = %o; want 0o600", dst, perm)
		}
	}
}

// TestScenario_SeedUserHome_SecondRun_NoClobber verifies operator-
// authored edits survive a re-seed (seed-on-empty semantics).
func TestScenario_SeedUserHome_SecondRun_NoClobber(t *testing.T) {
	home := t.TempDir()
	if _, err := seedUserHome(home); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	// Operator edits one file.
	edited := filepath.Join(home, "instructions.md")
	custom := []byte("# Custom workflow router — edited by operator\n")
	if err := os.WriteFile(edited, custom, 0o600); err != nil {
		t.Fatalf("operator edit: %v", err)
	}
	// Re-seed.
	written, err := seedUserHome(home)
	if err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if written != 0 {
		t.Errorf("second seed wrote %d files; want 0 (existing files preserved)", written)
	}
	got, err := os.ReadFile(edited)
	if err != nil {
		t.Fatalf("read edited: %v", err)
	}
	if string(got) != string(custom) {
		t.Errorf("operator edit clobbered; got %q", string(got))
	}
}

// TestScenario_UserHomeFiles_LayoutInvariants pins the canonical
// shape of the seed library — the eight files the project ships
// must all be present + readable + non-empty.
func TestScenario_UserHomeFiles_LayoutInvariants(t *testing.T) {
	want := []string{
		"instructions.md",
		"commands/status.md",
		"commands/verify.md",
		"commands/spec-check.md",
		"guidelines/engineering.md",
		"guidelines/ci.md",
		"guidelines/go.md",
		"guidelines/python.md",
		"guidelines/cpp.md",
		"guidelines/rust.md",
	}
	files := config.UserHomeFiles()
	if len(files) != len(want) {
		t.Fatalf("files = %d; want %d", len(files), len(want))
	}
	for _, w := range want {
		data, ok := files[w]
		if !ok {
			t.Errorf("missing seed file %s", w)
			continue
		}
		if len(data) == 0 {
			t.Errorf("seed file %s is empty", w)
		}
		// Every file carries the ghyll-bias header so operators
		// know they're seeds, not policy.
		if !strings.Contains(string(data), "ghyll bias") {
			t.Errorf("seed file %s missing 'ghyll bias' header", w)
		}
	}
}

// TestScenario_UserHomeGuideline_ResolvesLanguages pins the
// language-name lookup the trait composer uses.
func TestScenario_UserHomeGuideline_ResolvesLanguages(t *testing.T) {
	for _, lang := range []string{"engineering", "go", "python", "cpp", "rust"} {
		data, ok := config.UserHomeGuideline(lang)
		if !ok {
			t.Errorf("UserHomeGuideline(%q) missing", lang)
			continue
		}
		if len(data) == 0 {
			t.Errorf("UserHomeGuideline(%q) empty", lang)
		}
	}
	if _, ok := config.UserHomeGuideline("brainfuck"); ok {
		t.Error("UserHomeGuideline(brainfuck) should be missing")
	}
}
