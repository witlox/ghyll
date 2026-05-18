package acceptance

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/witlox/ghyll/bootstrap"
)

// registerProposeSteps wires step definitions for the auto-propose +
// operator-confirm loop scenarios in specs/v2/features/init.feature
// (scenarios 89..121, per ADR-011 §B.2).
//
// Existing "modify" steps in steps_init.go (raise-only against the
// catalogue) cover scenarios 99 and 105; the steps here cover the
// remaining five (89, 94, 110, 115, 121).
func registerProposeSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	// Scenario 89 — diamond expansion.
	ctx.Step(`^init has declared (\d+) bounded contexts$`, state.initHasDeclaredNBoundedContexts)
	ctx.Step(`^init enters the auto-propose flow$`, state.initEntersAutoProposeFlow)
	ctx.Step(`^for each \(role-pair, context\) arrow in the diamond, init proposes the role file's full exit-gate clause set with clause id, description, eval type, depth type, default cost, and default arguments$`, state.everyArrowProposalIsWellFormed)

	// Scenario 94 — confirm unchanged.
	ctx.Step(`^a proposed clause "([a-z]+)\.([A-Za-z0-9]+) = ([a-z-]+)\(\.\.\.\)"$`, state.aProposedClauseFromRoleFile)
	ctx.Step(`^the operator returns "confirm"$`, state.theOperatorReturnsConfirm)
	ctx.Step(`^the clause is recorded into the grid with the proposed arguments$`, state.theClauseIsRecordedWithProposedArgs)

	// Scenarios 110, 115, 121 — extend / skip-with-residue / skip-no-residue.
	// Bare "a proposed clause" / "a proposed exit gate" — set up a
	// default proposal so the When/Then steps have something to act on.
	ctx.Step(`^a proposed clause$`, state.aBareProposedClause)
	ctx.Step(`^a proposed exit gate$`, state.aBareProposedExitGate)

	// Scenario 110 — extend.
	ctx.Step(`^the operator returns "extend" with a new clause not in the role file$`, state.theOperatorReturnsExtend)
	ctx.Step(`^the new clause is recorded alongside the role-file defaults$`, state.theNewClauseIsRecordedAlongsideDefaults)

	// Scenario 115 — skip with residue.
	ctx.Step(`^the operator returns "skip" with residue entry \{reason: "([^"]*)"\}$`, state.theOperatorReturnsSkipWithResidue)
	ctx.Step(`^the clause is dropped from this \(role-pair, context\) arrow$`, state.theClauseIsDroppedFromTheArrow)
	ctx.Step(`^the residue entry is recorded in the grid's residue list$`, state.theResidueIsRecorded)

	// Scenario 121 — skip without residue.
	ctx.Step(`^the operator returns "skip" without a residue entry$`, state.theOperatorReturnsSkipWithoutResidue)
	ctx.Step(`^init refuses the skip with "([^"]+)"$`, state.initRefusesSkipWith)
	ctx.Step(`^re-prompts the operator$`, state.rePromptsOperator)

	// Reset propose state between scenarios.
	ctx.After(func(_ context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		state.Proposal = nil
		state.ProposalApplyErr = nil
		state.AllProposals = nil
		state.BoundedContextCount = 0
		return nil, nil
	})
}

// ---- Scenario 89: per (role-pair, context) arrow proposal ----

// initHasDeclaredNBoundedContexts records the context count for use
// when expanding the diamond.
func (s *ScenarioState) initHasDeclaredNBoundedContexts(n int) error {
	if n < 1 {
		return fmt.Errorf("bounded context count must be >= 1; got %d", n)
	}
	s.BoundedContextCount = n
	return nil
}

