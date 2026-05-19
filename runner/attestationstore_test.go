package runner

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

// validDepthTypeRec is a fixture for the happy-path tests.
func validDepthTypeRec() AttestationRecord {
	return AttestationRecord{
		ID:             "att-A1-C1-v1",
		Kind:           AttestationKindDepthType,
		ArrowID:        "A1",
		ClauseID:       "C1",
		OpID:           "op-alice",
		AttestedByRole: "implementer",
		SourceRole:     "analyst",
		TargetRole:     "architect",
		Verdict:        AttestationPass,
		Reason:         "verified",
		Timestamp:      1747663200000000000,
		GridVersion:    1,
	}
}

func validOnTheSpotRec() AttestationRecord {
	return AttestationRecord{
		ID:             "att-A2-v3",
		Kind:           AttestationKindOnTheSpot,
		ArrowID:        "A2",
		OpID:           "op-bob",
		AttestedByRole: "integrator",
		SourceRole:     "analyst",
		TargetRole:     "architect",
		Verdict:        AttestationPass,
		Timestamp:      1747663200000000000,
		GridVersion:    3,
	}
}

func TestScenario_AttestationStore_RecordAndLookup_Roundtrip(t *testing.T) {
	s := NewAttestationStore()
	rec := validDepthTypeRec()
	if err := s.Record(rec); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got, ok := s.Lookup(rec.ID)
	if !ok {
		t.Fatal("Lookup miss after Record")
	}
	if got != rec {
		t.Fatalf("Lookup returned %+v; want %+v", got, rec)
	}
	if s.Len() != 1 || s.Version() != 1 {
		t.Fatalf("Len=%d Version=%d; want 1,1", s.Len(), s.Version())
	}
}

func TestScenario_AttestationStore_IdempotentReRecord_SameContent(t *testing.T) {
	s := NewAttestationStore()
	rec := validDepthTypeRec()
	if err := s.Record(rec); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(rec); err != nil {
		t.Fatalf("re-Record with identical content should be silent; got %v", err)
	}
	if s.Version() != 1 {
		t.Fatalf("Version=%d; want 1 (idempotent re-Record should NOT bump)", s.Version())
	}
}

func TestScenario_AttestationStore_ConflictingReRecord_Errors(t *testing.T) {
	s := NewAttestationStore()
	rec := validDepthTypeRec()
	if err := s.Record(rec); err != nil {
		t.Fatal(err)
	}
	conflicting := rec
	conflicting.Verdict = AttestationFail
	err := s.Record(conflicting)
	if !errors.Is(err, ErrAttestationDuplicate) {
		t.Fatalf("expected ErrAttestationDuplicate; got %v", err)
	}
}

func TestScenario_AttestationStore_SelfCert_SourceRoleRejected(t *testing.T) {
	s := NewAttestationStore()
	rec := validDepthTypeRec()
	rec.AttestedByRole = "analyst" // == SourceRole
	err := s.Record(rec)
	if !errors.Is(err, ErrAttestationSelfCert) {
		t.Fatalf("expected ErrAttestationSelfCert; got %v", err)
	}
}

func TestScenario_AttestationStore_SelfCert_TargetRoleRejected(t *testing.T) {
	s := NewAttestationStore()
	rec := validDepthTypeRec()
	rec.AttestedByRole = "architect" // == TargetRole
	err := s.Record(rec)
	if !errors.Is(err, ErrAttestationSelfCert) {
		t.Fatalf("expected ErrAttestationSelfCert; got %v", err)
	}
}

func TestScenario_AttestationStore_SelfCert_CaseInsensitive(t *testing.T) {
	s := NewAttestationStore()
	rec := validDepthTypeRec()
	rec.AttestedByRole = "ANALYST" // case variant of SourceRole
	if err := s.Record(rec); !errors.Is(err, ErrAttestationSelfCert) {
		t.Fatalf("expected ErrAttestationSelfCert under case-variation; got %v", err)
	}
}

