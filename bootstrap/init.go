package bootstrap

import (
	"errors"
	"fmt"
)

// End-of-init grid assembly.
//
// After sub-phase A (profile + contexts) and sub-phase B (auto-propose
// + operator verdicts) have completed, init has the inputs needed to
// write the project's first grid: bounded contexts, language bindings,
// and the per-(role-pair, context) arrow exit gates with their
// recorded clauses and residue.
//
// BuildInitGrid assembles those inputs into a *Grid the caller
// persists via Grid.Write (atomic temp+rename per ADR-010). It does
// not write to disk itself — callers separate "ready to write" from
// "writing" so the grid can be inspected, attested over, or sent
// through a dry-run before persistence.

// BuildInit errors.
var (
	ErrInitOpIDEmpty           = errors.New("init-op-id-empty")
	ErrInitProfileNil          = errors.New("init-profile-nil")
	ErrInitProposalsEmpty      = errors.New("init-proposals-empty")
	ErrInitVerdictsIncomplete  = errors.New("init-verdicts-incomplete")
	ErrInitContextNotInProfile = errors.New("init-arrow-context-not-in-profile")
	ErrInitRefusalAccepted     = errors.New("init-refusal-accepted")
)

// BuildInitGrid composes a *Grid from the init flow's outputs. The
// returned grid is at version 1 (the first grid this project has);
// subsequent amendments produce grid.v2.yaml, v3.yaml, etc.
//
// Preconditions:
//
//   - opID is non-empty. Init must have a session before writing.
//   - profile is non-nil. (A nil profile means sub-phase A never ran.)
//   - If profile.RefusalAccepted() is true, BuildInitGrid refuses with
//     ErrInitRefusalAccepted. The operator accepted refusal; no grid
//     should exist.
//   - proposals contains at least one arrow proposal.
//   - Every proposal's AllVerdictsReceived() is true. ADR-011 step 3:
//     "Grid is recorded only after every proposed clause has received
//     a verdict."
//   - Every proposal's Context exists in profile.BoundedContexts. An
//     arrow for an undeclared context is a caller bug; we refuse it
//     here as a defense-in-depth check.
//
// Each ArrowProposal becomes one entry in Grid.Arrows; each
// ResidueEntry becomes one entry in Grid.Residue. The arrow shape
// (untyped map) carries upstream/downstream/context + a clauses
// list of {id, eval, depth, concept, args, cost, source}. The
// residue shape carries {arrow, clause-id, concept, reason}.
//
// Grid.LanguageBindings is left as the caller supplied (typically
// empty for greenfield first init; the runner declares missing
// bindings via Grid.DeclareBinding during re-entry per D18).
func BuildInitGrid(opID string, profile *ProjectProfile, proposals []*ArrowProposal) (*Grid, error) {
	if opID == "" {
		return nil, ErrInitOpIDEmpty
	}
	if profile == nil {
		return nil, ErrInitProfileNil
	}
	if profile.RefusalAccepted() {
		return nil, ErrInitRefusalAccepted
	}
	if len(proposals) == 0 {
		return nil, ErrInitProposalsEmpty
	}

	// Index profile contexts so we can reject arrows pointing at
	// undeclared ones.
	declared := make(map[string]struct{}, len(profile.BoundedContexts))
	for _, c := range profile.BoundedContexts {
		declared[c.ID] = struct{}{}
	}

	for _, ap := range proposals {
		if ap == nil {
			return nil, errors.New("BuildInitGrid: nil ArrowProposal in proposals")
		}
		if !ap.AllVerdictsReceived() {
			return nil, fmt.Errorf("%w: arrow %s→%s/%s has %d of %d verdicts",
				ErrInitVerdictsIncomplete, ap.Upstream, ap.Downstream, ap.Context,
				len(ap.verdicts), len(ap.Proposed))
		}
		if _, ok := declared[ap.Context]; !ok {
			return nil, fmt.Errorf("%w: %q (arrow %s→%s)",
				ErrInitContextNotInProfile, ap.Context, ap.Upstream, ap.Downstream)
		}
	}

	g := NewGrid(opID)
	g.BoundedContexts = append([]BoundedContext(nil), profile.BoundedContexts...)

	for _, ap := range proposals {
		g.Arrows = append(g.Arrows, serializeArrow(ap))
		for _, r := range ap.Residue() {
			g.Residue = append(g.Residue, serializeResidue(ap, r))
		}
	}

	return g, nil
}

// serializeArrow turns one ArrowProposal into the untyped map shape
// Grid.Arrows uses. Field names match the YAML conventions in
// gates.md (kebab-case).
func serializeArrow(ap *ArrowProposal) map[string]any {
	clauses := make([]map[string]any, 0, len(ap.Recorded()))
	for _, c := range ap.Recorded() {
		entry := map[string]any{
			"id":     c.ID,
			"eval":   c.EvalType,
			"depth":  c.DepthType,
			"cost":   c.Cost,
			"source": clauseSourceString(c.Source),
		}
		if c.ConceptName != "" {
			entry["concept"] = c.ConceptName
		}
		if len(c.Args) > 0 {
			entry["args"] = c.Args
		}
		if c.Description != "" {
			entry["description"] = c.Description
		}
		clauses = append(clauses, entry)
	}
	return map[string]any{
		"upstream":   ap.Upstream,
		"downstream": ap.Downstream,
		"context":    ap.Context,
		"clauses":    clauses,
	}
}

// serializeResidue turns one ResidueEntry into the untyped map shape
// Grid.Residue uses. Includes the arrow identity so the runner can
// trace residue back to the (role-pair, context) it came from.
func serializeResidue(ap *ArrowProposal, r ResidueEntry) map[string]any {
	entry := map[string]any{
		"arrow":     fmt.Sprintf("%s→%s/%s", ap.Upstream, ap.Downstream, ap.Context),
		"clause-id": r.ClauseID,
		"reason":    r.Reason,
	}
	if r.ConceptName != "" {
		entry["concept"] = r.ConceptName
	}
	if r.Description != "" {
		entry["description"] = r.Description
	}
	return entry
}

// clauseSourceString returns the wire form for a ClauseSource (used
// in YAML serialization).
func clauseSourceString(s ClauseSource) string {
	switch s {
	case SourceRoleDefault:
		return "role-default"
	case SourceOperatorExtension:
		return "operator-extension"
	default:
		return "unknown"
	}
}
