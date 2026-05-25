# Final adversarial on diamond closure — 2026-05-25

## Summary

Cold-context final adversarial pass on the integrator-closure pair
(`483d123` code closures for I-C-1 + 3 Highs + 3 Mediums, `9d05176`
6 doc edits). READ-ONLY review of every claim in the integrator's
remediation, with end-to-end re-trace of the new `/invalidate-arrow`
producer chain, I-H-3 TOCTOU verification, I-H-2 cycle-event order +
race semantics, I-M-1 swap-then-abort regression risk, doc-drift
spot-check across 7 surfaces, and untouched-finding audit on the 7
items the wiring adversarial flagged as deferred to the integrator.

**Verdict: Yes-after-3-fixes.** The diamond is structurally sound,
race-clean, at coverage 78.5%, but two contract drifts (one Critical,
one Medium) and one doc gap surfaced that warrant one more closure
cycle before push. The Critical is small (a Payload-key mismatch
between the producer and ADR-v4-005); fixable in ~5 lines.

Counts: **1 Critical, 0 High, 3 Medium, 2 Low.**

## Critical (must close before push)

| ID | Title | Code site | Why critical | Fix |
|---|---|---|---|---|
| F-C-1 | `/invalidate-arrow` producer's typed Payload diverges from ADR-v4-005 contract | `cmd/ghyll/invalidate_arrow_cmd.go:158-170` publishes `Payload: {arrow_id, op_id, reason, source}`. ADR-v4-005 line 40 says the required Payload keys for `OpEventArrowInvalidated` are `arrow_id, op_id, reason, timestamp`. `source` is not in the ADR; `timestamp` is in the ADR but not in the Payload (it's only on `e.Timestamp`, which is a different surface — subscribers reading `Payload["timestamp"]` see empty). The ADR explicitly says "A single unit test (`TestScenario_OperatorBus_PayloadContract_OutcomeKeyConsistency`) enforces the contract across event kinds"; that test does not exist in the codebase (grep finds zero hits). The closure ships the producer the integrator pass demanded but ships it against an internal-only schema instead of the ADR contract. | The whole point of the I-C-1 closure was to ship the producer per its accepted ADR. Shipping the producer with a Payload that doesn't satisfy its ADR's "required keys" list is the same shape of structural incompleteness the integrator pass identified (consumer/contract without producer) — now reflected as producer-without-contract-compliance. Subscribers that consult ADR-v4-005 to read `Payload["timestamp"]` get the empty string, not the operator's timestamp. | Two-line change: add `"timestamp": e.Timestamp.UTC().Format(time.RFC3339Nano)` to the Payload literal at invalidate_arrow_cmd.go:164-169. Either drop `source` (engine schema's `source` column already defaults to `"operator"` in the observer code at session_engine.go:664) OR update ADR-v4-005 line 40 to list `source` as a fifth required key. Also: ship the missing `TestScenario_OperatorBus_PayloadContract_OutcomeKeyConsistency` the ADR claims exists — otherwise this exact category of drift recurs on every new event kind. |

## High (should close; would generate a follow-up GH issue if shipped)

(none this round)

## Medium (acceptable to ship; flag for next polish sprint)

