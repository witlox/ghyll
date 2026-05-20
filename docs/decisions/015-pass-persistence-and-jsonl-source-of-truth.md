# ADR-015: Pass entity persistence + JSONL becomes attestation source of truth

## Status

Accepted (2026-05-20). Gate-1 adversarial review of this ADR
produced 18 findings; all remediated in-document. Disposition log:
`specs/v2/validation-impl-pass-tier1-remediation.md`.

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

-- Per F-7 / F-17 of the gate-1 review: end_status reconciliation
-- writes need provenance. ALTER on existing DBs, default '' so
-- runner-set rows are distinguishable from recovery-set rows.
ALTER TABLE evaluation_runs ADD COLUMN recovery_source TEXT NOT NULL DEFAULT '';
```

`schemaVersion` bumps from 2 to 3 (the `ALTER TABLE` is a real
migration; existing DBs need it applied once via
`ensureSchemaVersion`).

The columns map 1:1 onto `runner.Pass` plus a `recovered_at`
shadow column (set by crash-recovery to mark which rows survived
a restart via the attestation-pending exception, per
`pass-persistence.md` invariant 4). `recovered_at` is **set-once**
(F-12): Recovery skips passes where `recovered_at != ''`, so a
re-run of Recovery on the same store produces an empty
`RecoveryReport` (idempotence).

`runner.Pass` gains an `Observe(fn PassObserver)` surface on
`PassRegistry`. **Emit runs without acquiring any registry lock**
(F-4): the observer list is registered one-shot at session start
and never mutated after that, so unlocked fanout is safe and
breaks the AB/BA deadlock with `PassRegistry.All()` → `p.State()`.

The emit point in `Pass.closeWith` runs AFTER `lockToken.Release`
(F-4): closeWith captures the event payload while `p.mu` is held,
releases the mutex AND the lock token, THEN calls
`p.registry.emit(payload)` with no locks held. Lock ordering is
always `p.mu → release → emit`, never `p.mu → emit`. `OpenPass`
emits `PassEventOpen` after `PassRegistry.Register` returns (no
lock held at emit time).

To avoid duplicate audit (N-1, N-2): the existing
`OperatorEvent` publishes inside `OpenPass` / `closeWith` (kinds
`OpEventPassOpened` / `OpEventPassClosed`) are **removed**. The
`PassEvent` fanout via the registry is the single audit path;
the journal observer additionally bridges `PassEvent` to the
bus for downstream subscribers if the live session has any.

**Pass events are critical-priority on the journal channel**
(F-11): `Journal.enqueue` for `jKindPass` blocks indefinitely
rather than dropping after the 100ms budget. Invariant 1 ("Pass
state persisted on every transition") requires this — a dropped
pass event means the engine row never updates, producing a
correctness lie at next-restart Recovery.

**PassRegistry.Resume** (F-3) reconstitutes a `*runner.Pass`
from a persisted `PassRecord` and re-acquires the per-(role,
context) lock via `RoleContextLockTable.TryAcquire`. Recovery
calls Resume for every preserved attestation-pending pass so:

- `PassRegistry.All()` lists them (so `/passes` shows the
  preserved set).
- The dispatcher refuses to open a competing pass on the same
  tuple (lock is held).
- The operator's verdict path can `Resume(...).Close(...)` the
  pass cleanly when the answer arrives.

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
- **JSONL state semantics** (F-5):
  - **Missing JSONL AND engine has zero attestation rows** →
    fresh project, treat as empty stream. `loadFromJSONL`
    returns `(loaded=0, truncated=false, err=nil)`. The file
    is created on the first successful `Record`.
  - **Missing JSONL AND engine has attestation rows** →
    `ErrAttestationAuditLost`. Operator must restore or run
    `ghyll attestations rebuild --force` (separate escape
    hatch).
  - **JSONL exists but unreadable / corrupt header** →
    `ErrAttestationAuditLost`.
  - **JSONL exists but trailing line truncated** (F-6) →
    lenient mode. `loadFromJSONL` returns `(loaded int,
    truncated=true, err=nil)`, stops at the last complete
    record. Session.Open emits
    `OpEventAttestationAuditDurabilityFailed` with offset
    detail. The writer truncates the file at the last
    complete offset on the next successful `Record` so the
    bad bytes are overwritten cleanly.

- **JSONL observer special-casing for error channel** (N-5):
  `AttestationObserver func(event AttestationEvent)` has no
  error return; observers can't fail the `Record` call. To
  enforce "JSONL fsync MUST succeed before in-memory mutation"
  per invariant 2, the JSONL writer is the **first observer
  fired** and is called inline within `Record`'s critical
  section. If it fails, `Record` returns
  `ErrAttestationAuditWriteFailed` and the in-memory map
  is not mutated; other observers never fire. The
  `AttestationObserver` signature stays unchanged for backward
  compatibility; the JSONL writer is internally privileged.

This inversion is the load-bearing change. The JSONL is already
fsync-durable (`runner/attestation_jsonl.go:46-94`); making it
authoritative removes the ordering question and aligns with the
"every JSONL line must outlive the engine" invariant the operator
spec implies.

### Part D: recovery component

New file: `engine/recovery.go`. Recovery is bounded by a single
sqlite transaction (F-10) so concurrent read-only CLIs see
pre- or post-recovery state but never a torn mid-recovery
snapshot.

```go
// RecoveryDeps bundles the runner stores + injected deps. Distinct
// from ReplayTargets so Recovery's needs don't bleed into Replay's
// signature.
type RecoveryDeps struct {
    Store        *Store
    Passes       *runner.PassRegistry         // for Resume on preserved passes (F-3)
    Attestations *runner.AttestationStore     // for JOIN-based detection (F-1)
    LockTable    *runner.RoleContextLockTable // for Resume to reacquire locks
    IBTracker    *runner.InsufficientBasisTracker // may reset counters
    JSONLPath    string                       // for re-truncation per F-6
    Now          func() time.Time             // injection for F-12 idempotence
}

