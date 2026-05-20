# Tier 1 integrator review (gate 2)

Reviewer: cold-context integrator, 2026-05-20.
Commits reviewed: `8241a25 ... 2410d9c` (steps 1–11 of the Tier 1
ADR-015 implementation).
Total findings: 12 (5 cross-package seam issues, 1 lifecycle-race
seam issue, 3 documentation drifts, 3 test-coverage gaps).

The integrator pass is orthogonal to the auditor (spec→code
fidelity) and the adversary (attacks on shipped code). The focus
here is: at the boundary between two or more packages, does data
flow stay coherent? When a Tier 1 component is exercised in a
context that the implementer didn't write a test for, does it
still produce a correct result?

## Cross-package seam issues

### G2-I-1: `ghyll engine recover --dry-run` always errors on a non-empty engine

**Seam**: `cmd/ghyll/engine_cmd.go` ↔ `engine/recovery.go` ↔
`database/sql` ↔ `modernc.org/sqlite`.

**Issue**: `cmdEngineRecover` wraps `engine.Recovery` in a
SAVEPOINT for dry-run semantics:

```go
// cmd/ghyll/engine_cmd.go:404
if _, err := store.DB().ExecContext(ctx, `SAVEPOINT recover_dryrun`); err != nil { ... }
rep, recErr := engine.Recovery(ctx, engine.RecoveryDeps{...}, ReplayCounts{})
```

`engine.Recovery` internally opens its own transaction via
`deps.Store.db.BeginTx(ctx, nil)` (engine/recovery.go:98). SQLite
does not permit `BEGIN` inside an active savepoint/transaction
on the same connection; it returns
`SQL logic error: cannot start a transaction within a transaction (1)`.

Reproduced locally against a clean modernc.org/sqlite store:
- `SAVEPOINT outer` succeeded.
- `db.BeginTx(...)` immediately returned the error above.

Compounding factor: the SAVEPOINT is issued via `db.ExecContext`,
which checks out a pooled connection. `db.BeginTx(...)` may
return a *different* connection. There is no
`SetMaxOpenConns(1)` on the engine store (verified by grep).
In the lucky case where two connections are used, the SAVEPOINT
sits on connection A, Recovery's BEGIN/COMMIT runs and *commits*
on connection B, then the ROLLBACK TO on connection A is a no-op
— meaning `--dry-run` would silently apply the recovery writes
for real. Either branch is broken; the BEGIN-in-savepoint branch
is what fires today on a single-connection pool.

**Detection**: a unit test for `cmdEngineRecover` against a
seeded engine.db with one orphan pass would fail immediately. No
such test exists today (see test-coverage gap G2-I-T3).

**Remediation**: refactor so the dry-run path uses one of:
1. Pass an explicit `*sql.Tx` (or `*sql.Conn`) into a new
   `engine.RecoveryInTx(ctx, tx, deps, counts)` and let the
   caller own commit/rollback. The session.Open path wraps in
   its own auto-commit; the CLI wraps in a tx it always rolls
   back. Recovery itself does no BeginTx.
2. Or, before calling Recovery, set `db.SetMaxOpenConns(1)` and
   replace SAVEPOINT/BEGIN with nested SAVEPOINTs only (do not
   call BeginTx inside the savepoint). Recovery would need a
   "txOrSavepoint" abstraction.

Option 1 is the cleaner inversion of control and matches the
"single transaction" F-10 invariant — the caller chooses where
the boundary is.

---

### G2-I-2: `ghyll arrow show <id>` reports `attestations: 0` for arrows with attestations (Tier 1 regression)

**Seam**: `cmd/ghyll/arrow_cmd.go` ↔ `engine/replay.go` ↔
`runner/attestationstore.go`.

**Issue**: Tier 1 commit `177f62e` (step 7) removed the
engine→runner attestations load step from `engine.Replay`:

```go
// engine/replay.go:91 (Tier 1)
if targets.Attestations != nil {
    c.Attestations = len(targets.Attestations.All())  // count only; no load
}
```

The new contract is that the in-memory `AttestationStore` is
pre-populated by `AttestationStore.LoadFromJSONL(...)` BEFORE
`Replay` is called. `cmd/ghyll/session_engine.openEngineWithOptions`
does this correctly (session_engine.go:171–193).

But `cmd/ghyll/arrow_cmd.go:cmdArrowShow` still calls `Replay`
into a freshly constructed `runner.NewAttestationStore()`
without first loading the JSONL:

