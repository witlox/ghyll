package acceptance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cucumber/godog"

	"github.com/witlox/ghyll/bootstrap"
)

// registerProfileSteps wires step definitions for init sub-phase A:
// project profile + greenfield/brownfield detection + operator context
// declaration. Covers scenarios 11, 19, 33 in
// specs/v2/features/init.feature.
func registerProfileSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	// Scenario 11 — empty repository / greenfield.
	ctx.Step(`^a project directory with no source files and no prior grid$`, state.aProjectDirWithNoSourceAndNoPriorGrid)
	ctx.Step(`^the operator runs "ghyll init"$`, state.theOperatorRunsGhyllInit)
	ctx.Step(`^declares op-id "([^"]+)"$`, state.declaresOpID)
	ctx.Step(`^init proposes the diamond as the declared arrow set$`, state.initProposesTheDiamond)
	ctx.Step(`^init proposes (\d+) bounded contexts initially$`, state.initProposesNBoundedContextsInitially)
	ctx.Step(`^init interrogates the operator to identify bounded contexts from intent$`, state.initInterrogatesForContexts)

	// Scenario 19 — operator declares contexts via interrogation.
	ctx.Step(`^a greenfield init in progress$`, state.aGreenfieldInitInProgress)
	ctx.Step(`^the operator has answered the context-identification interrogation with \["([^"]+)", "([^"]+)"\]$`, state.theOperatorAnsweredWithTwoContexts)
	ctx.Step(`^init records (\d+) bounded contexts$`, state.initRecordsNBoundedContexts)
	ctx.Step(`^init auto-proposes role exit-gate clauses for each \(role-pair, context\) arrow per the auto-propose feature$`, state.initAutoProposesPerArrow)

	// Scenario 33 — brownfield discovery.
	ctx.Step(`^a project directory with existing source code in src/([A-Za-z0-9_-]+)/ and src/([A-Za-z0-9_-]+)/$`, state.aProjectDirWithSourceInTwoSubdirs)
	ctx.Step(`^no prior \.ghyll/grid\.current file exists$`, state.noPriorGridCurrentExists)
	ctx.Step(`^init runs the mode-determinable-from-repo rule and determines mode = brownfield$`, state.initDeterminesBrownfieldMode)
	ctx.Step(`^init proposes bounded contexts \["([^"]+)", "([^"]+)"\] based on directory structure$`, state.initProposesContextsFromDirStructure)
	ctx.Step(`^the operator confirms or refines the proposal$`, state.theOperatorConfirmsOrRefines)

	// Per-scenario cleanup for the profile tempdir.
	ctx.After(func(_ context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if state.ProjectTestDir != "" {
			_ = os.RemoveAll(state.ProjectTestDir)
			state.ProjectTestDir = ""
		}
		state.Profile = nil
		state.ProfileErr = nil
		return nil, nil
	})
}

// ensureProjectTestDir creates a per-scenario tempdir on first use.
func (s *ScenarioState) ensureProjectTestDir() error {
	if s.ProjectTestDir != "" {
		return nil
	}
	dir, err := os.MkdirTemp("", "ghyll-profile-test-")
	if err != nil {
		return fmt.Errorf("ensureProjectTestDir: %w", err)
	}
	s.ProjectTestDir = dir
	return nil
}

// ---- Scenario 11: empty repository (greenfield) ----

// aProjectDirWithNoSourceAndNoPriorGrid creates an empty tempdir.
func (s *ScenarioState) aProjectDirWithNoSourceAndNoPriorGrid() error {
	return s.ensureProjectTestDir()
}

// theOperatorRunsGhyllInit profiles the test directory and stashes
// the resulting ProjectProfile.
func (s *ScenarioState) theOperatorRunsGhyllInit() error {
	if err := s.ensureProjectTestDir(); err != nil {
		return err
	}
	s.Profile, s.ProfileErr = bootstrap.ProfileRepo(s.ProjectTestDir)
	return s.ProfileErr
}

