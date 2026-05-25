package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScenario_ReadVersion_RejectsOversizedGrid verifies Tier 3 /
// SR H-2 — a 5 MiB grid.v1.yaml is refused before yaml.Unmarshal.
func TestScenario_ReadVersion_RejectsOversizedGrid(t *testing.T) {
	dir := t.TempDir()
	ghyllDir := filepath.Join(dir, ".ghyll")
	if err := os.MkdirAll(ghyllDir, 0o755); err != nil {
		t.Fatal(err)
	}
	huge := strings.Repeat("# x\n", 5*1024*256) // ~5 MiB of comments
	if err := os.WriteFile(filepath.Join(ghyllDir, "grid.v1.yaml"), []byte(huge), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadVersion(dir, 1)
	if err == nil {
		t.Error("oversized grid accepted; want error")
	}
	if !strings.Contains(err.Error(), "exceeds") && !strings.Contains(err.Error(), "size") {
		t.Errorf("err = %v; want size-cap rejection", err)
	}
}

// TestScenario_ReadVersion_RejectsTooManyBoundedContexts verifies
// Tier 3 / SR L-8 — 300 bounded contexts exceeds the cap.
func TestScenario_ReadVersion_RejectsTooManyBoundedContexts(t *testing.T) {
	dir := t.TempDir()
	ghyllDir := filepath.Join(dir, ".ghyll")
	if err := os.MkdirAll(ghyllDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("grid-version: 1\ncreated-at: t\ncreated-by-op-id: alice\nbounded-contexts:\n")
	for i := 0; i < 300; i++ {
		b.WriteString("  - id: \"ctx-")
		b.WriteString(itoaBuf(i))
		b.WriteString("\"\n")
	}
	b.WriteString("depth-ladder:\n  - tier: 0\n    label: NONE\nseverity-threshold: medium\ninsufficient-basis-rounds-max: 3\nremediation-rounds-max: 5\n")
	if err := os.WriteFile(filepath.Join(ghyllDir, "grid.v1.yaml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadVersion(dir, 1)
	if err == nil {
		t.Error("300-context grid accepted; want cap-rejection")
	}
}

// TestScenario_ReadVersion_RejectsOutOfRangeResidueCap verifies
// Tier 3 / SR M-10 — residue-note-max-bytes outside [1024, 1 MiB]
// is refused at load.
func TestScenario_ReadVersion_RejectsOutOfRangeResidueCap(t *testing.T) {
	dir := t.TempDir()
	ghyllDir := filepath.Join(dir, ".ghyll")
	if err := os.MkdirAll(ghyllDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := []byte(`grid-version: 1
created-at: t
created-by-op-id: alice
bounded-contexts:
  - id: ctxA
depth-ladder:
  - tier: 0
    label: NONE
severity-threshold: medium
insufficient-basis-rounds-max: 3
remediation-rounds-max: 5
residue-note-max-bytes: 1
`)
	if err := os.WriteFile(filepath.Join(ghyllDir, "grid.v1.yaml"), yaml, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadVersion(dir, 1)
	if err == nil {
		t.Error("residue=1 accepted; want range rejection")
	}
}

// TestScenario_GridWrite_GhyllDirIsPrivate covers post-prod-readiness
// adversarial M-B: the .ghyll/ directory must be created at 0o700,
// not the os.MkdirAll default of 0o755. The grid yaml file itself
// is project-shared (0o644 OK) but the sibling engine.db contains
// op-id-keyed attestation records — a world-stat-able directory
// leaks engine.db's existence + size to other users on the host.
func TestScenario_GridWrite_GhyllDirIsPrivate(t *testing.T) {
	dir := t.TempDir()
	g := NewGrid("alice")
	if err := g.Write(dir); err != nil {
		t.Fatalf("Grid.Write: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, ".ghyll"))
	if err != nil {
		t.Fatalf("stat .ghyll: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf(".ghyll perm = %#o; want %#o", got, 0o700)
	}
}

// TestScenario_GridWrite_TightensPreExisting0755Dir confirms the
// chmod path: if a previous binary (or a parallel writer) already
// created .ghyll at 0o755, the next Grid.Write call tightens it to
// 0o700.
func TestScenario_GridWrite_TightensPreExisting0755Dir(t *testing.T) {
	dir := t.TempDir()
	ghyllDir := filepath.Join(dir, ".ghyll")
	if err := os.MkdirAll(ghyllDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Sanity: ensure the pre-existing dir is loose so the test
	// genuinely exercises the chmod path.
	if info, err := os.Stat(ghyllDir); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o755 {
		t.Fatalf("pre-existing perm = %#o; test setup expected %#o", info.Mode().Perm(), 0o755)
	}
	g := NewGrid("alice")
	if err := g.Write(dir); err != nil {
		t.Fatalf("Grid.Write: %v", err)
	}
	info, err := os.Stat(ghyllDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf(".ghyll perm = %#o after Write; want %#o", got, 0o700)
	}
}

// itoaBuf — local stringer to avoid strconv import.
func itoaBuf(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
