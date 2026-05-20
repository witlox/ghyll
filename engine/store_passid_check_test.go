package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/witlox/ghyll/runner"
)

// TestScenario_AttestationsSchema_PassIDCheck verifies the
// attestations table CHECK (pass_id <> ”) constraint rejects
// inserts with an empty pass_id and accepts well-formed records
// via CatchUpAttestations.
func TestScenario_AttestationsSchema_PassIDCheck(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "engine.sqlite")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	// Read the CREATE SQL and assert it contains the pass_id
	// constraint.
	var ddl string
	if err := store.DB().QueryRowContext(context.Background(),
		`SELECT sql FROM sqlite_master WHERE type='table' AND name='attestations'`,
	).Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ddl, "pass_id <> ''") && !strings.Contains(ddl, "pass_id != ''") {
		t.Errorf("attestations DDL missing pass_id check:\n%s", ddl)
	}

	// Attempt to insert a row with empty pass_id — schema rejects.
	_, err = store.DB().ExecContext(context.Background(), `
		INSERT INTO attestations (
			attestation_id, kind, arrow_id, clause_id, op_id,
			attested_by_role, verdict, timestamp, grid_version, pass_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "att-bad", "depth-type", "A", "C", "alice", "operator", "pass", 1, 1, "")
	if err == nil {
		t.Error("INSERT with empty pass_id succeeded; CHECK constraint not enforced")
	} else if !strings.Contains(err.Error(), "CHECK") && !strings.Contains(err.Error(), "constraint") {
		t.Errorf("err = %v; want CHECK constraint failure", err)
	}

	// And a row with non-empty pass_id succeeds (via runner.Record
	// so the rest of validation runs).
	atts := runner.NewAttestationStore()
	if err := atts.Record(runner.AttestationRecord{
		ID: "att-good", Kind: runner.AttestationKindDepthType,
		ArrowID: "A", ClauseID: "C", OpID: "alice",
		AttestedByRole: "operator",
		Verdict:        runner.AttestationPass,
		Timestamp:      1, GridVersion: 1,
		PassID: "P-1",
	}); err != nil {
		t.Fatalf("Record valid: %v", err)
	}
	if _, _, err := store.CatchUpAttestations(context.Background(), atts); err != nil {
		t.Fatalf("CatchUp: %v", err)
	}
}
