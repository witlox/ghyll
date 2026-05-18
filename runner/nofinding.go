package runner

import (
	"context"
	"fmt"
	"strings"
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
// findings store under.
type findingsHookKey struct{}

// WithFindingsStore attaches a FindingsStore to ctx so the
// no-open-finding evaluator can read it. Per validation-pass-4
// F26, nested attachment is forbidden: if a store is already
// attached on ctx, this panics with "findings-store-already-attached"
// to prevent silent shadowing by test helpers.
func WithFindingsStore(ctx context.Context, store *FindingsStore) context.Context {
	if existing := ctx.Value(findingsHookKey{}); existing != nil {
		panic("findings-store-already-attached: nested WithFindingsStore silently shadows; pass the existing store down instead")
	}
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
		t, err := coerceSeverityArg(v)
		if err != nil {
			return nil, fmt.Errorf("no-open-finding: severity-threshold: %w", err)
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

	findings, version := store.ForArrowVersioned(arrowID)
	blocking := []map[string]any{}
	for _, f := range findings {
		if isBlockingFinding(f, threshold) {
			// F7: include description, raised-at, raised-by-role so
			// operator can triage without a second store lookup.
			blocking = append(blocking, map[string]any{
				"id":             f.ID,
				"type":           string(f.Type),
				"severity":       f.Severity,
				"status":         f.Status.String(),
				"description":    f.Description,
				"raised-at":      f.RaisedAt,
				"raised-by-role": f.RaisedByRole,
			})
		}
	}

	pass := len(blocking) == 0
	details := map[string]any{
		"arrow-id":          arrowID,
		"finding-count":     len(findings),
		"blocking-findings": blocking,
		"threshold":         threshold,
		"store-version":     version, // F5: caller can cross-check TOCTOU
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
//   - F8: any other (corrupt / future enum) status → blocking.
//     Default-to-block matches DeriveArrowStatus's handling of
//     unknown ClauseStatus — corruption must not silently pass.
func isBlockingFinding(f FindingRecord, threshold int) bool {
	switch f.Status {
	case FindingStatusOpen, FindingStatusRunning:
		// F9: clamp out-of-range severity to nearest valid rank as
		// defense in depth. Raise rejects them, but a corrupt
		// FindingRecord constructed outside Raise should not bypass
		// the gate.
		sev := f.Severity
		if sev < SeverityInfo {
			sev = SeverityInfo
		}
		if sev > SeverityCritical {
			sev = SeverityCritical
		}
		return sev >= threshold
	case FindingStatusUnevaluated:
		return true
	case FindingStatusResolved, FindingStatusAcceptedRisk:
		return false
	}
	// F8: corrupt / unknown status defaults to blocking.
	return true
}

// coerceSeverityArg accepts the operator's severity-threshold in
// either int (validate 0..4) or string (route through
// parseSeverityRank). F21.
func coerceSeverityArg(v any) (int, error) {
	switch x := v.(type) {
	case string:
		return parseSeverityRank(x)
	case int:
		if x < SeverityInfo || x > SeverityCritical {
			return 0, fmt.Errorf("severity rank %d out of 0..4", x)
		}
		return x, nil
	case int64:
		return coerceSeverityArg(int(x))
	case float64:
		if x != float64(int(x)) {
			return 0, fmt.Errorf("severity rank %v is not an integer", x)
		}
		return coerceSeverityArg(int(x))
	}
	return 0, fmt.Errorf("severity-threshold must be string or int; got %T", v)
}

// parseSeverityRank maps the severity wire form ("info", "low",
// "medium", "high", "critical") to its rank (0..4). Whitespace
// and case-insensitive (F10).
func parseSeverityRank(s string) (int, error) {
	norm := strings.TrimSpace(strings.ToLower(s))
	switch norm {
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
