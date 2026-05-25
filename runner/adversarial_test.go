package runner

import (
	"context"
	"errors"
	"fmt"
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
	return NewRunner(reg, nil, DepthRankNone)
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
		Requirements: []Requirement{{ID: "R1", MinDepth: DepthRankMocked, Description: "req R1"}},
	})
	if len(report.DepthBelowMinFindings) != 0 {
		t.Errorf("at-or-above min should not raise; got %d", len(report.DepthBelowMinFindings))
	}
}

func TestAdversary_RequirementsDeclaredIdempotentlyAcrossRounds(t *testing.T) {
	// F3: each remediation round MUST use a fresh Adversary.
	// Cross-round Requirement idempotency is a property of the
	// ClassificationsStore, not the Adversary.
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	r := passConceptRunner(t, true)
	attack := AdversaryAttack{
		ArrowID: "A1", PassID: "P1",
		Requirements: []Requirement{{ID: "R1", MinDepth: DepthRankShallow, Description: "req R1"}},
	}
	// Round 0 — fresh adversary
	a1 := NewAdversary(findings, classifications, r)
	report1, err := a1.Attack(context.Background(), attack)
	if err != nil {
		t.Fatal(err)
	}
	if len(report1.HarnessErrors) != 0 {
		t.Errorf("round 0 harness errors: %v", report1.HarnessErrors)
	}
	// Round 1 — fresh adversary (F3 invariant)
	attack.Round = 1
	a2 := NewAdversary(findings, classifications, r)
	report2, err := a2.Attack(context.Background(), attack)
	if err != nil {
		t.Fatal(err)
	}
	if len(report2.HarnessErrors) != 0 {
		t.Errorf("round 1 should not error on dup requirement: %v", report2.HarnessErrors)
	}
}

func TestAdversary_SingleShot_RefusesReUse(t *testing.T) {
	// F3: gates.md §11 mandates a fresh adversary instance per
	// remediation round. The runner enforces this with a used flag.
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	r := passConceptRunner(t, true)
	a := NewAdversary(findings, classifications, r)
	if _, err := a.Attack(context.Background(), AdversaryAttack{
		ArrowID: "A1", PassID: "P1",
	}); err != nil {
		t.Fatal(err)
	}
	_, err := a.Attack(context.Background(), AdversaryAttack{
		ArrowID: "A1", PassID: "P1",
	})
	if !errors.Is(err, ErrAdversaryAlreadyUsed) {
		t.Errorf("second Attack should error with ErrAdversaryAlreadyUsed; got %v", err)
	}
}

