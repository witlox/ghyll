package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScenario_LoadFromTree_DetectsPathMismatch verifies
// Tier 3 / gate-2 SEC-H-4 polish: a JSONL file dropped at a path
// that doesn't match its records' EncodeAttestationPath surfaces
// via store.PathMismatches().
func TestScenario_LoadFromTree_DetectsPathMismatch(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "attestations")
	// Place a JSONL file at the WRONG path. The record inside
	// encodes to v1/ctxA/stratum-L1/analyst__architect/P-1.jsonl
	// — we drop it at v1/MISLEADING/stratum-L1/analyst__architect/P-1.jsonl.
	wrongDir := filepath.Join(root, "v1", "MISLEADING", "stratum-L1", "analyst__architect")
	if err := os.MkdirAll(wrongDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := AttestationRecord{
		ID:             "att-mismatch",
		Kind:           AttestationKindDepthType,
		ArrowID:        "A",
		ClauseID:       "C",
		OpID:           "alice",
		AttestedByRole: "operator",
		SourceRole:     "analyst",
		TargetRole:     "architect",
		Verdict:        AttestationPass,
		Timestamp:      1,
		GridVersion:    1,
		PassID:         "P-1",
		Context:        "ctxA",
		Stratum:        "L1",
	}
	line, _ := jsonlMarshal(newJsonlRecord(rec))
	if err := os.WriteFile(filepath.Join(wrongDir, "P-1.jsonl"), line, 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewAttestationStore()
	loaded, _, err := store.LoadFromTree(root, false)
	if err != nil {
		t.Fatalf("LoadFromTree: %v", err)
	}
	if loaded != 1 {
		t.Errorf("loaded = %d; want 1", loaded)
	}
	mismatches := store.PathMismatches()
	if len(mismatches) != 1 {
		t.Fatalf("PathMismatches = %v; want 1 entry", mismatches)
	}
	if !strings.Contains(mismatches[0], "MISLEADING") {
		t.Errorf("mismatch entry doesn't name the wrong dir: %q", mismatches[0])
	}
}

// TestScenario_LoadFromTree_CleanPath_NoMismatch verifies the
// happy path doesn't false-positive.
func TestScenario_LoadFromTree_CleanPath_NoMismatch(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "attestations")
	// Write the record through the tree writer so it lands at
	// the correct path.
	tw, err := NewAttestationTreeWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tw.Close() }()
	rec := AttestationRecord{
		ID: "att-clean", Kind: AttestationKindDepthType,
		ArrowID: "A", ClauseID: "C", OpID: "alice", AttestedByRole: "operator",
		SourceRole: "analyst", TargetRole: "architect",
		Verdict: AttestationPass, Timestamp: 1, GridVersion: 1,
		PassID: "P-clean", Context: "ctxA", Stratum: "L1",
	}
	if err := tw.PrimaryWriter()(rec); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()

	store := NewAttestationStore()
	if _, _, err := store.LoadFromTree(root, false); err != nil {
		t.Fatal(err)
	}
	if mismatches := store.PathMismatches(); len(mismatches) != 0 {
		t.Errorf("unexpected mismatches: %v", mismatches)
	}
}
