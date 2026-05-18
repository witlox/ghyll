package runner

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
// The store keeps Requirements (operator-declared at init) and
// Classifications (observed by the adversary) separately. The
// pairing is computed on read so a partial classification pass
// (some requirements observed, others pending) is distinguishable
// from "no work done."
type ClassificationsStore struct {
	mu              sync.RWMutex
	reqs            map[string]map[string]Requirement // arrowID → reqID → Requirement
	classifications map[string]map[string]Classification
	version         uint64
}

// NewClassificationsStore returns an empty store.
func NewClassificationsStore() *ClassificationsStore {
	return &ClassificationsStore{
		reqs:            make(map[string]map[string]Requirement),
		classifications: make(map[string]map[string]Classification),
	}
}

// Classifications store errors.
var (
	ErrRequirementUnknown      = errors.New("requirement-unknown")
	ErrRequirementDuplicateID  = errors.New("requirement-duplicate-id")
	ErrClassificationDuplicate = errors.New("classification-duplicate")
)

// DeclareRequirement registers an operator-declared Requirement on
// the named arrow. Returns ErrRequirementDuplicateID if the ID
// already exists on that arrow.
func (s *ClassificationsStore) DeclareRequirement(arrowID string, r Requirement) error {
	if arrowID == "" {
		return errors.New("arrow-id-empty")
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
	return nil
}

// RecordClassification stores the adversary's observed depth for a
// requirement. The requirement must have been declared first;
// otherwise ErrRequirementUnknown.
//
// Re-recording overwrites the prior classification (the adversary
// may re-classify on remediation re-runs).
func (s *ClassificationsStore) RecordClassification(arrowID string, c Classification) error {
	if arrowID == "" {
		return errors.New("arrow-id-empty")
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
	s.classifications[arrowID][c.RequirementID] = c
	s.version++
	return nil
}

// RequirementsForArrow returns a sorted snapshot of the arrow's
// declared requirements.
func (s *ClassificationsStore) RequirementsForArrow(arrowID string) []Requirement {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.reqs[arrowID]
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
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.classifications[arrowID]
	out := make([]Classification, 0, len(src))
	for _, c := range src {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RequirementID < out[j].RequirementID })
	return out
}

// Version returns the current mutation counter.
func (s *ClassificationsStore) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// classificationsHookKey is the ctx key for the store.
type classificationsHookKey struct{}

// WithClassificationsStore attaches a store to ctx. Like
// WithFindingsStore (nofinding.go), nested attachment is forbidden
// — silent shadowing is the hazard.
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