func TestAdversary_UnevaluatedClauseRaisesFinding(t *testing.T) {
	// F4: a clause that ends Unevaluated (no model, missing dep)
	// must raise a clause-falsification finding with status
	// Unevaluated. It is NOT a silent pass.
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	reg := NewRegistry()
	_ = reg.Register("unevaluated-clause", func(_ context.Context, _ Clause) (*Result, error) {
		return &Result{Unevaluated: true, Reason: "no model"}, nil
	})
	r := NewRunner(reg, nil, DepthRankNone)
	a := NewAdversary(findings, classifications, r)
	report, err := a.Attack(context.Background(), AdversaryAttack{
		ArrowID: "A1", PassID: "P1",
		DepthClauses: []Clause{{Concept: "unevaluated-clause", ClauseID: "C1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.ClauseFalsifications) != 1 {
		t.Fatalf("expected 1 entry; got %d", len(report.ClauseFalsifications))
	}
	if !report.ClauseFalsifications[0].Unevaluated {
		t.Errorf("entry should be Unevaluated; got %+v", report.ClauseFalsifications[0])
	}
	stored := findings.ForArrow("A1")
	if len(stored) != 1 || stored[0].Status != FindingStatusUnevaluated {
		t.Errorf("findings = %+v; want one unevaluated", stored)
	}
}

func TestAdversary_HookPanicSurfacesAsHarnessError(t *testing.T) {
	// F5: a panicking hook must NOT crash the goroutine; it's
	// recovered into a HarnessError.
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	r := passConceptRunner(t, true)
	a := NewAdversary(findings, classifications, r)
	a.OpenSweep = func(_ context.Context, _ AdversaryAttack) ([]FindingRecord, error) {
		panic("LLM exploded")
	}
	report, err := a.Attack(context.Background(), AdversaryAttack{ArrowID: "A", PassID: "P"})
	if err != nil {
		t.Fatalf("Attack should not propagate hook panic: %v", err)
	}
	if len(report.HarnessErrors) == 0 {
		t.Error("expected hook panic to appear in HarnessErrors")
	}
}

func TestAdversary_BelowMinResolvedOnReClassifyAboveMin(t *testing.T) {
	// F16: a re-classification observing Above-min for a previously-
	// below-min requirement must auto-resolve the stale finding.
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	r := passConceptRunner(t, true)

	// Round 0: classify below-min → finding raised.
	a1 := NewAdversary(findings, classifications, r)
	a1.Classify = func(_ context.Context, _ AdversaryAttack) ([]Classification, error) {
		return []Classification{{RequirementID: "R1", Observed: DepthRankMocked, Evidence: "mock"}}, nil
	}
	report1, _ := a1.Attack(context.Background(), AdversaryAttack{
		ArrowID: "A1", PassID: "P1",
		Requirements: []Requirement{{ID: "R1", MinDepth: DepthRankRealistic, Description: "checkout"}},
	})
	if len(report1.DepthBelowMinFindings) != 1 {
		t.Fatalf("round 0 should raise 1 below-min; got %v", report1.DepthBelowMinFindings)
	}

	// Round 1: fresh adversary; classify above-min → prior finding auto-resolved.
	a2 := NewAdversary(findings, classifications, r)
	a2.Classify = func(_ context.Context, _ AdversaryAttack) ([]Classification, error) {
		return []Classification{{RequirementID: "R1", Observed: DepthRankRealistic, Evidence: "live pg"}}, nil
	}
	report2, _ := a2.Attack(context.Background(), AdversaryAttack{
		ArrowID: "A1", PassID: "P1", Round: 1,
		Requirements: []Requirement{{ID: "R1", MinDepth: DepthRankRealistic, Description: "checkout"}},
	})
	if len(report2.ResolvedFindings) != 1 {
		t.Errorf("round 1 should auto-resolve 1 finding; got %v", report2.ResolvedFindings)
	}
	// Verify the stale finding is in fact resolved.
	for _, f := range findings.ForArrow("A1") {
		if f.Type == FindingTypeDepthBelowMin && f.Status != FindingStatusResolved {
			t.Errorf("below-min finding should be Resolved; got %v", f.Status)
		}
	}
}

func TestAdversary_ConcurrentAttacksOnDifferentArrows(t *testing.T) {
	// F33: gates.md §11 implies different arrows can be attacked
	// independently. With the F3 single-shot enforcement, each
	// concurrent attack uses its OWN Adversary so there is no
	// shared mutable receiver state. The race detector validates.
	t.Parallel()
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	r := passConceptRunner(t, true)
	done := make(chan struct{}, 4)
	for i := 0; i < 4; i++ {
		i := i
		go func() {
			a := NewAdversary(findings, classifications, r)
			_, err := a.Attack(context.Background(), AdversaryAttack{
				ArrowID: fmt.Sprintf("arrow-%d", i),
				PassID:  fmt.Sprintf("pass-%d", i),
			})
			if err != nil {
				t.Errorf("concurrent attack %d: %v", i, err)
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 4; i++ {
		<-done
	}
}

func TestAdversary_DefaultIDGenUniqueInTightLoop(t *testing.T) {
	// F37: tight-loop uniqueness; pre-flight for engine integration.
	seen := map[string]struct{}{}
	for i := 0; i < 10_000; i++ {
		id := defaultAdversaryFindingIDGen()
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate ID at iter %d: %q", i, id)
		}
		seen[id] = struct{}{}
	}
}

func TestAdversary_OpenSweepHookBadFindingSurfacesSyntheticHarnessError(t *testing.T) {
	// F18: open-sweep producing a finding rejected by Raise (e.g.,
	// Severity out of range) must surface a synthetic harness-error
	// finding rather than silently disappearing.
	findings := NewFindingsStore()
	classifications := NewClassificationsStore()
	r := passConceptRunner(t, true)
	a := NewAdversary(findings, classifications, r)
	a.OpenSweep = func(_ context.Context, _ AdversaryAttack) ([]FindingRecord, error) {
		return []FindingRecord{
			{Severity: 99, Description: "real defect"}, // Type missing → fails Raise
		}, nil
	}
	report, _ := a.Attack(context.Background(), AdversaryAttack{ArrowID: "A1", PassID: "P1"})
	if len(report.HarnessErrors) == 0 {
		t.Error("bad finding should surface as HarnessError")
	}
	stored := findings.ForArrow("A1")
	if len(stored) == 0 {
		t.Error("a synthetic open-sweep finding should be raised for visibility")
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
