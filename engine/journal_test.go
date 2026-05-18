package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/witlox/ghyll/runner"
)

// helper: open store + journal + a FindingsStore wired to it.
func setupJournal(t *testing.T) (*Store, *Journal, *runner.FindingsStore, *runner.ClassificationsStore, *runner.Grid, *runner.AmendmentQueue) {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "engine.db"))
	if err != nil {
		t.Fatal(err)
	}
	j := NewJournal(store, nil)
	fs := runner.NewFindingsStore()
	cs := runner.NewClassificationsStore()
	g := runner.NewGrid()
	aq := runner.NewAmendmentQueue()
	j.AttachFindings(fs)
	j.AttachClassifications(cs)
	j.AttachGrid(g)
	j.AttachAmendments(aq)
	t.Cleanup(func() {
		j.Close()
		_ = store.Close()
	})
	return store, j, fs, cs, g, aq
}

func TestJournal_FindingsRaiseAndTransitionPersist(t *testing.T) {
	store, _, fs, _, _, _ := setupJournal(t)
	ctx := context.Background()
	if err := fs.Raise(runner.FindingRecord{
		ID: "F1", ArrowID: "A1",
		Type:     runner.FindingTypeLocalBug,
		Severity: runner.SeverityHigh, Status: runner.FindingStatusOpen,
		Description: "off-by-one", RaisedAt: "t0",
	}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.GetFinding(ctx, "F1")
	if err != nil || !ok {
		t.Fatalf("get: %v ok=%v", err, ok)
	}
	if got.Status != "open" || got.Severity != 3 {
		t.Errorf("persisted: %+v", got)
	}

	// Transition
	if err := fs.Transition("F1", runner.FindingStatusResolved); err != nil {
		t.Fatal(err)
	}
	got, _, _ = store.GetFinding(ctx, "F1")
	if got.Status != "resolved" {
		t.Errorf("after transition: status = %q", got.Status)
	}
	// Audit table.
	ts, _ := store.ListTransitions(ctx, "F1", 100)
	if len(ts) != 1 {
		t.Fatalf("transitions: %d; want 1", len(ts))
	}
	if ts[0].FromStatus != "open" || ts[0].ToStatus != "resolved" {
		t.Errorf("transition row: %+v", ts[0])
	}
}

func TestJournal_FindingsForgetPersists(t *testing.T) {
	store, _, fs, _, _, _ := setupJournal(t)
	ctx := context.Background()
	_ = fs.Raise(runner.FindingRecord{
		ID: "F1", ArrowID: "A1", Type: runner.FindingTypeLocalBug,
		Status: runner.FindingStatusOpen,
	})
	if err := fs.Forget("F1"); err != nil {
		t.Fatal(err)
	}
	_, ok, _ := store.GetFinding(ctx, "F1")
	if ok {
		t.Error("forgotten finding still in store")
	}
}

func TestJournal_ClassificationsPersist(t *testing.T) {
	store, _, _, cs, _, _ := setupJournal(t)
	ctx := context.Background()
	_ = cs.DeclareRequirement("A1", runner.Requirement{
		ID: "R1", MinDepth: runner.DepthRankMocked, Description: "checkout",
	})
	_ = cs.RecordClassification("A1", runner.Classification{
		RequirementID: "R1", Observed: runner.DepthRankRealistic, Evidence: "live pg",
	})
	reqs, _ := store.ListRequirements(ctx, RequirementFilter{ArrowID: "A1"})
	cls, _ := store.ListClassifications(ctx, RequirementFilter{ArrowID: "A1"})
	if len(reqs) != 1 || reqs[0].MinDepth != int(runner.DepthRankMocked) {
		t.Errorf("requirements: %+v", reqs)
	}
	if len(cls) != 1 || cls[0].Observed != int(runner.DepthRankRealistic) {
		t.Errorf("classifications: %+v", cls)
	}
}

func TestJournal_OverwriteTracksAudit(t *testing.T) {
	store, _, _, cs, _, _ := setupJournal(t)
	ctx := context.Background()
	_ = cs.DeclareRequirement("A1", runner.Requirement{
		ID: "R1", MinDepth: runner.DepthRankShallow, Description: "x",
	})
	_ = cs.RecordClassification("A1", runner.Classification{
		RequirementID: "R1", Observed: runner.DepthRankShallow, Evidence: "first",
	})
	_ = cs.RecordClassification("A1", runner.Classification{
		RequirementID: "R1", Observed: runner.DepthRankRealistic, Evidence: "second",
	})
	rows, err := store.db.QueryContext(ctx,
		`SELECT before_observed, after_observed FROM classification_overwrites WHERE arrow_id = ? AND req_id = ?`,
		"A1", "R1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	count := 0
	for rows.Next() {
		var b, a int
		_ = rows.Scan(&b, &a)
		if b != int(runner.DepthRankShallow) || a != int(runner.DepthRankRealistic) {
			t.Errorf("overwrite: before=%d after=%d", b, a)
		}
		count++
	}
	if count != 1 {
		t.Errorf("overwrite rows = %d; want 1", count)
	}
}

func TestJournal_GridArrowAppendPersists(t *testing.T) {
	store, _, _, _, g, _ := setupJournal(t)
	ctx := context.Background()
	def := runner.ArrowDefinition{
		ID:         "A1",
		SourceRole: "analyst", TargetRole: "architect",
		Stratum: "L4", Context: "checkout",
		Clauses: []runner.Clause{{Concept: "lint-clean"}},
	}
	if _, err := g.Append(def); err != nil {
		t.Fatal(err)
	}
	arrows, _ := store.ListArrows(ctx, ArrowFilter{})
	if len(arrows) != 1 {
		t.Fatalf("arrows persisted = %d; want 1", len(arrows))
	}
	if arrows[0].Kind != "append" {
		t.Errorf("kind = %q; want append", arrows[0].Kind)
	}
}

func TestJournal_GridOnTheSpotKindDistinguished(t *testing.T) {
	store, _, _, _, g, _ := setupJournal(t)
	ctx := context.Background()
	def := runner.ArrowDefinition{
		ID: "A1", SourceRole: "analyst", TargetRole: "architect",
		Stratum: "L4", Context: "checkout",
		Clauses: []runner.Clause{{Concept: "x"}},
	}
	_, _ = g.AppendOnTheSpot(def)
	arrows, _ := store.ListArrows(ctx, ArrowFilter{Kind: "on-the-spot"})
	if len(arrows) != 1 {
		t.Errorf("on-the-spot arrows = %d; want 1", len(arrows))
	}
}

func TestJournal_AmendmentEnqueueAndDrain(t *testing.T) {
	store, _, _, _, _, aq := setupJournal(t)
	ctx := context.Background()
	req := runner.AmendmentRequest{
		ID:          "am1",
		Reason:      runner.AmendmentReasonMissingCrossContextSpec,
		SourceArrow: "A1", TargetRole: "analyst",
		Contexts:   []string{"payment", "identity"},
		FindingIDs: []string{"F1"},
		CreatedAt:  "t0",
	}
	if err := aq.Enqueue(req); err != nil {
		t.Fatal(err)
	}
	pending := false
	got, _ := store.ListAmendments(ctx, AmendmentFilter{Drained: &pending})
	if len(got) != 1 {
		t.Fatalf("pending amendments: %d; want 1", len(got))
	}
	_ = aq.Drain()
	drained := true
	got, _ = store.ListAmendments(ctx, AmendmentFilter{Drained: &drained})
	if len(got) != 1 {
		t.Errorf("drained amendments: %d; want 1", len(got))
	}
}

func TestJournal_EvaluationRunPersists(t *testing.T) {
	store, j, _, _, _, _ := setupJournal(t)
	ctx := context.Background()
	reg := runner.NewRegistry()
	_ = reg.Register("test", func(_ context.Context, _ runner.Clause) (*runner.Result, error) {
		return &runner.Result{Pass: true}, nil
	})
	r := runner.NewRunner(reg)
	j.AttachRunner(r)
	run, err := r.Evaluate(ctx, "C1", "P1", runner.Clause{Concept: "test", ArrowID: "A1"})
	if err != nil {
		t.Fatal(err)
	}
	if run == nil {
		t.Fatal("evaluate returned nil")
	}
	runs, _ := store.ListEvaluationRuns(ctx, RunFilter{ArrowID: "A1"})
	if len(runs) != 1 {
		t.Errorf("runs persisted = %d; want 1", len(runs))
	}
	if runs[0].EndStatus != "pass" {
		t.Errorf("run.EndStatus = %q", runs[0].EndStatus)
	}
}
