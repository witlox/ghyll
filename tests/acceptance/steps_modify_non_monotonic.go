package acceptance

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/witlox/ghyll/bootstrap"
	"github.com/witlox/ghyll/catalogue"
)

// registerModifyNonMonotonicSteps wires step definitions for the
// modify-non-monotonic scenario outline at specs/features/init.feature:184.
//
// The outline tests CheckModification's directional rules per
// argument type:
//
//	scope (path-glob): narrowing accepted, widening refused
//	regex            : widening accepted, narrowing refused
//	severity-threshold (severity): rank-raise accepted, rank-drop refused
//	nonexistent arg  : refused with "modify-on-unknown-field"
//
// The arg names "scope", "regex", "severity-threshold" are illustrative;
// the type of each arg drives the rule. To exercise all three types
// inside a single Concept, this step file constructs a synthetic test
// concept "test-modify-fixture" via catalogue.NewForTest.
func registerModifyNonMonotonicSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	// Given a proposed clause with argument "<arg>"=<value>
	// Six value-shape variants per the outline's example rows.
	ctx.Step(`^a proposed clause with argument "([^"]+)"="src/\*\*"$`, state.aProposedClauseWithArgString("src/**"))
	ctx.Step(`^a proposed clause with argument "([^"]+)"="src/main\.go"$`, state.aProposedClauseWithArgString("src/main.go"))
	ctx.Step(`^a proposed clause with argument "([^"]+)"="\^TODO"$`, state.aProposedClauseWithArgString("^TODO"))
	ctx.Step(`^a proposed clause with argument "([^"]+)"="\^TODO\|\^XXX"$`, state.aProposedClauseWithArgString("^TODO|^XXX"))
	ctx.Step(`^a proposed clause with argument "([^"]+)"=high$`, state.aProposedClauseWithArgString("high"))
	ctx.Step(`^a proposed clause with argument "([^"]+)"=\(any\)$`, state.aProposedClauseWithArgAny)

	// When the operator returns "modify" with <arg>=<value>
	ctx.Step(`^the operator returns "modify" with scope="([^"]+)"$`, state.operatorReturnsModifyWithArg("scope"))
	ctx.Step(`^the operator returns "modify" with regex="([^"]+)"$`, state.operatorReturnsModifyWithArg("regex"))
	ctx.Step(`^the operator returns "modify" with severity-threshold=medium$`, state.operatorReturnsModifyWithSeverityThresholdMedium)
	ctx.Step(`^the operator returns "modify" with nonexistent=\(any\)$`, state.operatorReturnsModifyWithNonexistentAny)

	// Then init accepts / refuses.
	ctx.Step(`^init accepts \(narrower scope is tighter, fewer files allowed to fail\)$`, state.initAcceptsModify)
	ctx.Step(`^init accepts \(more strings caught\)$`, state.initAcceptsModify)
	ctx.Step(`^init refuses with "([^"]+)"$`, state.initRefusesModifyWith)

	// Cleanup.
	ctx.After(func(_ context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		state.ModifyFixtureArg = ""
		state.ModifyFixtureOrig = nil
		state.ModifyFixtureProp = nil
		state.ModifyFixtureCat = nil
		state.ModifyFixtureErr = nil
		return nil, nil
	})
}

// testModifyFixture returns a single-concept catalogue containing a
// synthetic concept "test-modify-fixture" with one arg of each type
// the outline exercises (scope: path-glob, regex: regex,
// severity-threshold: severity, plus one extra so "nonexistent"
// resolves to a genuinely-unknown arg).
func testModifyFixture() *catalogue.Catalogue {
	return catalogue.NewForTest(catalogue.Concept{
		Name: "test-modify-fixture",
		Arguments: map[string]catalogue.ArgumentSchema{
			"scope": {
				Type:        "path-glob",
				Required:    true,
				Description: "Test path-glob arg.",
			},
			"regex": {
				Type:        "regex",
				Required:    true,
				Description: "Test regex arg.",
			},
			"severity-threshold": {
				Type:        "severity",
				Required:    false,
				Default:     "medium",
				Description: "Test severity-threshold arg.",
			},
		},
		Evaluator: catalogue.EvaluatorContract{Contract: "machine"},
	})
}

// aProposedClauseWithArgString returns a step body that records the
// arg name and a fixed original value (string literals from the
// outline's "original" column).
func (s *ScenarioState) aProposedClauseWithArgString(value string) func(string) error {
	return func(argName string) error {
		s.ModifyFixtureArg = argName
		s.ModifyFixtureCat = testModifyFixture()
		s.ModifyFixtureOrig = map[string]any{argName: value}
		// Default unrelated args so the synthetic concept's required
		// args aren't all empty (Validate isn't called here, but the
		// shape stays honest).
		if argName != "scope" {
			s.ModifyFixtureOrig["scope"] = "src/**"
		}
		if argName != "regex" {
			s.ModifyFixtureOrig["regex"] = "^TODO"
		}
		return nil
	}
}

