package runner

import (
	"context"
	"errors"
	"testing"
)

// orchestratorFixture wires up the dependencies for orchestrator
// tests. The factory pattern produces a fresh Adversary per round.
type orchestratorFixture struct {
	findings        *FindingsStore
	classifications *ClassificationsStore
	bus             *OperatorBus
	registry        *Registry
	runner          *Runner
	openSweep       OpenSweepFn
	classify        DepthClassifyFn
}

func newOrchestratorFixture(t *testing.T) *orchestratorFixture {
	t.Helper()
	reg := NewRegistry()
	RegisterBuiltins(reg)
	return &orchestratorFixture{
		findings:        NewFindingsStore(),
		classifications: NewClassificationsStore(),
		bus:             NewOperatorBus(),
		registry:        reg,
		runner:          NewRunner(reg).WithActualTier(DepthRankRealistic),
		openSweep:       noopOpenSweep,
		classify:        noopClassify,
	}
}

// factory returns an AdversaryFactory that wires a fresh Adversary
// using the fixture's shared stores and hooks.
func (f *orchestratorFixture) factory() AdversaryFactory {
	return func(round int) *Adversary {
		a := NewAdversary(f.findings, f.classifications, f.runner)
		a.ApplyDefaults()
		a.OpenSweep = f.openSweep
		a.Classify = f.classify
		return a
	}
}

func TestScenario_Orchestrator_NoFindings_ConvergesFirstRound(t *testing.T) {
	f := newOrchestratorFixture(t)
	orch := &AdversarialOrchestrator{
		Factory:           f.factory(),
		Findings:          f.findings,
		Bus:               f.bus,
		MaxRounds:         3,
		SeverityThreshold: SeverityMedium,
	}
	attack := AdversaryAttack{
		ArrowID:      "A1",
		PassID:       "P1",
		DepthClauses: []Clause{{Concept: "no-todo-marker", ClauseID: "C1"}},
	}
	res, err := orch.Run(context.Background(), attack)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != OutcomeRemediationConverged {
		t.Fatalf("Outcome = %q; want converged", res.Outcome)
	}
	if res.RoundsRun != 1 {
		t.Fatalf("RoundsRun = %d; want 1", res.RoundsRun)
	}
}

func TestScenario_Orchestrator_ProducerRemediatesBetweenRounds_Converges(t *testing.T) {
	f := newOrchestratorFixture(t)
	called := 0
	f.openSweep = func(_ context.Context, _ AdversaryAttack) ([]FindingRecord, error) {
		called++
		if called == 1 {
			return []FindingRecord{{
				ID: "F1", ArrowID: "A1", Type: FindingTypeLocalBug,
				Severity: SeverityHigh, Status: FindingStatusOpen,
			}}, nil
		}
		return nil, nil
	}
	orch := &AdversarialOrchestrator{
		Factory:           f.factory(),
		Findings:          f.findings,
		Bus:               f.bus,
		MaxRounds:         3,
		SeverityThreshold: SeverityMedium,
		ProducerRemediate: func(_ context.Context, open []FindingRecord) error {
			for _, fr := range open {
				_ = f.findings.Transition(fr.ID, FindingStatusRunning)
				_ = f.findings.TransitionWithReason(fr.ID, FindingStatusAcceptedRisk, "operator", "documented")
			}
			return nil
		},
	}
	attack := AdversaryAttack{
		ArrowID:      "A1",
		PassID:       "P1",
		DepthClauses: []Clause{{Concept: "no-todo-marker", ClauseID: "C1"}},
	}
	res, err := orch.Run(context.Background(), attack)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != OutcomeRemediationConverged {
		t.Fatalf("Outcome = %q; want converged", res.Outcome)
	}
}

func TestScenario_Orchestrator_MaxRoundsExceeded_Escalates(t *testing.T) {
	f := newOrchestratorFixture(t)
	round := 0
	f.openSweep = func(_ context.Context, _ AdversaryAttack) ([]FindingRecord, error) {
		round++
		return []FindingRecord{{
			ID: fmtID("F", round), ArrowID: "A1", Type: FindingTypeLocalBug,
			Severity: SeverityHigh, Status: FindingStatusOpen,
		}}, nil
	}
	orch := &AdversarialOrchestrator{
		Factory:           f.factory(),
		Findings:          f.findings,
		Bus:               f.bus,
		MaxRounds:         2,
		SeverityThreshold: SeverityMedium,
		ProducerRemediate: nil,
	}
	attack := AdversaryAttack{
		ArrowID:      "A1",
		PassID:       "P1",
		DepthClauses: []Clause{{Concept: "no-todo-marker", ClauseID: "C1"}},
	}
	res, err := orch.Run(context.Background(), attack)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != OutcomeRemediationEscalated {
		t.Fatalf("Outcome = %q; want escalated", res.Outcome)
	}
	if res.RoundsRun != 2 {
		t.Fatalf("RoundsRun = %d; want 2", res.RoundsRun)
	}
	if len(res.FinalOpen) == 0 {
		t.Fatal("FinalOpen should report unresolved findings on escalation")
	}
}