```go
// cmd/ghyll/arrow_cmd.go:89–100
attestations := runner.NewAttestationStore()
if _, err := engine.Replay(ctx, store, engine.ReplayTargets{
    ...
    Attestations: attestations,   // empty; never populated
}); err != nil { ... }
...
atts := attestations.ForArrow(arrowID)
ui.Info("  attestations: %d", len(atts))
```

Reproduced with a one-off unit test (deleted after verification):
recorded one depth-type attestation against arrow A-test, ran
`cmdArrowShow A-test --dir <workdir>`, observed
`attestations: 0` despite the JSONL + engine table both holding
the row.

**Detection**: the existing `TestScenario_ArrowShow_HappyPath_RendersArrow`
asserts `attestations: 0` against a fixture with no attestations.
It does not exercise the path with a recorded attestation.

**Remediation**: in `cmdArrowShow`, after constructing the
empty `AttestationStore` and before calling `Replay`, call
`attestations.LoadFromJSONL(filepath.Join(filepath.Dir(dbPath),
"attestations.jsonl"), attCount > 0)` where `attCount` comes
from `store.CountAttestations(ctx)`. The fresh-project case
(file missing + engine empty) is silently OK; the inconsistent
case surfaces via `ErrAttestationAuditLost`. This mirrors what
`session_engine.openEngineWithOptions` already does.

A regression test that records one attestation, closes the
session, and asserts `attestations: 1` from `cmdArrowShow` would
catch this and any future divergence.

---

### G2-I-3: `engine.Recovery` report never surfaces to the operator on session start

**Seam**: `cmd/ghyll/session.go` (initEngine) ↔
`cmd/ghyll/session_engine.go` (replayEngine + RecoveryReport
accessor) ↔ user-facing output.

**Issue**: `replayEngine` captures `engine.Recovery`'s
`RecoveryReport` in `r.recoveryReport` (session_engine.go:307)
and exposes a `RecoveryReport()` accessor. `session.initEngine`
never calls this accessor, so the operator sees no notice when
crash recovery:

- aborted N orphan passes (`OpEventRecoveryPassAbortedCrash`),
- preserved M attestation-pending passes
  (`OpEventRecoveryAttestationRepublished`),
- flipped K evaluation_runs from `running` to `pass`/`fail`
  (`OpEventRecoveryAttestationReplay`).

The whole point of building the report-via-events surface
(deliberately NOT publishing to the OperatorBus per F-18) is that
session.Open prints them. Today the report fields are written
but never read.

Verified by grep: `RecoveryReport()` has zero non-test callers.

**Detection**: a Session-level unit test that seeds an open pass
in engine.db, opens a session, and asserts the
`s.output(...)` channel emitted a "crash recovery: 1 pass
aborted" line.

**Remediation**: in `session.initEngine` after `attachJournal`
succeeds, call `rt.RecoveryReport()` and `s.output(...)` a
summary line for each of the four counts when non-zero, plus
the first N events (capped, similar to the replay-errors
elision pattern at session.go:301–307).

---

### G2-I-4: Dispatcher does not stamp `GridVersion` on freshly opened passes; engine row stores `grid_version=0`

**Seam**: `runner/dispatcher.go` ↔ `runner/pass.go` ↔
`runner/projectstatus.go` (PassRegistry.Register) ↔
`engine/journal.go` (handlePass) ↔ `engine/records.go`
(UpsertPass) ↔ `passes` table.

**Issue**: Commit `2410d9c` reports a fix that stamps
`GridVersion` on `PassEventOpen` emitted by
`PassRegistry.Register`. The fix is in
runner/projectstatus.go:191:

```go
GridVersion: p.GridVersion(),
```

But `p.GridVersion()` returns `p.gridVersion`, which is
populated by `OpenPass` from `opts.GridVersion`
(pass.go:126). The dispatcher does NOT pass
`GridVersion: req.GridVersion` into `PassOptions`:

```go
// runner/dispatcher.go:161
pass, err := OpenPass(PassOptions{
    PassID:    passID,
    Role:      req.Role,
    Context:   req.Context,
    ArrowID:   req.Arrow.ID,
    LockTable: d.LockTable,
    LockTTL:   d.DefaultLockTTL,
    Bus:       d.Bus,
    Now:       now,
    // GridVersion: req.GridVersion  ← MISSING
})
```

