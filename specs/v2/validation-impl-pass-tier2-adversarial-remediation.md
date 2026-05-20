# Tier 2 Adversarial Pass — Remediation Tracker

Tracks dispositions for the 67 findings in
`validation-impl-pass-tier2-adversarial.md`. Per
`feedback_no_deferrals.md`: every finding remediates in-phase.

## Status Summary

| Tier        | Total | Done | Pending |
|-------------|------:|-----:|--------:|
| Critical    |    12 |    8 |       4 |
| High        |    25 |    1 |      24 |
| Medium      |    16 |    0 |      16 |
| Low         |     9 |    0 |       9 |
| Test gaps   |     7 |    0 |       7 |

## Critical — Dispositions

### Remediated

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

### Pending

| ID | Severity | Description |
|---|---|---|
| CORR-A-5 | critical | AdversaryRole has no production producer — orchestrator never stamps it |
| CORR-A-12 | high | /op-id rejection messages don't match BDD wire forms |
| CORR-A-13 | high | OpEventAttestationRequested carries no Context/Stratum/SourceRole/TargetRole/GridVersion |
| CORR-A-11 | high | /attest CLI bypasses unit-payload validation (Unit empty + no flag) |

## High — Pending

CORR-A-7 (init-path doc stale), CORR-A-8 (recovery republish loses
Context/Stratum), CORR-A-10 (residueNoteMaxBytes was racy; superseded
by atomic.Int64 in CORR-A-4 commit — verify), CORR-A-14, CORR-A-15,
CORR-A-18 (init AttestationRecord producer), CORR-A-19 (path-truncation
Reason annotation), CORR-A-21 (op-id Unicode/BiDi), SEC-H-2..H-5
(op-id replay, aggregate divergence false-neg, tree-vs-payload
divergence, ANSI smuggle), CONC-H-1..H-6 (publish-outside-lock,
escalation invariants, bus Unsubscribe).

## Medium — Pending

SEC-M-1..M-5, CONC-M-1..M-5, CORR-A-22..A-27. Mostly diagnostics + edge cases.

## Low — Pending

SEC-L-1..L-5, CONC-L-1..L-4. Hardening polish.

## Test gaps — Pending

T-1: race-detector coverage Publish + DrainPending
T-2: TermModal ctx-cancel asserts no goroutine leak (LineReader test covers this — CLOSE)
T-3: single-io.Reader REPL + TermModal interleave (LineReader test covers — CLOSE)
T-4: DrainPending error-mid-snapshot tail preservation (covered by drain-tail tests — CLOSE)
T-5: arrowResolver false for escalation (still pending)
T-6: ibTracker subscriber re-entry under -race (pending; needs publish-outside-lock fix)
T-7: Close-while-DrainPending race (pending)

## Decisions logged

- HintJSON "" and "{}" are treated as equivalent in
  AttestationRecordsEqual (decision: spec defaults to "{}", engine
  default is "" — normalize at compare to keep idempotent re-Record
  silent for all callers).
- Tier 1 → Tier 2 migration synthesizes "migrated-<id>" /
  "_legacy" placeholders for missing PassID/Context/Stratum so the
  tree-write succeeds without mutating in-memory records.
- validateAttestationTier2 is a separate function; recordReplay
  stays lenient to preserve pre-Tier-2 audit load.
- LineReader is per-session, lazy-constructed in REPL — NewSession
  defers TermModal.Lines binding to the REPL-time wire-up so test
  fixtures don't accidentally read os.Stdin.
