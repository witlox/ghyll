package runner

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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

	// Tier 2 additions (ADR-016 + gate-1 remediation):

	// PassID links the verdict to its pass. Required on every
	// Tier 2-produced record; '' tolerated only for pre-Tier-2
	// rows (legacy load path). Empty PassID at Record-write time
	// rejects with ErrAttestationPassIDEmpty (gate-1 F-6).
	PassID string

	// Context + Stratum are stamped by the dispatcher at
	// record-construction time so EncodeAttestationPath is a
	// pure function (gate-1 F-2). For init arrows both are the
	// literal "init".
	Context string
	Stratum string

	// AdversaryRole is non-empty only when the verdict was
	// captured during an adversary-phase pass (gate-1 F-3). The
	// orchestrator stamps it. Empty otherwise. §12.2 self-cert
	// check extends to forbid AdversaryRole == SourceRole or
	// TargetRole and the literal "__".
	AdversaryRole string

	// Unit names the shape of the operator's evidence. Empty for
	// pre-Tier-2 rows.
	Unit VerdictUnit

	// UnitPayload is the typed payload per Unit. Marshaled to
	// UnitPayloadJSON at Record time.
	UnitPayload VerdictUnitPayload

	// UnitPayloadJSON is the canonical JSON serialization of
	// UnitPayload persisted on disk. The Record write path sets
	// this from UnitPayload via json.Marshal so callers can
	// either fill the typed struct or the JSON string (the typed
	// struct wins if both are set).
	UnitPayloadJSON string

	// HintJSON is the dispatcher-synthesized hint shown to the
	// operator in the verdict modal. Default '{}' (gate-1 F-25)
	// so the verifier's json.Unmarshal parses pre-Tier-2 rows.
	HintJSON string
}

// VerdictUnit names the shape of the operator's evidence
// (ADR-016 Part C).
type VerdictUnit string

const (
	VerdictUnitConfirm                  VerdictUnit = "confirm"
	VerdictUnitRecordLocationsInspected VerdictUnit = "record-locations-inspected"
	VerdictUnitWriteResidueNote         VerdictUnit = "write-residue-note"
)

// VerdictUnitPayload carries the typed shape per-unit. JSON-
// marshaled into AttestationRecord.UnitPayloadJSON at write time.
type VerdictUnitPayload struct {
	// Inspected is non-empty when Unit == record-locations-inspected.
	Inspected []string `json:"inspected,omitempty"`
	// Residue is non-empty when Unit == write-residue-note.
	Residue string `json:"residue,omitempty"`
}

// DefaultMaxResidueNoteBytes caps a write-residue-note payload at
// 16 KiB by default. Operator-tunable via the grid file's
// `residue-note-max-bytes` setting (bootstrap.GridDefaults).
const DefaultMaxResidueNoteBytes = 16 * 1024

// AttestationRecordsEqual compares two records field-by-field.
// Direct == fails because the Tier 2 VerdictUnitPayload contains
// a slice (Inspected). Order-sensitive comparison on Inspected
// is fine since the runtime always constructs the slice in a
// deterministic order.
func AttestationRecordsEqual(a, b AttestationRecord) bool {
	// HintJSON: empty string and "{}" both denote "no hint";
	// engine reads back "{}" by default. Normalize before compare
	// so idempotent re-Record from JSONL doesn't trip the conflict
	// probe (gate-2 CORR-A-2).
	hintA := a.HintJSON
	if hintA == "" {
		hintA = "{}"
	}
	hintB := b.HintJSON
	if hintB == "" {
		hintB = "{}"
	}
	if a.ID != b.ID || a.Kind != b.Kind || a.ArrowID != b.ArrowID ||
		a.ClauseID != b.ClauseID || a.OpID != b.OpID ||
		a.AttestedByRole != b.AttestedByRole ||
		a.SourceRole != b.SourceRole || a.TargetRole != b.TargetRole ||
		a.Verdict != b.Verdict || a.Reason != b.Reason ||
		a.Timestamp != b.Timestamp || a.GridVersion != b.GridVersion ||
		a.PassID != b.PassID || a.Context != b.Context ||
		a.Stratum != b.Stratum || a.AdversaryRole != b.AdversaryRole ||
		a.Unit != b.Unit || a.UnitPayloadJSON != b.UnitPayloadJSON ||
		hintA != hintB {
		return false
	}
	if len(a.UnitPayload.Inspected) != len(b.UnitPayload.Inspected) {
		return false
	}
	for i := range a.UnitPayload.Inspected {
		if a.UnitPayload.Inspected[i] != b.UnitPayload.Inspected[i] {
			return false
		}
	}
	return a.UnitPayload.Residue == b.UnitPayload.Residue
}

