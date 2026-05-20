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
