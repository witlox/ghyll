package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenStore(filepath.Join(dir, "engine.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenStore_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "engine.db")
	s1, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = s1.Close()
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = s2.Close()
}

func TestFindings_UpsertAndQuery(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.UpsertFinding(ctx, FindingRecord{
		ID: "F1", ArrowID: "A1", Type: "local-bug",
		Severity: 3, Status: "open", Description: "off by one",
		RaisedAt: "2026-05-18T12:00:00Z", RaisedByRole: "integrator",
		StoreVersion: 1, UpdatedAt: "2026-05-18T12:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetFinding(ctx, "F1")
	if err != nil || !ok {
		t.Fatalf("Get: %v ok=%v", err, ok)
	}
	if got.Severity != 3 || got.Description != "off by one" {
		t.Errorf("Get returned wrong record: %+v", got)
	}
	// Upsert (Transition path)
	if err := s.UpsertFinding(ctx, FindingRecord{
		ID: "F1", ArrowID: "A1", Type: "local-bug",
		Severity: 3, Status: "resolved",
		TransitionCount: 1, StoreVersion: 2,
		UpdatedAt: "2026-05-18T12:01:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.GetFinding(ctx, "F1")
	if got.Status != "resolved" || got.TransitionCount != 1 {
		t.Errorf("upsert didn't update: %+v", got)
	}
}

func TestFindings_DeleteCascadesTransitions(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	_ = s.UpsertFinding(ctx, FindingRecord{
		ID: "F1", ArrowID: "A1", Type: "local-bug",
		Severity: 1, Status: "open",
	})
	if err := s.InsertTransition(ctx, TransitionRecord{
		FindingID: "F1", FromStatus: "open", ToStatus: "resolved",
		At: "2026-05-18T12:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFinding(ctx, "F1"); err != nil {
		t.Fatal(err)
	}
	// Transitions should be cascaded (ON DELETE CASCADE).
	ts, err := s.ListTransitions(ctx, "F1", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 0 {
		t.Errorf("transitions not cascaded: %d remaining", len(ts))
	}
}

func TestListFindings_FilterByArrow(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for i, arrow := range []string{"A1", "A1", "A2"} {
		_ = s.UpsertFinding(ctx, FindingRecord{
			ID: fmt.Sprintf("F%03d", i), ArrowID: arrow,
			Type: "local-bug", Severity: i + 1, Status: "open",
		})
	}
	got, err := s.ListFindings(ctx, FindingFilter{ArrowID: "A1", MinSeverity: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("arrow A1 returned %d findings; want 2", len(got))
	}
	for _, f := range got {
		if f.ArrowID != "A1" {
			t.Errorf("filter leaked: %+v", f)
		}
	}
}

func TestListFindings_FilterByMinSeverity(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for i, sev := range []int{0, 1, 2, 3, 4} {
		_ = s.UpsertFinding(ctx, FindingRecord{
			ID: fmt.Sprintf("F%03d", i), ArrowID: "A",
			Type: "x", Severity: sev, Status: "open",
		})
	}
	got, err := s.ListFindings(ctx, FindingFilter{MinSeverity: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("MinSeverity=3 returned %d; want 2 (sev 3 and 4)", len(got))
	}
	for _, f := range got {
		if f.Severity < 3 {
			t.Errorf("below threshold leaked: %+v", f)
		}
	}
}

func TestRequirementsAndClassifications_Roundtrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.UpsertRequirement(ctx, RequirementRecord{
		ArrowID: "A1", ReqID: "R1", MinDepth: 2,
		Description: "checkout integration",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertClassification(ctx, ClassificationRecord{
		ArrowID: "A1", ReqID: "R1", Observed: 3, Evidence: "live pg",
	}); err != nil {
		t.Fatal(err)
	}
	reqs, _ := s.ListRequirements(ctx, RequirementFilter{ArrowID: "A1"})
	cls, _ := s.ListClassifications(ctx, ClassificationFilter{ArrowID: "A1"})
	if len(reqs) != 1 || len(cls) != 1 {
		t.Errorf("reqs=%d cls=%d; want 1,1", len(reqs), len(cls))
	}
	// Delete arrow → both gone.
	if err := s.DeleteArrow(ctx, "A1"); err != nil {
		t.Fatal(err)
	}
	reqs, _ = s.ListRequirements(ctx, RequirementFilter{ArrowID: "A1"})
	cls, _ = s.ListClassifications(ctx, ClassificationFilter{ArrowID: "A1"})
	if len(reqs) != 0 || len(cls) != 0 {
		t.Errorf("DeleteArrow didn't clean up: reqs=%d cls=%d", len(reqs), len(cls))
	}
}

func TestGridArrows_VersionedAppend(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	clauses := JSONSlice([]map[string]any{{"concept": "lint-clean"}})
	for i, ver := range []uint64{1, 2, 3} {
		if err := s.InsertGridArrow(ctx, GridArrowRecord{
			ID: "A1", GridVersion: ver,
			SourceRole: "analyst", TargetRole: "architect",
			Stratum: "L4", Context: "checkout",
			ClausesJSON: clauses, RequirementsJSON: "[]",
			Kind: "append", DeclaredAt: "t" + string(rune('0'+i)),
		}); err != nil {
			t.Fatalf("insert v%d: %v", ver, err)
		}
	}
	got, _ := s.ListArrows(ctx, ArrowFilter{})
	if len(got) != 3 {
		t.Errorf("got %d arrow rows; want 3", len(got))
	}
	// Re-insert same (id, ver) fails.
	err := s.InsertGridArrow(ctx, GridArrowRecord{
		ID: "A1", GridVersion: 1, SourceRole: "x", TargetRole: "y",
		Stratum: "L4", Context: "c", ClausesJSON: "[]", RequirementsJSON: "[]",
		Kind: "append",
	})
	if err == nil {
		t.Error("duplicate (id,ver) should error")
	}
}

func TestAmendments_PendingAndDrained(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	contexts := JSONSlice([]string{"payment", "identity"})
	if err := s.UpsertAmendment(ctx, AmendmentRecord{
		ID: "am1", Reason: "missing-cross-context-spec",
		SourceArrow: "A1", TargetRole: "analyst",
		ContextsJSON: contexts, FindingIDsJSON: "[\"F1\"]",
		CreatedAt: "2026-05-18T12:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	pending := false
	got, _ := s.ListAmendments(ctx, AmendmentFilter{Drained: &pending, SourceArrow: "A1"})
	if len(got) != 1 {
		t.Fatalf("pending amendments: got %d", len(got))
	}
	if got[0].DrainedAt.Valid {
		t.Error("pending amendment should have null drained_at")
	}
}

func TestEvaluationRuns_Roundtrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	rj, _ := json.Marshal(map[string]any{"pass": true})
	if err := s.InsertEvaluationRun(ctx, EvaluationRunRecord{
		ID: "run-1", ClauseID: "C1", PassID: "P1",
		ArrowID: "A1", GridVersion: 1,
		EvaluatorConcept: "lint-clean", EvaluatorGeneration: 1,
		StartedAt: "t0", CompletedAt: "t1",
		StartStatus: "pending", EndStatus: "pass",
		ResultJSON: string(rj),
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ListEvaluationRuns(ctx, RunFilter{ArrowID: "A1"})
	if len(got) != 1 {
		t.Fatalf("runs: got %d; want 1", len(got))
	}
	if got[0].ID != "run-1" {
		t.Errorf("run id = %q", got[0].ID)
	}
}

func TestListFindings_DefaultLimit(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	// Insert 200; default limit caps at 100.
	for i := 0; i < 200; i++ {
		// E9: use fmt.Sprintf so IDs are unambiguously unique.
		_ = s.UpsertFinding(ctx, FindingRecord{
			ID:      fmt.Sprintf("F%05d", i),
			ArrowID: "A", Type: "x", Severity: 1, Status: "open",
		})
	}
	got, _ := s.ListFindings(ctx, FindingFilter{MinSeverity: -1})
	if len(got) != 100 {
		t.Errorf("default limit = %d; want 100", len(got))
	}
}