So `Register`'s fix stamps `0` — the bug it claims to fix.
The acceptance test that "surfaced this" in commit `2410d9c`
seeds `GridVersion: 1` directly into `OpenPass` (steps_tier1_
recovery.go:437, 510), not through `Dispatch`. So it passes
while the dispatcher path is still broken.

**Detection**: extend `TestScenario_Tier0_RunArrow_DispatcherEndToEnd`
(cmd/ghyll/tier0_wiring_test.go:247) to call
`rt.Store().GetPass(ctx, passID)` after the dispatch and assert
`got.GridVersion == 1` (or whatever non-zero version the test
seeds). It currently checks DispatchResult fields but not the
engine row.

**Remediation**: add `GridVersion: req.GridVersion` to the
`OpenPass(PassOptions{...})` literal in
runner/dispatcher.go:161. One-line fix; the PassOptions field
already exists.

---

### G2-I-5: `PassRegistry.Resume` builds passes with `bus: nil`; resumed passes silently skip bus bridge

**Seam**: `engine/recovery.go` (preserveOpen) ↔
`runner/projectstatus.go` (Resume) ↔ `runner/pass.go` (closeWith
→ emit) ↔ `runner.OperatorBus` subscribers (CLI UI, IB tracker
via bus, future operator UI).

**Issue**: `PassRegistry.Resume` constructs a fresh `*Pass`
(projectstatus.go:255–264) without a `bus` field, so
`p.bus == nil`. `RecoveryDeps` has no `Bus` field, so the
information isn't even available to recovery.

When this resumed pass later closes, `closeWith` calls
`registry.emit`, which checks `if pass.bus == nil { return }`
BEFORE the bus-bridge switch (projectstatus.go:149). Result:

- The engine journal IS notified (it subscribes via
  `registry.Observe`, not via the bus).
- Legacy `OpEventPassOpened` / `OpEventPassClosed` bus
  subscribers are NEVER notified for resumed passes — only for
  freshly opened passes.

Any consumer that relies on the bus for pass-lifecycle audit
(e.g., a future operator UI subscribing to OperatorBus, the
"engine status CLI" tail that ADR-012 mentions) will silently
miss the close transition for every recovered pass. The engine
row IS persisted, so a polling consumer recovers, but a
push-driven consumer drifts.

**Detection**: add a `t.Run("resumed pass close emits to bus")`
case in `runner/pass_test.go` that resumes a pass, subscribes
to a fresh bus, closes the pass, and asserts the bus saw the
event. It will fail.

**Remediation**: add a `Bus *OperatorBus` field to
`runner.ResumeOptions` and a `Bus *OperatorBus` to
`engine.RecoveryDeps`. Recovery's `preserveOpen` passes
`deps.Bus` through. `session_engine.replayEngine` plumbs
`r.bus`. Then `Resume` sets `p.bus = opts.Bus` on the
constructed *Pass.

Alternatively (less plumbing): document that the bus bridge is
"open-only" for resumed passes and document it in
state-machine.md F-4. Either choice is defensible; today the
behavior is undocumented divergence.

---

### G2-I-6: Race between `Journal.Close` and `enqueue(jKindPass)` can panic on send-to-closed-channel

**Seam**: `engine/journal.go` close path ↔ enqueue path ↔
`runner/projectstatus.go` PassRegistry.emit observer.

**Issue**: `Journal.enqueue` (journal.go:143) checks
`j.closed.Load()` at entry, then performs a non-blocking select
send (handles closed channel via `default`), then for the
specific case of `jKindPass` falls through to an unconditional
blocking send:

```go
// engine/journal.go:154–159
if e.kind == jKindPass {
    j.events <- e   // unconditional; panics on closed channel
    return
}
```

TOCTOU race:

1. enqueue() reads `closed=false` (line 144).
2. enqueue() hits the `default` branch (channel full at the
   moment).
3. `Journal.Close()` runs on another goroutine:
   `CompareAndSwap(false, true)` succeeds → `close(j.events)`.
4. enqueue() reaches line 157, sends on the now-closed channel
   → panic: send on closed channel.

The other-kind path (lines 163–172) does `select { case
j.events <- e: case <-t.C: }` — `case ch <- e` on a closed
channel actually still panics (Go select does not exempt closed
channels from the panic). So that path has the same flaw, but
it's been there pre-Tier 1. What Tier 1 added is a NEW unguarded
send for `jKindPass`, making the window slightly wider (no
timeout fallback to a `dropped` increment).

