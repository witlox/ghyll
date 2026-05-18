package acceptance

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/witlox/ghyll/bootstrap"
)

// registerInitSteps wires step definitions for project-initialization
// scenarios (specs/v2/features/init.feature).
//
// This file grows incrementally as init component pieces land. Only
// scenarios whose every step has a definition will actually run; others
// show as "undefined" (godog Strict=false in acceptance_test.go).
func registerInitSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	// Op-id session-lifecycle steps (currently the only init pieces shipped).
	ctx.Step(`^the operator provides empty op-id "([^"]*)"$`, state.operatorProvidesEmptyOpID)
	ctx.Step(`^session start is refused with "([^"]*)"$`, state.sessionStartIsRefusedWith)
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
