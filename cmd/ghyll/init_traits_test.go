package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScenario_ResolveTraitLanguages_AutoFromProfile verifies the
// --language auto path filters profile.Languages against the seed
// library's actual coverage.
func TestScenario_ResolveTraitLanguages_AutoFromProfile(t *testing.T) {
	cases := []struct {
		name    string
		profile []string
		want    []string
	}{
		{"go only", []string{"go"}, []string{"go"}},
		{"go + python", []string{"go", "python"}, []string{"go", "python"}},
		{"unsupported language filtered", []string{"go", "haskell", "python"}, []string{"go", "python"}},
		{"dedup", []string{"go", "go", "python"}, []string{"go", "python"}},
		{"empty profile", []string{}, []string{}},
		{"only unsupported", []string{"haskell", "ocaml"}, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveTraitLanguages("auto", tc.profile)
			if err != nil {
				t.Fatalf("resolveTraitLanguages: %v", err)
			}
			if !equalStringSlice(got, tc.want) {
				t.Errorf("got %v; want %v", got, tc.want)
			}
		})
	}
}

// TestScenario_ResolveTraitLanguages_ExplicitList verifies the
// comma-separated explicit form is validated + deduped + ordered.
func TestScenario_ResolveTraitLanguages_ExplicitList(t *testing.T) {
	got, err := resolveTraitLanguages("go,python", nil)
	if err != nil {
		t.Fatalf("resolveTraitLanguages: %v", err)
	}
	want := []string{"go", "python"}
	if !equalStringSlice(got, want) {
		t.Fatalf("got %v; want %v", got, want)
	}
	// Whitespace + dedup + lowercase.
	got, err = resolveTraitLanguages("Go, python , go", nil)
	if err != nil {
		t.Fatalf("resolveTraitLanguages whitespace: %v", err)
	}
	if !equalStringSlice(got, want) {
		t.Fatalf("got %v; want %v", got, want)
	}
}

// TestScenario_ResolveTraitLanguages_NoneAndExplicitUnsupported.
func TestScenario_ResolveTraitLanguages_NoneAndExplicitUnsupported(t *testing.T) {
	got, err := resolveTraitLanguages("none", []string{"go"})
	if err != nil {
		t.Fatalf("none: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("none returned %v; want empty", got)
	}
	_, err = resolveTraitLanguages("haskell", nil)
	if !errors.Is(err, errUnsupportedTraitLanguage) {
		t.Errorf("explicit haskell err = %v; want errUnsupportedTraitLanguage", err)
	}
}

// TestScenario_WriteTraitBlock_FreshFile creates a new instructions.md
// with the marker-delimited block.
func TestScenario_WriteTraitBlock_FreshFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "instructions.md")
	if err := writeTraitBlock(path, []string{"go"}, false); err != nil {
		t.Fatalf("writeTraitBlock: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "<!-- ghyll-traits-begin -->") {
		t.Error("missing ghyll-traits-begin marker")
	}
	if !strings.Contains(body, "<!-- ghyll-traits-end -->") {
		t.Error("missing ghyll-traits-end marker")
	}
	if !strings.Contains(body, "# Engineering bias") {
		t.Error("missing engineering.md content")
	}
	if !strings.Contains(body, "# Go bias") {
		t.Error("missing go.md content")
	}
}

// TestScenario_WriteTraitBlock_NoForceLeavesExistingBlockAlone.
func TestScenario_WriteTraitBlock_NoForceLeavesExistingBlockAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "instructions.md")
	if err := writeTraitBlock(path, []string{"go"}, false); err != nil {
		t.Fatalf("first: %v", err)
	}
	before, _ := os.ReadFile(path)
	// Operator edits outside the block.
	edited := string(before) + "\n## My note\nKeep this paragraph.\n"
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatalf("edit: %v", err)
	}
	// Re-run without --force-traits → block + operator prose preserved.
	if err := writeTraitBlock(path, []string{"python"}, false); err != nil {
		t.Fatalf("second: %v", err)
	}
	after, _ := os.ReadFile(path)
	if !strings.Contains(string(after), "# Go bias") {
		t.Error("re-run without --force-traits clobbered the Go trait block")
	}
	if strings.Contains(string(after), "# Python bias") {
		t.Error("re-run without --force-traits installed Python trait block (should have skipped)")
	}
	if !strings.Contains(string(after), "Keep this paragraph.") {
		t.Error("operator prose lost")
	}
}

// TestScenario_WriteTraitBlock_ForceReplacesBlock verifies
// --force-traits rewrites the block in place and preserves operator
// prose above + below.
func TestScenario_WriteTraitBlock_ForceReplacesBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "instructions.md")
	if err := writeTraitBlock(path, []string{"go"}, false); err != nil {
		t.Fatalf("first: %v", err)
	}
	before, _ := os.ReadFile(path)
	// Wrap the file with operator prose above + below.
	wrapped := "# My header\n\n" + string(before) + "\n## My footer\nFooter prose.\n"
	if err := os.WriteFile(path, []byte(wrapped), 0o600); err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if err := writeTraitBlock(path, []string{"rust"}, true); err != nil {
		t.Fatalf("force re-run: %v", err)
	}
	after, _ := os.ReadFile(path)
	if !strings.Contains(string(after), "# Rust bias") {
		t.Error("force re-run missing Rust trait block")
	}
	if strings.Contains(string(after), "# Go bias") {
		t.Error("force re-run still has Go trait block (should be replaced)")
	}
	if !strings.Contains(string(after), "# My header") {
		t.Error("operator prose above the block was lost")
	}
	if !strings.Contains(string(after), "Footer prose.") {
		t.Error("operator prose below the block was lost")
	}
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
