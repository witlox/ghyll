package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/witlox/ghyll/bootstrap"
	"github.com/witlox/ghyll/ui"
)

// TestScenario_CmdInitBootstrap_WritesGridForEmptyRepo runs the
// bootstrap pipeline end-to-end against a fresh, near-empty temp
// directory and verifies the resulting grid is well-formed and
// readable via bootstrap.Read.
//
// Integrator finding C-1: this is the production-time wiring that
// the BDD-only path used to lack. The test exercises the same code
// path users hit when they run `ghyll init --op-id alice ./newproj`.
func TestScenario_CmdInitBootstrap_WritesGridForEmptyRepo(t *testing.T) {
	dir := t.TempDir()
	// Drop a marker file so the dir isn't completely empty (mirrors
	// the realistic "operator just `git init`'d a fresh repo" state;
	// also exercises the profile walker without requiring any
	// recognized language extension).
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# new project\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := cmdInitBootstrap([]string{"--op-id", "alice@example.com", dir}); err != nil {
		t.Fatalf("cmdInitBootstrap: %v", err)
	}

	gridPath := filepath.Join(dir, ".ghyll", "grid.v1.yaml")
	if _, err := os.Stat(gridPath); err != nil {
		t.Fatalf("grid file not written: %v", err)
	}

	// grid.current pointer must be present and name v1.
	pointerPath := filepath.Join(dir, ".ghyll", "grid.current")
	pointer, err := os.ReadFile(pointerPath)
	if err != nil {
		t.Fatalf("grid.current missing: %v", err)
	}
	if got := strings.TrimSpace(string(pointer)); got != "v1" {
		t.Errorf("grid.current = %q; want %q", got, "v1")
	}

	// Parse via the canonical reader and verify non-zero arrow count
	// (4 role pairs × at least 1 context = at least 4 arrows).
	g, err := bootstrap.Read(dir)
	if err != nil {
		t.Fatalf("bootstrap.Read(%s): %v", dir, err)
	}
	if g.GridVersion != 1 {
		t.Errorf("GridVersion = %d; want 1", g.GridVersion)
	}
	if g.CreatedByOpID != "alice@example.com" {
		t.Errorf("CreatedByOpID = %q; want %q", g.CreatedByOpID, "alice@example.com")
	}
	if len(g.BoundedContexts) == 0 {
		t.Errorf("BoundedContexts empty; expected at least one auto-declared context")
	}
	if len(g.Arrows) != 4*len(g.BoundedContexts) {
		t.Errorf("Arrows = %d; want %d (4 role pairs * %d contexts)",
			len(g.Arrows), 4*len(g.BoundedContexts), len(g.BoundedContexts))
	}
}

