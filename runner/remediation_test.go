package runner

import (
	"context"
	"errors"
	"strings"
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

func fixToAcceptedRisk(store *FindingsStore) FixAttemptFn {
	return func(_ context.Context, open []FindingRecord) (bool, error) {
		for _, f := range open {
			if err := store.Transition(f.ID, FindingStatusAcceptedRisk); err != nil {
				return false, err
			}
		}
		return true, nil
	}
}

func newFailingRunner(t *testing.T) *Runner {
	t.Helper()
	reg := NewRegistry()
	_ = reg.Register("test-clause", func(_ context.Context, _ Clause) (*Result, error) {
		return &Result{Pass: false}, nil
	})
	return NewRunner(reg, nil, DepthRankNone)
}

func newPassingRunner(t *testing.T) *Runner {
	t.Helper()
	reg := NewRegistry()
	_ = reg.Register("test-clause", func(_ context.Context, _ Clause) (*Result, error) {
		return &Result{Pass: true}, nil
	})
	return NewRunner(reg, nil, DepthRankNone)
}

func TestRemediation_ConvergesOnSecondRound(t *testing.T) {
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	failing := newFailingRunner(t)
	passing := newPassingRunner(t)

	out, err := RunRemediationLoop(context.Background(), RemediationConfig{
		FixAttempt: fixToResolved(findings),
		AdversaryBuilder: func(round int) *Adversary {
			r := failing
			if round >= 1 {
				r = passing
			}
			return NewAdversary(findings, classifications, r)
		},
		AttackBuilder: func(round int) AdversaryAttack {
			return AdversaryAttack{
				ArrowID: "A1", PassID: "P1", Round: round,
				DepthClauses: []Clause{{Concept: "test-clause", ClauseID: "C1"}},
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome != RemediationConverged {
		t.Errorf("outcome = %v; want converged", out.Outcome)
	}
	if out.RoundsExecuted != 2 {
		t.Errorf("rounds = %d; want 2", out.RoundsExecuted)
	}
}

func TestRemediation_EscalatesAtRoundsMax(t *testing.T) {
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	failing := newFailingRunner(t)
	out, err := RunRemediationLoop(context.Background(), RemediationConfig{
		RoundsMax:  3,
		FixAttempt: fixToResolved(findings),
		AdversaryBuilder: func(_ int) *Adversary {
			return NewAdversary(findings, classifications, failing)
		},
		AttackBuilder: func(round int) AdversaryAttack {
			return AdversaryAttack{
				ArrowID: "A1", PassID: "P1", Round: round,
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
	failing := newFailingRunner(t)
	out, err := RunRemediationLoop(context.Background(), RemediationConfig{
		FixAttempt: func(_ context.Context, _ []FindingRecord) (bool, error) {
			return false, nil
		},
		AdversaryBuilder: func(_ int) *Adversary {
			return NewAdversary(findings, classifications, failing)
		},
		AttackBuilder: func(round int) AdversaryAttack {
			return AdversaryAttack{
				ArrowID: "A1", PassID: "P1", Round: round,
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
}

func TestRemediation_EscalatesWhenNoFixCallback(t *testing.T) {
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	failing := newFailingRunner(t)
	out, err := RunRemediationLoop(context.Background(), RemediationConfig{
		AdversaryBuilder: func(_ int) *Adversary {
			return NewAdversary(findings, classifications, failing)
		},
		AttackBuilder: func(round int) AdversaryAttack {
			return AdversaryAttack{
				ArrowID: "A1", PassID: "P1", Round: round,
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

func TestRemediation_ZeroFindingsConvergesOnFirstRound(t *testing.T) {
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	passing := newPassingRunner(t)
	out, _ := RunRemediationLoop(context.Background(), RemediationConfig{
		FixAttempt: fixToResolved(findings),
		AdversaryBuilder: func(_ int) *Adversary {
			return NewAdversary(findings, classifications, passing)
		},
		AttackBuilder: func(round int) AdversaryAttack {
			return AdversaryAttack{
				ArrowID: "A1", PassID: "P1", Round: round,
				DepthClauses: []Clause{{Concept: "test-clause", ClauseID: "C1"}},
			}
		},
	})
	if out.Outcome != RemediationConverged {
		t.Errorf("outcome = %v; want converged", out.Outcome)
	}
	if out.RoundsExecuted != 1 {
		t.Errorf("zero findings should converge after 1 round; got %d", out.RoundsExecuted)
	}
}

func TestRemediation_RequiresAttackBuilder(t *testing.T) {
	_, err := RunRemediationLoop(context.Background(), RemediationConfig{
		AdversaryBuilder: func(_ int) *Adversary { return nil },
	})
	if err == nil {
		t.Error("nil AttackBuilder should error")
	}
}

func TestRemediation_RequiresAdversaryBuilder(t *testing.T) {
	_, err := RunRemediationLoop(context.Background(), RemediationConfig{
		AttackBuilder: func(_ int) AdversaryAttack { return AdversaryAttack{} },
	})
	if err == nil {
		t.Error("nil AdversaryBuilder should error")
	}
}

func TestRemediation_ContextCancellation(t *testing.T) {
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	failing := newFailingRunner(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := RunRemediationLoop(ctx, RemediationConfig{
		FixAttempt: fixToResolved(findings),
		AdversaryBuilder: func(_ int) *Adversary {
			return NewAdversary(findings, classifications, failing)
		},
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

func TestRemediation_AcceptedRiskCountsAsConverged(t *testing.T) {
	// F34 (test-coverage): producer transitioning to accepted-risk
	// converges on the next round.
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	failing := newFailingRunner(t)
	passing := newPassingRunner(t)
	out, _ := RunRemediationLoop(context.Background(), RemediationConfig{
		FixAttempt: fixToAcceptedRisk(findings),
		AdversaryBuilder: func(round int) *Adversary {
			r := failing
			if round >= 1 {
				r = passing
			}
			return NewAdversary(findings, classifications, r)
		},
		AttackBuilder: func(round int) AdversaryAttack {
			return AdversaryAttack{
				ArrowID: "A1", PassID: "P1", Round: round,
				DepthClauses: []Clause{{Concept: "test-clause", ClauseID: "C1"}},
			}
		},
	})
	if out.Outcome != RemediationConverged {
		t.Errorf("accepted-risk should count as converged; got %v", out.Outcome)
	}
}

func TestRemediation_HookErrorBudget(t *testing.T) {
	// F26: consecutive FixAttempt errors escalate before rounds-max.
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	failing := newFailingRunner(t)
	errCount := 0
	out, _ := RunRemediationLoop(context.Background(), RemediationConfig{
		MaxFixErrors: 2,
		FixAttempt: func(_ context.Context, _ []FindingRecord) (bool, error) {
			errCount++
			return true, errors.New("flaky LLM")
		},
		AdversaryBuilder: func(_ int) *Adversary {
			return NewAdversary(findings, classifications, failing)
		},
		AttackBuilder: func(round int) AdversaryAttack {
			return AdversaryAttack{
				ArrowID: "A1", PassID: "P1", Round: round,
				DepthClauses: []Clause{{Concept: "test-clause", ClauseID: "C1"}},
			}
		},
	})
	if out.Outcome != RemediationEscalatedHookError {
		t.Errorf("outcome = %v; want escalated-hook-error", out.Outcome)
	}
	if errCount != 2 {
		t.Errorf("errCount = %d; want 2 (MaxFixErrors=2)", errCount)
	}
}

func TestRemediation_SeverityThresholdAffectsConvergence(t *testing.T) {
	// F27: a low-severity finding below the threshold does NOT keep
	// the loop spinning.
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	// Manually raise a low-severity finding then run the loop with
	// SeverityThreshold=High; the loop should converge on round 0.
	_ = findings.Raise(FindingRecord{
		ID: "F-low", ArrowID: "A1", Type: FindingTypeOpenSweep,
		Severity: SeverityLow, Status: FindingStatusOpen,
	})
	passing := newPassingRunner(t)
	out, _ := RunRemediationLoop(context.Background(), RemediationConfig{
		SeverityThreshold: SeverityHigh,
		FixAttempt:        fixToResolved(findings),
		AdversaryBuilder: func(_ int) *Adversary {
			return NewAdversary(findings, classifications, passing)
		},
		AttackBuilder: func(round int) AdversaryAttack {
			return AdversaryAttack{ArrowID: "A1", PassID: "P1", Round: round}
		},
	})
	if out.Outcome != RemediationConverged {
		t.Errorf("low-severity below threshold should converge; got %v", out.Outcome)
	}
}

func TestRemediation_UnevaluatedSurfacesConvergedWithUnevaluated(t *testing.T) {
	// F9: an unevaluated finding leaves the loop in
	// converged-with-unevaluated rather than plain converged.
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	_ = findings.Raise(FindingRecord{
		ID: "F-unev", ArrowID: "A1", Type: FindingTypeClauseFalsification,
		Severity: SeverityInfo, Status: FindingStatusUnevaluated,
	})
	passing := newPassingRunner(t)
	out, _ := RunRemediationLoop(context.Background(), RemediationConfig{
		FixAttempt: fixToResolved(findings),
		AdversaryBuilder: func(_ int) *Adversary {
			return NewAdversary(findings, classifications, passing)
		},
		AttackBuilder: func(round int) AdversaryAttack {
			return AdversaryAttack{ArrowID: "A1", PassID: "P1", Round: round}
		},
	})
	if out.Outcome != RemediationConvergedWithUnevaluated {
		t.Errorf("outcome = %v; want converged-with-unevaluated", out.Outcome)
	}
}

func TestRemediation_HarnessErrorsCarryRoundPrefix(t *testing.T) {
	// F32: cross-round triage needs round provenance.
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	reg := NewRegistry()
	_ = reg.Register("erroring", func(_ context.Context, _ Clause) (*Result, error) {
		return nil, errors.New("evaluator failed to run")
	})
	r := NewRunner(reg, nil, DepthRankNone)
	out, _ := RunRemediationLoop(context.Background(), RemediationConfig{
		RoundsMax:  2,
		FixAttempt: fixToResolved(findings),
		AdversaryBuilder: func(_ int) *Adversary {
			return NewAdversary(findings, classifications, r)
		},
		AttackBuilder: func(round int) AdversaryAttack {
			return AdversaryAttack{
				ArrowID: "A1", PassID: "P1", Round: round,
				DepthClauses: []Clause{{Concept: "erroring", ClauseID: "C1"}},
			}
		},
	})
	foundRound0 := false
	for _, e := range out.HarnessErrors {
		if strings.HasPrefix(e, "round 0:") {
			foundRound0 = true
		}
	}
	if !foundRound0 {
		t.Errorf("HarnessErrors should be prefixed with round number; got %v", out.HarnessErrors)
	}
}

func TestRemediation_AttackRoundNotOverwritten(t *testing.T) {
	// F8: the loop trusts AttackBuilder's attack.Round value; it
	// does NOT overwrite. Test by setting an absurd Round in the
	// builder and asserting it survives.
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	passing := newPassingRunner(t)
	out, _ := RunRemediationLoop(context.Background(), RemediationConfig{
		FixAttempt: fixToResolved(findings),
		AdversaryBuilder: func(_ int) *Adversary {
			return NewAdversary(findings, classifications, passing)
		},
		AttackBuilder: func(_ int) AdversaryAttack {
			return AdversaryAttack{ArrowID: "A1", PassID: "P1", Round: 42}
		},
	})
	if len(out.Reports) < 1 {
		t.Fatal("expected at least one round report")
	}
	if out.Reports[0].Round != 42 {
		t.Errorf("attack.Round was overwritten; got %d, want 42", out.Reports[0].Round)
	}
}

func TestRemediation_FixAttemptSnapshotImmutable(t *testing.T) {
	// F28: mutating the snapshot has no effect on the store.
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	failing := newFailingRunner(t)
	out, _ := RunRemediationLoop(context.Background(), RemediationConfig{
		RoundsMax: 1,
		FixAttempt: func(_ context.Context, open []FindingRecord) (bool, error) {
			// Naive: mutate the snapshot instead of calling Transition.
			for i := range open {
				open[i].Status = FindingStatusResolved
			}
			// Return madeProgress=false so the loop escalates after 1 round
			// instead of looping forever.
			return false, nil
		},
		AdversaryBuilder: func(_ int) *Adversary {
			return NewAdversary(findings, classifications, failing)
		},
		AttackBuilder: func(round int) AdversaryAttack {
			return AdversaryAttack{
				ArrowID: "A1", PassID: "P1", Round: round,
				DepthClauses: []Clause{{Concept: "test-clause", ClauseID: "C1"}},
			}
		},
	})
	if out.Outcome != RemediationEscalatedNoProgress {
		t.Errorf("snapshot mutation has no effect; should escalate no-progress; got %v", out.Outcome)
	}
	// Store should still have the finding open.
	for _, f := range findings.ForArrow("A1") {
		if f.Status == FindingStatusResolved {
			t.Errorf("snapshot mutation corrupted store: finding %s is Resolved", f.ID)
		}
	}
}

func TestVerificationAutoInsert_AddsBothClauses(t *testing.T) {
	existing := []Clause{{Concept: "lint-clean", ClauseID: "C1"}}
	out := VerificationAutoInsert("A1", existing)
	if len(out) != 3 {
		t.Fatalf("len = %d; want 3", len(out))
	}
	// F42: assert presence, not position.
	concepts := map[string]bool{}
	for _, c := range out {
		concepts[c.Concept] = true
	}
	for _, want := range []string{"no-open-finding", "every-requirement-meets-min-depth"} {
		if !concepts[want] {
			t.Errorf("expected concept %q in auto-insert; got %v", want, concepts)
		}
	}
}

func TestVerificationAutoInsert_PreservesOperatorOverride(t *testing.T) {
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
		t.Errorf("no-open-finding count = %d; want 1", count)
	}
}

func TestVerificationAutoInsert_CaseInsensitiveDedup(t *testing.T) {
	// F31: dedup must be case-insensitive.
	existing := []Clause{
		{Concept: "No-Open-Finding", ClauseID: "C1"},
	}
	out := VerificationAutoInsert("A1", existing)
	noopCount := 0
	for _, c := range out {
		if strings.EqualFold(c.Concept, "no-open-finding") {
			noopCount++
		}
	}
	if noopCount != 1 {
		t.Errorf("case-insensitive dedup failed; count = %d", noopCount)
	}
}

func TestVerificationAutoInsert_EmptyArrowIDReturnsInputUnchanged(t *testing.T) {
	// F30: empty arrowID is operator misconfiguration.
	existing := []Clause{{Concept: "lint-clean"}}
	out := VerificationAutoInsert("", existing)
	if len(out) != len(existing) {
		t.Errorf("empty arrowID should return input unchanged; got %d clauses", len(out))
	}
}

func TestVerificationAutoInsert_SynthesizesClauseID(t *testing.T) {
	// F29: inserted clauses must carry non-empty ClauseIDs so
	// Runner.Evaluate accepts them.
	out := VerificationAutoInsert("A1", nil)
	for _, c := range out {
		if c.ClauseID == "" {
			t.Errorf("auto-inserted clause %q has empty ClauseID", c.Concept)
		}
	}
}

func TestVerificationAutoInsert_DoesNotMutateInput(t *testing.T) {
	existing := []Clause{{Concept: "lint-clean"}}
	_ = VerificationAutoInsert("A1", existing)
	if len(existing) != 1 {
		t.Errorf("input mutated; len = %d", len(existing))
	}
}