// declaresOpID starts an operator session with the given op-id, the
// "And declares op-id ..." continuation in scenario 11.
func (s *ScenarioState) declaresOpID(opID string) error {
	sess, err := bootstrap.StartSession(opID)
	if err != nil {
		s.OperatorSessionErr = err
		return fmt.Errorf("StartSession: %w", err)
	}
	s.OperatorSession = sess
	return nil
}

// initProposesTheDiamond verifies the diamond's four roles are declared.
func (s *ScenarioState) initProposesTheDiamond() error {
	if s.Profile == nil {
		return errors.New("no profile built; ghyll init step must run first")
	}
	if len(s.Profile.DiamondRoles) != 4 {
		return fmt.Errorf("DiamondRoles len = %d; want 4 (the fixed diamond per ADR-003)",
			len(s.Profile.DiamondRoles))
	}
	expected := bootstrap.FixedDiamondRoles
	for i, role := range s.Profile.DiamondRoles {
		if role != expected[i] {
			return fmt.Errorf("DiamondRoles[%d] = %q; want %q", i, role, expected[i])
		}
	}
	return nil
}

// initProposesNBoundedContextsInitially asserts the initial context count.
func (s *ScenarioState) initProposesNBoundedContextsInitially(n int) error {
	if s.Profile == nil {
		return errors.New("no profile built")
	}
	if len(s.Profile.BoundedContexts) != n {
		return fmt.Errorf("initial BoundedContexts = %d; want %d", len(s.Profile.BoundedContexts), n)
	}
	return nil
}

// initInterrogatesForContexts verifies the profile reports it needs
// operator interrogation to identify contexts.
func (s *ScenarioState) initInterrogatesForContexts() error {
	if s.Profile == nil {
		return errors.New("no profile built")
	}
	if !s.Profile.NeedsContextInterrogation() {
		return errors.New("profile should need context interrogation; got false")
	}
	return nil
}

// ---- Scenario 19: operator declares contexts via interrogation ----

// aGreenfieldInitInProgress sets up an empty tempdir and runs
// ProfileRepo so the test starts from a fresh greenfield profile.
func (s *ScenarioState) aGreenfieldInitInProgress() error {
	if err := s.ensureProjectTestDir(); err != nil {
		return err
	}
	p, err := bootstrap.ProfileRepo(s.ProjectTestDir)
	if err != nil {
		return fmt.Errorf("ProfileRepo: %w", err)
	}
	if !p.IsGreenfield() {
		return fmt.Errorf("expected greenfield; got %q", p.Mode)
	}
	s.Profile = p
	return nil
}

// theOperatorAnsweredWithTwoContexts records two contexts onto the
// profile (the interrogation's outcome).
func (s *ScenarioState) theOperatorAnsweredWithTwoContexts(ctxA, ctxB string) error {
	if s.Profile == nil {
		return errors.New("no greenfield profile in progress")
	}
	if err := s.Profile.DeclareContext(ctxA, ""); err != nil {
		return fmt.Errorf("DeclareContext(%q): %w", ctxA, err)
	}
	if err := s.Profile.DeclareContext(ctxB, ""); err != nil {
		return fmt.Errorf("DeclareContext(%q): %w", ctxB, err)
	}
	return nil
}

// initRecordsNBoundedContexts asserts the count after declaration.
func (s *ScenarioState) initRecordsNBoundedContexts(n int) error {
	if s.Profile == nil {
		return errors.New("no profile")
	}
	if len(s.Profile.BoundedContexts) != n {
		return fmt.Errorf("BoundedContexts = %d; want %d", len(s.Profile.BoundedContexts), n)
	}
	return nil
}

