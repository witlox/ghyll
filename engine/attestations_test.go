package engine

import (
	"context"
	"testing"

	"github.com/witlox/ghyll/runner"
)

func sampleDepthTypeRecord() runner.AttestationRecord {
	return runner.AttestationRecord{
		ID:             "att-A1-C1-v1",
		Kind:           runner.AttestationKindDepthType,
		ArrowID:        "A1",
		ClauseID:       "C1",
		OpID:           "op-alice",
		AttestedByRole: "implementer",
		SourceRole:     "analyst",
		TargetRole:     "architect",
		Verdict:        runner.AttestationPass,
		Reason:         "verified",
		Timestamp:      1747663200_000000000,
		GridVersion:    1,
	}
}

func sampleOnTheSpotRecord() runner.AttestationRecord {
	return runner.AttestationRecord{
		ID:             "att-A2-v1",
		Kind:           runner.AttestationKindOnTheSpot,
		ArrowID:        "A2",
		OpID:           "op-bob",
		AttestedByRole: "integrator",
		SourceRole:     "analyst",
		TargetRole:     "architect",
		Verdict:        runner.AttestationPass,
		Timestamp:      1747663260_000000000,
		GridVersion:    1,
	}
}

func TestEngineAttestations_InsertAndList_RoundtripPreservesAllFields(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	depth := sampleDepthTypeRecord()
	ots := sampleOnTheSpotRecord()
	if err := store.insertAttestation(ctx, depth); err != nil {
		t.Fatalf("insert depth-type: %v", err)
	}
	if err := store.insertAttestation(ctx, ots); err != nil {
		t.Fatalf("insert on-the-spot: %v", err)
	}

	rows, err := store.listAttestations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("listAttestations returned %d rows; want 2", len(rows))
	}
	// ORDER BY timestamp ASC — depth (earlier) first, then ots.
	if rows[0] != depth {
		t.Errorf("rows[0] = %+v; want %+v", rows[0], depth)
	}
	if rows[1] != ots {
		t.Errorf("rows[1] = %+v; want %+v", rows[1], ots)
	}
}

func TestEngineAttestations_OnTheSpot_ClauseIDPersistsAsNull(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if err := store.insertAttestation(ctx, sampleOnTheSpotRecord()); err != nil {
		t.Fatal(err)
	}
	// Direct SQL probe — clause_id should be NULL, not the empty
	// string, so the partial index ON clause_id WHERE NOT NULL is
	// correct.
	var hasNull bool
	err := store.db.QueryRowContext(ctx,
		`SELECT clause_id IS NULL FROM attestations WHERE attestation_id = ?`,
		"att-A2-v1").Scan(&hasNull)
	if err != nil {
		t.Fatal(err)
	}
	if !hasNull {
		t.Fatal("on-the-spot record's clause_id should persist as NULL, not empty string")
	}
}

func TestEngineAttestations_CheckConstraint_RejectsBogusKind(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	rec := sampleDepthTypeRecord()
	rec.Kind = "bogus"
	err := store.insertAttestation(ctx, rec)
	if err == nil {
		t.Fatal("CHECK constraint should reject unknown kind at DB layer")
	}
}

func TestEngineAttestations_CheckConstraint_DepthTypeRequiresClauseID(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	rec := sampleDepthTypeRecord()
	rec.ClauseID = "" // becomes NULL — violates CHECK
	err := store.insertAttestation(ctx, rec)
	if err == nil {
		t.Fatal("CHECK constraint should reject depth-type with NULL clause_id")
	}
}

func TestEngineAttestations_CheckConstraint_OnTheSpotRejectsClauseID(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	rec := sampleOnTheSpotRecord()
	rec.ClauseID = "C1" // not allowed for on-the-spot
	err := store.insertAttestation(ctx, rec)
	if err == nil {
		t.Fatal("CHECK constraint should reject on-the-spot with non-NULL clause_id")
	}
}

func TestEngineAttestations_CheckConstraint_RejectsBogusVerdict(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	rec := sampleDepthTypeRecord()
	rec.Verdict = "maybe"
	err := store.insertAttestation(ctx, rec)
	if err == nil {
		t.Fatal("CHECK constraint should reject unknown verdict at DB layer")
	}
}

// TestEngineAttestations_CheckConstraint_RejectsSelfCertSource — the
// schema-layer backstop for §12.2 / ADR-009. The runner layer
// already rejects self-cert, but an out-of-band SQL insert bypasses
// the runner.AttestationStore.Record validation. The CHECK
// constraint ensures the DB never holds a self-cert row.
func TestEngineAttestations_CheckConstraint_RejectsSelfCertSource(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	rec := sampleDepthTypeRecord()
	rec.AttestedByRole = rec.SourceRole // explicit self-cert against source
	err := store.insertAttestation(ctx, rec)
	if err == nil {
		t.Fatal("CHECK constraint should reject source-role self-cert at DB layer")
	}
}

func TestEngineAttestations_CheckConstraint_RejectsSelfCertTarget(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	rec := sampleDepthTypeRecord()
	rec.AttestedByRole = rec.TargetRole
	err := store.insertAttestation(ctx, rec)
	if err == nil {
		t.Fatal("CHECK constraint should reject target-role self-cert at DB layer")
	}
}

