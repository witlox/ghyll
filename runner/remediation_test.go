package runner

import (
	"context"
	"errors"
	"testing"
)

// fixToResolved is a FixAttemptFn that transitions every open finding
// to resolved — simulates a producer that always succeeds.
func fixToResolved(store *FindingsStore) FixAttemptFn {
	return func(_ context.Context, open []FindingRecord) (bool, error) {
		for _, f := range open {
			if err := store.Transition(f.ID, FindingStatusResolved); err != nil {
				return false, err
			}
		}
		return true, nil
	}
}

func TestRemediation_ConvergesOnSecondRound(t *testing.T) {
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	r := passConceptRunner(t, false) // clause fails on round 0
	a := NewAdversary(findings, classifications, r)

	// Round 0 falsifies; producer fixes; subsequent rounds need to
	// see a passing clause. Swap the registry binding between rounds.
	passingReg := NewRegistry()
	_ = passingReg.Register("test-clause", func(_ context.Context, _ Clause) (*Result, error) {
		return &Result{Pass: true}, nil
	})
	swapAfterFirstRound := func(round int) AdversaryAttack {
		if round == 1 {
			a.Runner = NewRunner(passingReg)
		}
		return AdversaryAttack{
			ArrowID: "A1", PassID: "P1",
			DepthClauses: []Clause{{Concept: "test-clause", ClauseID: "C1"}},
		}
	}
	out, err := a.RunRemediationLoop(context.Background(), RemediationConfig{
		FixAttempt:    fixToResolved(findings),
		AttackBuilder: swapAfterFirstRound,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome != RemediationConverged {
		t.Errorf("outcome = %v; want converged", out.Outcome)
	}
	if out.RoundsExecuted != 2 {
		t.Errorf("rounds = %d; want 2 (round 0 fails, round 1 passes)", out.RoundsExecuted)
	}
}

func TestRemediation_EscalatesAtRoundsMax(t *testing.T) {
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	r := passConceptRunner(t, false) // always fails
	a := NewAdversary(findings, classifications, r)
	out, err := a.RunRemediationLoop(context.Background(), RemediationConfig{
		RoundsMax:  3,
		FixAttempt: fixToResolved(findings),
		AttackBuilder: func(_ int) AdversaryAttack {
			return AdversaryAttack{
				ArrowID: "A1", PassID: "P1",
				DepthClauses: []Clause{{Concept: "test-clause", ClauseID: "C1"}},
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome != RemediationEscalatedRounds {
		t.Errorf("outcome = %v; want escalated-rounds-max", out.Outcome)
	}
	if out.RoundsExecuted != 3 {
		t.Errorf("rounds = %d; want 3", out.RoundsExecuted)
	}
}

func TestRemediation_EscalatesOnNoProgress(t *testing.T) {
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	r := passConceptRunner(t, false)
	a := NewAdversary(findings, classifications, r)
	out, err := a.RunRemediationLoop(context.Background(), RemediationConfig{
		FixAttempt: func(_ context.Context, _ []FindingRecord) (bool, error) {
			return false, nil // producer gives up
		},
		AttackBuilder: func(_ int) AdversaryAttack {
			return AdversaryAttack{
				ArrowID: "A1", PassID: "P1",
				DepthClauses: []Clause{{Concept: "test-clause", ClauseID: "C1"}},
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome != RemediationEscalatedNoProgress {
		t.Errorf("outcome = %v; want escalated-no-progress", out.Outcome)
	}
	if out.RoundsExecuted != 1 {
		t.Errorf("should escalate after first failed round; got %d", out.RoundsExecuted)
	}
}

func TestRemediation_EscalatesWhenNoFixCallback(t *testing.T) {
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	r := passConceptRunner(t, false)
	a := NewAdversary(findings, classifications, r)
	out, err := a.RunRemediationLoop(context.Background(), RemediationConfig{
		// FixAttempt unset
		AttackBuilder: func(_ int) AdversaryAttack {
			return AdversaryAttack{
				ArrowID: "A1", PassID: "P1",
				DepthClauses: []Clause{{Concept: "test-clause", ClauseID: "C1"}},
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome != RemediationEscalatedNoProgress {
		t.Errorf("nil FixAttempt should escalate; got %v", out.Outcome)
	}
}

func TestRemediation_ZeroFindingsConvergesOnRoundOne(t *testing.T) {
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	r := passConceptRunner(t, true) // passes immediately
	a := NewAdversary(findings, classifications, r)
	out, _ := a.RunRemediationLoop(context.Background(), RemediationConfig{
		FixAttempt: fixToResolved(findings),
		AttackBuilder: func(_ int) AdversaryAttack {
			return AdversaryAttack{
				ArrowID: "A1", PassID: "P1",
				DepthClauses: []Clause{{Concept: "test-clause", ClauseID: "C1"}},
			}
		},
	})
	if out.Outcome != RemediationConverged {
		t.Errorf("outcome = %v; want converged", out.Outcome)
	}
	if out.RoundsExecuted != 1 {
		t.Errorf("zero findings should converge on round 0 (1 round executed); got %d", out.RoundsExecuted)
	}
}

func TestRemediation_RequiresAttackBuilder(t *testing.T) {
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	r := passConceptRunner(t, true)
	a := NewAdversary(findings, classifications, r)
	_, err := a.RunRemediationLoop(context.Background(), RemediationConfig{})
	if err == nil {
		t.Error("nil AttackBuilder should error")
	}
}

func TestRemediation_ContextCancellation(t *testing.T) {
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	r := passConceptRunner(t, false)
	a := NewAdversary(findings, classifications, r)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	out, err := a.RunRemediationLoop(ctx, RemediationConfig{
		FixAttempt: fixToResolved(findings),
		AttackBuilder: func(_ int) AdversaryAttack {
			return AdversaryAttack{ArrowID: "A1", PassID: "P1"}
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled; got %v", err)
	}
	if out.Outcome != RemediationContextCancelled {
		t.Errorf("outcome = %v; want context-cancelled", out.Outcome)
	}
}

func TestVerificationAutoInsert_AddsBothClauses(t *testing.T) {
	existing := []Clause{
		{Concept: "lint-clean", ClauseID: "C1"},
	}
	out := VerificationAutoInsert("A1", existing)
	if len(out) != 3 {
		t.Fatalf("len = %d; want 3", len(out))
	}
	gotConcepts := []string{out[1].Concept, out[2].Concept}
	wantConcepts := map[string]bool{
		"no-open-finding":                   true,
		"every-requirement-meets-min-depth": true,
	}
	for _, c := range gotConcepts {
		if !wantConcepts[c] {
			t.Errorf("unexpected concept %q in auto-insert", c)
		}
	}
}

func TestVerificationAutoInsert_PreservesOperatorOverride(t *testing.T) {
	// If the operator already declared no-open-finding with custom
	// args, the auto-insert SHOULD NOT duplicate it.
	existing := []Clause{
		{Concept: "no-open-finding", ClauseID: "C1", Args: map[string]any{"severity-threshold": "high"}},
	}
	out := VerificationAutoInsert("A1", existing)
	count := 0
	for _, c := range out {
		if c.Concept == "no-open-finding" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("no-open-finding count = %d; want 1 (operator override preserved)", count)
	}
}

func TestVerificationAutoInsert_DoesNotMutateInput(t *testing.T) {
	existing := []Clause{{Concept: "lint-clean"}}
	_ = VerificationAutoInsert("A1", existing)
	if len(existing) != 1 {
		t.Errorf("input mutated; len = %d", len(existing))
	}
}
