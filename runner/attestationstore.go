package runner

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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

	// ErrAttestationAuditWriteFailed — the inline JSONL audit write
	// (the primary writer, ADR-015 Part C) failed. The in-memory
	// map is unchanged; downstream observers (engine journal, tree
	// writer) never fired.
	ErrAttestationAuditWriteFailed = errors.New("attestation-audit-write-failed")

	// ErrAttestationAuditLost — the JSONL file is missing or
	// unreadable AND the engine table has attestation rows.
	// Operator must restore from backup or run an explicit
	// rebuild-from-engine path.
	ErrAttestationAuditLost = errors.New("attestation-audit-lost")
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

	// primaryWriter (ADR-015 Part C / Tier 1 F-1, F-2) is the
	// JSONL audit writer. When set, Record calls it INLINE before
	// mutating byID; if it returns an error, Record returns
	// ErrAttestationAuditWriteFailed and neither byID nor the
	// downstream observers fire. This makes the JSONL the source
	// of truth for attestations (inverting the original ADR-010
	// framing).
	primaryWriter func(AttestationRecord) error
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

// SetPrimaryWriter registers the inline-blocking audit writer
// per ADR-015 Part C. Record calls it FIRST; if it returns an
// error, Record returns ErrAttestationAuditWriteFailed and
// nothing else fires. Setting nil disables the inline-write
// invariant (used by tests that don't exercise the JSONL path).
func (s *AttestationStore) SetPrimaryWriter(fn func(AttestationRecord) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.primaryWriter = fn
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
//
// ADR-015 Part C: when a primaryWriter is set, Record calls it
// inline BEFORE mutating byID. A primaryWriter failure returns
// ErrAttestationAuditWriteFailed and aborts the Record call —
// neither byID nor the downstream observers fire. This makes
// the JSONL audit trail the source of truth.
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
	if s.primaryWriter != nil {
		if err := s.primaryWriter(rec); err != nil {
			return fmt.Errorf("%w: %w", ErrAttestationAuditWriteFailed, err)
		}
	}
	s.byID[rec.ID] = rec
	s.version++
	s.emit(AttestationEvent{Kind: AttestationEventRecord, Record: rec})
	return nil
}

// recordReplay populates byID + version WITHOUT firing the
// primaryWriter or observers. Used by LoadFromJSONL: the JSONL
// IS the source of truth, so re-loading it must not echo back to
// the same writer (would duplicate every line). Observers also
// skip because the engine journal isn't attached at replay time.
//
// Validation still runs so a corrupt line is rejected; the
// caller decides whether one bad line is fatal or skippable.
func (s *AttestationStore) recordReplay(rec AttestationRecord) error {
	if err := validateAttestation(rec); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.byID[rec.ID]; ok {
		if existing == rec {
			return nil
		}
		return fmt.Errorf("%w: id=%s", ErrAttestationDuplicate, rec.ID)
	}
	s.byID[rec.ID] = rec
	s.version++
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

// LoadFromJSONL reads path line-by-line and populates byID via
// recordReplay (no observer fanout, no primary writer recursion).
// Per ADR-015 Part C this is how the engine "catches up" from the
// authoritative JSONL audit trail at session start.
//
// Returns:
//   - loaded: count of valid records loaded.
//   - truncated: true iff the file ends with a partial trailing
//     line (no terminating newline on the last record); load
//     stopped at the last complete record. Operator should call
//     AttestationJSONLWriter.TruncateAt(lastCompleteOffset) on
//     the next successful Record so the bad bytes are overwritten.
//   - err:
//   - nil for the lenient cases above (missing+engine-empty,
//     trailing truncation).
//   - ErrAttestationAuditLost when the file is missing AND
//     engineHasRows is true.
//   - ErrAttestationAuditLost when a mid-file line is malformed
//     (JSON parse error or validation failure on a non-trailing
//     line) — that's data corruption, not truncation.
//   - The underlying os/io error for other access failures.
//
// engineHasRows distinguishes the fresh-project case (count=0 →
// empty stream OK) from the broken-audit case (count>0 → fatal).
// session.Open passes Store.CountAttestations(ctx) > 0.
func (s *AttestationStore) LoadFromJSONL(path string, engineHasRows bool) (loaded int, truncated bool, err error) {
	f, openErr := os.Open(path)
	if openErr != nil {
		if errors.Is(openErr, os.ErrNotExist) {
			if engineHasRows {
				return 0, false, fmt.Errorf("%w: %s missing but engine has rows", ErrAttestationAuditLost, path)
			}
			return 0, false, nil // fresh project
		}
		return 0, false, fmt.Errorf("%w: open %s: %w", ErrAttestationAuditLost, path, openErr)
	}
	defer func() { _ = f.Close() }()

	reader := bufio.NewReader(f)
	var (
		offset      int64
		lastGood    int64
		lineNo      int
		truncatedOK bool
	)
	for {
		lineBytes, readErr := reader.ReadBytes('\n')
		if len(lineBytes) > 0 {
			lineNo++
			// If readErr == io.EOF and the buffer has bytes BUT no
			// trailing newline, the file ended with a partial record.
			if readErr == io.EOF && (len(lineBytes) == 0 || lineBytes[len(lineBytes)-1] != '\n') {
				truncatedOK = true
				break
			}
			line := strings.TrimSpace(string(lineBytes))
			if line == "" || strings.HasPrefix(line, "#") {
				offset += int64(len(lineBytes))
				lastGood = offset
				continue
			}
			var rec jsonlRecord
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				return loaded, false, fmt.Errorf("%w: line %d: %w", ErrAttestationAuditLost, lineNo, err)
			}
			attRec := AttestationRecord{
				ID:             rec.ID,
				Kind:           AttestationKind(rec.Kind),
				ArrowID:        rec.ArrowID,
				ClauseID:       rec.ClauseID,
				OpID:           rec.OpID,
				AttestedByRole: rec.AttestedByRole,
				SourceRole:     rec.SourceRole,
				TargetRole:     rec.TargetRole,
				Verdict:        AttestationVerdict(rec.Verdict),
				Reason:         rec.Reason,
				Timestamp:      rec.Timestamp,
				GridVersion:    rec.GridVersion,
			}
			if err := s.recordReplay(attRec); err != nil {
				return loaded, false, fmt.Errorf("%w: line %d: %w", ErrAttestationAuditLost, lineNo, err)
			}
			loaded++
			offset += int64(len(lineBytes))
			lastGood = offset
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return loaded, false, fmt.Errorf("read %s: %w", path, readErr)
		}
	}
	_ = lastGood // reserved for future TruncateAt offset hand-off via session.Open
	return loaded, truncatedOK, nil
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
