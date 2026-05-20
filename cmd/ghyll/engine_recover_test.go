package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/witlox/ghyll/engine"
)

// TestScenario_EngineRecover_DryRun_ReportsAndRollsBack covers
// G2-F-2 / G2-I-1 / G2-I-T1: `ghyll engine recover --dry-run`
// must run RecoveryInTx against a rolled-back transaction. The
// engine row state MUST NOT change after the CLI returns.
func TestScenario_EngineRecover_DryRun_ReportsAndRollsBack(t *testing.T) {
	rt, workdir := newTier0Runtime(t)
	if _, err := rt.replayEngine(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := rt.attachJournal(nil); err != nil {
		t.Fatal(err)
	}
	// Seed an orphan pass directly via the store.
	if err := rt.Store().UpsertPass(context.Background(), engine.PassRecord{
		PassID: "P-orphan", Role: "analyst", Context: "A", ArrowID: "A1",
		State: "open", OpenedAt: "2026-05-20T10:00:00Z",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rt.journal.Flush()
	rt.closeEngine()

	withUICaptured(t, func(out, _ *bytes.Buffer) {
		if err := cmdEngineRecover([]string{"--dry-run", "--dir", workdir}); err != nil {
			t.Fatalf("cmdEngineRecover: %v", err)
		}
		got := out.String()
		if !strings.Contains(got, "orphans aborted:") {
			t.Errorf("dry-run output missing orphan count\n--- got ---\n%s", got)
		}
		if !strings.Contains(got, "--dry-run; no changes persisted") {
			t.Errorf("dry-run banner missing\n--- got ---\n%s", got)
		}
	})

	// Critical: verify the rollback worked. The orphan must STILL
	// be state=open after the dry-run.
	store, err := engine.OpenStore(workdir + "/.ghyll/engine.db")
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	defer func() { _ = store.Close() }()
	got, ok, err := store.GetPass(context.Background(), "P-orphan")
	if err != nil {
		t.Fatalf("GetPass: %v", err)
	}
	if !ok {
		t.Fatal("P-orphan missing after dry-run")
	}
	if got.State != "open" {
		t.Errorf("post-dry-run state = %q; want open (rollback failed?)", got.State)
	}
	if got.RecoveredAt != "" {
		t.Errorf("post-dry-run recovered_at = %q; want empty (rollback failed?)", got.RecoveredAt)
	}
}

// TestScenario_EngineRecover_RefusesCommit verifies --commit is
// explicitly refused with a clear message.
func TestScenario_EngineRecover_RefusesCommit(t *testing.T) {
	_, workdir := newTier0Runtime(t)
	err := cmdEngineRecover([]string{"--commit", "--dir", workdir})
	if err == nil {
		t.Fatal("--commit should be refused")
	}
	if !strings.Contains(err.Error(), "--commit is not supported") {
		t.Errorf("err = %v; want --commit refusal", err)
	}
}
