Feature: Operator attestation flow

  # Coordinates operator verdicts on attested clauses. Owns the operator
  # event bus. Captures verdicts, emits typed JSONL records, handles
  # insufficient-basis-rounds-max escalation, supports multi-operator
  # handoff within a single pass.
  # See specs/direction/components/attestation.md.

  # ---- Session lifecycle ----

  Scenario: Session start with op-id
    Given the harness has no active operator session
    When the operator declares op-id "alice@example.com"
    Then the component creates a session bound to that op-id
    And the session is active for subsequent verdicts
    And verdict-capture API calls without an active session are
        refused with "no-active-session"

  Scenario: Multi-operator handoff in one pass
    Given operator Alice is active and attests clause C1
    When Alice ends her session
    And operator Bob declares op-id "bob@example.com" and starts
    Then Bob is now active
    And Bob may attest clauses C2, C3 within the same pass
    And the attestation file for the pass records Alice's verdict on
        C1 with op-id alice and Bob's verdicts on C2, C3 with op-id bob

  Scenario: Empty op-id is refused
    Given operator attempts to declare op-id ""
    Then session start is refused with "op-id-required"

  # ---- Hint presentation and verdict capture ----

  Scenario: Operator returns pass
    Given an attestation request for clause C5 with hint
        { locations: [features/contextA/payment.feature:42-67],
          basis: "all failure-path scenarios in this region",
          residue: "happy-path tests not scanned" }
    And operator Alice is active
    When Alice inspects the locations and submits verdict "pass" with
        unit "confirm"
    Then a record is appended to the per-pass attestation file at
        "attestations/v<N>/contextA/stratum-<S>/<role-pair>/<pass-id>.jsonl"
        where <role-pair> uses "__" as the separator (e.g.,
        analyst__architect or analyst__adversary__architect or init__analyst)
    And the record carries unit "confirm", clause C5, verdict "pass",
        ts, op-id "alice@example.com"
    And the component signals the state-machine engine to transition
        C5 to "pass"

  Scenario: Operator returns fail with record-locations
    Given an attestation request for clause C5
    When Alice submits verdict "fail" with unit
        "record-locations-inspected" and inspected list
        [features/contextA/payment.feature:42-50]
    Then a record is appended with the inspected list
    And C5's status becomes "fail"
    And the producer is notified of the failure to remediate

  Scenario: Operator returns insufficient-basis with residue note
    Given an attestation request for clause C5
    When Alice submits verdict "insufficient-basis" with unit
        "write-residue-note" and residue-note "feature file is too
        large to manually inspect; need a deeper artifact"
    Then a record is appended with the residue note
    And the attestation flow signals state-machine engine to
        transition C5 to "insufficient-basis"
    And the engine derives the arrow's status (attestation flow does
        NOT directly set arrow status; arrow status is always derived)
    And the round counter for C5 increments to 1

  # ---- Insufficient-basis escalation (insufficient-basis-rounds-max) ----

  Scenario: Three rounds, then escalation
    Given clause C5 has received "insufficient-basis" from rounds 1 and 2
    And the producer has re-emitted the hint at a deeper depth tier each round
    When round 3 also returns "insufficient-basis"
    Then the component records the escalation
    And presents the operator with two options:
      1. attest "accepted-risk" with "write-residue-note" recording
         why the basis remains insufficient
      2. route the artifact back upstream for deeper rework with
         rationale "requires-deeper-artifact"
    And neither option is the default — operator must choose

  Scenario: Operator accepts risk on the third round
    Given the escalation prompt
    When operator chooses option 1 with residue note
    Then a record is appended with unit "write-residue-note", verdict
        "accepted-risk", op-id, inspected list, and residue-note
    And the FINDING associated with C5 transitions to status "accepted-risk"
    And C5's CLAUSE-status transitions to "pass" once all findings on
        the clause are disposed (resolved or accepted-risk)
    And the round counter resets

  Scenario: Operator routes upstream
    Given the escalation prompt
    When operator chooses option 2 with rationale
    Then the component signals the runner that C5's upstream artifact
        requires deeper rework
    And the arrow's pass is aborted with reason "requires-deeper-artifact"
    And the producer role is re-routed at a deeper tier to produce a
        richer artifact

  Scenario: insufficient-basis-rounds-max is configurable
    Given init declared insufficient-basis-rounds-max=5 for this project
    When clause C5 receives "insufficient-basis" for the 4th time
    Then no escalation is triggered yet (max not reached)
    And the round counter is 4

  # ---- Accepted-risk for findings ----

  Scenario: Producer proposes accepted-risk
    Given finding F1 with status "open"
    And the producer proposes accepted-risk for F1
    When the adversarial component hands F1 to attestation flow
    Then this component presents F1's evidence and the producer's
        rationale to the active operator
    And captures the operator's verdict

  Scenario: Operator attests accepted-risk on F1
    Given the operator inspects F1's evidence
    When operator submits "accepted-risk" verdict
    Then a record is appended (unit per severity)
    And F1's status becomes "accepted-risk"

  Scenario: Operator rejects accepted-risk proposal
    Given the operator finds the producer's proposal weak
    When operator submits "fail" on the accepted-risk request
    Then F1 stays "open"
    And the producer must continue remediation

  # ---- Verifier-driven verdict replay ----

  Scenario: Verifier reads attestation log
    Given a pass-id and a clause-id
    When the verifier component reads the attestation file
    Then it finds all records for that clause in chronological order
    And can reconstruct the operator's decision chain
    And can verify that the required fields per unit are present

  Scenario: Missing required field is detected
    Given an attestation record with unit "record-locations-inspected"
        but no "inspected" array
    When the verifier reads it
    Then the record is flagged as malformed
    And the operator session that produced it is alerted
