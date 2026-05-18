# Component: adversarial phase orchestrator

The adversarial phase is **the first phase of every depth-sensitive
arrow** (`gates.md` §11). It attacks the upstream artifact from a
fresh model instance with clean context, running three sub-activities
that raise findings. The phase replaces what a standalone adversary
role would do; it is per-arrow, not per-role.

This component orchestrates the phase: spawning the adversary,
running the three sub-activities in order, raising findings into the
state machine, coordinating the remediation loop, and handing off to
verification.

> Status: design intent.

---

## Scope

**In scope.** Spawning the synthetic `adversary` role-id instance
(`gates.md` §1.1) with clean context. Running three sub-activities:
clause-falsification, open sweep, depth classification. Raising
findings with severity. The remediation loop: receiving producer
fixes / accepted-risk proposals, re-attacking with a fresh adversary,
deciding `resolved` or `still-open`. Handing the arrow to verification
once findings converge.

**Out of scope.** The verification phase (runner). The producer's
fix work (the producer role, not this component). The operator's
attestation of `accepted-risk` (attestation flow).

---

## Domain model

| Term | Definition |
|---|---|
| **Adversary instance** | A fresh model invocation with clean context, labeled with the synthetic `adversary` role-id. Has no memory of the producer's reasoning or prior context. One adversary per attack round. |
| **Clean context** | The adversary receives ONLY the upstream artifact + the arrow's clause definitions + the depth-ladder. It does NOT receive the producer's chain-of-thought, prior remediation rounds' findings, or any session-state from the upstream role. |
| **Attack round** | One adversarial pass. Either the initial attack (round 0) or a re-attack after remediation (round N+1). Each round spawns a fresh adversary. |
| **Clause-falsification** | First sub-activity. For each declared `depth-sensitive` clause on the arrow, try to construct a counterexample making the clause fail. Raises findings on success. |
| **Open sweep** | Second sub-activity. Scan the artifact for defects that no declared clause names. Raises findings of any severity. |
| **Depth classification** | Third sub-activity. Walk each requirement in the upstream artifact and classify on the depth ladder (`gates.md` §11.1). Raise a finding for any requirement below its declared minimum. |
| **Remediation round** | The bounded loop between adversary findings and producer fixes. After each producer fix, a new adversary instance re-attacks. |
| **Convergence** | The remediation loop reaches a state where all findings above the severity threshold are `resolved` or `accepted-risk`. At convergence, the phase signals verification. |
| **Non-convergence** | After N rounds (default 5; init may override), findings still remain `open`. The phase escalates rather than spinning indefinitely. |

---

## Invariants

1. **Fresh adversary per round.** Each attack round spawns a new
   model instance. There is no persistent adversary memory across
   rounds. A finding may be re-raised in round N+1 with different
   evidence; it does not carry over from round N.
2. **Clean context.** The adversary's input is bounded to: the
   upstream artifact, the arrow's clause definitions, the depth
   ladder, and the project's depth-type config (model tier
   routing). Nothing else.