In normal shutdown order (`session.Close` → `engine.closeEngine`
→ `journal.Close`) the runner has finished its work and no Pass
is closing, but a `defer p.Close("reason")` firing late in the
chain — e.g., a deferred Abort in a panic-recover path — can hit
this window.

**Detection**: a stress test that opens a pass, races
`p.Close("done")` with `journal.Close()` under `-race`, run
1000 iterations. Likely passes most runs and panics on
unlucky scheduling. Verified the pattern by reading the close
+ enqueue code; no test currently exercises this concurrency.

**Remediation**: gate the unconditional send with a recover or
move the close-check inside the same select. Cleanest pattern:

```go
defer func() {
    if r := recover(); r != nil {
        j.dropped.Add(1)
    }
}()
if e.kind == jKindPass {
    j.events <- e
    return
}
```

— OR replace the unbounded block with a select on a quit channel
that `Close()` triggers. Recovery from the panic is the smaller
change.

The F-11 claim "block indefinitely (modulo Close)" is only
truthful if Close translates a blocked sender into "drop +
increment counter" — not "panic the process."

## Documentation drift

### G2-I-D1: CLAUDE.md project-structure block lists engine as "Replay (loads persisted entities)" — Recovery missing

`CLAUDE.md:79–80`:

```
engine/   sqlite-backed persistent store + Journal observer fanout +
          Replay (loads persisted entities at session start)
```

Tier 1 adds a third surface (`engine.Recovery`), which the
description should mention. Operators reading CLAUDE.md to
orient themselves to the codebase won't discover the Recovery
component.

Suggested wording: append "+ Recovery (crash reconciliation
between engine, JSONL audit, and runner stores at session
start)".

The Key Design Decisions list (`CLAUDE.md:107–116`) also stops
at ADR-007/008 — ADR-015 is the most structurally significant
Tier 1 decision and deserves a line.

### G2-I-D2: `main.go` top-level usage banner does not list `engine recover` subcommand

`cmd/ghyll/main.go:25–34`:

```go
"usage: ghyll run [dir] [--model <model>]",
"       ...",
"       ghyll engine status [--dir <path>]",
"       ghyll engine replay [--dir <path>]",
"       ghyll engine verify-attestations [--dir <path>]",
"       ghyll arrow show <arrow-id> [--dir <path>]",
```

Tier 1 ships `ghyll engine recover [--dry-run] [--dir <path>]`
but the discovery surface (typing `ghyll` with no args) does
not list it. The `engineUsage` constant inside `engine_cmd.go`
does list it, but only after the user has typed `ghyll engine`
without a subcommand.

Suggested fix: add the line to the main usage banner.

### G2-I-D3: `docs/operator-guide.md` does not document `engine recover`

The operator-guide.md describes `ghyll engine status` (line 93)
and `ghyll engine replay` but has no section for `engine
recover`. F-14 (the gate-1 finding that drove this CLI) is
about operator visibility into crash recovery; shipping the
binary without docs is half the fix.

Suggested fix: add a "Crash recovery preview" subsection under
"Engine inspection" that explains: when to run it, what
`--dry-run` is, what the output rows mean (orphans aborted /
preserved / runs flipped), and why `--commit` is refused.

### G2-I-D4: `runner/attestation_jsonl.go` Observer docstring is stale post-Tier 1

`runner/attestation_jsonl.go:104–112`:

```
// Observer returns an AttestationObserver that appends one JSON
// line per Record event. Wire via
// `attestationStore.Observe(writer.Observer())`. ...
```

Tier 1 (ADR-015 Part C) made the JSONL writer the *primary
writer* (called inline, fails closed). `session_engine.attachJournal`
no longer subscribes the Observer — it calls
`SetPrimaryWriter(jsonlWriter.PrimaryWriter())`. The Observer
function still exists but is unused. The docstring should
either:
- Mark Observer deprecated / fallback-only, or
- Be removed and replaced with a pointer to `PrimaryWriter()`.

`cmd/ghyll/session_engine.go:418–424` also still says
"Last attestation Record events publish to the AttestationStore
Observer, which writes to the JSONL file" — that's the pre-Tier-1
flow. Update or remove.

## Test-coverage gaps

### G2-I-T1: `cmdEngineRecover` has zero direct test coverage

The new `ghyll engine recover --dry-run` CLI surface ships with
no unit test. The combination of G2-I-1 (the SAVEPOINT bug) and
this gap means the CLI has never been exercised against a
seeded orphan pass. Recommended: add at minimum:

