# Component: pass persistence + crash recovery

Refines `state-machine.md` F-4 (pass lifecycle) and F-6 (snapshot
and replay) by specifying *where* pass state lives, *when* it gets
written, and *what crash recovery does* when the harness restarts
mid-pass.

> Status: design intent (analyst pass).
> Predecessor: `state-machine.md` (general lifecycle).
> Architect: needs an ADR for the JSONL-source-of-truth inversion
> (amends or supersedes ADR-010).

---

## Scope

**In scope.**

- Persistence of the `Pass` entity to the v2 engine sqlite store
  (a new `passes` table alongside `findings`, `grid_arrows`,
  `amendments`, `attestations`, `evaluation_runs`).
- Pass lifecycle observers that feed the existing Journal goroutine
  fanout.
- Replay-on-startup behaviour: which passes come back, which get
  marked `aborted:crash`, which are preserved.
- Reconciliation of attestation-record-already-written but
  clause-status-not-flipped after a crash.
- Reconciliation of partial / torn checkpoint-log records.
- Operator-facing queries: `/passes` listing live + recent
  historical, query-by-id for a specific historical pass.

**Out of scope.**

- The Pass entity definition itself — `runner.Pass`,
  `runner.PassRegistry`, `runner.PassOptions` already exist
  (`runner/pass.go`, `runner/projectstatus.go:62`). This spec
  adds the persistence boundary, not the lifecycle.
- The amendment-lock recovery story for "lock held by a process
  that crashed" (`amendment.feature:124-132`) — the engine's
  lockfile is the OS-level construct; recovery happens via stale-
  lockfile detection in `cmd/ghyll/session.go`, separate concern.
- Distributed multi-machine engines (out of v1 per
  `state-machine.md:316-318`).

---

## Domain model

| Term | Definition |
|---|---|
| **Pass record** | The persistent shape of a `Pass`: `pass_id`, `role`, `context`, `arrow_id`, `state`, `opened_at`, `closed_at`, `close_reason`, `grid_version`. State ∈ {`open`, `closed`, `aborted`}. One row per `pass_id`. |
| **Live pass** | A row whose `state = 'open'`. Held in memory by `PassRegistry`; persisted on every state transition. |
| **Historical pass** | A row whose `state ∈ {'closed', 'aborted'}`. Not held in memory; readable via the engine table. |
| **Orphan pass** | A row found at startup with `state = 'open'` and no live process owns it. Crash recovery is the only path that observes this. |
| **Attestation-pending pass** | An orphan pass whose `arrow_id` has at least one clause with a published-but-unanswered hint on the operator bus (an attestation request without a corresponding record in the attestation table). |
| **Recovery event** | A typed `OperatorEvent` emitted during replay for every reconciliation action (mark-aborted-crash, mark-aborted-attestation-recovered, status-flip-from-jsonl). Audit trail for the operator. |

---

## Invariants

1. **Pass state is persisted on every transition.** `OpenPass`
   inserts; `Close`/`Abort` UPDATE the same row. The observer fires
   AFTER the in-memory state mutation, BEFORE the lock token is
   released, so a crash between mutate-and-release cannot lose
   the transition — the next state read sees the persisted value.
2. **JSONL is the source of truth for attestations.** **Amends
   ADR-010.** The attestation JSONL file is written *first* (with
   fsync) and is the authoritative record. The engine `attestations`
   table is a derived cache rebuilt on replay from the JSONL plus
   any operator-fed late corrections. A clause-status transition
   that follows an attestation MUST land only AFTER the JSONL
   line is fsync-durable.
3. **Crash recovery is deterministic and idempotent.** Running the
   recovery pass twice on the same store produces the same
   end-state. Recovery is read-then-write: read the engine + JSONL,
   compute the reconciliation set, apply transitions in batch,
   write recovery events.
4. **Attestation-pending passes survive crash recovery.** A pass
   with state `open` AND at least one of its clauses has a
   published-but-unanswered attestation request stays `open` after
   recovery. The hint is re-published on the bus so a reconnecting
   UI can render it. The pass's `recovered_at` timestamp is
   recorded for audit.
5. **All other open passes become `aborted` with reason
   `crash`.** Recovery sets `closed_at = recovered_at`,
   `close_reason = "crash"`. Findings raised under the pass keep
   their `grid_version` tag (already an invariant of the existing
   `FindingsStore`).
