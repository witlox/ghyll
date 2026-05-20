package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/witlox/ghyll/bootstrap"
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
