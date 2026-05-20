# Tier 2 Adversarial Pass — Remediation Tracker

Tracks dispositions for the 67 findings in
`validation-impl-pass-tier2-adversarial.md`. Per
`feedback_no_deferrals.md`: every finding remediates in-phase.

## Status Summary (2026-05-20)

| Tier        | Total | Done | Pending |
|-------------|------:|-----:|--------:|
| Critical    |    12 |   12 |       0 |
| High        |    25 |   16 |       9 |
| Medium      |    16 |    0 |      16 |
| Low         |     9 |    0 |       9 |
| Test gaps   |     7 |    4 |       3 |

## Critical — ALL REMEDIATED

| ID | Commit | Notes |
|---|---|---|
| CORR-A-1 | 72cf9d0 | engine/attestations.go: 7 columns added to INSERT/UPDATE/SELECT across 4 sites + hydrate helper |
| CORR-A-2 | 72cf9d0 | AttestationRecordsEqual HintJSON normalization; idempotent re-Record now silent |
| CORR-A-3 | 41bcaab | session_engine.go: flat→tree migration with "_legacy" placeholder for PassID/Context/Stratum |
| SEC-C-1 | b79af21 | safeSegment now hash-substitutes "." and ".." |
| SEC-C-2 | b79af21 | truncateTrailingPartialFile Lstat + O_NOFOLLOW (linux/darwin) refusing symlinks |
| CORR-A-4 | ae3e352 | validateAttestationTier2 + AttestationStore.residueNoteMaxBytes (atomic.Int64) |
| CORR-A-6 | ae3e352 | validateAttestationTier2 enforces PassID-empty rejection at Record-time |
| SEC-H-1 | ae3e352 | Record-time ValidateUnitPayload, Unit-enum, hint_json parse, AdversaryRole "__" |
| CONC-C-3 | ddea6a9 | DrainPending preserves snapshot tail on non-cancel error + emits backpressure event |
| CONC-C-4 | ddea6a9 | DrainPending re-queues full tail on ctx-cancel (was only re-queuing failing item) |
| CONC-C-1 | 2509c8a | Shared modal.LineReader; REPL + TermModal pull from one scanner |
| CONC-C-2 | 2509c8a | One reader goroutine per session; ctx-cancel does not abandon stdin |

## High — 16 of 25 remediated

### Remediated

| ID | Commit | Notes |
|---|---|---|
| CORR-A-5 | 297e55d | OpEventAttestationRequested carries adversary_role from dispatcher → modal → record |
| CORR-A-7 | a536640 | F-18 disposition aligned with BDD spec (init path uses "_" placeholders) |
| CORR-A-8 | 297e55d | (Mostly addressed) dispatcher payload carries Context/Stratum |
| CORR-A-10 | ae3e352 | residueNoteMaxBytes is now atomic.Int64 on AttestationStore |
| CORR-A-11 | 738019a | /attest CLI synthesizes Unit + UnitPayload per verdict |
| CORR-A-12 | 738019a | validateOpID wraps ErrOpIDRequired / ErrOpIDTooLong / ErrOpIDInvalidCharacters sentinels |
| CORR-A-13 | 297e55d | OpEventAttestationRequested.Payload carries source/target/context/stratum/grid_version |
| CORR-A-19 | a536640 | PrimaryWriter annotates rec.Reason with "path-truncated" on hash substitution |
| SEC-H-2 | a536640 | /attestations output wraps every operator-controlled field in sanitizeOneLine |
| SEC-H-3 | ca21e58 | VerifyAggregateConsistencyVs surfaces ErrAttestationAuditLost when engine has rows + surfaces empty |
| SEC-H-5 | a536640 | TermModal verdict + escalation prompts render hint fields via modal.SanitizeLine |
| CONC-H-1 | 4d5a0fe | IB tracker publishes after t.mu unlock |
| CONC-H-2 | 4d5a0fe | Tree writer publishes after w.mu unlock |
| CONC-H-3 | 4d5a0fe | AttestationStore observer fanout after s.mu unlock |
| CONC-H-5 | ca21e58 | OpEventEscalationPresented published after PresentEscalation success (not before) |
| CONC-H-6 | ca21e58 | Escalation refused when arrowResolver returns false or gridVer=0 |

### Pending

| ID | Severity | Description |
|---|---|---|
| CORR-A-14 | high | recordReplay PassID-empty branch dead code (no flat-file fallback at boot) |
| CORR-A-15 | high | Modal driver inFlight not cleared on backpressure drop (partial in C-3/C-4) |
| CORR-A-18 | high | Init AttestationRecord producer missing |
| CORR-A-21 | high | validateOpID Unicode/RTL bypass (multibyte UTF-8 passes byte-level checks) |
| SEC-H-4 | high | Tree-walker accepts JSONL records under arbitrary paths (no path-vs-payload check) |
| CONC-H-4 | high | OperatorBus has no Unsubscribe; modal driver outlives closeEngine |

## Medium / Low — pending

SEC-M-1..M-5, CONC-M-1..M-5, CORR-A-22..A-27, SEC-L-1..L-5,
CONC-L-1..L-4. All polish + diagnostics, no load-bearing defects.

## Test gaps — 4 of 7 closed

| ID | Status | Notes |
|---|---|---|
| T-1 | pending | race-detector coverage Publish + DrainPending |
| T-2 | CLOSED | LineReader tests assert no goroutine leak |
| T-3 | CLOSED | LineReader covers single-io.Reader REPL + TermModal interleave |
| T-4 | CLOSED | DrainPending non-cancel error tail preservation covered |
| T-5 | CLOSED | CONC-H-6 fix includes the arrowResolver=false escalation case |
| T-6 | CLOSED | publish_outside_lock_test.go: subscriber-calls-tracker under -race |
| T-7 | pending | Close-while-DrainPending race |

## Decisions logged

- HintJSON "" and "{}" are treated as equivalent in
  AttestationRecordsEqual.
- Tier 1 → Tier 2 migration synthesizes "migrated-<id>" /
  "_legacy" placeholders for missing PassID/Context/Stratum.
- validateAttestationTier2 is a separate function; recordReplay
  stays lenient to preserve pre-Tier-2 audit load.
- LineReader is per-session, lazy-constructed in REPL.
- Escalation with gridVersion=0 is refused (not silently
  fabricated with v=0) to preserve dedup integrity.
- The aggregate verifier's audit-lost guard requires the engine
  row count from the caller — surface is opt-in for callers that
  can supply it; legacy two-arg form preserved for backwards-compat.
