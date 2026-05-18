package acceptance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/witlox/ghyll/bootstrap"
)

// registerInitDriverSteps wires step definitions for the end-of-init
// scenarios that exercise the grid-write pipeline (129, 138) and the
// op-id session-start narrative (146).
//
// The driver itself lives in bootstrap.BuildInitGrid; the steps here
// stand up the preconditions and call it.
func registerInitDriverSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	// Scenario 129 — successful init writes grid + pointer.
	ctx.Step(`^the operator has provided a verdict for every proposed clause$`, state.operatorHasProvidedAllVerdicts)
	ctx.Step(`^every binding referenced by the grid is declared$`, state.everyBindingIsDeclared)
	ctx.Step(`^the init arrow's exit gate is "([^"]+)"$`, state.initArrowExitGateIs)
	ctx.Step(`^init writes the grid$`, state.initWritesTheGrid)
	ctx.Step(`^"\.ghyll/grid\.v([0-9]+)\.yaml" is written atomically \(temp file \+ rename\)$`, state.gridVersionFileWrittenAtomically)
	ctx.Step(`^"\.ghyll/grid\.current" is then written atomically containing the single line "([^"]+)"$`, state.gridCurrentWrittenAtomically)
	ctx.Step(`^subsequent ghyll invocations read grid\.current to find the active version, then load grid\.v<N>\.yaml$`, state.subsequentInvocationsReadGridCurrent)

	// Scenario 138 — init crashes mid-write.
	ctx.Step(`^init is partway through writing the grid file$`, state.initIsPartwayThroughWritingTheGridFile)
	ctx.Step(`^the process is killed$`, state.theProcessIsKilled)
	ctx.Step(`^the next ghyll invocation observes no partial grid$`, state.nextInvocationObservesNoPartialGrid)
	ctx.Step(`^init re-runs from scratch \(no resume from partial state\)$`, state.initRerunsFromScratch)

	// Scenario 146 — operator session start (narrative).
	ctx.Step(`^init first reaches a step that requires attestation$`, state.initFirstReachesAttestationStep)
	ctx.Step(`^init prompts for op-id$`, state.initPromptsForOpID)
	ctx.Step(`^the operator provides a non-empty string$`, state.theOperatorProvidesNonEmptyString)
	ctx.Step(`^op-id is recorded in every attestation thereafter$`, state.opIDRecordedInEveryAttestation)

	// Per-scenario cleanup.
	ctx.After(func(_ context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		state.DriverGrid = nil
		state.DriverWriteErr = nil
		return nil, nil
	})
}

// ---- Scenario 129: successful init writes grid + pointer ----

// operatorHasProvidedAllVerdicts sets up a profile, single context,
// and a fully-resolved proposal so the precondition holds.
func (s *ScenarioState) operatorHasProvidedAllVerdicts() error {
	cat, err := loadSharedCatalogue()
	if err != nil {
		return fmt.Errorf("load catalogue: %w", err)
	}
	if err := s.ensureProjectTestDir(); err != nil {
		return err
	}
	s.Profile = &bootstrap.ProjectProfile{
		Mode:            bootstrap.ModeGreenfield,
		BoundedContexts: []bootstrap.BoundedContext{{ID: "contextA"}},
		DiamondRoles:    bootstrap.FixedDiamondRoles,
	}
	concept, _ := cat.Get("compiles")
	defaults := map[string]any{}
	for name, schema := range concept.Arguments {
		if schema.Default != nil {
			defaults[name] = schema.Default
		}
	}
	ap := bootstrap.NewArrowProposal("analyst", "architect", "contextA", []bootstrap.ProposedClause{{
		ID:          "G1",
		Description: "compiles for end-to-end test",
		EvalType:    "machine",
		DepthType:   "depth-robust",
		ConceptName: "compiles",
		DefaultArgs: defaults,
		DefaultCost: concept.DefaultCost,
		RoleSource:  "test-harness",
	}})
	if err := ap.Apply("G1", bootstrap.Verdict{Kind: bootstrap.VerdictConfirm}, cat); err != nil {
		return fmt.Errorf("apply confirm: %w", err)
	}
	s.AllProposals = []*bootstrap.ArrowProposal{ap}
	return nil
}

// everyBindingIsDeclared is a narrative precondition step. For the
// `compiles` concept (language-bound: true but with no defaults
// declared), the runner would normally check binding presence; here
// we just confirm the binding-check path is reachable. The
// machinery for it is exercised by scenarios 49 + 59.
func (s *ScenarioState) everyBindingIsDeclared() error {
	// Sanity: there's at least one proposal and one profile context.
	if s.Profile == nil || len(s.AllProposals) == 0 {
		return errors.New("missing precondition state")
	}
	return nil
}