// Recovery scans the engine + JSONL state at session start,
// reconciles split-brain conditions, and returns a
// RecoveryReport whose Events list is the session.Open
// surfacing path (NOT the OperatorBus — F-18). Idempotent
// per F-12: skips passes where recovered_at != ''.
//
// REFUSES TO RUN if ReplayCounts.Errors is non-empty (F-13);
// returns ErrRecoveryReplayDirty so the operator gets a clear
// "previous start left malformed rows; investigate" message.
//
// Order of operations (all inside ONE BeginTx/Commit per F-10):
//   1. orphanScan: SELECT FROM passes WHERE state='open'.
//   2. attestationPendingScan: per-orphan, run the JOIN
//      defined in pass-persistence.md (evaluation_runs ⋈
//      attestations) to identify attestation-pending. Preserved
//      orphans get PassRegistry.Resume + recovered_at stamp;
//      RecoveryReport.Events appends recovery-attestation-
//      republished with hint payload from the evaluation_runs
//      row.
//   3. orphanAbort: remaining orphans → UPDATE passes SET
//      state='aborted', closed_at=now, close_reason='crash'.
//      RecoveryReport.Events appends recovery-pass-aborted-
//      crash per pass.
//   4. evaluationRunReconcile: for every (run, ref) where
//      run.end_status='running' AND ref has a JSONL verdict,
//      Store.UpdateEvaluationRunReconciled writes the new
//      end_status, recovery_source='recovery-attestation-replay',
//      and recovered_at. Verdict → ClauseStatus mapping (F-7):
//        attestation 'pass' → StatusPass
//        attestation 'fail' → StatusFail
//        attestation 'insufficient-basis' → StatusRunning
//          (process-local InsufficientBasis flag cannot be
//          reconstructed; keep status pending so dispatcher
//          re-emits the hint on next traversal).
//      RecoveryReport.Events appends recovery-attestation-
//      replay per run.
//   5. (No torn-row detect; invariant 6 dropped per F-15.)
func Recovery(ctx context.Context, deps RecoveryDeps,
    replayCounts ReplayCounts) (RecoveryReport, error)

var ErrRecoveryReplayDirty = errors.New(
    "recovery: ReplayCounts.Errors non-empty; refuse to proceed")
```

The function is called once by `cmd/ghyll/session.go:engineRuntime.Open`
AFTER `engine.Replay` returns and BEFORE `attachJournal` subscribes
the live observers. This ordering ensures recovery's writes don't
re-journal back into the engine (the same invariant
`engine/replay.go:18-20` enforces for replay). Recovery writes
go **directly to the Store** (engine layer); the in-memory
attestation cache is repopulated by `loadFromJSONL` at step 3 of
session_engine.Open. There is no in-memory store for
`evaluation_runs` (F-2); UpdateEvaluationRunReconciled writes
straight to sqlite.

**Operator CLI ergonomics** (F-14): `ghyll engine replay` now
prints a banner "this is a replay-only count; session start
additionally runs Recovery (use `ghyll engine recover --dry-run`
to preview)". A new subcommand `ghyll engine recover --dry-run`
opens the store read/write, runs Recovery inside a BeginTx that
ROLLBACKs at the end, and prints what Recovery would do without
committing.

### Part E: attestation-pending detection via JOIN (F-1 resolution)

A-3 (attestation-request persistence) does **not** require a new
table. Gate-1 review F-1 surfaced that:

1. `OpEventAttestationRequested` has zero publishers in the
   codebase today — the bus was a false detection signal.
2. The `depth_type_attestation_ref` carried on `evaluation_runs`
   IS the persistent attestation-request signal: it lands when
   the runner persists the clause run via the existing
   `Journal.handleRun` path, before any operator answer.

Attestation-pending detection becomes a JOIN:

```sql
SELECT p.pass_id, e.clause_id, e.depth_type_attestation_ref, e.arrow_id
FROM passes p
JOIN evaluation_runs e ON e.pass_id = p.pass_id
LEFT JOIN attestations a ON a.attestation_id = e.depth_type_attestation_ref
WHERE p.state = 'open'
  AND e.depth_type_attestation_ref != ''
  AND e.end_status = 'running'
  AND a.attestation_id IS NULL;
```

Every row in that result is an attestation-pending clause; its
pass survives recovery. The hint payload (`arrow_id` +
`clause_id` + `depth_type_attestation_ref`) is reconstructed
from the row and surfaced via `RecoveryReport.Events`.

A dedicated `attestation_requests` table (originally A-3) is
**not** added in Tier 1. It remains a Tier 2 consideration if
the operator-UI flow needs richer request metadata (timestamps,
operator who emitted, hint body); for crash recovery alone the
JOIN suffices.

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
