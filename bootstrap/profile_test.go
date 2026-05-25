package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestProfileRepo_EmptyDirIsGreenfield(t *testing.T) {
	// Scenario 11: a project directory with no source files and no
	// prior grid is greenfield with 0 contexts.
	dir := t.TempDir()
	p, err := ProfileRepo(dir)
	if err != nil {
		t.Fatalf("ProfileRepo: %v", err)
	}
	if !p.IsGreenfield() {
		t.Errorf("Mode = %q; want greenfield", p.Mode)
	}
	if len(p.BoundedContexts) != 0 {
		t.Errorf("BoundedContexts = %v; want empty", p.BoundedContexts)
	}
	if len(p.Languages) != 0 {
		t.Errorf("Languages = %v; want empty", p.Languages)
	}
	if !p.NeedsContextInterrogation() {
		t.Error("greenfield with 0 contexts should NeedContextInterrogation")
	}
	// Diamond roles are fixed.
	if len(p.DiamondRoles) != 4 {
		t.Errorf("DiamondRoles len = %d; want 4", len(p.DiamondRoles))
	}
}

func TestProfileRepo_OnlyDocsIsGreenfield(t *testing.T) {
	// A repo with only docs / specs / a README is still greenfield — no
	// source code has been written yet.
	dir := t.TempDir()
	mustWrite(t, dir, "README.md", "# Project")
	mustMkdir(t, dir, "docs")
	mustWrite(t, dir, "docs/architecture.md", "# Arch")
	mustMkdir(t, dir, "specs")
	mustWrite(t, dir, "specs/foo.md", "# Foo")
	p, err := ProfileRepo(dir)
	if err != nil {
		t.Fatalf("ProfileRepo: %v", err)
	}
	if !p.IsGreenfield() {
		t.Errorf("Mode = %q; want greenfield (docs/specs only)", p.Mode)
	}
}

func TestProfileRepo_BrownfieldFromSrcDirectories(t *testing.T) {
	// Scenario 33: existing source in src/contextA/ and src/contextB/
	// → brownfield with two proposed contexts.
	dir := t.TempDir()
	mustMkdir(t, dir, "src/contextA")
	mustWrite(t, dir, "src/contextA/main.go", "package contextA")
	mustMkdir(t, dir, "src/contextB")
	mustWrite(t, dir, "src/contextB/main.go", "package contextB")
	p, err := ProfileRepo(dir)
	if err != nil {
		t.Fatalf("ProfileRepo: %v", err)
	}
	if !p.IsBrownfield() {
		t.Errorf("Mode = %q; want brownfield", p.Mode)
	}
	if len(p.BoundedContexts) != 2 {
		t.Fatalf("BoundedContexts len = %d; want 2", len(p.BoundedContexts))
	}
	// Lexicographic order.
	want := []string{"contextA", "contextB"}
	for i, c := range p.BoundedContexts {
		if c.ID != want[i] {
			t.Errorf("BoundedContexts[%d].ID = %q; want %q", i, c.ID, want[i])
		}
	}
	// Language detection from .go files.
	if len(p.Languages) != 1 || p.Languages[0] != "go" {
		t.Errorf("Languages = %v; want [go]", p.Languages)
	}
	if p.NeedsContextInterrogation() {
		t.Error("brownfield with discovered contexts should not need interrogation")
	}
}

func TestProfileRepo_BrownfieldMultipleLanguages(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, dir, "src/contextA")
	mustWrite(t, dir, "src/contextA/main.go", "package contextA")
	mustMkdir(t, dir, "src/contextB")
	mustWrite(t, dir, "src/contextB/main.py", "def main(): pass")
	mustMkdir(t, dir, "src/contextC")
	mustWrite(t, dir, "src/contextC/main.rs", "fn main() {}")
	p, err := ProfileRepo(dir)
	if err != nil {
		t.Fatalf("ProfileRepo: %v", err)
	}
	// Languages sorted lexicographically.
	want := []string{"go", "python", "rust"}
	if len(p.Languages) != 3 {
		t.Fatalf("Languages = %v; want [go python rust]", p.Languages)
	}
	for i, lang := range p.Languages {
		if lang != want[i] {
			t.Errorf("Languages[%d] = %q; want %q", i, lang, want[i])
		}
	}
}

