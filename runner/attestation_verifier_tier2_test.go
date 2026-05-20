package runner

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// --- per-line Tier 2 checks ------------------------------------

func TestScenario_AttestationVerifier_AdversaryRoleSelfCert(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string // substring of the expected issue.Reason
	}{
		{
			name: "adversary equals source",
			line: `{"attestation_id":"a1","kind":"depth-type","arrow_id":"A","clause_id":"C","op_id":"o","attested_by_role":"operator","source_role":"analyst","target_role":"architect","adversary_role":"analyst","verdict":"pass","timestamp":1,"grid_version":1}`,
			want: "adversary_role",
		},
		{
			name: "adversary equals target",
			line: `{"attestation_id":"a1","kind":"depth-type","arrow_id":"A","clause_id":"C","op_id":"o","attested_by_role":"operator","source_role":"analyst","target_role":"architect","adversary_role":"architect","verdict":"pass","timestamp":1,"grid_version":1}`,
			want: "adversary_role",
		},
		{
			name: "adversary contains __",
			line: `{"attestation_id":"a1","kind":"depth-type","arrow_id":"A","clause_id":"C","op_id":"o","attested_by_role":"operator","source_role":"analyst","target_role":"architect","adversary_role":"__weird","verdict":"pass","timestamp":1,"grid_version":1}`,
			want: `"__"`,
		},
	}
	v := &AttestationVerifier{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, _ := v.Verify(strings.NewReader(tc.line + "\n"))
			if res.Failed != 1 {
				t.Fatalf("Failed = %d; want 1", res.Failed)
			}
			found := false
			for _, iss := range res.Issues {
				if strings.Contains(iss.Reason, tc.want) {
					found = true
				}
			}
			if !found {
				t.Errorf("no issue mentions %q: %+v", tc.want, res.Issues)
			}
		})
	}
}

func TestScenario_AttestationVerifier_UnitPayloadValidated(t *testing.T) {
	v := &AttestationVerifier{}
	// confirm with non-empty payload → ErrVerdictUnitMissingField
	bad := `{"attestation_id":"a1","kind":"depth-type","arrow_id":"A","clause_id":"C","op_id":"o","attested_by_role":"operator","verdict":"pass","timestamp":1,"grid_version":1,"unit":"confirm","unit_payload_json":"{\"residue\":\"oops\"}"}`
	res, _ := v.Verify(strings.NewReader(bad + "\n"))
	if res.Failed != 1 {
		t.Fatalf("Failed = %d; want 1", res.Failed)
	}
	if !strings.Contains(res.Issues[0].Reason, "ValidateUnitPayload") {
		t.Errorf("issue does not mention ValidateUnitPayload: %q", res.Issues[0].Reason)
	}
}

func TestScenario_AttestationVerifier_UnitUnrecognized(t *testing.T) {
	v := &AttestationVerifier{}
	bad := `{"attestation_id":"a1","kind":"depth-type","arrow_id":"A","clause_id":"C","op_id":"o","attested_by_role":"operator","verdict":"pass","timestamp":1,"grid_version":1,"unit":"bogus"}`
	res, _ := v.Verify(strings.NewReader(bad + "\n"))
	if res.Failed != 1 {
		t.Fatalf("Failed = %d; want 1", res.Failed)
	}
	if !strings.Contains(res.Issues[0].Reason, "bogus") {
		t.Errorf("issue does not flag unknown unit: %q", res.Issues[0].Reason)
	}
}

func TestScenario_AttestationVerifier_HintJSONMalformed(t *testing.T) {
	v := &AttestationVerifier{}
	bad := `{"attestation_id":"a1","kind":"depth-type","arrow_id":"A","clause_id":"C","op_id":"o","attested_by_role":"operator","verdict":"pass","timestamp":1,"grid_version":1,"hint_json":"{not-json"}`
	res, _ := v.Verify(strings.NewReader(bad + "\n"))
	if res.Failed != 1 || !strings.Contains(res.Issues[0].Reason, "hint_json") {
		t.Errorf("expected hint_json malformed issue; got %+v", res.Issues)
	}
}

func TestScenario_AttestationVerifier_Tier2PassThrough_HappyPath(t *testing.T) {
	v := &AttestationVerifier{}
	good := `{"attestation_id":"a1","kind":"depth-type","arrow_id":"A","clause_id":"C","op_id":"o","attested_by_role":"operator","source_role":"analyst","target_role":"architect","verdict":"pass","timestamp":1,"grid_version":1,"pass_id":"p-1","context":"ctx-A","stratum":"S1","unit":"confirm","unit_payload_json":"{}","hint_json":"{\"arrow_id\":\"A\"}"}`
	res, _ := v.Verify(strings.NewReader(good + "\n"))
	if res.Failed != 0 {
		t.Errorf("Failed = %d; want 0 for clean tier-2 row: %+v", res.Failed, res.Issues)
	}
}

// --- VerifyAggregateConsistency --------------------------------

func TestScenario_VerifyAggregateConsistency_BothMissing_NoError(t *testing.T) {
	v := &AttestationVerifier{}
	dir := t.TempDir()
	res, err := v.VerifyAggregateConsistency(
		filepath.Join(dir, "nonexistent.jsonl"),
		filepath.Join(dir, "nonexistent-tree"),
	)
	if err != nil {
		t.Fatalf("err = %v; want nil for both-missing", err)
	}
	if res.FlatLoaded != 0 || res.TreeLoaded != 0 {
		t.Errorf("loaded counts non-zero: %+v", res)
	}
}

func TestScenario_VerifyAggregateConsistency_BothEmpty_OK(t *testing.T) {
	v := &AttestationVerifier{}
	dir := t.TempDir()
	// Empty flat file + empty tree dir → agreement (both have zero records).
	if _, err := v.VerifyAggregateConsistency("", dir); err != nil {
		t.Errorf("err = %v; want nil", err)
	}
}

func TestScenario_VerifyAggregateConsistency_OnlyInFlat_Diverges(t *testing.T) {
	v := &AttestationVerifier{}
	dir := t.TempDir()
	flatPath := filepath.Join(dir, "flat.jsonl")
	tree := filepath.Join(dir, "tree")
	// Write a single record to flat; leave tree empty.
	w, err := NewAttestationJSONLWriter(flatPath)
	if err != nil {
		t.Fatal(err)
	}
	writer := w.PrimaryWriter()
	if err := writer(AttestationRecord{
		ID:             "att-only-flat",
		Kind:           AttestationKindDepthType,
		ArrowID:        "A",
		ClauseID:       "C",
		OpID:           "o",
		AttestedByRole: "operator",
		Verdict:        AttestationPass,
		Timestamp:      1,
		GridVersion:    1,
	}); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	res, err := v.VerifyAggregateConsistency(flatPath, tree)
	if !errors.Is(err, ErrAttestationAggregateDivergence) {
		t.Fatalf("err = %v; want ErrAttestationAggregateDivergence", err)
	}
	if len(res.OnlyInFlat) != 1 || res.OnlyInFlat[0] != "att-only-flat" {
		t.Errorf("OnlyInFlat = %v; want [att-only-flat]", res.OnlyInFlat)
	}
}
