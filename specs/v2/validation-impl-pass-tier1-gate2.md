# Tier 1 adversarial review (gate 2, post-implementation)

Reviewer: cold-context adversary, 2026-05-20.
Commits reviewed: `8241a25` ... `2410d9c` on `main` (9 commits — passes
table, PassRegistry observers + Resume, Journal.AttachPasses, JSONL
inversion, engine.Recovery, session orchestration, `ghyll engine
recover --dry-run`, BDD bindings).
Cross-references: `specs/v2/validation-impl-pass-tier1.md` (gate-1
findings — verified whether actually fixed in code).

Total findings: 11. Severity: 4 critical / 4 high / 3 medium.

Headline finding (most load-bearing): **G2-F-1 — `Journal.enqueue`
sends to a possibly-closed channel, panicking the runner. The F-11
fix (jKindPass blocks indefinitely) widens the panic window rather
than closing it.**

---

## Critical findings (must remediate)

### G2-F-1: `Journal.enqueue` races `Journal.Close`; pass-priority blocking send panics on a closed channel

**Affects**: `engine/journal.go:143-173`, `engine/journal.go:114-120`.

**Claim** (`engine/journal.go:138-142`): "F-11 (Tier 1): jKindPass is
CRITICAL PRIORITY. … For this kind only, the enqueue blocks
indefinitely rather than honoring the 100ms budget."

**Counterclaim**: The `closed.Load()` check at line 144 is a TOCTOU.
Between the load and any subsequent send (`j.events <- e` at lines
150, 157, 166), `Journal.Close()` can run. Close does
`j.closed.CompareAndSwap(false, true)` and then `close(j.events)`. A
goroutine that already passed the `closed.Load()` check and is now
about to send to `j.events` will panic with **"send on closed
channel"** when the close happens first.

The F-11 priority bump makes this strictly worse. Line 157
(`j.events <- e` for jKindPass) is an **unbounded** blocking send,
so the time-window for Close to race is unbounded. Before this
change, the bounded timer (`select case <-t.C`) at least bounded the
panic-window to ~100ms; now a pass-event enqueue can hold an
arbitrarily long send window into a possibly-closed channel.

Concurrent scenario:

1. Pass dispatcher closes its Pass on goroutine X. closeWith → emit
   → AttachPasses observer body → `j.enqueue({kind: jKindPass})`.
2. Goroutine X passes `closed.Load() == false`, hits the
   non-blocking `select case j.events <- e: default:`, the channel
   is full (e.g., a backed-up writer in CI under load), falls
   through to line 157.
3. Concurrently, goroutine Y (session.Close → closeEngine) runs
   `j.Close()` → `j.closed.CompareAndSwap(false, true)` succeeds →
   `close(j.events)`.
4. Goroutine X resumes at line 157 `j.events <- e`. Panic: send on
   closed channel. The runner goroutine dies.

The bounded-budget path (line 166) has the same race; the timer
can't prevent the runtime's send-to-closed-channel panic.

**Reproduction**: Force the channel full (buffer=1, consumer asleep)
and call Close concurrently with a pass-close that enqueues. Tests
today don't exercise this — `engine/journal_test.go` has no
Close-during-enqueue racetest.

**Remediation**:

Two options:

- **A (use sync.Mutex around close+send)**: replace the atomic flag
  with a RWMutex. `Close()` takes the write lock, sets closed,
  `close(j.events)`, releases. `enqueue()` takes the read lock,
  checks `closed`, performs the send while holding the read lock.
  Close blocks until all in-flight enqueues finish.

- **B (recover() guard on each send)**: wrap each `j.events <- e`
  call in a closure with `defer recover()` to swallow the panic
  and increment `j.dropped`. Documented as "panic-safe enqueue".
  Less clean but minimally invasive.

Recommend A. The runner must not panic from concurrent shutdown.

Confidence: 90%.

---

### G2-F-2: `ghyll engine recover --dry-run` SAVEPOINT does not actually contain Recovery's transaction — `database/sql` pool can split SAVEPOINT and BEGIN across connections

**Affects**: `cmd/ghyll/engine_cmd.go:404-417`,
`engine/store.go:55-61` (no `SetMaxOpenConns(1)`),
`engine/recovery.go:96-101` (internal BeginTx).

**Claim** (`engine_cmd.go:395-403`): "take a sqlite SAVEPOINT before
the call and ROLLBACK TO after. SQLite supports nested savepoints,
and Recovery's inner BeginTx becomes a SAVEPOINT under it."

**Counterclaim**: This is **broken** under `database/sql` connection
pooling. Three structural problems:

