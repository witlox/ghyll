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
| **Attestation-pending pass** | An orphan pass with at least one `evaluation_runs` row matching `pass_id = P` AND `depth_type_attestation_ref != ''` AND `end_status = 'running'` AND no `attestations` row with `attestation_id = depth_type_attestation_ref`. (F-1 of validation-impl-pass-tier1.md: defined via the engine JOIN, not via the volatile `OperatorBus`; persistent attestation-request store is Tier 2.) |
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
   whose `evaluation_runs` × `attestations` JOIN identifies at
   least one clause as attestation-pending (per the definition
   above) stays `open` after recovery. Recovery calls
   `PassRegistry.Resume(rec, lockTable)` (F-3) to reconstitute
   the in-memory `*Pass` AND re-acquire the per-(role, context)
   lock token, so the dispatcher cannot race a duplicate pass on
   the same tuple. The hint is reconstructed from the
   evaluation_runs row (`arrow_id` + `clause_id` +
   `depth_type_attestation_ref`) and surfaced via
   `RecoveryReport.Events`, NOT via the volatile OperatorBus
   (F-18: bus has no subscribers at recovery time). The first
   preservation stamps `recovered_at`; subsequent recoveries
   skip the pass if `recovered_at != ''` (F-12 idempotence).
5. **All other open passes become `aborted` with reason
   `crash`.** Recovery sets `closed_at = recovered_at`,
   `close_reason = "crash"`. Findings raised under the pass keep
   their `grid_version` tag (already an invariant of the existing
   `FindingsStore`).
6. **JSONL torn-line handling is lenient; engine row torn-write
   detection is dropped.** Per F-6 / F-15: sqlite WAL provides
   row-level atomicity for engine writes; an additional row-hash
   verification was rejected as premature. The remaining torn-
   write surface is the JSONL: if `loadFromJSONL` encounters a
   truncated trailing line, it returns `(loaded int, truncated
   bool, err error)` where `truncated=true` means it stopped at
   the last complete record. Session.Open emits
   `OpEventAttestationAuditDurabilityFailed` with offset detail;
   on the next successful `Record`, the JSONL writer truncates
   the file at the last complete offset before appending. **The
   "Crash mid checkpoint-log write" deferred BDD scenario is
   retired** (see `state-machine.feature` change in this phase).
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
  And P1 has an evaluation_runs row for clause C5 with
      depth_type_attestation_ref="att-X-C5-v1", end_status="running"
  And no attestations row has attestation_id="att-X-C5-v1"
  When engine.Recovery runs
  Then P1's engine row state remains `open` and recovered_at is set
  And PassRegistry.Resume reconstitutes the in-memory *Pass + re-
      acquires the (role, context) lock token
  And RecoveryReport.Events contains a `recovery-attestation-republished`
      event with P1's pass-id, C5's clause-id, and att-X-C5-v1
  And session.Open surfaces RecoveryReport.Events to the operator
      (NOT via OperatorBus — bus has no subscribers at recovery time)
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

### F-5: JSONL trailing-line truncation handled leniently

```gherkin
Scenario: Recovery skips a truncated trailing JSONL line
  Given .ghyll/attestations.jsonl has N complete records followed by a
        partial record (no terminating newline)
  When attestations.loadFromJSONL is called at session.Open
  Then it returns (loaded=N, truncated=true, err=nil)
  And session.Open emits OpEventAttestationAuditDurabilityFailed
      with detail "trailing truncated line at offset M skipped"
  And on the next successful AttestationStore.Record, the writer
      truncates the file at offset M before appending
```