1. `TestScenario_EngineRecover_DryRun_ReportsOrphanCount` —
   seed one open pass in engine.db, invoke `cmdEngineRecover`,
   assert output contains "orphans aborted: 1" AND the pass row
   is still `state=open` (because rollback should have undone the
   abort).
2. `TestScenario_EngineRecover_RefusesCommit` — invoke with
   `--commit`, assert the typed refusal message.
3. `TestScenario_EngineRecover_MissingDB` — invoke against a
   workdir with no engine.db, assert the missing-engine marker
   is emitted.

Test (1) is what would have caught G2-I-1.

### G2-I-T2: No test exercises arrow_cmd.cmdArrowShow with recorded attestations

The happy-path arrow show test (arrow_cmd_test.go:25) seeds an
arrow with zero attestations and asserts `attestations: 0`.
No test records an attestation and verifies arrow show prints
`attestations: 1`. This is the gap that hid G2-I-2.

Recommended: extend the happy-path test, or add a sibling test,
that records an attestation through `rt.AttestationStore().Record`,
closes the session, runs `cmdArrowShow`, and asserts a non-zero
attestation count in the output.

### G2-I-T3: No race test for `Journal.Close` vs in-flight `jKindPass` enqueue

G2-I-6 identifies a concurrency window where a pass close
emitting through PassRegistry → Journal.enqueue races
Journal.Close. The test would loop:

```go
for i := 0; i < 1000; i++ {
    j := NewJournal(store, nil)
    j.AttachPasses(reg)
    go func() { p.Close("done") }()
    j.Close()
}
```

Under `-race` this should reveal the panic-on-closed-channel
window. None exists today.

### G2-I-T4: tier1-recovery BDD step tmpdir leaks across runs

`tests/acceptance/steps_tier1_recovery.go:35`:

```go
dir, err := os.MkdirTemp("", "tier1-recovery-")
```

The `ctx.After` hook closes `state.TR1Store` but never
`os.RemoveAll(state.TR1Workdir)`. Each acceptance scenario in
the Tier 1 batch leaves a `tier1-recovery-*` directory under
`/tmp` (or `$TMPDIR`). A long-running CI host accumulates these.

Not load-bearing — the directory contents are an engine.db and
a JSONL file, both small — but a leak in pattern. Fix: in the
After hook, `if state.TR1Workdir != "" { _ = os.RemoveAll(state.TR1Workdir) }`.

## Verdict

**Tier 1 is structurally sound at the runner ↔ engine boundary**
— the PassRegistry observer plumbing, the Journal pass-kind
priority block (modulo G2-I-6), the JSONL-source-of-truth
inversion via SetPrimaryWriter, and the engine.Recovery
single-transaction reconciliation are all coherent at the seams
the implementer wired.

**Tier 1 is broken at the cmd/ghyll ↔ Tier 1 boundary.** Three
of the six seam findings (G2-I-1, G2-I-2, G2-I-4) are bugs that
the operator or downstream code will encounter on first
non-trivial use of the new surface:

- The new `ghyll engine recover --dry-run` always errors.
- The existing `ghyll arrow show` lost its attestation column.
- The dispatcher path stores `grid_version=0` for all live
  passes (the "fix" in 2410d9c stamps 0 because the input is 0).

The most load-bearing of these is **G2-I-1** because it
completely breaks the new CLI introduced as a *deliberate
operator-visibility surface* (F-14). The CLI exists to give the
operator a way to preview what session.Open would do; the
preview command cannot run.

The second-most load-bearing is **G2-I-2** because it's a
silent regression in an existing command (arrow show) — anyone
relying on it to audit attestations on an arrow now gets a
false-zero. Silent wrong is worse than loud wrong.

The third (G2-I-4) is more contained — the engine row's
`grid_version` is wrong, but the runtime correctness flow
doesn't read it (Recovery does, but only to seed
`ResumeOptions.GridVersion`, which propagates the zero further
into the PassEventRecover event; no decision is made on the
value being non-zero).

Recommendation: gate-2 should not pass without remediation of
G2-I-1, G2-I-2, G2-I-3, G2-I-4. G2-I-5 and G2-I-6 should be
remediated in a same-tier follow-up, with documented test
additions per G2-I-T1 through G2-I-T4. The doc drifts
(G2-I-D1 through G2-I-D4) are low-effort and should ship in
the same pass.
