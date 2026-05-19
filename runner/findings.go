package runner

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Findings model. Per gates.md §7.3: a finding lives on the arrow
// (not on a clause); it has an id, a type, a severity, and a
// status. Lifecycle (post validation-pass-4 F6 reconciliation):
//
//	open           → running | resolved | accepted-risk
//	running        → resolved | accepted-risk | unevaluated | open
//	resolved       → open (reopen on regression)
//	accepted-risk  → open (operator amendment; see TransitionWithReason)
//	unevaluated    → running | open
//
// FindingType is the integrator-introduced enum
// {local-bug, missing-cross-context-spec, ...} — extensible per
// project but constrained by cardinality-check on integrator G4.
// The runner doesn't enforce the closed set; the gate clauses do.

// FindingType is a free-form string. Operators declare the project's
// enum at init (integrator role file or amendment) and a
// cardinality-check clause asserts values stay inside the enum.
type FindingType string

// findingTypePattern bounds the wire form (F22): printable ASCII,
// starts with a letter, dashes allowed. Reject whitespace, control
// chars, trailing newlines, and Unicode look-alikes at Raise time.
var findingTypePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Common finding types observed across roles. Operators may add
// more; these are the integrator-spec canonical members.
const (
	FindingTypeLocalBug                FindingType = "local-bug"
	FindingTypeMissingCrossContextSpec FindingType = "missing-cross-context-spec"
	FindingTypeUnableToHint            FindingType = "unable-to-hint"
)

// FindingRecord is the persistent shape of a finding. Lightweight
// compared to the runtime Finding struct (arrow.go) which only
// carries derive-status inputs.
//
// TransitionCount tracks lifecycle churn (F25). Incremented by
// Transition; readable via Get / ForArrow. Operators can use it to
// detect ping-pong loops.
type FindingRecord struct {
	ID              string
	ArrowID         string // which arrow raised it
	Type            FindingType
	Severity        int // 0=info..4=critical per arrow.go's constants
	Status          FindingStatus
	Description     string // operator-facing message
	RaisedAt        string // RFC3339; zero string for unknown
	RaisedByRole    string // analyst, architect, integrator, etc.
	TransitionCount int    // F25: monotonic per-record churn counter
}

// AsDeriveInput returns the slimmed Finding used by
// DeriveArrowStatus. The full FindingRecord is overkill for status
// derivation; this projection is what arrow.go consumes.
func (r FindingRecord) AsDeriveInput() Finding {
	return Finding{
		Status:       r.Status,
		SeverityRank: r.Severity,
	}
}

// FindingsStore is the in-memory finding registry keyed by arrow.
// Construct via NewFindingsStore. Thread-safe; the runner accesses
// it from per-clause evaluation goroutines.
//
// Per validation-pass-4 F1: byID stores an index into byArrow, not
// a pointer. Appending to byArrow[id] reallocates the backing
// array; if byID held pointers they would silently dangle.
// Indices are stable across slice growth.
//
// Persistence is the state-machine engine's job (a later component);
// this struct is the runtime hot path. A separate ledger would
// pull from FindingsStore at checkpoint boundaries.
type FindingsStore struct {
	mu        sync.RWMutex
	byArrow   map[string][]FindingRecord // arrowID → findings
	byID      map[string]findingLocator  // findingID → (arrowID, idx)
	version   uint64                     // F5: snapshot version
	observers []FindingsObserver         // phase-5 hook
}

// FindingsObserver is invoked under the store's write lock on every
// mutation (Raise/Transition/Forget/ForgetArrow). The future state-
// machine engine registers an observer to journal mutations without
// polling Version().
//
// Observers MUST be fast and non-blocking — they run with the store's
// write lock held. Long work (disk I/O, network) should hand off to a
// goroutine and return immediately.
//
// Kind values are the wire-stable names below. `before` is the zero
// FindingRecord for Raise events; `after` is the zero FindingRecord
// for Forget events. Both are populated for Transition.
type FindingsObserver func(event FindingsEvent)

// FindingsEventKind names the mutation type for FindingsEvent.
type FindingsEventKind string

const (
	FindingsEventRaise       FindingsEventKind = "raise"
	FindingsEventTransition  FindingsEventKind = "transition"
	FindingsEventForget      FindingsEventKind = "forget"
	FindingsEventForgetArrow FindingsEventKind = "forget-arrow"
)

