package main

import (
	"strings"
	"testing"

	"github.com/witlox/ghyll/dialect"
)

// Tier 3 coverage push — small zero-coverage fns.

func TestScenario_AttestationPendingResponse_FormatsKeyFields(t *testing.T) {
	dec := dialect.RoutingDecision{
		Reason:      dialect.RoutingReason("gate-unsatisfiable"),
		Action:      dialect.ActionGateUnsatisfiable,
		TargetModel: "deep",
	}
	got := attestationPendingResponse(dec, "test-detail")
	for _, want := range []string{"attestation-pending", "gate-unsatisfiable", "test-detail"} {
		if !strings.Contains(got, want) {
			t.Errorf("response %q missing %q", got, want)
		}
	}
}

func TestScenario_HandlePassByIDCommand_NoEngine(t *testing.T) {
	s := &Session{output: func(string) {}}
	r := s.handlePassByIDCommand("P-1")
	if !strings.Contains(r.Output, "engine not initialized") {
		t.Errorf("output = %q; want engine-not-initialized", r.Output)
	}
}

func TestScenario_HandlePassByIDCommand_MissingID(t *testing.T) {
	s := newOperatorTestSession(t)
	r := s.handlePassByIDCommand("")
	if !strings.Contains(r.Output, "usage:") {
		t.Errorf("output = %q; want usage", r.Output)
	}
}

func TestScenario_HandlePassByIDCommand_NotFound(t *testing.T) {
	s := newOperatorTestSession(t)
	r := s.handlePassByIDCommand("P-nonexistent")
	if !strings.Contains(r.Output, "not-found") {
		t.Errorf("output = %q; want not-found", r.Output)
	}
}