func TestScenario_Orchestrator_PublishesLifecycleEvents(t *testing.T) {
	f := newOrchestratorFixture(t)
	var events []OperatorEventKind
	f.bus.Subscribe(func(e OperatorEvent) { events = append(events, e.Kind) })

	orch := &AdversarialOrchestrator{
		Factory:           f.factory(),
		Findings:          f.findings,
		Bus:               f.bus,
		MaxRounds:         2,
		SeverityThreshold: SeverityMedium,
	}
	attack := AdversaryAttack{
		ArrowID:      "A1",
		PassID:       "P1",
		DepthClauses: []Clause{{Concept: "no-todo-marker", ClauseID: "C1"}},
	}
	_, err := orch.Run(context.Background(), attack)
	if err != nil {
		t.Fatal(err)
	}
	saw := func(k OperatorEventKind) bool {
		for _, e := range events {
			if e == k {
				return true
			}
		}
		return false
	}
	if !saw(OpEventAdversarialRoundStart) {
		t.Errorf("missing adversarial-round-start event; got %v", events)
	}
	if !saw(OpEventRemediationConverged) {
		t.Errorf("missing remediation-converged event; got %v", events)
	}
}

func TestScenario_Orchestrator_NilFactoryErrors(t *testing.T) {
	orch := &AdversarialOrchestrator{Findings: NewFindingsStore()}
	_, err := orch.Run(context.Background(), AdversaryAttack{ArrowID: "A1"})
	if !errors.Is(err, ErrOrchestratorNoFactory) {
		t.Fatalf("got %v; want ErrOrchestratorNoFactory", err)
	}
}

func TestScenario_Orchestrator_NilFindingsErrors(t *testing.T) {
	orch := &AdversarialOrchestrator{Factory: func(int) *Adversary { return nil }}
	_, err := orch.Run(context.Background(), AdversaryAttack{ArrowID: "A1"})
	if !errors.Is(err, ErrOrchestratorNoFindings) {
		t.Fatalf("got %v; want ErrOrchestratorNoFindings", err)
	}
}

func TestScenario_Orchestrator_ProducerErrorAborts(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.openSweep = func(_ context.Context, _ AdversaryAttack) ([]FindingRecord, error) {
		return []FindingRecord{{
			ID: "F1", ArrowID: "A1", Type: FindingTypeLocalBug,
			Severity: SeverityHigh, Status: FindingStatusOpen,
		}}, nil
	}
	orch := &AdversarialOrchestrator{
		Factory:           f.factory(),
		Findings:          f.findings,
		Bus:               f.bus,
		MaxRounds:         3,
		SeverityThreshold: SeverityMedium,
		ProducerRemediate: func(_ context.Context, _ []FindingRecord) error {
			return errors.New("producer harness blew up")
		},
	}
	attack := AdversaryAttack{
		ArrowID:      "A1",
		PassID:       "P1",
		DepthClauses: []Clause{{Concept: "no-todo-marker", ClauseID: "C1"}},
	}
	res, err := orch.Run(context.Background(), attack)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != OutcomeProducerError {
		t.Fatalf("Outcome = %q; want producer-error", res.Outcome)
	}
}

func TestScenario_Orchestrator_ContextCancelExitsCleanly(t *testing.T) {
	f := newOrchestratorFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	orch := &AdversarialOrchestrator{
		Factory:           f.factory(),
		Findings:          f.findings,
		MaxRounds:         3,
		SeverityThreshold: SeverityMedium,
	}
	res, err := orch.Run(ctx, AdversaryAttack{
		ArrowID: "A1", PassID: "P1",
		DepthClauses: []Clause{{Concept: "no-todo-marker", ClauseID: "C1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != OutcomeCanceled {
		t.Fatalf("Outcome = %q; want canceled", res.Outcome)
	}
}

// fmtID generates a deterministic finding ID for test scenarios.
func fmtID(prefix string, n int) string {
	if n < 10 {
		return prefix + string(rune('0'+n))
	}
	return prefix + string(rune('A'+(n-10)))
}
