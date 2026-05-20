# Tier 1 auditor review (gate 2)

Reviewer: cold-context auditor, 2026-05-20.
Commits reviewed: 8241a25 ... 2410d9c (9 commits).
Overall fidelity: **MEDIUM-HIGH**. Persistence + recovery substrate is
sound and matches the ADR's load-bearing decisions; gaps are at the
"surface to operator" perimeter (events not drained, `/passes <id>`
not wired, two required tests missing, stale doc comment, dead enum).
No silent correctness regressions on the gate-and-arrow path.

## Per-invariant fidelity

| # | Invariant | Enforcement point | Fidelity | Notes |
|---|---|---|---|---|
| 1 | Pass state persisted on every transition | `runner/pass.go:164-193` `closeWith` captures payload under `p.mu`, releases lock, then `registry.emit` (no `p.mu` held); `runner/projectstatus.go:141-170` `emit` only holds `r.mu` for slice snapshot; `engine/journal.go:143-159` `enqueue` for `jKindPass` blocks indefinitely; `engine/journal.go:419-441` `AttachPasses` / `handlePass` writes via `UpsertPass`. | HIGH | F-4 + F-11 cleanly implemented. The journal hot path is documented and exercised by `TestScenario_Pass_ClosePostLockReleaseEmit`. |
| 2 | JSONL is source of truth for attestations | `runner/attestationstore.go:192-214` `Record` calls `primaryWriter` BEFORE `byID` mutation and returns `ErrAttestationAuditWriteFailed` on failure; `runner/attestation_jsonl.go:265-289` `PrimaryWriter` marshals+writes+fsyncs and returns the error inline; `runner/attestationstore.go:385-455` `LoadFromJSONL` rebuilds the in-memory cache before Replay; `engine/attestations.go:160-172` `CatchUpAttestations` writes the engine cache from in-memory state. | HIGH | The inversion is fully realized. JSONL writer is the privileged first sink; observers can't see records the JSONL didn't accept. |
| 3 | Recovery is deterministic + idempotent | `engine/recovery.go:79-131` wraps the scan in one `BeginTx/Commit`; `engine/records.go:649-656` upsert preserves `recovered_at` set-once via `CASE WHEN passes.recovered_at = ''`; `engine/recovery.go:143-150` `orphanScan` excludes rows with `recovered_at != ''`; `engine/recovery.go:90-92` defaults `Now` to `time.Now` but tests inject a fixed clock. `TestRecovery_Idempotent` proves the no-op second-call behavior. | HIGH | F-12 fully addressed. |
| 4 | Attestation-pending passes survive recovery | `engine/recovery.go:181-216` `attestationPendingScan` runs the canonical JOIN from ADR-015 Part E (matches the SQL verbatim); `engine/recovery.go:221-272` `preserveOpen` UPDATEs `recovered_at`, calls `PassRegistry.Resume` to rebuild in-memory `*Pass` AND re-acquire the lock token; emits `OpEventRecoveryAttestationRepublished` into `RecoveryReport.Events`. | HIGH | F-1 + F-3 + F-18 (events-into-report) cleanly wired. `TestRecovery_AttestationPendingPreserved` asserts `lockTable.InspectHolder("analyst","A") == "P1"` post-recovery — the F-3 promise. |
| 5 | Other open passes → aborted:crash | `engine/recovery.go:276-303` `orphanAbort` UPDATEs inside the single Recovery transaction; emits `OpEventRecoveryPassAbortedCrash` per pass. | HIGH | |
| 6 | ~~Torn checkpoint records detected~~ | DROPPED per F-15. `pass-persistence.md` and ADR-015 acknowledge the drop; `state-machine.feature:220-225` retires the "Crash mid checkpoint-log write" scenario via a comment block. | HIGH | |
| 7 | Query historical pass is a read, not reconstruction | `engine/records.go:669-687` `Store.GetPass` SELECTs by `pass_id`, returns `(rec, false, nil)` on miss. | HIGH (Store level) / MEDIUM (CLI/REPL level) | The Store API is correct. The `/passes <id>` slash-command variant is NOT wired in `cmd/ghyll/session.go:1093,1336-1362` — only the bare `/passes` lists open passes. F-3's BDD scenario passes because the binding goes straight to `Store.GetPass`, not via `DispatchSlashCommand`. |