// TestScenario_CmdInitBootstrap_RefusesToClobber verifies that
// `ghyll init` will not overwrite an existing grid.v1.yaml. Re-init
// support is a downstream amendment-component feature; the
// bootstrap path produces v1 only.
func TestScenario_CmdInitBootstrap_RefusesToClobber(t *testing.T) {
	dir := t.TempDir()
	ghyllDir := filepath.Join(dir, ".ghyll")
	if err := os.MkdirAll(ghyllDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-create a stub grid file. Content does not need to be
	// well-formed; the refuse-to-clobber check predates parsing.
	if err := os.WriteFile(filepath.Join(ghyllDir, "grid.v1.yaml"), []byte("grid-version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := cmdInitBootstrap([]string{"--op-id", "alice@example.com", dir})
	if err == nil {
		t.Fatal("cmdInitBootstrap returned nil; want refuse-to-clobber error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("err = %v; want already-exists clobber refusal", err)
	}
}

// TestScenario_CmdInitBootstrap_RejectsMissingOpID asserts the
// op-id flag is mandatory.
func TestScenario_CmdInitBootstrap_RejectsMissingOpID(t *testing.T) {
	dir := t.TempDir()
	err := cmdInitBootstrap([]string{dir})
	if err == nil || !strings.Contains(err.Error(), "--op-id is required") {
		t.Errorf("err = %v; want --op-id required", err)
	}
}

// TestScenario_CmdInitBootstrap_RejectsInvalidOpID verifies the
// bootstrap path shares validateOpID with the attest path.
func TestScenario_CmdInitBootstrap_RejectsInvalidOpID(t *testing.T) {
	dir := t.TempDir()
	err := cmdInitBootstrap([]string{"--op-id", "alice/bob", dir})
	if err == nil {
		t.Fatal("err = nil; want op-id rejection")
	}
	if !errors.Is(err, ErrOpIDInvalidCharacters) {
		t.Errorf("err = %v; want ErrOpIDInvalidCharacters", err)
	}
}

// TestScenario_CmdInitBootstrap_NormalizesOpIDToNFC covers H-A
// post-prod-readiness adversarial remediation: ghyll init must store
// the NFC-normalized form of the operator's op-id so the grid's
// created-by-op-id equals what bootstrap.Session would record from
// the same input via a different encoding form.
//
// Decomposed "café" is constructed via explicit byte sequence to
// keep the test robust against editor / source-formatter Unicode
// normalization (which would silently make decomposed == composed
// in the source file). Composed form is built the same way for
// symmetry.
func TestScenario_CmdInitBootstrap_NormalizesOpIDToNFC(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Decomposed: c, a, f, e (U+0065), combining acute (U+0301).
	// UTF-8 bytes: 0x63 0x61 0x66 0x65 0xCC 0x81.
	decomposed := string([]byte{0x63, 0x61, 0x66, 0x65, 0xCC, 0x81})
	// Composed: c, a, f, é (U+00E9). UTF-8 bytes: 0x63 0x61 0x66 0xC3 0xA9.
	composed := string([]byte{0x63, 0x61, 0x66, 0xC3, 0xA9})
	if decomposed == composed {
		t.Fatal("test fixture bug: decomposed and composed byte sequences must differ")
	}

	if err := cmdInitBootstrap([]string{"--op-id", decomposed, dir}); err != nil {
		t.Fatalf("cmdInitBootstrap (decomposed): %v", err)
	}
	g, err := bootstrap.Read(dir)
	if err != nil {
		t.Fatalf("bootstrap.Read: %v", err)
	}
	if g.CreatedByOpID != composed {
		t.Errorf("CreatedByOpID = %q; want NFC-normalized %q", g.CreatedByOpID, composed)
	}
}

// TestScenario_CmdInitBootstrap_AutoDeclaredDefaultContextHinted
// covers post-prod-readiness adversarial L-B: when the repo has no
// detected bounded contexts and ghyll auto-declares a single
// "default" context, the success summary must call that out
// inline so the operator running init in a multi-context repo
// doesn't silently miss the "no contexts detected" signal.
func TestScenario_CmdInitBootstrap_AutoDeclaredDefaultContextHinted(t *testing.T) {
	dir := t.TempDir()
	// A docs-only repo: greenfield, no bounded contexts to detect.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var captured bytes.Buffer
	prevOut, prevErr := ui.Stdout(), ui.Stderr()
	ui.SetOutput(&captured, prevErr)
	t.Cleanup(func() { ui.SetOutput(prevOut, prevErr) })

	if err := cmdInitBootstrap([]string{"--op-id", "alice", dir}); err != nil {
		t.Fatalf("cmdInitBootstrap: %v", err)
	}
	out := captured.String()
	if !strings.Contains(out, "default context auto-declared") {
		t.Errorf("success summary missing auto-declared hint; got:\n%s", out)
	}
	if !strings.Contains(out, "no bounded contexts detected") {
		t.Errorf("success summary missing detection-signal hint; got:\n%s", out)
	}
}

// TestScenario_CmdInitBootstrap_DetectedContextsNoAutoHint is the
// inverse: when bounded contexts ARE detected from src/<n>/ dirs,
// the success summary must NOT carry the auto-declared hint.
func TestScenario_CmdInitBootstrap_DetectedContextsNoAutoHint(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src", "checkout")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package checkout\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var captured bytes.Buffer
	prevOut, prevErr := ui.Stdout(), ui.Stderr()
	ui.SetOutput(&captured, prevErr)
	t.Cleanup(func() { ui.SetOutput(prevOut, prevErr) })

	if err := cmdInitBootstrap([]string{"--op-id", "alice", dir}); err != nil {
		t.Fatalf("cmdInitBootstrap: %v", err)
	}
	out := captured.String()
	if strings.Contains(out, "default context auto-declared") {
		t.Errorf("success summary carried auto-declared hint when contexts WERE detected; got:\n%s", out)
	}
}

// TestScenario_CmdInitMain_RoutesAttestVsBootstrap verifies the
// router: `ghyll init attest ...` reaches the existing attest
// command; `ghyll init ...` (no subcommand) reaches the bootstrap
// command. Both share the `init` umbrella without colliding.
func TestScenario_CmdInitMain_RoutesAttestVsBootstrap(t *testing.T) {
	// attest path with no args triggers cmdInitAttest's own usage
	// error (the --op-id required check), not the bootstrap path's.
	err := cmdInitMain([]string{"attest"})
	if err == nil || !strings.Contains(err.Error(), "ghyll init attest:") {
		t.Errorf("attest route err = %v; want ghyll init attest prefix", err)
	}

	// bootstrap path with no args triggers cmdInitBootstrap's
	// --op-id check, not the attest path's.
	err = cmdInitMain([]string{})
	if err == nil || !strings.Contains(err.Error(), "ghyll init:") || strings.Contains(err.Error(), "attest") {
		t.Errorf("bootstrap route err = %v; want ghyll init (non-attest) prefix", err)
	}
}
