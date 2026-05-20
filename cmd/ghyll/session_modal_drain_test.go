package main

import (
	gocontext "context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/witlox/ghyll/cmd/ghyll/modal"
	"github.com/witlox/ghyll/runner"
)

// TestScenario_Session_DrainModalPending_NoEngine_NoOps verifies
// DrainModalPending is a no-op on a bare session (no engine, no
// driver). Used by REPL tests that DisableEngine.
func TestScenario_Session_DrainModalPending_NoEngine_NoOps(t *testing.T) {
	s := &Session{output: func(string) {}}
	// Must not panic, must not block.
	s.DrainModalPending(gocontext.Background())
}

// TestScenario_Session_DrainModalPending_DispatchesQueuedVerdict
// verifies the REPL drain path: a published verdict event makes
// it through DrainModalPending → StubModal → AttestationStore.
func TestScenario_Session_DrainModalPending_DispatchesQueuedVerdict(t *testing.T) {
	s := newOperatorTestSession(t)
	s.opID = "alice"
	stub := &modal.StubModal{
		Verdicts: []modal.VerdictSubmission{
			{Verdict: runner.AttestationPass, Unit: runner.VerdictUnitConfirm},
		},
	}
	s.modalPrompt = stub
	s.modalDriver = newModalDriver(
		stub,
		s.engine.AttestationStore(),
		s.engine.Passes(),
		s.engine.Bus(),
		s.engine.InsufficientBasisTracker(),
		func() string { return s.opID },
		s.buildArrowResolver(s.engine),
		0,
	)
	hint := runner.Hint{ArrowID: "A1", ClauseID: "C1", AttestationRef: "att-drain-1"}
	hj, _ := json.Marshal(hint)
	s.engine.Bus().Publish(runner.OperatorEvent{
		Kind:     runner.OpEventAttestationRequested,
		ArrowID:  "A1",
		ClauseID: "C1",
		PassID:   "P-drain-1",
		Detail:   string(hj),
	})
	if got := s.modalDriver.PendingLen(); got != 1 {
		t.Fatalf("PendingLen after publish = %d; want 1", got)
	}
	s.DrainModalPending(gocontext.Background())
	if got := s.modalDriver.PendingLen(); got != 0 {
		t.Errorf("PendingLen after drain = %d; want 0", got)
	}
	rec, ok := s.engine.AttestationStore().Lookup("att-drain-1")
	if !ok {
		t.Fatal("attestation not recorded")
	}
	if rec.Verdict != runner.AttestationPass {
		t.Errorf("verdict = %q; want pass", rec.Verdict)
	}
	if rec.SourceRole != "analyst" || rec.TargetRole != "architect" {
		t.Errorf("roles = %q/%q; want analyst/architect (from arrow A1)", rec.SourceRole, rec.TargetRole)
	}
}

// TestScenario_Session_DrainModalPending_SurfacesNonCancelError
// verifies a non-cancel error from DrainPending is surfaced via
// s.output (not silently swallowed). Uses a tiny pendingMaxLen +
// republishing prompt to force ErrModalDrainCapExceeded.
func TestScenario_Session_DrainModalPending_SurfacesNonCancelError(t *testing.T) {
	s := newOperatorTestSession(t)
	s.opID = "alice"
	var diagOutput []string
	s.output = func(msg string) { diagOutput = append(diagOutput, msg) }

	// republishingPrompt drops a fresh attestation-requested event
	// on every PresentVerdict call → drain never finishes →
	// ErrModalDrainCapExceeded.
	prompt := &republishingPrompt{bus: s.engine.Bus(), counter: new(int32)}
	s.modalDriver = newModalDriver(
		prompt,
		s.engine.AttestationStore(),
		s.engine.Passes(),
		s.engine.Bus(),
		s.engine.InsufficientBasisTracker(),
		func() string { return s.opID },
		s.buildArrowResolver(s.engine),
		0,
	)
	hint := runner.Hint{ArrowID: "A1", ClauseID: "C1", AttestationRef: "att-runaway"}
	hj, _ := json.Marshal(hint)
	s.engine.Bus().Publish(runner.OperatorEvent{
		Kind:     runner.OpEventAttestationRequested,
		ArrowID:  "A1",
		ClauseID: "C1",
		PassID:   "p",
		Detail:   string(hj),
	})
	s.DrainModalPending(gocontext.Background())
	found := false
	for _, m := range diagOutput {
		if strings.Contains(m, "modal drain") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected diagnostic about modal drain in output; got %v", diagOutput)
	}
}
