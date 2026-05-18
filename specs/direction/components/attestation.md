# Component: attestation flow

The attestation flow component coordinates **operator verdicts on
attested clauses**. The runner emits hint requests; the producer role
returns hints; this component presents hints to the operator,
collects verdicts, appends typed JSONL records, and feeds the verdict
back to the runner. It also manages the `insufficient-basis`
N-round escalation and the `op-id` session lifecycle.

> Status: design intent.

---

## Scope

**In scope.** Operator-session lifecycle (`op-id` declaration,
multi-operator handoff). The operator event bus — the single channel
through which all operator-facing communication flows (attestation
requests, escalation prompts, init prompts, refusal prompts,
aborted-pass notifications). Hint presentation. Verdict capture and
record emission (typed JSONL per `gates.md` §10.2). Coordination of
the `insufficient-basis` escalation path (`gates.md` §10 + D23).
Multi-operator coordination within a single pass.

**Out of scope.** Hint generation (the producer role does that).
Clause status updates (the state machine engine, called by this
component). The operator UI itself (a separate concern; this
component provides the data, the UI renders it).

---

## Domain model

| Term | Definition |
|---|---|
| **Operator session** | A logical session bound to an `op-id`. Started when the operator declares identity; ends when the operator explicitly closes or the harness terminates. |
| **`op-id`** | A declared string identifying the operator (conventionally email or handle). Non-empty; recorded in every attestation. |
| **Hint** | The structured pointer emitted by a producer per `gates.md` §9: `{clause, locations, basis, residue}`. |
| **Attestation request** | A pending decision: a hint awaiting an operator verdict. Carries `(clause-id, pass-id, hint, severity-context)`. |
| **Verdict** | One of `pass`, `fail`, `insufficient-basis` (per `gates.md` §10). Plus, for `attested-accepted-risk`: an additional `accepted-risk` verdict for findings. |
| **Attestation record** | One JSONL line written to the per-pass attestation file. Shape per `gates.md` §10.2 (unit-typed: `confirm`, `record-locations-inspected`, `write-residue-note`). |
| **Round counter** | For `insufficient-basis`: a count of how many times the same clause has received this verdict in successive remediation rounds. Triggers escalation at N (default 3). |

---

## Invariants

1. **`op-id` required.** Every attestation record has a non-empty
   `op-id`. The component refuses verdict capture if no operator
   session is active.
2. **Producer cannot attest own arrows.** The operator session's
   `op-id` is distinct from any synthetic role-id. A producer role
   instance cannot impersonate an operator. (Enforced by separate
   API surfaces: producers emit hints; only operator sessions emit
   verdicts.)
3. **Append-only attestation log.** Records are appended; never
   modified or deleted. Multiple verdicts on the same clause in a
   single pass appear as multiple records, in chronological order.
   The latest record's verdict is authoritative.
4. **Unit-typed records.** Each record matches one of three shapes
   per `gates.md` §10.2 (`confirm`, `record-locations-inspected`,
   `write-residue-note`). The component validates the shape on
   write.
5. **Verdict→status mapping is total.** Every verdict maps to a
   defined clause status (`pass` → `pass`, `fail` → `fail`,
   `insufficient-basis` → keep `insufficient-basis` per §10).
6. **Insufficient-basis counter is per-clause-per-pass.** The
   counter resets when the pass aborts or completes; a new pass on
   the same arrow starts with counter at 0.
7. **Multi-operator chains preserved.** When multiple operators
   touch the same pass, their verdicts appear in chronological
   order in the attestation file. The full chain is auditable.

---

## Behaviors (features)

### F-1: Session lifecycle

```gherkin
Feature: Operator session

  Scenario: Session start with op-id
    Given the harness has no active operator session
    When the operator declares `op-id = "alice@example.com"`
    Then the component creates a session bound to that op-id
    And the session is active for subsequent verdicts
    And verdict-capture API calls without an active session are
        refused with `no-active-session`

  Scenario: Multi-operator handoff in one pass
    Given operator Alice is active and attests clause C1
    When Alice ends her session
    And operator Bob declares `op-id = "bob@example.com"` and starts
    Then Bob is now active
    And Bob may attest clauses C2, C3 within the same pass
    And the attestation file for the pass records:
      - Alice's verdict on C1 with op-id=alice
      - Bob's verdicts on C2, C3 with op-id=bob

  Scenario: Empty op-id is refused
    Given operator attempts to declare `op-id = ""`
    Then session start is refused with `op-id-required`
```

### F-2: Hint presentation and verdict capture

