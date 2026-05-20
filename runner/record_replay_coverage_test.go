package runner

import "testing"

// Tier 3 coverage push — exercise recordReplay's duplicate-ID
// resolution path (CORR-A-22: timestamp-based last-write-wins).

func TestScenario_recordReplay_DuplicateLaterWins(t *testing.T) {
	s := NewAttestationStore()
	earlier := AttestationRecord{
		ID: "att-dup", Kind: AttestationKindDepthType,
		ArrowID: "A", ClauseID: "C", OpID: "alice",
		AttestedByRole: "operator", SourceRole: "analyst", TargetRole: "architect",
		Verdict: AttestationInsufficientBasis, Timestamp: 1, GridVersion: 1,
		PassID: "P-1",
	}
	if err := s.recordReplay(earlier); err != nil {
		t.Fatal(err)
	}
	later := earlier
	later.Verdict = AttestationPass
	later.Timestamp = 100
	if err := s.recordReplay(later); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Lookup("att-dup")
	if !ok {
		t.Fatal("Lookup missing")
	}
	if got.Verdict != AttestationPass {
		t.Errorf("verdict = %q; want pass (later-wins)", got.Verdict)
	}
}

func TestScenario_recordReplay_DuplicateEarlierIgnored(t *testing.T) {
	s := NewAttestationStore()
	later := AttestationRecord{
		ID: "att-dup", Kind: AttestationKindDepthType,
		ArrowID: "A", ClauseID: "C", OpID: "alice",
		AttestedByRole: "operator", SourceRole: "analyst", TargetRole: "architect",
		Verdict: AttestationPass, Timestamp: 100, GridVersion: 1,
		PassID: "P-1",
	}
	if err := s.recordReplay(later); err != nil {
		t.Fatal(err)
	}
	earlier := later
	earlier.Verdict = AttestationFail
	earlier.Timestamp = 50
	if err := s.recordReplay(earlier); err != nil {
		t.Errorf("earlier replay errored: %v; want silent nil", err)
	}
	got, _ := s.Lookup("att-dup")
	if got.Verdict != AttestationPass {
		t.Errorf("verdict = %q; want pass (later still wins)", got.Verdict)
	}
}

func TestScenario_recordReplay_IdempotentSameContent(t *testing.T) {
	s := NewAttestationStore()
	rec := AttestationRecord{
		ID: "att-idem", Kind: AttestationKindDepthType,
		ArrowID: "A", ClauseID: "C", OpID: "alice",
		AttestedByRole: "operator", SourceRole: "analyst", TargetRole: "architect",
		Verdict: AttestationPass, Timestamp: 1, GridVersion: 1, PassID: "P-1",
	}
	if err := s.recordReplay(rec); err != nil {
		t.Fatal(err)
	}
	if err := s.recordReplay(rec); err != nil {
		t.Errorf("idempotent re-replay errored: %v", err)
	}
}
