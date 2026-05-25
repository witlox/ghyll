# Adversarial on implementer commits — 2026-05-25

Cold-context, READ-ONLY adversarial pass on the four diamond-v4 gap
commits (b1119be, e971eb0, 071198a, 57ab330) plus the eight ADRs
under `docs/decisions/v4/`. Scope: deferred-item audit, wiring
verification, contracted test presence, race + coverage.

## Summary

Four commits land the **substrate** for the four gaps. None of the
four gaps' **production seams** are wired. The artifact is a Lego
set: every brick is shaped correctly, none are snapped together.

- **Substrate quality:** high. Unit tests pass, race-clean,
  go vet clean.
- **Production reachability:** **zero** of the four gaps actually
  fixes the user-visible defects code-eval-2026-05-25.md identified.
- Every clause requiring a language binding still crashes with
  `ErrConceptNotRegistered` because `registerGridBindings` is never
  invoked from `session.go` / `openEngine`.
- Every adversarial cycle still runs only from tests because
  `PassDispatcher` has no `Hooks` field and no production path
  calls `runDispatcherAdversarialPhase` (helper does not exist).
- Every amendment in the queue still stays in the queue forever
  because `engineRuntime` has no `committer`, no `/drain-amendments`
  case in session.go, and the bus's `arrow_invalidations` table
  does not exist in the engine schema.
- The user's standing rule "no deferrals on adversarial-pass
  findings" was violated: the implementer flagged 3 explicit
  `TODO(diamond-v4)` items in commit messages (plus 2 inline TODOs
  in runner/uniquedef.go and runner/predicateform.go).

The four gaps are NOT closed. The substrate is necessary but
insufficient. The integrator pass cannot proceed.

## Critical (must close before integrator)

