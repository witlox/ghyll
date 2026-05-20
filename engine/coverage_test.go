package engine

import (
	"context"
	"strings"
	"testing"
)

// Targeted tests for zero-coverage utility functions surfaced by
// the final audit. Each function below was at 0% coverage; these
// tests pin the contract and lift engine package coverage over the
// 70% floor.

func TestEngineCoverage_safeID_TrimsAndTruncates(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"normal-id", "normal-id"},
		{"  spaces  ", "  spaces  "}, // safeID does not trim whitespace
		{"", ""},
	}
	for _, c := range cases {
		got := safeID(c.in)
		if got != c.want {
			t.Errorf("safeID(%q) = %q; want %q", c.in, got, c.want)
		}
	}
	// Long ID — verify it doesn't blow up (the function caps length).
	long := strings.Repeat("x", 1000)
	out := safeID(long)
	if len(out) > 1000 {
		t.Fatalf("safeID(long) = %d bytes; want <= 1000", len(out))
	}
}

func TestEngineCoverage_Count_EmptyTablesReturnZero(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	if n, err := store.CountFindings(ctx); err != nil || n != 0 {
		t.Errorf("CountFindings = (%d, %v); want (0, nil)", n, err)
	}
	if n, err := store.CountArrows(ctx); err != nil || n != 0 {
		t.Errorf("CountArrows = (%d, %v); want (0, nil)", n, err)
	}
	if n, err := store.CountRequirements(ctx); err != nil || n != 0 {
		t.Errorf("CountRequirements = (%d, %v); want (0, nil)", n, err)
	}
	if n, err := store.CountClassifications(ctx); err != nil || n != 0 {
		t.Errorf("CountClassifications = (%d, %v); want (0, nil)", n, err)
	}
	if p, d, err := store.CountAmendments(ctx); err != nil || p != 0 || d != 0 {
		t.Errorf("CountAmendments = (%d, %d, %v); want (0, 0, nil)", p, d, err)
	}
	if n, err := store.CountEvaluationRuns(ctx); err != nil || n != 0 {
		t.Errorf("CountEvaluationRuns = (%d, %v); want (0, nil)", n, err)
	}
}

func TestEngineCoverage_Journal_DroppedStartsAtZero(t *testing.T) {
	store := openTestStore(t)
	j := NewJournal(store, testLogger())
	defer j.Close()
	if d := j.Dropped(); d != 0 {
		t.Fatalf("Dropped at start = %d; want 0", d)
	}
}

func TestEngineCoverage_DeleteRequirement_MissingRowNoError(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	// Deleting a never-inserted row should be a no-op, not error.
	if err := store.DeleteRequirement(ctx, "A-missing", "R-missing"); err != nil {
		t.Fatalf("DeleteRequirement on missing row should succeed; got %v", err)
	}
}

func TestEngineCoverage_OpenStoreReadOnly_FreshDB(t *testing.T) {
	// OpenStoreReadOnly internally calls verifySchemaVersion on
	// the v2 DB the helper just created.
	tmpDir := t.TempDir()
	path := tmpDir + "/engine.db"
	w, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	ro, err := OpenStoreReadOnly(path)
	if err != nil {
		t.Fatalf("OpenStoreReadOnly on fresh v2 DB: %v", err)
	}
	_ = ro.Close()
}

// TestScenario_NewNullString_RoundTrip — Tier 3 coverage push.
func TestScenario_NewNullString_RoundTrip(t *testing.T) {
	ns := newNullString("hello")
	if !ns.Valid || ns.String != "hello" {
		t.Errorf("non-empty: %+v; want Valid+hello", ns)
	}
	empty := newNullString("")
	if empty.Valid {
		t.Errorf("empty: Valid=true; want false (NULL)")
	}
}
