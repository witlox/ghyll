package acceptance

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/witlox/ghyll/bootstrap"
)

// registerBindingSteps wires step definitions for the re-init-on-
// missing-binding scenarios in specs/features/init.feature (49, 59).
//
// The flow under test: the runner asks the grid whether every binding
// some pending evaluation requires is declared. If any is missing,
// CheckRequiredBindings returns a *MissingBindingError listing ALL
// gaps; the runner suspends, init re-enters scoped to that list, the
// operator declares the bindings, and evaluation resumes.
func registerBindingSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	// Scenario 49 — single missing binding.
	ctx.Step(`^a project with an initialized grid$`, state.aProjectWithInitializedGrid)
	ctx.Step(`^a declared ([a-z][a-z0-9-]*) clause references language "([^"]+)"$`, state.aDeclaredClauseReferencesLanguage)
	ctx.Step(`^the project has no ([a-z][a-z0-9-]*)\.([a-z]+) binding$`, state.theProjectHasNoBinding)
	ctx.Step(`^the harness attempts to evaluate the clause$`, state.theHarnessAttemptsToEvaluateTheClause)
	ctx.Step(`^the harness suspends the current pass with reason "([^"]+)"$`, state.theHarnessSuspendsWithReason)
	ctx.Step(`^the harness re-enters init scoped to the missing binding only$`, state.theHarnessReentersScopedToMissingBinding)
	ctx.Step(`^the operator declares the binding$`, state.theOperatorDeclaresTheBinding)
	ctx.Step(`^the suspended pass resumes against the now-complete config$`, state.theSuspendedPassResumes)

	// Scenario 59 — multiple missing bindings.
	ctx.Step(`^a pass that references three bindings, two of which are missing$`, state.aPassWithThreeRefsTwoMissing)
	ctx.Step(`^the harness suspends and re-enters init$`, state.theHarnessSuspendsAndReenters)
	ctx.Step(`^init collects all missing bindings and presents them together for operator declaration in a single re-entry$`, state.initCollectsAllMissing)

	// Per-scenario cleanup of binding-test state.
	ctx.After(func(_ context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		state.BindingGrid = nil
		state.RequiredBindings = nil
		state.PendingClauseConcept = ""
		state.PendingClauseLanguage = ""
		state.MissingBindingErr = nil
		return nil, nil
	})
}

// ---- Scenario 49: single missing binding ----

// aProjectWithInitializedGrid stands up a fresh in-memory grid the
// rest of the binding flow operates against. We don't persist to disk
// here — the binding-availability check is in-process.
func (s *ScenarioState) aProjectWithInitializedGrid() error {
	s.BindingGrid = bootstrap.NewGrid("alice@example.com")
	return nil
}

// aDeclaredClauseReferencesLanguage records the (concept, language)
// the upcoming evaluation will need a binding for. Scenario 49 calls
// this with concept=mutation-score, language=rust.
func (s *ScenarioState) aDeclaredClauseReferencesLanguage(concept, language string) error {
	if s.BindingGrid == nil {
		return errors.New("no initialized grid; precondition unmet")
	}
	s.PendingClauseConcept = concept
	s.PendingClauseLanguage = language
	s.RequiredBindings = []bootstrap.BindingKey{
		{Concept: concept, Language: language},
	}
	return nil
}

// theProjectHasNoBinding is an assertion step: the named binding is
// not declared on the grid. Scenario 49 uses this to set up the
// missing-binding condition explicitly.
func (s *ScenarioState) theProjectHasNoBinding(concept, language string) error {
	if s.BindingGrid == nil {
		return errors.New("no grid")
	}
	if _, exists := s.BindingGrid.LookupBinding(concept, language); exists {
		return fmt.Errorf("binding %s.%s was declared but scenario says it isn't", concept, language)
	}
	return nil
}

// theHarnessAttemptsToEvaluateTheClause runs CheckRequiredBindings —
// the harness-side check that drives the suspend. The error is
// stashed for subsequent Then steps to inspect.
func (s *ScenarioState) theHarnessAttemptsToEvaluateTheClause() error {
	if s.BindingGrid == nil || len(s.RequiredBindings) == 0 {
		return errors.New("required bindings not set up")
	}
	s.MissingBindingErr = s.BindingGrid.CheckRequiredBindings(s.RequiredBindings)
	return nil
}

// theHarnessSuspendsWithReason verifies the suspend reason matches
// the expected sentinel.
func (s *ScenarioState) theHarnessSuspendsWithReason(reason string) error {
	if s.MissingBindingErr == nil {
		return fmt.Errorf("expected suspend with reason %q; no error returned", reason)
	}
	if !errors.Is(s.MissingBindingErr, bootstrap.ErrMissingBinding) {
		return fmt.Errorf("expected ErrMissingBinding; got %v", s.MissingBindingErr)
	}
	if !strings.Contains(s.MissingBindingErr.Error(), reason) {
		return fmt.Errorf("error %q does not contain reason %q", s.MissingBindingErr, reason)
	}
	return nil
}

