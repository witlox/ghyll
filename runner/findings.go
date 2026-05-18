package runner

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Findings model. Per gates.md §7.3: a finding lives on the arrow
// (not on a clause); it has an id, a type, a severity, and a
// status. The full lifecycle is open → running → resolved /
// accepted-risk / unevaluated.
//
// FindingType is the integrator-introduced enum
// {local-bug, missing-cross-context-spec, ...} — extensible per
// project but constrained by cardinality-check on integrator G4.
// The runner doesn't enforce the closed set; the gate clauses do.

// FindingType is a free-form string. Operators declare the project's
// enum at init (integrator role file or amendment) and a
// cardinality-check clause asserts values stay inside the enum.
type FindingType string

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
type FindingRecord struct {
	ID           string
	ArrowID      string // which arrow raised it
	Type         FindingType
	Severity     int // 0=info..4=critical per arrow.go's constants
	Status       FindingStatus
	Description  string // operator-facing message
	RaisedAt     string // RFC3339; zero string for unknown
	RaisedByRole string // analyst, architect, integrator, etc.
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
// Persistence is the state-machine engine's job (a later component);
// this struct is the runtime hot path. A separate ledger would
// pull from FindingsStore at checkpoint boundaries.
type FindingsStore struct {
	mu      sync.RWMutex
	byArrow map[string][]FindingRecord // arrowID → findings
	byID    map[string]*FindingRecord  // findingID → ptr
}

// NewFindingsStore returns an empty store.
func NewFindingsStore() *FindingsStore {
	return &FindingsStore{
		byArrow: make(map[string][]FindingRecord),
		byID:    make(map[string]*FindingRecord),
	}
}

// Findings store errors.
var (
	ErrFindingIDEmpty       = errors.New("finding-id-empty")
	ErrFindingArrowIDEmpty  = errors.New("finding-arrow-id-empty")
	ErrFindingTypeEmpty     = errors.New("finding-type-empty")
	ErrFindingDuplicateID   = errors.New("finding-duplicate-id")
	ErrFindingUnknownID     = errors.New("finding-unknown-id")
	ErrFindingInvalidStatus = errors.New("finding-invalid-status")
)

// Raise adds a new finding to the store. ID must be non-empty and
// unique (Raise returns ErrFindingDuplicateID otherwise). Initial
// status is open unless the record specifies otherwise.
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.byID[r.ID]; dup {
		return fmt.Errorf("%w: %s", ErrFindingDuplicateID, r.ID)
	}
	// Default status to open if zero-valued (FindingStatusOpen == 0).
	stored := r
	s.byArrow[r.ArrowID] = append(s.byArrow[r.ArrowID], stored)
	// Point byID at the slice's tail (must re-resolve on slice
	// reallocations — store the index rather than the pointer).
	idx := len(s.byArrow[r.ArrowID]) - 1
	s.byID[r.ID] = &s.byArrow[r.ArrowID][idx]
	return nil
}

// Transition updates a finding's status. Per gates.md §7.3, valid
// transitions are open → running, running → resolved /
// accepted-risk / unevaluated, and resolved → open (reopen).
// Other transitions return ErrFindingInvalidStatus.
func (s *FindingsStore) Transition(id string, to FindingStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.byID[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrFindingUnknownID, id)
	}
	if !validFindingTransition(rec.Status, to) {
		return fmt.Errorf("%w: %s → %s on %s",
			ErrFindingInvalidStatus, rec.Status, to, id)
	}
	rec.Status = to
	return nil
}

// validFindingTransition reports whether a status transition is
// allowed per gates.md §7.3.
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
		// Reopen allowed if regression observed.
		return to == FindingStatusOpen
	case FindingStatusAcceptedRisk:
		// Sticky — operator must explicitly amend the grid to
		// re-open. We allow open transition for now; spec may
		// tighten.
		return to == FindingStatusOpen
	case FindingStatusUnevaluated:
		// Unevaluated transitions to running on retry.
		return to == FindingStatusRunning || to == FindingStatusOpen
	}
	return false
}

// ForArrow returns a snapshot of findings on the named arrow. The
// returned slice is a deep copy; mutating it doesn't affect the
// store.
func (s *FindingsStore) ForArrow(arrowID string) []FindingRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.byArrow[arrowID]
	out := make([]FindingRecord, len(src))
	copy(out, src)
	// Stable sort by ID for deterministic output.
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Get returns a copy of the finding by ID, or zero+false if absent.
func (s *FindingsStore) Get(id string) (FindingRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.byID[id]
	if !ok {
		return FindingRecord{}, false
	}
	return *rec, true
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
func ParseFindingStatus(s string) (FindingStatus, error) {
	switch strings.TrimSpace(strings.ToLower(s)) {
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