## Per-failure-mode fidelity

| FM | Failure | Handled? | Notes |
|---|---|---|---|
| FM-1 | sqlite write fails on INSERT during OpenPass | YES (indirect) | The runner-level `OpenPass` doesn't write to engine directly — it acquires the lock first. `PassRegistry.Register` calls `emit` which routes through the journal. A failed enqueue (which can't fail for `jKindPass` because it blocks indefinitely) cannot leave the lock orphaned because the lock is held by the live `*Pass`. The contract's "persistence write BEFORE registry registration" framing is NOT what the code does — it's "register first, then persist via observer" — but the lock-table guard makes that safe. Defensible deviation from contract framing. |
| FM-2 | sqlite write fails on UPDATE during Close/Abort | PARTIAL | `handlePass` calls `j.logErr("UpsertPass", ...)` — a sqlite failure logs and continues. The in-memory state still transitions; the journal `dropped` counter doesn't tick (jKindPass blocks indefinitely). A sustained sqlite failure would log many warnings; there is no "audit lost" surface for pass persistence specifically. |
| FM-3 | JSONL fsync ok, engine INSERT fails | YES | The journal observer is decoupled from the inline JSONL write. Replay reconstructs from JSONL via `CatchUpAttestations`. |
| FM-4 | engine + JSONL agree, end_status still running | YES | `recoveryRun.evaluationRunReconcile` (`engine/recovery.go:309-367`) is the explicit reconciliation path; `recovery_source` records provenance. Tests: `TestRecovery_EvaluationRunReconcile` + BDD "Crash between attestation write and clause-status flip". |
| FM-5 | Two restart processes run recovery concurrently | YES (out of band) | `cmd/ghyll/lockfile.go` enforces single-active-session. Engine CLI subcommands bypass the lockfile but per F-10 + `engine/recovery.go:98` (single `BeginTx`) the worst case is a CLI seeing pre- or post-recovery state, not torn. |
| FM-6 | Recovery itself crashes mid-reconciliation | YES | Single `BeginTx`/`Commit` means a panic/crash before commit leaves no partial state. The `defer tx.Rollback()` at line 102-106 makes this explicit. |
| FM-7 | Attestation request in operator-bus-only state | YES (resolved by F-1) | The JOIN replaces the bus; `evaluation_runs.depth_type_attestation_ref` is the persistent signal. Confirmed by `TestRecovery_AttestationPendingPreserved`'s seed-and-detect flow. |

## ADR Part-by-part fidelity

### Part A: passes schema

| Element | Contract | Implementation | Fidelity |
|---|---|---|---|
| Table columns | 10 columns (pass_id, role, context, arrow_id, grid_version, state, opened_at, closed_at, close_reason, recovered_at) | Identical (`engine/store.go:410-421`) | HIGH |
| CHECK constraint | `state IN ('open','closed','aborted')` | Identical (`engine/store.go:416`) | HIGH |
| Indexes | `idx_passes_state`, `idx_passes_arrow`, `idx_passes_role_ctx` | Identical (`engine/store.go:422-424`) | HIGH |
| schemaVersion bump | 2 → 3 | `engine/store.go:112` `schemaVersion = 3` + `ensureRecoverySourceColumn` migration runs ALTER conditionally | HIGH |
| Set-once `recovered_at` | UPSERT must preserve | `engine/records.go:649-656` `CASE WHEN passes.recovered_at = '' THEN excluded.recovered_at ELSE passes.recovered_at END` — explicit; covered by `TestScenario_UpsertPass_RecoveredAtSetOnce` | HIGH |

### Part B: replay ordering

Contract:
```
1. attestations (from JSONL) → 2. grid arrows → 3. requirements →
4. classifications → 5. findings → 6. amendments → 7. passes →
8. recovery scan
```

Implementation reality:
```
openEngine:    LoadFromJSONL → CatchUpAttestations (engine cache)
replayEngine:  engine.Replay (grid → reqs → classifications →
               findings → amendments) → engine.Recovery
```

