package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/witlox/ghyll/runner"
)

// TestScenario_EngineAttestations_Tier2Columns_FullRoundtrip verifies
// every Tier 2 column (pass_id, context, stratum, adversary_role,
// unit, unit_payload_json, hint_json) survives insert → list →
// readAttestation. Gate-2 CORR-A-1 regression coverage.
func TestScenario_EngineAttestations_Tier2Columns_FullRoundtrip(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "engine.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	rec := runner.AttestationRecord{
		ID:              "att-A1-C1-v1",
		Kind:            runner.AttestationKindDepthType,
		ArrowID:         "A1",
		ClauseID:        "C1",
		OpID:            "alice",
		AttestedByRole:  "operator",
		SourceRole:      "analyst",
		TargetRole:      "architect",
		Verdict:         runner.AttestationFail,
		Reason:          "needs review",
		Timestamp:       1716100000_000000000,
		GridVersion:     7,
		PassID:          "P-roundtrip",
		Context:         "ctxA",
		Stratum:         "L1",
		AdversaryRole:   "adversary",
		Unit:            runner.VerdictUnitRecordLocationsInspected,
		UnitPayload:     runner.VerdictUnitPayload{Inspected: []string{"file.go:1-5", "other.go:7"}},
		UnitPayloadJSON: `{"inspected":["file.go:1-5","other.go:7"]}`,
		HintJSON:        `{"arrow_id":"A1"}`,
	}
	if err := store.insertAttestation(context.Background(), rec); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := store.readAttestation(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !runner.AttestationRecordsEqual(got, rec) {
		t.Errorf("readAttestation roundtrip mismatch:\n got: %+v\nwant: %+v", got, rec)
	}

	recs, err := store.listAttestations(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("list len = %d; want 1", len(recs))
	}
	if !runner.AttestationRecordsEqual(recs[0], rec) {
		t.Errorf("listAttestations roundtrip mismatch:\n got: %+v\nwant: %+v", recs[0], rec)
	}

	// Verify hydrateUnitPayload populated the typed slice from JSON.
	if len(got.UnitPayload.Inspected) != 2 {
		t.Errorf("hydrate UnitPayload.Inspected = %v; want 2 entries", got.UnitPayload.Inspected)
	}
}

// TestScenario_EngineAttestations_Tier2_IdempotentReInsert verifies a
// re-insert with byte-identical Tier 2 content is silently no-op
// (gate-2 CORR-A-2 — was emitting spurious divergence events on every
// boot because AttestationRecordsEqual compared all fields).
func TestScenario_EngineAttestations_Tier2_IdempotentReInsert(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "engine.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	rec := runner.AttestationRecord{
		ID:              "att-idempotent",
		Kind:            runner.AttestationKindDepthType,
		ArrowID:         "A1",
		ClauseID:        "C1",
		OpID:            "alice",
		AttestedByRole:  "operator",
		Verdict:         runner.AttestationPass,
		Timestamp:       1716100000_000000000,
		GridVersion:     1,
		PassID:          "P-idempotent",
		Context:         "ctxA",
		Stratum:         "L1",
		Unit:            runner.VerdictUnitConfirm,
		UnitPayloadJSON: "{}",
		HintJSON:        "{}",
	}
	if err := store.insertAttestation(context.Background(), rec); err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	// Re-insert identical content — must NOT return ErrAttestationConflict.
	if err := store.insertAttestation(context.Background(), rec); err != nil {
		t.Fatalf("idempotent re-insert: %v", err)
	}
}