| ID | Title | Commit | Code site | Why critical | Fix |
|---|---|---|---|---|---|
| C-1 | `registerGridBindings` is dead code in production | e971eb0 | `cmd/ghyll/binding_register.go:60` (defined); zero production callsite — only `binding_register_test.go` invokes it | This is **the entire point** of Gap 3. Without it, any arrow clause with a language-bound concept (compiles, lint-clean, tests-pass, mutation-score, every-step-bound, no-orphan-symbol, acyclic-dependency-graph) raises `ErrConceptNotRegistered` at evaluate-time. The Gap 3 commit message admits this: "the end-to-end session-engine integration … is deferred to a follow-up PR". The defect code-eval-2026-05-25.md identified is **unchanged in production**. | Plumb `*bootstrap.Grid` through `openEngineWithOptions`; call `registerGridBindings(rt.registry, grid, workdir)` after grid construction; call `verifyBindingsCoverage` post-Replay. |
| C-2 | `verifyBindingsCoverage` never invoked post-Replay | e971eb0 | `cmd/ghyll/binding_register.go:221` (defined); `session.go` does not call it | The R17/R18 closures (dedup + arg validation + missing-coverage error surfacing) are part of Gap 3's contract per `diamond-load-bearing-revised-v2.md:2310`. Without this call, a typo in the grid file silently passes startup and then crashes the first arrow that needs the binding. | Call `verifyBindingsCoverage(rt.registry, rt.grid, untypedGrid)` after `replayEngine` returns; treat `*MissingBindingError` as a hard session-open error. |
| C-3 | `engineRuntime` has no `committer`; `AmendmentCommitter.Commit` is unreachable in production | 071198a | `cmd/ghyll/session_engine.go:48-105` (struct definition has 7 fields including `passes`, `amendments`, `bus`; no `committer`). Zero production reference to `AmendmentCommitter` outside `runner/amendment_commit_*_test.go`. | This is **the entire point** of Gap 2. The queue still accumulates indefinitely; no amendment ever applies to the live grid; the snapshot-swap mechanism + FIFO check are never exercised at runtime. The Gap 2 commit message admits this: "the drain-amendments slash command, the engineRuntime.committer construction at session open, … land in a follow-up PR". | Add `committer *runner.AmendmentCommitter` to `engineRuntime`; construct in `openEngineWithOptions` with `Grid`, `LiveRegistry`, `Amendments`, `Findings`, `Passes`, `Workdir`, `BindingsReRegister: registerGridBindings`; expose `engineRuntime.Committer()` for the slash command. |
| C-4 | `/drain-amendments` slash command does not exist | 071198a | `cmd/ghyll/session.go:1302-1347` (5 cases: `/exit /deep /plan /fast /status`); no `/drain-amendments`. | The operator cannot trigger an amendment commit. The substrate is reachable from `engineRuntime.committer` (when wired) but the chat-loop has no UX path. Spec contract per `diamond-load-bearing-revised-v2.md`; commit message admits "the drain-amendments slash command … lands in a follow-up PR". | Add `case "/drain-amendments":` in `DispatchSlashCommand`; drain head of `engineRuntime.Amendments()`, build CommitRequest, invoke `committer.Commit`, surface OpEventAmendmentEnqueueRefused / OpEventAmendmentDrained to the modal. |
| C-5 | `arrow_invalidations` table never created in engine schema | 071198a + ADR-v4-008 | `engine/store.go:124-377` defines 11 tables; `arrow_invalidations` is not one. Zero grep hits for `arrow_invalidations` in `engine/`. | `OpEventArrowInvalidated` is published, journal observer claims to INSERT to `arrow_invalidations` per the commit message, but the target table does not exist. A live emit of this event in production will fail at the prepared-statement layer the first time the journal sees one. | Add `migrateAddArrowInvalidations` per ADR-v4-008; create the table with the columns from ADR §29-37; call from `engine.OpenStore` after the existing CREATE TABLE block. |
| C-6 | `migrateAddRemediationColumns` not shipped; `passes.remediation_outcome` + `passes.remediation_rounds_used` absent | 071198a + ADR-v4-008 | `engine/store.go:365-379` defines `passes` without these columns. Zero grep hits for `remediation_outcome`/`remediation_rounds_used` in `engine/`. | ADR-v4-008 specifies these as MUST-ALTER on open. Without them, the dispatcher cannot persist the cycle's outcome — RemediationReport has nowhere to land. Inconsistent with ADR-v4-008's "Status: Accepted". | Add `migrateAddRemediationColumns` per ADR §29-33; idempotent ALTER TABLE on `passes`. |
| C-7 | `PassDispatcher` has no `Hooks` field; adversarial cycle never runs in production | 57ab330 | `runner/dispatcher.go:61-106` (PassDispatcher struct: 10 fields; no `Hooks`). `runner/dispatcher.go:184-326` Dispatch does NOT partition clauses, does NOT call `RunRemediationLoop`, does NOT call any AdversarialHooks. | This is **the entire point** of Gap 1. AdversarialHooks bundle is constructible but no production caller wires it to a dispatcher. The Adversary.Attack code path remains test-only — exactly the defect code-eval-2026-05-25.md flagged. Commit message admits "The dispatcher's Dispatch path doesn't yet partition + invoke the cycle". | Add `Hooks *AtomicAdversarialHooks` field to PassDispatcher; in Dispatch, after running clauses, `sensitive, robust := PartitionClauses(req.Arrow.Clauses)` and if `len(sensitive) > 0`, call `runDispatcherAdversarialPhase` helper (also unshipped). |
| C-8 | `runDispatcherAdversarialPhase` helper does not exist | 57ab330 | Zero grep hits for `runDispatcherAdversarialPhase` in any `.go` file. | The dispatcher integration site lives in `cmd/ghyll` (per ADR-v4-007 logic — needs the dialect). Substrate ships error sentinels (`ErrAdversaryHooksNotWired`, `ErrDispatchNoAuditSubscriber`, `ErrDispatchRecursionExceeded`) and helpers (`PartitionClauses`, `RequireAuditSubscriber`, `CheckRecursionBudget`, `IncrementRecursionDepth`) — none are invoked. | Implement the helper per pseudocode in `diamond-load-bearing-revised-v2.md:1789-1850`: pre-check audit subscriber, partition, run loop, derive new arrow status (or set ArrowStatusAbortedRemediation on non-converged), persist RemediationReport. |
| C-9 | `/adversary` slash command does not exist | 57ab330 | `cmd/ghyll/session.go:1302-1347` does not include `/adversary`. | Operators cannot enable/disable the cycle at runtime (ADR-v4-002 "auto-enable" is one half; the manual override the ADR also references is unbuilt). `AtomicAdversarialHooks.Store(nil)` (disable) has no UX caller. | Add `case "/adversary":` parsing enable/disable; toggle the atomic pointer on PassDispatcher.Hooks (when C-7 lands). |
| C-10 | No production caller subscribes to the bus with the `"audit"` tag → `HasAuditSubscriber()` always returns false in production | 071198a | `cmd/ghyll/session_engine.go:217` calls `jw.WithBus(rt.bus)`, but the bus path it wires is **not** through `SubscribeTagged("audit")` (the JSONL writer subscribes via the AttestationStore observer, not the bus subscriber chain). Zero grep hits for `SubscribeTagged` outside tests. | The R6 closure depends on this tag: when C-7 lands and Dispatch calls `RequireAuditSubscriber(bus)`, it will refuse every dispatch in production because no subscriber carries the tag. Substrate ships a check that fails closed against a wiring gap. | Either subscribe the JSONL writer via `bus.SubscribeTagged(writer.OnEvent, "audit")` in `attachJournal`, or document that the audit floor is satisfied by the AttestationJSONLWriter.Observer path. |
| C-11 | Modal driver does not subscribe to the 4 new event kinds | 071198a + 57ab330 | `cmd/ghyll/modal_driver.go:137` subscribes once via `bus.Subscribe(d.OnEvent)` (the untagged fan-out catches the new events at the wire level), but the `OnEvent` switch in modal_driver.go does not have explicit cases for `OpEventAdversarialRoundStart`, `OpEventAmendmentEnqueueRefused`, `OpEventRecoveryAmendmentsPending`, `OpEventArrowInvalidated`. Without case handling, the events are dropped silently from the operator UI. Commit message admits "modal event subscriptions for OpEventAdversarialRoundStart et al." are deferred. | Verify (and fix) modal_driver.OnEvent dispatch on the four new event kinds. |

