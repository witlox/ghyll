# Component: machine-clause runner

The runner is the **enforcement spine**. It invokes clause evaluators,
collects clause statuses, derives arrow status, and refuses
transitions when an arrow's status is not `complete`. The catalogue
(`components/concepts.md`) describes *what* the evaluators do; the
runner describes *when* and *how* they are invoked.

> Status: design intent. Per `build-notes.md` build order, the runner
> is the third component built (after concepts and init).

---

## Scope

**In scope.** Invoking machine evaluators per their concept schemas.
Collecting per-clause results. Coordinating with the attestation
flow component for `attested` clauses. Deriving arrow status per
`gates.md` §7.2 precedence. Refusing role transitions when arrow
status is not `complete`. Recording per-pass results in the
checkpoint log.

**Out of scope.** The evaluators themselves (those live in the
language bindings). The attestation flow (separate component). The
adversarial phase orchestration (separate component, though the
runner serves as the verification phase of §11). Grid amendment
(separate component).

---

## Domain model

| Term | Definition |
|---|---|
| **Evaluator** | A concrete instrument that decides a machine clause: a binary, script, or harness function. Per `components/concepts.md`, each language-bound concept's evaluator is the binding (e.g., `mutation-score.go = go-mutesting`). Universal concepts have built-in evaluators. |
| **Evaluation run** | A single invocation of an evaluator against a clause's arguments. Has an `evaluation-run-id`, a `clause-id`, a `pass-id`, a `started-at`, a `completed-at`, and a `result`. |
| **Evaluation result** | `{ pass: boolean, details: object }` per the concept's schema. The `details` field carries concept-specific output (e.g., `mutation-score.details.score`). |
| **Pass execution** | A walk through one arrow's clauses for one pass. Per `gates.md` §7.1a, identified by `pass-id` and bound to one `arrow-id`. The runner orchestrates the walk. |
| **Transition refusal** | The runner's act of denying a role transition when the upstream arrow's derived status is not `complete`. Refusal is total; there is no soft-refuse / warning mode. |
| **Concurrent execution** | Multiple pass-executions in flight at the same time, per `gates.md` D22 + D28. The runner coordinates with the global lock and `single-active-role-instance` constraint. |
| **Hint emission** | For `attested` clauses, the runner asks the *producer role* to emit a hint (per `gates.md` §9). The runner does not produce hints itself; it requests them. |

---

## Invariants

1. **Evaluator-result determinism (best effort).** Given the same
   clause arguments and the same artifact state, an evaluator should
   produce the same `{pass, details}` result. The runner does not
   enforce this (some evaluators are inherently non-deterministic —
   e.g., timing-sensitive mutation tests), but persistently
   non-deterministic evaluators are a binding bug.
2. **Total refusal.** When an arrow's derived status is not
   `complete`, every downstream role transition is refused. No
   exceptions, no warnings-only mode, no override flag.
3. **No mid-clause partial state.** A clause is either `pending`,
   `running`, or in a post-evaluation status. There is no observable
   "halfway evaluated" state from outside the runner.
4. **Arrow status is derived, never set.** The runner does not assign
   `complete` to an arrow; it derives the status from the clause
   statuses and finding state per `gates.md` §7.2. The only way to
   *set* arrow status directly is via `invalidated` from the grid
   amendment component.
5. **Concurrency safety.** Two evaluation runs for the same
   `(arrow-id, clause-id, pass-id)` triple cannot exist
   simultaneously. Per-clause transition serialization is owned by
   the **state-machine engine's per-clause transition lock** (D34);
   the runner observes that lock when invoking evaluators. The
   runner additionally owns a **per-`(role, context)` lock** that
   enforces `single-active-role-instance` at pre-spawn time (before
   any pass starts).
6. **Persistence is monotonic per pass.** Within a single pass-id,
   clause statuses can only transition along the lifecycle in §7.1
   (no backwards movement). A pass that needs to re-evaluate is a
   new pass.
7. **Refusal is structural, not advisory.** Refused transitions
   return a structured error including the failing clauses and the
   blocking arrow's id. The error is consumable by tooling.

---

## Behaviors (features)

### F-1: Single machine clause evaluation

