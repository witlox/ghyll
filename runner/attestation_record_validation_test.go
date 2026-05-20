package runner

import (
	"errors"
	"strings"
	"testing"
)

// TestScenario_AttestationStore_Record_RejectsEmptyPassID verifies
// the Tier 2 enforcement landed at Record-time (gate-2 CORR-A-6).
func TestScenario_AttestationStore_Record_RejectsEmptyPassID(t *testing.T) {
	store := NewAttestationStore()
	err := store.Record(AttestationRecord{
		ID:             "att-no-passid",
		Kind:           AttestationKindDepthType,
		ArrowID:        "A1",
		ClauseID:       "C1",
		OpID:           "alice",
		AttestedByRole: "operator",
		SourceRole:     "analyst",
		TargetRole:     "architect",
		Verdict:        AttestationPass,
		Timestamp:      1,
		GridVersion:    1,
		// PassID intentionally empty
	})
	if !errors.Is(err, ErrAttestationPassIDEmpty) {
		t.Errorf("err = %v; want ErrAttestationPassIDEmpty", err)
	}
}

// TestScenario_AttestationStore_Record_RejectsInvalidUnit verifies
// gate-2 CORR-A-4 — unrecognized Unit at Record-time errors.
func TestScenario_AttestationStore_Record_RejectsInvalidUnit(t *testing.T) {
	store := NewAttestationStore()
	err := store.Record(AttestationRecord{
		ID: "att-bad-unit", Kind: AttestationKindDepthType,
		ArrowID: "A1", ClauseID: "C1", OpID: "alice",
		AttestedByRole: "operator",
		SourceRole:     "analyst", TargetRole: "architect",
		Verdict: AttestationPass, Timestamp: 1, GridVersion: 1,
		PassID: "P-1",
		Unit:   "bogus",
	})
	if !errors.Is(err, ErrVerdictUnitInvalid) {
		t.Errorf("err = %v; want ErrVerdictUnitInvalid", err)
	}
}

// TestScenario_AttestationStore_Record_EnforcesResidueCap covers
// gate-2 SEC-H-1 — the AttestationStore residue cap kicks in
// at Record-time, not just inside the modal driver.
func TestScenario_AttestationStore_Record_EnforcesResidueCap(t *testing.T) {
	store := NewAttestationStore()
	store.SetResidueNoteMaxBytes(64)
	err := store.Record(AttestationRecord{
		ID: "att-residue-cap", Kind: AttestationKindDepthType,
		ArrowID: "A1", ClauseID: "C1", OpID: "alice",
		AttestedByRole: "operator",
		SourceRole:     "analyst", TargetRole: "architect",
		Verdict: AttestationInsufficientBasis, Timestamp: 1, GridVersion: 1,
		PassID:      "P-1",
		Unit:        VerdictUnitWriteResidueNote,
		UnitPayload: VerdictUnitPayload{Residue: strings.Repeat("x", 65)},
	})
	if !errors.Is(err, ErrVerdictResidueTooLong) {
		t.Errorf("err = %v; want ErrVerdictResidueTooLong", err)
	}
}

// TestScenario_AttestationStore_Record_AdversaryRoleReservedSeparator
// verifies gate-2 SEC-H-1 — adversary_role containing "__"
// rejects (the reserved tree-path separator).
func TestScenario_AttestationStore_Record_AdversaryRoleReservedSeparator(t *testing.T) {
	store := NewAttestationStore()
	err := store.Record(AttestationRecord{
		ID: "att-adv-sep", Kind: AttestationKindDepthType,
		ArrowID: "A1", ClauseID: "C1", OpID: "alice",
		AttestedByRole: "operator",
		SourceRole:     "analyst", TargetRole: "architect",
		AdversaryRole: "weird__sep",
		Verdict:       AttestationPass, Timestamp: 1, GridVersion: 1,
		PassID: "P-1",
	})
	if !errors.Is(err, ErrAttestationSelfCert) {
		t.Errorf("err = %v; want ErrAttestationSelfCert (with __ rejection)", err)
	}
}

// TestScenario_AttestationStore_Record_MalformedHintJSON rejects
// records whose HintJSON is non-empty and non-"{}" but does not
// parse as a JSON object (gate-2 CORR-A-4 / F-25).
func TestScenario_AttestationStore_Record_MalformedHintJSON(t *testing.T) {
	store := NewAttestationStore()
	err := store.Record(AttestationRecord{
		ID: "att-bad-hint", Kind: AttestationKindDepthType,
		ArrowID: "A1", ClauseID: "C1", OpID: "alice",
		AttestedByRole: "operator",
		SourceRole:     "analyst", TargetRole: "architect",
		Verdict: AttestationPass, Timestamp: 1, GridVersion: 1,
		PassID:   "P-1",
		HintJSON: "{not-json",
	})
	if err == nil || !strings.Contains(err.Error(), "hint_json malformed") {
		t.Errorf("err = %v; want hint_json malformed", err)
	}
}

// TestScenario_AttestationStore_SetResidueNoteMaxBytes_RoundTrip
// verifies the atomic.Int64 accessor.
func TestScenario_AttestationStore_SetResidueNoteMaxBytes_RoundTrip(t *testing.T) {
	store := NewAttestationStore()
	if got := store.ResidueNoteMaxBytes(); got != 0 {
		t.Errorf("default = %d; want 0 (unset)", got)
	}
	store.SetResidueNoteMaxBytes(1024)
	if got := store.ResidueNoteMaxBytes(); got != 1024 {
		t.Errorf("after Set = %d; want 1024", got)
	}
}