| Aspect | Fidelity | Notes |
|---|---|---|
| Step 1 (attestations from JSONL) | HIGH | Done in `openEngine` before `Replay`. |
| Steps 2-6 (replay other entities) | HIGH | `engine/replay.go:91-198`. Step ordering matches. |
| Step 7 (passes load) | LOW (deviation) | `Replay` has NO "load passes" step. `ReplayTargets.Passes` is **not added**; `ReplayCounts.PassesOpen/Closed/Aborted` are **not added**. Recovery reads `passes` directly via SQL (`orphanScan` at `engine/recovery.go:143-167`). Functionally equivalent — historical passes don't need an in-memory representation, preserved passes get `Resume`-d by Recovery. But the contract specified an explicit step + counts. |
| Step 8 (recovery scan) | HIGH | `engine/recovery.go:79-131` invoked from `cmd/ghyll/session_engine.go:295-307` immediately after Replay. |

### Part C: JSONL-source-of-truth inversion

| Element | Contract | Implementation | Fidelity |
|---|---|---|---|
| `AttestationStore.Record` returns `ErrAttestationAuditWriteFailed` on JSONL failure | yes | `runner/attestationstore.go:205-209` | HIGH |
| JSONL writer is FIRST observer (inline) | yes | `primaryWriter` is called BEFORE `byID` mutation and BEFORE other observers (`runner/attestationstore.go:205-213`); `SetPrimaryWriter` is wired in `cmd/ghyll/session_engine.go:363-372` from `attachJournal` | HIGH |
| `loadFromJSONL` four-case table (missing+empty, missing+rows, unreadable, truncated) | yes | `runner/attestationstore.go:385-455` returns the canonical `(loaded, truncated, err)`; tests: `TestRecovery_JSONLMissingFreshProject`, `TestRecovery_JSONLMissingWithRows` | HIGH |
| `AttestationJSONLWriter.TruncateTrailingPartial` | yes | `runner/attestation_jsonl.go:193-256`. Implementation scans backward for last newline rather than taking an offset argument (the contract said `TruncateAt(offset int64)`). Functionally equivalent but the signature deviates. | MEDIUM (minor signature deviation) |
| Engine catch-up | yes | `engine/attestations.go:160-172` `CatchUpAttestations` | HIGH |
| Inversion's blast radius (`session_engine.go`, `arrow_cmd.go`, `engine_cmd.go`, replay tests) | enumerated | `cmd/ghyll/session_engine.go:171-193` wires LoadFromJSONL → CatchUp BEFORE Replay; `cmd/ghyll/arrow_cmd.go:94-100` Replay still skips Passes (matches deviation in Part B). `cmd/ghyll/engine_cmd.go:259-320` `cmdEngineReplay` does NOT load attestations from JSONL — it opens read-only and just reports replay counts. Probably an oversight: per F-14 the `replay` CLI should reflect the inversion. | MEDIUM |

### Part D: Recovery component

| Element | Contract | Implementation | Fidelity |
|---|---|---|---|
| `RecoveryDeps` struct (Store, Passes, Attestations, LockTable, IBTracker, JSONLPath, Now) | yes | `engine/recovery.go:40-48` 1:1 with contract | HIGH |
| Single `BeginTx`/`Commit` (F-10) | yes | `engine/recovery.go:98-130` | HIGH |
| Refuses on dirty replay (F-13) | yes | `engine/recovery.go:84-86` returns `ErrRecoveryReplayDirty` if `len(replayCounts.Errors) > 0`; test `TestRecovery_ReplayCountsErrorsRefuses` confirms | HIGH |
| Five-step scan sequence | yes | `orphanScan` → `attestationPendingScan` → `preserveOpen` → `orphanAbort` → `evaluationRunReconcile` (`engine/recovery.go:108-126`) | HIGH (5th "torn-row" step correctly dropped per F-15) |
| Idempotence via set-once `recovered_at` (F-12) | yes | `engine/recovery.go:148` orphanScan filters `recovered_at = ''`; `preserveOpen` UPDATE has `WHERE recovered_at = ''`; `orphanAbort` UPDATE stamps `recovered_at` to mark "processed". | HIGH |
| `RecoveryReport.Events` is the operator surface (not bus) | yes — events flow into report, not bus | `engine/recovery.go:253-269,294-301,358-364` append to `report.Events`. **However**: `cmd/ghyll/session_engine.go:307` captures the report and the getter at line 318-322 exposes it, but **no caller drains it onto the chat-loop's first iteration**. The events vanish. F-18 remediation is only half-applied. | MEDIUM (substrate done, surface gap) |
| `RecoveryReport.JSONLTruncatedSkipped` | declared in contract | **MISSING**. The field is absent from the struct (`engine/recovery.go:55-61`) and no path emits `OpEventRecoveryJSONLTruncated` (declared but unused at `runner/operatorbus.go:74`). The truncation signal is surfaced via the JSONL writer's `OpEventAttestationAuditDurabilityFailed` bus event, NOT via a recovery event. | LOW |