6. **Torn checkpoint records are detected and rolled back.** If the
   sqlite-level row update was partial (sqlite WAL guarantees this
   is rare but possible at the JSONL-engine seam), the recovery
   pass detects the inconsistency by hashing the row + comparing
   to the journal record's hash. Mismatched rows are rolled back
   to the last verified state; the affected pass is re-marked
   `aborted:crash`.
7. **`Query historical pass` is a read, not a reconstruction.** A
   historical pass-id query SELECTs from the engine table; if the
   row exists, return it. If not, return `not-found`. No
   reconstruction from clause/finding rows — those are *part of*
   the pass's surface but the pass row itself is the entry point.

---

## State machines (cross-reference)

The Pass state machine in `gates.md` §7.1a defines `running` →
`completed`, `running` → `aborted`. This spec adds the persistence
shadow: a pass row exists from the moment `OpenPass` returns and
mutates on every subsequent transition. The runtime states
(`PassStateOpen` / `PassStateClosed` / `PassStateAborted` in
`runner/pass.go:14-18`) map 1:1 to the column values
`'open'` / `'closed'` / `'aborted'`.

Recovery introduces no new states — it just transitions orphan
`open` rows to `aborted` (most) or leaves them `open` (attestation-
pending).

---

## Behaviors (features)

### F-1: Pass open + close emits journal records

```gherkin
Scenario: OpenPass persists a row
  Given a fresh engine store
  When the runner calls OpenPass(P1, analyst, contextA, A1)
  Then the engine `passes` table has exactly one row keyed by P1
  And its state is `open`
  And its opened_at is set
  And its closed_at is the zero string

Scenario: Close transitions the row
  Given pass P1 is `open` in the engine
  When the runner calls P1.Close("derived-complete")
  Then the engine row for P1 has state = `closed`
  And closed_at is set
  And close_reason = "derived-complete"

Scenario: Abort transitions the row
  Given pass P1 is `open` in the engine
  When the runner calls P1.Abort("amendment drained: missing-cross-context-spec")
  Then the engine row for P1 has state = `aborted`
  And close_reason carries the abort message
```

### F-2: Restart loads historical passes; orphans become aborted:crash

```gherkin
Scenario: Restart with no open passes
  Given the engine store has 3 closed passes and 2 aborted passes
  When the harness restarts and engine.Replay runs
  Then the in-memory PassRegistry is empty
  And the 5 historical rows remain queryable

Scenario: Restart aborts orphan open passes
  Given the engine store has 1 open pass P1 with no live process
  And P1's arrow has no awaiting-attestation clauses
  When engine.Replay runs
  Then P1 is transitioned to `aborted` with reason `crash`
  And an OperatorEvent of kind `recovery-pass-aborted-crash` is published
  And P1's row in the engine has state = `aborted`, close_reason = `crash`

Scenario: Restart preserves attestation-pending open passes
  Given the engine store has 1 open pass P1
  And P1's arrow has clause C5 with an attestation request that has no
      corresponding record in the attestation table
  When engine.Replay runs
  Then P1's state remains `open`
  And the attestation request is re-published on the OperatorBus
  And an OperatorEvent of kind `recovery-attestation-republished` is
      published with P1's pass-id and C5's clause-id
  And P1's `recovered_at` is set
```

### F-3: Query historical pass

```gherkin
Scenario: Query a closed pass
  Given the engine has a closed pass P5 (close_reason "derived-complete")
  When the operator queries `/passes P5`
  Then the response includes P5's role, context, arrow_id, opened_at,
      closed_at, close_reason, and grid_version

Scenario: Query a never-existed pass
  Given the engine has no row keyed by P-missing
  When the operator queries `/passes P-missing`
  Then the response is `not-found`
  And the engine does not attempt reconstruction from clause / finding rows
```

### F-4: Crash between attestation-record and clause-status flip

```gherkin
Scenario: JSONL has the verdict, clause status hasn't transitioned
  Given the JSONL file at .ghyll/attestations.jsonl has a record:
        attestation_id=att-1, clause_id=C5, verdict=pass, timestamp=T
  And the engine `attestations` table has no row for att-1
  And the engine `evaluation_runs` table has C5 with end_status=running
  When engine.Replay runs
  Then the engine attestations table gets a row for att-1
      (engine table catches up from JSONL — JSONL is source of truth)
  And the clause C5's end_status is reconciled to `pass`
  And an OperatorEvent of kind `recovery-attestation-replay` is published
      with att-1, C5
  And no `split-brain` persists (JSONL says pass; engine + runtime say pass)
```

