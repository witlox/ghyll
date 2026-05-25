package runner

import (
	"context"
	"fmt"
)

// single-active-role-instance built-in evaluator (Gap 4 / ADR-v4-006).
//
// Per gates/concepts/single-active-role-instance.yaml:
//
//	role:     role-id, required
//	context:  bounded-context-id, required
//
// The evaluator consults the runner's *PassRegistry (the live-pass
// view) and counts open passes on the (role, context) tuple. The
// pass that contains THIS clause is excluded (design H3 closure:
// filter-self contract — a clause cannot conflict with its own
// containing pass).
//
// Pass iff: ≤ 1 conflicting pass (i.e., zero OTHER passes on the
// tuple).
//
// Uses the EvaluatorWithRunner signature (ADR-v4-006) because the
// PassRegistry is owned by the *Runner instance, not the package.

// EvaluateSingleActiveRoleInstance is the runner-typed built-in for
// single-active-role-instance.
func EvaluateSingleActiveRoleInstance(_ context.Context, r *Runner, c Clause) (*Result, error) {
	role, err := requireStringArg(c.Args, "role")
	if err != nil {
		return nil, fmt.Errorf("single-active-role-instance: %w", err)
	}
	bctx, err := requireStringArg(c.Args, "context")
	if err != nil {
		return nil, fmt.Errorf("single-active-role-instance: %w", err)
	}
	if r == nil || r.passes == nil {
		// No live-pass view available. Surface as Unevaluated rather
		// than silently passing — the operator should wire the
		// PassRegistry (cmd/ghyll does so at runtime; tests opt in).
		return &Result{
			Unevaluated: true,
			Reason:      "no-pass-registry-attached",
			Details: map[string]any{
				"role":    role,
				"context": bctx,
			},
		}, nil
	}
	conflicting := []string{}
	for _, p := range r.passes.All() {
		if p == nil {
			continue
		}
		if p.Role() != role || p.Context() != bctx {
			continue
		}
		if p.State() != PassStateOpen {
			continue
		}
		// Filter-self: a clause inside an open pass on the same
		// (role, context) MUST NOT conflict with itself.
		if c.PassID != "" && p.ID() == c.PassID {
			continue
		}
		conflicting = append(conflicting, p.ID())
	}
	return &Result{
		Pass: len(conflicting) == 0,
		Details: map[string]any{
			"role":                 role,
			"context":              bctx,
			"conflicting-pass-ids": conflicting,
		},
	}, nil
}