```gherkin
Feature: Operator decides an attested clause

  Scenario: Operator returns pass
    Given an attestation request for clause C5 with hint
        { locations: [features/contextA/payment.feature:42-67],
          basis: "all failure-path scenarios in this region",
          residue: "happy-path tests not scanned" }
    And operator Alice is active
    When Alice inspects the locations and submits verdict `pass`
        with unit `confirm`
    Then a record is appended to
        `attestations/v<N>/contextA/stratum-<S>/<role-pair>/<pass-id>.jsonl`,
        where `<role-pair>` uses `__` (double underscore) as the
        separator (e.g., `analyst__architect`,
        `analyst__adversary__architect`, `init__analyst`):
        { unit: confirm,
          clause: C5,
          verdict: pass,
          ts: <now>,
          op-id: alice@example.com }
    And the component signals the state machine engine to
        transition C5 to `pass`

  Scenario: Operator returns fail with record-locations
    Given an attestation request for clause C5
    When Alice submits verdict `fail` with unit
        `record-locations-inspected` and
        `inspected: [features/contextA/payment.feature:42-50]`
    Then a record is appended with the inspected list
    And C5's status becomes `fail`
    And the producer is notified of the failure to remediate

  Scenario: Operator returns insufficient-basis with residue note
    Given an attestation request for clause C5
    When Alice submits verdict `insufficient-basis` with unit
        `write-residue-note` and
        `residue-note: "feature file is too large to manually
                         inspect; need a deeper artifact"`
    Then a record is appended with the residue note
    And the attestation flow signals state-machine engine to
        transition C5 to `insufficient-basis`
    And the engine *derives* the arrow's status (the attestation
        flow does NOT directly set arrow status; arrow status is
        always derived from clause+finding state per
        state-machine.md invariant 2)
    And the round counter for C5 increments to 1
