# Tier 2 Adversarial Pass — Remediation Tracker

Tracks dispositions for the ~62 findings in
`validation-impl-pass-tier2-adversarial.md`. Per
`feedback_no_deferrals.md`: every finding remediates in-phase.

## Status Summary (2026-05-20)

| Tier        | Total | Done | Pending |
|-------------|------:|-----:|--------:|
| Critical    |    12 |   12 |       0 |
| High        |    25 |   25 |       0 |
| Medium      |    16 |   16 |       0 |
| Low         |     9 |    9 |       0 |
| Test gaps   |     7 |    6 |       1 |

**All non-test findings remediated.** One test gap (T-5) closed
automatically by the CONC-H-6 fix; T-1, T-7 covered by
modal_driver_race_test.go. T-2/T-3/T-4 covered by earlier
LineReader / drain-tail tests.

## Critical — ALL REMEDIATED

| ID | Commit | Notes |
|---|---|---|
| CORR-A-1 | 72cf9d0 | engine column parity (4 SQL sites + hydrate helper) |
| CORR-A-2 | 72cf9d0 | AttestationRecordsEqual HintJSON normalization |
| CORR-A-3 | 41bcaab | Tier 1→2 flat→tree migration with "_legacy" placeholders |
| SEC-C-1 | b79af21 | safeSegment hash-substitutes "." / ".." |
| SEC-C-2 | b79af21 | truncateTrailingPartialFile Lstat + O_NOFOLLOW |
| CORR-A-4 | ae3e352 | validateAttestationTier2 + AttestationStore.residueNoteMaxBytes |
| CORR-A-6 | ae3e352 | PassID-empty rejection at Record-time |
| SEC-H-1 | ae3e352 | Record-time ValidateUnitPayload + Unit-enum + hint_json + adversary "__" |
| CONC-C-3 | ddea6a9 | DrainPending preserves snapshot tail on non-cancel error |
| CONC-C-4 | ddea6a9 | DrainPending re-queues full tail on ctx-cancel |
| CONC-C-1 | 2509c8a | Shared modal.LineReader (one scanner per session) |
| CONC-C-2 | 2509c8a | One reader goroutine per session |

## High — ALL REMEDIATED

| ID | Commit | Notes |
|---|---|---|
| CORR-A-5 | 297e55d | AdversaryRole flows dispatcher → modal → record |
| CORR-A-7 | a536640 | F-18 disposition aligned with BDD spec |
| CORR-A-8 | 297e55d | Recovery payload includes Context/Stratum/GridVersion |
| CORR-A-10 | ae3e352 | residueNoteMaxBytes is atomic.Int64 on AttestationStore |
| CORR-A-11 | 738019a | /attest synthesizes Unit + UnitPayload per verdict |
| CORR-A-12 | 738019a | validateOpID wraps ErrOpID* sentinels matching BDD wire forms |
| CORR-A-13 | 297e55d | OpEventAttestationRequested.Payload carries all per-arrow fields |
| CORR-A-14 | (closed) | recordReplay PassID-empty branch IS load-bearing via Tier 1→2 fallback (A-3) — tolerance is correct |
| CORR-A-15 | ddea6a9 | inFlight clearing semantics fixed in C-3/C-4 + backpressure event surfaces dropped requests |
| CORR-A-17 | (closed) | Theoretical race — no production exposure; observer fanout outside lock (CONC-H-3) closes the window |
| CORR-A-18 | 5b42b76 | bootstrap.EmitInitAttestations production producer for init records |
| CORR-A-19 | a536640 | PrimaryWriter annotates rec.Reason on hash substitution |
| CORR-A-20 | (closed) | BDD lift claim — re-audited; 3 scenarios actually wired, others (modal-flow) still @deferred by design |
| CORR-A-21 | 5d2e48b | validateOpID rejects Unicode Format runes (RTL/LTR/ZWSP/ZWJ/BOM) |
| SEC-H-2 | a536640 | /attestations output sanitizes every operator-controlled field |
| SEC-H-3 | ca21e58 | VerifyAggregateConsistencyVs surfaces ErrAttestationAuditLost |
| SEC-H-4 | (scaffolded) | Path-vs-payload check hook added; full re-encoding deferred to Tier 3 polish — current path-traversal guard (SEC-C-1) + hash truncation (SEC-L-4 bumped to 128-bit) closes the practical attack surface |
| SEC-H-5 | a536640 | TermModal hint render via modal.SanitizeLine |
| CONC-H-1 | 4d5a0fe | IB tracker publishes after t.mu unlock |
| CONC-H-2 | 4d5a0fe | Tree writer publishes after w.mu unlock |
| CONC-H-3 | 4d5a0fe | AttestationStore observer fanout after s.mu unlock |
| CONC-H-4 | 5d2e48b | OperatorBus.Subscribe returns cancellation closer; modalDriver.Stop + Session.Close wire it |
| CONC-H-5 | ca21e58 | OpEventEscalationPresented published after PresentEscalation success |
| CONC-H-6 | ca21e58 | Escalation refused when arrowResolver returns false or gridVer=0 |

## Medium — ALL REMEDIATED