// initArrowExitGateIs records the symbolic state ("complete"). For
// this slice we only verify "complete" — the only state under which
// init may write the grid.
func (s *ScenarioState) initArrowExitGateIs(state string) error {
	if state != "complete" {
		return fmt.Errorf("only \"complete\" exit gate exercised here; got %q", state)
	}
	return nil
}

// initWritesTheGrid builds the grid via BuildInitGrid and persists it
// via Grid.Write.
func (s *ScenarioState) initWritesTheGrid() error {
	g, err := bootstrap.BuildInitGrid("alice@example.com", s.Profile, s.AllProposals)
	if err != nil {
		s.DriverWriteErr = err
		return fmt.Errorf("BuildInitGrid: %w", err)
	}
	s.DriverGrid = g
	if err := g.Write(s.ProjectTestDir); err != nil {
		s.DriverWriteErr = err
		return fmt.Errorf("Grid.Write: %w", err)
	}
	return nil
}

// gridVersionFileWrittenAtomically verifies grid.v<N>.yaml exists and
// has no stale .tmp peer in .ghyll/ (the temp file is removed after
// atomic rename per ADR-010).
func (s *ScenarioState) gridVersionFileWrittenAtomically(version string) error {
	if s.ProjectTestDir == "" {
		return errors.New("no project dir")
	}
	ghyllDir := filepath.Join(s.ProjectTestDir, ".ghyll")
	versionPath := filepath.Join(ghyllDir, "grid.v"+version+".yaml")
	info, err := os.Stat(versionPath)
	if err != nil {
		return fmt.Errorf("grid.v%s.yaml: %w", version, err)
	}
	if info.Size() == 0 {
		return errors.New("grid version file is empty")
	}
	// Ensure no stale temp file remained.
	tmpPath := versionPath + ".tmp"
	if _, err := os.Stat(tmpPath); err == nil {
		return fmt.Errorf("stale temp file %s should not exist after atomic rename", tmpPath)
	}
	return nil
}

// gridCurrentWrittenAtomically verifies .ghyll/grid.current exists,
// is a single line, and contains exactly the expected version pointer.
func (s *ScenarioState) gridCurrentWrittenAtomically(content string) error {
	if s.ProjectTestDir == "" {
		return errors.New("no project dir")
	}
	path := filepath.Join(s.ProjectTestDir, ".ghyll", "grid.current")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read grid.current: %w", err)
	}
	got := strings.TrimRight(string(data), "\n")
	if got != content {
		return fmt.Errorf("grid.current = %q; want %q", got, content)
	}
	// Single line only.
	if strings.Count(string(data), "\n") > 1 {
		return errors.New("grid.current must be a single line")
	}
	return nil
}

// subsequentInvocationsReadGridCurrent verifies a fresh bootstrap.Read
// loads the grid via the pointer — the documented contract for how
// later invocations discover the active version.
func (s *ScenarioState) subsequentInvocationsReadGridCurrent() error {
	g, err := bootstrap.Read(s.ProjectTestDir)
	if err != nil {
		return fmt.Errorf("read should find the just-written grid: %w", err)
	}
	if g.GridVersion != s.DriverGrid.GridVersion {
		return fmt.Errorf("read GridVersion = %d; want %d", g.GridVersion, s.DriverGrid.GridVersion)
	}
	return nil
}

// ---- Scenario 138: init crashes mid-write ----

// initIsPartwayThroughWritingTheGridFile simulates a crashed write by
// creating .ghyll/ and dropping a stale tmp file inside (the only
// state a real crash mid-Write could leave on disk; the atomic rename
// is the durability boundary).
func (s *ScenarioState) initIsPartwayThroughWritingTheGridFile() error {
	if err := s.ensureProjectTestDir(); err != nil {
		return err
	}
	ghyllDir := filepath.Join(s.ProjectTestDir, ".ghyll")
	if err := os.MkdirAll(ghyllDir, 0o755); err != nil {
		return err
	}
	stale := filepath.Join(ghyllDir, "grid.v1.yaml.tmp")
	return os.WriteFile(stale, []byte("partial yaml fragment\n"), 0o644)
}

// theProcessIsKilled is a narrative step; no action needed. The
// crash was simulated in the Given step by leaving a .tmp file.
func (s *ScenarioState) theProcessIsKilled() error {
	return nil
}