// initAutoProposesPerArrow verifies that auto-propose can run against
// every declared (role-pair, context) arrow — i.e., BuildProposal
// succeeds for the analyst role file against each declared context.
// This is the "per the auto-propose feature" cross-reference in
// scenario 19's Then step.
func (s *ScenarioState) initAutoProposesPerArrow() error {
	if s.Profile == nil || len(s.Profile.BoundedContexts) == 0 {
		return errors.New("profile has no contexts; auto-propose precondition unmet")
	}
	cat, err := loadSharedCatalogue()
	if err != nil {
		return fmt.Errorf("load catalogue: %w", err)
	}
	rf, err := bootstrap.ParseRoleFile("../../specs/direction/roles/analyst.md")
	if err != nil {
		return fmt.Errorf("ParseRoleFile(analyst): %w", err)
	}
	for _, c := range s.Profile.BoundedContexts {
		ap, err := bootstrap.BuildProposal(rf, cat, "analyst", "architect", c.ID)
		if err != nil {
			return fmt.Errorf("BuildProposal for %s: %w", c.ID, err)
		}
		if len(ap.Proposed) == 0 {
			return fmt.Errorf("context %s: 0 proposed clauses", c.ID)
		}
	}
	return nil
}

// ---- Scenario 33: brownfield directory discovery ----

// aProjectDirWithSourceInTwoSubdirs creates src/<ctxA>/ and
// src/<ctxB>/ with a placeholder Go file each.
func (s *ScenarioState) aProjectDirWithSourceInTwoSubdirs(ctxA, ctxB string) error {
	if err := s.ensureProjectTestDir(); err != nil {
		return err
	}
	for _, name := range []string{ctxA, ctxB} {
		dir := filepath.Join(s.ProjectTestDir, "src", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
		// A .go file so language scan registers brownfield mode.
		path := filepath.Join(dir, "main.go")
		if err := os.WriteFile(path, []byte("package "+name), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}

// noPriorGridCurrentExists is a precondition assertion: the project
// dir must have no .ghyll/grid.current. The tempdir setup guarantees
// this; here we just verify.
func (s *ScenarioState) noPriorGridCurrentExists() error {
	if s.ProjectTestDir == "" {
		return errors.New("no project dir established")
	}
	path := filepath.Join(s.ProjectTestDir, ".ghyll", "grid.current")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("precondition violated: %s exists", path)
	}
	return nil
}

// initDeterminesBrownfieldMode asserts ProfileRepo classified the dir
// as brownfield.
func (s *ScenarioState) initDeterminesBrownfieldMode() error {
	if s.Profile == nil {
		return errors.New("no profile; init step must run first")
	}
	if !s.Profile.IsBrownfield() {
		return fmt.Errorf("mode = %q; want brownfield", s.Profile.Mode)
	}
	return nil
}

// initProposesContextsFromDirStructure asserts the two named contexts
// were proposed (lexicographic order from ProfileRepo).
func (s *ScenarioState) initProposesContextsFromDirStructure(ctxA, ctxB string) error {
	if s.Profile == nil {
		return errors.New("no profile")
	}
	want := []string{ctxA, ctxB}
	if len(s.Profile.BoundedContexts) != 2 {
		return fmt.Errorf("BoundedContexts len = %d; want 2", len(s.Profile.BoundedContexts))
	}
	got := []string{s.Profile.BoundedContexts[0].ID, s.Profile.BoundedContexts[1].ID}
	for i := range want {
		if got[i] != want[i] {
			return fmt.Errorf("BoundedContexts[%d] = %q; want %q", i, got[i], want[i])
		}
	}
	return nil
}

// theOperatorConfirmsOrRefines is narrative — the operator's
// confirmation flow is operator-event-driven and outside the
// machine-decidable surface ProfileRepo provides. Verifying the
// profile is well-formed is sufficient signal for this step.
func (s *ScenarioState) theOperatorConfirmsOrRefines() error {
	if s.Profile == nil {
		return errors.New("no profile to confirm")
	}
	return nil
}
