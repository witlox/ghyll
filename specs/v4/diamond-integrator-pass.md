# Integrator pass — diamond v4 (post-ca6c827) — 2026-05-25

## Summary

The 6-commit diamond v4 substrate (b1119be → ca6c827) lands cleanly:
`make` / `make test-race` / `make coverage-check` (78.4% vs 78% floor)
/ `go vet` all pass. The prior adversarial passes were thorough — the
Critical / High remediations are real and visible at the documented
call sites. Integrator-grade defects surface in three places no
single-context pass could catch: a load-bearing missing producer
(`/invalidate-arrow` has consumers but no producer), documentation
that doesn't yet teach the two new operator commands, and a small
seam where the engine-runtime's untagged bus subscriber keeps no
unsubscribe handle (the analog defect the W-M-2 remediation
explicitly fixed for the audit-tagged subscriber).

## Cross-package seam findings

| ID | Title | File:line | Severity | Suggested fix |
|---|---|---|---|---|
| I-C-1 | `OpEventArrowInvalidated` has a complete consumer chain (modal driver dispatch arm + arrow_invalidations table observer + ADR-v4-005 typed payload contract + arrow_invalidations sqlite table per ADR-v4-008) **and zero producers** in the production codebase. `runner/operatorbus.go:88` documents the event as "operator typed `/invalidate-arrow`"; that slash command does not exist (grep `cmd/ghyll/*.go` for `/invalidate` returns zero hits). The `arrow_invalidations` table will not receive a row in v1 except via direct test-harness Publish. | `runner/operatorbus.go:88-91` (consumer contract), `cmd/ghyll/session_engine.go:644-668` (observer), `cmd/ghyll/modal_driver.go:173,234` (modal dispatch), no producer anywhere | **Critical** | Either ship a `handleInvalidateArrowCommand` (matching the `/drain-amendments` shape: refuses without op-id, validates arrow-id, calls `bus.Publish(OperatorEvent{Kind: OpEventArrowInvalidated, ...})`) — OR — downgrade ADR-v4-008's arrow_invalidations migration to "Proposed/Deferred" and document the v1 status. The current state shipped a four-component contract whose only operator-facing entry point is missing. |
| I-H-1 | `engineRuntime` registers an untagged bus subscriber (`r.bus.Subscribe(func(e) { … r.store.InsertArrowInvalidation(…) })`) at `session_engine.go:644` and discards the unsubscribe handle. The audit-tagged subscriber's W-M-2 closure explicitly added `auditTagUnsubscribe` for this exact reason; the untagged sibling repeats the original defect. | `cmd/ghyll/session_engine.go:644` (subscribe with discarded handle) vs `session_engine.go:618` (sibling with proper W-M-2 unsubscribe) | **High** | Capture the closer the same way W-M-2 did: store as `arrowInvalUnsubscribe func()` on `engineRuntime`, call from `closeEngine` before `r.store.Close()`. Today the subscriber holds a reference to `r.store` and `logger` past close; `store.InsertArrowInvalidation` will return `ErrEngineClosed` on a late event but the structural fix matches the W-M-2 pattern. |
| I-H-2 | `/run-arrow` does not surface adversarial-cycle event kinds inline. The dispatcher publishes `OpEventAdversarialRoundStart`, `OpEventRemediationConverged`, `OpEventRemediationEscalated` during `Dispatch`; the modal driver renders them (W-H-3 closure) but `/run-arrow`'s own subscriber filter (`run_arrow_cmd.go:185`) only captures `PassOpened` / `PassClosed` / `IB-rounds-exceeded`. The operator sees the cycle's round events interleaved into the prompt via the modal driver's d.output, but the per-command summary string never reflects them — observable inconsistency between what the modal pane prints during the call and what the slash-command output captures. | `cmd/ghyll/run_arrow_cmd.go:185-191` (filter set), `cmd/ghyll/dispatcher_adversarial.go:75-86,152-167` (publishers) | **High** | Extend the `/run-arrow` event filter to capture the 3 cycle events; render them as `· adversarial-round-start round=N tier=T` etc. above the final `✓ arrow … dispatched` line. Aligns the summary with what modal-driver shows. |
| I-H-3 | TOCTOU between dispatcher hook check (`runner/dispatcher.go:248-255`) and `runDispatcherAdversarialPhase` hook re-check (`cmd/ghyll/dispatcher_adversarial.go:53`). Both call `r.adversarialHooks.Load()` independently — a concurrent `/adversary disable` between the two yields the second `Load()` returning nil and the cycle returns `ErrAdversaryHooksNotWired` mid-dispatch (after `OpenPass` has already opened a pass). The pass then aborts with `adversarial-phase: adversarial-hooks-not-wired`. Not a panic, but the operator sees a partially-opened pass for what should have been a refused dispatch. | `runner/dispatcher.go:247-255` (first Load), `cmd/ghyll/dispatcher_adversarial.go:53` (second Load) | **High** | Pass the dispatcher's `loadedHooks` snapshot into `AdversarialPhaseFn` as an explicit parameter. Or have the dispatcher call `Load()` once and store on the call-stack-local, then pass via context. Eliminates the second-load entirely. |