### Part E: JOIN-based detection

The JOIN in `engine/recovery.go:185-194` is functionally identical to the
ADR-015 Part E SQL. The implementation runs **per-orphan** (one query
per orphan pass with `LIMIT 1`) rather than the single all-rows query
in the spec; for the expected pass cardinality (single-operator
throughput, low double digits), this is fine. Fidelity: HIGH.

## Per-finding remediation traceability

| Finding | Remediation claimed | Present in code? | Notes |
|---|---|---|---|
| F-1 | JOIN-based detection over evaluation_runs ⋈ attestations | YES | `engine/recovery.go:185-194` |
| F-2 | `UpdateEvaluationRunReconciled` + `recovery_source` column + direct-Store writes | YES | `engine/records.go:454-485` + `engine/store.go:165-199` migration + `engine/recovery.go:309-367` uses direct `tx.ExecContext`; recovery `_does not_` go through runner-layer mutators |
| F-3 | `PassRegistry.Resume(opts, lockTable)` rebuilds *Pass + re-acquires lock | YES | `runner/projectstatus.go:199-271` (signature uses `ResumeOptions` rather than `engine.PassRecord` to avoid import cycle — defensible deviation). `TestScenario_PassRegistry_ResumeRebuildsRegistry` confirms the lock is held. |
| F-4 | Emit-without-r.mu; `closeWith` lock order `p.mu → release → emit` | YES | `runner/pass.go:164-193`; `runner/projectstatus.go:141-145` only holds `r.mu.RLock()` for the slice snapshot, releases before observer fanout; `TestScenario_Pass_ClosePostLockReleaseEmit` exercises the deadlock surface |
| F-5 | `LoadFromJSONL` four-case table | YES | `runner/attestationstore.go:385-455` |
| F-6 | Lenient JSONL trailing truncation + truncate-on-next-Record | YES (writer + load) / PARTIAL (report surface) | Load+writer wired; `cmd/ghyll/session_engine.go:367-371` calls `TruncateTrailingPartial` after attach. **But** no `recovery-jsonl-truncated` event is appended to `RecoveryReport.Events`. Operator visibility comes via `OpEventAttestationAuditDurabilityFailed` on the bus (not recovery report). |
| F-7 | `UpdateEvaluationRunReconciled` + `recovery_source` + verdict→ClauseStatus map + schemaVersion bump | YES | `engine/records.go:454-485` + `engine/recovery.go:369-389` `verdictToClauseStatus`. Test: `TestRecovery_VerdictToClauseStatus` covers the mapping. |
| F-8 | All four production callers updated + CLI output extension | PARTIAL | `session_engine.go` updated; `arrow_cmd.go` left without Passes (no Passes step in Replay, so OK); `engine_cmd.go cmdEngineReplay` does NOT print pass + recovery counters — only adds the "use `ghyll engine recover --dry-run` to preview" banner (`cmd/ghyll/engine_cmd.go:316-319`). Replay-counts pass fields don't exist in code, so they can't be printed. |
| F-9 | `RecoveryDeps` struct | YES | `engine/recovery.go:40-48` |
| F-10 | Single BeginTx | YES | `engine/recovery.go:98-130` |
| F-11 | `jKindPass` blocks indefinitely on enqueue | YES | `engine/journal.go:154-159` `if e.kind == jKindPass { j.events <- e; return }` outside the bounded budget |
| F-12 | `recovered_at` set-once; idempotent re-run | YES | `engine/records.go:649-656` UPSERT clause; `engine/recovery.go:148` orphanScan filter; `TestRecovery_Idempotent` |
| F-13 | `ErrRecoveryReplayDirty` on `replayCounts.Errors != nil` | YES | `engine/recovery.go:66-67,84-86`; test confirms |
| F-14 | New `ghyll engine recover --dry-run` subcommand + replay banner | YES | `cmd/ghyll/engine_cmd.go:332-445` `cmdEngineRecover`; replay banner at line 316-319. **Caveat**: `cmdEngineRecover` doesn't pass `Passes` or `LockTable` to `RecoveryDeps`, so the preserveOpen path silently skips Resume (`engine/recovery.go:239-260` guards on both). Dry-run report will under-count "preserved" passes' resume side-effects (they don't happen) but the engine row update + event emission are intact. SAVEPOINT/inner-BeginTx interaction at `engine_cmd.go:404-417` is unusual but tests pass. |
| F-15 | Drop invariant 6; retire torn-checkpoint scenario | YES | `specs/features/state-machine.feature:220-225` comment block retires the scenario; `pass-persistence.md` invariant 6 documented as dropped. |
| F-16 | `Store.GetPass` + `Store.ListPasses` + `/passes <id>` slash command | PARTIAL | Store-side APIs exist (`engine/records.go:669-737`). The `/passes <id>` REPL variant is **NOT wired** — `cmd/ghyll/session.go:1093` only matches `line == "/passes"`. F-3 BDD scenario passes because the binding calls `Store.GetPass` directly, not via `DispatchSlashCommand`. |
| F-17 | `schemaVersion = 3` + ALTER migration | YES | `engine/store.go:112,165-199` |
| F-18 | Recovery emits into `RecoveryReport.Events`, NOT bus; session.Open surfaces | PARTIAL | Substrate done. `cmd/ghyll/session_engine.go:307` captures the report; `RecoveryReport()` accessor at line 318-322 exists. **But** no caller drains the events onto chat-loop output or any UI surface. Operator sees no banner about recovery actions. |

## Required-unit-test traceability

Contract (`tier-1-pass-persistence-contracts.md:594-625`) lists 12
required tests. Cross-checked against the test files:

| Test | Present | File:line |
|---|---|---|
| `TestRecovery_NoOpenPasses` | YES | `engine/recovery_test.go:27` |
| `TestRecovery_OrphanAbort` | YES | `engine/recovery_test.go:43` |
| `TestRecovery_AttestationPendingPreserved` | YES | `engine/recovery_test.go:78` |
| `TestRecovery_EvaluationRunReconcile` | YES | `engine/recovery_test.go:138` |
| `TestRecovery_Idempotent` | YES | `engine/recovery_test.go:192` |
| `TestRecovery_JSONLMissingFreshProject` | YES | `engine/recovery_test.go:235` |
| `TestRecovery_JSONLMissingWithRows` | YES | `engine/recovery_test.go:248` |
| `TestRecovery_JSONLTrailingTruncated` | **MISSING** | — |
| `TestRecovery_ReplayCountsErrorsRefuses` | YES | `engine/recovery_test.go:224` |
| `TestRecovery_SingleTransactionAtomicity` | **MISSING** | — |
| `TestPass_ClosePostLockReleaseEmit` | YES (renamed `TestScenario_Pass_ClosePostLockReleaseEmit`) | `runner/pass_test.go:104` |
| `TestPass_ResumeRebuildsRegistry` | YES (renamed `TestScenario_PassRegistry_ResumeRebuildsRegistry`) | `runner/pass_test.go:75` |

10 of 12 shipped. Two missing tests are exactly the surfaces with
weakest fidelity: JSONL trailing truncation (F-6) and recovery
single-transaction atomicity (F-10).

## BDD step-binding load-bearing assessment

Sample audit of `tests/acceptance/steps_tier1_recovery.go`:

| Step | Load-bearing? | Notes |
|---|---|---|
| "the engine performs crash recovery on restart" (line 74) | YES | Actually calls `engine.Recovery` with real deps; the report+state are inspected by subsequent steps. |
| "crash recovery does NOT mark P1 as aborted" (line 262) | YES | Reads back via `Store.GetPass`, asserts `State == "open"` AND `RecoveredAt != ""` AND `OrphansPreserved == 1`. |
| "the hint has been published to the operator event bus" (line 236) | NARRATIVE-ONLY | Step body returns nil with a comment explaining the JOIN replaces the bus. Acceptable framing per the F-1 remediation. |
| "the JSONL record was appended successfully" (line 353) | LOAD-BEARING | Calls `CatchUpAttestations` against an in-memory `AttestationStore` that has the verdict — simulates the post-load state. |
| "no 'split-brain' persists" (line 413) | PARTIAL | Asserts the in-memory attestation matches; the engine row's `end_status` was already asserted "pass" in the preceding step. Acceptable. |
| "the runner emits a checkpoint with pass-id, arrow-id..." (line 458) | LOAD-BEARING | Reads `PassRecord` via `Store.GetPass`, asserts every field. Cross-table provenance (evaluation_runs, findings) is acknowledged-as-out-of-scope in the comment. |

The bindings are mostly load-bearing; the narrative steps (e.g.,
"the hint has been published" / "the operator has not yet returned
a verdict") return nil but each is followed by a load-bearing step
that asserts the substrate. No "state theater" — every scenario has
real-substrate verification.

## Code-quality red flags

1. **`runner/projectstatus.go:61` — stale comment + editing artifact**:
   ```
   // Close/Abort. Crash-recovery does NOT persist passes — open
   // Tier 1 (ADR-015): the in-memory registry is the runtime
   ```
   The phrase "Crash-recovery does NOT persist passes — open" is a
   leftover from the pre-Tier-1 docstring (N-3 in the gate-1 review
   explicitly called this out). The remediation log claims "comment
   must be removed/updated" but only half the sentence was deleted.
   Reading the docblock as a whole now contradicts itself.

2. **`runner/projectstatus.go:209` — undefined sentinel**:
   The docstring at line 224 references `ErrPassResumeInvalidState`,
   but no `var ErrPassResumeInvalidState = errors.New(...)` exists
   in the file (or anywhere in the runner package). The contracts
   listed this error explicitly at `tier-1-pass-persistence-contracts.md:209`.
   `Resume` returns the underlying validation errors (`ErrPassIDEmpty`,
   etc.) instead. Cosmetic — Resume's validation works — but the
   contract surface is incomplete.

3. **`runner/operatorbus.go:74` — dead enum**:
   `OpEventRecoveryJSONLTruncated` is declared but never emitted by
   any code path. The F-6 remediation surfaces JSONL truncation via
   `OpEventAttestationAuditDurabilityFailed` (on the bus) and via
   `cmd/ghyll/session_engine.go:367-371` calling
   `TruncateTrailingPartial`. The dedicated recovery event has no
   producer.

4. **`engine/recovery.go:55-61` — `RecoveryReport.JSONLTruncatedSkipped` field missing**:
   The contract lists this field; the struct omits it. Operators have
   no way to programmatically count truncation events through the
   report shape.

5. **`runner/attestationstore.go:453` — comment punted**:
   ```
   _ = lastGood // reserved for future TruncateAt offset hand-off via session.Open
   ```
   The `lastGood` offset is computed but unused. The TruncateTrailingPartial
   path scans backward instead of using this hint. Minor — works
   correctly, but the loop's bookkeeping accumulates dead state.

6. **`cmd/ghyll/session_engine.go:318-322` — `RecoveryReport()` accessor never read**:
   ```bash
   $ grep -rn "RecoveryReport()" /home/witlox/ghyll/cmd/
   # only the definition; no caller
   ```
   The substrate exists; nothing surfaces it to the operator. F-18's
   "session.Open surfaces RecoveryReport.Events to the operator" is
   half-implemented.

7. **`cmd/ghyll/session.go:1093` — `/passes <id>` not wired**:
   The slash dispatcher only matches `line == "/passes"` exactly. A
   `line` starting with `/passes ` (with arguments) falls through to
   `Handled: false`. F-16's REPL surface is missing.

8. **`cmd/ghyll/engine_cmd.go:407-412` — `cmdEngineRecover` calls
   `Recovery` without `Passes` or `LockTable`**: the preserveOpen
   step's `if r.deps.Passes != nil && r.deps.LockTable != nil` guard
   silently skips `Resume`. Dry-run will report `OrphansPreserved`
   correctly (the engine row update happens unconditionally) but
   the side effect of "Resume rebuilds the registry" is not modeled
   in the preview. Operator-facing impact is small (the actual session
   start will Resume); diagnostic impact is invisible because the
   CLI's purpose is to preview engine-side mutations.

9. **`engine/replay.go` — `ReplayTargets.Passes` and `ReplayCounts.PassesOpen/Closed/Aborted` missing**:
   The contract explicitly says these are added. The implementation
   skips them entirely (Recovery reads passes directly via SQL). Function
   works but the contract surface is incomplete — any future tool
   consuming `ReplayCounts` for telemetry won't have pass figures.

10. **`engine/journal.go:419-441` — `AttachPasses` registers an observer
    that ignores `PassEventRecover`**: looking at `handlePass`, every
    kind routes to a single `UpsertPass` call. That's correct (Resume
    UPSERTS the row via the same path), but the journal does not
    differentiate the four event kinds — a future observer that wanted
    to act only on "recover" events couldn't piggyback on this path.
    Not a bug today; structural fragility.

## Per-package test depth assessment

Sample 5 tests:

| Test | Verdict | Notes |
|---|---|---|
| `TestRecovery_AttestationPendingPreserved` (engine/recovery_test.go:78) | LOAD-BEARING | Seeds pass + evaluation_run + uses real `runner.NewRoleContextLockTable`. Asserts the lock holder is `P1` (i.e., Resume actually re-acquired it) — proves F-3 end-to-end. |
| `TestRecovery_Idempotent` (engine/recovery_test.go:192) | LOAD-BEARING | Re-runs Recovery twice, asserts the second call's report is fully empty. Tests the F-12 invariant directly. |
| `TestRecovery_EvaluationRunReconcile` (engine/recovery_test.go:138) | LOAD-BEARING | Verifies the SQL `recovery_source` column is set; verifies the verdict→ClauseStatus mapping (`pass→pass`). The mapping table also has a dedicated `TestRecovery_VerdictToClauseStatus`. |
| `TestScenario_Pass_ClosePostLockReleaseEmit` (runner/pass_test.go:104) | LOAD-BEARING (race-detector-sensitive) | Observer body re-enters `reg.All()` → `p.State()` — exactly the AB/BA path F-4 fixed. If the fix regressed, the test deadlocks via the 2-second timeout. Note: the test does NOT use `-race`, so a subtle race could slip; the make target `make test-race` covers that. |
| `TestScenario_UpsertPass_RecoveredAtSetOnce` (engine/passes_test.go:58) | LOAD-BEARING | Calls UpsertPass twice with different `RecoveredAt` values, asserts the second is ignored. Direct test of the CASE-WHEN UPSERT logic. F-12 substrate. |

Overall test depth is good. The "narrative-only" BDD steps are bracketed
by load-bearing steps that exercise the substrate.

## Verdict

Overall fidelity: **MEDIUM-HIGH**.

The core load-bearing pieces — schema, recovery, JSONL inversion,
JOIN-based detection, Resume re-acquiring locks, set-once `recovered_at`,
single-tx atomicity, dirty-replay refusal — are all implemented and
load-bearingly tested. The substrate that ADR-015 calls "the
load-bearing change" (Part C JSONL inversion + Part D Recovery
component) is faithful to the spec.

