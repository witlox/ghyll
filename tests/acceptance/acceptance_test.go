package acceptance

import (
	"os"
	"testing"

	"github.com/cucumber/godog"
	"github.com/cucumber/godog/colors"
)

// TestFeatures runs all godog acceptance scenarios.
// Feature files are loaded from specs/features/*.feature.
func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			Format: "pretty",
			Paths: []string{
				"../../specs/features",
				"../../specs/v2/features/init.feature",
				"../../specs/v2/features/runner-step3.feature",
				"../../specs/v2/features/state-machine.feature",
				"../../specs/v2/features/amendment.feature",
			},
			Output:   colors.Colored(os.Stdout),
			TestingT: t,
			// Strict mode: undefined/pending steps cause failure.
			//
			// Currently FALSE because the v1 BDD suite has ~75 SHALLOW
			// state-theater scenarios that return nil from steps without
			// calling production code (audited 2026-05-19). Setting
			// Strict=true would fail them all, drowning out new-scenario
			// regressions in noise.
			//
			// Roadmap: when Phase C (lift v1 SHALLOW → THOROUGH) finishes
			// and the v1 step files only return nil with explicit
			// `godog.ErrPending` for known-deferred bodies, flip this to
			// true so undefined-by-omission surfaces loudly. Phase F
			// (final tag) requires Strict=true.
			Strict: false,
			// Tag filter — skip @phase11-marked scenarios. They depend
			// on code surfaces not yet shipped (full attestation flow,
			// Pass entity, checkpoint log, ProjectStatus aggregator,
			// crash recovery). See specs/v2-final-plan.md D-4.
			//
			// godog's tag-expression syntax is cucumber-standard:
			// `~@tag` negates (skip scenarios with this tag). Verified
			// in this suite's run output — @phase11 scenarios do NOT
			// execute. The bare `@phase11` (without the tilde) would
			// run ONLY phase11 scenarios; the inverse, what we want, is
			// the tilde form.
			Tags: "~@phase11",
		},
	}

	if suite.Run() != 0 {
		t.Fatal("acceptance tests failed")
	}
}

// InitializeScenario registers all step definitions.
// Steps are organized by feature file — each feature has its own
// steps_*.go file to keep things navigable.
func InitializeScenario(ctx *godog.ScenarioContext) {
	// Cross-cutting state shared between steps
	state := &ScenarioState{}

	// Register steps by feature area
	registerConfigSteps(ctx, state)
	registerRoutingSteps(ctx, state)
	registerStreamSteps(ctx, state)
	registerMemorySteps(ctx, state)
	registerDriftSteps(ctx, state)
	registerSyncSteps(ctx, state)
	registerToolSteps(ctx, state)
	registerEditSteps(ctx, state)
	registerGlobSteps(ctx, state)
	registerWebSteps(ctx, state)
	registerCompactionSteps(ctx, state)
	registerVaultSteps(ctx, state)
	registerKeySteps(ctx, state)
	registerSessionFeatureSteps(ctx, state)

	// Init step definitions (specs/v2/features/init.feature).
	registerInitSteps(ctx, state)
	registerProposeSteps(ctx, state)
	registerProfileSteps(ctx, state)
	registerRefusalSteps(ctx, state)
	registerBindingSteps(ctx, state)
	registerInitDriverSteps(ctx, state)
	registerSessionRegistrySteps(ctx, state)
	registerModifyNonMonotonicSteps(ctx, state)
	registerOrphanSteps(ctx, state)

	// Runner step definitions (specs/v2/features/runner.feature).
	registerRunnerSteps(ctx, state)

	// State-machine step definitions (specs/v2/features/state-machine.feature).
	// Phase B1 of v2-final consolidation.
	registerStateMachineSteps(ctx, state)

	// Amendment step definitions (specs/v2/features/amendment.feature).
	// Phase B2 of v2-final consolidation.
	registerAmendmentSteps(ctx, state)
}

// ScenarioState is defined in state.go (shared across step files).
