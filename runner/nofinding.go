package runner

import (
	"context"
	"fmt"
)

// no-open-finding built-in evaluator. Per
// gates/concepts/no-open-finding.yaml: all findings on the named
// arrow are resolved or accepted-risk; no findings are open above
// the severity threshold; no findings have severity unevaluated.
//
// Used by integrator G5 and auto-inserted on every adversarial
// arrow during verification (gates.md §11.3).
//
// The evaluator consults an in-process FindingsStore looked up via
// the runner's findings hook. The hook is set per-Runner via
// WithFindings; tests inject a FindingsStore with pre-staged
// records.

// findingsHookKey is the context key the evaluator looks up the
// findings store under. Using a context key (rather than a global)
// keeps tests isolated and lets multiple runners coexist in the
// same process with independent finding state.
type findingsHookKey struct{}

// WithFindingsStore attaches a FindingsStore to ctx so the
// no-open-finding evaluator (and other findings-aware checks)
// can read it. The Runner threads ctx through to evaluators.
func WithFindingsStore(ctx context.Context, store *FindingsStore) context.Context {
	return context.WithValue(ctx, findingsHookKey{}, store)
}

// FindingsFromContext returns the FindingsStore attached via
// WithFindingsStore, or nil if no store is attached.
func FindingsFromContext(ctx context.Context) *FindingsStore {
	v := ctx.Value(findingsHookKey{})
	if v == nil {
		return nil
	}
	if store, ok := v.(*FindingsStore); ok {
		return store
	}
	return nil
}

// EvaluateNoOpenFinding is the built-in for no-open-finding.
func EvaluateNoOpenFinding(ctx context.Context, c Clause) (*Result, error) {
	arrowID, err := requireStringArg(c.Args, "arrow-id")
	if err != nil {
		return nil, fmt.Errorf("no-open-finding: %w", err)
	}
	threshold := SeverityMedium // schema default
	if v, ok := c.Args["severity-threshold"]; ok {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("no-open-finding: severity-threshold must be string, got %T", v)
		}
		t, err := parseSeverityRank(s)
		if err != nil {
			return nil, fmt.Errorf("no-open-finding: %w", err)
		}
		threshold = t
	}

	store := FindingsFromContext(ctx)
	if store == nil {
		// No store attached. The runner can't make a decision: a
		// missing store means findings information is not
		// available, not "no findings exist."
		return &Result{
			Unevaluated: true,
			Reason:      "no findings store attached to ctx; evaluator cannot decide",
			Details:     map[string]any{"arrow-id": arrowID},
		}, nil
	}

	findings := store.ForArrow(arrowID)
	blocking := []map[string]any{}
	for _, f := range findings {
		if isBlockingFinding(f, threshold) {
			blocking = append(blocking, map[string]any{
				"id":       f.ID,
				"type":     string(f.Type),
				"severity": f.Severity,
				"status":   f.Status.String(),
			})
		}
	}

	pass := len(blocking) == 0
	details := map[string]any{
		"arrow-id":          arrowID,
		"finding-count":     len(findings),
		"blocking-findings": blocking,
		"threshold":         threshold,
	}
	if !pass {
		details["error"] = fmt.Sprintf("%d finding(s) blocking", len(blocking))
	}
	return &Result{Pass: pass, Details: details}, nil
}

// isBlockingFinding reports whether the finding blocks the arrow
// per gates.md §7.3 + the no-open-finding concept:
//   - Status open or running AND severity >= threshold → blocking.
//   - Status unevaluated → blocking regardless of severity (severity
//     itself is unassigned per §7.3, validation-pass-3 F25).
//   - Status resolved or accepted-risk → not blocking.
func isBlockingFinding(f FindingRecord, threshold int) bool {
	switch f.Status {
	case FindingStatusOpen, FindingStatusRunning:
		return f.Severity >= threshold
	case FindingStatusUnevaluated:
		return true
	}
	return false
}

// parseSeverityRank maps the severity wire form ("info", "low",
// "medium", "high", "critical") to its rank (0..4).
func parseSeverityRank(s string) (int, error) {
	switch s {
	case "info":
		return SeverityInfo, nil
	case "low":
		return SeverityLow, nil
	case "medium":
		return SeverityMedium, nil
	case "high":
		return SeverityHigh, nil
	case "critical":
		return SeverityCritical, nil
	}
	return 0, fmt.Errorf("severity %q not in {info, low, medium, high, critical}", s)
}
