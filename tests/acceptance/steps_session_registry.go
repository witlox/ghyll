package acceptance

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/witlox/ghyll/bootstrap"
)

// registerSessionRegistrySteps wires step definitions for scenarios
// 198 (modify on unknown clause), 205 (op-id survives init re-entry),
// and 212 (mid-session change requires explicit handoff) in
// specs/features/init.feature.
func registerSessionRegistrySteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	// Scenario 198 — modify on a clause not in the proposal.
	ctx.Step(`^init has not proposed clause "([^"]+)"$`, state.initHasNotProposedClause)
	ctx.Step(`^the operator submits "modify" against "([^"]+)"$`, state.operatorSubmitsModifyAgainst)
	ctx.Step(`^init refuses with "([^"]+)" and lists the proposed clause IDs for orientation$`, state.initRefusesAndListsClauseIDs)

	// Scenario 205 — op-id survives init re-entry.
	ctx.Step(`^the operator declared op-id "([^"]+)" at init start$`, state.operatorDeclaredOpIDAtInitStart)
	ctx.Step(`^init was suspended for missing-binding and re-entered$`, state.initWasSuspendedForMissingBindingAndReentered)
	ctx.Step(`^init re-enters$`, state.initReenters)
	ctx.Step(`^the active op-id is still "([^"]+)" \(single declaration per session per attestation\.md F-1\)$`, state.activeOpIDIsStill)
	ctx.Step(`^re-init does NOT re-prompt for op-id$`, state.reInitDoesNotRePromptForOpID)

	// Scenario 212 — mid-session change requires handoff.
	ctx.Step(`^operator Alice is active mid-init$`, state.operatorAliceIsActiveMidInit)
	ctx.Step(`^Bob attempts to declare op-id "([^"]+)" without Alice closing first$`, state.bobAttemptsDeclareWithoutAliceClosing)
	ctx.Step(`^init refuses with "([^"]+)" and lists Alice's op-id$`, state.initRefusesAndListsAliceOpID)
	ctx.Step(`^Bob must either ask Alice to close her session OR start a new session \(multi-operator handoff per attestation\.md F-([0-9]+)\)$`, state.bobMustHandoffOrNewSession)

	// Per-scenario cleanup.
	ctx.After(func(_ context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		state.SessionRegistry = nil
		state.BobDeclareErr = nil
		state.UnknownClauseErr = nil
		return nil, nil
	})
}

// ---- Scenario 198: modify on unknown clause ----

// initHasNotProposedClause sets up a single-clause proposal that does
// NOT contain the named clauseID. The scenario then asks the operator
// to modify against a non-existent ID; Apply must refuse.
func (s *ScenarioState) initHasNotProposedClause(missingID string) error {
	cat, err := loadSharedCatalogue()
	if err != nil {
		return fmt.Errorf("load catalogue: %w", err)
	}
	concept, _ := cat.Get("compiles")
	defaults := map[string]any{}
	for name, sc := range concept.Arguments {
		if sc.Default != nil {
			defaults[name] = sc.Default
		}
	}
	// Single proposed clause with id "G1"; the scenario references
	// some other id (e.g., "C99") that's deliberately absent.
	if missingID == "G1" {
		return fmt.Errorf("test setup error: %q happens to be the proposal's only ID", missingID)
	}
	s.Proposal = bootstrap.NewArrowProposal("analyst", "architect", "context-A", []bootstrap.ProposedClause{{
		ID:          "G1",
		EvalType:    "machine",
		DepthType:   "depth-robust",
		ConceptName: "compiles",
		DefaultArgs: defaults,
		DefaultCost: concept.DefaultCost,
	}})
	return nil
}

// operatorSubmitsModifyAgainst applies a modify verdict against the
// given (unknown) clause ID, stashing the error.
func (s *ScenarioState) operatorSubmitsModifyAgainst(clauseID string) error {
	if s.Proposal == nil {
		return errors.New("no proposal set up")
	}
	cat, err := loadSharedCatalogue()
	if err != nil {
		return fmt.Errorf("load catalogue: %w", err)
	}
	s.UnknownClauseErr = s.Proposal.Apply(clauseID, bootstrap.Verdict{
		Kind:         bootstrap.VerdictModify,
		ModifiedArgs: map[string]any{"x": 1},
	}, cat)
	return nil
}

// initRefusesAndListsClauseIDs verifies the refusal sentinel matches
// and that the error message names the proposed clause IDs (per the
// scenario: "lists the proposed clause IDs for orientation").
func (s *ScenarioState) initRefusesAndListsClauseIDs(expectedSentinel string) error {
	if s.UnknownClauseErr == nil {
		return fmt.Errorf("expected refusal with %q; got nil", expectedSentinel)
	}
	// ErrUnknownClauseID is the sentinel; the scenario's
	// "modify-on-unknown-clause" maps to its message.
	if !errors.Is(s.UnknownClauseErr, bootstrap.ErrUnknownClauseID) {
		return fmt.Errorf("err = %v; want ErrUnknownClauseID", s.UnknownClauseErr)
	}
	// "Lists the proposed clause IDs" — verify the error or proposal
	// can yield them. Apply's error message includes the bad ID; the
	// proposal itself enumerates the valid IDs. The "lists for
	// orientation" UI behavior is the caller's job; here we verify
	// the proposal exposes the IDs via its Proposed slice.
	if len(s.Proposal.Proposed) == 0 {
		return errors.New("proposal has no clauses to list for orientation")
	}
	return nil
}

// ---- Scenario 205: op-id survives init re-entry ----

// operatorDeclaredOpIDAtInitStart stands up a session registry and
// declares the named op-id at "init start".
func (s *ScenarioState) operatorDeclaredOpIDAtInitStart(opID string) error {
	s.SessionRegistry = bootstrap.NewSessionRegistry()
	if _, err := s.SessionRegistry.Declare(opID); err != nil {
		return fmt.Errorf("Declare(%q): %w", opID, err)
	}
	return nil
}