1. **No connection pinning**: `engine.OpenStore` (engine/store.go:55-61)
   constructs a `*sql.DB` with no `SetMaxOpenConns(1)`. The default
   pool keeps a variable number of idle connections; each
   `db.ExecContext` may pick a different idle connection.

2. **Savepoint is per-connection**: SQLite SAVEPOINTs live on the
   connection that created them. `store.DB().ExecContext(ctx,
   "SAVEPOINT recover_dryrun")` runs on whichever connection the
   pool hands out; the connection then returns to the pool. The
   next `db.ExecContext` may take a different connection that has
   no savepoint named `recover_dryrun`.

3. **Recovery's BeginTx pins its own connection**: `engine.Recovery`
   calls `deps.Store.db.BeginTx(ctx, nil)` (engine/recovery.go:98)
   which pins ONE connection — possibly NOT the one that holds the
   savepoint. Recovery's UPDATEs commit on that connection. The
   outer `ROLLBACK TO SAVEPOINT recover_dryrun` then runs on a
   third connection and either errors ("no such savepoint") or no-
   ops on a connection that never had the savepoint.

4. **`BEGIN inside SAVEPOINT` is sqlite-undefined**: even if all
   three statements pinned to one connection, SQLite forbids
   `BEGIN` while a transaction is open. Recovery's BeginTx would
   error out, OR (driver-specific) silently re-use the surrounding
   transaction — in which case the inner Commit closes the OUTER
   savepoint's transaction, and ROLLBACK TO has nothing to roll
   back.

