package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/witlox/ghyll/runner"
)

// fullReplayCycle runs an end-to-end "session 1 writes, session 2
// replays" scenario. Returns the second-session targets so tests
// can assert against them.
func fullReplayCycle(t *testing.T) (*Store, ReplayTargets, ReplayCounts) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "engine.db")

	// Session 1: write a representative mix.
	{
		store, err := OpenStore(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		j := NewJournal(store, testLogger())
		fs := runner.NewFindingsStore()
		cs := runner.NewClassificationsStore()
		g := runner.NewGrid()
		aq := runner.NewAmendmentQueue()
		j.AttachFindings(fs)
		j.AttachClassifications(cs)
		j.AttachGrid(g)
		j.AttachAmendments(aq)

		// Grid arrow
		def := runner.ArrowDefinition{
			ID:         "A1",
			SourceRole: "analyst", TargetRole: "architect",
			Stratum: "L4", Context: "checkout",
			Clauses: []runner.Clause{{Concept: "lint-clean", ClauseID: "C1"}},
			Requirements: []runner.Requirement{
				{ID: "R1", MinDepth: runner.DepthRankMocked, Description: "checkout"},
			},
		}
		_, _ = g.Append(def)

		// Requirement + classification
		_ = cs.DeclareRequirement("A1", runner.Requirement{
			ID: "R1", MinDepth: runner.DepthRankMocked, Description: "checkout",
		})
		_ = cs.RecordClassification("A1", runner.Classification{
			RequirementID: "R1", Observed: runner.DepthRankRealistic, Evidence: "pg",
		})

		// Findings — one open, one resolved
		_ = fs.Raise(runner.FindingRecord{
			ID: "F1", ArrowID: "A1", Type: runner.FindingTypeLocalBug,
			Severity: runner.SeverityHigh, Status: runner.FindingStatusOpen,
			Description: "off-by-one", RaisedAt: "t0",
		})
		_ = fs.Raise(runner.FindingRecord{
			ID: "F2", ArrowID: "A1", Type: runner.FindingTypeLocalBug,
			Severity: runner.SeverityLow, Status: runner.FindingStatusOpen,
			RaisedAt: "t1",
		})
		_ = fs.Transition("F2", runner.FindingStatusResolved)

		// Amendments — one drained (already processed) + one pending.
		// Enqueue + drain the first; then enqueue a different ID
		// without calling Reset (Reset would clear the drained_at
		// column in sqlite per the journal's session-boundary
		// semantics).
		_ = aq.Enqueue(runner.AmendmentRequest{
			ID: "am-drained", Reason: runner.AmendmentReasonMissingCrossContextSpec,
			SourceArrow: "A1", TargetRole: "analyst",
			Contexts: []string{"a", "b"}, FindingIDs: []string{"F2"}, CreatedAt: "t0",
		})
		_ = aq.Drain()
		_ = aq.Enqueue(runner.AmendmentRequest{
			ID: "am-pending", Reason: runner.AmendmentReasonMissingCrossContextSpec,
			SourceArrow: "A1", TargetRole: "analyst",
			Contexts: []string{"payment", "identity"}, FindingIDs: []string{"F1"}, CreatedAt: "t1",
		})

		j.Close()
		_ = store.Close()
	}

	// Session 2: fresh stores, replay from sqlite.
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	targets := ReplayTargets{
		Findings:        runner.NewFindingsStore(),
		Classifications: runner.NewClassificationsStore(),
		Grid:            runner.NewGrid(),
		Amendments:      runner.NewAmendmentQueue(),
	}
	counts, err := Replay(ctx, store, targets)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, targets, counts
}

func TestReplay_RestoresGridArrows(t *testing.T) {
	_, tgt, counts := fullReplayCycle(t)
	if counts.Arrows != 1 {
		t.Errorf("Arrows replayed = %d; want 1", counts.Arrows)
	}
	def, ok := tgt.Grid.Lookup("A1")
	if !ok || def.SourceRole != "analyst" {
		t.Errorf("grid arrow missing or wrong: %+v", def)
	}
}

func TestReplay_RestoresRequirementsAndClassifications(t *testing.T) {
	_, tgt, counts := fullReplayCycle(t)
	if counts.Requirements != 1 || counts.Classifications != 1 {
		t.Errorf("counts: reqs=%d cls=%d", counts.Requirements, counts.Classifications)
	}
	reqs := tgt.Classifications.RequirementsForArrow("A1")
	if len(reqs) != 1 || reqs[0].MinDepth != runner.DepthRankMocked {
		t.Errorf("reqs: %+v", reqs)
	}
	cls := tgt.Classifications.ClassificationsForArrow("A1")
	if len(cls) != 1 || cls[0].Observed != runner.DepthRankRealistic {
		t.Errorf("cls: %+v", cls)
	}
}

func TestReplay_RestoresFindingsWithStatuses(t *testing.T) {
	_, tgt, counts := fullReplayCycle(t)
	if counts.Findings != 2 {
		t.Errorf("findings replayed = %d; want 2", counts.Findings)
	}
	f1, _ := tgt.Findings.Get("F1")
	f2, _ := tgt.Findings.Get("F2")
	if f1.Status != runner.FindingStatusOpen {
		t.Errorf("F1 status = %v; want open", f1.Status)
	}
	if f2.Status != runner.FindingStatusResolved {
		t.Errorf("F2 status = %v; want resolved (post-transition)", f2.Status)
	}
}

func TestReplay_AmendmentDedupSurvivesRestart(t *testing.T) {
	// validation-pass-4 F44 + phase-9 invariant: a drained amendment
	// MUST stay refused after process restart.
	_, tgt, counts := fullReplayCycle(t)
	if counts.AmendmentsActive != 1 {
		t.Errorf("active = %d; want 1", counts.AmendmentsActive)
	}
	if counts.AmendmentsDrained != 1 {
		t.Errorf("drained = %d; want 1", counts.AmendmentsDrained)
	}
	// Re-enqueueing the drained amendment must fail.
	err := tgt.Amendments.Enqueue(runner.AmendmentRequest{
		ID: "am-drained", Reason: runner.AmendmentReasonMissingCrossContextSpec,
		SourceArrow: "A1", TargetRole: "analyst",
		Contexts: []string{"a", "b"}, FindingIDs: []string{"F2"},
	})
	if err == nil {
		t.Error("re-enqueueing a drained amendment should be refused after replay")
	}
}

func TestReplay_RejectsNonEmptyTargets(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "e.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	tgt := ReplayTargets{
		Findings:        runner.NewFindingsStore(),
		Classifications: runner.NewClassificationsStore(),
		Grid:            runner.NewGrid(),
		Amendments:      runner.NewAmendmentQueue(),
	}
	// Pre-populate one of them — replay must refuse.
	_ = tgt.Findings.Raise(runner.FindingRecord{
		ID: "F", ArrowID: "A", Type: runner.FindingTypeLocalBug,
	})
	_, err = Replay(context.Background(), store, tgt)
	if err == nil {
		t.Error("Replay should refuse non-empty targets")
	}
}