func TestProfileRepo_SkipsHarnessAndBuildDirs(t *testing.T) {
	// Source files in .git/, .ghyll/, node_modules/, vendor/, target/,
	// build/, dist/, etc. must NOT count toward brownfield detection.
	dir := t.TempDir()
	mustMkdir(t, dir, ".git/objects")
	mustWrite(t, dir, ".git/objects/foo.go", "package foo")
	mustMkdir(t, dir, "node_modules/pkg")
	mustWrite(t, dir, "node_modules/pkg/index.js", "export {}")
	mustMkdir(t, dir, "vendor/lib")
	mustWrite(t, dir, "vendor/lib/lib.go", "package lib")
	mustMkdir(t, dir, "target/debug")
	mustWrite(t, dir, "target/debug/main.rs", "fn main() {}")
	p, err := ProfileRepo(dir)
	if err != nil {
		t.Fatalf("ProfileRepo: %v", err)
	}
	if !p.IsGreenfield() {
		t.Errorf("Mode = %q; want greenfield (all source under skip-dirs)", p.Mode)
	}
}

func TestProfileRepo_SrcWithoutSubdirsHasNoContexts(t *testing.T) {
	// A src/ directory containing only files (no subdirectories)
	// proposes zero contexts — the operator must declare them.
	dir := t.TempDir()
	mustMkdir(t, dir, "src")
	mustWrite(t, dir, "src/main.go", "package main")
	p, err := ProfileRepo(dir)
	if err != nil {
		t.Fatalf("ProfileRepo: %v", err)
	}
	if !p.IsBrownfield() {
		t.Errorf("Mode = %q; want brownfield (.go file present)", p.Mode)
	}
	if len(p.BoundedContexts) != 0 {
		t.Errorf("BoundedContexts = %v; want empty (no src/<ctx>/ dirs)", p.BoundedContexts)
	}
	if !p.NeedsContextInterrogation() {
		t.Error("brownfield with no proposed contexts should need interrogation")
	}
}

func TestProfileRepo_NoSrcDir(t *testing.T) {
	// Source code outside src/ (e.g., a Go module with code at repo
	// root) → brownfield, but no contexts proposed (no src/<ctx>/).
	dir := t.TempDir()
	mustWrite(t, dir, "main.go", "package main")
	p, err := ProfileRepo(dir)
	if err != nil {
		t.Fatalf("ProfileRepo: %v", err)
	}
	if !p.IsBrownfield() {
		t.Errorf("Mode = %q; want brownfield", p.Mode)
	}
	if len(p.BoundedContexts) != 0 {
		t.Errorf("BoundedContexts = %v; want empty (no src/)", p.BoundedContexts)
	}
}

func TestProfileRepo_EmptyPath(t *testing.T) {
	_, err := ProfileRepo("")
	if !errors.Is(err, ErrProfileNilDir) {
		t.Errorf("err = %v; want ErrProfileNilDir", err)
	}
}

func TestProfileRepo_MissingDir(t *testing.T) {
	_, err := ProfileRepo("/no/such/dir/that/exists")
	if err == nil {
		t.Error("expected error for missing dir; got nil")
	}
}

func TestProfileRepo_PathIsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "afile")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ProfileRepo(path)
	if err == nil {
		t.Error("expected error for non-directory; got nil")
	}
}

func TestDeclareContext_AppendsToProfile(t *testing.T) {
	// Scenario 19: operator answers context-identification interrogation;
	// init records the declared contexts.
	dir := t.TempDir()
	p, err := ProfileRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.DeclareContext("ContextA", "Payments processing"); err != nil {
		t.Fatalf("DeclareContext(ContextA): %v", err)
	}
	if err := p.DeclareContext("ContextB", "User identity"); err != nil {
		t.Fatalf("DeclareContext(ContextB): %v", err)
	}
	if len(p.BoundedContexts) != 2 {
		t.Errorf("BoundedContexts len = %d; want 2", len(p.BoundedContexts))
	}
	if p.NeedsContextInterrogation() {
		t.Error("after two DeclareContext, interrogation should be complete")
	}
}