| ID | Title | Code site | Notes |
|---|---|---|---|
| F-M-1 | `OpEventAdversarialRoundStart` Payload missing 3 of 6 ADR-required keys | `cmd/ghyll/dispatcher_adversarial.go:86-95` publishes Payload `{tier_label, sensitive_clauses}`. ADR-v4-005 line 33 requires `arrow_id, pass_id, round, rounds_max, open_findings, tier_label`. Three of six required keys are populated only on the typed top-level fields (ArrowID, PassID) but not in the Payload; `round`, `rounds_max`, `open_findings` are entirely absent. The /run-arrow filter (I-H-2) captures the event and renders e.Detail, so the operator surface degrades silently. Same shape of drift as F-C-1, lower-severity because the consumer side (modal driver + /run-arrow filter) does not yet switch on these keys. | Add the 4 missing keys to the dispatcher_adversarial.go publish. Same fix pattern as F-C-1. |
| F-M-2 | `OpEventRemediationConverged` / `OpEventRemediationEscalated` Payload key mismatch | `cmd/ghyll/dispatcher_adversarial.go:173-176` publishes Payload `{outcome, rounds_executed}`. ADR-v4-005 line 35 requires `arrow_id, pass_id, outcome, rounds_used, reason` (escalated also requires `reason`). `rounds_executed` vs `rounds_used` is a key-name mismatch (subscribers reading `Payload["rounds_used"]` see empty); `arrow_id`/`pass_id`/`reason` are missing from the Payload entirely. Same drift shape. | Rename to `rounds_used`; add `arrow_id`, `pass_id`, and (for escalated) `reason` to the Payload. |
| F-M-3 | W-M-1, W-M-3, W-M-4, W-L-1, W-L-2 from prior wiring adversarial: **all 5 still open** | The integrator pass closed W-M-2 / W-L-3 / W-H-1..W-H-4 + the C / I-H-1..3 / I-M-1..3 set. The wiring adversarial's W-M-1 (no /help, /status doesn't list commands), W-M-3 (arrow_invalidations insert failure logs only, no bus republish per ADR-015 Part C pattern), W-M-4 (drain-amendments uses `continue` on per-amendment failure, no first-error abort), W-L-1 (uniquedef + predicateform yaml-path walker leaf-shape divergence), W-L-2 (`remediationConvergedDriver` is a copy not a re-export of `runner.remediationConverged`) were flagged "for the next polish sprint" — they are still open. None blocks the push but the integrator's report doesn't acknowledge they were knowingly deferred (the integrator-pass mentions only I-* findings as outstanding before push), so the audit-trail signal is muddled. | Either accept them explicitly in the integrator pass (3-line addendum noting "wiring adversarial W-M-{1,3,4} + W-L-{1,2} accepted as next-sprint polish") or close them now (each is ~10-30 lines). Recommended: explicit acknowledgement in the integrator-pass doc. |

## Low (acceptable to ship)