**Net effect**: `ghyll engine recover --dry-run` may write
PERSISTENT changes to the engine DB, contradicting its docstring
("opens the store read/write, runs Recovery inside a transaction
that is ALWAYS rolled back"). An operator using the CLI to
"preview" recovery actually triggers them.

**Reproduction**:

1. Seed a crashed-session DB with 1 open pass row.
2. Run `ghyll engine recover --dry-run`. The CLI prints "preserved"
   or "aborted" counts AND commits the change.
3. Re-run `ghyll engine recover --dry-run`. The pass is now
   `state=aborted` from the first call; the second call sees no
   orphans and reports zero. The "dry run" was real.

**Verification gap**: No tests for `cmdEngineRecover` exist
(`grep -rn "Test.*Recover.*DryRun\|dry-run\|dryrun" tests/ → 0
matches`). The savepoint behavior was never validated end-to-end.

**Remediation**:

1. Pin the pool to one connection: `s.db.SetMaxOpenConns(1)` in
   `openStoreDSN`. Single-writer is already the sqlite invariant.

2. OR: refactor `engine.Recovery` to accept a `*sql.Conn` (acquired
   via `db.Conn(ctx)`) instead of running its own BeginTx. The
   dry-run path then acquires one Conn, issues SAVEPOINT, passes
   the Conn to a `RecoveryOnConn(ctx, conn, deps)`, issues ROLLBACK
   TO on the same Conn.

3. Add `TestScenario_EngineRecoverDryRun_LeavesDBUnchanged` that
   seeds a row, runs the CLI in-process, asserts the row is still
   `state=open`.

Confidence: 95%.

---

### G2-F-3: F-18 not remediated — Recovery events are captured in `engineRuntime.recoveryReport` but session.Open never surfaces them

**Affects**: `cmd/ghyll/session_engine.go:315-323`,
`cmd/ghyll/session.go:255-308`, `runner/operatorbus.go:67-74`
(declared but unsubscribed event kinds).

**Claim** (`engine/recovery.go:51-54`): "session.Open surfaces these
to the operator on chat-loop startup — Recovery does NOT publish to
the OperatorBus (F-18)."

**Claim** (`runner/operatorbus.go:67-74`): "Emitted by
engine.Recovery into RecoveryReport.Events, NOT to the bus. …
session.Open is responsible for surfacing these on the chat-loop's
first iteration."

**Counterclaim**: `RecoveryReport()` is defined on `engineRuntime`
(session_engine.go:318-323), but `grep -rn '\.RecoveryReport()'`
across the whole repo returns **zero callers**. session.Open
(session.go:267-308) replays the engine and surfaces replay counts
and per-row errors but never reads `rt.RecoveryReport()` or prints
any of its `Events` slice.

Net: every Recovery action — orphan-abort, attestation-republish,
verdict reconciliation — fires into a Go slice that nothing ever
reads or displays. The operator sees no banner about the recovery,
identical to the pre-fix world where the bus was empty. Gate-1 F-18
claimed remediated; in code it is not.

Concretely, after a crash:

1. Session restart → openEngine → replayEngine (runs Recovery
   internally; aborts 5 orphans, preserves 1 attestation-pending,
   replays 2 verdicts).
2. session.Open prints "ℹ engine replayed: N arrows, …" but
   nothing about the recovery work.
3. `rt.recoveryReport.Events` holds 8 OperatorEvents; they vanish
   when session.Open returns.

**Reproduction**:

```go
// Trace through session.go:267-308 — no call to rt.RecoveryReport().
// The Events field is unreachable from any user-visible surface.
```

The BDD scenarios verify the events are PRESENT in the
RecoveryReport (steps_tier1_recovery.go:280-294) but not that any
operator-facing surface displays them. The lift is theatre.

**Remediation**:

In `Session.Open` after `attachJournal` succeeds:

```go
report := rt.RecoveryReport()
if report.OrphansAborted+report.OrphansPreserved+report.EvaluationRunsFlipped > 0 {
    s.output(fmt.Sprintf(
        "⚠ recovery: %d orphans aborted, %d preserved (attestation-pending), %d runs reconciled",
        report.OrphansAborted, report.OrphansPreserved, report.EvaluationRunsFlipped))
    for _, ev := range report.Events {
        s.output("  - " + ev.Kind + " pass=" + ev.PassID + " arrow=" + ev.ArrowID + " " + ev.Detail)
    }
}
```

OR push the events through the OperatorBus AFTER the chat loop
subscribes. Add a `Session.SurfaceRecoveryReport()` call to the
chat-loop's first iteration so subscribers see them.

Confidence: 99%.

---

### G2-F-4: `OpEventRecoveryJSONLTruncated` declared but no publisher exists; truncation is silent to the operator

**Affects**: `runner/operatorbus.go:74`,
`cmd/ghyll/session_engine.go:181-185,367-371`,
`runner/attestationstore.go:385-454`.

**Claim** (`runner/operatorbus.go:74`):
`OpEventRecoveryJSONLTruncated OperatorEventKind = "recovery-jsonl-truncated"`
— declared as a recovery event kind.

**Counterclaim**: `grep -rn 'OpEventRecoveryJSONLTruncated'
/home/witlox/ghyll --include='*.go'` returns ONE match: the
declaration. No code publishes this event. When
`LoadFromJSONL` returns `truncated=true`, the openEngine path does:

```go
rt.jsonlTruncated = truncated
if logger != nil && (loaded > 0 || truncated) {
    logger.Info("engine: attestation jsonl loaded",
        "path", rt.jsonlPath, "loaded", loaded, "truncated", truncated)
}
```

Slog at Info level → goes to whatever slog writer is configured.
Operators in a CLI session see nothing on stdout/stderr. No
RecoveryReport.Events entry. The `rt.jsonlTruncated` flag is
consumed only by `TruncateTrailingPartial` in attachJournal — also
silent on success, only a `logger.Warn` on failure (which is itself
panicky — see G2-F-5).

So when a kernel panic + post-crash JSONL truncation occurs, the
operator gets no visible signal that audit data was discarded.
Worse: gate-1 F-6 specifically called out "the session emits an
OpEventAttestationAuditDurabilityFailed event with `Detail:
'trailing truncated line at offset N skipped'`". The remediation
declared a NEW event kind but wired no emit.

**Remediation**:

In `openEngineWithOptions` (session_engine.go:176-181), after
`LoadFromJSONL` returns truncated=true:

```go
if truncated {
    rt.bus.Publish(runner.OperatorEvent{
        Kind:    runner.OpEventRecoveryJSONLTruncated,
        Detail:  fmt.Sprintf("trailing partial line in %s skipped at load", rt.jsonlPath),
    })
    // also append to RecoveryReport.Events so session.Open surfaces it.
}
```

Note: the bus has zero subscribers at this point (F-18 remediation
moved Recovery events out of the bus). So **the publish must
either go into RecoveryReport.Events directly** (Recovery's pattern)
**OR** be deferred to a queue that the chat-loop's first iteration
drains. Pick one and wire it.

Confidence: 95%.

---

## High findings

### G2-F-5: `attachJournal` calls `logger.Warn` on a nil logger when JSONL truncate fails — panic on disk error during recovery

**Affects**: `cmd/ghyll/session_engine.go:367-371`,
`cmd/ghyll/session.go:281` (calls `rt.attachJournal(nil)`).

**Code**:

```go
// session_engine.go:367-371
if r.jsonlTruncated {
    if err := r.jsonlWriter.TruncateTrailingPartial(); err != nil {
        logger.Warn("engine: jsonl truncate failed", "err", err)
    }
}
```

**Counterclaim**: `session.go:281` calls `rt.attachJournal(nil)` —
the `logger` parameter is nil. When `TruncateTrailingPartial`
returns an error (disk full, ENOSPC, EIO mid-`f.Sync()`), the
`logger.Warn(...)` call on a **nil `*slog.Logger`** panics.

Inside `engine.NewJournal(r.store, logger)`, the journal-internal
code defensively converts nil to `slog.Default()` (engine/journal.go:89-91).
But the `attachJournal` body uses the bare `logger` parameter
without the same fallback.

The truncate path is unreachable on healthy systems but is
**precisely the path the operator triggers when recovering from
crashes** — i.e., the cases where disks are most likely degraded.
Failure during recovery should never panic; it should be reported.

**Reproduction**:

1. Crash a session with a partial trailing line in
   `.ghyll/attestations.jsonl`.
2. Fill the disk so f.Truncate fails with ENOSPC.
3. `ghyll run .` → session.Open → openEngineWithOptions →
   replayEngine → attachJournal(nil) → TruncateTrailingPartial
   fails → `nil.Warn(...)` → segmentation fault.

**Remediation**:

```go
if r.jsonlTruncated {
    if err := r.jsonlWriter.TruncateTrailingPartial(); err != nil {
        if logger == nil {
            logger = slog.Default()
        }
        logger.Warn("engine: jsonl truncate failed", "err", err)
    }
}
```

Or, better: hoist the nil-guard to the top of `attachJournal` so
every subsequent `logger.X` call is safe.

Confidence: 99%.

---

### G2-F-6: Recovery transaction rollback leaves in-memory PassRegistry + LockTable mutations intact — engine row vs in-memory split brain on partial recovery

**Affects**: `engine/recovery.go:96-130,221-272`,
`runner/projectstatus.go:225-271`,
`runner/rolelock.go:119-161`.

**Claim** (`engine/recovery.go:96-106`): "F-10: single transaction
wrap so concurrent read-only CLIs see pre- or post-recovery
atomically." Deferred `tx.Rollback()` handles error / panic.

**Counterclaim**: `preserveOpen` (engine/recovery.go:221-272)
performs TWO classes of mutation per preserved pass:

1. **Transactional** — `r.tx.ExecContext(... UPDATE passes SET
   recovered_at = ...)`. Atomic with the surrounding tx.

2. **NON-transactional, in-memory** — `r.deps.Passes.Resume(...)`
   calls `lockTable.TryAcquire` (mutates `RoleContextLockTable`)
   AND `r.passes[p.ID()] = p` (mutates `PassRegistry.passes`).
   These mutations are **NOT** rolled back by `tx.Rollback`.

If `orphanAbort` or `evaluationRunReconcile` errors after some
`preserveOpen` calls have run:

- `tx.Rollback` reverts the engine `recovered_at` UPDATE.
- The PassRegistry still holds the resumed `*Pass` instances.
- The LockTable still holds the (role, context) tokens.

Net state after a failed Recovery:

- Engine: `passes` row says `state=open`, `recovered_at=''`.
- In-memory: registry says the pass is open AND a lock is held.

In the current call chain (session_engine.replayEngine → returns
err → session.Open → outputs warning, returns), the runtime is
discarded by `closeEngine()`. So in PRACTICE the split-brain
doesn't survive the failed start. BUT:

1. The contract in `engine/recovery.go:218` ("PassRegistry.Resume
   rebuilds the in-memory *Pass + re-acquires the lock token.
   Failure here is logged but the engine row is still preserved")
   already acknowledges that engine and in-memory may diverge.
   The Resume **success** path also creates this divergence on
   later-step failure.

2. If a future code path (post-Tier-1) catches the Recovery error
   and continues anyway, the split-brain persists. A re-Dispatch
   on `(analyst, A)` would get `ErrRoleContextBusy` from the
   stale lock holder, even though the engine considers the pass
   un-preserved.

3. F-12 idempotence is also broken in this state: on a fresh
   process restart, the engine row has `recovered_at=''` so the
   pass is re-considered orphan. Recovery aborts it. But on a
   process that survives the failed Recovery, the in-memory state
   says preserved-and-locked. The two facts disagree, and the
   operator's `/passes` view contradicts `ghyll engine status`.

**Reproduction**:

Inject an error in `evaluationRunReconcile` (e.g., via a context
cancellation between `preserveOpen` and `evaluationRunReconcile`).

```go
tx.Rollback runs → passes.recovered_at = ''
r.deps.Passes.Len() == 1   ← stale
r.deps.LockTable.InspectHolder("analyst", "A") returns "P1", held=true
```

**Remediation**:

Option A: track all in-memory mutations and undo them on Rollback.

```go
type undoFunc func()
var undo []undoFunc
defer func() {
    if !committed {
        for i := len(undo) - 1; i >= 0; i-- { undo[i]() }
    }
}()
// when Resume succeeds:
undo = append(undo, func() {
    p.lockToken.Release()
    r.deps.Passes.Unregister(p.ID())
})
```

Option B: structure Recovery so all engine writes happen FIRST
(transactional), commit, THEN do in-memory Resume calls. If Resume
fails after commit, log and continue (engine is the source of
truth; in-memory will be re-built on next restart).

Recommend B — it matches the principle that sqlite is the recovery
source of truth.

Confidence: 85%.

---

### G2-F-7: `CatchUpAttestations` writes individual rows without a transaction — partial catch-up + non-deterministic order means JSONL/engine convergence is timing-dependent on failure

**Affects**: `engine/attestations.go:160-172`,
`cmd/ghyll/session_engine.go:190-193`.

**Code** (`engine/attestations.go:160-172`):

```go
func (s *Store) CatchUpAttestations(ctx context.Context, src *runner.AttestationStore) (int, error) {
    ...
    for _, rec := range src.All() {   // map iteration — randomized
        if err := s.insertAttestation(ctx, rec); err != nil {
            return count, fmt.Errorf("catch-up attestation %s: %w", rec.ID, err)
        }
        count++
    }
    return count, nil
}
```

**Counterclaim**: Two problems.

1. **No transaction**: each `insertAttestation` is its own
   auto-commit statement. If the loop fails midway (e.g.,
   ErrAttestationConflict on rec #5 of 100), rows 0..4 are
   persisted and 5..99 are not. The engine and JSONL are now
   partially converged.

2. **Non-deterministic order**: `src.All()` calls
   `AttestationStore.All()` which iterates `s.byID` — a Go map.
   Map iteration order is randomized. On retry, a different
   subset may insert before the same conflict reappears. The
   operator cannot rely on "rerun and see the same prefix
   succeed".

3. **Conflict path is fatal**: `session_engine.go:191`
   propagates the error all the way up. A JSONL with a
   conflicting record (e.g., operator hand-edited the file
   after a verdict was recorded with different content) causes
   `session.Open` to fail with no recovery path. Per ADR-015
   Part C, JSONL is the source of truth — so the engine SHOULD
   defer to JSONL on conflict. Instead, CatchUp refuses to
   start.

**Reproduction**:

1. Engine has attestation att-X with verdict=fail (committed in a
   previous session before ADR-015 inversion).
2. JSONL contains att-X with verdict=pass (operator's authoritative
   record).
3. Next session start → LoadFromJSONL populates in-memory att-X
   with verdict=pass.
4. CatchUpAttestations → `insertAttestation` → INSERT IGNORE
   succeeds with 0 affected rows → content-equality probe → rec
   != existing → ErrAttestationConflict.
5. Session refuses to start. No way for the operator to choose
   "JSONL wins" without manual sqlite surgery.

**Remediation**:

1. Wrap CatchUpAttestations in a `BeginTx`. Per-row failure rolls
   back the catch-up so the in-memory and engine cache stay
   consistent.

2. Sort `src.All()` by ID before iteration so retry behavior is
   deterministic.

3. On `ErrAttestationConflict` during CatchUp specifically (NOT
   during normal Record), **prefer the JSONL value**: UPDATE the
   engine row to match. Per ADR-015 Part C, JSONL is the source
   of truth; conflict resolution should reflect that. Emit an
   `OpEventAttestationAuditDurabilityFailed` with
   `Detail: "engine row overridden by JSONL"` so the operator
   sees the divergence.

4. Alternative: if the operator MUST be alerted before a silent
   override, add a `--allow-jsonl-override` CLI flag and refuse to
   start without it.

Confidence: 80%.

---

### G2-F-8: Recovery's `time.Parse` of `OpenedAt` silently drops parse errors; corrupt timestamps produce passes with zero `openedAt`

**Affects**: `engine/recovery.go:240`.

**Code**:

```go
openedAt, _ := time.Parse(time.RFC3339Nano, item.Pass.OpenedAt)
if _, err := r.deps.Passes.Resume(runner.ResumeOptions{
    ...
    OpenedAt:    openedAt,
    ...
```

**Counterclaim**: The parse error is discarded. If `item.Pass.OpenedAt`
is empty (e.g., a row inserted via the BDD seed `UpsertPass` that
omitted `OpenedAt`, as several do — `steps_tier1_recovery.go:69-71`)
or contains non-RFC3339Nano bytes (a legacy v2 row that used a
different format, or out-of-band SQL insert), the parse returns
`time.Time{}` (zero) with no signal.

The resumed pass then has `openedAt = time.Time{}`. This breaks:

1. **Forensic value**: the BDD scenario "Pass aborted records reason
   in checkpoint" (steps_tier1_recovery.go:544-559) requires
   `OpenedAt != ""` for "forensic timestamps". For a preserved
   pass, the in-memory `Pass.OpenedAt()` returns the zero time —
   distinct from the engine row's `OpenedAt = ""`.

2. **Sort ordering**: `PassRegistry.All()` returns slices that
   downstream code sorts by OpenedAt — zero timestamps sort
   chronologically before any real timestamp, breaking display
   order.

3. **The BDD test "Pass aborted by crash recovery"** at
   `steps_tier1_recovery.go:65-72` seeds a pass with
   `OpenedAt: "2026-05-20T10:00:00Z"` (RFC3339, NOT RFC3339Nano).
   Parse succeeds because RFC3339Nano accepts strings without
   fractional seconds. Fine. But if a future row arrives with a
   subtle format drift (e.g., "+00:00" → "Z" mismatch, or a
   space-instead-of-T separator), parse fails silently.

**Reproduction**:

```go
rec := PassRecord{
    PassID: "P1", Role: "analyst", Context: "A", ArrowID: "A1",
    State: "open", OpenedAt: "2026-05-20 10:00:00 UTC", // wrong format
}
// → Resume gets time.Time{} silently.
```

**Remediation**:

```go
openedAt, err := time.Parse(time.RFC3339Nano, item.Pass.OpenedAt)
if err != nil {
    r.report.Events = append(r.report.Events, runner.OperatorEvent{
        Kind:   runner.OpEventRecoveryAttestationRepublished,
        PassID: item.Pass.PassID,
        Detail: "WARNING unparseable opened_at; falling back to now: " + err.Error(),
    })
    openedAt = now
}
```

OR: fall back to `now` and log. Either way, surface the error
through RecoveryReport.Events.

Confidence: 75%.

---

## Medium findings

### G2-F-9: BDD scenario binding mutates shared `state.TR1Passes` / `state.TR1LockTable` mid-scenario, breaking before/after isolation

**Affects**: `tests/acceptance/steps_tier1_recovery.go:425-449,503-521`.

**Code** (lines 428-429):

```go
ctx.Step(`^pass P1 has reached terminal arrow status$`, func() error {
    state.TR1Passes = runner.NewPassRegistry()       // ← overwrite
    state.TR1LockTable = runner.NewRoleContextLockTable()  // ← overwrite
    journal := engine.NewJournal(state.TR1Store, nil)
    ...
})
```

**Counterclaim**: The Before hook (line 34-55) initializes
`TR1Passes` and `TR1LockTable` per scenario. The "Pass completes
and emits checkpoint" step REPLACES both fields mid-scenario.

Two issues:

1. If a scenario interleaves `pass P1 was running but the runner
   crashed` (uses TR1Passes for recovery Resume) with the
   `pass P1 has reached terminal arrow status` step (replaces
   TR1Passes), prior recovery state is silently dropped. The test
   harness gives the impression of testing end-to-end flows but
   actually tests two disjoint sub-states linked by name only.

2. The replacement also leaks the previous `RoleContextLockTable`
   without releasing tokens. If the previous test acquired locks
   (TR1Passes.Resume calls TryAcquire), those tokens are stuck
   in the orphaned table. In long test runs, the file descriptor
   for the journal's sqlite handle (also leaked — `journal.Close()`
   IS called in this step, so the FD is OK, but the second journal
   on the same store opens a fresh consumer goroutine that
   competes with… no, actually only one journal at a time. OK,
   sqlite is fine; just the lock leak.)

**Reproduction**:

Trace through the "Pass completes and emits checkpoint" feature
flow. The step file replaces `TR1Passes`. If a subsequent step
inspected the original Passes, it sees an empty registry.

**Remediation**:

The step should not assign to `state.TR1Passes` / `state.TR1LockTable`
mid-scenario. Either:

- Use scenario-local variables for the "fresh registry + journal"
  setup.
- OR document explicitly that the scenario REPLACES state, and
  add a corresponding teardown for the journal goroutine.

The pattern is also fragile because `state` is shared across the
suite's parallel goroutines (godog runs scenarios sequentially per
suite, but the package-level `state` is reused).

Confidence: 75%.

---

### G2-F-10: BDD `MkdirTemp` workdir is created per-scenario but never removed; test runs leak GB of empty engine.dbs over time

**Affects**: `tests/acceptance/steps_tier1_recovery.go:34-61`.

**Code**:

```go
ctx.Before(func(c context.Context, sc *godog.Scenario) (context.Context, error) {
    dir, err := os.MkdirTemp("", "tier1-recovery-")
    ...
    state.TR1Workdir = dir
    ...
})
ctx.After(func(c context.Context, sc *godog.Scenario, _ error) (context.Context, error) {
    if state.TR1Store != nil {
        _ = state.TR1Store.Close()
    }
    return c, nil
})
```

**Counterclaim**: The After hook closes the Store but does NOT
`os.RemoveAll(state.TR1Workdir)`. The temp dirs accumulate. Each
contains a `.ghyll/engine.db` (the OpenStore call in the Before
hook creates one; the temp dir's other contents are minor).

Across a long CI run with the suite re-running (e.g., flaky test
retries), thousands of `tier1-recovery-XXXXX` directories
accumulate under `$TMPDIR`. Eventually exhausts disk space on
CI runners.

**Remediation**:

```go
ctx.After(func(c context.Context, sc *godog.Scenario, _ error) (context.Context, error) {
    if state.TR1Store != nil {
        _ = state.TR1Store.Close()
    }
    if state.TR1Workdir != "" {
        _ = os.RemoveAll(state.TR1Workdir)
    }
    return c, nil
})
```

Confidence: 99%.

---

### G2-F-11: `OpEventAttestationRequested` declared but still has zero publishers — gate-1 F-1 "Option A (JOIN-based detection)" remediation is incomplete

**Affects**: `runner/operatorbus.go:42`,
gate-1 F-1 remediation claim.

**Claim** (gate-1 F-1 remediation, accepted Option A): Define
attestation-pending via the JOIN of `evaluation_runs × attestations`.
ADR-015 Part D's `attestationPendingScan` then has an executable
contract. **Republishing the hint requires inferring the request
payload from the evaluation_runs row.**

**Counterclaim**: The implementation (engine/recovery.go:181-216)
DID adopt Option A's JOIN-based detection. Good. But:

1. **The gate-1 F-1 Option A spec demanded that "republish" emits a
   re-published attestation request**. The Recovery code emits
   `OpEventRecoveryAttestationRepublished` (not
   `OpEventAttestationRequested`). The semantic distinction
   matters: a fresh subscriber (operator UI reconnecting after
   restart) expecting `OpEventAttestationRequested` for "show me
   pending verdicts" would NOT pick up the recovery-republished
   variant. The two event kinds bifurcate the subscriber surface.

2. **`OpEventAttestationRequested` STILL has zero publishers**:
   `grep -rn 'OpEventAttestationRequested' /home/witlox/ghyll
   --include='*.go'` returns one match (the declaration). So the
   live runtime — not just recovery — never fires the "operator,
   please verify this clause" hint. The dispatcher in
   `runner/dispatcher.go:221-234` sets the in-process
   `input.AwaitingAttestation` flag but does not publish on the
   bus.

   This is the original F-1 problem: a fresh project running its
   first arrow that needs attestation NEVER signals the operator
   via the bus. Recovery's republish via a DIFFERENT event kind
   doesn't help because the subscriber for live attestation
   requests is still nonexistent.

**Reproduction**:

```bash
grep -rn 'OpEventAttestationRequested' /home/witlox/ghyll --include='*.go'
# /home/witlox/ghyll/runner/operatorbus.go:42:    OpEventAttestationRequested OperatorEventKind = "attestation-requested"
# (one line — the declaration)
```

**Remediation**:

Either:

A. **Have Recovery's republish emit `OpEventAttestationRequested`
   instead of (or in addition to) the recovery-specific kind**. A
   subscriber to "attestation-requested" then sees both fresh and
   post-recovery requests. The recovery-specific kind becomes a
   forensic tag, optional.

B. **Wire `OpEventAttestationRequested` to fire from
   `runner/dispatcher.go` when `AwaitingAttestation = true` is
   set**. This was the missing piece in F-1's analysis. Until this
   happens, no live "please verify" signal exists.

Recommend both: A for symmetry with Recovery; B for live flow.

Confidence: 80%.

---

## Notes (not findings — context for the implementer)

### G2-N-1: `Pass.closeWith` uses `time.Now()` directly (line 172) with no clock-injection. Tests cannot pin `closedAt` for the in-memory Pass; only the engine row's `ClosedAt` can be pinned (via journal clock injection). Mirroring `OpenPass`'s `Now func() time.Time` field would close the gap. Not load-bearing but reduces test determinism.

### G2-N-2: `engine.Recovery` honors `RecoveryDeps.Now` for the engine UPDATE timestamps (lines 225-226, 280-281, 341), but the `Pass.markRecovered` call (engine/recovery.go:248) passes the same `now` value as a closure. The closure is captured in `ResumeOptions.Now` and used by `markRecovered(now())`. So far so good. But: if Recovery is called twice in the same process with two DIFFERENT `Now` functions (e.g., a test that varies the clock), the first call's pass already has `recoveredAt` stamped, the second call skips (per F-12). The closures don't leak between calls.

### G2-N-3: The `engine/recovery.go:343 r.deps.Attestations.Lookup(rr.Ref)` call relies on the in-memory AttestationStore being populated by `LoadFromJSONL` at session_engine.openEngineWithOptions:176. Tests that call `engine.Recovery` directly (e.g., TestRecovery_EvaluationRunReconcile) populate `atts` via `atts.Record(...)`. That path uses the primaryWriter path (which is nil in tests) and the observer path (which is nil too). The test side-steps the JSONL-source-of-truth path. Not a finding, but the unit test does NOT verify the LoadFromJSONL → Lookup chain end-to-end.

### G2-N-4: `PassRegistry.emit` reads `pass := r.passes[e.PassID]` under RLock, then accesses `pass.bus.Publish` outside the lock (projectstatus.go:142-169). `pass.bus` is set at OpenPass time and never reassigned — safe. But the bus may be a stale pointer if `Pass` is later "cleaned up" (it isn't in current code, but a future change introducing `Pass.invalidate` would create a UAF). Document the invariant: `Pass.bus` is immutable post-construction.

### G2-N-5: `Journal.Flush` (engine/journal.go:223-237) sends a `jKindFlush` event on the channel and waits for the close signal. If `Flush` is called concurrently with `Close`, the send may race with `close(j.events)` — same G2-F-1 panic. The contract says "Returns immediately if the journal is closed", but only checks `j.closed.Load()` at the entry; between that check and the send, Close can race. Same remediation as G2-F-1.

### G2-N-6: `Pass.markRecovered` (runner/pass.go:261-281) emits `PassEventRecover` with `State: p.state` — which is `PassStateOpen` for a resumed pass. The persistence path
(engine/journal.go:427-441) writes that state to the `passes` table.
Since `r.deps.Passes.Resume` is called by Recovery BEFORE the journal
is attached, the emit goes nowhere (no observers). Good. But: if a
LATER code path (operator manually triggers re-resume, future
feature) calls `Resume` AFTER the journal is attached, the
emit would re-write the engine row with `state=open` and
`recovered_at=<new stamp>`. The UpsertPass clause says
`recovered_at = CASE WHEN passes.recovered_at = '' THEN excluded.recovered_at ELSE passes.recovered_at END`,
so it stays at the first-recovered value (good F-12 behavior). State
stays open. So this is safe — but only by accident. Document.

### G2-N-7: The `recovery_source` column on `evaluation_runs` is added via `ALTER TABLE ... ADD COLUMN` (engine/store.go:193-197). The PRAGMA-based idempotence check (lines 168-189) iterates table_info rows to check column existence. Safe. But: under sqlite < 3.35 the ALTER doesn't support every clause; sqlite < 3.25 doesn't support some DEFAULT expressions. The schema uses `DEFAULT ''` which is supported widely. Not a finding for the current modernc.org/sqlite (bundled), but a portability note.

### G2-N-8: `ListPasses` (engine/records.go:692-737) orders by `opened_at ASC`, but `opened_at` is a TEXT column. For ISO-8601 / RFC3339 strings the lexicographic order matches chronological order. Fine in practice. But the same column under recovery has `OpenedAt: "2026-05-20T10:00:00Z"` (no fractional) vs the journal's RFC3339Nano writes ("2026-05-20T10:00:00.123456789Z"). Lex order still works (the shorter string sorts BEFORE the longer one with a nano suffix, which is also chronologically earlier or equal). Edge-case noted, not load-bearing.

---

## Summary

Total findings: 11. Severity: 4 critical / 4 high / 3 medium.

**Critical**: G2-F-1 (journal send-on-closed-channel panic),
G2-F-2 (CLI dry-run does not roll back),
G2-F-3 (Recovery events captured but never surfaced — F-18 not fixed),
G2-F-4 (jsonl-truncated event has no publisher).

**High**: G2-F-5 (attachJournal nil-logger panic on disk error),
G2-F-6 (Recovery rollback leaves in-memory split brain),
G2-F-7 (CatchUpAttestations not transactional, conflict refuses start),
G2-F-8 (OpenedAt parse error silently dropped).

**Medium**: G2-F-9 (BDD step mutates shared scenario state),
G2-F-10 (BDD temp dirs not cleaned up),
G2-F-11 (OpEventAttestationRequested still has no publisher).

The most load-bearing single bug is **G2-F-2 (savepoint dry-run is
broken)** for operator-trust impact, **G2-F-1 (panic on enqueue
race)** for runtime stability, and **G2-F-3 (recovery events
invisible)** for the gate-1 F-18 lift being theatre — the operator
sees nothing, identical to the pre-fix world.
