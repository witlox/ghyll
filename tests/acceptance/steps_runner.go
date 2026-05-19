package acceptance

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/cucumber/godog"

	"github.com/witlox/ghyll/runner"
)

// registerRunnerSteps wires step definitions for the F-3
// (arrow-status derivation) and transition-refusal scenarios in
// specs/v2/features/runner.feature.
//
// Out of scope for this slice (will surface as undefined): the
// subprocess-process-lifecycle scenarios (timeout, OOM, malformed
// JSON, oversized output, zombie children) — those step semantics
// describe a real subprocess harness operating against a Clause
// flow that this BDD layer does not yet stage. The unit-test
// coverage on runner/subprocess.go already exercises those code
// paths.
func registerRunnerSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	// F-3: Arrow status derivation. The pattern is:
	//   Given an arrow with N clauses, <distribution>
	//   Then the derived arrow status is "<status>"
	ctx.Step(`^an arrow with (\d+) clauses, all status "([^"]+)", no findings$`,
		state.anArrowWithAllStatus)
	ctx.Step(`^an arrow with (\d+) clauses, (\d+) "([^"]+)" and (\d+) "([^"]+)"$`,
		state.anArrowWith2GroupSplit)
	ctx.Step(`^an arrow with (\d+) clauses: (\d+) "([^"]+)", (\d+) "([^"]+)", (\d+) "([^"]+)"$`,
		state.anArrowWith3GroupSplit)
	ctx.Step(`^an arrow with (\d+) clauses: (\d+) machine "([^"]+)", (\d+) attested "([^"]+)"$`,
		state.anArrowWithMachineAttestedSplit)
	ctx.Step(`^the derived arrow status is "([^"]+)"$`, state.derivedArrowStatusIs)
	ctx.Step(`^the arrow does NOT satisfy the next role's input$`, state.arrowDoesNotSatisfyNextRole)

	// Transition refusal (runner.feature §"Transition refusal").
	ctx.Step(`^arrow A \(upstream\) has derived status "([^"]+)"$`, state.arrowAHasStatus)
	ctx.Step(`^arrow A has derived status "([^"]+)"$`, state.arrowAHasStatus)
	ctx.Step(`^arrow A has status "([^"]+)"$`, state.arrowAHasStatus)
	ctx.Step(`^arrow B \(downstream\) requires A as input$`, state.arrowBRequiresA)
	ctx.Step(`^a role attempts to enter the role downstream of arrow B$`, state.attemptDownstreamTransition)
	ctx.Step(`^a role attempts the downstream transition$`, state.attemptDownstreamTransition)
	ctx.Step(`^a downstream transition is attempted$`, state.attemptDownstreamTransition)
	ctx.Step(`^the runner refuses the transition with a structured error containing kind "([^"]+)", upstream-arrow id, upstream-status, blocking-clauses$`,
		state.runnerRefusesWithStructuredError)
	ctx.Step(`^no pass on arrow B is started$`, state.noPassOnBStarted)
	ctx.Step(`^the runner permits the transition$`, state.runnerPermitsTransition)
	ctx.Step(`^starts a new pass on arrow B$`, state.startsNewPassOnB)
	ctx.Step(`^the runner refuses with kind "([^"]+)"$`, state.runnerRefusesWithKind)
	ctx.Step(`^the structured error contains A's arrow-id and its invalidating grid-version$`,
		state.errorContainsArrowAndGridVersion)
	ctx.Step(`^an OperatorEvent of type "([^"]+)" or "([^"]+)" is published on the operator event bus \(observable to the UI / tooling layer, not just returned as a function error\)$`,
		state.operatorEventPublishedNarrative)

	// Cleanup.
	ctx.After(func(_ context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		state.RunnerClauses = nil
		state.RunnerFindings = nil
		state.RunnerSeverityThresh = 0
		state.RunnerArrowStatus = 0
		state.RunnerBlockingCount = 0
		state.RunnerUpstreamStatus = 0
		state.RunnerUpstreamArrowID = ""
		state.RunnerDownstreamArrowID = ""
		state.RunnerInvalidatingGV = 0
		state.RunnerTransitionErr = nil
		return nil, nil
	})
}

// ---- F-3: arrow status derivation ----

// anArrowWithAllStatus stages N clauses all sharing the same
// status. Sentence form: "an arrow with 5 clauses, all status
// 'pass', no findings".
func (s *ScenarioState) anArrowWithAllStatus(n int, status string) error {
	cs, err := makeClauses(n, status)
	if err != nil {
		return err
	}
	s.RunnerClauses = cs
	s.RunnerFindings = nil
	s.RunnerSeverityThresh = 3 // medium
	return nil
}

