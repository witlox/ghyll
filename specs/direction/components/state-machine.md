# Component: status state machine engine

The state machine engine maintains and queries the three lifecycles
defined in `gates.md` §7 (clause, arrow, finding) plus the pass
lifecycle in §7.1a and the project-level composite in §7.4. The
schema specifies the *rules*; this component spec specifies *how*
they are stored, derived, queried, and persisted.

> Status: design intent.

---

## Scope

**In scope.** In-memory representation of statuses. Transition
validation (rejects illegal transitions per §7.1, §7.2, §7.3, §7.1a).
Derivation (arrow status from clauses, project status from arrows).
Query interface for other components. Persistence to the checkpoint
log on pass finalization.

**Out of scope.** Setting status from external sources (the runner
sets clause statuses; the engine just validates and records). Pass
execution itself (that's the runner). Persistence layer mechanics
(uses existing Merkle DAG checkpoint infrastructure).

---

## Domain model

| Term | Definition |
|---|---|
| **Status store** | The in-memory data structure holding current statuses for all active passes and arrows in the project. Bounded — only `running` passes have full state; `completed`/`aborted` passes are flushed to the checkpoint log. |
| **Transition** | A movement of a clause / arrow / finding / pass from one status to another. Must be valid per the §7 state machines. |
| **Derived value** | A status not directly set, computed from sources. Arrow status is derived from clause statuses + invalidation events. Project status is derived from arrow statuses + residue. |
| **Query** | A read against the status store or checkpoint log. Read-only; queries do not change state. |
| **Snapshot** | The complete state of the status store at a point in time, suitable for serialization or backup. |

---

## Invariants

1. **Transitions are validated.** Every status change goes through
   the engine. The engine rejects illegal transitions (e.g., `pass`
   → `pending` for a clause) and reports the violation. Direct
   mutation of the store is impossible from outside. **The engine
   owns the per-clause transition lock** (D34) — only one
   transition per `(pass-id, clause-id)` can be in flight at a
   time; concurrent requests serialize on the lock.
2. **Derivation is pure.** Given the same inputs (clause statuses,
   finding states, invalidation events), arrow-status derivation
   always produces the same output. The derivation function has no
   side effects.
3. **Status is per-pass for clauses.** The store keys clause status
   by `(pass-id, clause-id)`. The same clause has independent status
   on each pass.
4. **`invalidated` arrow status is externally set.** Per `gates.md`
   §7.2, `invalidated` is not lifted from clause states; it is set
   on the arrow by the grid amendment component. The engine accepts
   `invalidated` writes from the amendment component as the only
   external arrow-status source.
5. **Project-level status reads are consistent.** A project-status
   query at time T returns a coherent triple
   `(complete-against-grid-vN, R, C)` reflecting the state at T,
   not a mix of stale and current per-arrow values. Implementation:
   acquire a read-lock for the duration of the project-status
   computation.
6. **Persistence on finalization.** When a pass reaches `completed`
   or `aborted`, its full status state is serialized to the
   checkpoint log. After finalization, the in-memory state for that
   pass can be evicted.

---

## State machines (cross-reference)

The engine implements four state machines, all defined in `gates.md`:

| Machine | States | Reference |
|---|---|---|
| **Clause** | `pending`, `running`, `pass`, `fail`, `awaiting-attestation`, `insufficient-basis`, `unevaluated` | §7.1 |
| **Arrow** | `complete`, `provisional`, `unevaluated`, `blocked`, `invalidated` | §7.2 |
| **Finding** | `open`, `running`, `resolved`, `accepted-risk`, `unevaluated` | §7.3 |
| **Pass** | `running`, `completed`, `aborted` (with `reason` field) | §7.1a |

The engine does not redefine the machines; it implements them
faithfully.

---

## Behaviors (features)

### F-1: Clause status transitions

```gherkin
Feature: Clause transitions through its lifecycle

  Scenario: Machine clause evaluated to pass
    Given a clause C1 with status `pending` on pass P1
    When the runner reports a successful evaluation
    Then the engine validates the transition pending → pass
    And records the new status with a timestamp

  Scenario: Invalid clause transition
    Given a clause C1 with status `pass` on pass P1
    When the runner attempts to set status `pending`
    Then the engine rejects the transition with
        `illegal-transition: pass → pending not in clause state machine`
    And the clause status is unchanged

  Scenario: Attested clause awaits operator
    Given an attested clause with hint emitted
    Then the engine records status `awaiting-attestation`
    And exposes the clause in the attestation-flow query

  Scenario: Depth-below-required produces unevaluated
    Given a depth-sensitive clause routed below required tier
    When the runner reports the depth gate failed
    Then the engine records status `unevaluated` with
        `reason: depth-below-required`
```

### F-2: Arrow status derivation

```gherkin
Feature: Arrow status derived from clauses + invalidation

  Scenario: Arrow derivation order of precedence
    Given an arrow A1 on pass P1 with clauses:
      | clause | status |
      | C1     | pass   |
      | C2     | fail   |
      | C3     | unevaluated |
      | C4     | awaiting-attestation |
    When the engine derives the arrow status
    Then the result is `blocked` (fail > unevaluated > provisional > complete)

  Scenario: Invalidation supersedes derived status
    Given arrow A1 with all clauses `pass` (would derive to `complete`)
    When the grid amendment component sets A1 to `invalidated`
    Then querying A1's status returns `invalidated`
    And the underlying clause statuses are unchanged in the store

  Scenario: Re-traversal clears invalidated
    Given arrow A1 has status `invalidated` from grid vN
    When a new pass P2 starts on A1 against grid v(N+1)
    Then the engine creates fresh clause statuses for P2
    And A1's status becomes the derived status from P2's clauses
    And the prior `invalidated` state moves to history (checkpoint log)
```

### F-3: Finding lifecycle

```gherkin
Feature: Finding transitions

  Scenario: Finding resolved by re-attack
    Given a finding F1 with status `open` and severity `medium`
    When the adversarial phase re-attacks after producer remediation
        and cannot reproduce F1
    Then the engine transitions F1 to `resolved`

  Scenario: Finding accepted as risk
    Given a finding F1 with status `open`
    When the producer proposes accepted-risk
    And the operator attests `accepted-risk`
    Then the engine transitions F1 to `accepted-risk`

  Scenario: Producer cannot accept own risk
    Given a finding F1 with status `open`
    When the producer attempts to set `accepted-risk` directly
    Then the engine rejects with `producer-cannot-accept-own-risk`
    And requires the verdict to come from the attestation flow
        component with operator op-id
```

### F-4: Pass lifecycle

```gherkin
Feature: Pass transitions

  Scenario: Pass starts running
    Given the runner starts a new pass P1 on arrow A1
    When the engine records the pass
    Then P1 has status `running` with `started-at` set
        and `completed-at` unset

  Scenario: Pass completes normally
    Given pass P1 is `running` and the runner has finalized clause results
    When the runner signals completion
    Then the engine transitions P1 to `completed`
    And `completed-at` is recorded
    And the pass's full state is flushed to the checkpoint log

  Scenario: Pass aborted by invalidation
    Given pass P1 is `running` in remediation phase
    When the grid amendment component signals abort with
        `reason: invalidated`
    Then the engine transitions P1 to `aborted` with that reason
    And findings from P1 are tagged with their original grid-version

  Scenario: Pass aborted by crash recovery
    Given pass P1 was `running` but the runner crashed
    When the engine performs crash recovery on restart
    Then the engine finds the orphaned pass and transitions it to
        `aborted` with `reason: crash`
```

### F-5: Project-level status query

```gherkin
Feature: Compute project-level status

  Scenario: All declared cells complete
    Given a grid vN with every declared arrow at status `complete`
    And the residue list is empty
    And no arrow has status `unevaluated`
    When a project-status query is issued
    Then the result is `(complete-against-grid-vN, R=0, C=0)`

  Scenario: Some unevaluated cells
    Given a grid vN with 10 arrows: 9 `complete`, 1 `unevaluated`
    When the project-status query runs
    Then the result includes `C=1`
    And the result lists the unevaluated arrow's id in detail

  Scenario: Residue computation
    Given a grid with 5 undeclared (stratum, context) cells
    And the harness computes each undeclared cell's imputed cost
        as the sum of per-clause default costs from the role's
        exit-gate template under the project's language bindings
    And the 5 cells compute to imputed costs [15, 15, 15, 15, 15]
        (uniform in this example; real values vary per role and
        per binding)
    When the residue is computed
    Then R = 15+15+15+15+15 = 75
    And the project-status query reports R=75 alongside C and
        complete-against-grid-vN

```

### F-6: Snapshot and replay

```gherkin
Feature: Snapshot and replay state from checkpoint log

  Scenario: Restart from checkpoint log
    Given the harness was running and is restarted
    When the engine initializes
    Then it reads the checkpoint log to reconstruct:
      - all `running` passes (treated as `aborted` with reason `crash`)
      - all `completed`/`aborted` passes (kept in log, not in
        in-memory store)
      - current grid version
      - current arrow statuses (per the latest completed pass per arrow)
    And the engine is ready to accept new pass starts

  Scenario: Query historical pass
    Given a query for pass P5 (completed and flushed)
    When the engine receives the query
    Then it reads from the checkpoint log
    And returns the historical pass's full state
```

---

## Failure modes

| ID | Failure | Blast radius | Intended degradation |
|---|---|---|---|
| FM-1 | Engine receives a transition request for a clause it doesn't know about | Single transition | Reject with `unknown-clause-id`. Caller (typically the runner) must register the pass and its clause set first. |
| FM-2 | Engine receives an illegal transition (e.g., pass → pending) | Single transition | Reject with `illegal-transition`. Current state preserved. |
| FM-3 | Two simultaneous updates to the same clause status from concurrent callers | Single clause | Resolved by the per-clause lock in the runner (engine assumes serialized updates per clause). If concurrent updates somehow reach the engine, it accepts the later timestamp's update and warns. |
| FM-4 | Checkpoint write fails (disk full, etc.) | Single pass | Pass finalization fails; pass remains `running` in memory until the checkpoint write succeeds. Repeated failures escalate to operator. |
| FM-5 | In-memory state diverges from checkpoint log (e.g., engine bug) | Whole project | The checkpoint log is the source of truth. On detected divergence, the engine logs an alert and re-reads from the log. May require operator intervention. |
| FM-6 | Project-level status query runs concurrently with an in-flight transition | Single query | Resolved by the read-lock invariant 5. The query waits or sees a consistent snapshot. |

---

## Cross-component interactions

- **Engine ← runner.** Runner reports per-clause status changes.
- **Engine ← attestation flow.** Attestation flow reports operator
  verdicts for `attested` clauses (these become clause transitions
  in the engine).
- **Engine ← adversarial phase.** Adversarial phase raises findings;
  the engine records them. Re-attacks produce finding transitions.
- **Engine ← grid amendment.** Amendment component sets
  `invalidated` on affected arrows.
- **Engine → runner.** Runner queries arrow status before permitting
  downstream transitions.
- **Engine → UI / tooling.** External readers query project status,
  arrow status, finding lists.
- **Engine ↔ checkpoint log.** Engine reads/writes the Merkle DAG
  checkpoint chain from existing `memory/` v1 infrastructure.

---

## Assumptions

| ID | Assumption | Falsifiability |
|---|---|---|
| A-1 | In-memory state for `running` passes is bounded in size. | Falsifies if a project has thousands of simultaneous passes. Currently no scaling concern; single-operator workflow expects few concurrent passes. |
| A-2 | Checkpoint log writes are fast enough to keep up with pass finalizations. | Falsifies if mutation-test pass finalizations produce so many results that write throughput becomes a bottleneck. |
| A-3 | The Merkle DAG checkpoint infrastructure (v1) is suitable for v2 pass state. | Falsifies if v2's pass-state size exceeds v1 checkpoint expectations. |

---

## Open questions

- **In-memory eviction policy.** Completed passes are flushed to the
  checkpoint log; should there be an eviction policy for stale
  `running` passes (e.g., a pass that's been running for >24h is
  treated as zombie)? Currently silent; relies on the runner's
  liveness checks.
- **Distributed engine.** If a project ever needs multi-machine
  coordination (per A-4 of `init.md`), the engine becomes a service
  rather than an in-process component. Out of scope for v1.
- **Query language.** External queries currently assume a fixed
  shape (project status, arrow status, pass-by-id). A richer query
  language (e.g., "all arrows in context X that are `provisional`
  due to operator delay") could be useful. Out of scope for v1;
  could layer on top.