func TestDeclareContext_TrimsAndValidates(t *testing.T) {
	dir := t.TempDir()
	p, _ := ProfileRepo(dir)

	if err := p.DeclareContext("  ", ""); !errors.Is(err, ErrProfileContextEmpty) {
		t.Errorf("empty id: got %v; want ErrProfileContextEmpty", err)
	}
	if err := p.DeclareContext("123-leading-digit", ""); !errors.Is(err, ErrProfileContextInvalid) {
		t.Errorf("leading digit: got %v; want ErrProfileContextInvalid", err)
	}
	if err := p.DeclareContext("has spaces", ""); !errors.Is(err, ErrProfileContextInvalid) {
		t.Errorf("has spaces: got %v; want ErrProfileContextInvalid", err)
	}
	if err := p.DeclareContext("has/slash", ""); !errors.Is(err, ErrProfileContextInvalid) {
		t.Errorf("slash: got %v; want ErrProfileContextInvalid", err)
	}

	// Valid ones.
	for _, id := range []string{"a", "abc", "abc-def", "abc_def", "abcDef123"} {
		freshDir := t.TempDir()
		fresh, _ := ProfileRepo(freshDir)
		if err := fresh.DeclareContext(id, ""); err != nil {
			t.Errorf("valid id %q rejected: %v", id, err)
		}
	}
}

func TestDeclareContext_RejectsDuplicate(t *testing.T) {
	// Brownfield with src/contextA/ proposed; declaring "contextA"
	// again must be refused.
	dir := t.TempDir()
	mustMkdir(t, dir, "src/contextA")
	mustWrite(t, dir, "src/contextA/main.go", "package contextA")
	p, _ := ProfileRepo(dir)
	if err := p.DeclareContext("contextA", "duplicate"); !errors.Is(err, ErrProfileContextDup) {
		t.Errorf("duplicate id: got %v; want ErrProfileContextDup", err)
	}
}

func TestDeclareContext_NilReceiver(t *testing.T) {
	var p *ProjectProfile
	if err := p.DeclareContext("x", ""); err == nil {
		t.Error("nil receiver should error")
	}
}

// TestScenario_ScanBrownfieldContexts_EnforcesCap covers
// post-prod-readiness adversarial M-A. A pathological repo with
// MaxBoundedContexts+10 valid src/<n>/ directories must surface
// ErrProfileTooManyContexts rather than synthesizing an unbounded
// grid downstream. Mirrors the cap DeclareContext enforces on the
// operator-driven path.
func TestScenario_ScanBrownfieldContexts_EnforcesCap(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create MaxBoundedContexts+10 valid context-id directories with
	// a .go file inside each so the language scan flips the repo
	// into brownfield mode.
	total := MaxBoundedContexts + 10
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("ctx%04d", i)
		full := filepath.Join(srcDir, name)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(full, "main.go"), []byte("package "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := ProfileRepo(dir)
	if err == nil {
		t.Fatalf("ProfileRepo accepted %d contexts; want ErrProfileTooManyContexts", total)
	}
	if !errors.Is(err, ErrProfileTooManyContexts) {
		t.Errorf("err = %v; want ErrProfileTooManyContexts", err)
	}
}

// TestScenario_ScanBrownfieldContexts_AtCapAccepted confirms the
// boundary: exactly MaxBoundedContexts is fine; one more trips
// the cap. Pairs with the over-cap test above.
func TestScenario_ScanBrownfieldContexts_AtCapAccepted(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxBoundedContexts; i++ {
		name := fmt.Sprintf("ctx%04d", i)
		full := filepath.Join(srcDir, name)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(full, "main.go"), []byte("package "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := ProfileRepo(dir)
	if err != nil {
		t.Fatalf("ProfileRepo at cap: %v", err)
	}
	if len(p.BoundedContexts) != MaxBoundedContexts {
		t.Errorf("BoundedContexts len = %d; want %d", len(p.BoundedContexts), MaxBoundedContexts)
	}
}

func TestFixedDiamondRoles(t *testing.T) {
	want := []string{"analyst", "architect", "implementer", "integrator"}
	if len(FixedDiamondRoles) != 4 {
		t.Fatalf("FixedDiamondRoles len = %d; want 4", len(FixedDiamondRoles))
	}
	for i, role := range FixedDiamondRoles {
		if role != want[i] {
			t.Errorf("FixedDiamondRoles[%d] = %q; want %q", i, role, want[i])
		}
	}
}

// mustWrite is a test helper that creates a file under dir with the
// given relative path and content; failing the test on any error.
func mustWrite(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", full, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

// mustMkdir creates a directory (and parents) under dir.
func mustMkdir(t *testing.T, dir, relPath string) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", full, err)
	}
}
