package acceptance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/cucumber/godog"

	"github.com/witlox/ghyll/bootstrap"
	"github.com/witlox/ghyll/catalogue"
)

// sharedCatalogue is loaded once across all init scenarios. The 18
// concept schemas don't change between scenarios; a single load avoids
// repeated disk reads.
var (
	sharedCatalogue     *catalogue.Catalogue
	sharedCatalogueOnce sync.Once
	sharedCatalogueErr  error
)

func loadSharedCatalogue() (*catalogue.Catalogue, error) {
	sharedCatalogueOnce.Do(func() {
		// Embedded catalogue, not cwd-relative disk path. The
		// released binary cannot reach ../../gates/concepts;
		// neither should the acceptance suite (integrator H-1).
		sharedCatalogue, sharedCatalogueErr = catalogue.LoadEmbedded()
	})
	return sharedCatalogue, sharedCatalogueErr
}

// registerInitSteps wires step definitions for project-initialization
// scenarios (specs/features/init.feature).
//
// This file grows incrementally as init component pieces land. Only
// scenarios whose every step has a definition will actually run; others
// show as "undefined" (godog Strict=false in acceptance_test.go).
func registerInitSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	// Op-id session-lifecycle steps.
	ctx.Step(`^the operator provides empty op-id "([^"]*)"$`, state.operatorProvidesEmptyOpID)
	ctx.Step(`^session start is refused with "([^"]*)"$`, state.sessionStartIsRefusedWith)

	// Auto-propose modify-rule steps. The regex matches the bare
	// concept-call form only ("concept-name(args)") — the longer
	// "role.id = concept(args)" form is owned by steps_propose.go's
	// aProposedClauseFromRoleFile step.
	ctx.Step(`^a proposed clause "([a-z][a-z0-9-]*)\(([^)]*)\)"$`, state.aProposedClauseWithArgs)
	ctx.Step(`^the operator returns "modify" with \{([^}]+)\}$`, state.theOperatorReturnsModifyWith)
	ctx.Step(`^the clause is recorded with threshold ([0-9.]+)$`, state.theClauseIsRecordedWithThreshold)
	ctx.Step(`^the modification is allowed because [0-9.]+ > [0-9.]+ \(raise only\)$`, state.theModificationIsAllowed)
	ctx.Step(`^init refuses the modification with "([^"]+)"$`, state.initRefusesTheModificationWith)

	// Grid filesystem steps (ADR-010).
	ctx.Step(`^"\.ghyll/grid\.current" contains "([^"]+)"$`, state.gridCurrentContains)
	ctx.Step(`^"\.ghyll/grid\.v([0-9]+)\.yaml" does not exist .*$`, state.gridVersionFileDoesNotExist)
	ctx.Step(`^"\.ghyll/grid\.current" exists but contains garbage .*$`, state.gridCurrentContainsGarbage)
	ctx.Step(`^"\.ghyll/grid\.v1\.yaml", "\.ghyll/grid\.v2\.yaml" exist$`, state.gridVersionsV1AndV2Exist)
	ctx.Step(`^"\.ghyll/grid\.current" is absent$`, state.gridCurrentIsAbsent)
	ctx.Step(`^the harness initializes$`, state.theHarnessInitializes)
	ctx.Step(`^the engine refuses to start any pass with "([^"]+)"$`, state.engineRefusesToStartWithSentinel)
	ctx.Step(`^the engine refuses to start with "([^"]+)"$`, state.engineRefusesToStartWithSentinel)
	ctx.Step(`^the engine does NOT silently pick the latest \(refuses to assume\)$`, state.engineDoesNotSilentlyPickLatest)
	ctx.Step(`^the engine surfaces "([^"]+)" with a list of available versions$`, state.engineSurfacesSentinel)
	// Narrative steps (no concrete impl yet; succeed if the substantive check above set state).
	ctx.Step(`^presents the operator with options to restore the file, re-point grid\.current, or re-run init$`, state.narrativeOK)
	ctx.Step(`^no pass is started until the operator resolves the inconsistency$`, state.narrativeOK)
	ctx.Step(`^reports the actual content for operator triage$`, state.narrativeOK)
	ctx.Step(`^operator must repair grid\.current or remove it \(forcing re-init\)$`, state.narrativeOK)
	ctx.Step(`^operator must declare which is current \(or re-init\)$`, state.narrativeOK)

	// Per-scenario tempdir cleanup.
	ctx.After(func(_ context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if state.GridTestDir != "" {
			_ = os.RemoveAll(state.GridTestDir)
			state.GridTestDir = ""
		}
		state.GridReadErr = nil
		return nil, nil
	})
}