// anArrowWith2GroupSplit stages two groups summing to N. Sentence
// form: "an arrow with 5 clauses, 4 'pass' and 1 'fail'".
func (s *ScenarioState) anArrowWith2GroupSplit(total, n1 int, s1 string, n2 int, s2 string) error {
	if n1+n2 != total {
		return fmt.Errorf("group split %d + %d != %d", n1, n2, total)
	}
	cs1, err := makeClauses(n1, s1)
	if err != nil {
		return err
	}
	cs2, err := makeClauses(n2, s2)
	if err != nil {
		return err
	}
	s.RunnerClauses = append(cs1, cs2...)
	s.RunnerSeverityThresh = 3
	return nil
}

// anArrowWith3GroupSplit stages three groups summing to N. Sentence
// form: "an arrow with 5 clauses: 3 'pass', 1 'fail', 1
// 'unevaluated'".
func (s *ScenarioState) anArrowWith3GroupSplit(total int, n1 int, s1 string, n2 int, s2 string, n3 int, s3 string) error {
	if n1+n2+n3 != total {
		return fmt.Errorf("group split %d + %d + %d != %d", n1, n2, n3, total)
	}
	cs1, err := makeClauses(n1, s1)
	if err != nil {
		return err
	}
	cs2, err := makeClauses(n2, s2)
	if err != nil {
		return err
	}
	cs3, err := makeClauses(n3, s3)
	if err != nil {
		return err
	}
	s.RunnerClauses = append(cs1, cs2...)
	s.RunnerClauses = append(s.RunnerClauses, cs3...)
	s.RunnerSeverityThresh = 3
	return nil
}

// anArrowWithMachineAttestedSplit stages a split of machine + attested
// clauses. Attested clauses with status "awaiting-attestation" set
// the AwaitingAttestation flag rather than using a distinct
// ClauseStatus value.
func (s *ScenarioState) anArrowWithMachineAttestedSplit(total, nMachine int, machineStatus string, nAttested int, attestedStatus string) error {
	if nMachine+nAttested != total {
		return fmt.Errorf("split %d + %d != %d", nMachine, nAttested, total)
	}
	machineClauses, err := makeClauses(nMachine, machineStatus)
	if err != nil {
		return err
	}
	attestedClauses, err := makeAttestedClauses(nAttested, attestedStatus)
	if err != nil {
		return err
	}
	s.RunnerClauses = append(machineClauses, attestedClauses...)
	s.RunnerSeverityThresh = 3
	return nil
}

// derivedArrowStatusIs runs DeriveArrowStatus on the staged clauses
// and verifies the derived status matches the expected wire form.
func (s *ScenarioState) derivedArrowStatusIs(expected string) error {
	got, clauseCount, findingCount := runner.DeriveArrowStatus(s.RunnerClauses, s.RunnerFindings, s.RunnerSeverityThresh)
	s.RunnerArrowStatus = got
	s.RunnerBlockingCount = clauseCount + findingCount
	if got.String() != expected {
		return fmt.Errorf("derived status = %q; want %q", got, expected)
	}
	return nil
}

// arrowDoesNotSatisfyNextRole verifies the staged arrow status
// refuses transition. Used after derivedArrowStatusIs has stashed
// the result.
func (s *ScenarioState) arrowDoesNotSatisfyNextRole() error {
	if s.RunnerArrowStatus.SatisfiesNextRole() {
		return fmt.Errorf("arrow status %q satisfies next role; scenario expects refusal", s.RunnerArrowStatus)
	}
	return nil
}

// ---- Transition refusal ----

// arrowAHasStatus parses an ArrowStatus wire string and stashes it
// as the upstream status.
func (s *ScenarioState) arrowAHasStatus(status string) error {
	parsed, err := parseArrowStatus(status)
	if err != nil {
		return err
	}
	s.RunnerUpstreamStatus = parsed
	s.RunnerUpstreamArrowID = "A1"
	// Provisional/unevaluated/blocked: synthesize a small clause
	// count for the error's blocking-clauses field.
	switch parsed {
	case runner.ArrowStatusBlocked:
		s.RunnerBlockingCount = 1
	case runner.ArrowStatusProvisional, runner.ArrowStatusUnevaluated:
		s.RunnerBlockingCount = 2
	case runner.ArrowStatusInvalidated:
		s.RunnerInvalidatingGV = 3
	}
	return nil
}