3. **Three sub-activities, in order.** Clause-falsification first
   (it's bounded and structured), then open sweep, then depth
   classification. Each sub-activity raises findings as it runs;
   the next sub-activity sees those findings only as context for
   "what's already known," never as a constraint on its own work.
4. **Severity assigned per finding.** Every finding has a severity
   on the enum `info / low / medium / high / critical` (`gates.md`
   §7.3). Severity assignment is `depth-sensitive`; if the
   adversary's tier is below the depth-sensitivity requirement, the
   severity is `unevaluated` and the finding's status reflects that.
5. **Producer cannot attack itself.** The adversary identity is
   structurally distinct from the producer; the producer role
   cannot serve as its own adversary. Enforced by the synthetic
   role-id mechanism.
6. **Bounded remediation.** No more than N rounds. After N, escalate
   to operator. Default N=5; init may override.
7. **No silent dismissal.** A finding can only transition to
   `resolved` if a fresh adversary's re-attack confirms the defect
   is no longer reproducible. Producer reporting "fixed" is not
   sufficient.
8. **Re-attack reads producer's fix output.** When re-attacking,
   the new adversary instance reads the *current* state of the
   upstream artifact (after fix), not the prior state. Otherwise
   re-attacks would always re-raise the same findings.

---

## Behaviors (features)

### F-1: Phase entry — initial attack

```gherkin
Feature: Initial adversarial attack on an arrow

  Scenario: Arrow enters adversarial phase
    Given an arrow A1 with at least one declared `depth-sensitive`
        clause
    And the upstream role has emitted its arrow artifact
    When the runner signals that A1 is ready for adversarial
        phase
    Then the orchestrator spawns adversary instance R0 with
        clean context
    And R0 receives:
      - the upstream arrow artifact (e.g., the analyst's specs)
      - the arrow's clause definitions
      - the project's depth-ladder labels and per-requirement
        minimum depths
    And R0 runs the three sub-activities in order
    And the depth tier used by R0 meets the maximum depth-sensitivity
        requirement across the clauses (per `gates.md` §8)

  Scenario: Pure machine arrow skips adversarial phase
    Given an arrow A2 with only `machine` / `depth-robust` clauses
    When the runner reaches A2
    Then the orchestrator does NOT spawn an adversary
    And the arrow proceeds directly to verification
```

### F-2: Clause-falsification

```gherkin
Feature: Clause-falsification sub-activity

  Scenario: Adversary attempts to falsify each depth-sensitive clause
    Given R0 has the arrow's clause set
    When R0 enters clause-falsification
    Then R0 takes each `depth-sensitive` clause in turn
    And attempts to construct a counterexample making that clause
        fail (e.g., for "every feature has failure-path scenarios",
        identify features lacking failure-path scenarios)
    And each successful falsification raises a finding with
        severity assigned per the stated rule

  Scenario: Falsification produces a finding
    Given clause C9 = "the negative space is specified" on an
        analyst→architect arrow
    When R0 finds that ContextA's spec mentions only happy paths
        with no rejection rules
    Then R0 raises finding F1:
        { type: clause-falsification,
          target-clause: C9,
          severity: high,
          basis: "scope: features/contextA/, rule: scan for
                  reject/refuse/error vocabulary; result: 0 hits",
          locations: [features/contextA/*.feature],
          evidence: <quote from the artifact> }
    And F1 is registered in the state machine with status `open`

  Scenario: Cannot falsify — clause passes
    Given clause C9 and R0 attempted falsification
    When R0 finds genuine failure-path coverage in the spec
    Then no finding is raised for C9 in this sub-activity
    And R0 records its falsification attempt as "no defect found"
        in the adversarial-phase audit trail
```

### F-3: Open sweep

```gherkin
Feature: Open sweep sub-activity

  Scenario: Adversary scans for un-clause-named defects
    Given clause-falsification has completed
    When R0 enters open sweep
    Then R0 reads the upstream artifact end-to-end with no
        clause-specific scope
    And R0 raises findings for any defect it notices, regardless
        of whether a clause covers the category (e.g., "context
        boundaries are described but the data flow between them is
        not")
    And each finding has severity assigned per a stated rule

  Scenario: Open sweep finds a structural concern
    Given the analyst's spec for ContextA references a service
        in ContextB that is never declared in
        `cross-context/interactions.md`
    When R0 sweeps
    Then R0 raises finding F2:
        { type: open-sweep,
          severity: high,
          basis: "scope: cross-context references in features/contextA/,
                  rule: every named external service must appear in
                  cross-context/interactions.md; result: 1 missing",
          locations: [features/contextA/payment.feature:23],
          evidence: "references `ContextB.charge()` but no
                     interaction declared" }
```

### F-4: Depth classification

```gherkin
Feature: Depth classification sub-activity

  Scenario: Walk requirements on the depth ladder
    Given the upstream artifact contains a list of requirements
        (e.g., analyst features, each with a declared minimum
        depth)
    When R0 enters depth classification
    Then R0 takes each requirement in turn
    And classifies it on the 4-tier depth ladder
        (NONE / SHALLOW / MOCKED / REALISTIC by default; project
        overrides apply)
    And for any requirement classified below its declared minimum,
        R0 raises a finding

  Scenario: Requirement below min
    Given requirement REQ-12 with declared minimum REALISTIC
    And R0 classifies REQ-12 as MOCKED (only mocked tests exist)
    Then R0 raises finding F3:
        { type: depth-below-min,
          target-requirement: REQ-12,
          classified: MOCKED,
          declared-min: REALISTIC,
          severity: medium,
          basis: "the requirement's tests all use mocks; no
                  realistic-tier test against real dependency was
                  found",
          locations: [tests/req-12/*.test.go] }

  Scenario: Adversary tier too shallow to classify
    Given R0's model tier is below the depth-sensitivity requirement
        for depth-classification
    When R0 attempts to classify
    Then R0 records the classification as `unevaluated` per
        requirement
    And findings raised by depth-classification have severity
        `unevaluated`
    And the arrow's verification will block (per the
        `every-requirement-meets-min-depth` clause auto-inserted
        at verification, §11)
```

### F-5: Remediation loop

```gherkin
Feature: Remediation loop

  Scenario: Producer fixes a finding
    Given finding F1 status `open` raised by adversary round R0
    When the producer (the upstream role) addresses F1 by editing
        the upstream artifact
    Then the producer signals the orchestrator that F1 is
        addressed
    And the orchestrator spawns a fresh adversary R1
    And R1 receives the same inputs as R0 EXCEPT it sees the
        updated upstream artifact
    And R1 re-runs clause-falsification scoped to F1's target
        (efficient re-attack)
    And if R1 cannot reproduce F1, F1 transitions to `resolved`
    And if R1 reproduces F1, F1 stays `open` and another round
        begins

  Scenario: Producer proposes accepted-risk
    Given finding F1 status `open`
    When the producer proposes accepted-risk
    Then the orchestrator hands F1 to the attestation flow
        component
    And the operator attests accepted-risk OR rejects
    And on accepted-risk: F1 transitions to `accepted-risk`
    And on rejected: F1 stays `open` and remediation continues

  Scenario: Multiple findings in flight
    Given findings F1, F2, F3 all `open` after R0
    When the producer fixes F1 and F2 but not F3
    Then the next round R1 re-attacks all three
    And F1, F2 transition to `resolved` if not reproduced
    And F3 stays `open`
    And remediation continues for F3

  Scenario: Non-convergence — escalate after N rounds
    Given finding F1 has been re-attacked through N=5 rounds and
        remains `open`
    When round 5 completes
    Then the orchestrator stops the remediation loop
    And escalates to the operator with kind `remediation-non-convergence`
    And the operator must decide: accepted-risk OR route the
        artifact for deeper rework upstream
```

### F-6: Phase exit to verification

```gherkin
Feature: Hand off to verification

  Scenario: Convergence — all findings disposed
    Given all findings above the severity threshold are
        `resolved` or `accepted-risk`
    When the remediation loop converges
    Then the orchestrator signals the runner to begin the
        verification phase
    And the runner auto-inserts `no-open-finding` and
        `every-requirement-meets-min-depth` (per §11)
    And the arrow proceeds to verification

  Scenario: Findings persist below threshold
    Given findings F4, F5 exist with severity `info` (below the
        `medium` threshold)
    When convergence is checked
    Then the orchestrator treats F4 and F5 as informational only
    And the phase converges (below-threshold findings do not
        block)
    And F4, F5 are recorded but visible in the arrow's finding log
```

---

## Failure modes

| ID | Failure | Blast radius | Intended degradation |
|---|---|---|---|
| FM-1 | Adversary model returns malformed output (e.g., findings without required severity field) | Single round | Treat as evaluator-output bug; orchestrator retries the round with explicit shape reminder. Persistent malformed output → operator triage. |
| FM-2 | Adversary tier unavailable (depth-sensitivity needs a tier the routing config doesn't provide) | Phase | Per `gates.md` §6, findings are `unevaluated` with reason `depth-below-required`. Operator escalates. |
| FM-3 | Producer never responds to a finding | Single finding | After timeout (default 1h, init-override), the orchestrator marks the round as stalled; operator-routable. |
| FM-4 | Producer signals "fixed" but artifact is unchanged | Single finding | The fresh adversary re-attacks the unchanged artifact, re-raises the finding. Loop continues, eventually hits N-round escalation. |
| FM-5 | Adversary attacks a non-deterministic artifact (e.g., spec with date-dependent rule) and findings vary between rounds | Single arrow | The schema does not assume determinism in upstream artifacts. Variability in findings between rounds is treated as legitimate. If it causes non-convergence, escalates per F-5 scenario 4. |
| FM-6 | Two adversarial phases run concurrently on the same arrow (different passes) | Two passes | Forbidden by `single-active-role-instance(role, context)` in the catalogue. Caught by the runner before spawn. |
| FM-7 | Adversary's clean-context spawn leaks state (e.g., the model tier has cached context from a prior session) | Phase integrity | This is a binding bug in how adversary instances are spawned. The orchestrator must validate clean context per spawn (e.g., new session token, no prior message history) — implementation requirement. |

---

## Cross-component interactions

- **Adversarial ← runner.** Runner signals "arrow ready for
  adversarial" once the upstream artifact is emitted and clauses
  are loaded.
- **Adversarial → state machine.** Findings are registered in the
  engine; the engine validates their state transitions.
- **Adversarial ↔ producer role (upstream).** The orchestrator
  notifies the producer of findings (so the producer can remediate);
  the producer signals "fixed" or "propose accepted-risk."
- **Adversarial ↔ attestation flow.** Accepted-risk proposals route
  to the attestation flow; operator verdicts come back.
- **Adversarial → runner (verification).** When convergence is
  reached, the orchestrator signals the runner to start the
  verification phase.
- **Adversarial ← grid amendment.** If an amendment lands mid-phase,
  the orchestrator halts the current round per D22 (mid-phase
  abort). Re-attack only starts again after the next pass on the
  re-traversed arrow.

---

## Assumptions

| ID | Assumption | Falsifiability |
|---|---|---|
| A-1 | Fresh-context spawn produces a meaningfully independent attack each round. | Falsifies if model tiers cache context across sessions (e.g., conversation API with retained memory). Mitigation: explicit clean-context verification per spawn. |
| A-2 | N=5 rounds is enough to converge for typical findings. | Falsifies if many real-world findings require >5 rounds. Init may raise N; if the average is consistently near the limit, the limit needs raising. |
| A-3 | Producers will respond to findings in reasonable time. | Falsifies if producers (LLMs) hang or refuse to address findings. Timeout per FM-3 catches this. |
| A-4 | Severity assignment by the adversary is more reliable than depth classification by the same adversary at the same tier. | Falsifies if severity is highly variable between rounds. The schema treats severity as depth-sensitive (D15); if it's actually unreliable, the unevaluated routing absorbs the cost. |

---

## Open questions

- **Targeted vs full re-attack.** F-5 scenario 1 says re-attack is
  "scoped to F1's target." But sometimes a producer's fix to F1
  inadvertently breaks F-elsewhere. Should re-attack be full each
  time, or targeted? Spec currently silent; implementation choice.
- **Adversary continuity across remediation.** Invariant 1 says
  fresh adversary per round. But an experienced adversary (across
  multiple projects, not within one project) might be more
  effective. The schema's "clean context" rule prevents intra-pass
  continuity; cross-project learning is out of scope.
- **Sub-activity parallelism.** Currently sub-activities run in
  order. Could clause-falsification and open-sweep run in parallel
  (depth-classification depends on neither)? Spec is sequential
  for clarity; parallelism is a future optimization.
- **Adversary self-attestation.** Findings raised by the adversary
  carry the `adversary` role-id as producer. Does the adversary's
  basis statement need a hint per §9? Currently yes
  (depth-sensitive judgements have hints), but the operator
  attesting the basis seems redundant when the basis itself is the
  evidence.
