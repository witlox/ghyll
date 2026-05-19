# Cross-context interactions

How the seven components of v2 ghyll interact. Consolidated from
each component spec's "Cross-component interactions" section. This
document is the *map*; the per-component specs hold the detail.

The components, named here:

1. **Catalogue** — owns per-concept schemas, language bindings.
2. **Init** — owns the v0→v1 bootstrap flow.
3. **State Machine** — owns the four lifecycles (clause / arrow /
   finding / pass), per-clause transition lock, in-memory store.
4. **Runner** — owns evaluator invocation, per-(role, context) lock,
   transition refusal.
5. **Adversarial** — owns the per-arrow adversarial phase.
6. **Amendment** — owns grid versioning, the project-wide write-lock,
   the FIFO amendment queue.
7. **Attestation** — owns operator sessions, hint presentation,
   verdict capture, the operator event bus, JSONL attestation files.

Plus two shared substrates (not components, but referenced by many):

- **Checkpoint log** (Merkle DAG, reused from v1 infrastructure).
- **Operator event bus** (a typed pub-sub, owned by Attestation but
  consumed by all).

---

## Interaction graph

```
                          ┌──────────────────┐
                          │                  │
                          │   Catalogue      │
                          │ (concepts +      │
                          │  bindings)       │
                          │                  │
                          └─────┬─────▲──────┘
                       schemas  │     │ binding lookup
                                ▼     │
        ┌───────────┐      ┌────────────────┐
        │           │ grid │                │
        │  Init     │─────▶│    Runner      │◀──── transition request
        │           │      │ (enforcement   │      from any role
        └─────▲─────┘      │  spine)        │
              │            └──┬──┬────┬─────┘
   re-init    │               │  │    │
   on missing │               │  │    │ hint+verdict request
   binding    │           runs│  │    │
              │           eval│  │    └──────────▶ ┌───────────────┐
              │               │  │                 │               │
              │               ▼  ▼ ◀────── verdict │ Attestation   │
              │            ┌────────────┐          │ (op sessions, │
              │            │            │          │  event bus,   │
              │            │   State    │◀─────────│  JSONL log)   │
              │            │   Machine  │          │               │
              │            │ (4 SMs +   │          └─────▲──┬──────┘
              │            │  per-clause│ accepted-      │  │
              │            │  lock +    │ risk proposal  │  │
              │            │  in-mem    │          ┌─────┘  │  events
              │            │  store)    │          │        │  to all
              │            └─▲────▲─────┘          │        ▼
              │              │    │           ┌────┴───────────┐
              │              │    │ findings  │                │
              │              │    └───────────│  Adversarial   │
              │              │ invalidated    │  (3 phases +   │
              │              │ status         │   adversary    │
              │              │                │   identity)    │
              │              │                └────▲───────────┘
              │     ┌────────┴─────────┐           │ start signal
              │     │                  │           │ from Runner
              │     │   Amendment      │           │
              │     │ (write-lock +    │           │
              │     │  FIFO queue +    │           │
              │     │  grid files)     │           │
              │     └──────────────────┘           │
              │                                    │
              └────────────────────────────────────┘
                         end-of-init takes the lock
                         (Amendment's lock; Init holds for v1 write)


    ╔═════════════════════════════════════════════════════╗
    ║         Operator Event Bus (owned by Attestation;    ║
    ║         all components publish events to it)         ║
    ╚═════════════════════════════════════════════════════╝

    ╔═════════════════════════════════════════════════════╗
    ║         Checkpoint Log (Merkle DAG; State Machine    ║
    ║         and Amendment write; State Machine reads     ║
    ║         on recovery)                                 ║
    ╚═════════════════════════════════════════════════════╝
```

---

## Each component's interfaces

### Catalogue

**Reads from disk** at startup: `gates/concepts/*.yaml` shipped with
the harness.

**Reads from grid:** `language-bindings` map (set at init).

**Consumed by:**

- **Init** — reads concept schemas to validate operator modifications
  (raise-only checks, argument type validation).
- **Runner** — looks up concept schemas to know which evaluator to
  invoke; combines with language binding to get the concrete command.
- **Adversarial** — references concepts for clause-falsification
  attempts (knows which arguments each concept takes).