What's missing is the **operator-facing surface perimeter**:

- **F-18 only half-implemented**: `RecoveryReport` is captured but
  never drained to the operator. Recovery events fire into nothing
  visible.
- **F-16 partially implemented**: `/passes <id>` slash command
  variant missing. F-3 BDD scenario passes via direct Store.GetPass
  call, but a real operator cannot use the documented REPL surface.
- **N-3 dangling**: docstring at `runner/projectstatus.go:61` is
  internally contradictory after partial editing.
- **`ErrPassResumeInvalidState` documented but absent**.
- **`OpEventRecoveryJSONLTruncated` declared but never emitted**.
- **`RecoveryReport.JSONLTruncatedSkipped` field absent**.
- **`ReplayCounts.PassesOpen/Closed/Aborted` and `ReplayTargets.Passes`
  absent** (the deviation from Part B ordering — functionally OK,
  contract-incomplete).
- **2 of 12 required unit tests missing**: `TestRecovery_JSONLTrailingTruncated`,
  `TestRecovery_SingleTransactionAtomicity`.
- **`cmd/ghyll/engine_cmd.go cmdEngineReplay` does NOT print the new
  pass/recovery counts** (F-8 follow-through).

None of these gaps regress correctness on the gate-and-arrow path.
The recovery mathematics is right; the recovery's operator-visible
surface is half-built.

