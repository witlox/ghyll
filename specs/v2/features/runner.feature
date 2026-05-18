Feature: Machine-clause runner (enforcement spine)

  # Invokes evaluators, coordinates with attestation flow for attested
  # clauses, derives arrow status per gates.md §7.2 precedence, refuses
  # transitions when arrow status is not "complete". Owns the per-(role,
  # context) lock; observes the state-machine engine's per-clause lock.
  # See specs/direction/components/runner.md.

  # ---- Single machine clause evaluation ----

  Scenario: Successful machine evaluation
    Given a clause "no-todo-marker(scope='src/**')" on arrow A1
    And the upstream artifact contains no TODO markers in scope
    When the runner evaluates the clause as part of pass P1
    Then the evaluator runs to completion
    And the clause status transitions: pending → running → pass
    And an evaluation-run record is appended to the pass log with
        evaluation-run-id, clause-id, pass-id, started-at,
        completed-at, and result {pass: true, details: {hits: []}}

  Scenario: Machine evaluation fails
    Given a clause "no-todo-marker(scope='src/**')" on arrow A1
    And the artifact contains "TODO: implement retries" at src/foo.go:42
    When the runner evaluates
    Then the clause status becomes "fail"
    And the result records the hit location
    And the arrow's derived status becomes "blocked"

  Scenario: Machine evaluator returns unevaluated due to depth
    Given a clause "mutation-score(...)" with depth-sensitive depth-type
    And the active model tier is below the clause's declared minimum
    When the runner attempts to invoke the evaluator
    Then the runner records the clause status "unevaluated" with
        reason "depth-below-required"
    And the evaluator is NOT invoked (depth gate is checked first)

  # ---- Attested clause coordination ----

  Scenario: Attested clause requires hint emission
    Given a clause "attested-G7" on arrow A1 with producer role "analyst"
    When the runner reaches this clause during pass P1
    Then the runner requests the producer role to emit a hint
    And the producer role returns a hint {clause, locations, basis, residue}
    And the runner forwards the hint to the attestation flow component
    And the clause status transitions: pending → awaiting-attestation

  Scenario: Operator returns attestation verdict
    Given a clause with status "awaiting-attestation"
    When the attestation flow component records an operator verdict
        (pass / fail / insufficient-basis)
    Then the runner updates the clause status to match the verdict
    And the arrow's derived status is recomputed

  Scenario: Producer cannot emit hint
    Given a clause where the producer reports "unable-to-hint"
    Then the runner records the clause "unevaluated" with reason
        "no-rule-selectable-locations"
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
    Then the runner refuses the transition with a structured error
        containing kind "transition-refused", upstream-arrow id,
        upstream-status, blocking-clauses
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
    And signals that A needs re-traversal first

  # ---- Verification phase orchestration ----

  Scenario: Adversarial phase ran verification auto-inserts
    Given an arrow that ran an adversarial phase
    When the runner enters the verification phase
    Then the runner auto-inserts the "no-open-finding" clause
    And auto-inserts the "every-requirement-meets-min-depth" clause
    And evaluates them alongside the arrow's declared verification clauses
    And these auto-inserted clauses CANNOT be skipped or weakened by
        the arrow definition

  Scenario: Pure machine arrow skips adversarial and verification only
    Given an arrow with only machine, depth-robust clauses
    Then the runner skips adversarial and remediation phases
    And runs verification only (just evaluates the declared machine clauses)

  # ---- Concurrent execution coordination ----

  Scenario: Two passes on different contexts run concurrently
    Given pass P1 on (analyst, contextA, stratum-1)
    And pass P2 on (analyst, contextB, stratum-1)
    When both are scheduled
    Then the runner permits both to run concurrently
    And the single-active-role-instance(analyst, contextA) constraint
        is satisfied for each
    And neither's evaluation runs interfere with the other's

  Scenario: Two passes on same (role, context) are refused
    Given pass P1 on (analyst, contextA) is running
    When a second pass P2 on (analyst, contextA) is requested
    Then the runner refuses P2 with kind "single-active-role-violation"
    And P2 is not started until P1 completes or aborts

  Scenario: Pass aborted due to grid amendment
    Given pass P1 is in remediation phase
    And a grid amendment lands invalidating P1's arrow
    When the grid amendment component signals abort
    Then the runner halts P1's evaluation runs in flight
    And records pass-status "aborted" with reason "invalidated"
    And preserves findings discovered before abort, tagged with their
        grid-version

  # ---- Per-pass checkpoint emission ----

  Scenario: Pass completes and emits checkpoint
    Given pass P1 has reached terminal arrow status
    When the runner finalizes the pass
    Then the runner emits a checkpoint with pass-id, arrow-id,
        grid-version, clause-by-clause status, finding ids raised,
        pass-status "completed", and timestamps
    And the checkpoint is appended to the project's checkpoint log

  Scenario: Pass aborted records reason in checkpoint
    Given pass P1 was aborted mid-phase
    When the runner finalizes the aborted pass
    Then the checkpoint records pass-status "aborted" with the abort reason
    And the partial evaluation results are persisted for forensic value
