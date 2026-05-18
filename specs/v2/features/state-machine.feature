Feature: Status state machine engine

  # Maintains and queries the four lifecycles (clause, arrow, finding,
  # pass) and the project-level composite. Owns the per-clause transition
  # lock. Persists per-pass state to the checkpoint log on finalization.
  # See specs/direction/components/state-machine.md.

  # ---- Clause status transitions ----

  Scenario: Machine clause evaluated to pass
    Given a clause C1 with status "pending" on pass P1
    When the runner reports a successful evaluation
    Then the engine validates the transition pending → pass
    And records the new status with a timestamp

  Scenario: Invalid clause transition rejected
    Given a clause C1 with status "pass" on pass P1
    When the runner attempts to set status "pending"
    Then the engine rejects the transition with
        "illegal-transition: pass → pending not in clause state machine"
    And the clause status is unchanged

  Scenario: Attested clause awaits operator
    Given an attested clause with hint emitted
    Then the engine records status "awaiting-attestation"
    And exposes the clause in the attestation-flow query

  Scenario: Depth-below-required produces unevaluated
    Given a depth-sensitive clause routed below required tier
    When the runner reports the depth gate failed
    Then the engine records status "unevaluated" with reason
        "depth-below-required"

  # ---- Arrow status derivation ----

  Scenario: Arrow derivation order of precedence
    Given an arrow A1 on pass P1 with clauses
      | clause | status               |
      | C1     | pass                 |
      | C2     | fail                 |
      | C3     | unevaluated          |
      | C4     | awaiting-attestation |
    When the engine derives the arrow status
    Then the result is "blocked" (fail > unevaluated > provisional > complete)

  Scenario: Invalidation supersedes derived status
    Given arrow A1 with all clauses "pass" (would derive to "complete")
    When the grid amendment component sets A1 to "invalidated"
    Then querying A1's status returns "invalidated"
    And the underlying clause statuses are unchanged in the store

  Scenario: Re-traversal clears invalidated
    Given arrow A1 has status "invalidated" from grid vN
    When a new pass P2 starts on A1 against grid v(N+1)
    Then the engine creates fresh clause statuses for P2
    And A1's status becomes the derived status from P2's clauses
    And the prior "invalidated" state moves to history (checkpoint log)

  # ---- Finding lifecycle ----

  Scenario: Finding resolved by re-attack
    Given a finding F1 with status "open" and severity "medium"
    When the adversarial phase re-attacks after producer remediation
        and cannot reproduce F1
    Then the engine transitions F1 to "resolved"

  Scenario: Finding accepted as risk
    Given a finding F1 with status "open"
    When the producer proposes accepted-risk
    And the operator attests "accepted-risk"
    Then the engine transitions F1 to "accepted-risk"

  Scenario: Producer cannot accept own risk
    Given a finding F1 with status "open"
    When the producer attempts to set "accepted-risk" directly
    Then the engine rejects with "producer-cannot-accept-own-risk"
    And requires the verdict to come from the attestation flow
        component with operator op-id

  # ---- Pass lifecycle ----

  Scenario: Pass starts running
    Given the runner starts a new pass P1 on arrow A1
    When the engine records the pass
    Then P1 has status "running" with started-at set and completed-at unset

  Scenario: Pass completes normally
    Given pass P1 is "running" and the runner has finalized clause results
    When the runner signals completion
    Then the engine transitions P1 to "completed"
    And completed-at is recorded
    And the pass's full state is flushed to the checkpoint log

  Scenario: Pass aborted by invalidation
    Given pass P1 is "running" in remediation phase
    When the grid amendment component signals abort with reason "invalidated"
    Then the engine transitions P1 to "aborted" with that reason
    And findings from P1 are tagged with their original grid-version

  Scenario: Pass aborted by crash recovery
    Given pass P1 was "running" but the runner crashed
    When the engine performs crash recovery on restart
    Then the engine finds the orphaned pass and transitions it to
        "aborted" with reason "crash"

  # ---- Project-level status query ----

  Scenario: All declared cells complete
    Given a grid vN with every declared arrow at status "complete"
    And the residue list is empty
    And no arrow has status "unevaluated"
    When a project-status query is issued
    Then the result is "(complete-against-grid-vN, R=0, C=0)"

  Scenario: Some unevaluated cells
    Given a grid vN with 10 arrows: 9 "complete", 1 "unevaluated"
    When the project-status query runs
    Then the result includes "C=1"
    And the result lists the unevaluated arrow's id in detail

  Scenario: Residue computation
    Given a grid with 5 undeclared (stratum, context) cells
    And the harness computes each undeclared cell's imputed cost as
        the sum of per-clause default costs from the role's exit-gate
        template under the project's language bindings
    And the 5 cells compute to imputed costs [15, 15, 15, 15, 15]
    When the residue is computed
    Then R = 15+15+15+15+15 = 75
    And the project-status query reports R=75 alongside C and
        complete-against-grid-vN

  # ---- Snapshot and replay ----

  Scenario: Restart from checkpoint log
    Given the harness was running and is restarted
    When the engine initializes
    Then it reads the checkpoint log to reconstruct
      - all "running" passes (treated as "aborted" with reason "crash")
      - all "completed"/"aborted" passes (kept in log, not in in-memory store)
      - current grid version
      - current arrow statuses (per the latest completed pass per arrow)
    And the engine is ready to accept new pass starts

  Scenario: Query historical pass
    Given a query for pass P5 (completed and flushed)
    When the engine receives the query
    Then it reads from the checkpoint log
    And returns the historical pass's full state