### Required actions before Tier 2

Blocking (correctness or contract-trace integrity):

1. Drain `engineRuntime.RecoveryReport().Events` onto the chat-loop's
   first iteration or via a startup banner. Without this, F-18 is
   half-implemented and operators see "your closed pass was marked
   crashed" without context.
2. Wire `/passes <id>` slash command route in `DispatchSlashCommand`;
   surface `Store.GetPass` output in the expected format.
3. Fix the dangling docstring at `runner/projectstatus.go:60-67`.
4. Ship `TestRecovery_JSONLTrailingTruncated` and
   `TestRecovery_SingleTransactionAtomicity` — both are required-by-contract
   and exercise the only invariants that aren't load-bearingly tested.

Non-blocking (contract-trace polish):

5. Add `ErrPassResumeInvalidState = errors.New(...)` or remove the
   docstring reference.
6. Either emit `OpEventRecoveryJSONLTruncated` from Recovery when
   `jsonlTruncated == true` AND `RecoveryReport.JSONLTruncatedSkipped`
   in the report, or remove the dead enum.
7. Add `ReplayTargets.Passes` + `ReplayCounts.PassesOpen/Closed/Aborted`
   (even if `Replay`'s body just counts them; Recovery already does
   the meaningful work). Otherwise update ADR-015 Part B to document
   the deviation.
