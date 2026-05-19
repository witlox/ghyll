# Step-3 runner scenarios that the in-process implementation covers
# end-to-end:
#   - F-3: arrow status derivation per gates.md §7.2 precedence.
#   - Transition refusal when upstream arrow is not complete.
#
# The full design-intent runner.feature describes subprocess-process-
# lifecycle scenarios (timeout, OOM, malformed JSON, zombie children)
# whose step semantics describe a real subprocess harness. Those are
# unit-tested in runner/subprocess_test.go; BDD wiring for them lands
# once we have a scenario harness that actually spawns subprocesses
# during BDD runs.

Feature: Machine-clause runner — arrow status derivation + transition refusal

  # ---- F-3: arrow status derivation ----

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