**Produces no events.** Purely read-only at runtime.

### Init

**Inputs:**

- `gates/concepts/*.yaml` from Catalogue.
- `roles/*.md` from disk (the role-file templates).
- Operator inputs via the event bus (declarations, verdicts).
- Repo state (file tree, languages present, prior grid if any).

**Outputs:**

- `.ghyll/grid.v1.yaml` + `.ghyll/grid.current = "v1"`. Atomic write.
- Init arrow's exit-gate clause statuses → State Machine.
- Init arrow's attestation records → Attestation file at
  `attestations/v1/_/_/init__analyst/<pass-id>.jsonl`.

**Interactions:**

- **→ State Machine:** records init-arrow's clause statuses,
  pass-status. State Machine treats the init arrow like any other
  arrow.
- **→ Attestation:** init's attested clauses go through the standard
  attestation flow. `op-id` is declared via the event bus at session
  start.
- **↔ Catalogue:** reads concept schemas (validation), reads default
  costs (for residue R computation later).
- **→ Adversarial:** init's own arrow has an adversarial phase (it
  carries depth-sensitive clauses about depth-type assignments and
  residue honesty). Adversary identity is `adversary` per `gates.md`
  §1.1.
- **→ Amendment:** init takes the project-wide grid write-lock at
  end-of-init to write v1. Uncontested at that point (no other arrow
  has run; the queue is empty).
- **← Runner:** re-init triggered when Runner detects a missing
  binding mid-evaluation. Re-init is scoped to the missing binding
  only; the rest of the grid is preserved.

### State Machine

**Owns:**

- In-memory store of all `running` passes' clause statuses, finding
  states, pass attributes.
- Per-clause transition lock.

**Interactions:**

- **← Runner:** reports per-clause status changes (running, pass,
  fail, etc.). Validates transitions; rejects illegal ones.
- **← Attestation:** operator verdicts → clause transitions
  (pass / fail / insufficient-basis). Attestation flow does NOT
  directly set arrow status — only clause status.
- **← Adversarial:** findings raised (creation transitions);
  re-attacks produce finding transitions (open → resolved).
- **← Amendment:** sets `invalidated` on affected arrows
  (the only externally-set arrow status; see §7.2).
- **→ Runner:** Runner queries arrow status before permitting
  downstream role transitions. Derivation is pure (clause + finding
  state → arrow status per the §7.2 precedence).
- **→ UI / tooling:** external readers query project status, arrow
  status, pass-by-id.
- **↔ Checkpoint log:** reads on boot recovery (reconstructs running
  pass state, marks orphans as `aborted: crash`); writes on pass
  finalization.

**Boot recovery order:** State Machine recovers *first* on harness
restart, before Amendment or Runner. Reads the checkpoint log to
reconstruct, marks any orphan `running` passes as `aborted` with
reason `crash`, prepares the in-memory store.

### Runner

**Owns:**

- Per-(role, context) lock (enforces single-active-role-instance at
  pre-spawn).
- Evaluator process spawning / lifecycle.
- Transition-refusal contract.

**Interactions:**

- **← Init:** reads the grid file via `grid.current` to know which
  arrows exist and which clauses each carries.
- **↔ Catalogue:** for each machine clause, looks up the concept
  schema and the language binding to get the concrete evaluator
  command.
- **↔ State Machine:** reports per-clause status; queries arrow
  status before permitting transitions.
- **↔ Attestation:** hands off attested clauses (forwards hints,
  receives verdicts).
- **↔ Adversarial:** signals "arrow ready for adversarial" when
  upstream emits its artifact; receives "begin verification" signal
  when remediation converges. Runner serves as the verification phase
  of the §11 three-phase flow.
- **↔ Amendment:** Amendment signals the runner to abort affected
  passes; runner halts evaluation runs in flight, records pass-status
  `aborted` with reason `invalidated`.
- **→ Checkpoint log:** emits at pass conclusion (state machine
  handles the actual write to the log).

**Boot order:** Runner is *third* (after State Machine and
Amendment). Ready to accept new pass starts once the prior two are
reconciled.

### Adversarial

**Owns:**