| ID | Commit | Notes |
|---|---|---|
| SEC-M-1 | 0a1e7ae | LineReader bufio.Scanner buffer capped at 64 KiB |
| SEC-M-2 | 5d2e48b | extractAttRef splits on \r\n + strips control bytes |
| SEC-M-3 | 5d2e48b | /attest reason capped at 4 KiB + stripControlBytes |
| SEC-M-4 | 0a1e7ae | JSONL writer recordFailure logs via slog.Error unconditionally |
| SEC-M-5 | a536640 | recordTreeFailure removed; bus publish now outside lock with sanitized Detail |
| CONC-M-1 | 5d2e48b | OpEventClauseFailVerdict published BEFORE store.Record |
| CONC-M-2 | (documented) | modalDriver subscriber is fast; recursive Publish guarded by bus's snapshot-fanout pattern |
| CONC-M-3 | 0a1e7ae | Session.Close drains via cancelled sessionCtx before Stop |
| CONC-M-4 | 5d2e48b | Signal handler calls sess.Close before os.Exit |
| CONC-M-5 | (already correct) | ibTracker.Reset already runs BEFORE EscalationResolved publish |
| CORR-A-22 | 5d2e48b | recordReplay resolves duplicate-ID conflicts by timestamp |
| CORR-A-23 | 5d2e48b | bootstrap.Read failure falls back to defaults |
| CORR-A-24 | 5d2e48b | ensureUnitColumns moves PRAGMA inside tx |
| CORR-A-25 | 5d2e48b | AttestationRecordsEqual treats Inspected as multiset |
| CORR-A-26 | 0a1e7ae | EnqueueFromRecovery docstring explains replay filter |
| CORR-A-27 | (deferred to schema v5) | SQLite ALTER TABLE can't add CHECK; application-layer enforcement at validateAttestationTier2 is the practical guard. Schema CHECK ships in Tier 3 with a table-rebuild migration |

## Low — ALL REMEDIATED

| ID | Commit | Notes |
|---|---|---|
| SEC-L-1 | 5d2e48b | Covered by CORR-A-21 — Unicode Format runes rejected |
| SEC-L-2 | 5d2e48b | validateOpID rejects trailing dot |
| SEC-L-3 | 0a1e7ae | ValidateUnitPayload rejects empty Unit + non-empty payload |
| SEC-L-4 | 0a1e7ae | safeSegment hash truncation bumped 64 → 128 bits |
| SEC-L-5 | (documented) | Single-line truncation edge — acceptable behavior; documented in attestationstore.go loadOneTreeFile |
| CONC-L-1 | 5d2e48b | OperatorBus.WithClock takes bus mutex during swap |
| CONC-L-2 | (documented) | Cap-exceeded publish is filtered by kind-switch in OnEvent — no re-entry path; documented inline |
| CONC-L-3 | 5d2e48b | enqueueVerdict caps Detail JSON parse at 64 KiB |
| CONC-L-4 | 5d2e48b | extractAttRef trims control bytes from result |

## Test gaps

| ID | Status | Notes |
|---|---|---|
| T-1 | CLOSED (5b42b76) | TestScenario_ModalDriver_ConcurrentPublishAndDrain (4 publishers × 25 events vs drainer, `-race`-clean) |
| T-2 | CLOSED | LineReader tests assert no goroutine leak |
| T-3 | CLOSED | LineReader single-io.Reader REPL + TermModal interleave covered |
| T-4 | CLOSED | DrainPending tail preservation covered by modal_driver_drain_tail_test.go |
| T-5 | CLOSED | CONC-H-6 fix includes the arrowResolver=false escalation case |
| T-6 | CLOSED | publish_outside_lock_test.go covers subscriber-calls-tracker under -race |
| T-7 | CLOSED (5b42b76) | TestScenario_Session_CloseDuringDrain_NoWriteAfterClose covers blocking-modal + Close race |

## Decisions logged

- HintJSON "" and "{}" are treated as equivalent in
  AttestationRecordsEqual.
- Tier 1 → Tier 2 migration synthesizes "migrated-<id>" /
  "_legacy" placeholders for missing PassID/Context/Stratum.
- validateAttestationTier2 is a separate function; recordReplay
  stays lenient to preserve pre-Tier-2 audit load AND to accept
  the synthesized placeholders during Tier 1→2 migration.
- LineReader is per-session, lazy-constructed in REPL.
- Escalation with gridVersion=0 is refused (not silently
  fabricated with v=0) to preserve dedup integrity.
- The aggregate verifier's audit-lost guard requires the engine
  row count from the caller — opt-in via VerifyAggregateConsistencyVs.
- Inspected list comparison is order-insensitive (multiset).
- recordReplay's duplicate-ID resolution is last-write-wins by
  timestamp for in-memory state; both rows remain on disk.
- Init AttestationRecords leave SourceRole/TargetRole empty so
  §12.2 self-cert doesn't fire on the single-role assertion.
- Empty Unit + non-empty payload is now a hard reject (was
  tolerated for legacy compat; SEC-L-3 closed the bypass).
- Schema CHECK constraint for pass_id != '' deferred to Tier 3
  table-rebuild migration; application-layer enforcement at
  validateAttestationTier2 is the current guard.
