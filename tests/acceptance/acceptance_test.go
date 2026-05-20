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
			},
			Output:   colors.Colored(os.Stdout),
			TestingT: t,
			// Strict mode: undefined or pending steps fail the
			// suite. All 13 original step-regex ambiguities have
			// been consolidated (the last three resolved by
			// lifting closure-local state to ScenarioState).
			Strict: true,
			// Tag filter — skip scenarios tagged `@deferred`. They
			// describe surface that depends on code not yet shipped
			// (full attestation event bus, Pass entity, ProjectStatus
			// aggregator, crash recovery).
			//
			// godog tag-expression syntax: `~@tag` negates (skip
			// scenarios carrying the tag).
			Tags: "~@deferred",
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

	// Init step definitions (specs/features/init.feature).
	registerInitSteps(ctx, state)
	registerProposeSteps(ctx, state)
	registerProfileSteps(ctx, state)
	registerRefusalSteps(ctx, state)
	registerBindingSteps(ctx, state)
	registerInitDriverSteps(ctx, state)
	registerSessionRegistrySteps(ctx, state)
	registerModifyNonMonotonicSteps(ctx, state)
	registerOrphanSteps(ctx, state)

	// Runner step definitions (specs/features/runner.feature).
	registerRunnerSteps(ctx, state)
	registerRunnerSubprocessSteps(ctx, state)

	// State-machine step definitions (specs/features/state-machine.feature).
	registerStateMachineSteps(ctx, state)

	// Amendment step definitions (specs/features/amendment.feature).
	registerAmendmentSteps(ctx, state)

	// Attestation step definitions (specs/features/attestation.feature).
	// Wires the surfaces that exist today (op-id validation, FindingStore
	// operator transitions). Deferred surface (full operator event bus,
	// JSONL verdict records) is tagged @deferred.
	registerAttestationSteps(ctx, state)

	// Adversarial step definitions (specs/features/adversarial.feature).
	// Wires per-round Adversary.Attack + OpenSweep/Classify hooks +
	// Findings raise/derive. Orchestrator-level concerns (multi-round
	// remediation, producer-fix-signal, operator-event-bus) are
	// tagged @deferred.
	registerAdversarialSteps(ctx, state)

	// Pass-lifecycle step definitions (state-machine.feature +
	// runner.feature scenarios that exercise Pass / PassRegistry
	// / AmendmentCommitter).
	registerPassLifecycleSteps(ctx, state)

	// Amendment-commit + pass-identity scenarios that exercise the
	// AmendmentCommitter end-to-end (amendment.feature).
	registerAmendmentDeferredSteps(ctx, state)

	// Adversarial deferred batch — multi-round remediation,
	// loop-bomb detection, bounded escalation, verification
	// auto-insert (adversarial.feature).
	registerAdversarialDeferredSteps(ctx, state)

	// Adversarial producer-fix-signal + accepted-risk-proposal
	// batch — wires the typed producer-fix messages and the
	// audit-trail of sub-activities post-Tier-2 substrate
	// (adversarial.feature Batch 5).
	registerAdversarialProducerFixSteps(ctx, state)

	// Runner deferred batch — concurrent / refused / amendment
	// abort / depth-gate scenarios (runner.feature).
	registerRunnerDeferredSteps(ctx, state)

	// Attestation deferred batch — verifier-reads-attestation-log
	// scenario (attestation.feature).
	registerAttestationDeferredSteps(ctx, state)

	// Tier 1 crash-recovery — 7 deferred scenarios across
	// state-machine.feature + runner.feature (ADR-015).
	registerTier1RecoverySteps(ctx, state)

	// Tier 2 modal + path encoding — three-role chain, init path,
	// missing-required-field detection (attestation.feature
	// @deferred lifts, ADR-016 Step 15).
	registerTier2ModalSteps(ctx, state)

	// Attestation modal deferred-lift batch — multi-operator
	// handoff, verdict pass/fail/IB, 3-round escalation, accepted-
	// risk, route-upstream, oversized residue, near-simultaneous
	// verdicts (attestation.feature post-Tier-2 lifts).
	registerAttestationModalSteps(ctx, state)
}

// ScenarioState is defined in state.go (shared across step files).
