# Tier 1 gate-1 remediation log

Disposition for the 18 findings raised in
`specs/v2/validation-impl-pass-tier1.md` (cold-context adversary,
2026-05-20). Per standing direction: every finding remediated
in-phase; no deferrals; no severity filtering.

All remediations land in this single phase before the implementer
starts coding.

---

## Critical (6 of 6 remediated)

### F-1 — Attestation-pending detection signal

**Disposition**: Adopt the JOIN-based detection. Updated
`pass-persistence.md` domain model + invariant 4; added ADR-015
Part E with the canonical SQL. The `OperatorBus` is removed as a
detection surface; the persistent signal is
`evaluation_runs.depth_type_attestation_ref`, which lands via the
existing `Journal.handleRun` path before any operator answer.
Attestation-request persistence (the original A-3 / Option B) is
NOT needed for Tier 1; remains a Tier 2 consideration if the
operator-UI flow needs richer request metadata.

### F-2 — Recovery writes need direct Store APIs

**Disposition**: Adopt. Added `Store.UpdateEvaluationRunReconciled`
+ a new `recovery_source TEXT NOT NULL DEFAULT ''` column on
`evaluation_runs`. Recovery writes go **directly to sqlite**
(engine layer); ADR-015 Part D rewritten to make this explicit.
No runner-layer mutator exists for `evaluation_runs` (Runner.Evaluate
is one-shot per clause); the direct-Store write pattern is correct.

### F-3 — Preserved passes need PassRegistry.Resume

**Disposition**: Adopt Option B. Added
`PassRegistry.Resume(rec PassRecord, lockTable
*RoleContextLockTable) (*Pass, error)` to the contracts; Recovery
calls it for every preserved pass. ADR-015 Part A's emit section
documents the call sequence. `/passes` lists preserved passes;
the dispatcher refuses to open a competing pass on the same tuple
because Resume re-acquires the lock token.

### F-4 — Lock-ordering deadlock on emit-under-r.mu

**Disposition**: Adopt Option A (FindingsStore pattern). The
`PassRegistry.emit` runs **without acquiring any registry lock**;
observer list is one-shot at session start, so unlocked fanout is
safe. Emit point in `Pass.closeWith` moves to AFTER
`lockToken.Release` so the cycle `p.mu → emit` becomes
`p.mu → release → emit`. ADR-015 Part A updated.

### F-5 — JSONL-missing on fresh projects

**Disposition**: Adopt. ADR-015 Part C now distinguishes four
cases: missing+engine-empty → fresh start; missing+engine-has-
rows → `ErrAttestationAuditLost`; unreadable → same; truncated
trailing line → lenient (see F-6). The `loadFromJSONL` contract
returns `(loaded int, truncated bool, err error)`.

### F-6 — JSONL torn-line handling

**Disposition**: Adopt the **Lenient** option from the report.
`loadFromJSONL` stops at the last complete record on truncation
and reports `truncated=true`. Session.Open emits
`OpEventAttestationAuditDurabilityFailed` with offset detail.
JSONL writer truncates the file at the last complete offset on
next `Record`. ADR-015 Part C updated.

---

## High (8 of 8 remediated)

### F-7 — `evaluation_runs.end_status` flip needs schema + API + mapping

**Disposition**: Adopt all three. Added:
- `Store.UpdateEvaluationRunReconciled(ctx, runID, endStatus,
  source, at)` API.
- `recovery_source TEXT NOT NULL DEFAULT ''` column.
- Verdict → ClauseStatus mapping table in ADR-015 Part D:
  `pass→StatusPass`, `fail→StatusFail`,
  `insufficient-basis→StatusRunning` (the process-local
  `InsufficientBasis` flag can't be reconstructed from disk;
  keeping status `running` causes the dispatcher to re-emit the
  hint on next traversal).
- `schemaVersion` bumps from 2 to 3 (F-17).

### F-8 — ReplayTargets.Passes breaking change for 4 call sites

**Disposition**: Adopt. Contracts doc now explicitly lists the
four call sites (`session_engine.go`, `engine_cmd.go`,
`arrow_cmd.go`, replay tests). `ghyll engine replay` CLI output
extends to print pass + recovery counts.

### F-9 — `Recovery` signature too narrow

**Disposition**: Adopt. Introduced `RecoveryDeps` struct in
ADR-015 Part D carrying `Store`, `Passes`, `Attestations`,
`LockTable`, `IBTracker`, `JSONLPath`, `Now func() time.Time`.
`Recovery(ctx, deps, replayCounts)` is the new signature.
Idempotence (F-12) requires the injected `Now`.

### F-10 — `engine status` CLI race with in-flight Recovery

**Disposition**: Adopt single-transaction. Recovery wraps its
five-step scan in one `BeginTx`/`Commit` so concurrent readers
see pre- or post-recovery atomically. ADR-015 Part D made
explicit.

### F-11 — Journal backpressure can drop pass events

**Disposition**: Adopt Option A (critical-priority for
`jKindPass`). The `Journal.enqueue` path special-cases pass
events to block indefinitely rather than drop after the 100ms
budget. Invariant 1 demands this; other event kinds keep the
drop semantics. ADR-015 Part A explicit.

