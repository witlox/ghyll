# Tier 1 gate-2 remediation log

Disposition for the combined findings of the three parallel
cold-context reviews:

- Auditor (35 items): `specs/v2/audit-tier1.md`
- Adversary (11 findings + 8 notes): `specs/v2/validation-impl-pass-tier1-gate2.md`
- Integrator (12 findings: 6 seam + 4 doc-drift + 4 test-gap): `specs/v2/integrator-tier1-gate2.md`

Per standing direction (`feedback_no_deferrals.md`): every finding
remediated in-phase. No deferrals. No severity filtering.

The three reports overlapped on the load-bearing issues; the
unified item list deduplicates.

---

## Critical (5 items, must remediate before integrator pass)

| ID | Sources | Title | Disposition |
|---|---|---|---|
| C-1 | G2-F-1 / G2-I-6 / G2-N-5 | Journal.enqueue panics on send-to-closed-channel for jKindPass | Adopt sync.RWMutex pattern: Close takes write lock; enqueue takes read lock; close+send serialize properly. |
| C-2 | G2-F-2 / G2-I-1 / G2-I-T1 | `ghyll engine recover --dry-run` SAVEPOINT broken — actually commits | Refactor: add `engine.RecoveryInTx(ctx, tx, deps, counts)` that runs against a caller-supplied tx. session.Open keeps the current BeginTx-internally signature; the CLI wraps a single tx it always rolls back. |
| C-3 | G2-F-3 / G2-I-3 / auditor-most-LB | RecoveryReport captured but never surfaced | session.initEngine calls `rt.RecoveryReport()` and uses `s.output(...)` to surface counts + per-event lines. |
| C-4 | G2-F-4 / auditor | OpEventRecoveryJSONLTruncated declared but never emitted | Emit into RecoveryReport.Events (NOT bus per F-18) when openEngine's LoadFromJSONL returns truncated=true; add JSONLTruncatedSkipped count field. |
| C-5 | G2-I-2 / G2-I-T2 | `ghyll arrow show` lost attestations column (regression) | cmdArrowShow loads JSONL into a fresh AttestationStore before Replay; mirrors session.openEngine. |

## High (6 items)

| ID | Sources | Title | Disposition |
|---|---|---|---|
| H-1 | G2-F-5 | attachJournal nil-logger panic on disk error | Hoist nil→slog.Default() guard to top of attachJournal. |
| H-2 | G2-F-6 | Recovery rollback leaves in-memory split brain | Move `Passes.Resume` + `LockTable.TryAcquire` calls OUT of the transaction (run after commit, per Option B). If Resume fails post-commit, emit event but engine is the source of truth. |
| H-3 | G2-F-7 | CatchUpAttestations non-transactional, randomized order, conflict refuses start | Wrap in BeginTx + sort by ID. On ErrAttestationConflict during catch-up, UPDATE engine row to match JSONL (JSONL is source of truth per ADR-015 Part C) and emit OpEventAttestationAuditDurabilityFailed. |
| H-4 | G2-F-8 | Recovery time.Parse silently discards parse error | Surface via RecoveryReport.Events; fall back to deps.Now(). |
| H-5 | G2-I-4 | Dispatcher doesn't pass GridVersion to OpenPass | One-line: `GridVersion: req.GridVersion` in dispatcher.go OpenPass call. |
| H-6 | G2-I-5 | Resumed passes have bus=nil → silent miss of close events | Add `Bus *OperatorBus` to runner.ResumeOptions + engine.RecoveryDeps. preserveOpen plumbs through. |

## Medium / contract drift (8 items)

| ID | Sources | Title | Disposition |
|---|---|---|---|
| M-1 | G2-F-9 | BDD step replaces shared TR1Passes/TR1LockTable mid-scenario | Use scenario-local vars in the "Pass completes/aborted" steps; don't reassign state. |
| M-2 | G2-F-10 / G2-I-T4 | BDD tmpdir leak | After hook RemoveAll's TR1Workdir. |
| M-3 | G2-F-11 | OpEventAttestationRequested still no publishers | Dispatcher emits it when input.AwaitingAttestation=true is set (runner/dispatcher.go:221-234). |
| M-4 | auditor | Dangling docstring projectstatus.go:60-67 | Clean up; the half-deleted "Crash-recovery does NOT persist passes" comment must go. |
| M-5 | auditor | ErrPassResumeInvalidState documented but undefined | Add the sentinel; emit from Resume when opts.PassID has an invalid shape. |
| M-6 | auditor | ReplayTargets.Passes + ReplayCounts pass counters absent (contract drift) | Add the fields. Replay can count passes via the Passes registry if provided. |
| M-7 | auditor | /passes <id> REPL variant not wired | Parse the arg; route to engine.GetPass. |
| M-8 | auditor | cmdEngineReplay doesn't print pass/recovery counts | Add counts + the F-14 banner pointing at `recover --dry-run`. |

## Tests (3 new)

| ID | Sources | Title |
|---|---|---|
| T-1 | auditor / G2-I-T3 | TestJournal_CloseRaceJKindPass — under -race, 1000 iterations of Close vs pass enqueue. |
| T-2 | auditor | TestRecovery_JSONLTrailingTruncated — partial last line; expect truncated=true, event in report, TruncateTrailingPartial on next Record. |
| T-3 | auditor | TestRecovery_SingleTransactionAtomicity — concurrent read sees pre- or post-recovery atomically. |
| T-4 | G2-I-T1 | TestEngineRecover_DryRun — seed orphan; invoke cmdEngineRecover; assert no commit + report rendered. |
| T-5 | G2-I-T2 | TestArrowShow_WithAttestations — record attestation; assert cmdArrowShow renders non-zero count. |

## Doc drift (4)

| ID | Sources | Title |
|---|---|---|
| D-1 | G2-I-D1 | CLAUDE.md project structure block missing Recovery + ADR-015. |
| D-2 | G2-I-D2 | main.go usage banner missing `engine recover`. |
| D-3 | G2-I-D3 | docs/operator-guide.md missing Crash-recovery section. |
| D-4 | G2-I-D4 | attestation_jsonl.go Observer docstring + session_engine.go comment are stale (Observer is fallback only post-Tier 1). |

---

## Total: 29 items.

Working order (commit batches):

1. **Critical-path correctness** (C-1, C-2, H-2, H-3, H-4, H-5, H-6, M-3) — these change the recovery + dispatcher contract.
2. **Operator surfacing** (C-3, C-4, H-1, M-8) — wires the visibility surface.
3. **Regressions** (C-5) — restore cmdArrowShow.
4. **Sentinels / contract drift** (M-4, M-5, M-6, M-7) — code-level cleanup.
5. **BDD hygiene** (M-1, M-2).
6. **New tests** (T-1 through T-5).
7. **Docs** (D-1 through D-4).
