package main

import (
	gocontext "context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/witlox/ghyll/engine"
	"github.com/witlox/ghyll/runner"
)

// writeLegacyJSONL writes a Tier-1-style JSONL with no PassID
// field — simulates a pre-Tier-2 audit trail. Direct write
// bypasses Record's Tier 2 PassID enforcement.
func writeLegacyJSONL(path string, recs ...runner.AttestationRecord) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	for _, rec := range recs {
		line, err := json.Marshal(map[string]any{
			"attestation_id":   rec.ID,
			"kind":             string(rec.Kind),
			"arrow_id":         rec.ArrowID,
			"clause_id":        rec.ClauseID,
			"op_id":            rec.OpID,
			"attested_by_role": rec.AttestedByRole,
			"source_role":      rec.SourceRole,
			"target_role":      rec.TargetRole,
			"verdict":          string(rec.Verdict),
			"reason":           rec.Reason,
			"timestamp":        rec.Timestamp,
			"grid_version":     rec.GridVersion,
		})
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(f, string(line)); err != nil {
			return err
		}
	}
	return f.Sync()
}

// TestScenario_OpenEngine_Tier1ToTier2Migration verifies a project
// upgrading from Tier 1 (flat .ghyll/attestations.jsonl + engine
// rows, no tree dir) boots cleanly: LoadFromTree fails with
// ErrAttestationAuditLost, the fallback loads from JSONL, and
// attachJournal re-emits each record through the tree primary
// writer. Gate-2 CORR-A-3 coverage.
func TestScenario_OpenEngine_Tier1ToTier2Migration(t *testing.T) {
	workdir := t.TempDir()
	ghyllDir := filepath.Join(workdir, ".ghyll")
	if err := os.MkdirAll(ghyllDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Step 1: simulate a Tier 1 project — flat JSONL with 2 records,
	// engine table with the same 2 rows, NO tree dir.
	dbPath := filepath.Join(ghyllDir, "engine.db")
	bootStore, err := engine.OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	rec1 := runner.AttestationRecord{
		ID:             "att-tier1-1",
		Kind:           runner.AttestationKindDepthType,
		ArrowID:        "A1",
		ClauseID:       "C1",
		OpID:           "alice",
		AttestedByRole: "operator",
		SourceRole:     "analyst",
		TargetRole:     "architect",
		Verdict:        runner.AttestationPass,
		Timestamp:      1716100000_000000000,
		GridVersion:    1,
	}
	rec2 := runner.AttestationRecord{
		ID:             "att-tier1-2",
		Kind:           runner.AttestationKindDepthType,
		ArrowID:        "A2",
		ClauseID:       "C1",
		OpID:           "alice",
		AttestedByRole: "operator",
		SourceRole:     "analyst",
		TargetRole:     "architect",
		Verdict:        runner.AttestationFail,
		Reason:         "needs review",
		Timestamp:      1716100100_000000000,
		GridVersion:    1,
	}
	// Write a Tier-1-style flat JSONL (no PassID) directly so we
	// bypass Record's Tier 2 PassID-required enforcement — the
	// whole point of this scenario is a pre-Tier-2 audit trail.
	flatPath := filepath.Join(ghyllDir, "attestations.jsonl")
	if err := writeLegacyJSONL(flatPath, rec1, rec2); err != nil {
		t.Fatal(err)
	}
	// Seed the engine table by loading the legacy JSONL via the
	// lenient replay path then CatchUp.
	seedStore := runner.NewAttestationStore()
	if _, _, err := seedStore.LoadFromJSONL(flatPath, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := bootStore.CatchUpAttestations(gocontext.Background(), seedStore); err != nil {
		t.Fatal(err)
	}
	_ = bootStore.Close()

	// Confirm the tree dir does NOT exist.
	treeRoot := filepath.Join(ghyllDir, "attestations")
	if _, err := os.Stat(treeRoot); !os.IsNotExist(err) {
		t.Fatalf("precondition: tree dir should not exist; got err=%v", err)
	}

	// Step 2: re-open the engine via openEngineWithOptions — the
	// path the production session takes.
	rt, err := openEngineWithOptions(workdir, nil, 3)
	if err != nil {
		t.Fatalf("openEngineWithOptions: %v", err)
	}
	defer rt.closeEngine()

	if !rt.tier1FallbackMigrated {
		t.Error("tier1FallbackMigrated false; expected true after JSONL fallback")
	}
	if got := rt.attestations.Len(); got != 2 {
		t.Errorf("in-memory store loaded %d records; want 2", got)
	}

	// Step 3: replay + attachJournal — rematerialization fires.
	if _, err := rt.replayEngine(gocontext.Background()); err != nil {
		t.Fatal(err)
	}
	if err := rt.attachJournal(nil); err != nil {
		t.Fatal(err)
	}

	// Step 4: tree dir must now contain JSONL files for each record.
	if _, err := os.Stat(treeRoot); err != nil {
		t.Fatalf("tree dir missing post-migration: %v", err)
	}
	var found int
	_ = filepath.Walk(treeRoot, func(_ string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() {
			found++
		}
		return nil
	})
	if found != 2 {
		t.Errorf("tree files = %d; want 2 (rec1 + rec2)", found)
	}
}