8. Extend `cmd/ghyll/engine_cmd.go cmdEngineReplay` output with
   passes + recovery counts (F-8 follow-through).

### Count of items in each fidelity bucket

- HIGH: 24 items (per-invariant 1-7 except #7 mixed; per-FM 1,3,4,5,6,7;
  ADR Part A all rows; Part D most rows; per-finding F-1 through F-5,
  F-7, F-9, F-10, F-11, F-12, F-13, F-15, F-17; 10 required tests
  shipped; BDD bindings load-bearing).
- MEDIUM: 7 items (invariant #7 CLI level; FM-2; Part C TruncateAt
  signature; Part C blast radius for engine_cmd; Part D F-18 surface;
  finding F-6 report side; finding F-8 partial; finding F-14 dry-run
  edge).
- LOW: 4 items (Part B passes step + counts; Part D
  `JSONLTruncatedSkipped` field; finding F-16 REPL surface; finding
  F-18 operator surface).

### Most load-bearing fidelity gap

**F-18 operator surfacing**: `RecoveryReport.Events` is computed
correctly but never drained anywhere the operator can see. A restart
that auto-aborts 5 orphan passes and reconciles 3 attestation
verdicts produces no banner, no chat-loop output, no `/passes`-visible
trace beyond the engine row mutations. This is the only gap that
materially affects the v2 promise "the operator knows what recovery
did". Everything else either has a working alternative (e.g., the
bus-side `OpEventAttestationAuditDurabilityFailed` for truncation)
or is contract-tracing polish. Without F-18's surface, the
"correctness over speed" doctrine's claim "the operator sees what
ghyll did" is broken at the most important boundary — recovery.