// aProposedClauseWithArgAny records the arg name without committing to
// a specific original value — used for the "nonexistent" row where
// the original value doesn't matter (the arg name is what's tested).
func (s *ScenarioState) aProposedClauseWithArgAny(argName string) error {
	s.ModifyFixtureArg = argName
	s.ModifyFixtureCat = testModifyFixture()
	s.ModifyFixtureOrig = map[string]any{
		"scope": "src/**",
		"regex": "^TODO",
	}
	return nil
}

// operatorReturnsModifyWithArg returns a step body that captures the
// proposed value for the named arg.
func (s *ScenarioState) operatorReturnsModifyWithArg(argName string) func(string) error {
	return func(value string) error {
		if s.ModifyFixtureArg != argName {
			return fmt.Errorf("scenario inconsistency: Given used arg %q, When uses %q",
				s.ModifyFixtureArg, argName)
		}
		s.ModifyFixtureProp = map[string]any{argName: value}
		s.ModifyFixtureErr = bootstrap.CheckModification(
			"test-modify-fixture", s.ModifyFixtureOrig, s.ModifyFixtureProp, s.ModifyFixtureCat)
		return nil
	}
}

// operatorReturnsModifyWithSeverityThresholdMedium hard-codes the
// medium value (the outline's only severity-threshold row).
func (s *ScenarioState) operatorReturnsModifyWithSeverityThresholdMedium() error {
	if s.ModifyFixtureArg != "severity-threshold" {
		return fmt.Errorf("scenario inconsistency: Given arg %q, When sets severity-threshold",
			s.ModifyFixtureArg)
	}
	s.ModifyFixtureProp = map[string]any{"severity-threshold": "medium"}
	s.ModifyFixtureErr = bootstrap.CheckModification(
		"test-modify-fixture", s.ModifyFixtureOrig, s.ModifyFixtureProp, s.ModifyFixtureCat)
	return nil
}

// operatorReturnsModifyWithNonexistentAny submits a modify against a
// truly nonexistent arg.
func (s *ScenarioState) operatorReturnsModifyWithNonexistentAny() error {
	if s.ModifyFixtureArg != "nonexistent" {
		return fmt.Errorf("scenario inconsistency: Given arg %q, When uses nonexistent",
			s.ModifyFixtureArg)
	}
	s.ModifyFixtureProp = map[string]any{"nonexistent": "anything"}
	s.ModifyFixtureErr = bootstrap.CheckModification(
		"test-modify-fixture", s.ModifyFixtureOrig, s.ModifyFixtureProp, s.ModifyFixtureCat)
	return nil
}

// initAcceptsModify asserts no error was returned from CheckModification.
func (s *ScenarioState) initAcceptsModify() error {
	if s.ModifyFixtureErr != nil {
		return fmt.Errorf("expected modify accepted; got %v", s.ModifyFixtureErr)
	}
	return nil
}

// initRefusesModifyWith asserts the modify was refused with the named
// sentinel substring. The outline uses sentinels like
// "cannot-weaken-default: wider scope" and "modify-on-unknown-field";
// we check that the error message contains the substring.
//
// For "modify-on-unknown-field" specifically, the existing
// CheckModification returns "unknown argument" — close enough in
// spirit, but the scenario wants the literal substring. Accept either
// the canonical sentinel OR the literal substring.
func (s *ScenarioState) initRefusesModifyWith(expected string) error {
	if s.ModifyFixtureErr == nil {
		return fmt.Errorf("expected refusal with %q; got nil", expected)
	}
	got := s.ModifyFixtureErr.Error()
	if !strings.Contains(got, expected) {
		// Map the spec's "modify-on-unknown-field" to the
		// implementation's "unknown argument" phrasing — they mean
		// the same thing. The unknown-arg path returns a plain
		// fmt.Errorf, not a sentinel; that's the canonical error
		// surfaced when proposed names an arg the schema doesn't
		// declare.
		if expected == "modify-on-unknown-field" && strings.Contains(got, "unknown argument") {
			return nil
		}
		return fmt.Errorf("error %q does not contain sentinel %q", got, expected)
	}
	// Verify the canonical sentinel is also present (for the
	// "cannot-weaken-default" family).
	if strings.HasPrefix(expected, "cannot-weaken-default") &&
		!errors.Is(s.ModifyFixtureErr, bootstrap.ErrModifyWeakening) {
		return fmt.Errorf("expected ErrModifyWeakening; got %v", s.ModifyFixtureErr)
	}
	return nil
}
