// Tests for the dispatcher's diamond-v4 wiring: Hooks field,
// AdversarialPhase invocation, audit-floor pre-check, recursion
// budget, and ArrowStatusAbortedRemediation early-return.

package runner

import (
	"context"
	"errors"
	"testing"
)

// TestScenario_RemediationConverged_PredicateMatchesSpec verifies
// the converged-vocabulary used by Dispatch's early-return decision.
func TestScenario_RemediationConverged_PredicateMatchesSpec(t *testing.T) {
	t.Parallel()
	cases := []struct {
		o    RemediationOutcome
		want bool
	}{
		{RemediationConverged, true},
		{RemediationConvergedWithUnevaluated, true},
		{RemediationEscalatedRounds, false},
		{RemediationEscalatedNoProgress, false},
		{RemediationEscalatedHookError, false},
		{RemediationContextCancelled, false},
	}
	for _, tc := range cases {
		if got := remediationConverged(tc.o); got != tc.want {
			t.Errorf("remediationConverged(%q) = %v; want %v", tc.o, got, tc.want)
		}
	}
}

// TestScenario_Dispatcher_DepthSensitive_AdversaryHooksUnwired_Refuses
// verifies the production refusal path: a depth-sensitive arrow
// dispatched without a hook bundle returns ErrAdversaryHooksNotWired
// BEFORE the pass counter spins.
func TestScenario_Dispatcher_DepthSensitive_AdversaryHooksUnwired_Refuses(t *testing.T) {
	t.Parallel()
	f := newDispatcherFixture(t)
	d := f.dispatcher()
	// Wire BOTH the empty hooks-pointer AND a non-nil AdversarialPhase
	// to trigger the pre-flight check.
	var hooks AtomicAdversarialHooks
	d.Hooks = &hooks
	d.AdversarialPhase = func(_ context.Context, _ *DispatchRequest, _ string, _ []Clause) (*RemediationReport, []Clause, error) {
		return nil, nil, nil
	}
	arrow := happyArrow()
	arrow.Clauses[0].DepthType = DepthTypeSensitive
	_, err := d.Dispatch(context.Background(), DispatchRequest{
		Role:       "analyst",
		Context:    "checkout",
		Arrow:      arrow,
		ActualTier: DepthRankShallow,
		ProjectDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected refusal, got nil")
	}
	if !errors.Is(err, ErrAdversaryHooksNotWired) {
		t.Fatalf("expected ErrAdversaryHooksNotWired, got %v", err)
	}
	// Counter MUST NOT have advanced (no pass row produced).
	if f.passes.Len() != 0 {
		t.Fatalf("refused dispatch must not spin counter: passes.Len=%d", f.passes.Len())
	}
}

// TestScenario_Dispatcher_AdversaryConverged_VerifiesOverRobust
// verifies the converged path: the dispatcher uses verifyClauses
// returned by the cycle helper for the post-cycle verification loop.
func TestScenario_Dispatcher_AdversaryConverged_VerifiesOverRobust(t *testing.T) {
	t.Parallel()
	f := newDispatcherFixture(t)
	d := f.dispatcher()
	var hooks AtomicAdversarialHooks
	bundle := &AdversarialHooks{
		Factory:     func(int) *Adversary { return NewAdversary(NewFindingsStore(), NewClassificationsStore(), f.runner) },
		OpenSweep:   func(context.Context, AdversaryAttack) ([]FindingRecord, error) { return nil, nil },
		Classify:    func(context.Context, AdversaryAttack) ([]Classification, error) { return nil, nil },
		ProducerFix: func(context.Context, []FindingRecord, int) ([]byte, error) { return []byte("x"), nil },
	}
	hooks.Store(bundle)
	d.Hooks = &hooks
	verifyCount := 0
	d.AdversarialPhase = func(_ context.Context, _ *DispatchRequest, _ string, _ []Clause) (*RemediationReport, []Clause, error) {
		verifyCount++
		// Return a converged report + one explicit verify clause.
		return &RemediationReport{Outcome: RemediationConverged},
			[]Clause{{Concept: "no-todo-marker", ClauseID: "verify-1",
				Args: map[string]any{"scope": "**", "markers": []any{"TODO"}}}},
			nil
	}
	arrow := happyArrow()
	arrow.Clauses[0].DepthType = DepthTypeSensitive
	res, err := d.Dispatch(context.Background(), DispatchRequest{
		Role:       "analyst",
		Context:    "checkout",
		Arrow:      arrow,
		ActualTier: DepthRankShallow,
		ProjectDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if verifyCount != 1 {
		t.Fatalf("AdversarialPhase invoked %d times; want 1", verifyCount)
	}
	if len(res.Runs) != 1 {
		t.Fatalf("expected 1 verify run, got %d", len(res.Runs))
	}
}

// TestScenario_Dispatcher_AdversaryNonConverged_AbortsWithRemediationStatus
// verifies R23: a non-converged outcome leads Dispatch to early-return
// with ArrowStatusAbortedRemediation (DeriveArrowStatus bypassed by design).
func TestScenario_Dispatcher_AdversaryNonConverged_AbortsWithRemediationStatus(t *testing.T) {
	t.Parallel()
	f := newDispatcherFixture(t)
	d := f.dispatcher()
	var hooks AtomicAdversarialHooks
	hooks.Store(&AdversarialHooks{
		Factory:     func(int) *Adversary { return &Adversary{} },
		OpenSweep:   func(context.Context, AdversaryAttack) ([]FindingRecord, error) { return nil, nil },
		Classify:    func(context.Context, AdversaryAttack) ([]Classification, error) { return nil, nil },
		ProducerFix: func(context.Context, []FindingRecord, int) ([]byte, error) { return nil, nil },
	})
	d.Hooks = &hooks
	d.AdversarialPhase = func(_ context.Context, _ *DispatchRequest, _ string, _ []Clause) (*RemediationReport, []Clause, error) {
		return &RemediationReport{Outcome: RemediationEscalatedRounds}, nil, nil
	}
	arrow := happyArrow()
	arrow.Clauses[0].DepthType = DepthTypeSensitive
	res, err := d.Dispatch(context.Background(), DispatchRequest{
		Role:       "analyst",
		Context:    "checkout",
		Arrow:      arrow,
		ActualTier: DepthRankShallow,
		ProjectDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if res.ArrowStatus != ArrowStatusAbortedRemediation {
		t.Fatalf("expected ArrowStatusAbortedRemediation, got %s", res.ArrowStatus)
	}
}
