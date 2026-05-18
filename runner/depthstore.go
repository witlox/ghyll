package runner

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ClassificationsStore is the per-arrow registry of declared
// Requirements and Classifications produced by the adversarial
// phase's depth-classification sub-activity (gates.md §11.1).
//
// Construct via NewClassificationsStore. Thread-safe. Attach to a
// ctx via WithClassificationsStore so the
// every-requirement-meets-min-depth evaluator can read it.
//
// Validation-pass-5 hardenings:
//   - SnapshotArrow (F10): single-RLock combined (reqs, cls,
//     version) read. Evaluators MUST use this instead of separate
//     RequirementsForArrow + ClassificationsForArrow if they need
//     to reason about (req, cls) pairs.
//   - Forget / ForgetArrow (F14): bounded long-session memory.
//   - Observer (F14): engine layer journals mutations.
//   - Overwrite tracking (F15): RecordClassification overwrite emits
//     an event so an adversarial role re-classifying silently is
//     auditable.
type ClassificationsStore struct {
	mu              sync.RWMutex
	reqs            map[string]map[string]Requirement // arrowID → reqID → Requirement
	classifications map[string]map[string]Classification
	version         uint64
	observers       []ClassificationsObserver
}

// ClassificationsEventKind names a mutation type.
type ClassificationsEventKind string

const (
	ClassificationsEventDeclare     ClassificationsEventKind = "declare"
	ClassificationsEventRecord      ClassificationsEventKind = "record"
	ClassificationsEventOverwrite   ClassificationsEventKind = "record-overwrite"
	ClassificationsEventForget      ClassificationsEventKind = "forget"
	ClassificationsEventForgetArrow ClassificationsEventKind = "forget-arrow"
)

// ClassificationsEvent is the payload delivered to a ClassificationsObserver.
type ClassificationsEvent struct {
	Kind          ClassificationsEventKind
	ArrowID       string
	RequirementID string
	Requirement   Requirement    // populated for declare events
	Before        Classification // populated for overwrite/forget events
	After         Classification // populated for record events
	Version       uint64
}

// ClassificationsObserver fires under the write lock on every
// mutation. Same constraints as FindingsObserver: must be fast and
// non-blocking.
type ClassificationsObserver func(event ClassificationsEvent)

// NewClassificationsStore returns an empty store.
func NewClassificationsStore() *ClassificationsStore {
	return &ClassificationsStore{
		reqs:            make(map[string]map[string]Requirement),
		classifications: make(map[string]map[string]Classification),
	}
}

// Observe registers an observer. Multiple observers fire in
// registration order. The store retains the function (no unregister).
func (s *ClassificationsStore) Observe(fn ClassificationsObserver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observers = append(s.observers, fn)
}

func (s *ClassificationsStore) emit(e ClassificationsEvent) {
	for _, ob := range s.observers {
		ob(e)
	}
}

// Classifications store errors.
var (
	ErrRequirementUnknown      = errors.New("requirement-unknown")
	ErrRequirementDuplicateID  = errors.New("requirement-duplicate-id")
	ErrClassificationDuplicate = errors.New("classification-duplicate")
	ErrArrowIDEmpty            = errors.New("arrow-id-empty")
)

// normalizeArrowID trims surrounding whitespace per F24 so " A1" and
// "A1" don't fork into two distinct map keys with silently divergent
// state.
func normalizeArrowID(arrowID string) (string, error) {
	trimmed := strings.TrimSpace(arrowID)
	if trimmed == "" {
		return "", ErrArrowIDEmpty
	}
	return trimmed, nil
}

// DeclareRequirement registers an operator-declared Requirement on
// the named arrow. Returns ErrRequirementDuplicateID if the ID
// already exists on that arrow. arrowID is trimmed.
func (s *ClassificationsStore) DeclareRequirement(arrowID string, r Requirement) error {
	arrowID, err := normalizeArrowID(arrowID)
	if err != nil {
		return err
	}
	if err := r.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reqs[arrowID] == nil {
		s.reqs[arrowID] = make(map[string]Requirement)
	}
	if _, dup := s.reqs[arrowID][r.ID]; dup {
		return fmt.Errorf("%w: %s", ErrRequirementDuplicateID, r.ID)
	}
	s.reqs[arrowID][r.ID] = r
	s.version++
	s.emit(ClassificationsEvent{
		Kind:          ClassificationsEventDeclare,
		ArrowID:       arrowID,
		RequirementID: r.ID,
		Requirement:   r,
		Version:       s.version,
	})
	return nil
}

