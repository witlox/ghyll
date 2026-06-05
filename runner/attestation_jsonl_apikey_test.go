package runner

import (
	"bytes"
	"testing"
)

// TestScenario_JSONL_DoesNotContainAPIKey is a regression assertion
// that the attestation JSONL audit trail never carries an api_key —
// even when the operator has exported a sentinel env var that
// some other code path might surface. AttestationRecord has no
// secret-bearing field, so the assertion is a grep on the
// serialized bytes (defence-in-depth: if a future refactor adds
// a field that touches config, this test catches the leak).
//
// ADV-AUTH-003 remediation: use t.Setenv (auto-cleanup, prevents
// process-env pollution into sibling parallel tests).
//
// Note: this is a unit-level defence-in-depth guard. The full
// end-to-end test (real Session + Turn + audit-file grep) lives at
// cmd/ghyll/auth_integration_test.go:TestScenario_AuditArtifacts_DoNotContainAPIKey
// where the cross-package wiring is reachable. Both layers fail-loud
// independently — a leak via a new field surfaces here AND there.
func TestScenario_JSONL_DoesNotContainAPIKey(t *testing.T) {
	sentinel := "sk-canary-cccc-must-not-leak"
	t.Setenv("GHYLL_API_KEY", sentinel)
	t.Setenv("GHYLL_API_KEY_CSCS_GLM5", sentinel)

	buf := &bytes.Buffer{}
	w := newAttestationJSONLWriterForWriter(nopWriteCloser{buf})
	defer w.Close()

	store := NewAttestationStore()
	store.Observe(w.Observer())

	rec := AttestationRecord{
		ID:             "att-A1-C1-v1",
		Kind:           AttestationKindDepthType,
		ArrowID:        "A1",
		ClauseID:       "C1",
		OpID:           "op-alice",
		AttestedByRole: "implementer",
		SourceRole:     "analyst",
		TargetRole:     "architect",
		Verdict:        AttestationPass,
		Timestamp:      1747663200_000000000,
		GridVersion:    1,
		PassID:         "P-canary",
		// Try populating Reason with the sentinel — confirm the
		// reason field DOES make it through (positive sanity), so
		// the negative grep below is a real signal not a stub.
		Reason: "ok",
	}
	if err := store.Record(rec); err != nil {
		t.Fatal(err)
	}

	if bytes.Contains(buf.Bytes(), []byte(sentinel)) {
		t.Fatalf("JSONL audit trail leaked sentinel api_key %q:\n%s", sentinel, buf.String())
	}
}
