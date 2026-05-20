package main

import (
	"testing"

	"github.com/witlox/ghyll/runner"
)

// TestScenario_SynthesizeAttestUnitPayload_PerVerdict verifies the
// /attest CLI picks the right Unit + payload shape for each
// verdict (gate-2 CORR-A-11). Without this, /attest persisted
// records with Unit="" and bypassed Tier 2 unit-payload validation.
func TestScenario_SynthesizeAttestUnitPayload_PerVerdict(t *testing.T) {
	cases := []struct {
		name        string
		verdict     runner.AttestationVerdict
		reason      string
		wantUnit    runner.VerdictUnit
		wantInspec  []string
		wantResidue string
	}{
		{
			name:     "pass → confirm",
			verdict:  runner.AttestationPass,
			reason:   "",
			wantUnit: runner.VerdictUnitConfirm,
		},
		{
			name:       "fail → record-locations with reason as singleton",
			verdict:    runner.AttestationFail,
			reason:     "feature.go:42-50",
			wantUnit:   runner.VerdictUnitRecordLocationsInspected,
			wantInspec: []string{"feature.go:42-50"},
		},
		{
			name:        "insufficient-basis → residue note",
			verdict:     runner.AttestationInsufficientBasis,
			reason:      "feature too large",
			wantUnit:    runner.VerdictUnitWriteResidueNote,
			wantResidue: "feature too large",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unit, payload := synthesizeAttestUnitPayload(tc.verdict, tc.reason)
			if unit != tc.wantUnit {
				t.Errorf("unit = %q; want %q", unit, tc.wantUnit)
			}
			if tc.wantResidue != "" && payload.Residue != tc.wantResidue {
				t.Errorf("residue = %q; want %q", payload.Residue, tc.wantResidue)
			}
			if len(tc.wantInspec) > 0 {
				if len(payload.Inspected) != len(tc.wantInspec) {
					t.Errorf("inspected = %v; want %v", payload.Inspected, tc.wantInspec)
				}
			}
		})
	}
}

// TestScenario_SynthesizeAttestUnitPayload_FailEmptyReason verifies
// that fail+empty reason produces an Inspected=[] which
// ValidateUnitPayload then rejects — the /attest user sees the
// error and must re-issue with a reason. Better UX than silent
// persistence with no evidence.
func TestScenario_SynthesizeAttestUnitPayload_FailEmptyReason(t *testing.T) {
	_, payload := synthesizeAttestUnitPayload(runner.AttestationFail, "")
	if len(payload.Inspected) != 0 {
		t.Errorf("Inspected = %v; want empty (will be rejected by ValidateUnitPayload downstream)", payload.Inspected)
	}
}