```gherkin
Feature: Evaluate a single machine clause

  Scenario: Successful machine evaluation
    Given a clause `no-todo-marker(scope="src/**")` on arrow A1
    And the upstream artifact contains no TODO markers in scope
    When the runner evaluates the clause as part of pass P1
    Then the evaluator runs to completion
    And the clause status transitions: pending → running → pass
        (the `running` state is the observable in-flight state per
        gates.md §7.1; the runner enters it before invoking the
        evaluator and exits to pass/fail on the evaluator's return)
    And an evaluation-run record is appended to the pass log:
        { evaluation-run-id, clause-id, pass-id,
          started-at, completed-at, result: {pass: true, details: {hits: []}} }

  Scenario: Machine evaluation fails
    Given a clause `no-todo-marker(scope="src/**")` on arrow A1
    And the artifact contains `TODO: implement retries` at src/foo.go:42
    When the runner evaluates
    Then the clause status becomes `fail`
    And the result records the hit location
    And the arrow's derived status becomes `blocked` (per §7.2)

  Scenario: Machine evaluator returns unevaluated due to depth
    Given a clause `mutation-score(...)` with depth-sensitive
        depth-type
    And the active model tier is below the clause's declared
        minimum
    When the runner attempts to invoke the evaluator
    Then the runner records the clause status `unevaluated` with
        reason `depth-below-required`
    And the evaluator is NOT invoked (depth gate is checked first)
```

### F-2: Attested clause coordination

```gherkin
Feature: Runner coordinates with attestation flow for attested clauses

  Scenario: Attested clause requires hint emission
    Given a clause `attested-G7` on arrow A1 with the producer
        role being `analyst`
    When the runner reaches this clause during pass P1
    Then the runner requests the producer role to emit a hint per
        `gates.md` §9
    And the producer role returns a hint
        { clause, locations, basis, residue }
    And the runner forwards the hint to the attestation flow
        component
    And the clause status transitions: pending → awaiting-attestation

  Scenario: Operator returns attestation verdict
    Given a clause with status `awaiting-attestation`
    When the attestation flow component records an operator
        verdict (pass / fail / insufficient-basis)
    Then the runner updates the clause status to match the verdict
    And the arrow's derived status is recomputed per §7.2

  Scenario: Producer cannot emit hint
    Given a clause where the producer reports `unable-to-hint`
    Then the runner records the clause `unevaluated` with reason
        `no-rule-selectable-locations`
    And the producer raises a finding of type `unable-to-hint`
        against itself (per `gates.md` §7.3, §9)
```

### F-3: Arrow status derivation

```gherkin
Feature: Arrow status is derived from clause and finding state

  Scenario: All clauses pass
    Given an arrow with 5 clauses, all status `pass`, no findings
    Then the derived arrow status is `complete`

  Scenario: One clause failed
    Given an arrow with 5 clauses, 4 `pass` and 1 `fail`
    Then the derived arrow status is `blocked`

  Scenario: One clause unevaluated (no fails)
    Given an arrow with 5 clauses, 4 `pass` and 1 `unevaluated`
    Then the derived arrow status is `unevaluated`
    And the arrow does NOT satisfy the next role's input

  Scenario: Fail and unevaluated coexist
    Given an arrow with 5 clauses: 3 `pass`, 1 `fail`, 1
        `unevaluated`
    Then the derived arrow status is `blocked` (fails take
        precedence over unevaluated per §7.2)

  Scenario: Awaiting attestation
    Given an arrow with 5 clauses: 3 machine `pass`, 2 attested
        `awaiting-attestation`
    Then the derived arrow status is `provisional`

  Scenario: Unevaluated trumps provisional
    Given an arrow with 5 clauses: 3 `pass`, 1 `unevaluated`, 1
        `awaiting-attestation`
    Then the derived arrow status is `unevaluated`
        (unevaluated > provisional)
```

### F-4: Transition refusal

```gherkin
Feature: Transition refusal

  Scenario: Downstream attempts to start before upstream complete
    Given arrow A (upstream) has derived status `provisional`
    And arrow B (downstream) requires A as input
    When a role attempts to enter the role downstream of arrow B
    Then the runner refuses the transition with a structured error:
        { kind: "transition-refused",
          upstream-arrow: <A's id>,
          upstream-status: "provisional",
          blocking-clauses: [...] }
    And no pass on arrow B is started

  Scenario: Transition allowed when upstream complete
    Given arrow A has derived status `complete`
    When a role attempts the downstream transition
    Then the runner permits the transition
    And starts a new pass on arrow B

  Scenario: Invalidated arrow refuses transitions
    Given arrow A has status `invalidated` (per §7.2)
    When a downstream transition is attempted
    Then the runner refuses with kind: "transition-refused-invalidated"
    And signals that A needs re-traversal first
```