## High

| ID | Title | Commit | Code site | Fix |
|---|---|---|---|---|
| H-1 | `engineRuntime.Bus()` is wired but the recovery-pending banner is not | 071198a | `cmd/ghyll/session.go:341-369` surfaces `recoveryReport` but `OpEventRecoveryAmendmentsPending` is not emitted by the recovery path in `engine/`. | Add the emit in `engine.Recovery` when the amendments table has un-drained rows; modal subscribes via C-11. |
| H-2 | Two inline TODOs landed silently in Gap 4 evaluators despite the user's "no inline TODOs" guidance | b1119be | `runner/uniquedef.go:224` "TODO(diamond-v4): wire a proper yaml-path"; `runner/predicateform.go:181` "TODO(diamond-v4): proper yaml-path" | Either resolve now (best-effort yaml-path parse) or escalate; do not silently leave inline TODOs in v4 work. |
| H-3 | Coverage claim is 78.3%; measured 78.2% | b1119be | `make coverage` total: `78.2%` | Off by 0.1%; cosmetic but the implementer's claim is unverifiable. Tier 3 floor is 78% (Makefile line 244); above floor either way, but the claim should be reproducible. |

## Medium

| ID | Title | Commit | Code site | Fix |
|---|---|---|---|---|
| M-1 | `FindingRecord.GridVersion` is added but never stamped at the call site | 57ab330 | `runner/findings.go` adds the field; commit message says "the raise path stamps it from clause.GridVersion at the call sites in adversarial.go — the integrator-pass-close enqueue path will pick up the wire in a follow-up" — a third deferral the commit message does not flag with TODO(diamond-v4). | Audit Raise/Transition callsites; require non-zero GridVersion or document why zero is permissible. |
| M-2 | `AmendmentCommitter.LiveRegistry` is a field on the struct; no production constructor sets it | 071198a | `runner/amendment_commit.go:50-60` (LiveRegistry field defined); test fixture sets it, no production caller does (because no production caller constructs the committer — see C-3) | Resolved when C-3 lands. |
| M-3 | Two-table dispatch (`EvaluatorWithRunner` + plain Evaluator) doubles registry-lookup paths; no integrator-pass test verifies they don't disagree on the same key | b1119be | `runner/runner.go` (Registry.LookupWithRunner / Lookup), tests `TestScenario_Runner_LookupWithRunnerPath` + `TestScenario_Runner_LookupFallback` exist but do not cover the diamond case where both tables hold the same key | Add `TestScenario_Runner_BothTablesShareKey_PrefersWithRunner` (the spec says with-runner takes precedence). |

## Low

| ID | Title | Commit | Code site | Fix |
|---|---|---|---|---|
| L-1 | ADR-v4-002 references "auto-enable on dialect availability" but Gap 1's commit does not ship the auto-enable hook either | 57ab330 + ADR-v4-002 | The hook bundle Factory is constructed externally; no `cmd/ghyll` code constructs it from the live dialect (ADR-v4-007 says cmd/ghyll owns this) | When C-7/C-8/C-9 land, ensure the dialect-availability path actually constructs the bundle. |
| L-2 | `code-eval-2026-05-25.md` is the reference but commit messages cite it by line numbers that no longer match (file appears to have been edited post-commit) | all 4 | Spot-check shows the file is present but commit messages reference "lines that ship in catalogue.LoadEmbedded" without anchoring; the verification surface is brittle | Anchor by symbol name, not line, in future commit messages. |

