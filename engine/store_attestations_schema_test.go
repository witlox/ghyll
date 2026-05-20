package engine

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestScenario_AttestationsSchema_Tier2Columns verifies that a fresh
// OpenStore on a brand-new database produces an attestations table
// with all 7 Tier 2 columns present (pass_id, context, stratum,
// adversary_role, unit, unit_payload_json, hint_json) and is
// idempotent on re-open.
func TestScenario_AttestationsSchema_Tier2Columns(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "engine.sqlite")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Read PRAGMA table_info(attestations) and assert all 7
	// Tier 2 columns exist.
	want := map[string]bool{
		"pass_id":           false,
		"context":           false,
		"stratum":           false,
		"adversary_role":    false,
		"unit":              false,
		"unit_payload_json": false,
		"hint_json":         false,
	}
	rows, err := store.DB().QueryContext(context.Background(), `PRAGMA table_info(attestations)`)
	if err != nil {
		t.Fatalf("PRAGMA: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			t.Fatal(err)
		}
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for col, found := range want {
		if !found {
			t.Errorf("Tier 2 column %q missing from attestations table", col)
		}
	}

	// Idempotence: re-open and check no error.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store2, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("second OpenStore (idempotent migration): %v", err)
	}
	_ = store2.Close()
}
