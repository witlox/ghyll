package runner

import (
	"context"
	"fmt"
)

// every-requirement-meets-min-depth built-in evaluator. Per
// gates.md §11 verification step: auto-inserted on every arrow that
// ran an adversarial phase. Guarantees the arrow cannot close while
// any requirement is classified below its declared minimum on the
// depth ladder.
//
// The evaluator consults a ClassificationsStore looked up via
// WithClassificationsStore(ctx). Missing store → Unevaluated with
// reason (parallel to no-open-finding's behavior).
//
// Arguments:
//
//	arrow-id : the arrow whose requirements to evaluate (required)

// EvaluateEveryRequirementMeetsMinDepth is the built-in.
func EvaluateEveryRequirementMeetsMinDepth(ctx context.Context, c Clause) (*Result, error) {
	arrowID, err := requireStringArg(c.Args, "arrow-id")
	if err != nil {
		return nil, fmt.Errorf("every-requirement-meets-min-depth: %w", err)
	}
	store := ClassificationsFromContext(ctx)
	if store == nil {
		return &Result{
			Unevaluated: true,
			Reason:      "no classifications-store attached to ctx; evaluator cannot decide",
			Details:     map[string]any{"arrow-id": arrowID},
		}, nil
	}

	reqs := store.RequirementsForArrow(arrowID)
	if len(reqs) == 0 {
		// No requirements declared on this arrow. Trivially passes —
		// nothing to classify. Distinct from "no classifications yet"
		// because requirements are operator-declared at init; their
		// absence is the operator's call.
		return &Result{
			Pass: true,
			Details: map[string]any{
				"arrow-id":          arrowID,
				"requirement-count": 0,
				"note":              "no requirements declared on this arrow",
			},
		}, nil
	}

	classifications := store.ClassificationsForArrow(arrowID)
	byReq := make(map[string]Classification, len(classifications))
	for _, c := range classifications {
		byReq[c.RequirementID] = c
	}

	var unclassified []string
	var belowMin []map[string]any
	for _, r := range reqs {
		c, ok := byReq[r.ID]
		if !ok {
			unclassified = append(unclassified, r.ID)
			continue
		}
		if c.Observed < r.MinDepth {
			belowMin = append(belowMin, map[string]any{
				"requirement-id": r.ID,
				"min-depth":      int(r.MinDepth),
				"observed":       int(c.Observed),
				"evidence":       c.Evidence,
				"description":    r.Description,
			})
		}
	}

	// Unclassified requirements → Unevaluated. The depth-classification
	// sub-activity didn't finish; closing the arrow would silently pass.
	if len(unclassified) > 0 {
		return &Result{
			Unevaluated: true,
			Reason: fmt.Sprintf("%d requirement(s) unclassified on arrow %s",
				len(unclassified), arrowID),
			Details: map[string]any{
				"arrow-id":          arrowID,
				"requirement-count": len(reqs),
				"unclassified":      unclassified,
			},
		}, nil
	}

	pass := len(belowMin) == 0
	details := map[string]any{
		"arrow-id":          arrowID,
		"requirement-count": len(reqs),
		"below-min-count":   len(belowMin),
		"below-min":         belowMin,
	}
	if !pass {
		details["error"] = fmt.Sprintf("%d requirement(s) classified below their declared minimum depth",
			len(belowMin))
	}
	return &Result{Pass: pass, Details: details}, nil
}