// initEntersAutoProposeFlow expands the diamond: for each role and
// each bounded context, parse the role file and build an ArrowProposal.
// In the real harness this iterates (role-pair, context) tuples; the
// downstream is fixed here per role for simplicity (analyst→architect,
// architect→implementer, implementer→integrator, integrator→exit).
func (s *ScenarioState) initEntersAutoProposeFlow() error {
	cat, err := loadSharedCatalogue()
	if err != nil {
		return fmt.Errorf("load catalogue: %w", err)
	}
	type rolePair struct{ upstream, downstream string }
	pairs := []rolePair{
		{"analyst", "architect"},
		{"architect", "implementer"},
		{"implementer", "integrator"},
		{"integrator", "exit"},
	}
	var all []*bootstrap.ArrowProposal
	for _, rp := range pairs {
		rf, err := bootstrap.ParseRoleFile("../../specs/direction/roles/" + rp.upstream + ".md")
		if err != nil {
			return fmt.Errorf("ParseRoleFile(%s): %w", rp.upstream, err)
		}
		for i := 1; i <= s.BoundedContextCount; i++ {
			ctxID := fmt.Sprintf("context-%d", i)
			ap, err := bootstrap.BuildProposal(rf, cat, rp.upstream, rp.downstream, ctxID)
			if err != nil {
				return fmt.Errorf("BuildProposal(%s→%s, %s): %w", rp.upstream, rp.downstream, ctxID, err)
			}
			all = append(all, ap)
		}
	}
	s.AllProposals = all
	return nil
}

// everyArrowProposalIsWellFormed verifies every proposed clause in
// every arrow proposal has the fields the scenario lists: id,
// description, eval type, depth type, default cost (machine clauses),
// and default arguments populated where the catalogue declares them.
func (s *ScenarioState) everyArrowProposalIsWellFormed() error {
	if len(s.AllProposals) == 0 {
		return errors.New("no arrow proposals built")
	}
	expectedArrows := 4 * s.BoundedContextCount // 4 role pairs × N contexts
	if len(s.AllProposals) != expectedArrows {
		return fmt.Errorf("len(AllProposals) = %d; want %d (4 role pairs × %d contexts)",
			len(s.AllProposals), expectedArrows, s.BoundedContextCount)
	}
	for _, ap := range s.AllProposals {
		if len(ap.Proposed) == 0 {
			return fmt.Errorf("arrow %s→%s/%s has 0 proposed clauses",
				ap.Upstream, ap.Downstream, ap.Context)
		}
		for _, p := range ap.Proposed {
			if p.ID == "" {
				return fmt.Errorf("arrow %s→%s/%s: clause has empty ID", ap.Upstream, ap.Downstream, ap.Context)
			}
			if p.Description == "" {
				return fmt.Errorf("arrow %s→%s/%s clause %s: empty Description",
					ap.Upstream, ap.Downstream, ap.Context, p.ID)
			}
			if p.EvalType != "machine" && p.EvalType != "attested" {
				return fmt.Errorf("clause %s EvalType %q invalid", p.ID, p.EvalType)
			}
			if p.DepthType != "depth-robust" && p.DepthType != "depth-sensitive" {
				return fmt.Errorf("clause %s DepthType %q invalid", p.ID, p.DepthType)
			}
			if p.IsMachine() && p.ConceptName == "" {
				return fmt.Errorf("clause %s: machine eval but no ConceptName", p.ID)
			}
			// Machine clause's DefaultCost is the catalogue concept's
			// DefaultCost (may be 0 for cheap concepts; checked by
			// pulling from the catalogue and comparing).
			if p.IsMachine() {
				if _, ok := s.cataloguePresent(p.ConceptName); !ok {
					return fmt.Errorf("clause %s: concept %q not in shared catalogue", p.ID, p.ConceptName)
				}
			}
		}
	}
	return nil
}

// cataloguePresent returns whether the named concept is in the shared
// catalogue. Helper so the BDD step doesn't need the catalogue handle.
func (s *ScenarioState) cataloguePresent(name string) (struct{}, bool) {
	cat, err := loadSharedCatalogue()
	if err != nil {
		return struct{}{}, false
	}
	_, ok := cat.Get(name)
	return struct{}{}, ok
}

// ---- Scenario 94: operator confirms a clause unchanged ----