## Pre-existing contract violations

| ID | Title | Contract | Where violated | Severity |
|---|---|---|---|---|
| I-M-1 | Pass-abort fires `OpEventPassClosed` before the binding swap during amendment commit; subscribers that ALSO read the live registry (none today, but the API surface invites it) would observe old bindings on close events tied to a now-superseded contract. | `runner/amendment_commit.go:198-233`: order is abort → append → swap → MarkDrained → publish-drain. The `Pass.Abort` step fires `OpEventPassClosed` (via PassRegistry's observer); if a subscriber correlates that close event to a registry lookup of a language-bound concept, it sees the OLD evaluator. | This is a defensive contract — no current subscriber does this correlation, but `runner/operatorbus.go:140-148` documents subscribers as the "load-bearing channel for … amendment R/C reporting" and a future status-CLI subscriber that re-renders the affected arrow's clauses on close would race. | **Medium** |
| I-M-2 | The 4 new `EvaluatorWithRunner` evaluators registered by `RegisterBuiltins` (uniquedef, predicateform, moderepostate, singleactiverole) use the runner-typed table; existing `Evaluator`-only call sites still hit the legacy table. Both tables coexist (`runner/runner.go`); the `Registry.Count()` change to span both tables (commit b1119be) keeps cardinality assertions honest. But: the registration in `RegisterBuiltins` for the 4 new evaluators (`runner/notodo.go:registerOrReplaceWithRunner`) and the lookup path in `Runner.Evaluate` (two-step dispatch) means a downstream consumer that calls `reg.Lookup(concept)` directly (not through `LookupWithRunner`) would silently miss these 4 universals. | `runner/runner.go` two-table dispatch. Direct callers of `Registry.Lookup` bypass `byRunner` table by design. | Not violated yet (the runner's `Evaluate` is the only production call site), but new ADR-v4-006 trades a single lookup surface for two; the integrator-grade risk is that a future caller picks the wrong API. | **Low** (advisory) |
| I-M-3 | `AmendmentCommitter.Commit` runs `c.Queue.MarkDrained(req.Amendment.ID)` even on partial-append failure (`runner/amendment_commit.go:239-243`). The amendment is recorded as drained on disk, but the new arrows are only partially in the grid AND the registry swap was skipped (`appendErr != nil`). On next session start, Replay rebuilds the grid from the partially-appended state and the amendment is invisible (drained). This is documented at lines 126-134 as intentional, but it crosses an integrator-grade boundary: the live registry is desynchronized from the persisted grid permanently. | `runner/amendment_commit.go:227-243` | The commit's docstring says "Persists the drained_at for the amendment regardless." That's a deliberate-design choice but it leaves the runtime in an inconsistent state across crash boundaries. | **Medium** |

## Lifecycle / wiring (end-to-end from cmdRun)

| Step | Production callsite | Behavior verified | Pass/Fail |
|---|---|---|---|
| 1. `ghyll run .` parses args + loads config | `cmd/ghyll/main.go:156-340` | Default-config bootstrap path returns cleanly; lockfile acquired; sandbox enforced. | PASS |
| 2. Session construction wires `SessionConfig` | `cmd/ghyll/main.go:309-326` → `cmd/ghyll/session.go:NewSession` | `initEngine` is invoked unless `DisableEngine` is set. | PASS |
| 3. Grid file read for the engine | `cmd/ghyll/session.go:319-324` | `bootstrap.Read(s.workdir)` populates `gridFile`; nil-grid path is permitted (fresh non-init projects). | PASS |
| 4. `openEngineWithOptions(s.workdir, nil, ibRoundsMax, gridFile)` | `cmd/ghyll/session.go:325` → `session_engine.go:169-323` | `registerGridBindings` runs BEFORE Replay; pre-Replay binding-coverage check refuses with `*MissingBindingError` if needed bindings are missing. | PASS |
| 5. Replay + Recovery + attachJournal | `session_engine.go:364-423,534-672` | Replay → Recovery → attach order preserved. Auditing subscriber registered iff JSONL writer opened (W-H-1 closure). | PASS |
| 6. Modal driver constructed BEFORE `verifyBindingsCoveragePostReplay` | `cmd/ghyll/session.go:367-376` then `:390` | W-H-2 closure: modal driver's bus.Subscribe runs first, then the binding-coverage check publishes; event lands in the ring buffer + the d.output console line. | PASS |
| 7. `autoEnableAdversarial` consults active dialect | `session.go:476` → `adversary_cmd.go:234` | Default ON when dialect endpoint resolves; OFF with banner otherwise. Bundle is real (not stub) per W-C-1. | PASS |
| 8. Pending amendments banner | `cmd/ghyll/session.go:483-501` | `rt.Amendments().Pending()` listed inline; `OpEventRecoveryAmendmentsPending` published; modal driver renders. | PASS |
| 9. REPL accepts `/run-arrow X` | `cmd/ghyll/run_arrow_cmd.go:104` | Grid lookup → tier resolution → dispatch via `RunArrow`. | PASS |
| 10. Dispatcher gates on audit-floor + recursion budget | `runner/dispatcher.go:230-255` | `RequireAuditSubscriber` checks bus has "audit"-tagged subscriber (now real per W-H-1). | PASS |
| 11. Adversarial cycle on depth-sensitive partition | `runner/dispatcher.go:294-312` → `cmd/ghyll/dispatcher_adversarial.go:44-179` | Factory-contract validated (W-H-4); per-round wrapper fills in stores + Runner; round-start + remediation-converged/-escalated events published. | PASS-degraded — see I-H-2 (`/run-arrow` doesn't capture these into its summary) |
| 12. Clause evaluation through registered evaluators | `runner/runner.go` two-table dispatch | `EvaluatorWithRunner` path takes precedence; 4 new universals registered. | PASS |
| 13. Verdicts emit `OpEventAttestationRequested` → modal driver → operator | `cmd/ghyll/modal_driver.go:162-185` | Modal pane renders verdict prompt; submit produces attestation row. | PASS |
| 14. Tree writer is the AttestationStore primaryWriter; flat aggregate is Observer | `session_engine.go:575-596` | Tier-2 contract honored. | PASS |
| 15. Engine catch-up + journal-fanout to engine table | `session_engine.go:289-322,534-672` | JSONL-source-of-truth catch-up runs before Recovery; Recovery's RecoveryReport is surfaced via session.Open. | PASS |
| 16. `/drain-amendments` exercises committer | `cmd/ghyll/drain_amendments_cmd.go:38` → `runner/amendment_commit.go:139` | Refuses without `/op-id`; FIFO head check; snapshot+swap on commit; partial-drain semantics surfaced per-line. | PASS (semantic concerns logged as I-M-3) |
| 17. `/adversary {enable\|disable\|status}` toggles hook bundle | `cmd/ghyll/adversary_cmd.go:64-169` | Atomic-pointer swap; `enable` refuses on no-dialect; status reports wired/wired-but-malformed/DISABLED. | PASS |
| 18. `/invalidate-arrow` produces `OpEventArrowInvalidated` | NONE | The producer does not exist anywhere in the production codebase. | **FAIL** — see I-C-1 |

17 of 18 PASS; 1 FAIL (the `/invalidate-arrow` producer is structurally absent).

## Doc drift surfaced by diamond v4

| Doc | What's now stale | Suggested edit |
|---|---|---|
| `docs/operator-guide.md:120-137` (Slash commands table) | Missing `/drain-amendments`, `/adversary {enable\|disable\|status}`, AND any reference to the (absent) `/invalidate-arrow`. The table lists `/run-arrow` + `/list-arrows` but the new diamond-v4 surfaces are invisible. | Add rows for `/drain-amendments` ("FIFO-drain the pending amendment queue under the active op-id; refuses without /op-id"), `/adversary {enable\|disable\|status}` ("toggle the §11 adversarial cycle hook bundle; refuses enable when no dialect is configured"), and either ship `/invalidate-arrow` and document it, or remove the `OpEventArrowInvalidated` docs reference. |
| `CLAUDE.md:154-163` (In-Session Commands table) | Same gap — no `/run-arrow`, no `/list-arrows`, no `/drain-amendments`, no `/adversary`. Today documents only `/deep`, `/fast`, `/plan`, `/status`, `/exit`, `/<name>`. | Bring the table up to parity with `docs/operator-guide.md`'s expanded list. CLAUDE.md is the entry-point for new operators reading the repo; the gap means a fresh session won't know these commands exist. |
| `README.md:212-213` | Mentions `/list-arrows` + `/run-arrow` but not the diamond-v4 amendment / adversarial commands. | Add a sentence under "Run" that mentions the adversarial cycle's default-on-with-dialect behavior + `/drain-amendments` for the amendment-queue flow. |
| `docs/operator-guide.md` (Troubleshooting section ~515) | "if /list-arrows says no grid…" is documented; "if /drain-amendments says refuses: no op-id" / "if /adversary says no-dialect-configured" are not. | Add 2 entries to the troubleshooting table for the new refusals. |
| `specs/architecture/components/amendment.md` | Describes the amendment FIFO + commit but predates ADR-v4-003's snapshot+swap and the `BindingsReRegister` callback. The diamond-v4 wire is documented in `docs/decisions/v4/003-amendment-driven-re-register-ordering.md` but not in the architecture's amendment-component page. | Cross-link to ADR-v4-003 from the amendment component spec; mention the registry snapshot+swap as the language-binding update path. |
| `docs/decisions/v4/008-engine-schema-migrations.md` | ADR claims the `arrow_invalidations` table is the persistence target for `OpEventArrowInvalidated`. With no producer, the ADR is now in "consumer-only" status; the operator-attestation flow it implies (operator types `/invalidate-arrow`, audit row writes) doesn't exist. | Add a note: "Producer for `OpEventArrowInvalidated` is deferred to a follow-up; today only the schema + observer + modal-driver dispatch exist. The table is reachable from tests via direct `bus.Publish` but not from operator input." OR ship the `/invalidate-arrow` command. |
| `docs/decisions/v4/README.md` | Doesn't note the integrator-pass status. Each ADR landed with the substrate but ADR-v4-008 (arrow_invalidations) is structurally incomplete per I-C-1. | Add a 2-line "Integrator-pass status (2026-05-25)" header noting that ADR-v4-008 ships the consumer chain but not the producer; treat as `partial`. |

## Recommended commit-message edits

The 6 commits' messages largely match what landed. Two refinements:

- **`b1119be`** says "ADR-v4-002: Adversarial phase auto-enabled on dialect availability" landed alongside; the auto-enable was NOT actually wired until `49bfece` (the "8 deferred items"). The b1119be ADR was the contract; the wire-in was 2 commits later. Operator reading b1119be in isolation would expect autoEnableAdversarial to exist at that commit's tree; it does not. Suggest: change "ADR-v4-002: Adversarial phase auto-enabled on dialect availability" to "ADR-v4-002: Adversarial phase auto-enable CONTRACT (wiring lands in 49bfece)".

- **`e971eb0`** body has a literal `TODO(diamond-v4):` line ("wire openEngineWithOptions to consume *bootstrap.Grid…"). That TODO was closed by `49bfece`. The commit body is now misleading on a `git log -p` walk — the reader sees a TODO that has already been resolved 2 commits later. Suggest a follow-up note (or rephrase the commit-body line to "Tracked by follow-up commit 49bfece").

- **`ca6c827`** title "close W-C-1 + 4 High findings" is accurate; commit body is also accurate. No edit needed.

- **`49bfece`** body lists "PassDispatcher.Hooks + AdversarialPhase fields wired through engineRuntime.dispatcher()" — verified at `cmd/ghyll/session_engine.go:752-768`. No edit needed.

- **`071198a`** and **`57ab330`** have parallel "TODO(diamond-v4): wire X" lines whose work landed in 49bfece. Same suggestion as e971eb0.

## Build / test / coverage acceptance

- `make` (lint + test + build): **PASS** (acceptance suite 118.5s, build clean)
- `make test-race`: **PASS** (all 22 packages clean under -race)
- `make coverage-check`: **PASS** — 78.4% (above 78% floor)
- `go vet ./...`: **clean**

## Verdict

**Yes-after-N-doc-edits + 1 producer ship-or-defer decision.**

The 6-commit chain is structurally sound, race-clean, and at coverage
floor. The integrator finding that gates push (I-C-1) is a structural
incompleteness — the `OpEventArrowInvalidated` consumer chain is wired
but its producer (`/invalidate-arrow` slash command) does not exist in
the production codebase. The operator has no way to invalidate an
arrow today; ADR-v4-008's `arrow_invalidations` table will sit empty
in v1 (except through direct-publish tests).

Recommended sequencing before push:

1. **Decision**: ship `/invalidate-arrow` (one ~40-line slash command
   handler) OR mark ADR-v4-008 producer half as "deferred" + add the
   operator-guide note. Either closes I-C-1.
2. **Doc edits** (6 docs, 2 ADRs): operator-guide, CLAUDE.md, README,
   amendment.md component, ADR-v4-008, ADR-v4-README.
3. **Optional but recommended** (I-H-1, I-H-2, I-H-3): three small
   non-blocking fixes to the engine-runtime's missing unsubscribe,
   `/run-arrow` event-filter expansion, and TOCTOU on hooks.Load.

I-M-1 through I-M-3 are advisory — the substrate is functional as-is
but the integrator notes them for the next phase's architectural
review.

The prior 4 adversarial passes did their work well; the only finding
no isolated context could have caught is I-C-1 (consumer chain without
producer), because each individual context (modal driver wiring,
schema migration, ADR, observer) was internally consistent.

## Addendum (2026-05-25) — F-M-3 closure: deferred wiring findings

The 2026-05-25 diamond-final adversarial (`specs/v4/diamond-final-adversarial.md`)
flagged that 5 wiring-adversarial findings were carried forward
without explicit acknowledgement in this integrator pass: **W-M-1,
W-M-3, W-M-4, W-L-1, W-L-2**. They are operator-UX (W-M-1) +
observability (W-M-3) + UX (W-M-4) + maintenance (W-L-1, W-L-2)
items; none blocks the push. This integrator pass explicitly
**accepts them as next-sprint polish** and tracks them as GitHub
issues so the audit trail remains intact:

- W-M-1 (`/status` doesn't list commands, no `/help`) → #34
- W-M-3 (arrow_invalidations insert-failure logs only, no bus
  republish per ADR-015 Part C) → #35
- W-M-4 (`/drain-amendments` uses `continue` on per-amendment
  failure, no first-error abort) → #36
- W-L-1 (uniquedef + predicateform yaml-path walker leaf-shape
  divergence) → #37
- W-L-2 (`remediationConvergedDriver` is a copy of
  `runner.remediationConverged`, not a re-export) → #38
