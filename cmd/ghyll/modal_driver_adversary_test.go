package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/witlox/ghyll/cmd/ghyll/modal"
	"github.com/witlox/ghyll/runner"
)

// TestScenario_ModalDriver_PropagatesAdversaryRole_FromEventPayload
// verifies gate-2 CORR-A-5: when the dispatcher stamps
// adversary_role on the event payload, the modal driver propagates
// it to AttestationRecord.AdversaryRole.
func TestScenario_ModalDriver_PropagatesAdversaryRole_FromEventPayload(t *testing.T) {
	stub := &modal.StubModal{
		Verdicts: []modal.VerdictSubmission{
			{Verdict: runner.AttestationPass, Unit: runner.VerdictUnitConfirm},
		},
	}
	fx := newDriverFixture(t, stub)
	hint := runner.Hint{ArrowID: "A", ClauseID: "c-1", AttestationRef: "att-adv-prop"}
	hj, _ := json.Marshal(hint)
	fx.bus.Publish(runner.OperatorEvent{
		Kind:     runner.OpEventAttestationRequested,
		ArrowID:  "A",
		ClauseID: "c-1",
		PassID:   "p-1",
		Role:     "analyst",
		Detail:   string(hj),
		Payload: map[string]string{
			"source_role":    "analyst",
			"target_role":    "architect",
			"adversary_role": "adversary",
			"context":        "ctxA",
			"stratum":        "L1",
			"grid_version":   "7",
		},
	})
	if err := fx.driver.DrainPending(context.Background()); err != nil {
		t.Fatalf("DrainPending: %v", err)
	}
	rec, ok := fx.store.Lookup("att-adv-prop")
	if !ok {
		t.Fatal("Lookup failed")
	}
	if rec.AdversaryRole != "adversary" {
		t.Errorf("AdversaryRole = %q; want adversary", rec.AdversaryRole)
	}
	if rec.SourceRole != "analyst" || rec.TargetRole != "architect" {
		t.Errorf("roles = %q/%q; want analyst/architect", rec.SourceRole, rec.TargetRole)
	}
	if rec.Context != "ctxA" || rec.Stratum != "L1" {
		t.Errorf("context/stratum = %q/%q; want ctxA/L1", rec.Context, rec.Stratum)
	}
	if rec.GridVersion != 7 {
		t.Errorf("GridVersion = %d; want 7", rec.GridVersion)
	}
}

// TestScenario_ModalDriver_NoAdversaryRole_LeavesFieldEmpty verifies
// vanilla 2-role passes still produce 2-role records.
func TestScenario_ModalDriver_NoAdversaryRole_LeavesFieldEmpty(t *testing.T) {
	stub := &modal.StubModal{
		Verdicts: []modal.VerdictSubmission{
			{Verdict: runner.AttestationPass, Unit: runner.VerdictUnitConfirm},
		},
	}
	fx := newDriverFixture(t, stub)
	hint := runner.Hint{ArrowID: "A", ClauseID: "c-1", AttestationRef: "att-vanilla"}
	hj, _ := json.Marshal(hint)
	fx.bus.Publish(runner.OperatorEvent{
		Kind: runner.OpEventAttestationRequested, ArrowID: "A", ClauseID: "c-1", PassID: "p", Detail: string(hj),
		// No Payload → no adversary stamp.
	})
	_ = fx.driver.DrainPending(context.Background())
	rec, _ := fx.store.Lookup("att-vanilla")
	if rec.AdversaryRole != "" {
		t.Errorf("AdversaryRole = %q; want empty (vanilla pass)", rec.AdversaryRole)
	}
}