- The per-arrow adversarial orchestrator.
- The fresh-context spawn discipline (each remediation round = new
  adversary instance, clean context).
- The remediation loop bounded by `remediation-rounds-max`.

**Interactions:**

- **← Runner:** signal "arrow ready for adversarial" once upstream
  emits its artifact and clauses are loaded.
- **→ State Machine:** registers findings (creation); records
  re-attack transitions (`open` → `resolved` if a fresh adversary
  cannot reproduce).
- **↔ Producer role (upstream):** notifies the producer of findings
  (so the producer can remediate); receives typed messages:
  `producer-fix-signal` and `accepted-risk-proposal`.
- **↔ Attestation:** accepted-risk proposals route to Attestation;
  operator verdicts come back.
- **→ Runner (verification):** when convergence is reached, signals
  the Runner to start the verification phase. Runner auto-inserts
  `no-open-finding` and `every-requirement-meets-min-depth`.
- **← Amendment:** if an amendment lands mid-phase, Amendment signals
  Adversarial to halt the current round per D22 (mid-phase abort).
  Re-attack starts again only after the next pass on the re-traversed
  arrow.

### Amendment

**Owns:**

- The project-wide grid write-lock.
- The FIFO amendment queue.
- The grid files on disk (`.ghyll/grid.v<N>.yaml`,
  `.ghyll/grid.current`).

**Interactions:**

- **← Integrator (role):** triggers amendments via
  `missing-cross-context-spec` finding type. The amendment component
  enqueues.
- **← Analyst (re-engaged):** produces the amended spec; Amendment
  receives the new arrow grid proposal.
- **→ State Machine:** calls the engine to set `invalidated` on
  affected arrows.
- **→ Runner:** signals affected `running` passes to abort. Findings
  on those passes are preserved with their `grid-version` tag (D22).
- **↔ Checkpoint log:** each commit produces a log entry: the v(N+1)
  snapshot, the affected arrows, the triggering integrator finding.
- **↔ Grid files:** atomic write of `grid.v(N+1).yaml` + `grid.current`
  per D31. Fsync ordering enforced (content durable before rename
  visible; rename durable before pointer update).
- **↔ Init:** Init takes Amendment's lock at end-of-init (D35) for
  the v1 write.

**Boot order:** Amendment recovers *second* (after State Machine,
before Runner). On boot: validate `grid.current` matches an existing
file; alert on divergence; clean up orphaned temp files; release any
crashed-while-held lock; require operator decision on grid integrity
issues before Runner becomes ready.

### Attestation

**Owns:**

- Operator sessions (`op-id` lifecycle).
- Hint presentation.
- Verdict capture and JSONL append.
- The operator event bus (all event types pub-sub here).
- The on-disk attestation files at
  `attestations/v<N>/<context>/stratum-<S>/<role-pair>/<pass-id>.jsonl`.

**Interactions:**

- **← Runner:** Runner forwards hints for attested clauses; requests
  verdicts.
- **← Adversarial:** Adversarial forwards accepted-risk proposals for
  findings.
- **→ State Machine:** operator verdicts become *clause* transitions
  (and *finding* transitions for accepted-risk on findings). Never
  arrow-status transitions directly.
- **→ Runner:** after a verdict, signals Runner to resume the pass
  (Runner recomputes arrow status via State Machine).
- **↔ Operator UI:** Attestation is the data provider; the UI
  consumes events on the bus and surfaces them to the operator. The
  UI is implementation; the contract is the event-bus message
  shapes.
- **↔ Attestation files on disk:** owns the per-pass JSONL files.
  Append-only. Fsync before verdict-acceptance is reported.

---

## Shared substrates

### Operator event bus

Owned by Attestation. All other components publish events to it.
Subscribers (typically the operator UI, plus tooling like the
verifier component) consume.

Event types (from various component specs + D43):