// ValidateUnitPayload returns nil iff payload satisfies the
// unit's required-field schema (ADR-016 Part C).
//
//	confirm                       — payload must be zero-value
//	record-locations-inspected    — Inspected must be non-empty
//	write-residue-note            — Residue non-empty + ≤ maxResidueBytes
//
// Called inside AttestationStore.Record before the primaryWriter
// fires. maxResidueBytes is the project-configured cap (default
// DefaultMaxResidueNoteBytes).
func ValidateUnitPayload(u VerdictUnit, p VerdictUnitPayload, maxResidueBytes int) error {
	if maxResidueBytes <= 0 {
		maxResidueBytes = DefaultMaxResidueNoteBytes
	}
	switch u {
	case VerdictUnitConfirm:
		if len(p.Inspected) != 0 || p.Residue != "" {
			return fmt.Errorf("%w: confirm payload must be empty", ErrVerdictUnitMissingField)
		}
		return nil
	case VerdictUnitRecordLocationsInspected:
		if len(p.Inspected) == 0 {
			return fmt.Errorf("%w: inspected", ErrVerdictInspectedEmpty)
		}
		return nil
	case VerdictUnitWriteResidueNote:
		if strings.TrimSpace(p.Residue) == "" {
			return fmt.Errorf("%w: residue", ErrVerdictUnitMissingField)
		}
		if len(p.Residue) > maxResidueBytes {
			return fmt.Errorf("%w: %d > %d", ErrVerdictResidueTooLong, len(p.Residue), maxResidueBytes)
		}
		return nil
	case "":
		// Tier 1 / legacy callers — Unit is optional today; only
		// Tier 2 modal flow requires it. Empty Unit means
		// "no unit-payload validation".
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrVerdictUnitInvalid, u)
	}
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

	// Tier 2 additions (ADR-016 + gate-1 remediation):

	// ErrVerdictUnitInvalid — VerdictUnit value not in the
	// known enum.
	ErrVerdictUnitInvalid = errors.New("verdict-unit-invalid")

	// ErrVerdictUnitMissingField — the Unit's required-field
	// schema isn't satisfied (e.g., confirm with extra fields,
	// write-residue-note with empty residue).
	ErrVerdictUnitMissingField = errors.New("verdict-unit-missing-field")

	// ErrVerdictResidueTooLong — write-residue-note payload
	// exceeds ResidueNoteMaxBytes (default 16 KiB).
	ErrVerdictResidueTooLong = errors.New("verdict-residue-too-long")

	// ErrVerdictInspectedEmpty — record-locations-inspected
	// without a non-empty Inspected list.
	ErrVerdictInspectedEmpty = errors.New("verdict-inspected-empty")

	// ErrAttestationPassIDEmpty — Tier 2 Record write path
	// rejects records with empty PassID (gate-1 F-6).
	ErrAttestationPassIDEmpty = errors.New("attestation-pass-id-empty")

	// ErrAttestationAggregateDivergence — verifier walked
	// both the tree and the flat aggregate and found a line
	// in one that's missing in the other (gate-1 F-1
	// follow-up). Audit-trail divergence.
	ErrAttestationAggregateDivergence = errors.New("attestation-aggregate-divergence")
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

	// residueNoteMaxBytes is the operator residue-note cap
	// applied by Record's ValidateUnitPayload call (gate-2
	// CORR-A-10 / F-24). atomic.Int64 so callers can re-tune
	// from a config-reload goroutine without taking s.mu.
	residueNoteMaxBytes atomic.Int64
}

