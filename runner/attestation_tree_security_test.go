package runner

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestScenario_EncodeAttestationPath_DotDotInContextSubstituted
// verifies a `Context: ".."` is hash-substituted at safeSegment so
// filepath.Join can't cancel `v<N>` (gate-2 SEC-C-1). Same for `.`.
func TestScenario_EncodeAttestationPath_DotDotInContextSubstituted(t *testing.T) {
	for _, attack := range []string{"..", "."} {
		rec := AttestationRecord{
			PassID:         "P-1",
			AttestedByRole: "operator",
			SourceRole:     "analyst",
			TargetRole:     "architect",
			Context:        attack,
			Stratum:        "L1",
			GridVersion:    1,
		}
		path, truncated, err := EncodeAttestationPath(rec)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if !truncated {
			t.Errorf("attack %q: expected truncated=true (hash-substituted)", attack)
		}
		segs := strings.Split(path, string(filepath.Separator))
		// segs[1] is the context segment. Must NOT be exactly the
		// attack string (would escape via filepath.Join).
		if segs[1] == attack {
			t.Errorf("attack %q: context segment passed through; path=%q", attack, path)
		}
		if !strings.HasPrefix(segs[1], "h-") {
			t.Errorf("attack %q: context segment %q lacks hash prefix", attack, segs[1])
		}
	}
}

// TestScenario_EncodeAttestationPath_StratumDotDot_NoEscape verifies
// that even though `Stratum: ".."` produces a segment "stratum-.."
// (which is a literal directory name, NOT a parent reference), the
// joined path stays under the v<N> root because the stratum prefix
// neutralizes traversal. Documents the prefix-defense invariant.
func TestScenario_EncodeAttestationPath_StratumDotDot_NoEscape(t *testing.T) {
	rec := AttestationRecord{
		PassID:         "P-1",
		AttestedByRole: "operator",
		SourceRole:     "analyst",
		TargetRole:     "architect",
		Context:        "ctxA",
		Stratum:        "..",
		GridVersion:    1,
	}
	path, _, err := EncodeAttestationPath(rec)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	full := filepath.Clean(filepath.Join("/root", path))
	if !strings.HasPrefix(full, "/root/v1/") {
		t.Errorf("path %q escapes v<N>", full)
	}
}

// TestScenario_TruncateTrailingPartialFile_RefusesSymlink verifies
// the symlink guard added in gate-2 SEC-C-2. Skipped on Windows
// (limited symlink support without privileges).
func TestScenario_TruncateTrailingPartialFile_RefusesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test skipped on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("important content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.jsonl")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	err := truncateTrailingPartialFile(link)
	if err == nil {
		t.Error("truncateTrailingPartialFile accepted a symlink; expected refusal")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("err = %v; want symlink-refusal", err)
	}
	// Target must be untouched.
	data, _ := os.ReadFile(target)
	if string(data) != "important content\n" {
		t.Errorf("target modified: %q", data)
	}
}

// TestScenario_EncodeAttestationPath_StayWithinTreeRoot is a higher-
// level adversarial check: regardless of operator-controlled
// Context/Stratum/SourceRole/TargetRole, the joined path stays
// under the v<N> subtree. Gate-2 SEC-C-1 integration check.
func TestScenario_EncodeAttestationPath_StayWithinTreeRoot(t *testing.T) {
	root := "/tmp/ghyll-tree-root"
	// Cover every operator-controllable field. Stratum + roles
	// are prefix-wrapped so ".." doesn't yield a bare-segment
	// traversal, but Context is a bare segment — that's the path
	// safeSegment hardens (SEC-C-1).
	cases := []struct {
		ctx     string
		stratum string
		src     string
		tgt     string
	}{
		{"..", "L1", "analyst", "architect"},
		{".", "L1", "analyst", "architect"},
		{"ctxA", "..", "analyst", "architect"},
		{"ctxA", ".", "analyst", "architect"},
		{"ctxA", "L1", "..", "architect"},
		{"ctxA", "L1", "analyst", ".."},
		// Combinations:
		{"..", "..", "..", ".."},
	}
	for _, c := range cases {
		rec := AttestationRecord{
			PassID:         "P-1",
			AttestedByRole: "operator",
			SourceRole:     c.src,
			TargetRole:     c.tgt,
			Context:        c.ctx,
			Stratum:        c.stratum,
			GridVersion:    1,
		}
		path, _, err := EncodeAttestationPath(rec)
		if err != nil {
			t.Errorf("case %+v: encode err %v", c, err)
			continue
		}
		full := filepath.Clean(filepath.Join(root, path))
		if !strings.HasPrefix(full, root+string(filepath.Separator)) {
			t.Errorf("case %+v: path %q escapes root %q", c, full, root)
		}
	}
}