| Event | Publisher | When |
|---|---|---|
| `attestation-request` | Runner / Attestation | Attested clause needs a verdict |
| `escalation-request` | Attestation | `insufficient-basis-rounds-max` reached on a clause |
| `refusal-prompt` | Init | Low-risk project profile detected |
| `pass-aborted` | Runner / State Machine | Pass terminated with non-completed status |
| `amendment-conflict` | Amendment | Grid integrity issue detected on boot |
| `amendment-queue-growing` | Amendment | Queue length above threshold (default 10) |
| `init-prompt` | Init | Operator input needed during init |
| `transition-refused-invalidated` | Runner | Downstream attempted to enter an invalidated arrow |
| `producer-signal-without-change` | Adversarial | Loop-bomb detected (FM-37) |
| `producer-no-response` | Runner / Adversarial | Producer hint-request timeout |
| `attestation-cancelled-by-abort` | Attestation | Pending attestations on an aborted pass |
| `recovery-event` | State Machine | Crash recovery reconciled state from log |

The event bus shape is small and stable; each event type has a
typed payload. Adding new event types requires updating the schema.

### Checkpoint log

Owned (write side) by State Machine and Amendment. Both append
records:

- State Machine: per-pass finalization records (the full pass
  state).
- Amendment: per-amendment commit records (the v(N+1) snapshot,
  affected arrows, triggering finding).

Reused from v1 Merkle DAG infrastructure (`memory/store.go`,
`memory/crypto.go`, `memory/sync.go`).

State Machine **reads** the log on boot recovery; other components
do not read directly.

---

## Cross-component invariants

Cross-references invariants in `invariants.md`:

| Invariant | Crosses |
|---|---|
| 1. Diamond is 4 roles | Init (auto-propose); Runner (transition refusal) |
| 3. Init mandatory before any other arrow | Init; Runner (refuses pre-init transitions); Amendment (queue empty at init) |
| 5. Init runs as a normal arrow | Init; State Machine; Attestation; Adversarial |
| 7. Arrow identity is structural | State Machine (lookups); Amendment (version bump); Runner (refusal) |
| 19. Derivation is pure | State Machine (own); Attestation (does NOT bypass) |
| 22. Catalogue closed at concept layer | Catalogue (own); Init (validates against); Runner (looks up) |
| 27. Producers cannot self-certify | Attestation (enforces); Adversarial (producer ≠ adversary) |
| 28. Producers cannot accept own risk | Adversarial (forwards proposal); Attestation (requires operator op-id) |
| 30. Fresh adversary per remediation round | Adversarial (own); enforced by spawn discipline, not by inter-component contract |
| 34. op-id required for every attestation | Attestation (enforces); Init (declares at session start) |
| 42–44. Three locks, three owners | State Machine; Runner; Amendment |
| 45. Boot recovery order is fixed | State Machine first → Amendment second → Runner third |
| 47. Atomic grid write | Amendment (own); Init (writes v1 with same atomicity) |
| 51. Total refusal | Runner (enforces); UI/tooling cannot override |

---

## Concurrency-safe interactions

Where components could race, the schema specifies the resolution:

| Race | Resolution |
|---|---|
| Two clause-status updates same `(pass-id, clause-id)` | State Machine's per-clause transition lock |
| Two passes attempt same `(role, bounded-context)` | Runner's per-(role, context) lock; second is refused with `single-active-role-violation` |
| Two amendments commit concurrently | Amendment's project-wide write-lock; FIFO queue |
| Reader observing `grid.current` during update | POSIX rename atomicity; reader sees either old or new version, never torn |
| Attestation written but clause-status not yet flipped, then crash | State Machine boot recovery reads latest attestation and reconciles |
| Pass aborted, but operator has a pending attestation request | Attestation publishes `attestation-cancelled-by-abort`; UI clears |
| Crashed while holding amendment lock | Amendment boot recovery detects orphaned lock (dead PID), releases |
| Adversary running and amendment lands | Amendment signals Runner; Runner halts evaluators; Adversarial aborts mid-phase per D22 |

---

## Synchronous vs asynchronous

All component-to-component calls within a single ghyll process are
**synchronous in-process function calls** (per `init.md` A-4:
single-process per project).

The **operator event bus** is the *only* asynchronous channel.
Operators consume events at their own pace; the harness does not
block waiting for a specific operator to be present (it blocks only
on attestation-required clauses, which are by definition awaiting
operator action).

If v2 is ever distributed across machines, the synchronous-in-process
assumption breaks. Out of scope for v1.
