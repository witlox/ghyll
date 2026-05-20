package runner

import (
	"errors"
	"path/filepath"
	"testing"
)

// TestScenario_VerifyAggregateConsistencyVs_AuditLostWhenEnginePopulated
// verifies gate-2 SEC-H-3: when both flat + tree are empty AND
// engineRowCount > 0, the verifier returns ErrAttestationAuditLost.
// The legacy variant (no engine row count) silently reported
// "clean" in that scenario — an attacker pre-startup wipe could
// suppress audit evidence.
func TestScenario_VerifyAggregateConsistencyVs_AuditLostWhenEnginePopulated(t *testing.T) {
	v := &AttestationVerifier{}
	dir := t.TempDir()
	// Both paths "absent" (nonexistent files).
	flatPath := filepath.Join(dir, "absent.jsonl")
	treeRoot := filepath.Join(dir, "absent-tree")
	res, err := v.VerifyAggregateConsistencyVs(flatPath, treeRoot, 5)
	if !errors.Is(err, ErrAttestationAuditLost) {
		t.Errorf("err = %v; want ErrAttestationAuditLost", err)
	}
	if res.FlatLoaded != 0 || res.TreeLoaded != 0 {
		t.Errorf("counts non-zero: %+v", res)
	}
}

// TestScenario_VerifyAggregateConsistencyVs_EngineZero_StillClean
// verifies the engine-row-count guard does not false-positive when
// engineRowCount==0 (fresh project).
func TestScenario_VerifyAggregateConsistencyVs_EngineZero_StillClean(t *testing.T) {
	v := &AttestationVerifier{}
	dir := t.TempDir()
	res, err := v.VerifyAggregateConsistencyVs(
		filepath.Join(dir, "absent.jsonl"),
		filepath.Join(dir, "absent-tree"),
		0,
	)
	if err != nil {
		t.Errorf("err = %v; want nil (fresh project)", err)
	}
	if res.FlatLoaded+res.TreeLoaded != 0 {
		t.Errorf("counts non-zero: %+v", res)
	}
}
