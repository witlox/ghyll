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
//
// Hardenings (validation-pass-7):
//   - F2: Routed bool distinguishes "successfully decided fast-tier"
//     from "errored, value is zero." Callers MUST check Routed before
//     trusting MinTier.
//   - F3: dead branch in MaxDepthClauseID logic removed.
//   - F4: IsKnownDepthType accepts the wire-form parser's canonical
//     output AND lowercase-trimmed variants (parity with parser).
//   - F6: error messages sanitize ClauseID via sanitizeOneLine.
//   - F14: AnyDepthSensitive removed; use HasSensitive() method.

// ClauseDepthType enumerates the depth-type values per gates.md §6.
type ClauseDepthType string

const (
	DepthTypeRobust    ClauseDepthType = "depth-robust"
	DepthTypeSensitive ClauseDepthType = "depth-sensitive"
)

// IsKnownDepthType reports whether t is one of the declared values
// AFTER normalization (case-insensitive, underscore-to-dash). Mirrors
// ParseClauseDepthType so YAML loaders that bypass the parser don't
// silently mis-validate (validation-pass-7 F4).
func IsKnownDepthType(t ClauseDepthType) bool {
	_, err := ParseClauseDepthType(string(t))
	return err == nil
}

// RoutingRequirement is the runner-layer routing verdict.
//
// Routed=false signals "no successful routing decision" (validation-
// pass-7 F2). When Routed=false, the other fields are zero-valued
// and MUST NOT be consulted. Callers MUST honor `err != nil` from
// RouteArrow rather than reading these fields.
//
// MaxDepthClauseID is the clause that drove MinTier. On ties (two or
// more depth-sensitive clauses sharing the same MinTier), the FIRST
// encountered in iteration order is recorded — deterministic given
// stable slice order (validation-pass-7 F5).
type RoutingRequirement struct {
	Routed           bool
	MinTier          DepthRank
	MaxDepthClauseID string
}

// HasSensitive reports whether any depth-sensitive clause drove the
// routing. Replaces the redundant AnyDepthSensitive field (F14).
func (r RoutingRequirement) HasSensitive() bool {
	return r.MaxDepthClauseID != ""
}

// Validate cross-checks the requirement is well-formed.
func (r RoutingRequirement) Validate() error {
	if !r.Routed {
		// An unrouted requirement is only valid as the zero value.
		zero := RoutingRequirement{}
		if r != zero {
			return errors.New("routing: unrouted requirement carries non-zero fields")
		}
		return nil
	}
	if !IsKnownDepthRank(r.MinTier) {
		return fmt.Errorf("routing: MinTier %d out of 0..3", r.MinTier)
	}
	return nil
}

// Routing errors.
var (
	ErrClauseDepthTypeUnknown        = errors.New("clause-depth-type-unknown")
	ErrClauseDepthRequirementInvalid = errors.New("clause-depth-requirement-invalid")
)

// describeClause returns a triage-friendly label for the clause in
// error messages (F6). Empty ClauseID falls back to <unset:concept>.
func describeClause(c Clause) string {
	id := strings.TrimSpace(c.ClauseID)
	if id == "" {
		concept := strings.TrimSpace(c.Concept)
		if concept == "" {
			return "<unset>"
		}
		return "<unset:" + sanitizeOneLine(concept) + ">"
	}
	return sanitizeOneLine(id)
}

// ValidateClauseDepthDeclaration returns an error if the clause
// doesn't carry a valid depth declaration. Per gates.md §6 authoring
// must explicitly declare the depth-type; the runner fails closed
// when invoked with an unstated declaration.
func ValidateClauseDepthDeclaration(c Clause) error {
	if !IsKnownDepthType(c.DepthType) {
		return fmt.Errorf("%w: clause %s depth-type=%q (must be depth-robust or depth-sensitive)",
			ErrClauseDepthTypeUnknown, describeClause(c), sanitizeOneLine(string(c.DepthType)))
	}
	if c.DepthType == DepthTypeSensitive {
		if !IsKnownDepthRank(c.MinDepthTier) {
			return fmt.Errorf("%w: clause %s MinDepthTier=%d out of 0..3",
				ErrClauseDepthRequirementInvalid, describeClause(c), c.MinDepthTier)
		}
		if c.MinDepthTier == DepthRankNone {
			return fmt.Errorf("%w: clause %s is depth-sensitive but MinDepthTier=NONE (declare SHALLOW or deeper)",
				ErrClauseDepthRequirementInvalid, describeClause(c))
		}
	}
	return nil
}

// RouteArrow returns the routing requirement for an arrow's clauses.
// Per gates.md §8: max across all clauses.
//
// Behavior:
//   - All clauses must carry a valid depth declaration. The first
//     invalid clause returns its error; the result is unrouted.
//   - Empty clause list → Routed=true, MinTier=NONE (fast tier).
//   - All depth-robust → Routed=true, MinTier=NONE (fast tier).
//   - Any depth-sensitive → Routed=true, MinTier = max over
//     depth-sensitive clauses' MinDepthTier; first encountered tie
//     wins (deterministic given stable slice order, F5).
//
// On error: returns (zero RoutingRequirement, error). The zero value
// has Routed=false so callers cannot confuse it with a valid
// fast-tier verdict (F2).
func RouteArrow(clauses []Clause) (RoutingRequirement, error) {
	for _, c := range clauses {
		if err := ValidateClauseDepthDeclaration(c); err != nil {
			return RoutingRequirement{}, err
		}
	}
	req := RoutingRequirement{Routed: true, MinTier: DepthRankNone}
	for _, c := range clauses {
		if c.DepthType != DepthTypeSensitive {
			continue
		}
		if c.MinDepthTier > req.MinTier {
			req.MinTier = c.MinDepthTier
			req.MaxDepthClauseID = c.ClauseID
		} else if req.MaxDepthClauseID == "" {
			// First depth-sensitive clause (when MinDepthTier ==
			// req.MinTier == 0). After validation MinDepthTier ≥ 1
			// so this is unreachable in practice; documented for
			// completeness in case validation changes (F3).
			req.MaxDepthClauseID = c.ClauseID
		}
	}
	return req, nil
}

// ParseClauseDepthType is the case-insensitive parser for the wire
// form. Underscores normalize to dashes.
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

// UnevaluatedReason is the typed enum for clause-Unevaluated reasons
// per gates.md §7.1. Evaluator authors SHOULD use these constants;
// the runner validates against this set (validation-pass-7 F11).
type UnevaluatedReason string

const (
	ReasonDepthBelowRequired    UnevaluatedReason = "depth-below-required"
	ReasonNoRuleSelectableHints UnevaluatedReason = "no-rule-selectable-locations"
	ReasonProducerNoResponse    UnevaluatedReason = "producer-no-response"
)

// IsKnownUnevaluatedReason reports whether r is in the §7.1 set.
func IsKnownUnevaluatedReason(r UnevaluatedReason) bool {
	switch r {
	case ReasonDepthBelowRequired,
		ReasonNoRuleSelectableHints,
		ReasonProducerNoResponse:
		return true
	}
	return false
}