func TestScenario_AttestationStore_SelfCert_TrimmedComparison(t *testing.T) {
	s := NewAttestationStore()
	rec := validDepthTypeRec()
	rec.AttestedByRole = "  architect  " // whitespace around TargetRole
	if err := s.Record(rec); !errors.Is(err, ErrAttestationSelfCert) {
		t.Fatalf("expected ErrAttestationSelfCert after trim; got %v", err)
	}
}

func TestScenario_AttestationStore_DepthType_RequiresClauseID(t *testing.T) {
	s := NewAttestationStore()
	rec := validDepthTypeRec()
	rec.ClauseID = ""
	err := s.Record(rec)
	if !errors.Is(err, ErrAttestationDepthTypeClauseEmpty) {
		t.Fatalf("expected ErrAttestationDepthTypeClauseEmpty; got %v", err)
	}
}

func TestScenario_AttestationStore_OnTheSpot_RejectsClauseID(t *testing.T) {
	s := NewAttestationStore()
	rec := validOnTheSpotRec()
	rec.ClauseID = "C1" // not allowed for on-the-spot
	err := s.Record(rec)
	if !errors.Is(err, ErrAttestationOnTheSpotClauseSet) {
		t.Fatalf("expected ErrAttestationOnTheSpotClauseSet; got %v", err)
	}
}

func TestScenario_AttestationStore_OnTheSpot_HappyPath(t *testing.T) {
	s := NewAttestationStore()
	rec := validOnTheSpotRec()
	if err := s.Record(rec); err != nil {
		t.Fatalf("on-the-spot Record: %v", err)
	}
	got, ok := s.Lookup(rec.ID)
	if !ok || got != rec {
		t.Fatalf("Lookup returned %+v ok=%v; want %+v true", got, ok, rec)
	}
}

func TestScenario_AttestationStore_RequiredFieldsValidated(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*AttestationRecord)
		want error
	}{
		{"empty ID", func(r *AttestationRecord) { r.ID = "" }, ErrAttestationIDEmpty},
		{"empty arrow", func(r *AttestationRecord) { r.ArrowID = "" }, ErrAttestationArrowEmpty},
		{"empty op-id", func(r *AttestationRecord) { r.OpID = "" }, ErrAttestationOpIDEmpty},
		{"empty role", func(r *AttestationRecord) { r.AttestedByRole = "" }, ErrAttestationAttestedByRoleEmpty},
		{"unknown kind", func(r *AttestationRecord) { r.Kind = AttestationKind("bogus") }, ErrAttestationKindInvalid},
		{"unknown verdict", func(r *AttestationRecord) { r.Verdict = AttestationVerdict("bogus") }, ErrAttestationVerdictInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := validDepthTypeRec()
			c.mod(&rec)
			if err := NewAttestationStore().Record(rec); !errors.Is(err, c.want) {
				t.Fatalf("got %v; want %v", err, c.want)
			}
		})
	}
}

func TestScenario_AttestationStore_LookupMiss(t *testing.T) {
	s := NewAttestationStore()
	_, ok := s.Lookup("does-not-exist")
	if ok {
		t.Fatal("Lookup should report miss")
	}
}

func TestScenario_AttestationStore_ForArrow_FiltersByArrowID(t *testing.T) {
	s := NewAttestationStore()
	rec1 := validDepthTypeRec()
	rec2 := validDepthTypeRec()
	rec2.ID = "att-A1-C2-v1"
	rec2.ClauseID = "C2"
	rec3 := validDepthTypeRec()
	rec3.ID = "att-A2-C1-v1"
	rec3.ArrowID = "A2"
	for _, r := range []AttestationRecord{rec1, rec2, rec3} {
		if err := s.Record(r); err != nil {
			t.Fatal(err)
		}
	}
	got := s.ForArrow("A1")
	if len(got) != 2 {
		t.Fatalf("ForArrow A1 returned %d records; want 2", len(got))
	}
}

