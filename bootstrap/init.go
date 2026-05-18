package bootstrap

import (
	"errors"
	"fmt"
)

// GridDefaults names the policy values BuildInitGrid bakes into the
// new grid. Per validation-pass-2 F32: previously these came from
// NewGrid's hardcoded defaults — the operator never saw them. Now
// the caller explicitly supplies (or explicitly accepts the
// hardcoded defaults via DefaultGridDefaults()) so the init flow has
// a documented decision point.
type GridDefaults struct {
	SeverityThreshold          string
	DepthLadder                []DepthLadderTier
	InsufficientBasisRoundsMax int
	RemediationRoundsMax       int
}

// DefaultGridDefaults returns the hardcoded ghyll defaults
// (severity-threshold=medium, 4-tier ladder, 3/5 round caps). Calling
// this is the explicit "I accept the defaults" path; passing custom
// values is the policy-tuning path.
func DefaultGridDefaults() GridDefaults {
	return GridDefaults{
		SeverityThreshold:          "medium",
		DepthLadder:                DefaultDepthLadder(),
		InsufficientBasisRoundsMax: 3,
		RemediationRoundsMax:       5,
	}
}

// validate rejects GridDefaults values that are obviously wrong.
func (d GridDefaults) validate() error {
	if d.SeverityThreshold == "" {
		return errors.New("GridDefaults: SeverityThreshold empty")
	}
	if len(d.DepthLadder) == 0 {
		return errors.New("GridDefaults: DepthLadder empty")
	}
	if d.InsufficientBasisRoundsMax < 1 {
		return errors.New("GridDefaults: InsufficientBasisRoundsMax must be >= 1")
	}
	if d.RemediationRoundsMax < 1 {
		return errors.New("GridDefaults: RemediationRoundsMax must be >= 1")
	}
	return nil
}

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
	ErrInitRefusalUnresolved   = errors.New("init-refusal-proposed-but-unresolved")
	ErrInitDefaultsNotReviewed = errors.New("init-grid-defaults-not-reviewed")
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
// BuildInitGrid composes a *Grid using the hardcoded
// DefaultGridDefaults. Use BuildInitGridWith to pass operator-tuned
// policy values explicitly.
func BuildInitGrid(opID string, profile *ProjectProfile, proposals []*ArrowProposal) (*Grid, error) {
	return BuildInitGridWith(opID, profile, proposals, DefaultGridDefaults())
}

// BuildInitGridWith is the explicit-defaults variant. The init flow
// SHOULD prefer this so policy values are operator-visible
// (validation-pass-2 F32).
func BuildInitGridWith(opID string, profile *ProjectProfile, proposals []*ArrowProposal, defaults GridDefaults) (*Grid, error) {
	if err := defaults.validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInitDefaultsNotReviewed, err)
	}
	return buildInitGridImpl(opID, profile, proposals, defaults)
}

func buildInitGridImpl(opID string, profile *ProjectProfile, proposals []*ArrowProposal, defaults GridDefaults) (*Grid, error) {
	if opID == "" {
		return nil, ErrInitOpIDEmpty
	}
	if profile == nil {
		return nil, ErrInitProfileNil
	}
	if profile.RefusalAccepted() {
		return nil, ErrInitRefusalAccepted
	}
	// Validation-pass-2 F37: if refusal was proposed but never
	// accepted or overridden, the operator hasn't resolved the
	// decision. Refuse rather than silently bypass the refusal flow.
	if profile.RefusalProposed() {
		refusal := profile.Refusal()
		if refusal == nil || (!refusal.Accepted && refusal.OverrideResidue == "") {
			return nil, fmt.Errorf("%w", ErrInitRefusalUnresolved)
		}
	}
	if len(proposals) == 0 {
		return nil, ErrInitProposalsEmpty
	}

	// Snapshot the bounded contexts under the profile mutex (F9).
	contexts := profile.BoundedContextsSnapshot()
	declared := make(map[string]struct{}, len(contexts))
	for _, c := range contexts {
		declared[c.ID] = struct{}{}
	}

	for _, ap := range proposals {
		if ap == nil {
			return nil, errors.New("BuildInitGrid: nil ArrowProposal in proposals")
		}
		if !ap.AllVerdictsReceived() {
			ap.mu.Lock()
			haveCount := len(ap.verdicts)
			wantCount := len(ap.Proposed)
			ap.mu.Unlock()
			return nil, fmt.Errorf("%w: arrow %s→%s/%s has %d of %d verdicts",
				ErrInitVerdictsIncomplete, ap.Upstream, ap.Downstream, ap.Context,
				haveCount, wantCount)
		}
		if _, ok := declared[ap.Context]; !ok {
			return nil, fmt.Errorf("%w: %q (arrow %s→%s)",
				ErrInitContextNotInProfile, ap.Context, ap.Upstream, ap.Downstream)
		}
	}

	g := NewGrid(opID)
	g.BoundedContexts = contexts
	// Apply operator-supplied defaults over the NewGrid baseline.
	g.SeverityThreshold = defaults.SeverityThreshold
	g.DepthLadder = append([]DepthLadderTier(nil), defaults.DepthLadder...)
	g.InsufficientBasisRoundsMax = defaults.InsufficientBasisRoundsMax
	g.RemediationRoundsMax = defaults.RemediationRoundsMax

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
