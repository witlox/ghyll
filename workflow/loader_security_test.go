package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScenario_ReadFileIfExists_RefusesSymlink verifies Tier 3 /
// SR H-3 — workflow loader's readFileIfExists refuses to follow
// a symlink pointing outside the workflow tree (e.g.,
// `instructions.md -> /etc/shadow`).
func TestScenario_ReadFileIfExists_RefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.md")
	if err := os.WriteFile(target, []byte("safe content"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	got := readFileIfExists(link)
	if got != "" {
		t.Errorf("symlink read returned %q; want empty (refused)", got)
	}
}

// TestScenario_ReadFileIfExists_RefusesOversized verifies the
// size cap.
func TestScenario_ReadFileIfExists_RefusesOversized(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.md")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 300*1024)), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readFileIfExists(path)
	if got != "" {
		t.Errorf("oversized read returned %d bytes; want empty (refused)", len(got))
	}
}

func TestScenario_ReadFileIfExists_AcceptsNormalFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "good.md")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readFileIfExists(path)
	if got != "hello" {
		t.Errorf("got %q; want hello", got)
	}
}