### F-12 — Recovery idempotence unprovable without `Now` injection

**Disposition**: Adopt. `recovered_at` is **set-once**: Recovery
skips passes where `recovered_at != ''`. Re-running Recovery on
the same store yields empty `RecoveryReport`. `Now func() time.Time`
injected via `RecoveryDeps` for testability. ADR-015 Part A
explicit.

### F-13 — Replay errors → half-loaded state for Recovery

**Disposition**: Adopt fail-loud. Recovery refuses to run if
`ReplayCounts.Errors` is non-empty; returns
`ErrRecoveryReplayDirty`. ADR-015 Part D explicit.

### F-14 — `ghyll engine replay` CLI ergonomics gap

**Disposition**: Adopt. Updated banner on `ghyll engine replay`
output; new `ghyll engine recover --dry-run` subcommand that
opens read/write, runs Recovery inside a transaction that
rolls back, prints what Recovery would do. ADR-015 Part D
explicit.

---

## Medium (4 of 4 remediated)

### F-15 — Torn-row mechanism for invariant 6

**Disposition**: Drop invariant 6 (architect's original
recommendation, formally adopted here). Sqlite WAL provides
row-level atomicity; the JSONL fsync inversion (Part C) covers
the load-bearing durability surface. The
"Crash mid checkpoint-log write" deferred BDD scenario is
**retired** in `state-machine.feature` — `pass-persistence.md`'s
"scenarios covered" count drops from 8 to 7.

### F-16 — Missing `Store.GetPass` / `/passes <id>` contracts

**Disposition**: Adopt. Added `Store.GetPass(ctx, id) (PassRecord,
bool, error)` + `Store.ListPasses(ctx, filter PassListFilter)
([]PassRecord, error)` to the contracts records section.
`handlePassesCommand` gains an optional ID argument; output
format specified (role, context, arrow_id, opened_at,
closed_at, close_reason, grid_version, recovered_at).

### F-17 — `schemaVersion` bump

**Disposition**: Adopt. `schemaVersion` bumps from 2 to 3.
`ensureSchemaVersion` runs the `ALTER TABLE evaluation_runs ADD
COLUMN recovery_source TEXT NOT NULL DEFAULT ''` migration once
on a v2 → v3 transition. Contracts updated.

### F-18 — Recovery events into zero-subscriber bus

**Disposition**: Adopt Option A. Recovery does NOT publish to
the bus; it returns `RecoveryReport.Events` and session.Open is
responsible for surfacing them to the operator (chat-loop banner
on first iteration). The bus stays for live runtime events.

---

## Notes incorporated (10 of 10)

| Note | Where addressed |
|---|---|
| N-1 (duplicate fanout: bus.Publish + registry.emit) | ADR-015 Part A: removed `OpEventPassOpened` / `OpEventPassClosed` from `OpenPass` / `closeWith`; PassEvent is single audit path. |
| N-2 (Register-emits-on-Open + bus.Publish duplicate) | Same as N-1. |
| N-3 (PassRegistry comment claims "crash-recovery does not persist") | Note added to contracts: that comment must be removed/updated in the Tier 1 implementation pass. |
| N-4 (`PassEventRecover` enum loop closure) | Resolved: `PassEventRecover` is emitted by `PassRegistry.Resume` (per F-3). |
| N-5 (JSONL observer error-channel special-casing) | ADR-015 Part C: JSONL writer is first observer fired inline within `Record`'s critical section; if it fails, `Record` returns error and no other observer fires. AttestationObserver signature stays unchanged. |
| N-6 (PassEventRecover vs OperatorBus event distinction) | Contracts already separate `PassEventKind` from `OperatorEventKind`; remediation log makes the distinction explicit. |
| N-7 (no-orphans restart should be no-op) | Test surface added (see N-10). |
| N-8 (engine_cmd bypasses session lockfile) | F-10 single-transaction Recovery makes this race benign — readers see atomic snapshot. |
| N-9 (`recoveryRun` struct sketch) | Contracts update specifies the struct. |
| N-10 (unit test list) | Contracts gain a "Required unit tests" section. |

---

## Net effect on Tier 1 scope

- **BDD scenarios lifted**: 7 (not 8; F-15 retires "Crash mid
  checkpoint-log write").
- **New ADR additions**: `recovery_source` column, schemaVersion
  bump to 3, four `recovery-*` event kinds, two new Store APIs
  (`UpdateEvaluationRunReconciled`, `GetPass`, `ListPasses`,
  `UpsertPass`), `PassRegistry.Resume`, `RecoveryDeps` struct.
- **Net adversary findings**: 18 raised, 18 remediated, 0
  deferred.
- **New ADRs**: 1 (ADR-015 amending ADR-010).

Implementer picks up
`specs/architecture/tier-1-pass-persistence-contracts.md` (now
fully remediated). No further architect work expected; if any
implementation surface contradicts the contracts, escalate back
via `specs/escalations/`.
