package runner

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// AttestationKind names the two attestation kinds owned by this
// store per ADR-010. Clause-verdict transitions during running
// passes are NOT here — those stay in FindingsStore.
type AttestationKind string

const (
	// AttestationKindDepthType — operator confirms a clause's
	// DepthType / MinDepthTier assignment (init pass). Per-clause;
	// ClauseID is populated.
	AttestationKindDepthType AttestationKind = "depth-type"

	// AttestationKindOnTheSpot — operator approves an on-the-spot
	// arrow definition (§12.2 / ADR-009). Per-arrow; ClauseID is
	// empty.
	AttestationKindOnTheSpot AttestationKind = "on-the-spot"
)

// AttestationVerdict is the operator's verdict on the attestation.
type AttestationVerdict string

const (
	AttestationPass              AttestationVerdict = "pass"
	AttestationFail              AttestationVerdict = "fail"
	AttestationInsufficientBasis AttestationVerdict = "insufficient-basis"
)

// AttestationRecord is one immutable operator-verdict row.
//
// IDs are deterministic per ADR-010 so a clause's
// DepthTypeAttestationRef can be computed at init before the
// record itself is persisted by the journal consumer goroutine.
// See ComputeAttestationID.
type AttestationRecord struct {
	ID             string
	Kind           AttestationKind
	ArrowID        string
	ClauseID       string // empty iff Kind == OnTheSpot
	OpID           string
	AttestedByRole string
	SourceRole     string // arrow's source role (recorded for §12.2 audit)
	TargetRole     string // arrow's target role (recorded for §12.2 audit)
	Verdict        AttestationVerdict
	Reason         string
	Timestamp      int64 // unix nanos
	GridVersion    uint64
}

// AttestationEventKind names a mutation type. Only Record exists
// today — attestations are immutable once written. The enum is
// future-proofing for invalidation or compensation events.
type AttestationEventKind string

const (
	AttestationEventRecord AttestationEventKind = "record"
)

// AttestationEvent is the observer payload.
type AttestationEvent struct {
	Kind   AttestationEventKind
	Record AttestationRecord
}

// AttestationObserver fires on every mutation. The engine attaches
// one observer at session start so the journal persists records.
// Observers MUST be fast (they run under the store's write lock).
type AttestationObserver func(event AttestationEvent)

// Attestation-store errors.
var (
	ErrAttestationIDEmpty             = errors.New("attestation-id-empty")
	ErrAttestationArrowEmpty          = errors.New("attestation-arrow-id-empty")
	ErrAttestationOpIDEmpty           = errors.New("attestation-op-id-empty")
	ErrAttestationAttestedByRoleEmpty = errors.New("attestation-attested-by-role-empty")
	ErrAttestationKindInvalid         = errors.New("attestation-kind-invalid")
	ErrAttestationVerdictInvalid      = errors.New("attestation-verdict-invalid")

	// ErrAttestationSelfCert is returned when AttestedByRole equals
	// SourceRole or TargetRole (case-insensitive, trimmed). The
	// store enforces §12.2 / ADR-009 at the persistence boundary so
	// out-of-band recording paths can't bypass the constraint.
	ErrAttestationSelfCert = errors.New("attestation-self-cert-forbidden")

	// ErrAttestationDepthTypeClauseEmpty — depth-type attestations
	// MUST carry a ClauseID; the CHECK constraint in the engine
	// schema mirrors this.
	ErrAttestationDepthTypeClauseEmpty = errors.New("attestation-depth-type-requires-clause-id")

	// ErrAttestationOnTheSpotClauseSet — on-the-spot attestations
	// MUST NOT carry a ClauseID.
	ErrAttestationOnTheSpotClauseSet = errors.New("attestation-on-the-spot-rejects-clause-id")

	// ErrAttestationDuplicate — same ID already recorded. Records
	// are immutable; idempotent re-record is silent on identical
	// content, errors on conflict.
	ErrAttestationDuplicate = errors.New("attestation-duplicate")
)

// AttestationStore is the in-memory cache of attestation records
// (ADR-010). Pattern matches FindingsStore: append-only mutations
// under a write lock, observer fanout for journaling. Lookup is
// the hot-path API (clause evaluation resolves
// DepthTypeAttestationRef through here).
//
// Records are immutable once written. Re-Record with identical
// content is a silent success; re-Record with conflicting content
// returns ErrAttestationDuplicate.
type AttestationStore struct {
	mu        sync.RWMutex
	byID      map[string]AttestationRecord
	version   uint64
	observers []AttestationObserver
}

// NewAttestationStore returns an empty store. The engine layer
// attaches an observer via Journal.AttachAttestations at session
// start so subsequent Record calls persist.
func NewAttestationStore() *AttestationStore {
	return &AttestationStore{byID: make(map[string]AttestationRecord)}
}

// Observe registers an observer to be invoked on every mutation.
// The engine attaches one observer at session start.
func (s *AttestationStore) Observe(fn AttestationObserver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observers = append(s.observers, fn)
}

// emit fires all observers under the existing write lock. Caller
// MUST hold s.mu (write lock).
func (s *AttestationStore) emit(e AttestationEvent) {
	for _, ob := range s.observers {
		ob(e)
	}
}