| ID | Title | Code site | Notes |
|---|---|---|---|
| F-L-1 | `parseInvalidateArrowArgs` does not strip quotes from `--reason "stale spec"` | `cmd/ghyll/invalidate_arrow_cmd.go:50-87`. Operator typing `/invalidate-arrow A1 --reason "stale spec"` lands the literal string `"stale spec"` (quote chars preserved) in the audit row. The reason text is operator-supplied free-text so this is cosmetic, but the audit row's reason column will contain the quote characters. | Strip leading/trailing `"` and `'` from the captured reason before storing. ~3 lines. |
| F-L-2 | `cli-reference.md` + `getting-started.md` minimum-slash-commands tables don't list the 3 new commands | `docs/usage/cli-reference.md:115-129` slash-commands table lists `/op-id`, `/attest`, `/attestations`, `/passes`, `/list-arrows`, `/run-arrow`, `/<name>` but not `/drain-amendments`, `/adversary`, `/invalidate-arrow`. `docs/usage/getting-started.md:171-183` minimum-daily-driver list has the same gap. The doc-edit commit (9d05176) updated `operator-guide.md` + `CLAUDE.md` + `README.md` + `amendment.md` + 2 ADRs but missed these 2 surfaces. A new operator reading `cli-reference.md` (the "canonical" CLI reference) will not learn the diamond v4 commands exist. | 2 single-line additions per file. ~10 lines total. |

## End-to-end re-audit

| Item | Production caller present | Production producer present | Tested E2E | Pass/Fail |
|---|---|---|---|---|
| 1. `/invalidate-arrow` arg-parse (positional + --reason) | session.go:1398 | invalidate_arrow_cmd.go:50 | parseInvalidateArrowArgs unit tests | PASS |
| 2. `/invalidate-arrow` no-op-id refusal | invalidate_arrow_cmd.go:105 | n/a | TestScenario_InvalidateArrow_SlashCommand_NoOpID | PASS |
| 3. `/invalidate-arrow` unknown-arrow refusal | invalidate_arrow_cmd.go:125 | n/a | TestScenario_InvalidateArrow_SlashCommand_ArrowNotInGrid | PASS |
| 4. `/invalidate-arrow` bus.Publish chain | invalidate_arrow_cmd.go:158 | bus + observer + sqlite | TestScenario_InvalidateArrow_SlashCommand_PublishesAndPersists asserts producer→bus→observer→sqlite row | PASS (but Payload-key drift, see F-C-1) |
| 5. `/invalidate-arrow` REPL dispatch | session.go:1398 | n/a | TestScenario_InvalidateArrow_DispatchWiredThroughSession | PASS |
| 6. I-H-1 unsubscribe on closeEngine | session_engine.go:660,732 | n/a | covered by closeEngine test paths | PASS |
| 7. I-H-2 /run-arrow filter captures 3 cycle events | run_arrow_cmd.go:195-197 | dispatcher_adversarial.go publishers | TestScenario_RunArrow_FilterCapturesAdversarialEvents | PASS |
| 8. I-H-2 /run-arrow renderer emits per-event lines | run_arrow_cmd.go:252-269 | n/a | TestScenario_RunArrow_AdversarialCycleEventsRenderedEndToEnd | PASS |
| 9. I-H-2 snapshot-after-unsubscribe (L-A pattern) | run_arrow_cmd.go:229-232 | n/a | covered by L-A test pattern | PASS |
| 10. I-H-3 TOCTOU fix: phase fn receives loaded hooks | dispatcher.go:309 (passes loadedHooks) | dispatcher_adversarial.go:49 (accepts hooks param) | dispatcher_wiring_test.go updated | PASS — second Load() truly eliminated from production path (defensive fallback at adversarial.go:62 only used by direct test paths) |
| 11. I-M-1 swap-then-abort regression | amendment_commit.go:231 (swap before line 243 abort) | n/a | amendment_commit_diamond_v4_test.go | PASS |
| 12. I-M-2 Registry.Registered predicate | runner.go (Registered method) | n/a | concept_registrykey_test.go | PASS |
| 13. I-M-3 typed Payload on AmendmentDrained | amendment_commit.go:288-296 | n/a | TestScenario_OperatorBus_PayloadContract_AmendmentDrained | PASS |

**13/13 PASS.** F-C-1's drift is on Payload-key compliance, not on the production chain functioning end-to-end (the row writes, the events emit, the modal-driver dispatches). The chain is structurally complete; the contract is the drift.

## Doc-drift re-audit

| Slash command | operator-guide | CLAUDE.md | README | glossary | why | cli-reference | getting-started |
|---|---|---|---|---|---|---|---|
| `/invalidate-arrow` | YES (l.139) | YES (l.169) | YES (l.217) | n/a (no slash table) | n/a (narrative) | **NO** | **NO** |
| `/drain-amendments` | YES (l.137) | YES (l.167) | YES (l.216) | n/a | n/a | **NO** | **NO** |
| `/adversary` | YES (l.138) | YES (l.168) | YES (l.215) | n/a | n/a | **NO** | **NO** |

Doc-drift: **3 of 7 surfaces have all 3 commands** (operator-guide, CLAUDE.md, README), **2 surfaces have slash-command tables but missed the new commands** (cli-reference, getting-started — see F-L-2), **2 surfaces are narrative-only and don't need slash-command tables** (glossary, why).

Counting per slash command across the 5 surfaces that have tables: 3 of 5 PASS for each command.

## Untouched-finding audit (W-M-1..W-M-4, W-L-1..W-L-3)

| Finding | Still open? | Severity if shipped |
|---|---|---|
| W-M-1 (/status doesn't list commands, no /help) | **Open** | Medium — discoverability gap |
| W-M-2 (audit-tag subscriber unsubscribe) | **Closed** in ca6c827 | (n/a — closed) |
| W-M-3 (arrow_invalidations insert-failure: log only, no bus-republish) | **Open** | Medium — silent row loss without ADR-015 Part C semantics |
| W-M-4 (drain-amendments partial-drain `continue` semantics) | **Open** | Medium — operator-attributable state divergence in error paths |
| W-L-1 (yaml-path walker leaf-shape divergence) | **Open** | Low — uniformity/maintenance |
| W-L-2 (`remediationConvergedDriver` is copy not re-export) | **Open** | Low — drift surface |
| W-L-3 (`/adversary enable` operator-facing claim) | **Closed** in ca6c827 (real "dialect=X bundle wired" output) | (n/a — closed) |

Status: **5 of 7 deferred items still open** (W-M-1, W-M-3, W-M-4, W-L-1, W-L-2). All are non-blocking. The integrator pass closed the 2 that were structural-correctness adjacent (W-M-2, W-L-3). The remaining 5 are operator-UX (W-M-1) + observability (W-M-3) + UX (W-M-4) + maintenance (W-L-1, W-L-2). See F-M-3 for the recommendation to acknowledge them in the integrator pass doc rather than close them inline.

## Build / race / coverage

- **`make` (lint + test + build):** PASS (acceptance suite 118.6s, build clean)
- **`go test -race -count=1 -short -timeout 240s ./...`:** PASS (22 packages clean under -race, no detector hits)
- **`make coverage-check`:** PASS — **78.5%** (above 78% floor; matches commit-message claim)
- **`go vet ./...`:** clean (exit 0, no output)

## Verdict

**Yes-after-3-fixes.**

The diamond v4 closure chain is structurally sound. The two closure
commits (483d123, 9d05176) close the integrator pass's I-C-1, I-H-1,
I-H-2, I-H-3, I-M-1, I-M-2, I-M-3 as documented. All 13 end-to-end
checks pass; the production chain from `ghyll run` → typed
`/invalidate-arrow A1 --reason X` → bus.Publish → observer →
arrow_invalidations row is intact and tested. The race detector is
clean across 22 packages; coverage is 78.5% (above floor); `make`
is green.

The three pre-push fixes:

1. **F-C-1 (Critical):** Bring `/invalidate-arrow` producer Payload
   into compliance with ADR-v4-005 — add `timestamp` key, decide
   whether `source` stays or is dropped, and ship the missing
   `TestScenario_OperatorBus_PayloadContract_OutcomeKeyConsistency`
   the ADR claims exists. ~10 lines + ~30-line test.
2. **F-L-2 (Low):** Add `/drain-amendments`, `/adversary`,
   `/invalidate-arrow` to `cli-reference.md` and `getting-started.md`
   slash-command tables. ~10 lines.
3. **F-M-3 (Medium):** Add a 3-line addendum to the integrator-pass
   doc acknowledging W-M-1, W-M-3, W-M-4, W-L-1, W-L-2 as next-sprint
   polish (or close them inline).

F-M-1 and F-M-2 (Payload-key drift on the 3 adversarial-cycle events)
are recommended for the same closure cycle since the fix shape is
identical to F-C-1 and the cost of fixing later is "we re-open this
adversarial round." F-L-1 is cosmetic and can defer.

After these 3 fixes (single commit + 1 doc edit + 1 spec addendum),
the chain can push. The 4 prior remediation cycles did genuine work;
the only drift remaining is contract compliance on the event Payload
contracts that ADR-v4-005 documents — a category of finding that
isolated single-context passes are structurally bad at detecting,
because each producer was reviewed against its own consumer surface
rather than against the ADR's central typed-payload contract.
