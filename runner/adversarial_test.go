package runner

import (
	"context"
	"errors"
	"testing"
)

// helper: tiny runner with one concept that returns operator-controlled
// pass/fail.
func passConceptRunner(t *testing.T, pass bool) *Runner {
	t.Helper()
	reg := NewRegistry()
	if err := reg.Register("test-clause", func(_ context.Context, _ Clause) (*Result, error) {
		return &Result{Pass: pass, Details: map[string]any{}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	return NewRunner(reg)
}

func TestAdversary_FalsifiesFailingClause(t *testing.T) {
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	r := passConceptRunner(t, false) // clause will fail
	a := NewAdversary(findings, classifications, r)
	report, err := a.Attack(context.Background(), AdversaryAttack{
		ArrowID: "A1",
		PassID:  "P1",
		DepthClauses: []Clause{
			{Concept: "test-clause", ClauseID: "C1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.ClauseFalsifications) != 1 {
		t.Fatalf("expected 1 falsification entry; got %d", len(report.ClauseFalsifications))
	}
	if !report.ClauseFalsifications[0].Falsified {
		t.Errorf("clause should be falsified (run returned fail); got %+v", report.ClauseFalsifications[0])
	}
	if report.ClauseFalsifications[0].FindingID == "" {
		t.Error("falsification should raise a finding")
	}
	// FindingsStore should have the raised finding.
	stored := findings.ForArrow("A1")
	if len(stored) != 1 || stored[0].Type != FindingTypeClauseFalsification {
		t.Errorf("findings = %+v", stored)
	}
}

func TestAdversary_PassingClauseRaisesNoFinding(t *testing.T) {
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	r := passConceptRunner(t, true)
	a := NewAdversary(findings, classifications, r)
	report, err := a.Attack(context.Background(), AdversaryAttack{
		ArrowID: "A1",
		PassID:  "P1",
		DepthClauses: []Clause{
			{Concept: "test-clause", ClauseID: "C1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.ClauseFalsifications[0].Falsified {
		t.Error("passing clause should not be falsified")
	}
	if len(findings.ForArrow("A1")) != 0 {
		t.Error("no findings expected for passing clause")
	}
}

func TestAdversary_OpenSweepHookRaisesFindings(t *testing.T) {
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	r := passConceptRunner(t, true)
	a := NewAdversary(findings, classifications, r)
	a.OpenSweep = func(_ context.Context, _ AdversaryAttack) ([]FindingRecord, error) {
		return []FindingRecord{
			{Type: FindingTypeOpenSweep, Severity: SeverityHigh, Description: "stale config check"},
		}, nil
	}
	report, err := a.Attack(context.Background(), AdversaryAttack{
		ArrowID: "A1", PassID: "P1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.OpenSweepFindings) != 1 {
		t.Errorf("open-sweep findings = %d; want 1", len(report.OpenSweepFindings))
	}
}

func TestAdversary_DepthClassifyHookRaisesBelowMin(t *testing.T) {
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	r := passConceptRunner(t, true)
	a := NewAdversary(findings, classifications, r)
	a.Classify = func(_ context.Context, _ AdversaryAttack) ([]Classification, error) {
		return []Classification{
			{RequirementID: "R1", Observed: DepthRankMocked, Evidence: "uses postgres mock"},
		}, nil
	}
	report, err := a.Attack(context.Background(), AdversaryAttack{
		ArrowID: "A1", PassID: "P1",
		Requirements: []Requirement{
			{ID: "R1", MinDepth: DepthRankRealistic, Description: "checkout integration"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.DepthBelowMinFindings) != 1 {
		t.Fatalf("depth-below-min findings = %d; want 1", len(report.DepthBelowMinFindings))
	}
	stored := findings.ForArrow("A1")
	if stored[0].Type != FindingTypeDepthBelowMin {
		t.Errorf("finding type = %v; want depth-below-min", stored[0].Type)
	}
	// Classifications recorded in the store.
	recorded := classifications.ClassificationsForArrow("A1")
	if len(recorded) != 1 || recorded[0].Observed != DepthRankMocked {
		t.Errorf("classifications = %+v", recorded)
	}
}

func TestAdversary_DepthClassifyMeetingMinRaisesNoFinding(t *testing.T) {
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	r := passConceptRunner(t, true)
	a := NewAdversary(findings, classifications, r)
	a.Classify = func(_ context.Context, _ AdversaryAttack) ([]Classification, error) {
		return []Classification{
			{RequirementID: "R1", Observed: DepthRankRealistic, Evidence: "live pg"},
		}, nil
	}
	report, _ := a.Attack(context.Background(), AdversaryAttack{
		ArrowID: "A1", PassID: "P1",
		Requirements: []Requirement{{ID: "R1", MinDepth: DepthRankMocked}},
	})
	if len(report.DepthBelowMinFindings) != 0 {
		t.Errorf("at-or-above min should not raise; got %d", len(report.DepthBelowMinFindings))
	}
}

func TestAdversary_RequirementsDeclaredIdempotentlyAcrossRounds(t *testing.T) {
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	r := passConceptRunner(t, true)
	a := NewAdversary(findings, classifications, r)
	attack := AdversaryAttack{
		ArrowID: "A1", PassID: "P1",
		Requirements: []Requirement{{ID: "R1", MinDepth: DepthRankShallow}},
	}
	report1, err := a.Attack(context.Background(), attack)
	if err != nil {
		t.Fatal(err)
	}
	if len(report1.HarnessErrors) != 0 {
		t.Errorf("round 0 harness errors: %v", report1.HarnessErrors)
	}
	attack.Round = 1
	report2, err := a.Attack(context.Background(), attack)
	if err != nil {
		t.Fatal(err)
	}
	if len(report2.HarnessErrors) != 0 {
		t.Errorf("round 1 should not error on dup requirement: %v", report2.HarnessErrors)
	}
}

func TestAdversary_RequiresFindingsStore(t *testing.T) {
	classifications := NewClassificationsStore()
	r := passConceptRunner(t, true)
	a := NewAdversary(nil, classifications, r)
	_, err := a.Attack(context.Background(), AdversaryAttack{ArrowID: "A", PassID: "P"})
	if err == nil {
		t.Error("nil FindingsStore should error")
	}
}

func TestAdversary_RequiresArrowIDAndPassID(t *testing.T) {
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	r := passConceptRunner(t, true)
	a := NewAdversary(findings, classifications, r)
	_, err := a.Attack(context.Background(), AdversaryAttack{ArrowID: "", PassID: "P"})
	if err == nil {
		t.Error("empty ArrowID should error")
	}
	_, err = a.Attack(context.Background(), AdversaryAttack{ArrowID: "A", PassID: ""})
	if err == nil {
		t.Error("empty PassID should error")
	}
}

func TestAdversary_HookErrorsSurfaceAsHarnessErrors(t *testing.T) {
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	r := passConceptRunner(t, true)
	a := NewAdversary(findings, classifications, r)
	a.OpenSweep = func(_ context.Context, _ AdversaryAttack) ([]FindingRecord, error) {
		return nil, errors.New("sweep blew up")
	}
	a.Classify = func(_ context.Context, _ AdversaryAttack) ([]Classification, error) {
		return nil, errors.New("classifier blew up")
	}
	report, err := a.Attack(context.Background(), AdversaryAttack{ArrowID: "A", PassID: "P"})
	if err != nil {
		t.Fatal(err) // hook errors must not abort the attack
	}
	if len(report.HarnessErrors) < 2 {
		t.Errorf("expected hook errors surfaced; got %v", report.HarnessErrors)
	}
}

func TestAdversary_AttackReport_AnyOpen(t *testing.T) {
	r := AttackReport{
		ClauseFalsifications: []ClauseFalsificationResult{{Falsified: true}},
	}
	if !r.AnyOpen() {
		t.Error("falsified should make AnyOpen true")
	}
	r2 := AttackReport{}
	if r2.AnyOpen() {
		t.Error("empty report should have AnyOpen false")
	}
}
