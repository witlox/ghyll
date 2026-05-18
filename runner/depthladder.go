package runner

import (
	"errors"
	"fmt"
	"strings"
)

// Depth ladder. Per gates.md §11.1: classifies *what depth the
// upstream artifact actually reached* — distinct from a clause's
// depth-type requirement (which is about the model required to
// evaluate the clause).
//
// The harness ships a default 4-tier ladder. The number of tiers is
// FIXED at 4. Projects may rename labels at initialization (D11 +
// gates.md §11.1 "Project override") but cannot add/remove tiers.
// Rank 0 is always the lowest (no implementation); rank 3 is always
// the strongest (real-dependency).

// DepthRank is the integer rank of a depth tier (0..3). Higher is
// deeper. Out-of-range values are not silently accepted; see
// IsKnownDepthRank.
type DepthRank int

const (
	// DepthRankNone is rank 0 — no implementation.
	DepthRankNone DepthRank = iota
	// DepthRankShallow is rank 1 — exists but doesn't exercise the
	// behavior.
	DepthRankShallow
	// DepthRankMocked is rank 2 — exercises behavior against mocks/stubs
	// only.
	DepthRankMocked
	// DepthRankRealistic is rank 3 — exercises behavior against real
	// (or production-equivalent) dependencies.
	DepthRankRealistic
)

// MinDepthRank and MaxDepthRank bound the legal range. Fixed at 0..3
// per gates.md §11.1: the *number* of tiers is non-configurable.
const (
	MinDepthRank DepthRank = DepthRankNone
	MaxDepthRank DepthRank = DepthRankRealistic
	depthTiers             = 4
)

// IsKnownDepthRank reports whether r is in the valid 0..3 range.
func IsKnownDepthRank(r DepthRank) bool {
	return r >= MinDepthRank && r <= MaxDepthRank
}

// DefaultDepthLabels is the harness-shipped default ladder. Projects
// may override these labels but not add/remove tiers.
var DefaultDepthLabels = [depthTiers]string{
	"NONE",
	"SHALLOW",
	"MOCKED",
	"REALISTIC",
}

// DepthLadder is the per-project ladder. Construct via
// NewDefaultDepthLadder or NewDepthLadder. Thread-safe to read after
// construction (immutable).
type DepthLadder struct {
	labels [depthTiers]string
	// byLabel maps the (lowercase-normalized) label back to the rank
	// for ParseDepthRank. Built at construction time.
	byLabel map[string]DepthRank
}

// NewDefaultDepthLadder returns the harness-shipped ladder.
func NewDefaultDepthLadder() *DepthLadder {
	return mustNewDepthLadder(DefaultDepthLabels)
}

// NewDepthLadder constructs a ladder with project-overridden labels.
// Each of the 4 labels must be non-empty after trimming and unique
// (case-insensitive). Returns an error otherwise.
func NewDepthLadder(labels [depthTiers]string) (*DepthLadder, error) {
	cleaned := [depthTiers]string{}
	seen := make(map[string]DepthRank, depthTiers)
	for i, raw := range labels {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return nil, fmt.Errorf("depth-ladder: tier %d label is empty", i)
		}
		key := strings.ToLower(trimmed)
		if other, dup := seen[key]; dup {
			return nil, fmt.Errorf("depth-ladder: label %q appears at tiers %d and %d", trimmed, other, i)
		}
		seen[key] = DepthRank(i)
		cleaned[i] = trimmed
	}
	return &DepthLadder{labels: cleaned, byLabel: seen}, nil
}

// mustNewDepthLadder is NewDepthLadder for known-good labels (only
// invoked with DefaultDepthLabels). Panics on error — guarded by
// tests.
func mustNewDepthLadder(labels [depthTiers]string) *DepthLadder {
	l, err := NewDepthLadder(labels)
	if err != nil {
		panic(fmt.Sprintf("depth-ladder: default-labels invalid: %v", err))
	}
	return l
}

// Label returns the project's label for rank r. Returns the empty
// string for out-of-range r (caller should pre-check via
// IsKnownDepthRank).
func (l *DepthLadder) Label(r DepthRank) string {
	if !IsKnownDepthRank(r) {
		return ""
	}
	return l.labels[r]
}

// ParseDepthRank maps a project label back to its rank. Case- and
// whitespace-insensitive. Returns an error for unknown labels.
func (l *DepthLadder) ParseDepthRank(s string) (DepthRank, error) {
	key := strings.ToLower(strings.TrimSpace(s))
	if r, ok := l.byLabel[key]; ok {
		return r, nil
	}
	return 0, fmt.Errorf("depth-ladder: %q is not a label in {%s}", s, strings.Join(l.labels[:], ", "))
}

// Labels returns a copy of the ladder's labels (rank order).
func (l *DepthLadder) Labels() []string {
	out := make([]string, depthTiers)
	copy(out, l.labels[:])
	return out
}

// Requirement is one item on an upstream artifact that the
// adversarial phase's depth-classification sub-activity classifies.
//
// Per gates.md §11.1: each arrow declares a minimum depth per
// requirement at initialization. The classification yields an
// observed rank; depth-classification raises a finding when
// observed < min.
type Requirement struct {
	// ID is the operator-stable identifier (often the spec-line id
	// or a generated UUID). Required.
	ID string

	// MinDepth is the operator-declared minimum acceptable depth
	// for this requirement.
	MinDepth DepthRank

	// Description is the operator-facing requirement text.
	Description string
}

// Validate checks that the Requirement is well-formed.
func (r Requirement) Validate() error {
	if strings.TrimSpace(r.ID) == "" {
		return errors.New("requirement-id-empty")
	}
	if !IsKnownDepthRank(r.MinDepth) {
		return fmt.Errorf("requirement %q: min-depth %d out of 0..3", r.ID, r.MinDepth)
	}
	return nil
}

// Classification is the depth-classification sub-activity's verdict
// on one requirement. Observed is the depth rank the classifier
// assigned; Evidence is the operator-facing rationale.
type Classification struct {
	RequirementID string
	Observed      DepthRank
	Evidence      string
}

// Validate checks that the Classification is well-formed.
func (c Classification) Validate() error {
	if strings.TrimSpace(c.RequirementID) == "" {
		return errors.New("classification-requirement-id-empty")
	}
	if !IsKnownDepthRank(c.Observed) {
		return fmt.Errorf("classification %q: observed depth %d out of 0..3", c.RequirementID, c.Observed)
	}
	return nil
}
