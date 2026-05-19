package main

import (
	"context"
	"strings"
	"testing"

	"github.com/witlox/ghyll/runner"
)

// newOperatorTestSession builds a session with the engine runtime
// fully wired so operator commands can read live state. Uses a
// real engineRuntime + a stub Session minimally populated.
func newOperatorTestSession(t *testing.T) *Session {
	t.Helper()
	rt, _ := newTier0Runtime(t)
	if _, err := rt.replayEngine(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := rt.attachJournal(nil); err != nil {
		t.Fatal(err)
	}
	// Append a real arrow so /attest can look it up.
	if _, err := rt.Grid().Append(runner.ArrowDefinition{
		ID:         "A1",
		SourceRole: "analyst",
		TargetRole: "architect",
		Stratum:    "L1",
		Context:    "checkout",
		Clauses:    []runner.Clause{{Concept: "no-todo-marker", ClauseID: "C1"}},
	}); err != nil {
		t.Fatal(err)
	}
	return &Session{
		engine:  rt,
		workdir: t.TempDir(),
		output:  func(string) {},
	}
}

func TestScenario_OperatorCmd_OpID_SetAndShow(t *testing.T) {
	s := newOperatorTestSession(t)

	r := s.DispatchSlashCommand("/op-id")
	if !r.Handled || !strings.Contains(r.Output, "no op-id set") {
		t.Fatalf("/op-id empty: %+v", r)
	}

	r = s.DispatchSlashCommand("/op-id alice@example.com")
	if !r.Handled || !strings.Contains(r.Output, "op-id set: alice@example.com") {
		t.Fatalf("/op-id set: %+v", r)
	}
	if s.opID != "alice@example.com" {
		t.Fatalf("opID = %q; want alice@example.com", s.opID)
	}

	r = s.DispatchSlashCommand("/op-id")
	if !strings.Contains(r.Output, "alice@example.com") {
		t.Fatalf("/op-id show: %+v", r)
	}

	r = s.DispatchSlashCommand("/op-id clear")
	if s.opID != "" {
		t.Fatal("clear should empty opID")
	}
	_ = r
}

func TestScenario_OperatorCmd_OpID_RejectsWhitespace(t *testing.T) {
	s := newOperatorTestSession(t)
	r := s.DispatchSlashCommand("/op-id alice bob")
	if !strings.Contains(r.Output, "must not contain whitespace") {
		t.Fatalf("expected whitespace rejection; got %+v", r)
	}
}

func TestScenario_OperatorCmd_Attest_RequiresOpID(t *testing.T) {
	s := newOperatorTestSession(t)
	r := s.DispatchSlashCommand("/attest att-A1-C1-v1 pass")
	if !strings.Contains(r.Output, "/op-id required") {
		t.Fatalf("expected op-id requirement; got %+v", r)
	}
}

func TestScenario_OperatorCmd_Attest_PassVerdict(t *testing.T) {
	s := newOperatorTestSession(t)
	s.DispatchSlashCommand("/op-id alice")
	r := s.DispatchSlashCommand("/attest att-A1-C1-v1 pass verified locally")
	if !strings.Contains(r.Output, "attestation att-A1-C1-v1 recorded") {
		t.Fatalf("attest pass: %+v", r)
	}
	// Verify the record landed.
	rec, ok := s.engine.AttestationStore().Lookup("att-A1-C1-v1")
	if !ok {
		t.Fatal("attestation not in store")
	}
	if rec.Verdict != runner.AttestationPass {
		t.Errorf("verdict = %q; want pass", rec.Verdict)
	}
	if rec.OpID != "alice" {
		t.Errorf("OpID = %q; want alice", rec.OpID)
	}
	if rec.AttestedByRole != "operator" {
		t.Errorf("AttestedByRole = %q; want operator (synthetic)", rec.AttestedByRole)
	}
	if rec.Reason != "verified locally" {
		t.Errorf("Reason = %q; want 'verified locally'", rec.Reason)
	}
}

func TestScenario_OperatorCmd_Attest_RejectsBadVerdict(t *testing.T) {
	s := newOperatorTestSession(t)
	s.DispatchSlashCommand("/op-id alice")
	r := s.DispatchSlashCommand("/attest att-A1-C1-v1 maybe")
	if !strings.Contains(r.Output, "not in {pass, fail, insufficient-basis}") {
		t.Fatalf("expected verdict error; got %+v", r)
	}
}

func TestScenario_OperatorCmd_Attest_RejectsUnknownArrow(t *testing.T) {
	s := newOperatorTestSession(t)
	s.DispatchSlashCommand("/op-id alice")
	r := s.DispatchSlashCommand("/attest att-A99-C1-v1 pass")
	if !strings.Contains(r.Output, "arrow") || !strings.Contains(r.Output, "not in grid") {
		t.Fatalf("expected unknown arrow; got %+v", r)
	}
}

func TestScenario_OperatorCmd_Attest_AliasesAccepted(t *testing.T) {
	s := newOperatorTestSession(t)
	s.DispatchSlashCommand("/op-id alice")
	for _, alias := range []string{"p", "ok", "PASS"} {
		// Use a distinct ref per alias to avoid duplicate-attestation conflict.
		ref := "att-A1-C1-v" + string(rune('1'+len(alias)))
		r := s.DispatchSlashCommand("/attest " + ref + " " + alias)
		if !strings.Contains(r.Output, "verdict=pass") {
			t.Errorf("alias %q: %+v", alias, r)
		}
	}
}

func TestScenario_OperatorCmd_Attest_UsageOnMissingArgs(t *testing.T) {
	s := newOperatorTestSession(t)
	s.DispatchSlashCommand("/op-id alice")
	r := s.DispatchSlashCommand("/attest")
	if !strings.Contains(r.Output, "usage:") {
		t.Fatalf("expected usage; got %+v", r)
	}
}

func TestScenario_OperatorCmd_Attestations_ListAll(t *testing.T) {
	s := newOperatorTestSession(t)
	s.DispatchSlashCommand("/op-id alice")
	s.DispatchSlashCommand("/attest att-A1-C1-v1 pass")

	r := s.DispatchSlashCommand("/attestations")
	if !strings.Contains(r.Output, "attestations (1)") {
		t.Fatalf("expected count line; got %+v", r)
	}
	if !strings.Contains(r.Output, "att-A1-C1-v1") {
		t.Fatalf("expected record id; got %+v", r)
	}
}

func TestScenario_OperatorCmd_Attestations_FilterByArrow(t *testing.T) {
	s := newOperatorTestSession(t)
	s.DispatchSlashCommand("/op-id alice")
	s.DispatchSlashCommand("/attest att-A1-C1-v1 pass")

	r := s.DispatchSlashCommand("/attestations A1")
	if !strings.Contains(r.Output, "att-A1-C1-v1") {
		t.Fatalf("filter A1: %+v", r)
	}
	r = s.DispatchSlashCommand("/attestations A-other")
	if !strings.Contains(r.Output, "no attestations") {
		t.Fatalf("filter A-other should be empty; got %+v", r)
	}
}

func TestScenario_OperatorCmd_Passes_EmptyAndPopulated(t *testing.T) {
	s := newOperatorTestSession(t)
	r := s.DispatchSlashCommand("/passes")
	if !strings.Contains(r.Output, "no open passes") {
		t.Fatalf("empty passes: %+v", r)
	}
	// Open a pass via the dispatcher's lock surface; bypass the
	// dispatcher itself (just exercise the registry).
	pass, _ := runner.OpenPass(runner.PassOptions{
		PassID: "P-1", Role: "analyst", Context: "checkout",
		ArrowID: "A1", LockTable: s.engine.RoleLocks(),
	})
	s.engine.Passes().Register(pass)
	defer pass.Close("done")

	r = s.DispatchSlashCommand("/passes")
	if !strings.Contains(r.Output, "open passes (1)") {
		t.Fatalf("populated passes: %+v", r)
	}
	if !strings.Contains(r.Output, "P-1") {
		t.Fatalf("missing pass id; got %+v", r)
	}
}

func TestScenario_OperatorCmd_ParseAttestationRef_DepthType(t *testing.T) {
	got, err := parseAttestationRef("att-A1-C1-v7")
	if err != nil {
		t.Fatal(err)
	}
	if got.kind != runner.AttestationKindDepthType {
		t.Errorf("kind = %q; want depth-type", got.kind)
	}
	if got.arrowID != "A1" || got.clauseID != "C1" || got.gridVersion != 7 {
		t.Errorf("parsed = %+v", got)
	}
}

func TestScenario_OperatorCmd_ParseAttestationRef_OnTheSpot(t *testing.T) {
	got, err := parseAttestationRef("att-A2-v3")
	if err != nil {
		t.Fatal(err)
	}
	if got.kind != runner.AttestationKindOnTheSpot {
		t.Errorf("kind = %q; want on-the-spot", got.kind)
	}
	if got.arrowID != "A2" || got.gridVersion != 3 || got.clauseID != "" {
		t.Errorf("parsed = %+v", got)
	}
}

func TestScenario_OperatorCmd_ParseAttestationRef_RejectsMalformed(t *testing.T) {
	cases := []string{"A1-C1-v7", "att-A1-C1", "att-A1-vXX"}
	for _, in := range cases {
		if _, err := parseAttestationRef(in); err == nil {
			t.Errorf("expected error on %q", in)
		}
	}
}
