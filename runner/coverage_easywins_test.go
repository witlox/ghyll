package runner

import (
	"path/filepath"
	"testing"
)

// Tier 3 coverage push — small accessors + helpers.

func TestScenario_InsufficientBasisTracker_Max(t *testing.T) {
	tr := NewInsufficientBasisTracker(7, nil)
	if got := tr.Max(); got != 7 {
		t.Errorf("Max() = %d; want 7", got)
	}
}

func TestScenario_AttestationTreeWriter_RootAndLastError(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "attestations")
	tw, err := NewAttestationTreeWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tw.Close() }()
	if got := tw.Root(); got != root {
		t.Errorf("Root() = %q; want %q", got, root)
	}
	if err := tw.LastError(); err != nil {
		t.Errorf("LastError() on fresh writer = %v; want nil", err)
	}
}

func TestScenario_AttestationTreeWriter_Observer_ForwardsRecord(t *testing.T) {
	dir := t.TempDir()
	tw, err := NewAttestationTreeWriter(filepath.Join(dir, "att"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tw.Close() }()
	ob := tw.Observer()
	ob(AttestationEvent{
		Kind: AttestationEventRecord,
		Record: AttestationRecord{
			ID: "att-obs", Kind: AttestationKindDepthType,
			ArrowID: "A", ClauseID: "C", OpID: "alice", AttestedByRole: "operator",
			SourceRole: "analyst", TargetRole: "architect",
			Verdict: AttestationPass, Timestamp: 1, GridVersion: 1,
			PassID: "P-obs", Context: "ctxA", Stratum: "L1",
		},
	})
	// Non-record event is a no-op.
	ob(AttestationEvent{Kind: "other"})
	matches, _ := filepath.Glob(filepath.Join(dir, "att", "v1", "ctxA", "stratum-L1", "analyst__architect", "*.jsonl"))
	if len(matches) == 0 {
		t.Error("Observer didn't write the record")
	}
}
