package bootstrap

import (
	"strings"
	"testing"

	"github.com/witlox/ghyll/runner"
)

// TestScenario_EmitInitAttestations_OneRecordPerArrow verifies
// gate-2 CORR-A-18: the production init AttestationRecord
// producer emits one record per grid arrow with AttestedByRole
// "init", satisfying the BDD init-path-encoding scenario in
// production (not just via a hand-built test fixture).
func TestScenario_EmitInitAttestations_OneRecordPerArrow(t *testing.T) {
	g := NewGrid("alice@example.com")
	g.GridVersion = 1
	g.Arrows = []map[string]any{
		{"upstream": "analyst", "downstream": "architect", "context": "checkout"},
		{"upstream": "architect", "downstream": "implementer", "context": "checkout"},
	}
	recs, err := EmitInitAttestations(g, "alice@example.com")
	if err != nil {
		t.Fatalf("EmitInitAttestations: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records; want 2", len(recs))
	}
	for _, rec := range recs {
		if rec.AttestedByRole != "init" {
			t.Errorf("AttestedByRole = %q; want init", rec.AttestedByRole)
		}
		if rec.Kind != runner.AttestationKindOnTheSpot {
			t.Errorf("Kind = %q; want on-the-spot", rec.Kind)
		}
		if rec.Verdict != runner.AttestationPass {
			t.Errorf("Verdict = %q; want pass", rec.Verdict)
		}
		if rec.PassID == "" {
			t.Errorf("PassID empty — would be rejected by Tier 2 Record")
		}
		if !strings.HasPrefix(rec.ID, "att-init-") {
			t.Errorf("ID = %q; want att-init- prefix", rec.ID)
		}
	}
}

// TestScenario_EmitInitAttestations_RejectsNilOrEmpty verifies
// the defensive guards.
func TestScenario_EmitInitAttestations_RejectsNilOrEmpty(t *testing.T) {
	if _, err := EmitInitAttestations(nil, "alice"); err == nil {
		t.Error("nil grid: expected error")
	}
	if _, err := EmitInitAttestations(NewGrid("a"), ""); err == nil {
		t.Error("empty opID: expected error")
	}
}

// TestScenario_EmitInitAttestations_RoundTrip_Record_Succeeds
// verifies the emitted records pass AttestationStore.Record's
// Tier 2 validation (PassID non-empty, Unit + payload consistent,
// HintJSON parseable). Without this, the production producer
// would emit records the store rejects.
func TestScenario_EmitInitAttestations_RoundTrip_Record_Succeeds(t *testing.T) {
	g := NewGrid("alice")
	g.GridVersion = 1
	g.Arrows = []map[string]any{
		{"upstream": "analyst", "downstream": "architect", "context": "checkout"},
	}
	recs, err := EmitInitAttestations(g, "alice")
	if err != nil {
		t.Fatal(err)
	}
	store := runner.NewAttestationStore()
	for _, rec := range recs {
		if err := store.Record(rec); err != nil {
			t.Errorf("Record(%s): %v", rec.ID, err)
		}
	}
	if store.Len() != 1 {
		t.Errorf("store.Len = %d; want 1", store.Len())
	}
}