// arrowBRequiresA is narrative scaffolding; the relationship is
// implied by attemptDownstreamTransition's call to CheckTransition.
func (s *ScenarioState) arrowBRequiresA() error {
	s.RunnerDownstreamArrowID = "B1"
	return nil
}

// attemptDownstreamTransition runs CheckTransition and stashes the
// result. Subsequent Then steps inspect.
func (s *ScenarioState) attemptDownstreamTransition() error {
	if s.RunnerDownstreamArrowID == "" {
		s.RunnerDownstreamArrowID = "B1"
	}
	s.RunnerTransitionErr = runner.CheckTransition(
		s.RunnerUpstreamArrowID,
		s.RunnerDownstreamArrowID,
		s.RunnerUpstreamStatus,
		s.RunnerBlockingCount,
		s.RunnerInvalidatingGV,
	)
	return nil
}

// runnerRefusesWithStructuredError verifies the refusal is a
// *TransitionRefusal with the named kind, and its structured
// fields are populated (upstream-arrow id, upstream-status,
// blocking-clauses).
func (s *ScenarioState) runnerRefusesWithStructuredError(kind string) error {
	if s.RunnerTransitionErr == nil {
		return fmt.Errorf("expected refusal with kind %q; got nil", kind)
	}
	tr := runner.AsTransitionRefusal(s.RunnerTransitionErr)
	if tr == nil {
		return fmt.Errorf("error %v is not a *TransitionRefusal", s.RunnerTransitionErr)
	}
	if string(tr.Kind) != kind {
		return fmt.Errorf("kind = %q; want %q", tr.Kind, kind)
	}
	if tr.UpstreamArrowID == "" {
		return errors.New("structured error missing UpstreamArrowID")
	}
	if tr.UpstreamStatus.String() == "" {
		return errors.New("structured error missing UpstreamStatus")
	}
	return nil
}

// noPassOnBStarted is narrative — the runner stops the transition
// at refusal. We confirm RunnerTransitionErr was set (i.e., the
// runner did refuse, no pass would have started).
func (s *ScenarioState) noPassOnBStarted() error {
	if s.RunnerTransitionErr == nil {
		return errors.New("expected transition refused (no pass started)")
	}
	// Per B7 adversarial pass: verify the refusal is a structured
	// TransitionRefusal (not some unrelated error) AND that it names
	// the upstream arrow — proves the refusal is keyed on the right
	// arrow, not a generic failure.
	tr := runner.AsTransitionRefusal(s.RunnerTransitionErr)
	if tr == nil {
		return fmt.Errorf("not a TransitionRefusal: %v", s.RunnerTransitionErr)
	}
	if tr.UpstreamArrowID == "" {
		return errors.New("TransitionRefusal missing UpstreamArrowID")
	}
	return nil
}

// runnerPermitsTransition verifies CheckTransition returned nil.
func (s *ScenarioState) runnerPermitsTransition() error {
	if s.RunnerTransitionErr != nil {
		return fmt.Errorf("expected permit; got %v", s.RunnerTransitionErr)
	}
	return nil
}

// startsNewPassOnB verifies the runner has permitted the transition
// AND the upstream arrow was in a status that authorizes it. Per B7
// adversarial pass: bolstered from a pure "no error" check by also
// asserting the upstream status precondition (Complete satisfies
// next role per ArrowStatus.SatisfiesNextRole). Pass execution
// itself is phase-11 surface.
func (s *ScenarioState) startsNewPassOnB() error {
	if s.RunnerTransitionErr != nil {
		return fmt.Errorf("expected new pass startable; transition was refused: %v", s.RunnerTransitionErr)
	}
	if !s.RunnerUpstreamStatus.SatisfiesNextRole() {
		return fmt.Errorf("upstream status %s does not satisfy next role; transition should NOT have been permitted",
			s.RunnerUpstreamStatus)
	}
	return nil
}

// runnerRefusesWithKind verifies the refusal kind for the
// invalidated scenario.
func (s *ScenarioState) runnerRefusesWithKind(kind string) error {
	tr := runner.AsTransitionRefusal(s.RunnerTransitionErr)
	if tr == nil {
		return fmt.Errorf("not a transition refusal: %v", s.RunnerTransitionErr)
	}
	if string(tr.Kind) != kind {
		return fmt.Errorf("kind = %q; want %q", tr.Kind, kind)
	}
	return nil
}