// FindingsEvent is the payload delivered to a FindingsObserver.
//
// Role + Reason populate from TransitionWithReason (validation-
// pass-9 J4) so the persistence layer's audit log preserves the
// "who + why" of every transition. Both empty for non-transition
// events.
//
// At is the wall-clock timestamp of the mutation, captured by the
// store at emit time. Distinct from FindingRecord.RaisedAt (which
// is the original raise time and stays fixed for the lifetime of
// the record).
type FindingsEvent struct {
	Kind    FindingsEventKind
	ArrowID string
	Before  FindingRecord
	After   FindingRecord
	Version uint64
	Role    string
	Reason  string
	At      string
}

// Observe registers an observer to be invoked on every mutation.
// Observers fire in registration order. The store retains the
// observer; there is no Unregister (a typical engine registers
// exactly one observer at startup).
func (s *FindingsStore) Observe(fn FindingsObserver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observers = append(s.observers, fn)
}

// emit fires all observers under the existing write lock. Caller MUST
// hold s.mu (write lock). Inlined into mutators so the lock window is
// the same span as the state mutation.
func (s *FindingsStore) emit(e FindingsEvent) {
	for _, ob := range s.observers {
		ob(e)
	}
}

// findingLocator points to a record's location inside byArrow.
// Cheap to copy; immune to slice reallocation.
type findingLocator struct {
	arrowID string
	idx     int
}

// NewFindingsStore returns an empty store.
func NewFindingsStore() *FindingsStore {
	return &FindingsStore{
		byArrow: make(map[string][]FindingRecord),
		byID:    make(map[string]findingLocator),
	}
}

// Findings store errors.
var (
	ErrFindingIDEmpty         = errors.New("finding-id-empty")
	ErrFindingArrowIDEmpty    = errors.New("finding-arrow-id-empty")
	ErrFindingTypeEmpty       = errors.New("finding-type-empty")
	ErrFindingTypeInvalid     = errors.New("finding-type-invalid")
	ErrFindingDuplicateID     = errors.New("finding-duplicate-id")
	ErrFindingUnknownID       = errors.New("finding-unknown-id")
	ErrFindingInvalidStatus   = errors.New("finding-invalid-status")
	ErrFindingInvalidSeverity = errors.New("finding-invalid-severity")
	// ErrFindingProducerSelfAccept guards the gates.md §7.3 invariant:
	// "the producer may NOT accept its own risk — only the operator may
	// attest `accepted-risk`." When the role on a transition is
	// "producer" and the target is accepted-risk, the call is refused.
	// The operator (via the attestation flow) is the only legitimate
	// path to accepted-risk.
	ErrFindingProducerSelfAccept = errors.New("producer-cannot-accept-own-risk")
)

// maxFindingTransitions bounds per-record lifecycle churn (F25).
// Beyond this, Transition returns ErrFindingTransitionChurn. The
// operator can amend or Forget to clear the record.
const maxFindingTransitions = 100

// ErrFindingTransitionChurn signals a record has cycled through its
// allowed transitions more than maxFindingTransitions times.
var ErrFindingTransitionChurn = errors.New("finding-transition-churn")

