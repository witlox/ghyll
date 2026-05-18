package runner

import (
	"errors"
	"fmt"
	"regexp"
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
	DepthRankNone DepthRank = iota
	DepthRankShallow
	DepthRankMocked
	DepthRankRealistic
)

const (
	MinDepthRank DepthRank = DepthRankNone
	MaxDepthRank DepthRank = DepthRankRealistic
	depthTiers             = 4

	// maxRequirementDescLen bounds Requirement.Description (F21).
	// maxClassificationEvidenceLen bounds Classification.Evidence.
	maxRequirementDescLen        = 8 * 1024
	maxClassificationEvidenceLen = 8 * 1024
)

// IsKnownDepthRank reports whether r is in the valid 0..3 range.
func IsKnownDepthRank(r DepthRank) bool {
	return r >= MinDepthRank && r <= MaxDepthRank
}

// depthLabelPattern restricts labels to ASCII (F23): a non-ASCII
// label could survive case-insensitive dup detection because
// `strings.ToLower` only handles ASCII-ish casing (Turkish dotless-I
// etc. cause false negatives).
var depthLabelPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`)

// defaultDepthLabelsValue is the package-private source of truth
// (F36: validation-pass-5). Use DefaultDepthLabels() to read.
var defaultDepthLabelsValue = [depthTiers]string{
	"NONE",
	"SHALLOW",
	"MOCKED",
	"REALISTIC",
}

// DefaultDepthLabels returns the harness-shipped default labels as
// a fresh array. Callers cannot mutate the package's source of
// truth (F36).
func DefaultDepthLabels() [depthTiers]string {
	return defaultDepthLabelsValue
}

// DepthLadder is the per-project ladder. Construct via
// NewDefaultDepthLadder or NewDepthLadder. Thread-safe to read after
// construction (immutable).
type DepthLadder struct {
	labels  [depthTiers]string
	byLabel map[string]DepthRank
}

// NewDefaultDepthLadder returns the harness-shipped ladder.
func NewDefaultDepthLadder() *DepthLadder {
	return mustNewDepthLadder(defaultDepthLabelsValue)
}

// NewDepthLadder constructs a ladder with project-overridden labels.
// Each label must match depthLabelPattern (ASCII-only) and be unique
// (case-insensitive).
func NewDepthLadder(labels [depthTiers]string) (*DepthLadder, error) {
	cleaned := [depthTiers]string{}
	seen := make(map[string]DepthRank, depthTiers)
	for i, raw := range labels {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return nil, fmt.Errorf("depth-ladder: tier %d label is empty", i)
		}
		if !depthLabelPattern.MatchString(trimmed) {
			return nil, fmt.Errorf("depth-ladder: tier %d label %q must match %s",
				i, trimmed, depthLabelPattern.String())
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

func mustNewDepthLadder(labels [depthTiers]string) *DepthLadder {
	l, err := NewDepthLadder(labels)
	if err != nil {
		panic(fmt.Sprintf("depth-ladder: default-labels invalid: %v", err))
	}
	return l
}

// Label returns the project's label for rank r. Out-of-range r
// returns a clearly-marked invalid string so downstream descriptions
// don't print empty fields (F35).
func (l *DepthLadder) Label(r DepthRank) string {
	if !IsKnownDepthRank(r) {
		return fmt.Sprintf("invalid-depth-rank(%d)", r)
	}
	return l.labels[r]
}

// ParseDepthRank maps a project label back to its rank. Case- and
// whitespace-insensitive.
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
//
// Per validation-pass-5 F12: MinDepth=DepthRankNone is rejected at
// Validate — a min of NONE makes the gate inert (any Observed >= 0
// passes). Operators must declare at least SHALLOW.
type Requirement struct {
	ID          string
	MinDepth    DepthRank
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
	if r.MinDepth == DepthRankNone {
		// F12: MinDepth=NONE silently passes everything; reject.
		return fmt.Errorf("requirement %q: min-depth NONE (0) is inert; declare at least SHALLOW", r.ID)
	}
	if strings.TrimSpace(r.Description) == "" {
		// F22: operator triage needs the description.
		return fmt.Errorf("requirement %q: description is empty", r.ID)
	}
	if len(r.Description) > maxRequirementDescLen {
		// F21: cap on operator-supplied free text.
		return fmt.Errorf("requirement %q: description exceeds %d bytes", r.ID, maxRequirementDescLen)
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
	if len(c.Evidence) > maxClassificationEvidenceLen {
		// F21: cap on operator-supplied free text.
		return fmt.Errorf("classification %q: evidence exceeds %d bytes", c.RequirementID, maxClassificationEvidenceLen)
	}
	return nil
}