// Record validates and persists an attestation. Enforces §12.2 /
// ADR-009 (no self-cert) and the depth-type/on-the-spot ClauseID
// constraints. Idempotent: re-Record with byte-identical content
// is silent; re-Record with conflicting content errors.
func (s *AttestationStore) Record(rec AttestationRecord) error {
	if err := validateAttestation(rec); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.byID[rec.ID]; ok {
		if existing == rec {
			return nil // idempotent — same content, no-op
		}
		return fmt.Errorf("%w: id=%s", ErrAttestationDuplicate, rec.ID)
	}
	s.byID[rec.ID] = rec
	s.version++
	s.emit(AttestationEvent{Kind: AttestationEventRecord, Record: rec})
	return nil
}

// Lookup returns the record with the given ID and a presence flag.
// (zero, false) when no record exists with that ID. Hot-path API
// used to resolve a Clause's DepthTypeAttestationRef.
//
// Verdict validation (e.g., "the looked-up record must have
// Verdict == AttestationPass before a pass can proceed") is the
// CALLER's responsibility, not the store's. The dispatch layer
// performs verdict checks before invoking Runner.Evaluate; the
// store is only the resolution surface.
func (s *AttestationStore) Lookup(id string) (AttestationRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.byID[id]
	return rec, ok
}

// All returns a snapshot of every recorded attestation. Insertion
// order is NOT guaranteed; callers wanting deterministic order
// should sort by ID or Timestamp themselves.
func (s *AttestationStore) All() []AttestationRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]AttestationRecord, 0, len(s.byID))
	for _, r := range s.byID {
		out = append(out, r)
	}
	return out
}

// ForArrow returns every attestation recorded against the given
// arrow ID. Snapshot — caller owns the returned slice. Used by
// the engine status CLI and the JSONL writer.
func (s *AttestationStore) ForArrow(arrowID string) []AttestationRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []AttestationRecord
	for _, r := range s.byID {
		if r.ArrowID == arrowID {
			out = append(out, r)
		}
	}
	return out
}

// Version returns the snapshot version. Incremented on every
// content-changing Record; identical re-Record (idempotent) does
// NOT bump the version. Useful for ensuring replay starts against
// an empty store (engine.ensureEmpty checks Version() == 0).
func (s *AttestationStore) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// Len returns the number of recorded attestations. Useful for
// tests and metrics.
func (s *AttestationStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byID)
}

// validateAttestation runs all the structural and §12.2 checks
// outside the lock so the critical section stays small.
//
// Orphan tolerance: validateAttestation deliberately does NOT
// cross-reference ArrowID against the grid. Replay populates the
// AttestationStore BEFORE the grid is loaded (ADR-010 ordering),
// so a grid check would always fail at replay. Stale attestations
// for arrows that no longer exist are silently accepted; the
// engine status CLI surfaces them via orphan detection as a
// follow-up enhancement.
func validateAttestation(rec AttestationRecord) error {
	if strings.TrimSpace(rec.ID) == "" {
		return ErrAttestationIDEmpty
	}
	if strings.TrimSpace(rec.ArrowID) == "" {
		return ErrAttestationArrowEmpty
	}
	if strings.TrimSpace(rec.OpID) == "" {
		return ErrAttestationOpIDEmpty
	}
	if strings.TrimSpace(rec.AttestedByRole) == "" {
		return ErrAttestationAttestedByRoleEmpty
	}
	switch rec.Kind {
	case AttestationKindDepthType:
		if strings.TrimSpace(rec.ClauseID) == "" {
			return ErrAttestationDepthTypeClauseEmpty
		}
	case AttestationKindOnTheSpot:
		if strings.TrimSpace(rec.ClauseID) != "" {
			return ErrAttestationOnTheSpotClauseSet
		}
	default:
		return fmt.Errorf("%w: %q", ErrAttestationKindInvalid, rec.Kind)
	}
	switch rec.Verdict {
	case AttestationPass, AttestationFail, AttestationInsufficientBasis:
		// OK
	default:
		return fmt.Errorf("%w: %q", ErrAttestationVerdictInvalid, rec.Verdict)
	}
	// §12.2 / ADR-009: AttestedByRole MUST NOT equal SourceRole or
	// TargetRole. Case-insensitive, trimmed. Empty Source/Target
	// roles are allowed (some callers may not know them yet); the
	// check fires only when they're present.
	att := strings.TrimSpace(rec.AttestedByRole)
	src := strings.TrimSpace(rec.SourceRole)
	tgt := strings.TrimSpace(rec.TargetRole)
	if src != "" && strings.EqualFold(att, src) {
		return fmt.Errorf("%w: attested-by-role %q equals source-role", ErrAttestationSelfCert, att)
	}
	if tgt != "" && strings.EqualFold(att, tgt) {
		return fmt.Errorf("%w: attested-by-role %q equals target-role", ErrAttestationSelfCert, att)
	}
	return nil
}

// ComputeAttestationID returns the deterministic ID for an
// attestation per ADR-010. Format:
//
//	depth-type:  att-<arrow_id>-<clause_id>-v<grid_version>
//	on-the-spot: att-<arrow_id>-v<grid_version>
//
// IDs are stable across init and replay so a Clause's
// DepthTypeAttestationRef can be computed before the
// AttestationRecord itself is persisted.
//
// Callers should sanitize arrow/clause IDs to avoid embedding
// dashes that would make the format ambiguous; the runner's
// existing identity validation already trims and rejects empty
// IDs.
func ComputeAttestationID(kind AttestationKind, arrowID, clauseID string, gridVersion uint64) string {
	switch kind {
	case AttestationKindDepthType:
		return fmt.Sprintf("att-%s-%s-v%d", arrowID, clauseID, gridVersion)
	case AttestationKindOnTheSpot:
		return fmt.Sprintf("att-%s-v%d", arrowID, gridVersion)
	default:
		return ""
	}
}
