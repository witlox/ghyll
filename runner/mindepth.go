package runner

import (
	"context"
	"fmt"
	"strings"
)

// every-requirement-meets-min-depth built-in evaluator. Per
// gates.md §11 verification step: auto-inserted on every arrow that
// ran an adversarial phase. Guarantees the arrow cannot close while
// any requirement is classified below its declared minimum on the
// depth ladder.
//
// The evaluator consults a ClassificationsStore looked up via
// WithClassificationsStore(ctx). Missing store → Unevaluated.
//
// Hardenings (validation-pass-5):
//   - F10: use SnapshotArrow for a single-RLock (reqs, cls, version)
//     read to avoid TOCTOU.
//   - F11: sanitize operator-supplied Evidence/Description before
//     placing into Result.Details.
//   - F13: empty requirements set → Unevaluated (operator probably
//     forgot to declare); operator opts into trivial-pass via
//     allow-empty=true.
//   - F20: embed store version into details so engine can detect
//     out-of-band mutation.
//   - F25: reject empty arrow-id.
//
// Arguments:
//
//	arrow-id    : the arrow whose requirements to evaluate (required)
//	allow-empty : bool, default false. When true, an arrow with no
//	              declared requirements passes trivially.

// EvaluateEveryRequirementMeetsMinDepth is the built-in.
func EvaluateEveryRequirementMeetsMinDepth(ctx context.Context, c Clause) (*Result, error) {
	arrowID, err := requireStringArg(c.Args, "arrow-id")
	if err != nil {
		return nil, fmt.Errorf("every-requirement-meets-min-depth: %w", err)
	}
	if strings.TrimSpace(arrowID) == "" {
		// F25: writers reject empty; reader should too.
		return nil, fmt.Errorf("every-requirement-meets-min-depth: arrow-id is empty")
	}
	allowEmpty := false
	if v, ok := c.Args["allow-empty"]; ok {
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("every-requirement-meets-min-depth: allow-empty must be bool, got %T", v)
		}
		allowEmpty = b
	}

	store := ClassificationsFromContext(ctx)
	if store == nil {
		return &Result{
			Unevaluated: true,
			Reason:      "no classifications-store attached to ctx; evaluator cannot decide",
			Details:     map[string]any{"arrow-id": arrowID},
		}, nil
	}

	reqs, classifications, version := store.SnapshotArrow(arrowID)
	if len(reqs) == 0 {
		if allowEmpty {
			return &Result{
				Pass: true,
				Details: map[string]any{
					"arrow-id":          arrowID,
					"requirement-count": 0,
					"note":              "no requirements declared on this arrow; allow-empty=true",
					"store-version":     version,
				},
			}, nil
		}
		// F13: empty requirements set is operator misconfiguration
		// (init forgot to declare); the verification gate that was
		// supposed to enforce silently passes without this guard.
		return &Result{
			Unevaluated: true,
			Reason:      "no requirements declared on this arrow; adversarial phase ran but classification has nothing to assert against",
			Details: map[string]any{
				"arrow-id":          arrowID,
				"requirement-count": 0,
				"store-version":     version,
			},
		}, nil
	}

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
				"evidence":       sanitizeOneLine(c.Evidence),
				"description":    sanitizeOneLine(r.Description),
			})
		}
	}

	if len(unclassified) > 0 {
		return &Result{
			Unevaluated: true,
			Reason: fmt.Sprintf("%d requirement(s) unclassified on arrow %s",
				len(unclassified), arrowID),
			Details: map[string]any{
				"arrow-id":          arrowID,
				"requirement-count": len(reqs),
				"unclassified":      unclassified,
				"store-version":     version,
			},
		}, nil
	}

	pass := len(belowMin) == 0
	details := map[string]any{
		"arrow-id":          arrowID,
		"requirement-count": len(reqs),
		"below-min-count":   len(belowMin),
		"below-min":         belowMin,
		"store-version":     version,
	}
	if !pass {
		details["error"] = fmt.Sprintf("%d requirement(s) classified below their declared minimum depth",
			len(belowMin))
	}
	return &Result{Pass: pass, Details: details}, nil
}
