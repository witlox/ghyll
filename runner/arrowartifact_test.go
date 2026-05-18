package runner

import (
	"context"
	"path/filepath"
	"testing"
)

func TestArrowArtifactPresent_Exists(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "out/report.md", "# Integration Report\n\nContent.\n")
	res, err := EvaluateArrowArtifactPresent(context.Background(), Clause{
		ProjectDir: dir,
		Args:       map[string]any{"artifact-path": "out/report.md"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pass {
		t.Errorf("expected pass; got %+v", res.Details)
	}
	if res.Details["exists"] != true {
		t.Errorf("details.exists should be true")
	}
}

func TestArrowArtifactPresent_Missing(t *testing.T) {
	dir := t.TempDir()
	res, _ := EvaluateArrowArtifactPresent(context.Background(), Clause{
		ProjectDir: dir,
		Args:       map[string]any{"artifact-path": "out/report.md"},
	})
	if res.Pass {
		t.Error("expected fail (missing)")
	}
	if res.Details["exists"] != false {
		t.Errorf("details.exists should be false")
	}
}

func TestArrowArtifactPresent_SymlinkRefused(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.md")
	writeFile(t, dir, "real.md", "content\n")
	symlink := filepath.Join(dir, "link.md")
	if err := makeSymlink(t, target, symlink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	res, _ := EvaluateArrowArtifactPresent(context.Background(), Clause{
		ProjectDir: dir,
		Args:       map[string]any{"artifact-path": "link.md"},
	})
	if res.Pass {
		t.Error("symlink should refuse")
	}
}

func TestArrowArtifactPresent_TooSmall(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "out/empty", "")
	res, _ := EvaluateArrowArtifactPresent(context.Background(), Clause{
		ProjectDir: dir,
		Args: map[string]any{
			"artifact-path":  "out/empty",
			"min-size-bytes": 1,
		},
	})
	if res.Pass {
		t.Error("zero-byte file should fail min-size check")
	}
}

func TestArrowArtifactPresent_SchemaCheckPasses(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "out/data.json", `{"ok": true}`)
	res, _ := EvaluateArrowArtifactPresent(context.Background(), Clause{
		ProjectDir: dir,
		Args: map[string]any{
			"artifact-path": "out/data.json",
			"schema-check":  "cat", // trivial: cat always exits 0
		},
	})
	if !res.Pass {
		t.Errorf("expected pass; got %+v", res.Details)
	}
}

func TestArrowArtifactPresent_SchemaCheckFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "out/data", "anything")
	res, _ := EvaluateArrowArtifactPresent(context.Background(), Clause{
		ProjectDir: dir,
		Args: map[string]any{
			"artifact-path": "out/data",
			"schema-check":  "false", // always exits 1
		},
	})
	if res.Pass {
		t.Error("schema-check failure should refuse")
	}
	if got := res.Details["error"]; got != "schema-check failed" {
		t.Errorf("error = %v; want schema-check failed", got)
	}
}

func TestArrowArtifactPresent_MissingPathArg(t *testing.T) {
	_, err := EvaluateArrowArtifactPresent(context.Background(), Clause{
		Args: map[string]any{},
	})
	if err == nil {
		t.Error("missing artifact-path should error")
	}
}

func TestArrowArtifactPresent_InvalidMinSize(t *testing.T) {
	_, err := EvaluateArrowArtifactPresent(context.Background(), Clause{
		Args: map[string]any{
			"artifact-path":  "x",
			"min-size-bytes": 0, // below the documented minimum of 1
		},
	})
	if err == nil {
		t.Error("min-size-bytes=0 should error")
	}
}

func TestCoerceInt64(t *testing.T) {
	cases := []struct {
		in   any
		want int64
		ok   bool
	}{
		{int(5), 5, true},
		{int64(7), 7, true},
		{float64(3), 3, true},
		{float64(2.5), 0, false}, // non-integer float
		{"five", 0, false},
	}
	for _, c := range cases {
		got, err := coerceInt64(c.in)
		if (err == nil) != c.ok {
			t.Errorf("coerceInt64(%v): err=%v; want ok=%v", c.in, err, c.ok)
			continue
		}
		if c.ok && got != c.want {
			t.Errorf("coerceInt64(%v) = %d; want %d", c.in, got, c.want)
		}
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"simple":           "'simple'",
		"with space":       "'with space'",
		"O'Brien":          `'O'\''Brien'`,
		"already 'quoted'": `'already '\''quoted'\'''`,
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q; want %q", in, got, want)
		}
	}
}