```

### F-3: Insufficient-basis escalation

```gherkin
Feature: After N rounds of insufficient-basis, escalate

  Scenario: Three rounds, then escalation
    Given clause C5 has received `insufficient-basis` from rounds
        1 and 2
    And the producer has re-emitted the hint at a deeper depth
        tier each round
    When round 3 also returns `insufficient-basis`
    Then the component records the escalation
    And presents the operator with two options:
      1. attest `accepted-risk` with a `write-residue-note`
         recording why the basis remains insufficient
      2. route the artifact back upstream for deeper rework with
         the rationale `requires-deeper-artifact`
    And neither option is the default — operator must choose

  Scenario: Operator accepts risk on the third round
    Given the escalation prompt
    When operator chooses option 1 with residue note
    Then a record is appended:
        { unit: write-residue-note,
          clause: C5,
          verdict: accepted-risk,
          ts: <now>,
          op-id: alice@example.com,
          inspected: [...],
          residue-note: "<text>" }
    And the FINDING associated with C5 (the accepted-risk
        proposal's target) transitions to status `accepted-risk`
        per gates.md §7.3
    And C5's CLAUSE-status transitions to `pass` once all findings
        on the clause are disposed (`resolved` or `accepted-risk`)
    And the round counter resets

  Note: clause status and finding status are independent. The
  clause is `pass` because the operator has finalized; the finding
  is `accepted-risk` because the risk was acknowledged. There is
  no "pass with metadata" — these are two distinct objects.

  Scenario: Operator routes upstream
    Given the escalation prompt
    When operator chooses option 2 with rationale
    Then the component signals the runner that C5's upstream
        artifact requires deeper rework
    And the arrow's pass is aborted with
        `reason: requires-deeper-artifact`
    And the producer role is re-routed at a deeper tier to
        produce a richer artifact

  Scenario: insufficient-basis-rounds-max is configurable
    Given init declared insufficient-basis-rounds-max=5 for this project
    When clause C5 receives `insufficient-basis` for the 4th time
    Then no escalation is triggered yet (max not reached)
    And the round counter is 4
```

### F-4: Accepted-risk for findings (via attestation)

```gherkin
Feature: Operator attests accepted-risk on a finding

  Scenario: Producer proposes accepted-risk
    Given finding F1 with status `open`
    And the producer (e.g., the analyst on an analyst→architect
        arrow) proposes accepted-risk for F1
    When the adversarial component hands F1 to attestation flow
    Then this component presents F1's evidence + the producer's
        rationale to the active operator
    And captures the operator's verdict

  Scenario: Operator attests accepted-risk on F1
    Given the operator inspects F1's evidence
    When operator submits `accepted-risk` verdict
    Then a record is appended (unit: record-locations-inspected or
        write-residue-note depending on severity)
    And F1's status becomes `accepted-risk` (per `gates.md` §7.3)

  Scenario: Operator rejects accepted-risk proposal
    Given the operator finds the producer's proposal weak
    When operator submits `fail` on the accepted-risk request
    Then F1 stays `open`
    And the producer must continue remediation
```

### F-5: Verifier-driven verdict replay

```gherkin
Feature: Attestation records are verifiable

  Scenario: Verifier reads attestation log
    Given a pass-id and a clause-id
    When the verifier component (a different process / later run)
        reads the attestation file
    Then it finds all records for that clause in chronological
        order
    And can reconstruct the operator's decision chain
    And can verify that the required fields per unit are present
        (per `gates.md` §10.2)

  Scenario: Missing required field is detected
    Given an attestation record with unit
        `record-locations-inspected` but no `inspected` array
    When the verifier reads it
    Then the record is flagged as malformed
    And the operator session that produced it is alerted
```

---

## Failure modes

| ID | Failure | Blast radius | Intended degradation |
|---|---|---|---|
| FM-1 | Operator submits a verdict before any session is active | Single verdict | Refused with `no-active-session`. Operator must declare op-id first. |
| FM-2 | Two operators submit verdicts on the same clause near-simultaneously | Single clause | The component serializes verdict-capture per clause; later verdict wins, both recorded in order. Audit log shows the conflict. |
| FM-3 | Disk write fails on attestation record append | Single verdict | Verdict capture fails; operator is prompted to retry. The clause stays in its prior status; no partial-state hazard. |
| FM-4 | Operator session times out mid-attestation | Single verdict | The pending attestation request is preserved; on next session start (same or different operator), the request is re-presented. Round counter is not incremented (the timeout isn't an insufficient-basis verdict). |
| FM-5 | A record on disk is hand-edited to falsify history | Audit integrity | The attestation file isn't tamper-proof against malicious operators; the schema relies on append-only convention. Crypto-signing of records (extending the v1 ed25519 infrastructure) is a future enhancement. |
| FM-6 | The pre-condition that producer-cannot-attest is bypassed by a bug | Single arrow | If a producer role somehow records a verdict, the verifier component detects it (op-id matches a known producer's namespace, not an operator identity) and flags. Schema invariant is structural; implementation must enforce. |

---

## Cross-component interactions

- **Attestation ← runner.** Runner forwards hints for attested
  clauses, requests verdicts.
- **Attestation ← adversarial.** Adversarial component forwards
  accepted-risk proposals for findings.
- **Attestation → state machine engine.** Operator verdicts become
  clause/finding transitions in the engine.
- **Attestation → runner.** After a verdict, the runner is signaled
  to resume the pass (recompute arrow status).
- **Attestation ↔ operator UI.** The UI is the operator-facing
  surface; this component provides the data (pending requests,
  hint contents, escalation prompts) and consumes the verdicts.
- **Attestation ↔ on-disk attestation files.** Owns
  `attestations/<vN>/<context>/<stratum>/<role-pair>/<pass-id>.jsonl`.

---

## Assumptions

| ID | Assumption | Falsifiability |
|---|---|---|
| A-1 | Operators will provide non-trivial residue notes when escalating to accepted-risk. | Falsifies if operators consistently write empty / placeholder notes. The schema records the procedure; behavioral quality is human-guarded. |
| A-2 | Multi-operator handoff within a pass is rare. | Falsifies if it becomes common (e.g., shift-based teams). Should still work, but UX may need refinement. |
| A-3 | `insufficient-basis-rounds-max = 3` is a reasonable default. | Falsifies if many real-world clauses hit escalation routinely. Init may raise. |
| A-4 | The append-only attestation log is sufficient for audit needs. | Falsifies if regulatory or contractual audit requires cryptographic signatures per record. Future: ed25519-sign each record. |

---

## Open questions

- **Hint freshness.** A hint emitted in remediation round 1 may no
  longer apply after the producer's fix; should the producer re-emit
  the hint before each operator-attestation request, or is the
  round-1 hint reused? Spec currently silent; implementation
  defaults to re-emit (safer).
- **Operator-facing time budgets.** The attestation `cost` (action
  units) implies a friction budget but does not enforce time.
  Should the component present a time hint ("estimated 5
  minutes") to the operator? Out of scope for v1.
- **Asynchronous verdicts.** Currently the schema assumes
  synchronous attestation: the operator is present, decides, the
  pass proceeds. For long-running passes, async (operator decides
  hours later) may be needed. Spec is silent; implementation could
  support either via the operator-UI layer.
- **Verdict revocation.** Once a verdict is recorded, can the
  operator change their mind? Currently no (append-only with latest
  wins, but no explicit revocation API). Could add a `revoke`
  unit; out of scope for v1.