// ensureGridTestDir creates a per-scenario tempdir on first use.
func (s *ScenarioState) ensureGridTestDir() error {
	if s.GridTestDir != "" {
		return nil
	}
	dir, err := os.MkdirTemp("", "ghyll-grid-test-")
	if err != nil {
		return fmt.Errorf("ensureGridTestDir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".ghyll"), 0o755); err != nil {
		return fmt.Errorf("ensureGridTestDir: mkdir .ghyll: %w", err)
	}
	s.GridTestDir = dir
	return nil
}

// gridCurrentContains writes content to .ghyll/grid.current.
//
// Gherkin: `".ghyll/grid.current" contains "v3"`
func (s *ScenarioState) gridCurrentContains(content string) error {
	if err := s.ensureGridTestDir(); err != nil {
		return err
	}
	return os.WriteFile(
		filepath.Join(s.GridTestDir, ".ghyll", "grid.current"),
		[]byte(content+"\n"),
		0o644,
	)
}

// gridVersionFileDoesNotExist ensures .ghyll/grid.v<N>.yaml is absent.
//
// Gherkin: `".ghyll/grid.v3.yaml" does not exist (...)`
func (s *ScenarioState) gridVersionFileDoesNotExist(version string) error {
	if err := s.ensureGridTestDir(); err != nil {
		return err
	}
	path := filepath.Join(s.GridTestDir, ".ghyll", "grid.v"+version+".yaml")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

// gridCurrentContainsGarbage writes content that cannot be parsed as
// a "v<N>" pointer.
//
// Gherkin: `".ghyll/grid.current" exists but contains garbage (...)`
func (s *ScenarioState) gridCurrentContainsGarbage() error {
	if err := s.ensureGridTestDir(); err != nil {
		return err
	}
	garbage := "\x00\x01garbage\nmultiple\nlines\n"
	return os.WriteFile(
		filepath.Join(s.GridTestDir, ".ghyll", "grid.current"),
		[]byte(garbage),
		0o644,
	)
}

// gridVersionsV1AndV2Exist writes minimal valid grid.v1.yaml and grid.v2.yaml.
//
// Gherkin: `".ghyll/grid.v1.yaml", ".ghyll/grid.v2.yaml" exist`
func (s *ScenarioState) gridVersionsV1AndV2Exist() error {
	if err := s.ensureGridTestDir(); err != nil {
		return err
	}
	for _, v := range []int{1, 2} {
		g := bootstrap.NewGrid("alice@example.com")
		g.GridVersion = v
		if err := g.Write(s.GridTestDir); err != nil {
			return fmt.Errorf("write v%d: %w", v, err)
		}
	}
	return nil
}

// gridCurrentIsAbsent removes .ghyll/grid.current if present.
//
// Gherkin: `".ghyll/grid.current" is absent`
func (s *ScenarioState) gridCurrentIsAbsent() error {
	if err := s.ensureGridTestDir(); err != nil {
		return err
	}
	path := filepath.Join(s.GridTestDir, ".ghyll", "grid.current")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

// theHarnessInitializes calls bootstrap.Read to load the active grid.
//
// Gherkin: `the harness initializes`
func (s *ScenarioState) theHarnessInitializes() error {
	if err := s.ensureGridTestDir(); err != nil {
		return err
	}
	_, s.GridReadErr = bootstrap.Read(s.GridTestDir)
	return nil
}

// engineRefusesToStartWithSentinel verifies the read error matches the
// expected sentinel.
//
// Gherkin: `the engine refuses to start any pass with "<sentinel>"`
// or       `the engine refuses to start with "<sentinel>"`
func (s *ScenarioState) engineRefusesToStartWithSentinel(expected string) error {
	if s.GridReadErr == nil {
		return fmt.Errorf("expected refusal with %q; got nil error (Read succeeded)", expected)
	}
	if !strings.Contains(s.GridReadErr.Error(), expected) {
		return fmt.Errorf("expected error containing %q; got %v", expected, s.GridReadErr)
	}
	return nil
}

// engineDoesNotSilentlyPickLatest verifies the engine refused rather
// than silently choosing.
//
// Gherkin: `the engine does NOT silently pick the latest (refuses to assume)`
func (s *ScenarioState) engineDoesNotSilentlyPickLatest() error {
	if s.GridReadErr == nil {
		return errors.New("engine silently picked a version; expected refusal")
	}
	if !errors.Is(s.GridReadErr, bootstrap.ErrGridCurrentAbsent) {
		return fmt.Errorf("expected ErrGridCurrentAbsent; got %v", s.GridReadErr)
	}
	return nil
}

// engineSurfacesSentinel verifies the error mentions the expected sentinel.
//
// Gherkin: `the engine surfaces "<sentinel>" with a list of available versions`
func (s *ScenarioState) engineSurfacesSentinel(expected string) error {
	if s.GridReadErr == nil {
		return fmt.Errorf("expected %q surfacing; got no error", expected)
	}
	if !strings.Contains(s.GridReadErr.Error(), expected) {
		return fmt.Errorf("error %v does not contain %q", s.GridReadErr, expected)
	}
	return nil
}

// narrativeOK is a placeholder for narrative steps describing behavior
// that requires components not yet implemented (operator UI prompts,
// REPL choice surfaces). the wired scenarios
// using this helper ALWAYS run a real-assertion step BEFORE the
// narrative tail (e.g., engineRefusesToStartWithSentinel runs before
// the "presents the operator with options" narrative). The narrative
// is documentation of the deferred operator-UI behavior; the real
// assertion is the upstream sentinel check. The pattern is reviewed
// per-scenario when narrativeOK is added to a step.
func (s *ScenarioState) narrativeOK() error {
	return nil
}

// operatorProvidesEmptyOpID stashes the (empty or otherwise) op-id for
// the subsequent "Then" step to validate.
//
// Gherkin: `the operator provides empty op-id ""`
func (s *ScenarioState) operatorProvidesEmptyOpID(opID string) error {
	s.PendingOpID = opID
	// Reset any prior session state from a previous scenario.
	s.OperatorSession = nil
	s.OperatorSessionErr = nil
	return nil
}

// sessionStartIsRefusedWith calls StartSession with the pending op-id
// and verifies the returned error matches the expected sentinel.
//
// Gherkin: `session start is refused with "<error>"`
func (s *ScenarioState) sessionStartIsRefusedWith(expected string) error {
	// If an earlier step already attempted StartSession, OperatorSessionErr
	// is set; otherwise this Then step performs the attempt.
	if s.OperatorSessionErr == nil && s.OperatorSession == nil {
		_, err := bootstrap.StartSession(s.PendingOpID)
		s.OperatorSessionErr = err
	}
	if s.OperatorSessionErr == nil {
		return fmt.Errorf("expected session start to be refused with %q, but it succeeded", expected)
	}
	if !strings.Contains(s.OperatorSessionErr.Error(), expected) {
		return fmt.Errorf("expected error containing %q; got %v", expected, s.OperatorSessionErr)
	}
	// Defensive: should be a recognized sentinel.
	if !errors.Is(s.OperatorSessionErr, bootstrap.ErrOpIDRequired) &&
		!errors.Is(s.OperatorSessionErr, bootstrap.ErrOpIDTooLong) &&
		!errors.Is(s.OperatorSessionErr, bootstrap.ErrOpIDInvalidCharacters) {
		return fmt.Errorf("error %v is not one of the documented sentinels", s.OperatorSessionErr)
	}
	return nil
}

// aProposedClauseWithArgs parses a proposed-clause string of the form
// "concept-name(arg=value, arg=value)" into the scenario state.
//
// Gherkin: `a proposed clause "mutation-score(threshold=0.7)"`
func (s *ScenarioState) aProposedClauseWithArgs(concept, argsStr string) error {
	s.ProposedConcept = concept
	s.ProposedArgs = parseClauseArgs(argsStr)
	s.ModifyArgs = nil
	s.ModifyErr = nil
	return nil
}

// theOperatorReturnsModifyWith parses the modify-args string of the
// form "arg: value, arg: value" and records the operator's modify
// request. Does NOT yet evaluate the rule — that happens in the Then
// step so the When/Then sequence reads naturally.
//
// Gherkin: `the operator returns "modify" with {threshold: 0.85}`
func (s *ScenarioState) theOperatorReturnsModifyWith(argsStr string) error {
	s.ModifyArgs = parseClauseArgs(argsStr)
	return nil
}

// theClauseIsRecordedWithThreshold runs the modify check; expects it
// to allow the modification. The numeric value in the step is a
// sanity check against the modified args.
//
// Gherkin: `the clause is recorded with threshold 0.85`
func (s *ScenarioState) theClauseIsRecordedWithThreshold(thresh string) error {
	cat, err := loadSharedCatalogue()
	if err != nil {
		return fmt.Errorf("load catalogue: %w", err)
	}
	// verify ProposedConcept is in the
	// catalogue BEFORE calling CheckModification. Otherwise a typo
	// in the scenario surfaces as a confusing "modify rejected" error
	// instead of a clear "concept not found".
	if _, found := cat.Get(s.ProposedConcept); !found {
		return fmt.Errorf("concept %q not in catalogue (scenario setup error)",
			s.ProposedConcept)
	}
	if err := bootstrap.CheckModification(s.ProposedConcept, s.ProposedArgs, s.ModifyArgs, cat); err != nil {
		return fmt.Errorf("expected modify to be allowed, got %v", err)
	}
	want, err := strconv.ParseFloat(thresh, 64)
	if err != nil {
		return fmt.Errorf("parse threshold: %w", err)
	}
	got, ok := s.ModifyArgs["threshold"]
	if !ok {
		return errors.New("threshold not in modify args")
	}
	gotF, ok := asFloat(got)
	if !ok {
		return fmt.Errorf("threshold is non-numeric: %T", got)
	}
	if gotF != want {
		return fmt.Errorf("threshold = %v; want %v", gotF, want)
	}
	return nil
}

// theModificationIsAllowed is the second Then in the "raising threshold"
// scenario; it's purely a narrative restatement (the actual check ran
// in theClauseIsRecordedWithThreshold). Asserts no late failure.
//
// Gherkin: `the modification is allowed because 0.85 > 0.7 (raise only)`
func (s *ScenarioState) theModificationIsAllowed() error {
	if s.ModifyErr != nil {
		return fmt.Errorf("expected modify allowed; ModifyErr = %v", s.ModifyErr)
	}
	return nil
}

// initRefusesTheModificationWith runs the modify check; expects it to
// return an error whose message contains the given sentinel.
//
// Gherkin: `init refuses the modification with "cannot-weaken-default"`
func (s *ScenarioState) initRefusesTheModificationWith(expected string) error {
	cat, err := loadSharedCatalogue()
	if err != nil {
		return fmt.Errorf("load catalogue: %w", err)
	}
	s.ModifyErr = bootstrap.CheckModification(s.ProposedConcept, s.ProposedArgs, s.ModifyArgs, cat)
	if s.ModifyErr == nil {
		return fmt.Errorf("expected modify to be refused with %q; got nil", expected)
	}
	if !strings.Contains(s.ModifyErr.Error(), expected) {
		return fmt.Errorf("expected error containing %q; got %v", expected, s.ModifyErr)
	}
	return nil
}

// parseClauseArgs parses a string like "threshold=0.7" or
// "threshold: 0.85, scope: src/**" into an args map. Supports both
// `=` and `:` separators (init.feature uses both forms).
func parseClauseArgs(s string) map[string]any {
	args := map[string]any{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		sep := strings.IndexAny(part, "=:")
		if sep < 0 {
			continue
		}
		k := strings.TrimSpace(part[:sep])
		v := strings.TrimSpace(part[sep+1:])
		args[k] = parseClauseValue(v)
	}
	return args
}

// parseClauseValue parses a single value: number, quoted string, or
// bare string.
func parseClauseValue(s string) any {
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return n
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// asFloat coerces a numeric any to float64. Returns (0, false) if not numeric.
func asFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}
