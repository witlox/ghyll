# ADR-015: Pass entity persistence + JSONL becomes attestation source of truth

## Status

Accepted (2026-05-20).

Amends:

- **ADR-010** (`010-attestation-store-runner-engine-split.md`) —
  inverts the "engine table is source of truth" framing for
  attestations.
- **ADR-013** (`013-pass-entity-and-registry.md`) — adds the
  persistence boundary the original Pass ADR deferred.

## Context

The analyst pass on Tier 1 (see
`specs/architecture/components/pass-persistence.md`) surfaced three
gaps in the v2 engine:

1. `runner.Pass` is in-memory only. The engine has tables for
   findings, arrows, amendments, attestations, evaluation_runs —
   but **no `passes` table**. A crash mid-pass loses pass state.
2. Crash recovery semantics are under-specified. Today
   `state-machine.md` F-6 says "all running passes → aborted:crash";
   the deferred BDD scenarios (`state-machine.feature:204-233`)
   require finer behavior: passes with attestation-pending clauses
   should survive a crash so the operator can still deliver a
   verdict.
3. The atomicity boundary between "attestation JSONL append" and
   "engine attestations INSERT" is untested. ADR-010 says the
   engine row is the source of truth; the JSONL is a derived audit
   trail. Under crash between fsync and INSERT, the JSONL has the
   verdict but the engine + clause state don't — split-brain.

The operator decision in the analyst pass:

1. Persist Pass state in v2 engine sqlite (new `passes` table).
2. Crash recovery preserves passes with attestation-pending clauses;
   all other open passes become `aborted:crash`.
3. **JSONL becomes the source of truth for attestations.** The
   engine `attestations` table is a derived cache rebuilt at
   replay from the JSONL plus any operator-fed late corrections.

## Decision

### Part A: passes table

Add `passes` to the engine schema, observed by the existing Journal
goroutine fanout pattern:

```sql
CREATE TABLE IF NOT EXISTS passes (
    pass_id        TEXT PRIMARY KEY,
    role           TEXT NOT NULL,
    context        TEXT NOT NULL,
    arrow_id       TEXT NOT NULL,
    grid_version   INTEGER NOT NULL DEFAULT 0,
    state          TEXT NOT NULL CHECK (state IN ('open','closed','aborted')),
    opened_at      TEXT NOT NULL DEFAULT '',
    closed_at      TEXT NOT NULL DEFAULT '',
    close_reason   TEXT NOT NULL DEFAULT '',
    recovered_at   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_passes_state ON passes(state);
CREATE INDEX IF NOT EXISTS idx_passes_arrow ON passes(arrow_id);
CREATE INDEX IF NOT EXISTS idx_passes_role_ctx ON passes(role, context);
```

The columns map 1:1 onto `runner.Pass` plus a `recovered_at`
shadow column (set by crash-recovery to mark which rows survived
a restart via the attestation-pending exception, per
`pass-persistence.md` invariant 4).

`runner.Pass` gains an `Observe(fn PassObserver)` surface on
`PassRegistry`, mirroring the `FindingsStore.Observe` pattern.
`Journal.AttachPasses(*runner.PassRegistry)` wires the observer
to the existing event channel. The observer fires AFTER the
in-memory state mutation but BEFORE the lock token release inside
`Pass.closeWith` (`runner/pass.go:150-169`).

### Part B: replay ordering

`engine.Replay` order becomes:

```
1. attestations (rebuild from JSONL; not from engine table — see Part C)
2. grid arrows
3. requirements
4. classifications
5. findings
6. amendments
7. passes   ← NEW
8. recovery scan   ← NEW (Part D)
```

Passes load AFTER findings because the recovery scan in step 8
consults findings (to tag preserved passes' findings with
grid-version) and the attestation store (rebuilt in step 1) to
detect attestation-pending clauses.

### Part C: JSONL is source of truth for attestations

The current ADR-010 framing — "engine table is source of truth,
JSONL is derived audit trail" — is inverted:

- **Write order**: JSONL fsync first; engine INSERT second.
  `AttestationStore.Record` calls the JSONL observer
  *synchronously inside* the same critical section, before the
  store's `byID` map mutates and before any other observer fires.
- **Replay**: `engine.Replay` for attestations reads the JSONL
  file at session start, parses each line, and INSERTs into the
  engine `attestations` table (catch-up). The engine table is a
  cache, not a record-of-truth.
- **Recovery**: if the JSONL has a verdict but the engine has no
  corresponding row, the recovery scan inserts the missing row
  and reconciles `evaluation_runs.end_status` to match the
  verdict (Part D).
- **JSONL gone / unreadable**: hard error. `ghyll` refuses to start
  the session with `ErrAttestationAuditLost`. Operator must
  restore the file (or run `ghyll attestations rebuild --force`
  which is a separate operator escape hatch with explicit consent).

This inversion is the load-bearing change. The JSONL is already
fsync-durable (`runner/attestation_jsonl.go:46-94`); making it
authoritative removes the ordering question and aligns with the
"every JSONL line must outlive the engine" invariant the operator
spec implies.