// Raise adds a new finding to the store. ID/ArrowID/Type must be
// non-empty; Type must match findingTypePattern (F22); Severity
// must be 0..4 (F9); Status must be a known enum value (F23).
// Returns ErrFindingDuplicateID on ID re-use.
func (s *FindingsStore) Raise(r FindingRecord) error {
	if r.ID == "" {
		return ErrFindingIDEmpty
	}
	if r.ArrowID == "" {
		return ErrFindingArrowIDEmpty
	}
	if r.Type == "" {
		return ErrFindingTypeEmpty
	}
	// F22: validate type shape.
	if !findingTypePattern.MatchString(string(r.Type)) {
		return fmt.Errorf("%w: %q", ErrFindingTypeInvalid, r.Type)
	}
	// F9: severity must be in [0, 4].
	if r.Severity < SeverityInfo || r.Severity > SeverityCritical {
		return fmt.Errorf("%w: %d (must be 0..4)", ErrFindingInvalidSeverity, r.Severity)
	}
	// F23: status must be a known enum value (or zero, which is
	// FindingStatusOpen — the default).
	if !isKnownFindingStatus(r.Status) {
		return fmt.Errorf("%w: status=%d (unknown enum value)", ErrFindingInvalidStatus, r.Status)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.byID[r.ID]; dup {
		return fmt.Errorf("%w: %s", ErrFindingDuplicateID, r.ID)
	}
	stored := r
	s.byArrow[r.ArrowID] = append(s.byArrow[r.ArrowID], stored)
	idx := len(s.byArrow[r.ArrowID]) - 1
	// F1: store an index, not a pointer. Slice growth no longer
	// dangles byID.
	s.byID[r.ID] = findingLocator{arrowID: r.ArrowID, idx: idx}
	s.version++
	s.emit(FindingsEvent{
		Kind:    FindingsEventRaise,
		ArrowID: r.ArrowID,
		After:   stored,
		Version: s.version,
		At:      time.Now().UTC().Format(time.RFC3339Nano),
	})
	return nil
}

// Transition updates a finding's status per the validFindingTransition
// set. Returns ErrFindingInvalidStatus on illegal transitions and
// ErrFindingTransitionChurn after maxFindingTransitions cycles.
//
// accepted-risk → open is allowed here but typically goes through
// TransitionWithReason (F6) so the caller's role is recorded.
func (s *FindingsStore) Transition(id string, to FindingStatus) error {
	return s.transitionImpl(id, to, "", "")
}

// TransitionWithReason is the audit-recording variant of Transition.
// Per validation-pass-4 F6, transitions out of accepted-risk and
// resolved should record an operator role + reason so attestation
// can audit them.
//
// role and reason are appended to the record's Description as
// "[<role>] <reason>" so a downstream sync still picks them up.
// (A full audit-log table belongs to the state-machine engine.)
func (s *FindingsStore) TransitionWithReason(id string, to FindingStatus, role, reason string) error {
	return s.transitionImpl(id, to, role, reason)
}

func (s *FindingsStore) transitionImpl(id string, to FindingStatus, role, reason string) error {
	if !isKnownFindingStatus(to) {
		return fmt.Errorf("%w: target=%d", ErrFindingInvalidStatus, to)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// gates.md §7.3: only the operator (via the attestation flow) may
	// attest accepted-risk. A transition whose stated role is the
	// producer is refused so the producer cannot self-accept. The
	// check is on parameters only (no shared state), but lives inside
	// the lock so it's adjacent to the rest of the validation logic
	// — easier to audit, no ordering hazard if a future change adds
	// state-dependent conditions.
	if to == FindingStatusAcceptedRisk && role == "producer" {
		return fmt.Errorf("%w: %s", ErrFindingProducerSelfAccept, id)
	}
	loc, ok := s.byID[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrFindingUnknownID, id)
	}
	rec := &s.byArrow[loc.arrowID][loc.idx]
	if !validFindingTransition(rec.Status, to) {
		return fmt.Errorf("%w: %s → %s on %s",
			ErrFindingInvalidStatus, rec.Status, to, id)
	}
	if rec.TransitionCount >= maxFindingTransitions {
		return fmt.Errorf("%w: %s has churned %d times", ErrFindingTransitionChurn, id, rec.TransitionCount)
	}
	before := *rec
	rec.Status = to
	rec.TransitionCount++
	if role != "" || reason != "" {
		note := fmt.Sprintf("[transition %s → %s by %s: %s]", rec.Status, to, role, reason)
		if rec.Description == "" {
			rec.Description = note
		} else {
			rec.Description = rec.Description + " " + note
		}
	}
	s.version++
	s.emit(FindingsEvent{
		Kind:    FindingsEventTransition,
		ArrowID: rec.ArrowID,
		Before:  before,
		After:   *rec,
		Version: s.version,
		Role:    role,
		Reason:  reason,
		At:      time.Now().UTC().Format(time.RFC3339Nano),
	})
	return nil
}

// isKnownFindingStatus reports whether v is in the declared enum.
func isKnownFindingStatus(v FindingStatus) bool {
	switch v {
	case FindingStatusOpen,
		FindingStatusRunning,
		FindingStatusResolved,
		FindingStatusAcceptedRisk,
		FindingStatusUnevaluated:
		return true
	}
	return false
}

// validFindingTransition reports whether a status transition is
// allowed per gates.md §7.3. See the package-level lifecycle comment
// for the canonical set.
func validFindingTransition(from, to FindingStatus) bool {
	switch from {
	case FindingStatusOpen:
		return to == FindingStatusRunning ||
			to == FindingStatusResolved ||
			to == FindingStatusAcceptedRisk
	case FindingStatusRunning:
		return to == FindingStatusResolved ||
			to == FindingStatusAcceptedRisk ||
			to == FindingStatusUnevaluated ||
			to == FindingStatusOpen
	case FindingStatusResolved:
		return to == FindingStatusOpen
	case FindingStatusAcceptedRisk:
		return to == FindingStatusOpen
	case FindingStatusUnevaluated:
		return to == FindingStatusRunning || to == FindingStatusOpen
	}
	return false
}

// ForArrow returns a deep-copy snapshot of findings on the named
// arrow, sorted by ID. The result is safe to retain past concurrent
// Transition / Raise calls (records are values, not pointers).
func (s *FindingsStore) ForArrow(arrowID string) []FindingRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.byArrow[arrowID]
	out := make([]FindingRecord, len(src))
	copy(out, src)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ArrowIDs returns the set of arrow IDs that currently have at
// least one finding. Sorted deterministically for stable iteration
// in the project status aggregator and test assertions.
func (s *FindingsStore) ArrowIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.byArrow))
	for id, fs := range s.byArrow {
		if len(fs) > 0 {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// ForArrowVersioned returns the same snapshot as ForArrow plus the
// store version at snapshot time (F5). The caller can re-check
// Version() before acting on the snapshot to detect TOCTOU.
func (s *FindingsStore) ForArrowVersioned(arrowID string) ([]FindingRecord, uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.byArrow[arrowID]
	out := make([]FindingRecord, len(src))
	copy(out, src)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, s.version
}

// Version returns the current store version. Incremented on every
// Raise / Transition / Forget. Used together with ForArrowVersioned
// to detect TOCTOU between snapshot and downstream consumer (F5).
func (s *FindingsStore) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// Get returns a copy of the finding by ID, or zero+false if absent.
func (s *FindingsStore) Get(id string) (FindingRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	loc, ok := s.byID[id]
	if !ok {
		return FindingRecord{}, false
	}
	return s.byArrow[loc.arrowID][loc.idx], true
}

// Forget removes the finding by ID. Returns ErrFindingUnknownID if
// the ID is absent. Per validation-pass-4 F24: the runtime cache
// must be bound for long sessions; engine code calls this at
// checkpoint boundaries.
//
// Note: Forget rebuilds byID's indices for the affected arrow's
// remaining slice entries. O(n) in the arrow's finding count.
func (s *FindingsStore) Forget(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	loc, ok := s.byID[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrFindingUnknownID, id)
	}
	arr := s.byArrow[loc.arrowID]
	before := arr[loc.idx]
	arr = append(arr[:loc.idx], arr[loc.idx+1:]...)
	s.byArrow[loc.arrowID] = arr
	delete(s.byID, id)
	// Rebuild indices for shifted entries.
	for i := loc.idx; i < len(arr); i++ {
		s.byID[arr[i].ID] = findingLocator{arrowID: loc.arrowID, idx: i}
	}
	s.version++
	s.emit(FindingsEvent{
		Kind:    FindingsEventForget,
		ArrowID: loc.arrowID,
		Before:  before,
		Version: s.version,
		At:      time.Now().UTC().Format(time.RFC3339Nano),
	})
	return nil
}

// ForgetArrow removes every finding on the named arrow. Returns the
// number forgotten. No error if the arrow is absent (returns 0).
func (s *FindingsStore) ForgetArrow(arrowID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	src, ok := s.byArrow[arrowID]
	if !ok {
		return 0
	}
	for _, r := range src {
		delete(s.byID, r.ID)
	}
	delete(s.byArrow, arrowID)
	s.version++
	// Fan-out one ForgetArrow event per record so observers can journal
	// each removal. Single bulk event would lose per-record identity.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, r := range src {
		s.emit(FindingsEvent{
			Kind:    FindingsEventForgetArrow,
			ArrowID: arrowID,
			Before:  r,
			Version: s.version,
			At:      now,
		})
	}
	return len(src)
}

// String returns the wire form of a FindingStatus.
func (s FindingStatus) String() string {
	switch s {
	case FindingStatusOpen:
		return "open"
	case FindingStatusRunning:
		return "running"
	case FindingStatusResolved:
		return "resolved"
	case FindingStatusAcceptedRisk:
		return "accepted-risk"
	case FindingStatusUnevaluated:
		return "unevaluated"
	}
	return "invalid-finding-status"
}

// ParseFindingStatus maps a wire-form string back to the enum.
// Whitespace, case, and `_`-for-`-` are all normalized (F41).
func ParseFindingStatus(s string) (FindingStatus, error) {
	norm := strings.TrimSpace(strings.ToLower(s))
	norm = strings.ReplaceAll(norm, "_", "-")
	switch norm {
	case "open":
		return FindingStatusOpen, nil
	case "running":
		return FindingStatusRunning, nil
	case "resolved":
		return FindingStatusResolved, nil
	case "accepted-risk":
		return FindingStatusAcceptedRisk, nil
	case "unevaluated":
		return FindingStatusUnevaluated, nil
	}
	return 0, fmt.Errorf("unknown finding-status %q", s)
}
