package acceptance

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/cucumber/godog"

	"github.com/witlox/ghyll/bootstrap"
	"github.com/witlox/ghyll/catalogue"
)

// sharedCatalogue is loaded once across all init scenarios. The 17
// concept schemas don't change between scenarios; a single load avoids
// repeated disk reads.
var (
	sharedCatalogue     *catalogue.Catalogue
	sharedCatalogueOnce sync.Once
	sharedCatalogueErr  error
)

func loadSharedCatalogue() (*catalogue.Catalogue, error) {
	sharedCatalogueOnce.Do(func() {
		sharedCatalogue, sharedCatalogueErr = catalogue.Load("../../gates/concepts")
	})
	return sharedCatalogue, sharedCatalogueErr
}

// registerInitSteps wires step definitions for project-initialization
// scenarios (specs/v2/features/init.feature).
//
// This file grows incrementally as init component pieces land. Only
// scenarios whose every step has a definition will actually run; others
// show as "undefined" (godog Strict=false in acceptance_test.go).
func registerInitSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	// Op-id session-lifecycle steps.
	ctx.Step(`^the operator provides empty op-id "([^"]*)"$`, state.operatorProvidesEmptyOpID)
	ctx.Step(`^session start is refused with "([^"]*)"$`, state.sessionStartIsRefusedWith)

	// Auto-propose modify-rule steps.
	ctx.Step(`^a proposed clause "([^"]+)\(([^)]*)\)"$`, state.aProposedClauseWithArgs)
	ctx.Step(`^the operator returns "modify" with \{([^}]+)\}$`, state.theOperatorReturnsModifyWith)
	ctx.Step(`^the clause is recorded with threshold ([0-9.]+)$`, state.theClauseIsRecordedWithThreshold)
	ctx.Step(`^the modification is allowed because [0-9.]+ > [0-9.]+ \(raise only\)$`, state.theModificationIsAllowed)
	ctx.Step(`^init refuses the modification with "([^"]+)"$`, state.initRefusesTheModificationWith)
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