### F-5: Verification phase orchestration

```gherkin
Feature: Verification phase auto-inserts machine clauses

  Scenario: Adversarial phase ran; verification auto-inserts
    Given an arrow that ran an adversarial phase (per §11)
    When the runner enters the verification phase
    Then the runner auto-inserts the `no-open-finding` clause
    And auto-inserts the `every-requirement-meets-min-depth`
        clause
    And evaluates them alongside the arrow's declared verification
        clauses
    And these auto-inserted clauses CANNOT be skipped or weakened
        by the arrow definition

  Scenario: Pure machine arrow skips adversarial / verification only
    Given an arrow with only machine, depth-robust clauses
    Then the runner skips adversarial and remediation phases
    And runs verification only (just evaluates the declared
        machine clauses)
```

### F-6: Concurrent execution coordination

```gherkin
Feature: Concurrent pass execution

  Scenario: Two passes on different contexts run concurrently
    Given pass P1 on (analyst, contextA, stratum-1)
    And pass P2 on (analyst, contextB, stratum-1)
    When both are scheduled
    Then the runner permits both to run concurrently
    And the `single-active-role-instance(analyst, contextA)`
        constraint is satisfied for each
    And neither's evaluation runs interfere with the other's

  Scenario: Two passes on same (role, context) are refused
    Given pass P1 on (analyst, contextA) is running
    When a second pass P2 on (analyst, contextA) is requested
    Then the runner refuses P2 with kind:
        "single-active-role-violation"
    And P2 is not started until P1 completes or aborts

  Scenario: Pass aborted due to grid amendment
    Given pass P1 is in remediation phase
    And a grid amendment lands invalidating P1's arrow
    When the grid amendment component signals abort
    Then the runner halts P1's evaluation runs in flight
    And records pass-status `aborted` with `reason: invalidated`
    And preserves findings discovered before abort, tagged with
        their grid-version
```

### F-7: Per-pass checkpoint emission

```gherkin
Feature: Each pass emits a checkpoint at conclusion

  Scenario: Pass completes
    Given pass P1 has reached terminal arrow status
    When the runner finalizes the pass
    Then the runner emits a checkpoint with:
      - pass-id, arrow-id, grid-version
      - clause-by-clause status
      - finding ids raised during the pass
      - pass-status (`completed`)
      - timestamps
    And the checkpoint is appended to the project's checkpoint log

  Scenario: Pass aborted
    Given pass P1 was aborted mid-phase
    When the runner finalizes the aborted pass
    Then the checkpoint records pass-status `aborted` with the
        abort reason (per `gates.md` §7.1a)
    And the partial evaluation results are persisted (useful for
        forensic value, not state advancement)
```

---

## Failure modes

| ID | Failure | Blast radius | Intended degradation |
|---|---|---|---|
| FM-1 | Evaluator binary crashes mid-evaluation | Single clause | The evaluation-run records `result: {pass: false, details: {crash: <stderr/stdout>}}` and the clause status is `fail`. Operator triage: real failure vs. evaluator bug. |
| FM-2 | Evaluator hangs (exceeds timeout) | Single clause + pass duration | The runner kills the evaluator after `timeout` (per concept schema or arrow override). Clause is `unevaluated` with reason `evaluator-timeout`. Re-run at next pass; recurring timeouts are a binding bug. |
| FM-3 | Evaluator returns malformed JSON / output | Single clause | Treated identically to FM-1 (crash). The runner is strict about evaluator-output shape. |
| FM-4 | Multiple evaluators competing for the same resource (CPU/IO) on a single machine | Pass throughput | The runner does not orchestrate resource scheduling; relies on OS. A future component (scheduler) may add concurrency limits per evaluator-class. |
| FM-5 | Producer role doesn't respond to hint request | Pass | After timeout, the clause is recorded `unevaluated` with reason `producer-no-response` (gates.md §7.1). The operator can re-route or wait for re-emission at deeper tier. |
| FM-6 | Runner crashes mid-pass | Single pass | On restart, the runner detects the orphaned pass (no `completed-at`, no abort record), marks it `aborted` with `reason: crash`, and continues. The user must re-traverse. |
| FM-7 | Two evaluators write to the same checkpoint file concurrently | Checkpoint log | The runner serializes checkpoint writes through the same project-wide write-lock used for grid amendments (D22). Two concurrent pass completions queue FIFO. |
| FM-8 | A transition-refusal error is suppressed or ignored by a buggy caller | Whole project | This is a caller bug, not a runner bug. The schema's invariant is "the runner refuses"; what callers do with the refusal is outside its scope. UI/tooling must surface refusals visibly. |

