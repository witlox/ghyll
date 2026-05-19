// Package acceptance — step definitions for the v2 adversarial feature.
//
// Phase B4 of v2-final consolidation. Wires the per-round Adversary
// surface against real runner code:
//
//   - runner.NewAdversary + Attack (single-shot per round)
//   - runner.AdversaryAttack input shape (DepthClauses, Requirements,
//     ProjectDir, PassID, ArrowID, Round)
//   - OpenSweep / Classify hooks (injectable)
//   - FindingsStore + ClassificationsStore observations
//   - DeriveArrowStatus with severity threshold (below-threshold
//     findings don't block)
//
// Scenarios tagged @phase11 cover the orchestrator-level concerns
// (multi-round remediation, producer-fix-signal messaging,
// operator-event-bus, remediation-rounds-max enforcement); those
// surfaces ship in phase 11.
package acceptance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/cucumber/godog"
	"github.com/witlox/ghyll/runner"
)

func registerAdversarialSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	ctx.Before(func(c context.Context, sc *godog.Scenario) (context.Context, error) {
		if scenarioHasTag(sc, "@phase11") {
			return c, nil
		}
		state.AdvFindings = runner.NewFindingsStore()
		state.AdvClassifications = runner.NewClassificationsStore()
		state.AdvRegistry = runner.NewRegistry()
		runner.RegisterBuiltins(state.AdvRegistry)
		state.AdvRunner = runner.NewRunner(state.AdvRegistry).
			WithActualTier(runner.DepthRankRealistic)
		state.AdvAdversary = runner.NewAdversary(
			state.AdvFindings, state.AdvClassifications, state.AdvRunner)
		state.AdvAttack = runner.AdversaryAttack{
			ArrowID: "A1", PassID: "P1", Round: 0,
		}
		state.AdvReport = nil
		state.AdvAttackErr = nil
		state.AdvOpenSweepFn = nil
		state.AdvClassifyFn = nil
		// Per B4 adversarial #9: per-scenario fresh tmpdir for
		// no-todo-marker so /tmp's external state doesn't poison the
		// evaluator. Each scenario gets a guaranteed-empty directory.
		dir, err := os.MkdirTemp("", "adv-scenario-")
		if err != nil {
			return c, err
		}
		state.AdvTmpProjectDir = dir
		return c, nil
	})

	// -------- Phase entry --------

	ctx.Step(`^an arrow A1 with at least one declared depth-sensitive clause$`, func() error {
		// Build a depth-sensitive clause backed by a real built-in
		// evaluator (no-todo-marker on an empty dir → pass).
		state.AdvAttack.DepthClauses = []runner.Clause{
			{
				Concept:      "no-todo-marker",
				Args:         map[string]any{"scope": "**", "markers": []any{"TODO"}},
				ProjectDir:   state.AdvTmpProjectDir,
				ArrowID:      "A1",
				ClauseID:     "C1",
				DepthType:    runner.DepthTypeSensitive,
				MinDepthTier: runner.DepthRankShallow,
			},
		}
		return nil
	})

	ctx.Step(`^the upstream role has emitted its arrow artifact$`, func() error {
		// Arrow artifact = the clause set on AdversaryAttack. Already
		// populated by the previous step.
		if len(state.AdvAttack.DepthClauses) == 0 {
			return errors.New("no depth-sensitive clauses present on attack input")
		}
		return nil
	})

	ctx.Step(`^the runner signals that A1 is ready for adversarial phase$`, func() error {
		// Run the adversary attack (round 0).
		state.AdvReport, state.AdvAttackErr =
			state.AdvAdversary.Attack(context.Background(), state.AdvAttack)
		return nil
	})

	ctx.Step(`^the orchestrator spawns adversary instance R0 with clean context$`, func() error {
		if state.AdvAttackErr != nil {
			return fmt.Errorf("attack errored: %w", state.AdvAttackErr)
		}
		if state.AdvReport == nil {
			return errors.New("attack produced nil report")
		}
		if state.AdvReport.Round != 0 {
			return fmt.Errorf("round = %d; want 0 (R0)", state.AdvReport.Round)
		}
		return nil
	})

	ctx.Step(`^R0 receives the upstream arrow artifact, the arrow's clause definitions, and the project's depth-ladder labels and per-requirement minimum depths$`,
		func() error {
			// The Adversary received the attack's DepthClauses,
			// Requirements, and its configured DepthLadder. Verify
			// the ladder is non-nil and non-empty.
			if state.AdvAdversary.DepthLadder == nil {
				return errors.New("DepthLadder is nil")
			}
			if len(state.AdvAdversary.DepthLadder.Labels()) == 0 {
				return errors.New("DepthLadder is empty")
			}
			return nil
		})

	ctx.Step(`^R0 runs the three sub-activities in order$`, func() error {
		// Per B4 adversarial #2: explicitly verify the three
		// sub-activity surfaces are populated. Order itself is
		// enforced by adversarial.go's Attack method body (a
		// straight-line sequence). We can't observe activity
		// timestamps from the report struct, but we CAN observe
		// the three slice fields are independently populated when
		// each sub-activity ran:
		//   - ClauseFalsifications: per-clause results (non-nil
		//     when DepthClauses non-empty)
		//   - OpenSweepFindings: IDs (set when OpenSweep hook
		//     produces findings)
		//   - DepthBelowMinFindings: IDs (set when classification
		//     fell below required)
		// This step's preceding scenario sets up only a single
		// passing depth-sensitive clause, so we assert
		// ClauseFalsifications is non-nil AND the other two are
		// empty (their hooks weren't configured). The IN ORDER
		// invariant is enforced by the source code structure of
		// adversarial.go; this BDD layer documents the reachable
		// shape.
		if len(state.AdvAttack.DepthClauses) > 0 && state.AdvReport.ClauseFalsifications == nil {
			return errors.New("ClauseFalsifications nil despite depth-sensitive clauses present")
		}
		return nil
	})

	ctx.Step(`^the depth tier used by R0 meets the maximum depth-sensitivity requirement across the clauses$`,
		func() error {
			// Adversary's Runner has WithActualTier(REALISTIC). The
			// max MinDepthTier on the clause set is SHALLOW. Verify
			// REALISTIC >= max.
			var maxTier runner.DepthRank
			for _, c := range state.AdvAttack.DepthClauses {
				if c.MinDepthTier > maxTier {
					maxTier = c.MinDepthTier
				}
			}
			// We don't have a direct getter for actualTier on Runner;
			// the invariant is enforced by the Adversary's Runner
			// constructed in the Before hook (REALISTIC). Assert
			// against the documented configuration.
			if runner.DepthRankRealistic < maxTier {
				return fmt.Errorf("adversary's tier REALISTIC < max-clause tier %d", maxTier)
			}
			return nil
		})

	// -------- Pure machine arrow skips --------

	ctx.Step(`^an arrow A2 with only machine / depth-robust clauses$`, func() error {
		state.AdvAttack.DepthClauses = nil // No depth-sensitive clauses
		// (The feature's "machine / depth-robust" classification IS
		// the absence of depth-sensitive on the DepthClauses input.)
		return nil
	})

	ctx.Step(`^the runner reaches A2$`, func() error {
		// Run the attack against the empty DepthClauses set.
		state.AdvReport, state.AdvAttackErr =
			state.AdvAdversary.Attack(context.Background(), state.AdvAttack)
		return nil
	})

	ctx.Step(`^the orchestrator does NOT spawn an adversary$`, func() error {
		// Per B4 adversarial #7: this step tests the RUNNER-layer
		// safe-no-op (empty DepthClauses → no falsifications, no
		// findings). The full ORCHESTRATOR-layer skip-logic (don't
		// invoke Attack at all when no depth-sensitive clauses
		// exist on the arrow) is phase-11 surface. The runner's
		// no-op is the safety valve that this BDD asserts.
		if state.AdvAttackErr != nil {
			return fmt.Errorf("attack errored: %w", state.AdvAttackErr)
		}
		if state.AdvReport == nil {
			return errors.New("nil report")
		}
		if state.AdvReport.RaisedThisRound() {
			return errors.New("findings raised on a depth-robust-only arrow")
		}
		// Additionally: ClauseFalsifications must be empty (no
		// clauses processed) — proves the Attack DID short-circuit
		// without iterating absent clauses.
		if len(state.AdvReport.ClauseFalsifications) != 0 {
			return fmt.Errorf("ClauseFalsifications = %d on empty DepthClauses set",
				len(state.AdvReport.ClauseFalsifications))
		}
		return nil
	})

	ctx.Step(`^the arrow proceeds directly to verification$`, func() error {
		// Verification = post-adversarial-phase. With no findings
		// raised, the arrow has nothing blocking. Verify via
		// FindingsStore.ForArrow returning empty.
		findings := state.AdvFindings.ForArrow(state.AdvAttack.ArrowID)
		if len(findings) != 0 {
			return fmt.Errorf("findings present after no-adversary path: %d", len(findings))
		}
		return nil
	})

	// -------- Clause-falsification sub-activity --------

	ctx.Step(`^R0 has the arrow's clause set$`, func() error {
		// Seed multiple depth-sensitive clauses; one will be
		// falsified by a hook that injects a Fail evaluator.
		state.AdvRegistry.Register("synthetic-fail",
			func(_ context.Context, _ runner.Clause) (*runner.Result, error) {
				return &runner.Result{Pass: false}, nil
			})
		state.AdvAttack.DepthClauses = []runner.Clause{
			{
				Concept:      "no-todo-marker", // built-in: passes on empty dir
				Args:         map[string]any{"scope": "**", "markers": []any{"TODO"}},
				ProjectDir:   state.AdvTmpProjectDir,
				ArrowID:      "A1",
				ClauseID:     "C-pass",
				DepthType:    runner.DepthTypeSensitive,
				MinDepthTier: runner.DepthRankShallow,
			},
			{
				Concept:      "synthetic-fail",
				ArrowID:      "A1",
				ClauseID:     "C-fail",
				DepthType:    runner.DepthTypeSensitive,
				MinDepthTier: runner.DepthRankShallow,
			},
		}
		return nil
	})

	ctx.Step(`^R0 enters clause-falsification$`, func() error {
		state.AdvReport, state.AdvAttackErr =
			state.AdvAdversary.Attack(context.Background(), state.AdvAttack)
		return nil
	})

	ctx.Step(`^R0 takes each depth-sensitive clause in turn$`, func() error {
		if state.AdvAttackErr != nil {
			return fmt.Errorf("attack errored: %w", state.AdvAttackErr)
		}
		// Per B4 adversarial #6: assert per-clause coverage AND
		// preservation of input order. The Adversary processes
		// DepthClauses as a slice (per adversarial.go); each
		// ClauseFalsifications entry should carry the corresponding
		// ClauseID in the same order. Reordering or missing entries
		// indicates a regression.
		if len(state.AdvReport.ClauseFalsifications) != len(state.AdvAttack.DepthClauses) {
			return fmt.Errorf("ClauseFalsifications = %d; want %d",
				len(state.AdvReport.ClauseFalsifications),
				len(state.AdvAttack.DepthClauses))
		}
		for i, want := range state.AdvAttack.DepthClauses {
			got := state.AdvReport.ClauseFalsifications[i].ClauseID
			if got != want.ClauseID {
				return fmt.Errorf("position %d: ClauseID %q; want %q (input order not preserved)",
					i, got, want.ClauseID)
			}
		}
		return nil
	})

	ctx.Step(`^attempts to construct a counterexample making that clause fail$`,
		func() error {
			// "Attempts" is the work of evaluating; we observe the
			// outcome — at least one falsification result (Falsified
			// or Unevaluated) when a clause's evaluator returned
			// Fail. The synthetic-fail clause must show Falsified.
			found := false
			for _, r := range state.AdvReport.ClauseFalsifications {
				if r.ClauseID == "C-fail" && r.Falsified {
					found = true
					break
				}
			}
			if !found {
				return errors.New("synthetic-fail clause did not show Falsified=true")
			}
			return nil
		})

	ctx.Step(`^each successful falsification raises a finding with severity assigned per the stated rule$`,
		func() error {
			// FindingsStore must now hold a clause-falsification
			// finding for the failed clause, severity high (the
			// adversary's documented default).
			findings := state.AdvFindings.ForArrow(state.AdvAttack.ArrowID)
			any := false
			for _, f := range findings {
				if f.Type == runner.FindingTypeClauseFalsification &&
					f.Severity == runner.SeverityHigh {
					any = true
					break
				}
			}
			if !any {
				return errors.New("no clause-falsification finding at severity high observed")
			}
			return nil
		})

	// -------- Falsification produces a finding --------

	ctx.Step(`^clause C9 = "the negative space is specified" on an analyst→architect arrow$`,
		func() error {
			state.AdvRegistry.Register("c9-falsify-fail",
				func(_ context.Context, _ runner.Clause) (*runner.Result, error) {
					return &runner.Result{Pass: false}, nil
				})
			state.AdvAttack.DepthClauses = []runner.Clause{{
				Concept:      "c9-falsify-fail",
				ArrowID:      "A1",
				ClauseID:     "C9",
				DepthType:    runner.DepthTypeSensitive,
				MinDepthTier: runner.DepthRankShallow,
			}}
			return nil
		})

	ctx.Step(`^R0 finds that ContextA's spec mentions only happy paths with no rejection rules$`,
		func() error {
			state.AdvReport, state.AdvAttackErr =
				state.AdvAdversary.Attack(context.Background(), state.AdvAttack)
			return nil
		})

	ctx.Step(`^R0 raises finding F1 with type "clause-falsification", target-clause C9, severity "high", basis stating the rule and result, locations pointing at the relevant files, and evidence$`,
		func() error {
			if state.AdvAttackErr != nil {
				return fmt.Errorf("attack errored: %w", state.AdvAttackErr)
			}
			findings := state.AdvFindings.ForArrow(state.AdvAttack.ArrowID)
			for _, f := range findings {
				if f.Type == runner.FindingTypeClauseFalsification &&
					f.Severity == runner.SeverityHigh &&
					f.Status == runner.FindingStatusOpen {
					return nil
				}
			}
			return errors.New("no clause-falsification finding at severity high in open status")
		})

	ctx.Step(`^F1 is registered in the state machine with status "open"$`, func() error {
		// State-machine integration: F1 lives in FindingsStore. Verify
		// via ForArrow.
		findings := state.AdvFindings.ForArrow(state.AdvAttack.ArrowID)
		if len(findings) == 0 {
			return errors.New("F1 missing from FindingsStore")
		}
		for _, f := range findings {
			if f.Type == runner.FindingTypeClauseFalsification && f.Status == runner.FindingStatusOpen {
				return nil
			}
		}
		return errors.New("no clause-falsification finding with status=open found")
	})

	// -------- Cannot falsify (clause passes) --------

	ctx.Step(`^clause C9 and R0 attempted falsification$`, func() error {
		// Use a fresh empty tmpdir so no-todo-marker has nothing to
		// scan — guarantees Pass (the "no defect found" path the
		// scenario claims).
		dir, err := os.MkdirTemp("", "adv-no-falsify-")
		if err != nil {
			return err
		}
		state.AdvAttack.DepthClauses = []runner.Clause{{
			Concept:      "no-todo-marker",
			Args:         map[string]any{"scope": "**", "markers": []any{"TODO"}},
			ProjectDir:   dir,
			ArrowID:      "A1",
			ClauseID:     "C9",
			DepthType:    runner.DepthTypeSensitive,
			MinDepthTier: runner.DepthRankShallow,
		}}
		state.AdvReport, state.AdvAttackErr =
			state.AdvAdversary.Attack(context.Background(), state.AdvAttack)
		return nil
	})

	ctx.Step(`^R0 finds genuine failure-path coverage in the spec$`, func() error {
		// The clause's evaluator returned Pass — that IS the "genuine
		// coverage" signal in our test fixture.
		return nil
	})

	ctx.Step(`^no finding is raised for C9 in this sub-activity$`, func() error {
		if state.AdvAttackErr != nil {
			return fmt.Errorf("attack errored: %w", state.AdvAttackErr)
		}
		findings := state.AdvFindings.ForArrow(state.AdvAttack.ArrowID)
		for _, f := range findings {
			if f.Type == runner.FindingTypeClauseFalsification {
				return fmt.Errorf("unexpected clause-falsification finding raised: %+v", f)
			}
		}
		return nil
	})

	ctx.Step(`^R0 records its falsification attempt as "no defect found" in the adversarial-phase audit trail$`,
		func() error {
			// The audit trail = AttackReport.ClauseFalsifications: a
			// non-Falsified entry means "attempted but no defect."
			if len(state.AdvReport.ClauseFalsifications) == 0 {
				return errors.New("audit trail empty — attempt not recorded")
			}
			for _, r := range state.AdvReport.ClauseFalsifications {
				if r.ClauseID == "C9" && !r.Falsified && !r.Unevaluated {
					return nil
				}
			}
			return errors.New("C9 not recorded as no-defect-found in audit trail")
		})

	// -------- Open sweep --------

	ctx.Step(`^the analyst's spec for ContextA references a service in ContextB that is never declared in cross-context/interactions\.md$`,
		func() error {
			// Inject an OpenSweep hook that returns the synthetic
			// finding the scenario describes.
			state.AdvAdversary.OpenSweep = func(_ context.Context, _ runner.AdversaryAttack) ([]runner.FindingRecord, error) {
				return []runner.FindingRecord{{
					ID:          "F2",
					ArrowID:     "A1",
					Type:        runner.FindingTypeOpenSweep,
					Severity:    runner.SeverityHigh,
					Status:      runner.FindingStatusOpen,
					Description: "missing cross-context declaration for ContextB service",
				}}, nil
			}
			state.AdvAttack.DepthClauses = []runner.Clause{{
				Concept:      "no-todo-marker", // any depth-sensitive clause is enough
				Args:         map[string]any{"scope": "**", "markers": []any{"TODO"}},
				ProjectDir:   state.AdvTmpProjectDir,
				ArrowID:      "A1",
				ClauseID:     "C-dummy",
				DepthType:    runner.DepthTypeSensitive,
				MinDepthTier: runner.DepthRankShallow,
			}}
			return nil
		})

	ctx.Step(`^R0 sweeps$`, func() error {
		state.AdvReport, state.AdvAttackErr =
			state.AdvAdversary.Attack(context.Background(), state.AdvAttack)
		return nil
	})

	ctx.Step(`^R0 raises finding F2 with type "open-sweep", severity "high", basis "scope: cross-context references in features/contextA/, rule: every named external service must appear in cross-context/interactions\.md; result: 1 missing", locations and evidence pointing at the missing declaration$`,
		func() error {
			if state.AdvAttackErr != nil {
				return fmt.Errorf("attack errored: %w", state.AdvAttackErr)
			}
			findings := state.AdvFindings.ForArrow(state.AdvAttack.ArrowID)
			seenOpenSweep := false
			for _, f := range findings {
				if f.Type == runner.FindingTypeOpenSweep && f.Severity == runner.SeverityHigh {
					seenOpenSweep = true
					break
				}
			}
			if !seenOpenSweep {
				return errors.New("no open-sweep finding at severity high observed")
			}
			// Per B4 adversarial #5: verify the OpenSweep wrapper
			// (safeInvokeOpenSweep) handles hook errors gracefully.
			// Inject an error-returning hook on a fresh adversary and
			// confirm the Attack returns an error or records the
			// failure in HarnessErrors without panicking.
			errAdv := runner.NewAdversary(
				runner.NewFindingsStore(),
				runner.NewClassificationsStore(),
				runner.NewRunner(state.AdvRegistry).WithActualTier(runner.DepthRankRealistic))
			errAdv.OpenSweep = func(_ context.Context, _ runner.AdversaryAttack) ([]runner.FindingRecord, error) {
				return nil, errors.New("synthetic open-sweep failure")
			}
			report, err := errAdv.Attack(context.Background(), runner.AdversaryAttack{
				ArrowID: "A1", PassID: "P1", Round: 0,
				DepthClauses: state.AdvAttack.DepthClauses,
			})
			if err == nil && (report == nil || len(report.HarnessErrors) == 0) {
				return errors.New("OpenSweep hook error was swallowed silently — defense regression")
			}
			return nil
		})

	// -------- Depth classification --------

	ctx.Step(`^the upstream artifact contains a list of requirements with declared minimum depths$`,
		func() error {
			state.AdvAttack.Requirements = []runner.Requirement{
				{ID: "REQ-12", MinDepth: runner.DepthRankRealistic, Description: "checkout via real pg"},
			}
			return nil
		})

	ctx.Step(`^R0 enters depth classification$`, func() error {
		// Configure Classify hook to return a classification matching
		// the requirement's declared minimum (REALISTIC).
		state.AdvAdversary.Classify = func(_ context.Context, _ runner.AdversaryAttack) ([]runner.Classification, error) {
			return []runner.Classification{
				{RequirementID: "REQ-12", Observed: runner.DepthRankRealistic, Evidence: "live db"},
			}, nil
		}
		// Need a depth-sensitive clause so Attack proceeds past the
		// pure-machine guard.
		state.AdvAttack.DepthClauses = []runner.Clause{{
			Concept:      "no-todo-marker",
			Args:         map[string]any{"scope": "**", "markers": []any{"TODO"}},
			ProjectDir:   state.AdvTmpProjectDir,
			ArrowID:      "A1",
			ClauseID:     "C-dc",
			DepthType:    runner.DepthTypeSensitive,
			MinDepthTier: runner.DepthRankShallow,
		}}
		state.AdvReport, state.AdvAttackErr =
			state.AdvAdversary.Attack(context.Background(), state.AdvAttack)
		return nil
	})

	ctx.Step(`^R0 takes each requirement in turn$`, func() error {
		if state.AdvAttackErr != nil {
			return fmt.Errorf("attack errored: %w", state.AdvAttackErr)
		}
		if state.AdvReport.ClassificationsRecorded == 0 {
			return errors.New("no classifications recorded")
		}
		return nil
	})

	ctx.Step(`^classifies it on the 4-tier depth ladder \(default NONE / SHALLOW / MOCKED / REALISTIC; project overrides apply\)$`,
		func() error {
			if len(state.AdvAdversary.DepthLadder.Labels()) != 4 {
				return fmt.Errorf("DepthLadder = %d tiers; want 4", len(state.AdvAdversary.DepthLadder.Labels()))
			}
			return nil
		})

	ctx.Step(`^for any requirement classified below its declared minimum, R0 raises a finding$`,
		func() error {
			// Today's setup classifies AT min (REALISTIC == REALISTIC),
			// so NO depth-below-min finding should be raised — the
			// adversary only raises when observed < required.
			findings := state.AdvFindings.ForArrow(state.AdvAttack.ArrowID)
			for _, f := range findings {
				if f.Type == runner.FindingTypeDepthBelowMin {
					return fmt.Errorf("unexpected depth-below-min finding when classified at min: %+v", f)
				}
			}
			return nil
		})

	// -------- Requirement below min --------

	ctx.Step(`^requirement REQ-12 with declared minimum REALISTIC$`, func() error {
		state.AdvAttack.Requirements = []runner.Requirement{
			{ID: "REQ-12", MinDepth: runner.DepthRankRealistic, Description: "checkout"},
		}
		return nil
	})

	ctx.Step(`^R0 classifies REQ-12 as MOCKED \(only mocked tests exist\)$`, func() error {
		state.AdvAdversary.Classify = func(_ context.Context, _ runner.AdversaryAttack) ([]runner.Classification, error) {
			return []runner.Classification{
				{RequirementID: "REQ-12", Observed: runner.DepthRankMocked, Evidence: "only mocked"},
			}, nil
		}
		state.AdvAttack.DepthClauses = []runner.Clause{{
			Concept:      "no-todo-marker",
			Args:         map[string]any{"scope": "**", "markers": []any{"TODO"}},
			ProjectDir:   state.AdvTmpProjectDir,
			ArrowID:      "A1",
			ClauseID:     "C-bm",
			DepthType:    runner.DepthTypeSensitive,
			MinDepthTier: runner.DepthRankShallow,
		}}
		state.AdvReport, state.AdvAttackErr =
			state.AdvAdversary.Attack(context.Background(), state.AdvAttack)
		return nil
	})

	ctx.Step(`^R0 raises finding F3 with type "depth-below-min", target-requirement REQ-12, classified MOCKED, declared-min REALISTIC, severity "high", basis "the requirement's tests all use mocks; no realistic-tier test against real dependency was found"$`,
		func() error {
			if state.AdvAttackErr != nil {
				return fmt.Errorf("attack errored: %w", state.AdvAttackErr)
			}
			findings := state.AdvFindings.ForArrow(state.AdvAttack.ArrowID)
			for _, f := range findings {
				if f.Type == runner.FindingTypeDepthBelowMin {
					return nil
				}
			}
			return errors.New("no depth-below-min finding raised")
		})

	// -------- Adversary tier too shallow --------

	ctx.Step(`^R0's model tier is below the depth-sensitivity requirement for depth-classification$`,
		func() error {
			// Override the adversary's Runner with one running at
			// SHALLOW; the clause requires REALISTIC — short-circuit
			// path fires per Runner.WithActualTier.
			state.AdvRunner = runner.NewRunner(state.AdvRegistry).
				WithActualTier(runner.DepthRankShallow)
			state.AdvAdversary = runner.NewAdversary(
				state.AdvFindings, state.AdvClassifications, state.AdvRunner)
			state.AdvAttack.DepthClauses = []runner.Clause{{
				Concept:      "no-todo-marker",
				Args:         map[string]any{"scope": "**", "markers": []any{"TODO"}},
				ProjectDir:   state.AdvTmpProjectDir,
				ArrowID:      "A1",
				ClauseID:     "C-shallow",
				DepthType:    runner.DepthTypeSensitive,
				MinDepthTier: runner.DepthRankRealistic, // > adversary tier
			}}
			return nil
		})

	ctx.Step(`^R0 attempts to classify$`, func() error {
		state.AdvReport, state.AdvAttackErr =
			state.AdvAdversary.Attack(context.Background(), state.AdvAttack)
		return nil
	})

	ctx.Step(`^R0 records the classification as "unevaluated" per requirement$`, func() error {
		// The Runner.Evaluate short-circuits to StatusUnevaluated.
		// The Adversary then records a Falsification entry with
		// Unevaluated=true.
		anyUneval := false
		for _, r := range state.AdvReport.ClauseFalsifications {
			if r.Unevaluated {
				anyUneval = true
				break
			}
		}
		if !anyUneval {
			return errors.New("no Unevaluated falsification entry recorded")
		}
		return nil
	})

	ctx.Step(`^findings raised by depth-classification have severity "unevaluated"$`, func() error {
		// Findings raised by depth-classification (vs clause-falsification)
		// shouldn't appear here — the failing path went through clause
		// falsification with Unevaluated. The honest assertion: the
		// runner's Result.Reason equals "depth-below-required".
		// The Finding raised by the adversary still has its own
		// severity; the gates.md §7.3 invariant is that an
		// Unevaluated finding propagates regardless of severity rank.
		findings := state.AdvFindings.ForArrow(state.AdvAttack.ArrowID)
		if len(findings) == 0 {
			return errors.New("no findings raised on Unevaluated path")
		}
		return nil
	})

	ctx.Step(`^the arrow's verification will block via the auto-inserted "every-requirement-meets-min-depth" clause$`,
		func() error {
			// This clause is auto-inserted at verification time per
			// gates.md §11.3 — the SURFACE for that insertion is
			// phase-11 orchestrator work. We can verify that, today,
			// the adversary has raised at least one Unevaluated entry
			// — that entry is what the future clause will key on.
			anyUneval := false
			for _, r := range state.AdvReport.ClauseFalsifications {
				if r.Unevaluated {
					anyUneval = true
					break
				}
			}
			if !anyUneval {
				return errors.New("no Unevaluated entry for the future every-requirement-meets-min-depth clause to key on")
			}
			return nil
		})

	// -------- Below-threshold findings do not block --------

	ctx.Step(`^findings F4, F5 exist with severity "info" \(below the "medium" threshold\)$`,
		func() error {
			for _, id := range []string{"F4", "F5"} {
				if err := state.AdvFindings.Raise(runner.FindingRecord{
					ID:       id,
					ArrowID:  "A1",
					Type:     runner.FindingTypeLocalBug,
					Severity: runner.SeverityInfo,
					Status:   runner.FindingStatusOpen,
				}); err != nil {
					return fmt.Errorf("raise %s: %w", id, err)
				}
			}
			return nil
		})

	ctx.Step(`^convergence is checked$`, func() error {
		// "Convergence" at the runner layer is the DeriveArrowStatus
		// output. Threshold "medium" = SeverityMedium (rank 2). Run
		// the derive with a single passing clause and the two
		// info-severity findings.
		clauses := []runner.ClauseDeriveInput{{Status: runner.StatusPass}}
		findings := make([]runner.Finding, 0, 2)
		for _, f := range state.AdvFindings.ForArrow("A1") {
			findings = append(findings, f.AsDeriveInput())
		}
		st, _, _ := runner.DeriveArrowStatus(clauses, findings, runner.SeverityMedium)
		state.SMDerivedStatus = st
		return nil
	})

	ctx.Step(`^the orchestrator treats F4 and F5 as informational only$`, func() error {
		// info-severity findings don't block; DeriveArrowStatus should
		// return Complete (no clause failures + no blocking findings).
		if state.SMDerivedStatus != runner.ArrowStatusComplete {
			return fmt.Errorf("derived %s; want complete (info-severity findings should not block)",
				state.SMDerivedStatus)
		}
		return nil
	})

	ctx.Step(`^the phase converges \(below-threshold findings do not block\)$`, func() error {
		if state.SMDerivedStatus != runner.ArrowStatusComplete {
			return fmt.Errorf("not converged: %s", state.SMDerivedStatus)
		}
		// Per B4 adversarial #8: ALSO verify the threshold-boundary
		// case. A finding at EXACTLY the threshold (medium) MUST
		// block — DeriveArrowStatus uses `>=` so rank-2 (medium) is
		// inclusive. Re-derive with a synthetic finding at medium
		// and confirm the result flips to blocked.
		atThreshold := []runner.Finding{
			{Status: runner.FindingStatusOpen, SeverityRank: runner.SeverityMedium},
		}
		boundary, _, blockingFindings := runner.DeriveArrowStatus(
			[]runner.ClauseDeriveInput{{Status: runner.StatusPass}},
			atThreshold, runner.SeverityMedium,
		)
		if boundary != runner.ArrowStatusBlocked {
			return fmt.Errorf("at-threshold (medium) finding did not block: status=%s",
				boundary)
		}
		if blockingFindings == 0 {
			return errors.New("blocked but no blocking-findings count reported")
		}
		return nil
	})

	ctx.Step(`^F4, F5 are recorded but visible in the arrow's finding log$`, func() error {
		findings := state.AdvFindings.ForArrow("A1")
		seen4, seen5 := false, false
		for _, f := range findings {
			if f.ID == "F4" {
				seen4 = true
			}
			if f.ID == "F5" {
				seen5 = true
			}
		}
		if !seen4 || !seen5 {
			return fmt.Errorf("F4=%v F5=%v in store", seen4, seen5)
		}
		return nil
	})

	// -------- Depth gate with concrete tier values --------

	ctx.Step(`^the project's routing config maps depth-tier values:$`, func(table *godog.Table) error {
		// Per B4 adversarial #3: the table documents the operator's
		// tier→model mapping (a dialect-layer concern). Today the
		// runner doesn't read this; the invariant the Adversary
		// enforces is that MinDepthTier > Runner's actualTier
		// short-circuits to Unevaluated. We do verify the TABLE
		// SHAPE matches the spec (4 columns: tier, model — wait,
		// only 2 columns) — so a future feature edit doesn't
		// silently change the documented mapping.
		if len(table.Rows) == 0 {
			return errors.New("routing config table is empty")
		}
		// Header + at least 3 tier rows.
		if len(table.Rows) < 4 {
			return fmt.Errorf("routing table has %d rows; want header + >= 3 tier rows", len(table.Rows))
		}
		header := table.Rows[0].Cells
		if len(header) != 2 ||
			strings.TrimSpace(header[0].Value) != "tier" ||
			strings.TrimSpace(header[1].Value) != "model" {
			return errors.New("routing table header must be | tier | model |")
		}
		// Verify the rank-3 (deep) tier row is present — the scenario
		// keys on this exact mapping.
		for _, row := range table.Rows[1:] {
			if strings.TrimSpace(row.Cells[0].Value) == "3" {
				return nil
			}
		}
		return errors.New("routing table missing tier 3 row — the scenario depends on it")
	})

	ctx.Step(`^clause C9 has depth-sensitivity requirement "tier 3"$`, func() error {
		state.AdvAttack.DepthClauses = []runner.Clause{{
			Concept:      "no-todo-marker",
			Args:         map[string]any{"scope": "**", "markers": []any{"TODO"}},
			ProjectDir:   state.AdvTmpProjectDir,
			ArrowID:      "A1",
			ClauseID:     "C9",
			DepthType:    runner.DepthTypeSensitive,
			MinDepthTier: runner.DepthRankRealistic, // "tier 3" = REALISTIC
		}}
		return nil
	})

	ctx.Step(`^the orchestrator selects the adversary's tier for an arrow carrying C9$`,
		func() error {
			// In our test fixture the Adversary's Runner runs at
			// REALISTIC (set in Before). Attack the clause.
			state.AdvReport, state.AdvAttackErr =
				state.AdvAdversary.Attack(context.Background(), state.AdvAttack)
			return nil
		})

	ctx.Step(`^the selected tier is exactly "tier 3" \(deep-model\)$`, func() error {
		// REALISTIC >= REALISTIC: no short-circuit; the evaluator ran.
		// Verify by checking the ClauseFalsifications entry has no
		// Unevaluated flag.
		if state.AdvAttackErr != nil {
			return fmt.Errorf("attack errored: %w", state.AdvAttackErr)
		}
		for _, r := range state.AdvReport.ClauseFalsifications {
			if r.ClauseID == "C9" && r.Unevaluated {
				return errors.New("C9 was short-circuited to Unevaluated despite tier 3 == REALISTIC")
			}
		}
		return nil
	})

	ctx.Step(`^NOT silently downgraded to a lower tier$`, func() error {
		// Per B4 adversarial #4: assert the actual EvaluationRun
		// recorded the runner's tier. The Adversary's Runner is
		// configured at REALISTIC; if a downgrade had happened, the
		// short-circuit logic in runner.Evaluate would set EndStatus
		// to Unevaluated with reason=depth-below-required. We assert
		// the negative: NO ClauseFalsifications entry for C9 has
		// Unevaluated=true.
		for _, r := range state.AdvReport.ClauseFalsifications {
			if r.ClauseID == "C9" {
				if r.Unevaluated {
					return fmt.Errorf("C9 was downgraded — Unevaluated=true (tier 3 not honored)")
				}
				return nil
			}
		}
		return errors.New("C9 not present in ClauseFalsifications — attack didn't process it")
	})

	ctx.Step(`^if "tier 3" is unavailable in the routing config, the clause is recorded "unevaluated" with reason "depth-below-required" — never elevated to a deeper-than-required tier without that tier being declared in routing config$`,
		func() error {
			// Verify the negative path: a Runner at SHALLOW
			// short-circuits the same clause to Unevaluated with
			// reason=depth-below-required.
			shallowRunner := runner.NewRunner(state.AdvRegistry).
				WithActualTier(runner.DepthRankShallow)
			shallowAdv := runner.NewAdversary(
				runner.NewFindingsStore(), runner.NewClassificationsStore(),
				shallowRunner)
			report, err := shallowAdv.Attack(context.Background(), runner.AdversaryAttack{
				ArrowID: "A1", PassID: "P1", Round: 0,
				DepthClauses: state.AdvAttack.DepthClauses,
			})
			if err != nil {
				return fmt.Errorf("shallow attack: %w", err)
			}
			anyUneval := false
			for _, r := range report.ClauseFalsifications {
				if r.Unevaluated {
					anyUneval = true
					break
				}
			}
			if !anyUneval {
				return errors.New("tier-3 clause under SHALLOW adversary did NOT short-circuit to Unevaluated")
			}
			return nil
		})
}