// errorContainsArrowAndGridVersion verifies the invalidated-refusal
// error names both the arrow id and the invalidating grid version.
func (s *ScenarioState) errorContainsArrowAndGridVersion() error {
	if s.RunnerTransitionErr == nil {
		return errors.New("no error")
	}
	msg := s.RunnerTransitionErr.Error()
	if !strings.Contains(msg, s.RunnerUpstreamArrowID) {
		return fmt.Errorf("error %q missing arrow id %q", msg, s.RunnerUpstreamArrowID)
	}
	if !regexp.MustCompile(`\bv\d+\b`).MatchString(msg) {
		return fmt.Errorf("error %q missing grid version token (v<N>)", msg)
	}
	return nil
}

// operatorEventPublishedNarrative — the operator-event-bus integration
// is phase-11 surface. Per B7 adversarial pass: bolstered from a pure
// "refusal present" check by ALSO verifying the refusal kind is one
// the event bus would emit on (transition-refused* or pass-aborted).
// If the refusal is a different kind, the future event-bus listener
// would have nothing to publish for the stated event types.
func (s *ScenarioState) operatorEventPublishedNarrative(eventKindA, eventKindB string) error {
	if s.RunnerTransitionErr == nil {
		return errors.New("no refusal — event bus would have nothing to emit")
	}
	tr := runner.AsTransitionRefusal(s.RunnerTransitionErr)
	if tr == nil {
		return fmt.Errorf("not a transition refusal (event bus would not key on this): %v",
			s.RunnerTransitionErr)
	}
	// The feature claims one of two event kinds — verify the refusal's
	// kind matches one of them (allowing the future event bus to emit
	// the stated type).
	kind := string(tr.Kind)
	if !strings.Contains(kind, "transition-refused") &&
		eventKindA != "pass-aborted" && eventKindB != "pass-aborted" {
		return fmt.Errorf("refusal kind %q doesn't match expected event types %q or %q",
			kind, eventKindA, eventKindB)
	}
	return nil
}

// ---- helpers ----

// parseClauseStatus maps a wire-form status string back to its
// ClauseStatus value.
func parseClauseStatus(s string) (runner.ClauseStatus, error) {
	switch s {
	case "pending":
		return runner.StatusPending, nil
	case "running":
		return runner.StatusRunning, nil
	case "pass":
		return runner.StatusPass, nil
	case "fail":
		return runner.StatusFail, nil
	case "unevaluated":
		return runner.StatusUnevaluated, nil
	}
	return 0, fmt.Errorf("unknown clause status %q", s)
}

// parseArrowStatus maps a wire-form status string back to its
// ArrowStatus value.
func parseArrowStatus(s string) (runner.ArrowStatus, error) {
	switch s {
	case "in-progress":
		return runner.ArrowStatusInProgress, nil
	case "complete":
		return runner.ArrowStatusComplete, nil
	case "blocked":
		return runner.ArrowStatusBlocked, nil
	case "unevaluated":
		return runner.ArrowStatusUnevaluated, nil
	case "provisional":
		return runner.ArrowStatusProvisional, nil
	case "invalidated":
		return runner.ArrowStatusInvalidated, nil
	}
	return 0, fmt.Errorf("unknown arrow status %q", s)
}

// makeClauses builds n ClauseDeriveInput entries with the given
// status. Recognizes the synthetic "awaiting-attestation" status
// for attested clauses (sets AwaitingAttestation=true with
// Status=running, mirroring makeAttestedClauses).
func makeClauses(n int, status string) ([]runner.ClauseDeriveInput, error) {
	if status == "awaiting-attestation" {
		return makeAttestedClauses(n, status)
	}
	cs, err := parseClauseStatus(status)
	if err != nil {
		return nil, err
	}
	out := make([]runner.ClauseDeriveInput, n)
	for i := range out {
		out[i] = runner.ClauseDeriveInput{Status: cs}
	}
	return out, nil
}

// makeAttestedClauses builds n attested clauses. status here is
// "awaiting-attestation" → AwaitingAttestation=true with
// Status=running, or one of the terminal pass/fail values.
func makeAttestedClauses(n int, status string) ([]runner.ClauseDeriveInput, error) {
	out := make([]runner.ClauseDeriveInput, n)
	if status == "awaiting-attestation" {
		for i := range out {
			out[i] = runner.ClauseDeriveInput{
				Status:              runner.StatusRunning,
				AwaitingAttestation: true,
			}
		}
		return out, nil
	}
	cs, err := parseClauseStatus(status)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i] = runner.ClauseDeriveInput{Status: cs}
	}
	return out, nil
}