// RecordClassification stores the adversary's observed depth for a
// requirement. The requirement must have been declared first;
// otherwise ErrRequirementUnknown.
//
// Re-recording overwrites the prior classification but emits a
// distinct ClassificationsEventOverwrite event so audit consumers
// can detect the overwrite (F15).
func (s *ClassificationsStore) RecordClassification(arrowID string, c Classification) error {
	arrowID, err := normalizeArrowID(arrowID)
	if err != nil {
		return err
	}
	if err := c.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.reqs[arrowID][c.RequirementID]; !ok {
		return fmt.Errorf("%w: %s on arrow %s", ErrRequirementUnknown, c.RequirementID, arrowID)
	}
	if s.classifications[arrowID] == nil {
		s.classifications[arrowID] = make(map[string]Classification)
	}
	before, hadPrior := s.classifications[arrowID][c.RequirementID]
	s.classifications[arrowID][c.RequirementID] = c
	s.version++
	if hadPrior {
		s.emit(ClassificationsEvent{
			Kind:          ClassificationsEventOverwrite,
			ArrowID:       arrowID,
			RequirementID: c.RequirementID,
			Before:        before,
			After:         c,
			Version:       s.version,
		})
	} else {
		s.emit(ClassificationsEvent{
			Kind:          ClassificationsEventRecord,
			ArrowID:       arrowID,
			RequirementID: c.RequirementID,
			After:         c,
			Version:       s.version,
		})
	}
	return nil
}

// RequirementsForArrow returns a sorted snapshot of the arrow's
// declared requirements. For (reqs, cls, version) tuples that must
// be reasoned about together, prefer SnapshotArrow.
func (s *ClassificationsStore) RequirementsForArrow(arrowID string) []Requirement {
	trimmed, err := normalizeArrowID(arrowID)
	if err != nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.reqs[trimmed]
	out := make([]Requirement, 0, len(src))
	for _, r := range src {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ClassificationsForArrow returns a sorted snapshot of recorded
// classifications.
func (s *ClassificationsStore) ClassificationsForArrow(arrowID string) []Classification {
	trimmed, err := normalizeArrowID(arrowID)
	if err != nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.classifications[trimmed]
	out := make([]Classification, 0, len(src))
	for _, c := range src {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RequirementID < out[j].RequirementID })
	return out
}

// SnapshotArrow returns (requirements, classifications, version) as
// a single atomic snapshot under one RLock (F10). Pairs that would
// race under split-lock reads now share one lock acquisition.
func (s *ClassificationsStore) SnapshotArrow(arrowID string) ([]Requirement, []Classification, uint64) {
	trimmed, err := normalizeArrowID(arrowID)
	if err != nil {
		return nil, nil, 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	srcR := s.reqs[trimmed]
	srcC := s.classifications[trimmed]
	reqs := make([]Requirement, 0, len(srcR))
	for _, r := range srcR {
		reqs = append(reqs, r)
	}
	sort.Slice(reqs, func(i, j int) bool { return reqs[i].ID < reqs[j].ID })
	cls := make([]Classification, 0, len(srcC))
	for _, c := range srcC {
		cls = append(cls, c)
	}
	sort.Slice(cls, func(i, j int) bool { return cls[i].RequirementID < cls[j].RequirementID })
	return reqs, cls, s.version
}

// Version returns the current mutation counter.
func (s *ClassificationsStore) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// Forget removes one requirement (and its classification, if any).
// Returns ErrRequirementUnknown if absent.
func (s *ClassificationsStore) Forget(arrowID, reqID string) error {
	trimmed, err := normalizeArrowID(arrowID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.reqs[trimmed][reqID]; !ok {
		return fmt.Errorf("%w: %s on arrow %s", ErrRequirementUnknown, reqID, trimmed)
	}
	cls, hadCls := s.classifications[trimmed][reqID]
	delete(s.reqs[trimmed], reqID)
	delete(s.classifications[trimmed], reqID)
	s.version++
	if hadCls {
		s.emit(ClassificationsEvent{
			Kind:          ClassificationsEventForget,
			ArrowID:       trimmed,
			RequirementID: reqID,
			Before:        cls,
			Version:       s.version,
		})
	} else {
		s.emit(ClassificationsEvent{
			Kind:          ClassificationsEventForget,
			ArrowID:       trimmed,
			RequirementID: reqID,
			Version:       s.version,
		})
	}
	return nil
}

// ForgetArrow removes every requirement + classification for the
// named arrow. Returns the number of requirements forgotten.
func (s *ClassificationsStore) ForgetArrow(arrowID string) int {
	trimmed, err := normalizeArrowID(arrowID)
	if err != nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.reqs[trimmed]
	n := len(src)
	if n == 0 {
		return 0
	}
	delete(s.reqs, trimmed)
	delete(s.classifications, trimmed)
	s.version++
	s.emit(ClassificationsEvent{
		Kind:    ClassificationsEventForgetArrow,
		ArrowID: trimmed,
		Version: s.version,
	})
	return n
}

// classificationsHookKey is the ctx key for the store.
type classificationsHookKey struct{}

// WithClassificationsStore attaches a store to ctx. Nested
// attachment panics (mirrors WithFindingsStore F26).
func WithClassificationsStore(ctx context.Context, s *ClassificationsStore) context.Context {
	if existing := ctx.Value(classificationsHookKey{}); existing != nil {
		panic("classifications-store-already-attached: nested WithClassificationsStore silently shadows")
	}
	return context.WithValue(ctx, classificationsHookKey{}, s)
}

// ClassificationsFromContext returns the store attached via
// WithClassificationsStore, or nil if absent.
func ClassificationsFromContext(ctx context.Context) *ClassificationsStore {
	v := ctx.Value(classificationsHookKey{})
	if v == nil {
		return nil
	}
	if s, ok := v.(*ClassificationsStore); ok {
		return s
	}
	return nil
}
