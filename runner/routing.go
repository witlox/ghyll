package runner

import (
	"errors"
	"fmt"
	"strings"
)

// Routing. Per gates.md §8: ghyll routes each pass to a model tier.
// Routing is driven by the GATE DEFINITION, never by a model's
// self-assessment.
//
// Rule: "a pass traversing an arrow runs at the lowest model tier
// whose depth meets the maximum depth requirement across all clauses
// on that arrow."
//
// ghyll defines depth *types*; the type→concrete-model mapping is
// operator-supplied config and lives OUTSIDE the runner. The runner
// returns a RoutingRequirement (an abstract tier request); a separate
// dispatcher binds RoutingRequirement.MinTier to an actual model.

// ClauseDepthType enumerates the depth-type values per gates.md §6.
// `depth-robust` clauses can be honestly evaluated by any tier; a
// weak model cannot fake them. `depth-sensitive` clauses require a
// model at or above the declared depth.
type ClauseDepthType string

const (
	DepthTypeRobust    ClauseDepthType = "depth-robust"
	DepthTypeSensitive ClauseDepthType = "depth-sensitive"
)

// IsKnownDepthType reports whether t is one of the declared values.
func IsKnownDepthType(t ClauseDepthType) bool {
	switch t {
	case DepthTypeRobust, DepthTypeSensitive:
		return true
	}
	return false
}

// RoutingRequirement is the runner-layer routing verdict. The
// configuration layer (operator-supplied) maps MinTier to a concrete
// model.
//
// AnyDepthSensitive flags whether any clause on the arrow was
// depth-sensitive. If false, MinTier defaults to DepthRankNone (any
// tier suffices) and the operator's config should route to the
// project's "fast" tier per gates.md §8.
//
// MaxDepthClauseID is the clause that drove MinTier; useful for
// operator triage when a high-tier route surprises them.
type RoutingRequirement struct {
	MinTier           DepthRank
	AnyDepthSensitive bool
	MaxDepthClauseID  string
}

// Validate cross-checks the requirement is well-formed.
func (r RoutingRequirement) Validate() error {
	if !IsKnownDepthRank(r.MinTier) {
		return fmt.Errorf("routing: MinTier %d out of 0..3", r.MinTier)
	}
	if r.AnyDepthSensitive && r.MaxDepthClauseID == "" {
		return errors.New("routing: AnyDepthSensitive set but MaxDepthClauseID is empty")
	}
	return nil
}

// Routing errors.
var (
	// ErrClauseDepthTypeUnknown signals a clause was authored
	// without a recognized depth-type. Per gates.md §6: the depth-
	// type MUST be operator-declared at clause-authoring time and
	// "must not default to depth-robust for convenience."
	ErrClauseDepthTypeUnknown = errors.New("clause-depth-type-unknown")

	// ErrClauseDepthRequirementInvalid signals a depth-sensitive
	// clause was authored with an out-of-range MinDepth.
	ErrClauseDepthRequirementInvalid = errors.New("clause-depth-requirement-invalid")
)

// ValidateClauseDepthDeclaration returns an error if the clause
// doesn't carry a valid depth declaration. Per gates.md §6 the
// authoring boundary is the right place to demand this; the runner
// fails closed when invoked with an unstated declaration.
func ValidateClauseDepthDeclaration(c Clause) error {
	if !IsKnownDepthType(c.DepthType) {
		return fmt.Errorf("%w: clause %q depth-type=%q (must be depth-robust or depth-sensitive)",
			ErrClauseDepthTypeUnknown, c.ClauseID, c.DepthType)
	}
	if c.DepthType == DepthTypeSensitive {
		if !IsKnownDepthRank(c.MinDepthTier) {
			return fmt.Errorf("%w: clause %q MinDepthTier=%d out of 0..3",
				ErrClauseDepthRequirementInvalid, c.ClauseID, c.MinDepthTier)
		}
		// A depth-sensitive clause requesting NONE (rank 0) is the
		// same defect class as Requirement.Validate's MinDepth=NONE
		// (validation-pass-5 F12): it makes the gate inert.
		if c.MinDepthTier == DepthRankNone {
			return fmt.Errorf("%w: clause %q is depth-sensitive but MinDepthTier=NONE (declare SHALLOW or deeper)",
				ErrClauseDepthRequirementInvalid, c.ClauseID)
		}
	}
	return nil
}

// RouteArrow returns the routing requirement for an arrow's clauses.
// Per gates.md §8: max across all clauses.
//
// Behavior:
//   - All clauses must carry a valid depth declaration
//     (ValidateClauseDepthDeclaration). The first invalid clause
//     returns its error; the runner does NOT silently default.
//   - Empty clause list → MinTier=NONE, AnyDepthSensitive=false.
//   - All depth-robust → MinTier=NONE, AnyDepthSensitive=false.
//   - Any depth-sensitive → MinTier = max over depth-sensitive
//     clauses' MinDepthTier; AnyDepthSensitive=true.
func RouteArrow(clauses []Clause) (RoutingRequirement, error) {
	req := RoutingRequirement{MinTier: DepthRankNone}
	if len(clauses) == 0 {
		return req, nil
	}
	for _, c := range clauses {
		if err := ValidateClauseDepthDeclaration(c); err != nil {
			return RoutingRequirement{}, err
		}
	}
	for _, c := range clauses {
		if c.DepthType != DepthTypeSensitive {
			continue
		}
		req.AnyDepthSensitive = true
		if c.MinDepthTier > req.MinTier {
			req.MinTier = c.MinDepthTier
			req.MaxDepthClauseID = c.ClauseID
		} else if req.MaxDepthClauseID == "" {
			// First depth-sensitive clause; pin even if equal to
			// the seed value.
			req.MaxDepthClauseID = c.ClauseID
		}
	}
	return req, nil
}

// ParseClauseDepthType is the case-insensitive parser for the wire
// form. Underscores normalize to dashes (same forgiving policy as
// ParseFindingStatus).
func ParseClauseDepthType(s string) (ClauseDepthType, error) {
	norm := strings.TrimSpace(strings.ToLower(s))
	norm = strings.ReplaceAll(norm, "_", "-")
	switch norm {
	case "depth-robust":
		return DepthTypeRobust, nil
	case "depth-sensitive":
		return DepthTypeSensitive, nil
	}
	return "", fmt.Errorf("unknown clause-depth-type %q", s)
}
