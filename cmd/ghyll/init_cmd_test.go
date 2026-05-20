package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/witlox/ghyll/bootstrap"
	"github.com/witlox/ghyll/engine"
)

// TestScenario_CmdInitAttest_RecordsOneRowPerArrow verifies the
// production CLI for gate-2 CORR-A-18: `ghyll init attest`
// writes one init AttestationRecord per arrow in the grid.
func TestScenario_CmdInitAttest_RecordsOneRowPerArrow(t *testing.T) {
	dir := t.TempDir()
	ghyllDir := filepath.Join(dir, ".ghyll")
	if err := os.MkdirAll(ghyllDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Seed a grid with two arrows.
	g := bootstrap.NewGrid("alice@example.com")
	g.GridVersion = 1
	g.BoundedContexts = []bootstrap.BoundedContext{{ID: "checkout"}}
	g.Arrows = []map[string]any{
		{"upstream": "analyst", "downstream": "architect", "context": "checkout"},
		{"upstream": "architect", "downstream": "implementer", "context": "checkout"},
	}
	if err := g.Write(dir); err != nil {
		t.Fatal(err)
	}

	if err := cmdInitAttest([]string{"--op-id", "alice@example.com", "--dir", dir}); err != nil {
		t.Fatalf("cmdInitAttest: %v", err)
	}

	// Reopen the engine and confirm 2 attestations landed.
	store, err := engine.OpenStoreReadOnly(filepath.Join(ghyllDir, "engine.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	n, err := store.CountAttestations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("engine attestation count = %d; want 2", n)
	}

	// Idempotent: re-running is a no-op.
	if err := cmdInitAttest([]string{"--op-id", "alice@example.com", "--dir", dir}); err != nil {
		t.Fatalf("cmdInitAttest second run: %v", err)
	}
	n, _ = store.CountAttestations(context.Background())
	if n != 2 {
		t.Errorf("after second run count = %d; want still 2 (idempotent)", n)
	}
}

// TestScenario_CmdInitAttest_RejectsInvalidOpID verifies the
// command shares validateOpID with the slash-command path.
func TestScenario_CmdInitAttest_RejectsInvalidOpID(t *testing.T) {
	err := cmdInitAttest([]string{"--op-id", "alice/bob"})
	if err == nil || !strings.Contains(err.Error(), "path-separator") {
		t.Errorf("err = %v; want path-separator rejection", err)
	}
}

// TestScenario_CmdInitAttest_RejectsMissingOpID verifies the
// usage check.
func TestScenario_CmdInitAttest_RejectsMissingOpID(t *testing.T) {
	err := cmdInitAttest([]string{})
	if err == nil || !strings.Contains(err.Error(), "--op-id is required") {
		t.Errorf("err = %v; want --op-id required", err)
	}
}
