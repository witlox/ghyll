package runner

import (
	"errors"
	"testing"
)

// TestScenario_AttestationRecord_AdversarySelfCertRejected
// verifies the Tier 2 (gate-1 F-3) extension to §12.2: when
// AdversaryRole is set, it must not equal SourceRole or
// TargetRole. The Record write path rejects with
// ErrAttestationSelfCert; the verifier flags the same case from
// JSONL.
func TestScenario_AttestationRecord_AdversarySelfCertRejected(t *testing.T) {
	cases := []struct {
		name string
		src  string
		tgt  string
		adv  string
	}{
		{name: "adversary equals source", src: "analyst", tgt: "architect", adv: "analyst"},
		{name: "adversary equals target", src: "analyst", tgt: "architect", adv: "architect"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewAttestationStore()
			err := store.Record(AttestationRecord{
				ID:             "att-self-cert-" + tc.name,
				Kind:           AttestationKindDepthType,
				ArrowID:        "A",
				ClauseID:       "C",
				OpID:           "alice",
				AttestedByRole: "operator",
				SourceRole:     tc.src,
				TargetRole:     tc.tgt,
				AdversaryRole:  tc.adv,
				Verdict:        AttestationPass,
				Timestamp:      1716100000_000000000,
				GridVersion:    1,
				PassID:         "P-1",
			})
			if !errors.Is(err, ErrAttestationSelfCert) {
				t.Errorf("Record err = %v; want ErrAttestationSelfCert", err)
			}
		})
	}
}

// TestScenario_EncodeAttestationPath_AdversaryRoleHonored ensures
// EncodeAttestationPath emits the 3-role chain form when
// AdversaryRole is non-empty. Mirrors the BDD scenario at the
// unit-test layer for fast feedback.
func TestScenario_EncodeAttestationPath_AdversaryRoleHonored(t *testing.T) {
	rec := AttestationRecord{
		PassID:         "P-1",
		AttestedByRole: "operator",
		SourceRole:     "analyst",
		TargetRole:     "architect",
		AdversaryRole:  "adversary",
		Context:        "ctxA",
		Stratum:        "L1",
		GridVersion:    3,
	}
	path, truncated, err := EncodeAttestationPath(rec)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if truncated {
		t.Errorf("3-role fixture should not truncate")
	}
	if !contains3Role(path) {
		t.Errorf("path %q lacks analyst__adversary__architect", path)
	}
}

func contains3Role(s string) bool {
	for i := 0; i+len("analyst__adversary__architect") <= len(s); i++ {
		if s[i:i+len("analyst__adversary__architect")] == "analyst__adversary__architect" {
			return true
		}
	}
	return false
}