### F-5: Crash mid-checkpoint write

```gherkin
Scenario: Engine row write partial (hash mismatch)
  Given the engine `passes` table has a row for P1 whose
        journal-derived hash does not match the in-row content
  When engine.Replay verifies the row
  Then the row is rolled back to the last verified state
  And if no verified state exists, the row is re-marked `aborted` with
      reason `crash`
  And an OperatorEvent of kind `recovery-torn-row-rollback` is published
      with P1's pass-id
```

### F-6: Per-pass checkpoint emission (runner.feature wiring)

```gherkin
Scenario: Pass completes and emits checkpoint
  Given pass P1 has reached arrow status `complete`
  When the dispatcher calls P1.Close
  Then the engine row for P1 is updated with state=closed, close_reason=derived-complete
  And an OperatorEvent of kind `pass-closed` carries the close metadata
  And subsequent restarts find P1 in the closed-passes history

Scenario: Pass aborted records reason in checkpoint
  Given pass P1 has been aborted with reason "amendment drained: ..."
  Then the engine row for P1 has state=aborted, close_reason carrying the abort message
  And subsequent restarts find P1 in the aborted-passes history with the same reason
```

---

## Failure modes

| ID | Failure | Blast radius | Intended degradation |
|---|---|---|---|
| FM-1 | sqlite write fails on `INSERT INTO passes` during `OpenPass` | Single pass | OpenPass returns the sqlite error wrapped in `ErrPassPersistFailed`. The in-memory `Pass` is NOT created; the lock token is NOT acquired (the persistence write happens BEFORE the registry registration, so a failed write doesn't leave the lock orphaned). |
| FM-2 | sqlite write fails on `UPDATE passes` during `Close`/`Abort` | Single pass | The in-memory state still transitions (the lock token releases); a journal-write-error event fires on the bus. The next restart will see the stale row and either roll it forward (closed/aborted in JSONL or finding-store) or treat it as orphan-crash. |
| FM-3 | JSONL fsync succeeds, engine `INSERT INTO attestations` fails | Single attestation | JSONL is now the source of truth (invariant 2); the engine table is a cache. Replay reconstructs the engine row from the JSONL on next start. A `recovery-attestation-replay` event fires. |
| FM-4 | Engine + JSONL agree, but `evaluation_runs.end_status` still says `running` after a crash | Single clause | Recovery's F-4 reconciliation reads the JSONL verdict, applies it to the clause via `Runner.transitionClause`, and fires `recovery-attestation-replay`. |
| FM-5 | Two restart processes try to run recovery concurrently | Whole project | The session lockfile (`cmd/ghyll/session.go`) holds the single-active-session invariant. A second process refuses to start. |
| FM-6 | Recovery itself crashes mid-reconciliation | Variable | Recovery is idempotent (invariant 3); the next start re-runs it. Partial state is acceptable because every recovery action is a deterministic function of the persisted store + JSONL state. |
| FM-7 | An operator-published attestation request was on the bus but never persisted (purely in-memory) | Single clause | Lost. The hint cannot be re-published on restart because the bus is volatile. Recovery treats the pass as plain orphan and aborts with reason `crash`. *Mitigation: the operator UI should treat bus events as advisory; only persisted attestation requests survive a crash.* |

FM-7 is the load-bearing one for the user direction "preserve attestation-pending passes". The current bus is volatile in-memory; for crash recovery to honor invariant 4, attestation **requests** (not just records) need a persistence path. Flagged for architect.

---

## Cross-component interactions

- **Engine ← PassRegistry**. Existing registry's mutation surface
  (`Register` / `Unregister`) needs an observer hook so persistence
  fires on every transition. Today `Register` is the only mutation
  call; the spec requires every state mutation to journal, so the
  observer wire-up is at the `Pass.closeWith` boundary
  (`runner/pass.go:150-169`) — that's where state actually
  transitions.
- **Engine.Replay ← passes table**. Replay loads in the order:
  attestations (already first per ADR-010) → grid arrows → 
  classifications → findings → amendments → **passes**. The
  passes step runs after findings because attestation-pending
  detection consults the engine attestation rows (rebuilt from
  JSONL) and the clause-status rows from `evaluation_runs`.
- **Runner ← engine.Pass query**. `runner.PassDispatcher` needs a
  way to ask "is the (role, context, arrow) tuple in use by an
  existing live pass?" today. This works through
  `RoleContextLockTable` already. Persistence does not change
  that — the lock table is the authoritative live-pass mechanism;
  the engine table is the persistence shadow.
- **OperatorBus → recovery events**. Four new event kinds:
  `recovery-pass-aborted-crash`, `recovery-attestation-republished`,
  `recovery-attestation-replay`, `recovery-torn-row-rollback`.
  Future operator UI subscribes to these to render a recovery banner.

---

## Assumptions

| ID | Assumption | Falsifiability |
|---|---|---|
| A-1 | Pass throughput is low enough that per-transition sqlite writes don't bottleneck. | Falsifies if a fan-out test produces thousands of simultaneous passes per second. Current invariant is one operator per repo → low ceiling. |
| A-2 | The `attestations` JSONL file is durable on its host filesystem (fsync semantics work). | Falsifies on NFS or other filesystems that don't honor fsync. Operators on such filesystems lose the inversion's guarantees. |
| A-3 | Attestation requests can be persisted (FM-7's mitigation). | Falsifies if the substrate continues to publish attestation requests only on the volatile bus. Forced into FM-7's degraded behavior until addressed. **Flagged for architect.** |
| A-4 | The Journal goroutine handoff pattern that existing entities use composes naturally with `Pass`. | Falsifies if Pass needs synchronous persistence (e.g., the dispatcher must know the row is durable before returning). If so, the persistence path becomes blocking. |
| A-5 | Recovery's "scan JSONL → catch up engine" is fast enough at session start. | Falsifies on multi-GB JSONL files. v1 has no projects this big; v2 inherits the assumption. |

---

## Open questions

- **Attestation-request persistence (A-3)**. Without it, FM-7
  forces the crash-with-awaiting-attestation flow to degrade.
  Architect call: persist on an `attestation_requests` table
  (mirror of `attestations` but with a `resolved_at` column), or
  defer this scenario class as a known degraded behavior in v1?
- **Recovery vs. attestation-flow ordering at replay**. Recovery
  scans for attestation-pending passes by joining `passes` ⋈
  `evaluation_runs` ⋈ `attestations`. The order in `Replay`
  matters: attestations are loaded first (ADR-010), then grid,
  then findings, then passes. The recovery scan happens *after*
  passes load. Architect should confirm this order is robust to
  partial JSONL.
- **JSONL-source-of-truth inversion blast radius**. Invariant 2
  amends ADR-010. Other observers (engine status CLI, future
  audit-verify tool) may currently assume engine table is
  authoritative. Architect to enumerate consumers and update.
- **`evaluation_runs.end_status` recovery**. F-4 reconciles by
  flipping `end_status` from `running` to the JSONL verdict.
  This is a write into a column today treated as "set once by
  the runner". Architect to confirm the column can carry
  recovery-source writes (or add a `recovery_status` shadow column).
- **Lock-file orphan recovery for amendments** (
  `amendment.feature:124-132`, out of scope per the Scope
  section but mentioned for completeness). Separate from this
  spec; belongs to the session-lifecycle component.

---

## Scenarios this spec covers

Once implemented, these `@deferred` scenarios should lift:

- `state-machine.feature`: Pass aborted by crash recovery
- `state-machine.feature`: Restart from checkpoint log
- `state-machine.feature`: Query historical pass
- `state-machine.feature`: Crash while clause is awaiting-attestation
- `state-machine.feature`: Crash between attestation write and clause-status flip
- `state-machine.feature`: Crash mid checkpoint-log write
- `runner.feature`: Pass completes and emits checkpoint
- `runner.feature`: Pass aborted records reason in checkpoint

= 8 of the remaining 48 `@deferred` scenarios.

---

## Handoff to architect

Architect picks up:

1. **ADR for invariant 2** (JSONL-source-of-truth inversion).
   Either amends ADR-010 or supersedes with ADR-015. Must
   enumerate downstream consumers + migration.
2. **Schema**: `passes` table columns + indexes. Pattern matches
   existing `findings` / `grid_arrows` shapes.
3. **Observer wire-up**: where `Pass.closeWith` calls a journal-bound
   observer; the existing engine `Journal` goroutine fanout pattern.
4. **Replay ordering**: where the passes load step slots into
   `engine/replay.go:Replay`.
5. **Recovery component**: a new file or extension of `replay.go`
   that runs the F-2 / F-4 / F-5 reconciliation logic.
6. **Decision on A-3** (attestation-request persistence) before the
   adversary review.
7. **Decision on the `evaluation_runs.end_status` recovery write**.
