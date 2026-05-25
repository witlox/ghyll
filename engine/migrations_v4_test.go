// Tests for diamond v4 / ADR-v4-008 schema migrations.

package engine

import (
	"context"
	"path/filepath"
	"testing"
)

// TestScenario_Engine_MigrateAddRemediationColumns_Idempotent
// verifies the ALTER TABLE adds the columns on first open AND is a
// no-op on subsequent opens against the same DB (R8 closure).
func TestScenario_Engine_MigrateAddRemediationColumns_Idempotent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "engine.db")
	s1, err := OpenStore(path)
	if err != nil {
		t.Fatalf("first OpenStore: %v", err)
	}
	cols1, err := tableColumns(s1.DB(), "passes")
	if err != nil {
		t.Fatalf("table_info pass 1: %v", err)
	}
	if _, ok := cols1["remediation_outcome"]; !ok {
		t.Error("expected remediation_outcome after first open")
	}
	if _, ok := cols1["remediation_rounds_used"]; !ok {
		t.Error("expected remediation_rounds_used after first open")
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	cols2, err := tableColumns(s2.DB(), "passes")
	if err != nil {
		t.Fatalf("table_info pass 2: %v", err)
	}
	if len(cols1) != len(cols2) {
		t.Fatalf("column count drifted across opens: %d -> %d", len(cols1), len(cols2))
	}
	_ = s2.Close()
}

// TestScenario_Engine_ArrowInvalidations_Persist verifies the
// arrow_invalidations table accepts inserts and the count round-trip
// is correct (R28 closure).
func TestScenario_Engine_ArrowInvalidations_Persist(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "engine.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	if err := s.InsertArrowInvalidation(ctx, "A1", "op-x", "stale", "operator", "2026-05-25T00:00:00Z"); err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	if err := s.InsertArrowInvalidation(ctx, "A1", "op-x", "stale-2", "operator", "2026-05-25T00:00:01Z"); err != nil {
		t.Fatalf("insert 2: %v", err)
	}
	if err := s.InsertArrowInvalidation(ctx, "A2", "op-y", "amended", "amendment", "2026-05-25T00:00:02Z"); err != nil {
		t.Fatalf("insert 3: %v", err)
	}
	if n, err := s.CountArrowInvalidations(ctx, "A1"); err != nil || n != 2 {
		t.Fatalf("count A1: got n=%d err=%v; want n=2", n, err)
	}
	if n, err := s.CountArrowInvalidations(ctx, "A2"); err != nil || n != 1 {
		t.Fatalf("count A2: got n=%d err=%v; want n=1", n, err)
	}
}