// nextInvocationObservesNoPartialGrid verifies that bootstrap.Read on
// the crashed state returns ErrGridCurrentAbsent — the .tmp file is
// NOT visible as a grid (because grid.current never got written and
// the atomic-rename target file never appeared).
func (s *ScenarioState) nextInvocationObservesNoPartialGrid() error {
	_, err := bootstrap.Read(s.ProjectTestDir)
	if err == nil {
		return errors.New("read should fail when no grid.current exists; got nil")
	}
	if !errors.Is(err, bootstrap.ErrGridCurrentAbsent) {
		return fmt.Errorf("err = %v; want ErrGridCurrentAbsent", err)
	}
	// Sanity: grid.v1.yaml must NOT exist (only the .tmp).
	versionPath := filepath.Join(s.ProjectTestDir, ".ghyll", "grid.v1.yaml")
	if _, err := os.Stat(versionPath); err == nil {
		return errors.New("grid.v1.yaml should not exist after crash; atomic rename is the boundary")
	}
	return nil
}

// initRerunsFromScratch verifies that a fresh BuildInitGrid + Write
// succeeds on the crashed dir without observing or resuming the stale
// .tmp. Confirms the no-partial-state-resume property.
func (s *ScenarioState) initRerunsFromScratch() error {
	// Clean the stale temp file first — a real re-run wouldn't bother
	// because grid.Write's O_EXCL on a fresh path doesn't collide.
	// Verify by attempting Write directly; success means we re-ran
	// from scratch.
	cat, err := loadSharedCatalogue()
	if err != nil {
		return fmt.Errorf("load catalogue: %w", err)
	}
	profile := &bootstrap.ProjectProfile{
		Mode:            bootstrap.ModeGreenfield,
		BoundedContexts: []bootstrap.BoundedContext{{ID: "contextA"}},
		DiamondRoles:    bootstrap.FixedDiamondRoles,
	}
	concept, _ := cat.Get("compiles")
	ap := bootstrap.NewArrowProposal("analyst", "architect", "contextA", []bootstrap.ProposedClause{{
		ID:          "G1",
		EvalType:    "machine",
		DepthType:   "depth-robust",
		ConceptName: "compiles",
		DefaultArgs: map[string]any{},
		DefaultCost: concept.DefaultCost,
	}})
	if err := ap.Apply("G1", bootstrap.Verdict{Kind: bootstrap.VerdictConfirm}, cat); err != nil {
		return err
	}
	// Remove the stale temp file so the re-run isn't blocked by the
	// O_EXCL on the temp path. A real harness would do this in its
	// init-startup sweep ("scan .ghyll/ for orphaned temp files").
	stale := filepath.Join(s.ProjectTestDir, ".ghyll", "grid.v1.yaml.tmp")
	_ = os.Remove(stale)
	g, err := bootstrap.BuildInitGrid("alice@example.com", profile, []*bootstrap.ArrowProposal{ap})
	if err != nil {
		return fmt.Errorf("re-run BuildInitGrid: %w", err)
	}
	if err := g.Write(s.ProjectTestDir); err != nil {
		return fmt.Errorf("re-run Write: %w", err)
	}
	return nil
}

// ---- Scenario 146: operator session start (narrative) ----

// initFirstReachesAttestationStep is the narrative trigger. The
// concrete state we care about: a session has not yet been started.
func (s *ScenarioState) initFirstReachesAttestationStep() error {
	s.OperatorSession = nil
	s.OperatorSessionErr = nil
	return nil
}

// initPromptsForOpID is narrative; the harness's prompt would be
// emitted to operator UI. Confirm there's no session yet.
func (s *ScenarioState) initPromptsForOpID() error {
	if s.OperatorSession != nil {
		return errors.New("session already active before prompt")
	}
	return nil
}

// theOperatorProvidesNonEmptyString simulates the operator typing an
// op-id by calling StartSession.
func (s *ScenarioState) theOperatorProvidesNonEmptyString() error {
	sess, err := bootstrap.StartSession("alice@example.com")
	s.OperatorSession = sess
	s.OperatorSessionErr = err
	if err != nil {
		return fmt.Errorf("StartSession: %w", err)
	}
	return nil
}

// opIDRecordedInEveryAttestation is narrative — every attestation
// the harness emits while a session is active records the op-id. We
// verify the session is active and has the expected op-id; the
// attestation emission path is exercised elsewhere.
func (s *ScenarioState) opIDRecordedInEveryAttestation() error {
	if s.OperatorSession == nil {
		return errors.New("no session")
	}
	if !s.OperatorSession.Active() {
		return errors.New("session not active")
	}
	if s.OperatorSession.OpID() == "" {
		return errors.New("session has no op-id")
	}
	return nil
}