### Part D: recovery component

New file: `engine/recovery.go`. Exported function:

```go
// Recovery scans the engine + JSONL state at session start, reconciles
// split-brain conditions, and emits one OperatorEvent per reconciliation
// action. Idempotent: running twice yields the same end-state.
//
// Order of operations:
//   1. orphanScan: open passes whose row exists but no live process.
//   2. attestationPendingScan: subset of orphans whose clauses await
//      attestation. These passes survive; their hints re-publish.
//   3. orphanAbort: remaining orphans → aborted:crash.
//   4. evaluationRunReconcile: clauses with end_status=running but
//      a JSONL verdict in the attestation store → flip status.
//   5. tornRowDetect: row hashes vs. journal hashes; mismatch → rollback.
//
// Returns RecoveryReport with per-step counts + emitted events.
func Recovery(ctx context.Context, store *Store, bus *runner.OperatorBus,
    targets ReplayTargets) (RecoveryReport, error)
```

The function is called once by `cmd/ghyll/session.go:engineRuntime.Open`
AFTER `engine.Replay` returns and BEFORE `attachJournal` subscribes
the live observers. This ordering ensures recovery's writes don't
re-journal back into the engine (the same invariant
`engine/replay.go:18-20` enforces for replay).

### Part E: attestation-request persistence (deferred)

The analyst flagged A-3 (attestation-request persistence) as an
open question. Without it, FM-7 in `pass-persistence.md` forces
attestation-pending passes that crashed before the request
persisted to degrade into `aborted:crash`.

**Decision**: defer to a follow-up ADR. The current operator bus
is volatile in-memory; making attestation requests durable is a
separate component (the existing `AttestationStore` is for
*records*, not *requests*). Tier 1 ships with FM-7's degraded
behavior; Tier 2 (operator session) revisits.

## Consequences

Positive:

- Pass state survives crashes; restart can resume operator-attention
  flows.
- Attestation records have a single source of truth (the JSONL on
  durable disk).
- Replay logic gets simpler: every entity has the same pattern
  (load from authoritative source → INSERT into engine cache).
- 8 of the remaining 48 `@deferred` BDD scenarios get a wirable
  substrate.

Negative:

- ADR-010 amended: downstream consumers reading attestation rows
  from the engine table need a note that the table is a derived
  cache.
- One extra sqlite write per Pass transition. Bounded by Pass
  throughput (≪ 100 transitions/second in single-operator flows).
- Recovery code path is new attack surface. Adversary pass MUST
  exercise: torn-row detection, JSONL-engine divergence under
  malformed input, recovery-during-recovery races, attestation-
  pending detection edge cases (no clauses / all clauses /
  cross-pass clauses).
- A-3 deferred. Operators relying on attestation-pending
  preservation see FM-7's degraded behavior if the request was
  in-flight when the crash happened.

## Alternatives considered

**Alt 1: Keep ADR-010 framing; persist Pass to memory checkpoint
chain.** Rejected — the v1 memory chain is for
operator-attestable session checkpoints, not high-frequency entity
mutations. Per-Pass-transition signed chain entries would blow up
chain length without operator value.

**Alt 2: Single atomic transaction wrapping attestation insert +
clause-status flip.** Rejected — couples the runner's clause
status path to the engine's transaction boundary; observer fanout
becomes synchronous and blocking. Today's pattern is
"observer-fires-then-journal-fans-out"; making it transactional
inverts that and breaks the existing FindingsStore / Grid /
AmendmentQueue uniformity.

**Alt 3: Defer pass persistence to v1.1.** Rejected — crash-loses-
state on a "correctness over speed" tool destroys trust. This is
the kind of bug ghyll is supposed to prevent in other people's
code, not exhibit in its own.

## Implementation seam

- `engine/store.go`: add `passes` to the CREATE TABLE block.
- `engine/records.go`: add `PassRecord` type + `UpsertPass`
  function.
- `runner/projectstatus.go` (`PassRegistry`): add `Observe` /
  `emit` mirroring `FindingsStore`.
- `runner/pass.go`: `OpenPass` calls observer with kind=open;
  `closeWith` calls observer with kind=closed/aborted.
- `engine/journal.go`: `AttachPasses` registers observer; `handlePass`
  routes to `UpsertPass`.
- `engine/replay.go`: passes load step; calls `Recovery` after.
- `engine/recovery.go`: new file, implements the five-step scan.
- `runner/attestationstore.go`: `Record` calls JSONL observer
  inline (already does for the auditor JSONL writer; lift the
  invariant to "MUST succeed before in-memory mutation").
- `cmd/ghyll/session_engine.go`: re-order to call `Recovery` after
  `Replay`, before `attachJournal`.

## References

- `specs/architecture/components/pass-persistence.md` (analyst
  output)
- `specs/architecture/components/state-machine.md` F-4, F-6
- `specs/features/state-machine.feature:120-233` (BDD scenarios)
- `specs/features/runner.feature:172-195` (per-pass checkpoint)
- ADR-010 (the inverted framing)
- ADR-013 (Pass entity, deferred persistence)