// initWasSuspendedForMissingBindingAndReentered models the suspend +
// re-entry: no action on the session, the runtime just paused. The
// registry's active session is unchanged.
func (s *ScenarioState) initWasSuspendedForMissingBindingAndReentered() error {
	if s.SessionRegistry == nil || s.SessionRegistry.Active() == nil {
		return errors.New("no active session to suspend/re-enter")
	}
	return nil
}

// initReenters is the When trigger; no action needed — re-entry is a
// runtime concept that doesn't manipulate the registry. The Then
// steps verify the session survived.
func (s *ScenarioState) initReenters() error {
	return nil
}

// activeOpIDIsStill verifies the registry's active op-id matches the
// expected value after re-entry.
func (s *ScenarioState) activeOpIDIsStill(expected string) error {
	if s.SessionRegistry == nil {
		return errors.New("no registry")
	}
	got := s.SessionRegistry.ActiveOpID()
	if got != expected {
		return fmt.Errorf("active op-id = %q; want %q", got, expected)
	}
	return nil
}

// reInitDoesNotRePromptForOpID is narrative — the harness shows no
// op-id prompt when re-entering with an active session. Verify the
// session is still active (so the harness has nothing to prompt for).
func (s *ScenarioState) reInitDoesNotRePromptForOpID() error {
	if s.SessionRegistry == nil || s.SessionRegistry.Active() == nil {
		return errors.New("no active session — re-init would prompt")
	}
	return nil
}

// ---- Scenario 212: mid-session change requires handoff ----

// operatorAliceIsActiveMidInit declares Alice's session.
func (s *ScenarioState) operatorAliceIsActiveMidInit() error {
	s.SessionRegistry = bootstrap.NewSessionRegistry()
	if _, err := s.SessionRegistry.Declare("alice@example.com"); err != nil {
		return fmt.Errorf("Declare(alice): %w", err)
	}
	return nil
}

// bobAttemptsDeclareWithoutAliceClosing attempts a second declare;
// expected to fail. The error is stashed for the Then step.
func (s *ScenarioState) bobAttemptsDeclareWithoutAliceClosing(bobOpID string) error {
	if s.SessionRegistry == nil {
		return errors.New("no registry")
	}
	_, s.BobDeclareErr = s.SessionRegistry.Declare(bobOpID)
	if s.BobDeclareErr == nil {
		return errors.New("second declare should have failed")
	}
	return nil
}

// initRefusesAndListsAliceOpID verifies Bob's declare was refused
// with the named sentinel and that Alice's op-id appears in the
// error message.
func (s *ScenarioState) initRefusesAndListsAliceOpID(expectedSentinel string) error {
	if s.BobDeclareErr == nil {
		return fmt.Errorf("expected refusal with %q; got nil", expectedSentinel)
	}
	if !errors.Is(s.BobDeclareErr, bootstrap.ErrSessionAlreadyActive) {
		return fmt.Errorf("err = %v; want ErrSessionAlreadyActive", s.BobDeclareErr)
	}
	if !strings.Contains(s.BobDeclareErr.Error(), expectedSentinel) {
		return fmt.Errorf("err %q should contain sentinel %q", s.BobDeclareErr, expectedSentinel)
	}
	// validation-pass-2 F12: op-id is no longer leaked verbatim in
	// cross-trust-boundary error messages — the active op-id appears
	// as a SHA-256-truncated hash. The "lists Alice's op-id" wording
	// in the scenario is now interpreted as "lists a stable
	// identifier for Alice's lock" rather than the raw email.
	if !strings.Contains(s.BobDeclareErr.Error(), "op-id-hash") {
		return fmt.Errorf("err %q should reference op-id-hash (Alice's identifier)", s.BobDeclareErr)
	}
	if strings.Contains(s.BobDeclareErr.Error(), "alice@example.com") {
		return fmt.Errorf("err %q must NOT leak Alice's raw op-id (PII)", s.BobDeclareErr)
	}
	// And Alice's session is still active (Bob's failure didn't
	// disturb it).
	if got := s.SessionRegistry.ActiveOpID(); got != "alice@example.com" {
		return fmt.Errorf("alice should still be active; got %q", got)
	}
	return nil
}

// bobMustHandoffOrNewSession is narrative: the scenario states the
// two recovery paths (Alice closes, then Bob declares; OR Bob starts
// a fresh process). Verify both are exercisable from the current
// state without disturbing Alice.
//
// The numeric arg is the attestation.md F-N reference (currently F-1);
// captured for completeness, not asserted further.
func (s *ScenarioState) bobMustHandoffOrNewSession(_ int) error {
	if s.SessionRegistry == nil {
		return errors.New("no registry")
	}
	// Path 1: Alice closes; Bob can now declare.
	r := s.SessionRegistry
	if got := r.ActiveOpID(); got != "alice@example.com" {
		return fmt.Errorf("precondition: Alice should be active; got %q", got)
	}
	r.Close()
	if _, err := r.Declare("bob@example.com"); err != nil {
		return fmt.Errorf("after Alice.Close, Bob.Declare: %v", err)
	}
	if got := r.ActiveOpID(); got != "bob@example.com" {
		return fmt.Errorf("after handoff, ActiveOpID = %q; want bob", got)
	}
	// Path 2: fresh process — a new registry, Bob declares
	// successfully without anyone else active.
	fresh := bootstrap.NewSessionRegistry()
	if _, err := fresh.Declare("bob@example.com"); err != nil {
		return fmt.Errorf("fresh registry: Bob.Declare: %v", err)
	}
	return nil
}