// TestEngineAttestations_CheckConstraint_EmptyRoles_SkipSelfCert verifies
// that records without recorded source/target roles (empty string)
// skip the schema self-cert check — those came from legacy paths
// that didn't record the audit identities.
func TestEngineAttestations_CheckConstraint_EmptyRoles_SkipSelfCert(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	rec := sampleDepthTypeRecord()
	rec.SourceRole = ""
	rec.TargetRole = ""
	rec.AttestedByRole = "anyone"
	if err := store.insertAttestation(ctx, rec); err != nil {
		t.Fatalf("empty source/target should bypass schema check; got %v", err)
	}
}

func TestEngineAttestations_InsertOnIdenticalContent_Idempotent(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	rec := sampleDepthTypeRecord()
	if err := store.insertAttestation(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := store.insertAttestation(ctx, rec); err != nil {
		t.Fatalf("re-insert of identical record should be idempotent; got %v", err)
	}
	n, err := store.CountAttestations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("CountAttestations = %d; want 1 (idempotent insert should not duplicate)", n)
	}
}

// TestEngineAttestations_InsertConflict_Rejected — records are
// immutable. Re-inserting under the same ID with different content
// errors with ErrAttestationConflict; the existing row stays
// intact.
func TestEngineAttestations_InsertConflict_Rejected(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	rec := sampleDepthTypeRecord()
	if err := store.insertAttestation(ctx, rec); err != nil {
		t.Fatal(err)
	}
	conflicting := rec
	conflicting.Verdict = runner.AttestationFail
	err := store.insertAttestation(ctx, conflicting)
	if err == nil || !errorsContains(err, "engine: attestation conflict") {
		t.Fatalf("expected ErrAttestationConflict; got %v", err)
	}
	// Existing row preserved unchanged.
	rows, err := store.listAttestations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("after rejected conflict, len = %d; want 1", len(rows))
	}
	if rows[0].Verdict != runner.AttestationPass {
		t.Fatalf("original verdict overwritten: %q; should still be pass", rows[0].Verdict)
	}
}

func errorsContains(err error, sub string) bool {
	if err == nil {
		return false
	}
	return contains(err.Error(), sub)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestEngineAttestations_CountAttestations_EmptyTable_ReturnsZero(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	n, err := store.CountAttestations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("CountAttestations on empty table = %d; want 0", n)
	}
}

// TestEngineAttestations_Journal_AttachAttestations_PersistsRecord
// covers the runner.AttestationStore → Journal → engine flow:
// recording in the runner store fires the observer; the journal
// consumer goroutine writes the row to sqlite.
func TestEngineAttestations_Journal_AttachAttestations_PersistsRecord(t *testing.T) {
	store := openTestStore(t)
	j := NewJournal(store, testLogger())
	t.Cleanup(j.Close)

	as := runner.NewAttestationStore()
	j.AttachAttestations(as)

	rec := sampleDepthTypeRecord()
	if err := as.Record(rec); err != nil {
		t.Fatal(err)
	}
	j.Flush()

	rows, err := store.listAttestations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("after Record + Flush, listAttestations returned %d rows; want 1", len(rows))
	}
	if rows[0] != rec {
		t.Errorf("persisted row %+v != recorded %+v", rows[0], rec)
	}
}

// TestEngineAttestations_Replay_PopulatesRunnerStore covers the
// reverse path: persisted rows replay back into a fresh
// runner.AttestationStore before any other replay target.
func TestEngineAttestations_Replay_PopulatesRunnerStore(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	// Persist three attestations directly (simulating a prior session).
	for _, rec := range []runner.AttestationRecord{
		sampleDepthTypeRecord(),
		sampleOnTheSpotRecord(),
	} {
		if err := store.insertAttestation(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}

	targets := ReplayTargets{
		Findings:        runner.NewFindingsStore(),
		Classifications: runner.NewClassificationsStore(),
		Grid:            runner.NewGrid(),
		Amendments:      runner.NewAmendmentQueue(),
		Attestations:    runner.NewAttestationStore(),
	}
	counts, err := Replay(ctx, store, targets)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if counts.Attestations != 2 {
		t.Errorf("counts.Attestations = %d; want 2", counts.Attestations)
	}
	if targets.Attestations.Len() != 2 {
		t.Errorf("runner store has %d; want 2", targets.Attestations.Len())
	}
	if got, ok := targets.Attestations.Lookup("att-A1-C1-v1"); !ok {
		t.Fatal("Lookup miss after replay")
	} else if got != sampleDepthTypeRecord() {
		t.Errorf("Lookup mismatch: %+v", got)
	}
}

// TestEngineAttestations_Replay_NilAttestationsTarget_Skipped pins
// the optional-target contract: callers (especially older tests)
// that don't supply an Attestations store get no attestation
// replay, no error.
func TestEngineAttestations_Replay_NilAttestationsTarget_Skipped(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	_ = store.insertAttestation(ctx, sampleDepthTypeRecord())

	targets := ReplayTargets{
		Findings:        runner.NewFindingsStore(),
		Classifications: runner.NewClassificationsStore(),
		Grid:            runner.NewGrid(),
		Amendments:      runner.NewAmendmentQueue(),
		// Attestations omitted (nil).
	}
	counts, err := Replay(ctx, store, targets)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if counts.Attestations != 0 {
		t.Errorf("nil Attestations target should yield 0 replay count; got %d", counts.Attestations)
	}
}