## Deferred-item audit

| Item | Genuinely deferred? | Production impact if not closed | Fix scope |
|---|---|---|---|
| `openEngineWithOptions(workdir, log, ib, grid)` signature change | **YES** — current signature is `(workdir, logger, ibRoundsMax int)`, no Grid param. Gap 3 commit message admits the deferral. | **CRITICAL.** Without the grid, `registerGridBindings` cannot be called → Gap 3 produces zero behavioral change in production. | ~30 LoC: thread `*bootstrap.Grid` through `openEngineWithOptions`, refactor 1 production caller (session.go:323) + 2 test callers (tier0_wiring_test.go:22 and 157). |
| post-Replay `verifyBindingsCoverage` call | **YES** — no production callsite exists | **CRITICAL.** A typo in the grid file silently passes startup and crashes the first arrow that needs the binding. R17/R18 closure unenforced. | ~15 LoC in session.go after `rt.replayEngine` returns. |
| `engineRuntime.committer` construction | **YES** — `engineRuntime` struct has no `committer` field (verified at session_engine.go:48-105) | **CRITICAL.** No commit path exists; every amendment stays enqueued indefinitely. Gap 2 produces zero behavioral change in production. | ~25 LoC: add field, construct in openEngineWithOptions, expose getter; add to closeEngine path. |
| `/drain-amendments` slash command | **YES** — verified by grep over `cmd/ghyll/session.go` (no `/drain-amendments` case) | **CRITICAL.** Operator has no UX to trigger a commit. | ~40 LoC in DispatchSlashCommand. |
| `arrow_invalidations` ALTER-TABLE migration | **YES** — zero hits for `arrow_invalidations` in `engine/` | **CRITICAL.** When `/invalidate-arrow` fires (or recovery republishes), the journal's INSERT fails at the SQL layer. ADR-v4-008 accepted but unimplemented. | ~20 LoC in engine/store.go's migration block. |
| `runDispatcherAdversarialPhase` driver | **YES** — zero hits for `runDispatcherAdversarialPhase` in any file | **CRITICAL.** No production code partitions clauses or invokes the adversarial cycle. Gap 1 produces zero behavioral change in production. | ~100 LoC helper in `cmd/ghyll/dispatcher_adversarial.go` (new file) per the spec pseudocode. |
| `/adversary` slash command | **YES** — verified by grep | **HIGH.** Without C-7/C-8 it has nothing to toggle; with them, operator cannot disable cycle if a dialect misbehaves. ADR-v4-002 envisages this. | ~30 LoC in DispatchSlashCommand. |
| modal `OpEventAdversarialRoundStart` subscriptions | **YES** — modal subscribes via untagged fan-out but no explicit case handlers for the 4 new event kinds | **HIGH.** Events emit, fan out, drop silently from operator UI. Cycle progress invisible. | ~40 LoC in modal_driver.OnEvent. |

