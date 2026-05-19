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

// registerRefusalSteps wires step definitions for the refusal-flow
// scenarios in specs/features/init.feature (25, 66, 77).
//
// The refusal flow is the project's correctness-via-friction lever:
// init refuses to run on projects ghyll is wrong for.
func registerRefusalSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	// Scenario 25 — greenfield refusal recommendation (prose profile).
	ctx.Step(`^a project the operator describes as "([^"]+)"$`, state.aProjectOperatorDescribesAs)
	ctx.Step(`^init evaluates the project profile$`, state.initEvaluatesTheProjectProfile)
	ctx.Step(`^init proposes refusal with rationale$`, state.initProposesRefusalWithRationale)
	ctx.Step(`^the operator either accepts refusal \(init exits\) or overrides \(init proceeds with a residue note recording the override\)$`, state.operatorAcceptsOrOverridesNarrative)

	// Scenarios 66 + 77 — table-based profile.
	ctx.Step(`^a project profile with$`, state.aProjectProfileWith)
	ctx.Step(`^init evaluates the profile$`, state.initEvaluatesTheProfile)
	ctx.Step(`^init proposes refusal$`, state.initProposesRefusal)
	ctx.Step(`^the operator may accept \(init exits, ghyll terminates\) or override \(residue note required, init proceeds\)$`, state.operatorAcceptsOrOverridesNarrative)
	ctx.Step(`^init proceeds without proposing refusal$`, state.initProceedsWithoutRefusal)
	ctx.Step(`^the auto-propose flow runs$`, state.theAutoProposeFlowRuns)

	// Reset risk-assessment-only state between scenarios. (Profile
	// state is cleaned by steps_profile.go's After hook.)
	ctx.After(func(_ context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		state.PendingRisk = bootstrap.RiskAssessment{}
		state.RiskEvaluated = false
		return nil, nil
	})
}

// ---- Scenario 25: prose-described project ----

// aProjectOperatorDescribesAs maps the prose description to a
// RiskAssessment. The scenario's prose ("throwaway script, one
// bounded context, no novel architecture") indicates all signals are
// low-stakes; we hardcode that mapping here. In a real harness this
// would be the operator's interrogation answers; the test pins the
// expected mapping for the canonical phrasing.
func (s *ScenarioState) aProjectOperatorDescribesAs(description string) error {
	// Naive keyword mapping. The scenario uses canonical phrasing
	// ("throwaway", "one bounded context", "no novel architecture")
	// that we read for risk signals. Future scenarios may add other
	// canonical phrases; extend the map then, don't try to NLP.
	lower := strings.ToLower(description)
	risk := bootstrap.RiskAssessment{}
	if strings.Contains(lower, "one bounded context") || strings.Contains(lower, "single bounded context") {
		risk.BoundedContextCount = 1
	}
	if strings.Contains(lower, "no novel architecture") {
		risk.NovelArchitecture = false
	}
	if strings.Contains(lower, "novel architecture") && !strings.Contains(lower, "no novel architecture") {
		risk.NovelArchitecture = true
	}
	if strings.Contains(lower, "throwaway") || strings.Contains(lower, "prototype") {
		// Throwaway script implies not correctness-critical.
		risk.CorrectnessCritical = false
	}
	if strings.Contains(lower, "correctness-critical") || strings.Contains(lower, "production") {
		risk.CorrectnessCritical = true
	}
	s.PendingRisk = risk
	return nil
}

// initEvaluatesTheProjectProfile runs ProposeRefusal on the profile +
// pending risk. Creates a minimal ProjectProfile if none exists.
func (s *ScenarioState) initEvaluatesTheProjectProfile() error {
	if s.Profile == nil {
		s.Profile = &bootstrap.ProjectProfile{}
	}
	if _, err := s.Profile.ProposeRefusal(s.PendingRisk); err != nil {
		return fmt.Errorf("ProposeRefusal: %w", err)
	}
	s.RiskEvaluated = true
	return nil
}

// initProposesRefusalWithRationale verifies the refusal was proposed
// and its rationale is non-empty.
func (s *ScenarioState) initProposesRefusalWithRationale() error {
	if !s.RiskEvaluated {
		return errors.New("risk not evaluated; precondition unmet")
	}
	if !s.Profile.RefusalProposed() {
		return errors.New("expected refusal to be proposed; got proceed")
	}
	if got := s.PendingRisk.Rationale(); got == "" {
		return errors.New("rationale is empty")
	}
	return nil
}