// NewAttestationStore returns an empty store. The engine layer
// attaches an observer via Journal.AttachAttestations at session
// start so subsequent Record calls persist.
func NewAttestationStore() *AttestationStore {
	return &AttestationStore{byID: make(map[string]AttestationRecord)}
}

// SetResidueNoteMaxBytes configures the residue-note cap consumed
// by Record's ValidateUnitPayload. Zero/negative disables the cap
// (ValidateUnitPayload falls back to DefaultMaxResidueNoteBytes).
// Atomic so callers don't need s.mu.
func (s *AttestationStore) SetResidueNoteMaxBytes(n int) {
	s.residueNoteMaxBytes.Store(int64(n))
}

// ResidueNoteMaxBytes returns the configured cap; primarily for
// tests and diagnostics.
func (s *AttestationStore) ResidueNoteMaxBytes() int {
	return int(s.residueNoteMaxBytes.Load())
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
	// Gate-2 CORR-A-4/A-6/SEC-H-1: every Tier 2-aware Record call
	// path passes through full unit-payload + hint + PassID checks.
	// Pre-Tier-2 callers (recordReplay) bypass this via the
	// separate recordReplay entry point; legacy rows with empty
	// Unit/PassID still load via the lenient path.
	if err := validateAttestationTier2(rec, s.ResidueNoteMaxBytes()); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.byID[rec.ID]; ok {
		if AttestationRecordsEqual(existing, rec) {
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
		if AttestationRecordsEqual(existing, rec) {
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
	// Tier 2 (gate-1 F-3): adversary_role MUST NOT equal
	// source/target. Only fires when AdversaryRole is set
	// (adversary-phase records).
	adv := strings.TrimSpace(rec.AdversaryRole)
	if adv != "" {
		if src != "" && strings.EqualFold(adv, src) {
			return fmt.Errorf("%w: adversary-role %q equals source-role", ErrAttestationSelfCert, adv)
		}
		if tgt != "" && strings.EqualFold(adv, tgt) {
			return fmt.Errorf("%w: adversary-role %q equals target-role", ErrAttestationSelfCert, adv)
		}
	}
	return nil
}

// validateAttestationTier2 runs the Tier 2-specific record checks
// the modal driver / dispatcher contracts mandate. Separated from
// validateAttestation so recordReplay (legacy load path) can skip
// these — pre-Tier-2 rows carry empty PassID/Unit by design.
//
// Checks (gate-2 CORR-A-4 / A-6 / SEC-H-1):
//
//   - PassID non-empty (the tree path encoder already enforces;
//     this catches /attest CLI + future writer paths)
//   - AdversaryRole does NOT contain the reserved "__" separator
//   - Unit, if non-empty, is in the enum
//   - UnitPayload (typed) passes ValidateUnitPayload against the
//     configured residue cap
//   - HintJSON, if non-empty and != "{}", parses as a JSON object
func validateAttestationTier2(rec AttestationRecord, residueMaxBytes int) error {
	if strings.TrimSpace(rec.PassID) == "" {
		return ErrAttestationPassIDEmpty
	}
	adv := strings.TrimSpace(rec.AdversaryRole)
	if adv != "" && strings.Contains(adv, "__") {
		return fmt.Errorf(`%w: adversary-role must not contain "__"`, ErrAttestationSelfCert)
	}
	if rec.Unit != "" {
		switch rec.Unit {
		case VerdictUnitConfirm, VerdictUnitRecordLocationsInspected, VerdictUnitWriteResidueNote:
			// OK
		default:
			return fmt.Errorf("%w: %q", ErrVerdictUnitInvalid, rec.Unit)
		}
		if err := ValidateUnitPayload(rec.Unit, rec.UnitPayload, residueMaxBytes); err != nil {
			return err
		}
	}
	if rec.HintJSON != "" && rec.HintJSON != "{}" {
		var probe map[string]any
		if err := json.Unmarshal([]byte(rec.HintJSON), &probe); err != nil {
			return fmt.Errorf("attestation hint_json malformed: %w", err)
		}
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
			attRec := jsonlRecordToAttRec(rec)
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

// LoadFromTree walks <root>/v*/<ctx>/stratum-*/<role-pair>/<pass-id>.jsonl
// and unions every JSONL line into byID via recordReplay. Per
// ADR-016 Part B (Tier 2): the tree is the authoritative load
// surface, replacing Tier 1's LoadFromJSONL of the flat file.
//
// Returns the same LoadResult shape as LoadFromJSONL:
//   - res.Loaded: count of valid records loaded.
//   - res.Truncated: true if ANY per-pass file ends with a
//     partial trailing line. The caller (session.openEngine)
//     calls AttestationTreeWriter.TruncateTrailingPartialAll on
//     a truncated=true return.
//   - res.LastCompleteOffset: not meaningful for trees (per-file
//     offsets vary); always 0.
//   - err:
//     missing tree + engineHasRows=true → ErrAttestationAuditLost
//     missing tree + engineHasRows=false → (0, false, nil) fresh
//     unreadable file mid-walk → ErrAttestationAuditLost
//     mid-file corrupt line → ErrAttestationAuditLost
//
// Trailing-truncation tolerance mirrors LoadFromJSONL's lenient
// mode: a partial last line stops the per-file read at the last
// complete record without erroring.
func (s *AttestationStore) LoadFromTree(root string, engineHasRows bool) (loaded int, truncated bool, err error) {
	stat, statErr := os.Stat(root)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			if engineHasRows {
				return 0, false, fmt.Errorf("%w: tree %s missing but engine has rows", ErrAttestationAuditLost, root)
			}
			return 0, false, nil // fresh project
		}
		return 0, false, fmt.Errorf("%w: stat %s: %w", ErrAttestationAuditLost, root, statErr)
	}
	if !stat.IsDir() {
		return 0, false, fmt.Errorf("%w: %s is not a directory", ErrAttestationAuditLost, root)
	}

	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		fileLoaded, fileTruncated, loadErr := s.loadOneTreeFile(path)
		if loadErr != nil {
			return loadErr
		}
		loaded += fileLoaded
		if fileTruncated {
			truncated = true
		}
		return nil
	})
	if walkErr != nil {
		return loaded, truncated, fmt.Errorf("%w: walk %s: %w", ErrAttestationAuditLost, root, walkErr)
	}
	return loaded, truncated, nil
}

// loadOneTreeFile reads a single per-pass JSONL file and feeds
// each valid record through recordReplay. Mirrors LoadFromJSONL's
// per-line logic.
func (s *AttestationStore) loadOneTreeFile(path string) (loaded int, truncated bool, err error) {
	f, openErr := os.Open(path)
	if openErr != nil {
		return 0, false, fmt.Errorf("open %s: %w", path, openErr)
	}
	defer func() { _ = f.Close() }()

	reader := bufio.NewReader(f)
	lineNo := 0
	for {
		lineBytes, readErr := reader.ReadBytes('\n')
		if len(lineBytes) > 0 {
			lineNo++
			// Partial trailing line: ends without newline AND EOF.
			if readErr == io.EOF && lineBytes[len(lineBytes)-1] != '\n' {
				truncated = true
				break
			}
			line := strings.TrimSpace(string(lineBytes))
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			var rec jsonlRecord
			if jerr := json.Unmarshal([]byte(line), &rec); jerr != nil {
				return loaded, false, fmt.Errorf("%s line %d: %w", path, lineNo, jerr)
			}
			attRec := jsonlRecordToAttRec(rec)
			if rerr := s.recordReplay(attRec); rerr != nil {
				return loaded, false, fmt.Errorf("%s line %d: %w", path, lineNo, rerr)
			}
			loaded++
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return loaded, truncated, nil
			}
			return loaded, false, fmt.Errorf("read %s: %w", path, readErr)
		}
	}
	return loaded, truncated, nil
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