**Essential vs cosmetic count: 8 of 8 are essential.** None are
polish flags. Five are CRITICAL (the gaps' load-bearing seams);
three are HIGH (operator UX + observability). Zero are cosmetic.

## Wiring spot-check (11 production callsites)

| Substrate | Production caller present | File:line | Pass/Fail |
|---|---|---|---|
| `registerGridBindings` | No | `cmd/ghyll/binding_register.go:60` (defined); zero non-test caller | **FAIL** |
| `verifyBindingsCoverage` | No | `cmd/ghyll/binding_register.go:221` (defined); zero non-test caller | **FAIL** |
| `AmendmentCommitter` construction in cmd/ghyll | No | zero non-test caller in cmd/ghyll/ | **FAIL** |
| `AmendmentCommitter.Commit` invocation | No | zero non-test caller | **FAIL** |
| `engineRuntime.committer` field | No | `cmd/ghyll/session_engine.go:48-105` defines 7 fields; `committer` is not one | **FAIL** |
| `/drain-amendments` case | No | `cmd/ghyll/session.go:1302-1347` has 5 cases; not this one | **FAIL** |
| `/adversary` case | No | same | **FAIL** |
| `arrow_invalidations` table | No | `engine/store.go` defines 11 tables; not this one | **FAIL** |
| `migrateAddRemediationColumns` | No | zero hits in `engine/` | **FAIL** |
| `PassDispatcher.Hooks` | No | `runner/dispatcher.go:61-106` defines 10 fields; not this one | **FAIL** |
| `runDispatcherAdversarialPhase` | No | zero hits anywhere | **FAIL** |

**11/11 FAIL.** The substrate is reachable from no production
codepath. This is the integrator pass's verdict: the gap commits do
not fix the gaps.

## Test spot-check (24 contracted tests)

| Test name | Present | File | Pass/Fail |
|---|---|---|---|
| TestScenario_ConceptClassification_18Total | YES | runner/concept_classification_test.go:11 | PASS |
| TestScenario_ConceptClassification_11UniversalsExactly | YES | runner/concept_classification_test.go:23 | PASS |
| TestScenario_ConceptClassification_AgreesWithYAML | YES | runner/concept_classification_test.go:33 | PASS |
| TestScenario_RegisterBuiltins_RegistersAll11Universals | YES | runner/concept_classification_test.go:63 | PASS |
| TestScenario_UniqueDefinition_NoDuplicates | YES | runner/uniquedef_test.go:10 | PASS |
| TestScenario_UniqueDefinition_DuplicatesDetected | YES | runner/uniquedef_test.go:32 | PASS |
| TestScenario_PredicateForm_AssertablePredicates | YES | runner/predicateform_test.go:9 | PASS |
| TestScenario_ModeDeterminableFromRepo_ValidEnum | YES | runner/moderepostate_test.go:10 | PASS |
| TestScenario_SingleActiveRoleInstance_NoConflict | YES | runner/singleactiverole_test.go:8 | PASS |
| TestScenario_Runner_LookupWithRunnerPath | YES | runner/singleactiverole_test.go:123 | PASS |
| TestScenario_Runner_LookupFallback | YES | runner/singleactiverole_test.go:138 | PASS |
| TestScenario_RegisterGridBindings_RegistersCompilesGo | YES | cmd/ghyll/binding_register_test.go:11 | PASS |
| TestScenario_RegisterGridBindings_RejectsInvalidKey | YES | cmd/ghyll/binding_register_test.go:27 | PASS |
| TestScenario_ConceptRegistryKey_UniversalReturnsBare | YES | runner/concept_registrykey_test.go:5 | PASS |
| TestScenario_Registry_SnapshotIsolation | YES | runner/concept_registrykey_test.go:37 | PASS |
| TestScenario_Registry_SwapIntoAtomicity | YES | runner/concept_registrykey_test.go:55 | PASS |
| TestScenario_AmendmentCommit_FIFOViolation_Refuses | YES | runner/amendment_commit_diamond_v4_test.go:12 | PASS |
| TestScenario_AmendmentCommit_NewLanguageBindings_OnCommitRequest | YES | runner/amendment_commit_diamond_v4_test.go:59 | PASS |
| TestScenario_AmendmentCommit_BindingsReRegisterFires_BeforeBump | YES | runner/amendment_commit_diamond_v4_test.go:87 | PASS |
| TestScenario_AmendmentCommit_RegistrySnapshotSwap_Atomic | YES | runner/amendment_commit_diamond_v4_test.go:125 | PASS |
| TestScenario_OperatorBus_HasAuditSubscriber_Predicate | YES | runner/amendment_commit_diamond_v4_test.go:158 | PASS |
| TestScenario_Dispatcher_PartitionClauses | YES | runner/dispatcher_adversarial_test.go:12 | PASS |
| TestScenario_Dispatcher_AdversaryHooksUnwired_RefusesPass | YES | runner/dispatcher_adversarial_test.go:31 | PASS |
| TestScenario_Dispatcher_HookSwap_RaceClean | YES | runner/dispatcher_adversarial_test.go:44 | PASS |

**24/24 PASS.** Contracted unit tests are real, named correctly,
green under -race -count=1. They verify the substrate; they cannot
verify the wiring (because the wiring is the absent piece).

## Race + coverage

- **`go test -race -count=1 -short ./...`**: ALL PACKAGES PASS.
  Full output:
  - `runner` 2.2s
  - `cmd/ghyll` 22.5s
  - `engine` 13.6s
  - `tests/acceptance` 162.7s (godog)
  - 19 other packages all pass.
  - **Race-clean.** No detector hits.
- **`go vet ./...`**: clean (exit 0, no output).
- **Coverage (`make coverage`)**: total **78.2%**.
  Claimed: 78.3%. Delta: -0.1%. Above Tier 3 floor (78%) per
  Makefile:244. Cosmetic discrepancy.

## ADR audit (8 files)

| ADR | File | Status |
|---|---|---|
| ADR-v4-001 | docs/decisions/v4/001-registry-key-shape.md (41 lines) | Real ADR — Context, Decision, Consequences, Accepted 2026-05-25. Implemented (registry key is flat `<concept>.<language>`). |
| ADR-v4-002 | docs/decisions/v4/002-adversarial-phase-conditional-enablement.md (50 lines) | Real ADR — "auto-enable on dialect availability". **Partially implemented**: hook bundle exists; auto-enable wire does NOT exist (no production caller constructs the bundle from the live dialect). |
| ADR-v4-003 | docs/decisions/v4/003-amendment-driven-re-register-ordering.md (58 lines) | Real ADR — snapshot/swap before bump. Substrate implemented in `runner/amendment_commit.go`; production wire absent (C-3). |
| ADR-v4-004 | docs/decisions/v4/004-concept-classification-auto-derived.md (48 lines) | Real ADR — auto-derive from embedded YAMLs. Fully implemented in `runner/concept_classification.go`. |
| ADR-v4-005 | docs/decisions/v4/005-operator-event-typed-payload.md (51 lines) | Real ADR — outcome/reason key split. Implemented: 4 new event kinds in `runner/operatorbus.go`. |
| ADR-v4-006 | docs/decisions/v4/006-evaluator-with-runner-two-table-lookup.md (50 lines) | Real ADR — two-table dispatch. Fully implemented in `runner/runner.go` Registry. |
| ADR-v4-007 | docs/decisions/v4/007-binding-registration-in-cmd-ghyll.md (64 lines) | Real ADR — placement rationale (cmd/ghyll, not runner, to avoid bootstrap→runner cycle). Substrate honors this; production wire absent (C-1, C-2). |
| ADR-v4-008 | docs/decisions/v4/008-engine-schema-migrations.md (55 lines) | Real ADR — explicit ALTER TABLE for `passes.remediation_outcome` + `arrow_invalidations`. **Unimplemented**: migrate functions do not exist in `engine/` (C-5, C-6). |

**ADR quality: all 8 are real ADRs**, not stubs. Context / Decision /
Consequences sections present; Accepted status with date. Three of
eight (002, 003, 007, 008 partially) reference functionality that
is shipped at the substrate level but not at the production
integration level — consistent with the broader deferral pattern.

## Verdict

**No — deferrals must close first.**

The implementer delivered the substrate for all four gaps to a
high standard: race-clean, vet-clean, 78.2% coverage, 24/24
contracted unit tests pass, 8 well-formed ADRs. The runner-side
load-bearing code is structurally correct.

The implementer did NOT deliver the production integration. 11/11
production callsites in this audit's spot-check are absent. The
defects code-eval-2026-05-25.md identified are **all four still
present** in `ghyll run .`:

- Any clause needing a language binding → ErrConceptNotRegistered
- Any depth-sensitive arrow → no adversarial cycle, no
  RemediationReport, no ArrowStatusAbortedRemediation derivation
- Any enqueued amendment → never drained, never applied
- Any `/invalidate-arrow` → engine SQL error (table missing)

This violates the user's standing rule "no deferrals on
adversarial-pass findings." Three of these deferrals were
acknowledged in commit messages as `TODO(diamond-v4)` — explicit
self-flagging that the work was left unfinished. Two further
deferrals (inline TODOs in `runner/uniquedef.go:224` and
`runner/predicateform.go:181`) were not surfaced at all.

The integrator pass cannot proceed because there is no production
integration to integrate. The substrate-only commits leave the
codebase in an internally inconsistent state: ADRs are accepted,
substrate ships, contracted tests pass — but the chat loop is
unchanged. A user running `ghyll run .` would observe ZERO of the
four gaps closed.

**Required next step:** the implementer must land the 8 deferred
items (C-1 through C-11; the 8 deferral table rows map onto these
critical findings) in a follow-up commit before any integrator
pass. Estimated scope: ~300-400 LoC of wiring concentrated in
`cmd/ghyll/session.go`, `cmd/ghyll/session_engine.go`, a new
`cmd/ghyll/dispatcher_adversarial.go`, and `engine/store.go`
migrations. The Gap 4 evaluator implementations (which DID ship
fully) are the only gap that is genuinely closed.
