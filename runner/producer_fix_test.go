package runner

import (
	"context"
	"errors"
	"testing"
)

func TestScenario_ProducerFix_HappyPath_ProgressEachRound(t *testing.T) {
	called := 0
	h := &ProducerFixHarness{
		ArrowID: "A1",
		Producer: func(_ context.Context, _ []FindingRecord, round int) ([]byte, error) {
			called++
			// Distinct artifact per round — no loop bomb.
			return []byte{byte(round)}, nil
		},
	}
	remediate := h.ProducerRemediate()
	for i := 1; i <= 3; i++ {
		if err := remediate(context.Background(), nil); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
	}
	if called != 3 || h.Round() != 3 {
		t.Fatalf("called=%d round=%d; want 3,3", called, h.Round())
	}
}

func TestScenario_ProducerFix_LoopBombDetected(t *testing.T) {
	h := &ProducerFixHarness{
		ArrowID: "A1",
		Producer: func(_ context.Context, _ []FindingRecord, _ int) ([]byte, error) {
			return []byte("same response every round"), nil
		},
	}
	remediate := h.ProducerRemediate()
	openFindings := []FindingRecord{
		{ID: "F1", ArrowID: "A1", Status: FindingStatusOpen, Severity: SeverityHigh},
	}
	// Round 1 establishes the baseline; no error.
	if err := remediate(context.Background(), openFindings); err != nil {
		t.Fatalf("round 1: %v", err)
	}
	// Round 2: same artifact, findings still open → loop bomb.
	err := remediate(context.Background(), openFindings)
	if !errors.Is(err, ErrProducerLoopBomb) {
		t.Fatalf("round 2: got %v; want ErrProducerLoopBomb", err)
	}
}

func TestScenario_ProducerFix_SameArtifactWithNoOpenFindingsIsOK(t *testing.T) {
	// If the producer's artifact is unchanged but the findings
	// list is empty, that's not a loop bomb — it's the
	// "everything was fine the first round" case. The orchestrator
	// converges separately.
	h := &ProducerFixHarness{
		ArrowID: "A1",
		Producer: func(_ context.Context, _ []FindingRecord, _ int) ([]byte, error) {
			return []byte("steady"), nil
		},
	}
	remediate := h.ProducerRemediate()
	if err := remediate(context.Background(), nil); err != nil {
		t.Fatalf("round 1: %v", err)
	}
	if err := remediate(context.Background(), nil); err != nil {
		t.Fatalf("round 2 with no open findings should not loop-bomb: %v", err)
	}
}

func TestScenario_ProducerFix_PublishesFixSignal(t *testing.T) {
	bus := NewOperatorBus()
	var got []OperatorEvent
	bus.Subscribe(func(e OperatorEvent) { got = append(got, e) })

	h := &ProducerFixHarness{
		ArrowID: "A1",
		Bus:     bus,
		Producer: func(_ context.Context, _ []FindingRecord, _ int) ([]byte, error) {
			return []byte{1, 2, 3}, nil
		},
	}
	if err := h.ProducerRemediate()(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	saw := false
	for _, e := range got {
		if e.Kind == OpEventProducerFixSignal {
			saw = true
		}
	}
	if !saw {
		t.Fatal("expected producer-fix-signal event")
	}
}

func TestScenario_ProducerFix_NilProducerErrors(t *testing.T) {
	h := &ProducerFixHarness{}
	err := h.ProducerRemediate()(context.Background(), nil)
	if err == nil || !errors.Is(err, errors.New("producer-fix: nil Producer")) && err.Error() != "producer-fix: nil Producer" {
		// errors.New comparison via Is needs sentinel; fall back to
		// string match for this non-sentinel error.
		if err == nil {
			t.Fatal("expected error on nil Producer")
		}
	}
}

func TestScenario_ProducerFix_NilHarnessErrors(t *testing.T) {
	var h *ProducerFixHarness
	err := h.runOneRound(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error on nil harness")
	}
}

func TestScenario_ProducerFix_PropagatesProducerError(t *testing.T) {
	boom := errors.New("producer harness exploded")
	h := &ProducerFixHarness{
		ArrowID: "A1",
		Producer: func(_ context.Context, _ []FindingRecord, _ int) ([]byte, error) {
			return nil, boom
		},
	}
	err := h.ProducerRemediate()(context.Background(), nil)
	if !errors.Is(err, boom) {
		t.Fatalf("got %v; want %v", err, boom)
	}
}

func TestScenario_ProducerFix_IntegratesWithOrchestrator(t *testing.T) {
	f := newOrchestratorFixture(t)
	// Open sweep raises one finding round 1, none thereafter.
	calls := 0
	f.openSweep = func(_ context.Context, _ AdversaryAttack) ([]FindingRecord, error) {
		calls++
		if calls == 1 {
			return []FindingRecord{{
				ID: "F1", ArrowID: "A1", Type: FindingTypeLocalBug,
				Severity: SeverityHigh, Status: FindingStatusOpen,
			}}, nil
		}
		return nil, nil
	}
	// Producer remediates by transitioning the finding; artifact
	// is unique per call so no loop bomb.
	producerCalls := 0
	harness := &ProducerFixHarness{
		ArrowID: "A1",
		Producer: func(_ context.Context, open []FindingRecord, round int) ([]byte, error) {
			producerCalls++
			for _, fr := range open {
				_ = f.findings.Transition(fr.ID, FindingStatusRunning)
				_ = f.findings.TransitionWithReason(fr.ID, FindingStatusAcceptedRisk, "operator", "documented")
			}
			return []byte{byte(round)}, nil
		},
	}
	orch := &AdversarialOrchestrator{
		Factory:           f.factory(),
		Findings:          f.findings,
		Bus:               f.bus,
		MaxRounds:         3,
		SeverityThreshold: SeverityMedium,
		ProducerRemediate: harness.ProducerRemediate(),
	}
	res, err := orch.Run(context.Background(), AdversaryAttack{
		ArrowID: "A1", PassID: "P1",
		DepthClauses: []Clause{{Concept: "no-todo-marker", ClauseID: "C1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != OutcomeRemediationConverged {
		t.Fatalf("outcome = %q; want converged", res.Outcome)
	}
	if producerCalls < 1 {
		t.Errorf("producer never called: %d", producerCalls)
	}
}