// theHarnessReentersScopedToMissingBinding verifies the re-entry's
// scope: the missing-binding error names exactly the bindings the
// runner needs declared. "Scoped to the missing binding only" means
// re-init doesn't drag in unrelated work.
func (s *ScenarioState) theHarnessReentersScopedToMissingBinding() error {
	mbe := bootstrap.AsMissingBindingError(s.MissingBindingErr)
	if mbe == nil {
		return errors.New("no MissingBindingError to scope re-entry from")
	}
	if len(mbe.Missing) != len(s.RequiredBindings) {
		// Scenario 49 sets up exactly one required binding, all
		// missing; re-entry scope = the missing list.
		return fmt.Errorf("re-entry scope has %d bindings; want %d (the missing set, not all required)",
			len(mbe.Missing), len(s.RequiredBindings))
	}
	return nil
}

// theOperatorDeclaresTheBinding declares the previously-missing
// binding on the grid, simulating the operator's re-entry action.
// Uses a synthetic command since the real value is operator-supplied;
// the test cares about the absence-to-presence transition.
func (s *ScenarioState) theOperatorDeclaresTheBinding() error {
	mbe := bootstrap.AsMissingBindingError(s.MissingBindingErr)
	if mbe == nil {
		return errors.New("no missing binding to declare")
	}
	for _, k := range mbe.Missing {
		cmd := fmt.Sprintf("synthetic-%s-binding-for-%s", k.Language, k.Concept)
		if err := s.BindingGrid.DeclareBinding(k.Concept, k.Language, cmd); err != nil {
			return fmt.Errorf("DeclareBinding %s.%s: %w", k.Concept, k.Language, err)
		}
	}
	return nil
}

// theSuspendedPassResumes re-runs CheckRequiredBindings against the
// now-complete config; expects nil. This is the "resume" step.
func (s *ScenarioState) theSuspendedPassResumes() error {
	if err := s.BindingGrid.CheckRequiredBindings(s.RequiredBindings); err != nil {
		return fmt.Errorf("pass should resume cleanly; got %v", err)
	}
	return nil
}

// ---- Scenario 59: multiple missing bindings ----

// aPassWithThreeRefsTwoMissing sets up the scenario-59 condition: a
// grid with one of three required bindings declared; the other two
// are missing. We pick concrete (concept, language) pairs from the
// catalogue.
func (s *ScenarioState) aPassWithThreeRefsTwoMissing() error {
	s.BindingGrid = bootstrap.NewGrid("alice@example.com")
	// Declare one of the three (lint-clean.go).
	if err := s.BindingGrid.DeclareBinding("lint-clean", "go", "staticcheck"); err != nil {
		return fmt.Errorf("declare lint-clean.go: %w", err)
	}
	// Three required; two missing.
	s.RequiredBindings = []bootstrap.BindingKey{
		{Concept: "lint-clean", Language: "go"},
		{Concept: "mutation-score", Language: "rust"},
		{Concept: "tests-pass", Language: "python"},
	}
	return nil
}

// theHarnessSuspendsAndReenters runs the check and confirms a
// suspend was triggered. The Then step inspects the count.
func (s *ScenarioState) theHarnessSuspendsAndReenters() error {
	if s.BindingGrid == nil {
		return errors.New("no grid")
	}
	s.MissingBindingErr = s.BindingGrid.CheckRequiredBindings(s.RequiredBindings)
	if s.MissingBindingErr == nil {
		return errors.New("expected missing-binding suspend; got nil")
	}
	if !errors.Is(s.MissingBindingErr, bootstrap.ErrMissingBinding) {
		return fmt.Errorf("expected ErrMissingBinding; got %v", s.MissingBindingErr)
	}
	return nil
}

// initCollectsAllMissing verifies the Missing list has exactly two
// entries (matching the scenario's "two of which are missing").
// Operator declaration happens in a single re-entry because the
// error carries the full list.
func (s *ScenarioState) initCollectsAllMissing() error {
	mbe := bootstrap.AsMissingBindingError(s.MissingBindingErr)
	if mbe == nil {
		return errors.New("no MissingBindingError")
	}
	if len(mbe.Missing) != 2 {
		return fmt.Errorf("Missing has %d entries; want 2 ("+
			strconv.Itoa(len(s.RequiredBindings))+" required, 1 declared)",
			len(mbe.Missing))
	}
	// Sanity: both missing entries are concept.language strings.
	for _, k := range mbe.Missing {
		if k.Concept == "" || k.Language == "" {
			return fmt.Errorf("malformed missing key %+v", k)
		}
	}
	return nil
}