Engine row torn-write detection (originally invariant 6) is
**dropped per F-6 / F-15**: sqlite WAL provides row-level
atomicity; adding row-hash verification was rejected as premature.
The "Crash mid checkpoint-log write" deferred BDD scenario is
retired in `state-machine.feature` as part of this phase.

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
| FM-7 | An attestation request was in operator-bus-only state (no `depth_type_attestation_ref` written to evaluation_runs) when the crash hit | Single clause | The JOIN-based detection (see Domain model) finds nothing → pass falls to orphan-abort. Mitigation: every code path that publishes `OpEventAttestationRequested` MUST first write the `depth_type_attestation_ref` to the `evaluation_runs` row (existing `runner/dispatcher.go:221-234` flow already does this — the ref lands when Runner.Evaluate persists the run, before the operator's verdict). FM-7 reduces to "the dispatcher crashed BETWEEN evaluating the clause and persisting the run" — a single-clause loss window equivalent to losing any other partial write. |

FM-7 is bounded by the existing engine persistence boundary at `runner.Runner.Evaluate` → journal → `evaluation_runs` row. The JOIN-based detection (F-1 of validation-impl-pass-tier1.md) replaces the original bus-based detection.

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
| A-3 | The `depth_type_attestation_ref` on a clause is persisted via `evaluation_runs` BEFORE the volatile bus event fires. Validated against `runner/dispatcher.go:221-234` + journal handleRun: yes. Resolves FM-7 via the JOIN-based detection (F-1 of validation-impl-pass-tier1.md). |
| A-4 | The Journal goroutine handoff pattern that existing entities use composes naturally with `Pass`. | Falsifies if Pass needs synchronous persistence (e.g., the dispatcher must know the row is durable before returning). If so, the persistence path becomes blocking. |
| A-5 | Recovery's "scan JSONL → catch up engine" is fast enough at session start. | Falsifies on multi-GB JSONL files. v1 has no projects this big; v2 inherits the assumption. |

---

## Open questions

All resolved by the gate-1 adversary review (see
`specs/v2/validation-impl-pass-tier1.md`):

- ~~Attestation-request persistence (A-3)~~. Resolved via the
  JOIN-based detection over `evaluation_runs` × `attestations`
  (F-1 / Option A). A dedicated `attestation_requests` table is
  Tier 2 work, not blocking Tier 1.
- ~~Recovery vs. attestation-flow ordering at replay~~. Resolved
  in ADR-015 Part B: attestations → grid → reqs → classif →
  findings → amendments → passes → Recovery. Recovery refuses to
  run if `ReplayCounts.Errors` is non-empty (F-13 / fail-loud).
- ~~JSONL-source-of-truth inversion blast radius~~. Resolved in
  ADR-015 Part C; the four consumers (`session_engine.go`,
  `arrow_cmd.go`, `engine_cmd.go`, replay tests) are enumerated
  in `tier-1-pass-persistence-contracts.md` (F-8).
- ~~`evaluation_runs.end_status` recovery~~. Resolved: new
  `Store.UpdateEvaluationRunReconciled` + `recovery_source`
  column + schemaVersion bump to 3 + verdict→ClauseStatus
  mapping table (F-7).
- ~~Lock-file orphan recovery for amendments~~. Still out of
  scope here; belongs to the session-lifecycle component.

---

## Scenarios this spec covers

Once implemented, these `@deferred` scenarios should lift:

- `state-machine.feature`: Pass aborted by crash recovery
- `state-machine.feature`: Restart from checkpoint log
- `state-machine.feature`: Query historical pass
- `state-machine.feature`: Crash while clause is awaiting-attestation
- `state-machine.feature`: Crash between attestation write and clause-status flip
- `runner.feature`: Pass completes and emits checkpoint
- `runner.feature`: Pass aborted records reason in checkpoint

= **7** of the remaining 48 `@deferred` scenarios. (Was 8;
`state-machine.feature`'s "Crash mid checkpoint-log write" is
retired per F-6 / F-15 — sqlite WAL atomicity covers row writes;
JSONL truncation is handled leniently and surfaces as
`OpEventAttestationAuditDurabilityFailed`.)

---

## Handoff to architect → adversary → implementer

Analyst → architect → adversary done. All 18 adversary findings
remediated in this spec + `ADR-015` + the contracts doc. See
`specs/v2/validation-impl-pass-tier1.md` for the findings and
`specs/v2/validation-impl-pass-tier1-remediation.md` for the
disposition log.

Implementer picks up the contracts at
`specs/architecture/tier-1-pass-persistence-contracts.md`.
