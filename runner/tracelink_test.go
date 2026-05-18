package runner

import (
	"context"
	"os"
	"testing"
)

// makeSymlink is a test helper used across runner tests. Returns
// the underlying os.Symlink error (likely fs.ErrPermission on
// Windows; tests should t.Skip on that).
func makeSymlink(t *testing.T, target, name string) error {
	t.Helper()
	return os.Symlink(target, name)
}

func TestTraceLinkPresent_AllLinksResolve(t *testing.T) {
	dir := t.TempDir()
	// Specs reference test names; tests exist with those names.
	writeFile(t, dir, "specs/feature-a.md", "See test `feature_a_test`.\n")
	writeFile(t, dir, "specs/feature-b.md", "See test `feature_b_test`.\n")
	writeFile(t, dir, "tests/feature_a_test.go", "package tests\n")
	writeFile(t, dir, "tests/feature_b_test.go", "package tests\n")
	res, err := EvaluateTraceLinkPresent(context.Background(), Clause{
		ProjectDir: dir,
		Args: map[string]any{
			"from":      "specs/*.md",
			"to":        "tests/*.go",
			"link-rule": "`(\\w+_test)`",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pass {
		t.Errorf("expected pass; got %+v", res.Details)
	}
	if res.Details["unmet"] != 0 {
		t.Errorf("unmet = %v; want 0", res.Details["unmet"])
	}
}

func TestTraceLinkPresent_MissingLinkRefused(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "specs/feature.md", "See test `nonexistent_test`.\n")
	writeFile(t, dir, "tests/other_test.go", "package tests\n")
	res, _ := EvaluateTraceLinkPresent(context.Background(), Clause{
		ProjectDir: dir,
		Args: map[string]any{
			"from":      "specs/*.md",
			"to":        "tests/*.go",
			"link-rule": "`(\\w+_test)`",
		},
	})
	if res.Pass {
		t.Errorf("expected fail (link target missing); got %+v", res.Details)
	}
}

func TestTraceLinkPresent_MultiplicityBounds(t *testing.T) {
	dir := t.TempDir()
	// Two refs in one spec; min=2.
	writeFile(t, dir, "specs/feature.md", "See `a_test` and `b_test`.\n")
	writeFile(t, dir, "tests/a_test.go", "")
	writeFile(t, dir, "tests/b_test.go", "")
	res, err := EvaluateTraceLinkPresent(context.Background(), Clause{
		ProjectDir: dir,
		Args: map[string]any{
			"from":             "specs/*.md",
			"to":               "tests/*.go",
			"link-rule":        "`(\\w+_test)`",
			"min-multiplicity": 2,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pass {
		t.Errorf("expected pass (2 links meets min=2); got %+v", res.Details)
	}
	// Same content, min=3: should fail.
	res, _ = EvaluateTraceLinkPresent(context.Background(), Clause{
		ProjectDir: dir,
		Args: map[string]any{
			"from":             "specs/*.md",
			"to":               "tests/*.go",
			"link-rule":        "`(\\w+_test)`",
			"min-multiplicity": 3,
		},
	})
	if res.Pass {
		t.Error("expected fail (2 links < min=3)")
	}
}

func TestTraceLinkPresent_InvalidRegex(t *testing.T) {
	_, err := EvaluateTraceLinkPresent(context.Background(), Clause{
		Args: map[string]any{
			"from":      "**",
			"to":        "**",
			"link-rule": "[unclosed",
		},
	})
	if err == nil {
		t.Error("invalid regex should error")
	}
}

func TestTraceLinkPresent_RegexNoCaptureGroup(t *testing.T) {
	_, err := EvaluateTraceLinkPresent(context.Background(), Clause{
		Args: map[string]any{
			"from":      "**",
			"to":        "**",
			"link-rule": "test", // no capture group
		},
	})
	if err == nil {
		t.Error("link-rule without capture should error")
	}
}

func TestBuildLinkIndex(t *testing.T) {
	files := []string{
		"tests/feature_a_test.go",
		"tests/feature_b_test.py",
	}
	idx := buildLinkIndex(files)
	wantKeys := []string{
		"tests/feature_a_test.go",
		"feature_a_test.go",
		"feature_a_test",
		"tests/feature_b_test.py",
		"feature_b_test.py",
		"feature_b_test",
	}
	for _, k := range wantKeys {
		files, ok := idx[k]
		if !ok || len(files) == 0 {
			t.Errorf("index missing key %q", k)
		}
	}
}

func TestBuildLinkIndex_BasenameCollisions(t *testing.T) {
	// F14: auth.go and auth.md both contribute basename "auth";
	// the index records BOTH so the operator sees the collision in
	// the per-from results rather than getting a silent false-pass.
	files := []string{
		"src/auth.go",
		"docs/auth.md",
	}
	idx := buildLinkIndex(files)
	authMatches, ok := idx["auth"]
	if !ok || len(authMatches) != 2 {
		t.Errorf("auth should resolve to 2 files; got %v", authMatches)
	}
}