---

## Cross-component interactions

- **Runner ← init.** The runner reads the grid (`grid.yaml` or
  equivalent) produced by init. The grid tells the runner which
  arrows exist, which clauses each arrow carries, and the per-clause
  arguments and depth types.
- **Runner ↔ catalogue / concept schemas.** For each machine
  clause, the runner looks up the concept's schema
  (`gates/concepts/<concept>.yaml`) to know which evaluator to
  invoke and how to validate the result shape.
- **Runner ↔ language bindings.** For language-bound concepts, the
  runner combines the concept (from the catalogue) with the binding
  (declared at init) to produce the concrete evaluator command.
- **Runner ↔ state machine engine.** The runner reports per-clause
  status to the state machine engine, which derives arrow status
  and project-level metrics.
- **Runner ↔ attestation flow.** For `attested` clauses, the runner
  hands off to the attestation flow component, then resumes when a
  verdict is recorded.
- **Runner ↔ adversarial phase.** The adversarial phase is a
  separate orchestrator; the runner serves as the *verification
  phase* of the §11 three-phase flow. The adversarial phase requests
  the runner to start verification once remediation converges.
- **Runner ↔ grid amendment / global lock.** When an amendment
  lands, the lock component signals the runner to abort affected
  passes (per F-6 scenario 3).
- **Runner ↔ checkpoint log.** The runner emits checkpoints at pass
  conclusion (F-7). The checkpoint log uses the v1 Merkle DAG
  infrastructure (existing code in `memory/`).

---

## Assumptions

| ID | Assumption | Falsifiability |
|---|---|---|
| A-1 | Evaluators run quickly enough to be invoked synchronously per clause. | Falsifies if a typical project's `mutation-score` clause takes more than minutes to run, making per-clause synchronous evaluation untenable. Would require async evaluator scheduling. |
| A-2 | Evaluators are well-behaved (don't fork, don't escape sandbox, don't mutate state outside their declared output). | Falsifies if real-world evaluator bindings prove leaky. Runner would need stronger sandboxing per evaluator. |
| A-3 | The grid is small enough that derive-arrow-status can run per clause-status-change without performance issue. | Falsifies if a project has thousands of arrows × clauses and recomputation becomes expensive; would require incremental status derivation. |
| A-4 | A single runner process per project is sufficient (no distributed coordination needed beyond the local lock). | Falsifies if teams need ghyll to coordinate across machines. The current schema's `op-id` field anticipates multi-operator but the runner assumes single-machine. |

---

## Open questions

- **Evaluator caching.** The same evaluator-arguments may be invoked
  repeatedly across passes if the upstream artifact hasn't changed.
  Should results be cached by content-hash of inputs? Could save
  significant time on `mutation-score` re-runs. Schema currently
  silent.
- **Evaluator parallelism.** Should multiple clauses in a single
  pass be evaluated in parallel? Currently the spec assumes
  sequential within a pass. Parallel evaluation would be safe (the
  per-clause lock prevents conflicts) but adds complexity.
- **Streaming vs batch results.** Some evaluators (e.g., a long
  mutation run) produce intermediate progress. Should the runner
  surface progress to the operator, or only the terminal result?
  Streaming requires evaluator-protocol support; not yet specified.
- **Refusal-with-context.** Currently `transition-refused` errors
  include `blocking-clauses` (the failing clause ids). Should they
  also include a suggested next step (e.g., "run pass P3 on arrow
  A to re-evaluate")? Out of scope for v1; could be a tooling
  layer.