func TestScenario_AttestationStore_Observer_FiresOnRecord(t *testing.T) {
	s := NewAttestationStore()
	var observed []AttestationEvent
	var obsMu sync.Mutex
	s.Observe(func(e AttestationEvent) {
		obsMu.Lock()
		defer obsMu.Unlock()
		observed = append(observed, e)
	})
	rec := validDepthTypeRec()
	if err := s.Record(rec); err != nil {
		t.Fatal(err)
	}
	if len(observed) != 1 {
		t.Fatalf("observer fired %d times; want 1", len(observed))
	}
	if observed[0].Kind != AttestationEventRecord {
		t.Fatalf("event kind = %q; want %q", observed[0].Kind, AttestationEventRecord)
	}
	if observed[0].Record != rec {
		t.Fatalf("event record %+v != recorded %+v", observed[0].Record, rec)
	}
}

func TestScenario_AttestationStore_Observer_DoesNotFireOnIdempotentReRecord(t *testing.T) {
	s := NewAttestationStore()
	calls := 0
	s.Observe(func(e AttestationEvent) { calls++ })
	rec := validDepthTypeRec()
	_ = s.Record(rec)
	_ = s.Record(rec) // identical re-record — no-op, no event
	if calls != 1 {
		t.Fatalf("observer called %d times; want 1 (idempotent re-Record must not re-emit)", calls)
	}
}

func TestScenario_AttestationStore_ComputeAttestationID_DepthType(t *testing.T) {
	id := ComputeAttestationID(AttestationKindDepthType, "A1", "C1", 7)
	if id != "att-A1-C1-v7" {
		t.Fatalf("got %q; want att-A1-C1-v7", id)
	}
}

func TestScenario_AttestationStore_ComputeAttestationID_OnTheSpot(t *testing.T) {
	id := ComputeAttestationID(AttestationKindOnTheSpot, "A2", "ignored", 3)
	if id != "att-A2-v3" {
		t.Fatalf("got %q; want att-A2-v3", id)
	}
}

func TestScenario_AttestationStore_ComputeAttestationID_UnknownKindEmpty(t *testing.T) {
	id := ComputeAttestationID(AttestationKind("bogus"), "A1", "C1", 1)
	if id != "" {
		t.Fatalf("unknown kind should return empty; got %q", id)
	}
}

// TestScenario_AttestationStore_DeterministicIDs_CanBeForwardReferenced
// pins ADR-010's claim: a clause's DepthTypeAttestationRef can be
// computed before the attestation itself exists in the store.
// Looking up the precomputed ID returns (zero, false) until Record
// arrives; after Record it resolves.
func TestScenario_AttestationStore_DeterministicIDs_CanBeForwardReferenced(t *testing.T) {
	s := NewAttestationStore()
	ref := ComputeAttestationID(AttestationKindDepthType, "A1", "C1", 1)
	if _, ok := s.Lookup(ref); ok {
		t.Fatal("Lookup of unrecorded ref should miss")
	}
	rec := validDepthTypeRec()
	if rec.ID != ref {
		t.Fatalf("fixture ID %q != computed ref %q — fixture out of sync with ComputeAttestationID",
			rec.ID, ref)
	}
	if err := s.Record(rec); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.Lookup(ref); !ok || got.ID != ref {
		t.Fatalf("after Record, Lookup(ref) should return the record; got %+v ok=%v",
			got, ok)
	}
}

func TestScenario_AttestationStore_BusyErrorIncludesIDs(t *testing.T) {
	s := NewAttestationStore()
	rec := validDepthTypeRec()
	_ = s.Record(rec)
	conflicting := rec
	conflicting.OpID = "op-different"
	err := s.Record(conflicting)
	if !strings.Contains(err.Error(), rec.ID) {
		t.Fatalf("duplicate error %q should name the ID %q", err, rec.ID)
	}
}
