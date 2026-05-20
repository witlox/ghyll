package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScenario_TreeWriter_PathTruncation_AnnotatesReason verifies
// gate-2 CORR-A-19: when EncodeAttestationPath hash-substitutes a
// segment, the PrimaryWriter appends "path-truncated" to rec.Reason
// so the persisted record signals the truncation. Without this the
// JSONL line carried no in-band marker that the path was hashed.
func TestScenario_TreeWriter_PathTruncation_AnnotatesReason(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "attestations")
	tw, err := NewAttestationTreeWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tw.Close() }()

	// SourceRole long enough to trigger maxPathComponentBytes
	// substitution.
	rec := AttestationRecord{
		ID:             "att-trunc",
		Kind:           AttestationKindDepthType,
		ArrowID:        "A",
		ClauseID:       "C",
		OpID:           "alice",
		AttestedByRole: "operator",
		SourceRole:     strings.Repeat("a", 300),
		TargetRole:     "architect",
		Verdict:        AttestationPass,
		Timestamp:      1,
		GridVersion:    1,
		PassID:         "P-trunc",
		Context:        "ctxA",
		Stratum:        "L1",
	}
	if err := tw.PrimaryWriter()(rec); err != nil {
		t.Fatalf("PrimaryWriter: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "v1", "ctxA", "stratum-L1", "h-*", "P-trunc.jsonl"))
	if len(matches) == 0 {
		t.Fatal("no JSONL file emitted under truncated path")
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "path-truncated") {
		t.Errorf("JSONL line lacks path-truncated annotation: %s", string(data))
	}
}

// TestScenario_TreeWriter_NoTruncation_NoAnnotation verifies the
// vanilla path doesn't get spuriously annotated.
func TestScenario_TreeWriter_NoTruncation_NoAnnotation(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "attestations")
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
		Reason: "verified",
	}
	if err := tw.PrimaryWriter()(rec); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "v1", "ctxA", "stratum-L1", "analyst__architect", "P-clean.jsonl"))
	if len(matches) == 0 {
		t.Fatal("no JSONL emitted")
	}
	data, _ := os.ReadFile(matches[0])
	if strings.Contains(string(data), "path-truncated") {
		t.Errorf("JSONL spuriously annotated path-truncated: %s", string(data))
	}
}