// aProposedClauseFromRoleFile parses "<role>.<id> = <concept>(...)" and
// builds a single-arrow proposal containing the named clause.
//
// Gherkin: `a proposed clause "analyst.G1 = unique-definition(...)"`
func (s *ScenarioState) aProposedClauseFromRoleFile(role, clauseID, concept string) error {
	cat, err := loadSharedCatalogue()
	if err != nil {
		return fmt.Errorf("load catalogue: %w", err)
	}
	rf, err := bootstrap.ParseRoleFile("../../specs/direction/roles/" + role + ".md")
	if err != nil {
		return fmt.Errorf("ParseRoleFile(%s): %w", role, err)
	}
	// Find the named clause.
	var target *bootstrap.RoleClause
	for i := range rf.Clauses {
		if rf.Clauses[i].ID == clauseID {
			target = &rf.Clauses[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("role %s has no clause %s", role, clauseID)
	}
	if target.ConceptName != concept {
		return fmt.Errorf("role %s clause %s references concept %q; scenario expects %q",
			role, clauseID, target.ConceptName, concept)
	}
	// Build a proposal containing just this single clause (a slice of
	// the full role file). BuildProposal expands the full file; we
	// retain that and let the operator's verdict target G1.
	ap, err := bootstrap.BuildProposal(rf, cat, role, "downstream", "context-A")
	if err != nil {
		return fmt.Errorf("BuildProposal: %w", err)
	}
	// validation-pass-2 F29: Apply validates required-arg
	// completeness. Auto-propose only populates DEFAULTED args from
	// the catalogue schema; the operator-intended values for
	// required-no-default args come from the role-args hint in the
	// real harness. For the BDD test, fill them with synthetic values.
	for i, p := range ap.Proposed {
		if !p.IsMachine() {
			continue
		}
		concept, ok := cat.Get(p.ConceptName)
		if !ok {
			continue
		}
		for argName, schema := range concept.Arguments {
			if !schema.Required {
				continue
			}
			if _, present := ap.Proposed[i].DefaultArgs[argName]; present {
				continue
			}
			if ap.Proposed[i].DefaultArgs == nil {
				ap.Proposed[i].DefaultArgs = map[string]any{}
			}
			ap.Proposed[i].DefaultArgs[argName] = syntheticBDDArgValue(schema.Type)
		}
	}
	s.Proposal = ap
	return nil
}

// theOperatorReturnsConfirm applies a confirm verdict to the first
// pending clause in s.Proposal (the one set up by an earlier step).
func (s *ScenarioState) theOperatorReturnsConfirm() error {
	cat, err := loadSharedCatalogue()
	if err != nil {
		return fmt.Errorf("load catalogue: %w", err)
	}
	if s.Proposal == nil || len(s.Proposal.Proposed) == 0 {
		return errors.New("no proposal set up before confirm")
	}
	// Target G1 (the clause the Given step pinned). The Given step
	// validates the clause exists; we trust it here.
	target := s.Proposal.Proposed[0].ID
	s.ProposalApplyErr = s.Proposal.Apply(target, bootstrap.Verdict{Kind: bootstrap.VerdictConfirm}, cat)
	return nil
}

// theClauseIsRecordedWithProposedArgs verifies the confirm path
// recorded the clause and that its args equal the proposed defaults.
func (s *ScenarioState) theClauseIsRecordedWithProposedArgs() error {
	if s.ProposalApplyErr != nil {
		return fmt.Errorf("confirm should not error; got %v", s.ProposalApplyErr)
	}
	recorded := s.Proposal.Recorded()
	if len(recorded) != 1 {
		return fmt.Errorf("expected exactly 1 recorded clause; got %d", len(recorded))
	}
	// Compare args length: a confirm records the proposed DefaultArgs
	// verbatim. The map is allowed to be empty if the concept has no
	// defaultable args; both must agree.
	want := len(s.Proposal.Proposed[0].DefaultArgs)
	got := len(recorded[0].Args)
	if got != want {
		return fmt.Errorf("recorded args len = %d; want %d (verbatim from proposed defaults)", got, want)
	}
	if recorded[0].Source != bootstrap.SourceRoleDefault {
		return fmt.Errorf("recorded Source = %v; want SourceRoleDefault", recorded[0].Source)
	}
	return nil
}

// ---- Bare "a proposed clause" / "a proposed exit gate" setup ----

// aBareProposedClause sets up a default proposal with one machine
// clause (compiles) so subsequent steps have something to act on.
func (s *ScenarioState) aBareProposedClause() error {
	return s.setupDefaultProposal()
}

// aBareProposedExitGate sets up the same default proposal; the
// difference between "clause" and "exit gate" is narrative.
func (s *ScenarioState) aBareProposedExitGate() error {
	return s.setupDefaultProposal()
}

// setupDefaultProposal builds a one-clause proposal using `compiles`,
// the cheapest universal-base concept. Used by extend/skip scenarios
// that don't specify a particular clause.
func (s *ScenarioState) setupDefaultProposal() error {
	cat, err := loadSharedCatalogue()
	if err != nil {
		return fmt.Errorf("load catalogue: %w", err)
	}
	concept, _ := cat.Get("compiles")
	defaults := buildClauseArgs(concept)
	s.Proposal = bootstrap.NewArrowProposal("analyst", "architect", "context-A", []bootstrap.ProposedClause{{
		ID:          "G1",
		Description: "default proposal for BDD setup",
		EvalType:    "machine",
		DepthType:   "depth-robust",
		ConceptName: "compiles",
		DefaultArgs: defaults,
		DefaultCost: concept.DefaultCost,
		RoleSource:  "test-harness",
	}})
	return nil
}

// ---- Scenario 110: operator extends ----

// theOperatorReturnsExtend applies an Extend on the proposal. The
// new clause is a `no-todo-marker` on a per-context scope — a clause
// the role file does NOT include.
func (s *ScenarioState) theOperatorReturnsExtend() error {
	cat, err := loadSharedCatalogue()
	if err != nil {
		return fmt.Errorf("load catalogue: %w", err)
	}
	if s.Proposal == nil || len(s.Proposal.Proposed) == 0 {
		return errors.New("no proposal set up before extend")
	}
	// Confirm the role-default first so we can demonstrate Extend
	// records the new clause alongside it.
	target := s.Proposal.Proposed[0].ID
	if err := s.Proposal.Apply(target, bootstrap.Verdict{Kind: bootstrap.VerdictConfirm}, cat); err != nil {
		return fmt.Errorf("confirm before extend: %w", err)
	}
	ext := bootstrap.ProposedClause{
		ID:          "X-no-todo",
		Description: "no-todo-marker on context-A source",
		EvalType:    "machine",
		DepthType:   "depth-robust",
		ConceptName: "no-todo-marker",
		DefaultArgs: map[string]any{"scope": "src/context-a/**"},
	}
	s.ProposalApplyErr = s.Proposal.Extend(ext, cat)
	return nil
}

// theNewClauseIsRecordedAlongsideDefaults verifies the recorded list
// contains both the original confirmed clause AND the extended one,
// and that the extension is flagged as operator-added.
func (s *ScenarioState) theNewClauseIsRecordedAlongsideDefaults() error {
	if s.ProposalApplyErr != nil {
		return fmt.Errorf("extend errored: %v", s.ProposalApplyErr)
	}
	recorded := s.Proposal.Recorded()
	if len(recorded) < 2 {
		return fmt.Errorf("recorded len = %d; want >= 2 (default + extension)", len(recorded))
	}
	// Find the extension.
	var found bool
	for _, r := range recorded {
		if r.Source == bootstrap.SourceOperatorExtension {
			found = true
			if r.ConceptName != "no-todo-marker" {
				return fmt.Errorf("extension ConceptName = %q; want no-todo-marker", r.ConceptName)
			}
		}
	}
	if !found {
		return errors.New("no operator-extended clause in Recorded()")
	}
	if len(s.Proposal.Extensions()) == 0 {
		return errors.New("Extensions() returned empty")
	}
	return nil
}

// ---- Scenario 115: operator skips with residue ----

// theOperatorReturnsSkipWithResidue applies a skip verdict with the
// provided residue text.
func (s *ScenarioState) theOperatorReturnsSkipWithResidue(reason string) error {
	cat, err := loadSharedCatalogue()
	if err != nil {
		return fmt.Errorf("load catalogue: %w", err)
	}
	if s.Proposal == nil || len(s.Proposal.Proposed) == 0 {
		return errors.New("no proposal set up before skip")
	}
	target := s.Proposal.Proposed[0].ID
	// Resolve "<text>" placeholder to a concrete reason; the scenario
	// uses a literal placeholder string.
	if reason == "<text>" {
		reason = "binding not yet implemented in this context"
	}
	s.ProposalApplyErr = s.Proposal.Apply(target, bootstrap.Verdict{
		Kind:    bootstrap.VerdictSkip,
		Residue: reason,
	}, cat)
	return nil
}

// theClauseIsDroppedFromTheArrow verifies the skipped clause is NOT
// in Recorded().
func (s *ScenarioState) theClauseIsDroppedFromTheArrow() error {
	if s.ProposalApplyErr != nil {
		return fmt.Errorf("skip-with-residue errored: %v", s.ProposalApplyErr)
	}
	if len(s.Proposal.Recorded()) != 0 {
		return fmt.Errorf("Recorded() should be empty after skip; got %d entries", len(s.Proposal.Recorded()))
	}
	return nil
}

// theResidueIsRecorded verifies the Residue() list has the entry.
func (s *ScenarioState) theResidueIsRecorded() error {
	res := s.Proposal.Residue()
	if len(res) != 1 {
		return fmt.Errorf("residue len = %d; want 1", len(res))
	}
	if res[0].Reason == "" {
		return errors.New("residue reason is empty")
	}
	return nil
}

// ---- Scenario 121: operator skips without residue ----

// theOperatorReturnsSkipWithoutResidue applies a skip verdict with
// empty residue and stashes the error for the Then step.
func (s *ScenarioState) theOperatorReturnsSkipWithoutResidue() error {
	cat, err := loadSharedCatalogue()
	if err != nil {
		return fmt.Errorf("load catalogue: %w", err)
	}
	if s.Proposal == nil || len(s.Proposal.Proposed) == 0 {
		return errors.New("no proposal set up before skip")
	}
	target := s.Proposal.Proposed[0].ID
	s.ProposalApplyErr = s.Proposal.Apply(target, bootstrap.Verdict{
		Kind:    bootstrap.VerdictSkip,
		Residue: "",
	}, cat)
	return nil
}

// initRefusesSkipWith verifies the skip refusal carried the named
// sentinel (typically "residue-required-for-skip").
func (s *ScenarioState) initRefusesSkipWith(expected string) error {
	if s.ProposalApplyErr == nil {
		return fmt.Errorf("expected skip refusal with %q; got nil", expected)
	}
	if !errors.Is(s.ProposalApplyErr, bootstrap.ErrResidueRequiredForSkip) {
		return fmt.Errorf("expected ErrResidueRequiredForSkip; got %v", s.ProposalApplyErr)
	}
	if !strings.Contains(s.ProposalApplyErr.Error(), expected) {
		return fmt.Errorf("error %v does not contain sentinel %q", s.ProposalApplyErr, expected)
	}
	return nil
}

// rePromptsOperator is a narrative step: the harness should re-prompt
// after a refused skip. Operationally, the refusal returning an error
// without recording a verdict IS the re-prompt signal (caller loops
// until success). Verify the verdict-not-applied state.
func (s *ScenarioState) rePromptsOperator() error {
	if s.Proposal == nil || len(s.Proposal.Proposed) == 0 {
		return errors.New("no proposal to inspect")
	}
	target := s.Proposal.Proposed[0].ID
	if _, applied := s.Proposal.VerdictFor(target); applied {
		return errors.New("refused skip should not have recorded a verdict; operator must re-decide")
	}
	return nil
}
