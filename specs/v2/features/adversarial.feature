# Implementation: built (phase 5 — runner/adversarial.go + runner/findings.go).
# Step impls land in Phase B4 of v2-final consolidation (see specs/v2-final-plan.md).
Feature: Per-arrow adversarial phase

  # Spawns a fresh adversary instance per round with clean context.
  # Runs three sub-activities (clause-falsification, open sweep, depth
  # classification) raising findings with severity. Bounded remediation
  # loop with full re-attack each round. Producer cannot attack itself.
  # See specs/direction/components/adversarial.md.

  # ---- Phase entry — initial attack ----

  Scenario: Arrow enters adversarial phase
    Given an arrow A1 with at least one declared depth-sensitive clause
    And the upstream role has emitted its arrow artifact
    When the runner signals that A1 is ready for adversarial phase
    Then the orchestrator spawns adversary instance R0 with clean context
    And R0 receives the upstream arrow artifact, the arrow's clause definitions, and the project's depth-ladder labels and per-requirement minimum depths
    And R0 runs the three sub-activities in order
    And the depth tier used by R0 meets the maximum depth-sensitivity requirement across the clauses

  Scenario: Pure machine arrow skips adversarial phase
    Given an arrow A2 with only machine / depth-robust clauses
    When the runner reaches A2
    Then the orchestrator does NOT spawn an adversary
    And the arrow proceeds directly to verification

  # ---- Clause-falsification sub-activity ----

  Scenario: Adversary attempts to falsify each depth-sensitive clause
    Given R0 has the arrow's clause set
    When R0 enters clause-falsification
    Then R0 takes each depth-sensitive clause in turn
    And attempts to construct a counterexample making that clause fail
    And each successful falsification raises a finding with severity assigned per the stated rule

  Scenario: Falsification produces a finding
    Given clause C9 = "the negative space is specified" on an analyst→architect arrow
    When R0 finds that ContextA's spec mentions only happy paths with no rejection rules
    Then R0 raises finding F1 with type "clause-falsification", target-clause C9, severity "high", basis stating the rule and result, locations pointing at the relevant files, and evidence
    And F1 is registered in the state machine with status "open"

  Scenario: Cannot falsify clause passes
    Given clause C9 and R0 attempted falsification
    When R0 finds genuine failure-path coverage in the spec
    Then no finding is raised for C9 in this sub-activity
    And R0 records its falsification attempt as "no defect found" in the adversarial-phase audit trail

  # ---- Open sweep sub-activity ----

  Scenario: Open sweep finds an un-clause-named defect
    Given the analyst's spec for ContextA references a service in ContextB that is never declared in cross-context/interactions.md
    When R0 sweeps
    Then R0 raises finding F2 with type "open-sweep", severity "high", basis "scope: cross-context references in features/contextA/, rule: every named external service must appear in cross-context/interactions.md; result: 1 missing", locations and evidence pointing at the missing declaration

  # ---- Depth classification sub-activity ----

  Scenario: Walk requirements on the depth ladder
    Given the upstream artifact contains a list of requirements with declared minimum depths
    When R0 enters depth classification
    Then R0 takes each requirement in turn
    And classifies it on the 4-tier depth ladder (default NONE / SHALLOW / MOCKED / REALISTIC; project overrides apply)
    And for any requirement classified below its declared minimum, R0 raises a finding

  Scenario: Requirement below min
    # B4 of v2-final consolidation: the runner currently raises every
    # depth-below-min finding at severity HIGH (conservative default,
    # runner/adversarial.go ~line 389). Per-rule severity assignment
    # is phase-11 surface; until then the BDD asserts actual behavior.
    Given requirement REQ-12 with declared minimum REALISTIC
    And R0 classifies REQ-12 as MOCKED (only mocked tests exist)
    Then R0 raises finding F3 with type "depth-below-min", target-requirement REQ-12, classified MOCKED, declared-min REALISTIC, severity "high", basis "the requirement's tests all use mocks; no realistic-tier test against real dependency was found"

  Scenario: Adversary tier too shallow to classify
    Given R0's model tier is below the depth-sensitivity requirement for depth-classification
    When R0 attempts to classify
    Then R0 records the classification as "unevaluated" per requirement
    And findings raised by depth-classification have severity "unevaluated"
    And the arrow's verification will block via the auto-inserted "every-requirement-meets-min-depth" clause

  # ---- Remediation loop ----

  @phase11
  Scenario: Producer fixes a finding with full re-attack
    Given finding F1 status "open" raised by adversary round R0
    When the producer addresses F1 by editing the upstream artifact
    Then the producer signals the orchestrator via a typed "producer-fix-signal" message containing pass-id and addressed-findings
    And the orchestrator spawns a fresh adversary R1 with NO shared session/context from R0 (verified: R1's input list contains only the upstream artifact, clause definitions, depth ladder, routing config — nothing from R0's stdout/state)
    And R1's model tier equals R0's model tier (same depth budget)
    And R1 visibly invokes all three sub-activities (clause-falsification, open-sweep, depth-classification) — each sub-activity emits a phase-entered marker in the audit-trail so a skipped sub-activity is detectable
    And R1 attacks the ENTIRE upstream artifact (NOT scoped to F1's target) per D32
    And if R1 cannot reproduce F1, F1 transitions to "resolved"
    And if R1 reproduces F1, F1 stays "open" and another round begins
    And any new findings R1 raises are added to the open set

  @phase11
  Scenario: Producer proposes accepted-risk
    Given finding F1 status "open"
    When the producer proposes accepted-risk via a typed "accepted-risk-proposal" message containing pass-id, finding-id, rationale, and inspected-context
    Then the orchestrator hands F1 to the attestation flow component
    And the operator attests accepted-risk OR rejects
    And on accepted-risk: F1 transitions to "accepted-risk"
    And on rejected: F1 stays "open" and remediation continues

  @phase11
  Scenario: Multiple findings in flight
    Given findings F1, F2, F3 all "open" after R0
    When the producer fixes F1 and F2 but not F3
    Then the next round R1 re-attacks all three
    And F1, F2 transition to "resolved" if not reproduced
    And F3 stays "open"
    And remediation continues for F3

  @phase11
  Scenario: Non-convergence escalates after remediation-rounds-max
    Given finding F1 has been re-attacked through remediation-rounds-max rounds (default 5) and remains "open"
    When the final round completes
    Then the orchestrator stops the remediation loop
    And escalates to the operator with kind "remediation-non-convergence"
    And the operator must decide: accepted-risk OR route the artifact for deeper rework upstream

  # ---- Phase exit to verification ----

  @phase11
  Scenario: Convergence — all findings disposed
    Given all findings above the severity threshold are "resolved" or "accepted-risk"
    When the remediation loop converges
    Then the orchestrator signals the runner to begin the verification phase
    And the runner auto-inserts "no-open-finding" and "every-requirement-meets-min-depth"
    And the arrow proceeds to verification

  Scenario: Findings below threshold do not block
    Given findings F4, F5 exist with severity "info" (below the "medium" threshold)
    When convergence is checked
    Then the orchestrator treats F4 and F5 as informational only
    And the phase converges (below-threshold findings do not block)
    And F4, F5 are recorded but visible in the arrow's finding log

  # ---- Adversarial additions: remediation-rounds-max boundary ----

  @phase11
  Scenario Outline: Remediation-rounds-max boundary
    Given remediation-rounds-max is configured to "<max>"
    And finding F1 remains "open" through <attempted> remediation rounds
    Then escalation is "<escalation>"

    Examples:
      | max | attempted | escalation                |
      | 5   | 4         | not yet (loop continues)   |
      | 5   | 5         | yes (loop stops, operator) |
      | 5   | 6         | impossible: loop stopped at 5 |
      | 1   | 1         | yes (operator immediately) |
      | 0   | 0         | rejected at init: max=0 invalid |

  @phase11
  Scenario: Producer signals fix but artifact is unchanged (loop bomb)
    Given finding F1 status "open" after round R0
    When the producer emits "producer-fix-signal" but the upstream artifact's content-hash is identical to the version R0 saw
    Then the orchestrator detects the no-op (compares pre/post hashes)
    And refuses to spawn R1 against the unchanged artifact
    And emits an OperatorEvent: "producer-signal-without-change" for the pass-id and the producer role-id
    And the round counter does NOT advance (this is not a legitimate round; loop bomb prevented)

  # ---- Adversarial additions: concrete depth-tier handling ----

  Scenario: Depth gate with concrete tier values
    Given the project's routing config maps depth-tier values:
      | tier | model        |
      | 1    | fast-model   |
      | 2    | medium-model |
      | 3    | deep-model   |
    And clause C9 has depth-sensitivity requirement "tier 3"
    When the orchestrator selects the adversary's tier for an arrow carrying C9
    Then the selected tier is exactly "tier 3" (deep-model)
    And NOT silently downgraded to a lower tier
    And if "tier 3" is unavailable in the routing config, the clause is recorded "unevaluated" with reason "depth-below-required" — never elevated to a deeper-than-required tier without that tier being declared in routing config