// operatorAcceptsOrOverridesNarrative is the narrative tail step.
// Verifies both paths are exercisable: AcceptRefusal must succeed,
// and OverrideRefusal must enforce non-empty residue. We use a fresh
// transient profile so the assertions don't disturb the scenario's
// main profile.
func (s *ScenarioState) operatorAcceptsOrOverridesNarrative() error {
	// Exercise the accept path on a transient profile.
	pAccept := &bootstrap.ProjectProfile{}
	if _, err := pAccept.ProposeRefusal(bootstrap.RiskAssessment{}); err != nil {
		return fmt.Errorf("ProposeRefusal (accept-side): %w", err)
	}
	if err := pAccept.AcceptRefusal(); err != nil {
		return fmt.Errorf("AcceptRefusal exercises: %w", err)
	}
	if !pAccept.RefusalAccepted() {
		return errors.New("accept-side: RefusalAccepted should be true")
	}
	// Exercise the override path: must refuse empty residue, accept
	// non-empty.
	pOverride := &bootstrap.ProjectProfile{}
	if _, err := pOverride.ProposeRefusal(bootstrap.RiskAssessment{}); err != nil {
		return fmt.Errorf("ProposeRefusal (override-side): %w", err)
	}
	if err := pOverride.OverrideRefusal(""); !errors.Is(err, bootstrap.ErrRefusalOverrideEmpty) {
		return fmt.Errorf("override-side: empty residue should fail with ErrRefusalOverrideEmpty; got %v", err)
	}
	if err := pOverride.OverrideRefusal("we want the machinery for training"); err != nil {
		return fmt.Errorf("override-side: non-empty residue should succeed; got %v", err)
	}
	return nil
}

// ---- Scenarios 66 + 77: table-based profile ----

// aProjectProfileWith parses the | property | value | table and
// builds a RiskAssessment.
//
// Recognized properties (case-insensitive on the property name):
//
//   - "bounded contexts"     int
//   - "cross-context seams"  int
//   - "novel architecture"   bool ("true" / "false")
//   - "correctness-critical" bool ("true" / "false")
//
// Unknown properties are an error (caught before the test acts).
func (s *ScenarioState) aProjectProfileWith(table *godog.Table) error {
	if table == nil || len(table.Rows) < 2 {
		return errors.New("table must have header + at least one row")
	}
	// Verify the header: | property | value |.
	header := table.Rows[0]
	if len(header.Cells) != 2 ||
		strings.TrimSpace(header.Cells[0].Value) != "property" ||
		strings.TrimSpace(header.Cells[1].Value) != "value" {
		return fmt.Errorf("table header must be | property | value |; got %v", header.Cells)
	}
	risk := bootstrap.RiskAssessment{}
	for i := 1; i < len(table.Rows); i++ {
		row := table.Rows[i]
		if len(row.Cells) != 2 {
			return fmt.Errorf("row %d: expected 2 cells, got %d", i, len(row.Cells))
		}
		key := strings.ToLower(strings.TrimSpace(row.Cells[0].Value))
		val := strings.TrimSpace(row.Cells[1].Value)
		switch key {
		case "bounded contexts":
			n, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("bounded contexts: %q not an int", val)
			}
			risk.BoundedContextCount = n
		case "cross-context seams":
			n, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("cross-context seams: %q not an int", val)
			}
			risk.CrossContextSeams = n
		case "novel architecture":
			b, err := strconv.ParseBool(val)
			if err != nil {
				return fmt.Errorf("novel architecture: %q not a bool", val)
			}
			risk.NovelArchitecture = b
		case "correctness-critical":
			b, err := strconv.ParseBool(val)
			if err != nil {
				return fmt.Errorf("correctness-critical: %q not a bool", val)
			}
			risk.CorrectnessCritical = b
		default:
			return fmt.Errorf("unknown profile property %q", key)
		}
	}
	s.PendingRisk = risk
	return nil
}

// initEvaluatesTheProfile runs ProposeRefusal against the
// table-derived risk. Same body as the scenario-25 variant; the only
// difference is which Given step populated PendingRisk.
func (s *ScenarioState) initEvaluatesTheProfile() error {
	return s.initEvaluatesTheProjectProfile()
}

// initProposesRefusal verifies a refusal was proposed (scenario 66).
func (s *ScenarioState) initProposesRefusal() error {
	if !s.RiskEvaluated {
		return errors.New("risk not evaluated")
	}
	if !s.Profile.RefusalProposed() {
		return errors.New("expected refusal; got proceed")
	}
	return nil
}

// initProceedsWithoutRefusal verifies the recommendation was Proceed
// (scenario 77).
func (s *ScenarioState) initProceedsWithoutRefusal() error {
	if !s.RiskEvaluated {
		return errors.New("risk not evaluated")
	}
	if s.Profile.RefusalProposed() {
		return errors.New("expected proceed; got refusal proposed")
	}
	return nil
}

// theAutoProposeFlowRuns is a narrative confirmation that the
// auto-propose machinery is reachable from this Proceed verdict. We
// validate the precondition: a non-nil profile in proceed state.
// Actual auto-propose execution is exercised by scenario 89 etc.
func (s *ScenarioState) theAutoProposeFlowRuns() error {
	if s.Profile == nil {
		return errors.New("no profile")
	}
	if s.Profile.RefusalProposed() {
		return errors.New("auto-propose should not run when refusal was proposed")
	}
	return nil
}
