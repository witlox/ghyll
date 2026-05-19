# Implementation: built (phase 3 — runner/runner.go Runner.Evaluate +
# runner/registry.go + runner/subprocess.go BindingEvaluator).
# F-3 arrow-status derivation step impls are already THOROUGH;
# subprocess/evaluator coordination scenarios lift in Phase B6 of
# v2-final consolidation.
Feature: Machine-clause runner (enforcement spine)

  # Invokes evaluators, coordinates with attestation flow for attested
  # clauses, derives arrow status per gates.md §7.2 precedence, refuses
  # transitions when arrow status is not "complete". Owns the per-(role,
  # context) lock; observes the state-machine engine's per-clause lock.
  # See specs/direction/components/runner.md.

  # ---- Single machine clause evaluation ----

  @phase11
  Scenario: Successful machine evaluation
    Given a clause "no-todo-marker(scope='src/**')" on arrow A1
    And the upstream artifact contains src/foo.go with no TODO markers
    And the upstream artifact contains src/bar.go with no TODO markers
    When the runner evaluates the clause as part of pass P1
    Then the evaluator process is spawned with a binding-resolved command
    And the evaluator's stdin/stdout/stderr are captured for inspection
    And the evaluator reads the resolved scope (recording which files were read)
    And the evaluator runs to completion with exit-code 0
    And the clause status transitions: pending → running → pass
    And the result.details.scanned-files is non-empty (proving real scan)
    And an evaluation-run record is appended with evaluation-run-id, clause-id, pass-id, started-at, completed-at, result, and the list of files actually scanned (so a stub returning empty hits without scanning is detectable)

  @phase11
  Scenario: Machine evaluation fails
    Given a clause "no-todo-marker(scope='src/**')" on arrow A1
    And the artifact contains "TODO: implement retries" at src/foo.go:42
    When the runner evaluates
    Then the clause status becomes "fail"
    And the result records the hit location
    And the arrow's derived status becomes "blocked"

  @phase11
  Scenario: Machine evaluator returns unevaluated due to depth
    # This depth-gate short-circuit is wired in state-machine.feature
    # ("Depth-below-required produces unevaluated") which covers the
    # same runner.WithActualTier vs Clause.MinDepthTier contract. The
    # @phase11 here marks the SPECIFIC mutation-score-concept fixture
    # as a phase-11 wiring (runner.feature's flavour differs in clause
    # naming but tests the same invariant).
    Given a clause "mutation-score(...)" with depth-sensitive depth-type
    And the active model tier is below the clause's declared minimum
    When the runner attempts to invoke the evaluator
    Then the runner records the clause status "unevaluated" with reason "depth-below-required"
    And the evaluator is NOT invoked (depth gate is checked first)

  # ---- Attested clause coordination ----

  @phase11
  Scenario: Attested clause requires hint emission
    Given a clause "attested-G7" on arrow A1 with producer role "analyst"
    When the runner reaches this clause during pass P1
    Then the runner requests the producer role to emit a hint
    And the producer role returns a hint {clause, locations, basis, residue}
    And the runner forwards the hint to the attestation flow component
    And the clause status transitions: pending → awaiting-attestation

  @phase11
  Scenario: Operator returns attestation verdict
    Given a clause with status "awaiting-attestation"
    When the attestation flow component records an operator verdict (pass / fail / insufficient-basis)
    Then the runner updates the clause status to match the verdict
    And the arrow's derived status is recomputed

  @phase11
  Scenario: Producer cannot emit hint
    Given a clause where the producer reports "unable-to-hint"
    Then the runner records the clause "unevaluated" with reason "no-rule-selectable-locations"
    And the producer raises a finding of type "unable-to-hint" against itself

  # ---- Arrow status derivation ----

  Scenario: All clauses pass
    Given an arrow with 5 clauses, all status "pass", no findings
    Then the derived arrow status is "complete"

  Scenario: One clause failed
    Given an arrow with 5 clauses, 4 "pass" and 1 "fail"
    Then the derived arrow status is "blocked"

  Scenario: One clause unevaluated with no fails
    Given an arrow with 5 clauses, 4 "pass" and 1 "unevaluated"
    Then the derived arrow status is "unevaluated"
    And the arrow does NOT satisfy the next role's input

  Scenario: Fail and unevaluated coexist
    Given an arrow with 5 clauses: 3 "pass", 1 "fail", 1 "unevaluated"
    Then the derived arrow status is "blocked"

  Scenario: Awaiting attestation produces provisional
    Given an arrow with 5 clauses: 3 machine "pass", 2 attested "awaiting-attestation"
    Then the derived arrow status is "provisional"

  Scenario: Unevaluated trumps provisional
    Given an arrow with 5 clauses: 3 "pass", 1 "unevaluated", 1 "awaiting-attestation"
    Then the derived arrow status is "unevaluated"

  # ---- Transition refusal ----

  Scenario: Downstream attempts to start before upstream complete
    Given arrow A (upstream) has derived status "provisional"
    And arrow B (downstream) requires A as input
    When a role attempts to enter the role downstream of arrow B
    Then the runner refuses the transition with a structured error containing kind "transition-refused", upstream-arrow id, upstream-status, blocking-clauses
    And no pass on arrow B is started

  Scenario: Transition allowed when upstream complete
    Given arrow A has derived status "complete"
    When a role attempts the downstream transition
    Then the runner permits the transition
    And starts a new pass on arrow B

  Scenario: Invalidated arrow refuses transitions
    Given arrow A has status "invalidated"
    When a downstream transition is attempted
    Then the runner refuses with kind "transition-refused-invalidated"
    And the structured error contains A's arrow-id and its invalidating grid-version
    And an OperatorEvent of type "pass-aborted" or "transition-refused-invalidated" is published on the operator event bus (observable to the UI / tooling layer, not just returned as a function error)

  # ---- Verification phase orchestration ----

  @phase11
  Scenario: Adversarial phase ran verification auto-inserts
    Given an arrow that ran an adversarial phase
    When the runner enters the verification phase
    Then the runner auto-inserts the "no-open-finding" clause
    And auto-inserts the "every-requirement-meets-min-depth" clause
    And evaluates them alongside the arrow's declared verification clauses
    And these auto-inserted clauses CANNOT be skipped or weakened by the arrow definition

  @phase11
  Scenario: Pure machine arrow skips adversarial and verification only
    Given an arrow with only machine, depth-robust clauses
    Then the runner skips adversarial and remediation phases
    And runs verification only (just evaluates the declared machine clauses)

  # ---- Concurrent execution coordination ----

  @phase11
  Scenario: Two passes on different contexts run concurrently
    Given pass P1 on (analyst, contextA, stratum-1)
    And pass P2 on (analyst, contextB, stratum-1)
    And both pass P1 and pass P2 evaluate the same clause-concept (e.g., `no-orphan-symbol`) at the same wall-clock instant
    When both are scheduled
    Then the runner permits both to run concurrently
    And both evaluators' start timestamps overlap (proving parallelism, not serialization)
    And the per-(role, context) lock for (analyst, contextA) is held by P1 only; the lock for (analyst, contextB) is held by P2 only
    And the state-machine per-clause locks for P1 and P2 are distinct (different pass-ids → different lock keys)
    And running with `-race` reports no data races on the shared evaluator-output buffer

  @phase11
  Scenario: Two passes on same (role, context) are refused
    Given pass P1 on (analyst, contextA) is running
    When a second pass P2 on (analyst, contextA) is requested
    Then the runner refuses P2 with kind "single-active-role-violation"
    And P2 is not started until P1 completes or aborts

  @phase11
  Scenario: Pass aborted due to grid amendment
    Given pass P1 is in remediation phase
    And a grid amendment lands invalidating P1's arrow
    When the grid amendment component signals abort
    Then the runner halts P1's evaluation runs in flight
    And records pass-status "aborted" with reason "invalidated"
    And preserves findings discovered before abort, tagged with their grid-version

  # ---- Per-pass checkpoint emission ----

  @phase11
  Scenario: Pass completes and emits checkpoint
    Given pass P1 has reached terminal arrow status
    When the runner finalizes the pass
    Then the runner emits a checkpoint with pass-id, arrow-id, grid-version, clause-by-clause status, finding ids raised, pass-status "completed", and timestamps
    And the checkpoint is appended to the project's checkpoint log

  @phase11
  Scenario: Pass aborted records reason in checkpoint
    Given pass P1 was aborted mid-phase
    When the runner finalizes the aborted pass
    Then the checkpoint records pass-status "aborted" with the abort reason
    And the partial evaluation results are persisted for forensic value

  # ---- Adversarial additions: evaluator process failures ----

  @requires-posix
  Scenario: Evaluator times out
    Given a clause with timeout-per-mutation 30s
    And the evaluator runs past 30s without producing output
    When the runner detects the timeout
    Then the runner sends SIGTERM to the evaluator process
    And after 5s grace, SIGKILL if still running
    And the clause status is "unevaluated" with reason "evaluator-timeout"
    And no orphan / zombie evaluator process remains
    And the timeout duration is recorded in the evaluation-run for operator triage

  @requires-posix
  Scenario: Evaluator killed by OOM
    Given the evaluator process is terminated by the OS OOM-killer (exit signal 9, no graceful stop)
    When the runner observes the abnormal termination
    Then the clause status is "fail" with details.error "evaluator-killed-by-signal" (NOT recorded as pass)
    And a clear distinction is made from "evaluator-timeout"

  @requires-posix
  Scenario: Evaluator returns malformed JSON
    Given the evaluator exits 0 but stdout is not valid JSON (truncated, binary, plain text, partial)
    When the runner parses the output
    Then parsing fails with a clear error
    And the clause status is "fail" with details.error "evaluator-output-malformed"
    And the raw output is preserved in the evaluation-run record for forensic inspection (truncated to ≤ 16KB)

  @requires-posix
  Scenario: Evaluator writes spurious stderr but exits 0
    Given the evaluator writes warning lines to stderr but exits 0 with valid JSON on stdout
    When the runner reads stdout for the result
    Then the result is parsed normally
    And the stderr content is captured in the evaluation-run record as metadata (not as failure signal)

  @requires-posix
  Scenario: Evaluator returns oversized output
    Given the evaluator produces stdout exceeding 100MB
    When the runner reads the output
    Then the runner enforces a max-output-bytes limit (configurable; default 16MB)
    And exceeding the limit fails the evaluation with details.error "evaluator-output-oversized"
    And the evaluator process is killed once the limit trips

  @requires-posix
  Scenario: Evaluator leaves zombie children
    Given the evaluator spawns subprocesses and doesn't wait on them
    When the evaluator main process exits
    Then the runner reaps any remaining children belonging to the evaluator's process group within 5s
    And no zombie processes accumulate across passes
