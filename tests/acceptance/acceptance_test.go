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
			Format:   "pretty",
			Paths:    []string{"../../specs/features"},
			Output:   colors.Colored(os.Stdout),
			TestingT: t,
			// Strict mode: undefined/pending steps cause failure.
			// Disable during early development to see what's missing.
			Strict: false,
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
	registerCompactionSteps(ctx, state)
	registerVaultSteps(ctx, state)
	registerKeySteps(ctx, state)
}

// ScenarioState is defined in state.go (shared across step files).
