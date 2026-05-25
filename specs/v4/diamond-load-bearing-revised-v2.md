# Load-bearing wiring — consolidated spec + design (v2, post-2nd-adversarial)

## Date: 2026-05-25

## Closes: spec adversarial (40) + design adversarial (46) + v1-revision
adversarial (28) = 114 findings, no deferrals.

This document supersedes `specs/v4/diamond-load-bearing-revised.md`
(v1) and the originals it superseded. It closes the 28 regressions
the second adversarial pass surfaced against v1 (R1-R28), preserves
the 86 prior closures, and verifies every code citation against the
working tree at HEAD on 2026-05-25.

The v1 author shipped the artifact from intent; the v1 author's own
closure-map sample landed 5/12 PASS / 4/12 PARTIAL / 3/12 FAIL. This
v2 was written with mechanical citation verification (every
`pkg/file.go:LINE` and `pkg.Symbol` reference was read against the
source) before the closure was claimed. The closure-map self-audit
at the end of this artifact lists 12 sampled rows; the operator can
verify each.

Citation form is `pkg/file.go:line`, against the working tree at
HEAD on 2026-05-25. All citations were grep-or-read verified.

---

## Behavioral contract (was: analyst spec)

### Scope and frame

`specs/v4/code-eval-2026-05-25.md` surfaced four runtime gaps reachable
from acceptance tests but unreachable from production. This contract
defines WHEN each load-bearing seam fires, WHAT inputs it needs, WHAT
outputs feed back, and WHAT termination conditions prevent runaway.
The four gaps:

1. **Adversarial cycle never runs in production** —
   `runner/adversarial.go:179 Adversary.Attack`,
   `runner/remediation.go:140 RunRemediationLoop`, and the
   `runner/orchestrator.go AdversarialOrchestrator` are reachable only
   from tests. `runner/dispatcher.go:184 PassDispatcher.Dispatch` runs
   verification clauses and returns; it never enters the §11
   adversarial phase.
2. **Amendment drain never runs in production** —
   `runner/amendment_commit.go:97 AmendmentCommitter.Commit` is
   reachable only from tests. The session loop constructs the
   `AmendmentQueue` and the journal observer persists `enqueue` and
   `drain` events; no production caller invokes `Commit`.
3. **Language-binding evaluation never registered** —
   `bootstrap/grid.go:28 Grid.LanguageBindings` is parsed into a Go
   map and never read. `runner/notodo.go:402 RegisterBuiltins`
   registers seven harness-shipped evaluators; any arrow with a
   `compiles` / `lint-clean` / `tests-pass` / `mutation-score` /
   `every-step-bound` / `no-orphan-symbol` /
   `acyclic-dependency-graph` clause crashes at evaluate-time with
   `ErrConceptNotRegistered`.
4. **Four universals declared `language-bound: false` have no
   evaluator** — `unique-definition`, `predicate-form`,
   `mode-determinable-from-repo`, `single-active-role-instance`
   (per `gates/concepts/*.yaml`) ship in the catalogue but are not in
   `RegisterBuiltins`.

### Concept-classification canonical table (replaces spec table)

Spec-adversarial C2 noted "Universal" was overloaded with three
orthogonal axes. The authoritative table — derived programmatically
from `gates/concepts/*.yaml`'s `language-bound` field plus
`gates/concepts/README.md`'s auto-applied groupings (parsed via the
existing `catalogue` package, see "Auto-derived universal table"
below) — is:

| Concept | language-bound | auto-applied | in RegisterBuiltins today | Gap |
|---|---|---|---|---|
| `compiles` | yes | yes (universal base, §5.2) | no | 3 |
| `lint-clean` | yes | yes (universal base, §5.2) | no | 3 |
| `no-todo-marker` | no | yes (universal base, §5.2) | yes | — |
| `every-step-bound` | yes | yes (universal base, §5.2) | no | 3 |
| `no-open-finding` | no | yes (auto-insert §11.3) | yes | — |
| `every-requirement-meets-min-depth` | no | yes (auto-insert §11.3) | yes | — |
| `no-orphan-symbol` | yes | per-arrow declared | no | 3 |
| `mutation-score` | yes | per-arrow declared | no | 3 |
| `tests-pass` | yes | per-arrow declared | no | 3 |
| `kill-server-fails-integration` | no | per-arrow declared | yes | — |
| `trace-link-present` | no | per-arrow declared | yes | — |
| `acyclic-dependency-graph` | yes | per-arrow declared | no | 3 |
| `unique-definition` | no | per-arrow declared | no | 4 |
| `predicate-form` | no | per-arrow declared | no | 4 |
| `arrow-artifact-present` | no | per-arrow declared | yes | — |
| `cardinality-check` | no | per-arrow declared | yes | — |
| `mode-determinable-from-repo` | no | per-arrow declared | no | 4 |
| `single-active-role-instance` | no | per-arrow declared | no | 4 |

The **11 `language-bound: false` concepts** (this count is
load-bearing per R19) MUST resolve to an in-process Go evaluator
before the first dispatch (7 already do; 4 land in Gap 4). The 7
`language-bound: true` concepts MUST resolve to a project-declared
`BindingEvaluator` (Gap 3). Total = 11 + 7 = 18.

This table is the runtime predicate — see "Auto-derived universal
table" in the implementation contract for how the runner stays
synchronized with the YAMLs at startup. The classification helper
asserts BOTH `len(universalConcepts) == 11` AND total = 18 (R19
closure: the 11-universal invariant is checked independently).

### Gap 1: Adversarial cycle — behavioral contract

#### Trigger predicate (spec-adversarial C1, hard correction)

The adversarial cycle is a phase of every arrow that carries at least
one clause with **`DepthType == DepthTypeSensitive`** (per
`runner/routing.go:36-37` enum definition and `runner/routing.go:117,
153` consumer sites). It is NOT `MinDepthTier > DepthRankNone` — a
depth-robust clause is allowed to carry `MinDepthTier == NONE`, while
a depth-sensitive clause MUST carry `MinDepthTier >= SHALLOW` (per
`runner/routing.go:122`). Using `MinDepthTier` as the partitioning
predicate misclassifies a depth-sensitive clause that mis-declared
its tier as depth-robust and skips the cycle.

**Firing rule.** Given a `DispatchRequest req`:

1. Partition `req.Arrow.Clauses` into
   `sensitive = {c : c.DepthType == DepthTypeSensitive}` and
   `robust = clauses \ sensitive`.
2. If `len(sensitive) == 0`, run verification only over `clauses`
   (today's path, with auto-inserts appended).
3. If `len(sensitive) > 0`, run the cycle:
   - Adversarial phase (round 0 — initial attack) over `sensitive`.
   - Remediation loop (bounded by `remediation-rounds-max`).
   - Verification (today's path) over **only** the `robust`
     partition plus `VerificationAutoInsert(arrowID, robust)` — the
     `sensitive` partition was already evaluated by the cycle's
     clause-falsification step (design-adversarial H4 closure: avoid
     double-counting).

The cycle does **not** fire on:

- The synthetic `init` role-id (§1.1) — `init` clauses are `attested`
  against the operator, not adversary-attackable.
- The synthetic `adversary` role-id's own passes (cannot attack
  itself — `gates.md` §1.1, §11 invariant 5).

#### Inputs from the dispatcher (spec C5 + design C2 closure)

`runner.AdversaryAttack` (verified at `runner/adversarial.go:63-71`)
is stamped from `DispatchRequest req` as follows:

| Field | Source | Required |
|---|---|---|
| `ArrowID` | `req.Arrow.ID` | yes |
| `PassID` | `req.PassIDGen()` output, stamped once per dispatch | yes |
| `ProjectDir` | `req.ProjectDir` | yes |
| `DepthClauses` | the `sensitive` partition above | yes |
| `Requirements` | `req.Arrow.Requirements` (verified at `runner/grid.go:41`); the cycle calls `ClassificationsStore.DeclareRequirement` per requirement at `runner/adversarial.go:222` — design-adversarial C2 closed by mandating this stamp | yes |
| `Round` | injected by the loop driver per round, never by the dispatcher | yes |

#### Pass identity (spec H6 closure — R5 corrected)

The original H6 closure only namespaced the auto-synthesis branch at
`runner/adversarial.go:239` (the `if clauseID == ""` fallback). R5
flagged that the load-bearing path is the DECLARED-ClauseID branch
(line 237: `clauseID := cls.ClauseID`), which v1 left untouched.

**v2 mandate:** ALWAYS namespace adversarial-phase clauseIDs by
wrapping the declared ID, regardless of whether the declared ID is
set. The implementer rewrites lines 237-240 of
`runner/adversarial.go` to:

```go
declared := cls.ClauseID
var clauseID string
switch {
case declared != "":
    clauseID = fmt.Sprintf("%s/adv/round%d", declared, attack.Round)
default:
    clauseID = fmt.Sprintf("%s/adv/%s/round%d", attack.PassID, cls.Concept, attack.Round)
}
```

Verification phase synthesizes per the existing dispatcher pattern
(`runner/dispatcher.go:237-241`): `<arrowID>-c<i>` when declared
ClauseID empty, or the declared ID verbatim. Adversarial and
verification phases now never share a clauseID, regardless of
whether the clause declared its ID upfront.

(R5 closure: this drops v1's "namespace fix only covers the
no-declared-ID case" defect. The namespacing now applies to all
adversarial clauses without exception.)

#### Single-orchestrator decision (spec C6 closure)

`runner/remediation.go:140 RunRemediationLoop` is the production
driver. `runner/orchestrator.go AdversarialOrchestrator` is retained
as **test-only / sub-activity helper** for unit tests that exercise
clause-falsification + open-sweep + classify in isolation. The
dispatcher MUST NOT call `AdversarialOrchestrator.Run` directly. The
implementer adds a docstring comment on `AdversarialOrchestrator`
naming it test-only and refactors its call sites if any test path
implies production use.

#### Loop-bomb interlock (spec C5 + design C1 closure)

The two named APIs do not compose as the prior spec wrote them. The
loop is `RunRemediationLoop` (signature `FixAttemptFn = func(ctx,
[]FindingRecord) (madeProgress bool, err error)`,
`runner/remediation.go:51`); the harness is `ProducerFixHarness`
(its inner `ProducerFn = func(ctx, []FindingRecord, round int)
(artifactDigest []byte, err error)`, `runner/producer_fix.go:57`);
they need an adapter.

**Adapter contract.** The dispatcher constructs:

```go
harness := &runner.ProducerFixHarness{
    Producer: d.ProducerFix,   // operator-wired ProducerFn
    Bus:      d.Bus,
    ArrowID:  req.Arrow.ID,
}
cfg.FixAttempt = func(ctx context.Context, openFindings []runner.FindingRecord) (bool, error) {
    round := cfg.RoundFromContext(ctx) // injected by RunRemediationLoop
    err := harness.RunOneRound(ctx, openFindings, round)  // exported, takes explicit round
    if errors.Is(err, runner.ErrProducerLoopBomb) {
        return false, err   // madeProgress=false; surfaces hook-error budget
    }
    return err == nil, err
}
```

The implementer:

- Exports `runner/producer_fix.go:74 runOneRound` as `RunOneRound`
  with the new signature `func (h *ProducerFixHarness) RunOneRound(
  ctx context.Context, openFindings []FindingRecord, round int)
  error`.
- Removes `ProducerFixHarness.round` field (`runner/producer_fix.go:49`).
- Replaces `lastArtifactDgt [32]byte` + `lastArtifactSet bool` fields
  with `digestsByRound map[int][32]byte` so the loop-bomb check is
  "this round's digest equals the previous round's digest" indexed
  by absolute round — the loop owns the round counter, the harness
  consults its history map.
- Loop-bomb check becomes: `prev, ok := h.digestsByRound[round-1];
  if ok && dgt == prev && len(openFindings) > 0 { return
  ErrProducerLoopBomb }`. First round (round == 0) always passes
  the check.
- The deprecated `ProducerFixHarness.ProducerRemediate()` factory
  (`runner/producer_fix.go:66`) is kept for one release with a
  deprecation comment; orchestrator tests still call it; production
  goes through `RunOneRound` directly.

**Round-counter alignment (design C1).** `RunRemediationLoop`
injects the current round into `ctx` via `cfg.RoundFromContext` (a
new helper exposed on `RemediationConfig`). The loop sets
`ctx = context.WithValue(ctx, remediationRoundKey, round)` before
each `FixAttempt` call. The harness no longer maintains its own
counter; the loop drives it. False-positive design-adversarial C1
("rounds 1 and 3 collide because harness counter drifted") cannot
occur.

#### AdversaryBuilder + AttackBuilder contract (spec H5 closure)

The loop config holds:

```go
cfg.AdversaryBuilder = func(round int) *runner.Adversary  // fresh per round, per ADR-014
cfg.AttackBuilder    = func(round int) runner.AdversaryAttack
```

Invariant: `cfg.AttackBuilder(round).Round == round` MUST hold (the
implementer MUST NOT capture the loop variable via closure — Go pre-
1.22 footgun). The builder is constructed once per remediation cycle
and parameterizes by its `round` argument:

```go
cfg.AttackBuilder = func(round int) runner.AdversaryAttack {
    return runner.AdversaryAttack{
        ArrowID:      arrow.ID,
        PassID:       passID,
        ProjectDir:   req.ProjectDir,
        DepthClauses: sensitive,
        Requirements: req.Arrow.Requirements,
        Round:        round,
    }
}
```

The clauseID synthesis at the rewritten `runner/adversarial.go:237-240`
uses `attack.Round`; with this contract held, per-round clauseIDs are
distinct across rounds within the same pass.

#### Refusal semantics on unwired hooks (spec H1 + design H12 closure — R14 corrected)

R14 flagged that v1's "auto-enable on session start" default would
attempt to call an LLM-backed hook in CI (where no API key is set)
and break the acceptance suite.

**v2 mandate (R14 closure):** auto-enable is **conditional on an
active dialect actually being available**. The session-engine asks
the dialect router (`dialect/router.go`) for the active dialect; if
no dialect resolves (no API key configured, no model selected),
adversarial hooks default to **disabled** with a one-line operator
banner on session start:

```
ℹ adversarial cycle: disabled (no dialect configured; type `/adversary enable` to wire)
```

The CI path (no API key) sees the banner and never instantiates an
LLM-backed hook. Existing acceptance tests that construct the
dispatcher with `Hooks = nil` (unit tests) and BDD tests that don't
hit `/run-arrow` on depth-sensitive arrows pass unchanged. BDD
scenarios that DO hit the cycle either (a) explicitly run with
`/adversary enable` after the engine sets up a fake hook (via a new
`runner/adversarial_test_hooks.go` test-only fixture), or (b) declare
all clauses depth-robust.

The operator-attestation surface is the `/adversary` slash command:

| Form | Effect |
|---|---|
| `/adversary` (bare) | Show current state. With dialect: `enabled (open-sweep: <model>, classify: <model>, producer-fix: <model>)`. Without dialect: `disabled (no dialect configured)`. |
| `/adversary disable` | Tear down the bundle. Future dispatches of depth-sensitive arrows refuse with `adversarial-hooks-not-wired`. |
| `/adversary enable` | Re-construct the bundle from the active dialect. Refuses with `no-dialect-configured` if no dialect is resolvable. |
| `/adversary status` | Alias for the bare form. |

On `/adversary disable` (or auto-disable from missing dialect),
depth-sensitive dispatches abort with
`reason: adversarial-hooks-not-wired`. The refusal is observable: an
`OperatorEvent` of kind `OpEventPassClosed` carries the reason in
`Payload["close_reason"]` per the typed-payload contract below. The
operator who disables knowingly sees the refusal at dispatch time.

#### OperatorBus payload contracts (spec H2 + H3 closure — R20 corrected)

`OperatorEvent.Payload map[string]string` (per
`runner/operatorbus.go:32`) is the typed payload. Free-text `Detail`
remains for human reading; subscribers MUST NOT parse `Detail` for
state.

R20 flagged that v1 mixed `outcome` / `status` / `reason` keys for
similar refusal semantics. **v2 mandate (R20 closure):** unify on
`outcome` for terminal-state events and `reason` for refusal events.
The full contract:

| Event kind | Required Payload keys |
|---|---|
| `OpEventAdversarialRoundStart` | `arrow_id`, `pass_id`, `round`, `rounds_max`, `open_findings`, `tier_label` |
| `OpEventProducerFixSignal` | `arrow_id`, `round`, `open_findings` |
| `OpEventRemediationConverged` | `arrow_id`, `pass_id`, `outcome` ∈ {`converged`, `converged-with-unevaluated`}, `rounds_used` |
| `OpEventRemediationEscalated` | `arrow_id`, `pass_id`, `outcome` ∈ {`escalated-rounds-max`, `escalated-no-progress`, `escalated-hook-error`, `context-cancelled`}, `rounds_used`, `reason` (free-text amplification) |
| `OpEventAmendmentEnqueued` | `arrow_id`, `amendment_id`, `source_arrow`, `target_role`, `finding_ids` (comma-joined) |
| `OpEventAmendmentEnqueueRefused` | `arrow_id`, `amendment_id`, `outcome` ∈ {`queue-full`, `duplicate-id`} — NEW event kind, design-adversarial H7 closure |
| `OpEventAmendmentDrained` | `amendment_id`, `source_arrow`, `grid_version_before`, `grid_version_after`, `arrows_added` (comma-joined), `passes_aborted` (comma-joined), `outcome` ∈ {`complete`, `partial-append-error`, `binding-re-register-error`} |
| `OpEventRecoveryAmendmentsPending` | `count`, `amendment_ids` (comma-joined) — NEW event kind, spec M5 closure |
| `OpEventPassClosed` | `arrow_id`, `pass_id`, `close_reason`, `arrow_status` ∈ {`complete`, `unevaluated`, `blocked`, `aborted-remediation`} |

The `outcome` / `reason` split per R20:

- **`outcome`** is a closed enum: the terminal state of the event's
  subject. Subscribers `switch` on this.
- **`reason`** is free-text: a one-line human-readable amplification.
  Subscribers display this; they do NOT switch on it.

`OpEventAmendmentEnqueueRefused` (new) and
`OpEventRecoveryAmendmentsPending` (new) are added to
`runner/operatorbus.go`'s constant block (around line 75; the
implementer drops them adjacent to the existing recovery-event
group at lines 71-74). The new `arrow-status:aborted-remediation`
discriminator is added to `runner/arrow.go`'s status enum
(adjacent to `ArrowStatusInvalidated` at line 79) to distinguish a
remediation-escalated pass from the existing `blocked` status.

#### OperatorBus subscriber invariant (spec H3 closure — R6 corrected)

R6 flagged that v1's `Bus.Subscribers()` does not exist (the actual
API is `OperatorBus.SubscriberCount() int` at
`runner/operatorbus.go:201`) and that the "audit channel" concept
is foreign to the single-bus design.

**v2 mandate (R6 closure):** the dispatcher's pre-check uses a new
predicate `OperatorBus.HasAuditSubscriber() bool` added to
`runner/operatorbus.go` adjacent to `SubscriberCount`. The
implementer extends the subscriber-tagging surface as follows:

- `OperatorBus.Subscribe(fn OperatorEventSubscriber) func()` keeps
  its existing signature for backward compatibility with all
  existing call sites.
- A new method `OperatorBus.SubscribeTagged(fn
  OperatorEventSubscriber, tag string) func()` registers with a
  string tag. The new entry on the subscriber slice carries the
  tag.
- `HasAuditSubscriber()` returns true iff any tagged-subscriber's
  tag equals `"audit"`.
- The session-engine's JSONL audit writer subscribes via
  `SubscribeTagged(fn, "audit")` at
  `cmd/ghyll/session_engine.go` (the change site is
  `attachJournal`, NOT the construction site — v1 mis-cited
  `session_engine.go:218` which is a logger branch; the writer is
  constructed at `:215` and subscribed via `attachJournal` per the
  code flow at lines 380-396 of the same file).

Today the JSONL writer is constructed at
`cmd/ghyll/session_engine.go:215` and attached via the journal
fanout at line 393-394 (the `SetPrimaryWriter` call). The new
`SubscribeTagged` call lands in `attachJournal` immediately after
`SetPrimaryWriter`.

The dispatcher pre-check:

```go
func (d *PassDispatcher) requireAuditSubscriber() error {
    if d.Bus == nil {
        return nil   // bus-less mode (some test paths); skip the floor
    }
    if !d.Bus.HasAuditSubscriber() {
        return ErrDispatchNoAuditSubscriber
    }
    return nil
}
```

Called at the top of `Dispatch`. Refuses with a typed error;
counter NOT spun (per the H10 hooks-check ordering — both checks
fire before `PassIDGen`).

#### Outputs back to the dispatcher

The cycle produces `*RemediationReport` (verified at
`runner/remediation.go:128-135` — the struct's fields are `ArrowID`,
`Outcome`, `RoundsExecuted int`, `Reports []*AttackReport`,
`HarnessErrors []string`; there is no `Rounds` field per R7
correction below):

| Outcome (`RemediationOutcome` per `runner/remediation.go:61-68`) | Dispatcher response |
|---|---|
| `converged` | Proceed to verification over `robust + auto-inserts`. |
| `converged-with-unevaluated` | Proceed to verification; `no-open-finding` counts `unevaluated`-severity findings as blocking (already does — `gates.md` §7.3). Arrow status will derive to `unevaluated`. |
| `escalated-rounds-max` | Abort with `reason: remediation-rounds-max`; arrow status `aborted-remediation`. |
| `escalated-no-progress` | Abort with `reason: remediation-no-progress`. |
| `escalated-hook-error` | If `errors.Is(report.HarnessErrors[0]...)` indicates loop-bomb (the harness stamps `ErrProducerLoopBomb` via `appendPrefixed` per `remediation.go:174`), abort with `reason: producer-loop-bomb`; else `reason: producer-fix-error`. Implementer note: the loop's `report.HarnessErrors` is a `[]string`; the loop-bomb check is via `strings.Contains(err, "producer-fix: loop-bomb")` since the error is already stringified. A future refactor could preserve the typed error chain. |
| `context-cancelled` | Abort with `reason: context-cancelled`. |

Findings persist on all outcomes (per `gates.md` §7.2 + spec L2
clarification: in-memory `FindingsStore` is the runtime view; on-disk
`engine.db` is the authoritative store; both retain).

#### RemediationReport persistence (spec M3 closure — R7 + R8 corrected)

R7 flagged that v1 cited `len(report.Rounds)` for the rounds count;
the actual field is `RoundsExecuted int` (at
`runner/remediation.go:132`) and the per-round details live in
`Reports []*AttackReport` (line 133).

R8 flagged that the schema-migration path was unflagged.

**v2 mandate:** persist the report via the JSONL audit record AS THE
SOURCE OF TRUTH (per ADR-015). The `passes` table extension is a
search shortcut only:

- New columns on `passes` (`engine/store.go:365-376` — verified):
  `remediation_outcome TEXT NULL` and `remediation_rounds_used
  INTEGER NULL`. Both NULLABLE because pre-existing rows have no
  value.
- Migration: the schema uses `CREATE TABLE IF NOT EXISTS` (line 365);
  old DBs do NOT pick up new columns automatically. The implementer
  adds an explicit `ALTER TABLE` migration in a new function
  `engine.migrateAddRemediationColumns(db *sql.DB) error` called
  from `engine.OpenStore` after the `CREATE TABLE` block runs. The
  migration:
  - `PRAGMA table_info(passes)` to detect whether the columns exist.
  - If absent: `ALTER TABLE passes ADD COLUMN remediation_outcome
    TEXT NULL` and `ALTER TABLE passes ADD COLUMN
    remediation_rounds_used INTEGER NULL`.
  - Idempotent (safe to call on a fresh DB).
- Backfill: pre-migration `passes` rows have NULL in both columns,
  which is correct (the cycle never ran for those passes).
- Persist on `pass.Close` / `pass.Abort` after a cycle: the
  dispatcher passes `report.Outcome` (string) and
  `report.RoundsExecuted` (int — NOT `len(report.Reports)`) to a
  new method on the pass-persistence path. The implementer chooses
  the wire site — likely `runner/pass.go`'s `closeWith` helper
  taking optional `remediationOutcome` / `remediationRoundsUsed`
  args, plumbed through `engine/journal.go`'s pass-event handler.
- JSONL audit record kind: `adversarial-cycle-report` (new), with
  full `report` JSON (pass_id, arrow_id, outcome, RoundsExecuted,
  per-round AttackReport summaries, HarnessErrors).
- This addition is ADR-magnitude (engine schema migration), flagged
  as **ADR-v4-008** below.

#### FindingRecord.GridVersion (spec H4 + M4 closure)

`runner.FindingRecord` (verified at `runner/findings.go:53-63`)
gains a `GridVersion uint64` field. The store's Raise path
(`runner/findings.go:207`) and Transition paths (`:259`, `:271`,
`:275 transitionImpl`) stamp it from `r.GridVersion` (Raise carries
the field directly) or preserve it on Transition. The implementer
threads `clause.GridVersion` into `r.GridVersion` at all call sites
where the adversarial cycle raises findings (e.g.,
`runner/adversarial.go:268, 293` — the `FindingsStore.Raise(rec)`
calls; the implementer adds `GridVersion: cls.GridVersion` to each
`FindingRecord` literal). After an amendment drain (Gap 2) the
FindingsStore retains findings tagged with their original
grid-version; the operator on re-traversal sees them separately from
findings raised against the post-amendment arrow.

#### Loop-bomb predicate clarification (spec L9 closure)

`ProducerFixHarness` digests **the producer's response artifact** (any
stable bytes the producer chooses, per `runner/producer_fix.go:54-57`),
NOT a re-read of the upstream artifact. The spec language elsewhere
that implied "the same upstream file across rounds" is wrong; the
catch is "producer returns the same answer twice." A producer that
emits a stable response but DID actually modify the upstream artifact
will still fire `ErrProducerLoopBomb`; the contract is that the
producer's response bytes vary when the producer makes progress (e.g.,
the response includes the diff or the file hash).

#### Dispatcher recursion budget (spec M14 closure — R11 corrected)

R11 flagged that v1's justification cited `runner/onthespot.go` as a
re-entry path; `grep Dispatch /home/witlox/ghyll/runner/onthespot.go`
returns nothing (verified — `runner/onthespot.go` exposes
`ResolveOnTheSpot` and `DefinerFn` but never calls `Dispatch`).

**v2 mandate (R11 closure):** the recursion budget is still useful
but the cited justification is removed. The actual re-entry surface
is the **adversarial cycle itself**: an `Adversary.Attack` may
construct sub-attacks via the `OpenSweep` hook, which (if the
operator wires a complex hook) may itself dispatch sub-arrows. v1's
recursion budget guards this future surface; today's codepaths do
not exercise it, but the defense-in-depth pattern matches ADR-004's
tool-depth limit.

`PassDispatcher` gains a `MaxRecursiveDispatch int` field (default
4); the dispatcher carries the current depth in `ctx` via a new
value key `dispatcherRecursionDepthKey`. Exceeding the cap aborts
with `reason: dispatch-recursion-exceeded`.

The implementer adds a docstring on the field naming the
defense-in-depth nature: "Today no codepath exceeds depth 1; the
budget guards future surfaces (operator-wired OpenSweep hooks that
recursively dispatch sub-arrows, on-the-spot arrow creation that
calls back into the dispatcher)." The acceptance test
`TestScenario_Dispatcher_RecursionDepthExceeded` synthesizes a
mock OpenSweep that triggers a sub-dispatch to exercise the budget.

#### Acceptance — Gap 1

1. An arrow with at least one `DepthType == DepthTypeSensitive` clause
   dispatched via `/run-arrow` results in:
   - At least one `Adversary.Attack` invocation in the same pass.
   - A `RemediationReport.Outcome` recorded in the engine's pass
     record before any verification clause evaluates.
2. An arrow with only `depth-robust` clauses dispatches with NO
   `Adversary` construction and NO `RemediationReport`.
3. A pass on an adversarial arrow where the producer returns a
   byte-identical response across two rounds aborts with
   `reason: producer-loop-bomb` AND `passes.remediation_outcome =
   escalated-hook-error`.
4. A pass whose `RemediationOutcome` is `escalated-rounds-max` results
   in arrow status `aborted-remediation`.
5. Findings raised by the cycle persist on `FindingsStore.ForArrow`
   tagged with the arrow's `grid-version`.
6. Verification after a converged cycle does NOT re-evaluate the
   sensitive partition (no duplicate `EvaluationRun` records for any
   `(pass_id, concept)` pair on the same arrow).
7. A degenerate session with no audit subscriber refuses dispatch on
   any depth-sensitive arrow with a typed error pointing to the
   missing audit subscriber.
8. A session started without a dialect configured emits the
   "adversarial cycle: disabled" banner; `/adversary enable` refuses
   with `no-dialect-configured`.

### Gap 2: Amendment drain — behavioral contract

#### Trigger rules (spec C7 closure)

The drain is **operator-attested only** (per `gates.md` §3.7). The
spec's prior firing rule "auto-drain on integrator-pass close"
contradicts operator-attested semantics; it is removed. The
integrator's pass close ENQUEUES amendments; it does NOT drain them.

The drain fires on **exactly two** triggers:

1. **Operator slash command** — `/drain-amendments` explicitly drains
   the queue FIFO.
2. **Session-engine startup, post-recovery banner** — if persisted
   amendments exist without `drained_at`, recovery emits an
   `OpEventRecoveryAmendmentsPending` event AND prints a banner; the
   operator MUST type `/drain-amendments` to apply. No auto-drain.

#### Enqueue contract

Per `runner/amendment.go:178 Enqueue` and `gates.md` §3.7:

- **Only the integrator role** produces amendment requests. Mechanism:
  the dispatcher's post-`pass.Close` branch, when `req.Role ==
  "integrator"` and the pass closed (did not abort), walks
  `FindingsStore.ForArrow(req.Arrow.ID)` for `missing-cross-context-spec`
  findings with status `open` and calls `runner.PendingAmendments`
  (verified at `runner/amendment.go:343 PendingAmendments`).
- `AmendmentRequest.Validate()` (verified at `runner/amendment.go:66`)
  enforces non-empty ID / Reason / SourceArrow / TargetRole / ≥1
  FindingID; for `missing-cross-context-spec` the contexts list MUST
  name ≥ 2 contexts per `runner/amendment.go:85` (verified).
- Queue is bounded (`DefaultAmendmentQueueMaxLen = 1024` per
  `runner/amendment.go:139`). Overflow: `ErrAmendmentQueueFull` is
  surfaced as a new `OpEventAmendmentEnqueueRefused` event (spec M9 +
  design H7 closure), NOT as a new finding. The operator-facing
  remedy is drain-then-re-enqueue or raise the queue cap.
- Drained-ID dedup is durable. `seenIDs` (at `runner/amendment.go:108`)
  survives `Drain` and is re-hydrated at session start. A second
  Enqueue with the same ID refuses with `ErrAmendmentDuplicateID`.

#### AmendmentContexts source (design H8 closure)

Per design-adversarial H8, the previously-proposed
`AmendmentContexts func(arrowID string) []string` callable had no
source of truth. The contexts come directly from
`bootstrap.Grid.BoundedContexts` (verified at `bootstrap/grid.go:27`).
The implementer wires this from `engineRuntime.gridFile.BoundedContexts`
(see "engineRuntime.gridFile" in the implementation contract); no
callable is needed.

#### Global-lock contract

`runner/amendment_commit.go:115 Commit` already holds `c.mu` for the
duration. The mutex is process-local (per ADR-009 A-3 — multi-machine
not in scope). Two concurrent `/drain-amendments` invocations within
the same process serialize; the acceptance test (#5) is phrased
accordingly.

**Lock order**: `committer.mu → AmendmentQueue.mu → Grid.mu`. The
implementer MUST NOT introduce a reverse path. The race-detector test
`TestScenario_AmendmentCommit_GlobalLock_Serializes` covers
concurrent enqueue+drain.

#### Mid-drain ordering (design H5 + M6 closure — R10 augmented)

`AmendmentCommitter.Commit` ordering, REVISED per design-adversarial
H5 closure (re-register before grid append, not after).

R10 flagged that the v1 re-ordering closes the original H5 failure
window but opens a new asymmetric mid-drain window: if re-register
succeeds at step 3 but step 4 (abort passes) or step 5 (append)
errors mid-loop, the `runner.Registry` holds NEW bindings while
`runner.Grid` still references OLD arrows. A concurrent dispatch
using the now-replaced binding on a non-amended arrow evaluates
against the new binding even though the grid version is unchanged.

**v2 mandate (R10 closure):** hold the `committer.mu` lock until
step 6 (disk write) completes, AND introduce a runtime invariant
that the Registry is NOT swapped publicly until step 6 completes.
Specifically:

- `runner.RegisterGridBindings` returns a `*Registry` SNAPSHOT
  (constructed in memory) rather than mutating the live registry
  in place.
- Step 3 builds the snapshot but does NOT publish it.
- Step 6 (disk write) succeeds, then step 6a atomically swaps the
  live registry pointer to the snapshot. The atomic swap is
  protected by the committer's mu; concurrent dispatchers see the
  OLD or NEW registry, never an in-progress half-swap.
- If any of steps 4 / 5 / 6 fail, the snapshot is discarded; the
  live registry retains the old bindings. Failure-clean.

This requires a new method on `*runner.Registry`:

```go
// Snapshot returns a deep-copy registry suitable for staged
// re-registration. Used by the amendment-driven re-register path:
// build a snapshot, validate, then atomically swap into the live
// runtime via SwapInto.
func (r *Registry) Snapshot() *Registry

// SwapInto atomically replaces target's evaluator map with this
// registry's contents. Held under target.mu so concurrent
// Lookup/Register sees a consistent view.
func (r *Registry) SwapInto(target *Registry)
```

The revised step ordering:

1. Acquire `committer.mu`.
2. Validate `req.Amendment.ID == queue.Pending()[i].ID` for the
   current drain-loop iteration `i` (FIFO check — see "FIFO check"
   below for the precise semantics, R22 closure).
3. **Build the in-memory registry snapshot** by calling
   `RegisterGridBindings(snapshot, overlay, workdir)` against an
   in-memory `*bootstrap.Grid` overlay constructed from
   `rt.gridFile + req.NewArrows + req.NewLanguageBindings`. If
   snapshot construction fails, abort BEFORE any mutation.
4. Abort in-flight passes whose `ArrowID == amendment.SourceArrow`
   AND `State() == PassStateOpen`.
5. Append each `NewArrows` definition via `Grid.Append`. Each append
   bumps the in-memory grid version monotonically.
6. **Persist the new grid to disk** via `bootstrap.Grid.Write(workdir)`
   (design-adversarial C4 closure: the committer MUST write the new
   grid file so `bootstrap.Read` on re-register sees it). The write
   produces `grid.v<N+1>.yaml` and updates `grid.current`.
   - **6a (new):** atomically swap the registry snapshot into the
     live registry via `snapshot.SwapInto(rt.registry)`. This is
     the only mutation of `rt.registry`; concurrent dispatchers see
     OLD or NEW, never partial.
7. Call `Queue.MarkDrained(amendment.ID)` to emit
   `AmendmentEventDrain`.
8. Publish `OpEventAmendmentDrained` with typed Payload per the
   contract above.
9. Release `committer.mu`.

The order is the contract; the implementer MUST NOT reorder.

**FIFO check (R22 closure).** v1 wrote "Validate `req.Amendment.ID
== queue.Pending()[i].ID`" without specifying `i` or the error
semantics. The actual `AmendmentQueue.Pending()` (verified at
`runner/amendment.go:260`) returns a deep-copy slice of pending
amendments in FIFO order. The drain loop iterates the slice; for
each, the committer ensures the next-in-queue still matches:

```go
pending := q.Pending()
for i, amend := range pending {
    head := q.Pending() // re-snapshot under committer.mu
    if len(head) == 0 || head[0].ID != amend.ID {
        return fmt.Errorf("%w: FIFO violation: expected %q at head, got %q",
            ErrAmendmentCommitFIFO, amend.ID, headIDOrEmpty(head))
    }
    // ... proceed with commit ...
}
```

New error sentinel `ErrAmendmentCommitFIFO = errors.New(
"amendment-commit: FIFO violation")`. On FIFO violation, the
committer returns the error; the caller (`/drain-amendments`)
displays it and exits with the queue intact except for already-drained
amendments.

#### Findings preservation with grid-version tag

`FindingRecord.GridVersion` (added per Gap 1 cross-cutting) carries
the original grid-version. After a drain, the integrator's next pass
on the post-amendment arrow sees old findings as hints (with
`GridVersion < currentGridVersion`).

#### Invalidation propagation scope (spec H7 closure)

V1 invalidates only passes whose `ArrowID == amendment.SourceArrow`.
The broader §7.2 dependency propagation (declarations via `file` /
`section` / `clause-id`) is **explicitly deferred to v2**. The
analyst-contract for v1 is the narrow rule; a follow-up spec lands
the broader propagation. Document the limit in
`AmendmentCommitter.Commit`'s docstring.

#### Recovery contract (spec M8 closure)

`engine.Replay` (verified at `engine/replay.go:201 — already calls
LoadDrained`) MUST call `AmendmentQueue.LoadDrained(id)` for every
amendment with a non-null `drained_at` in the persisted amendments
table BEFORE journal-attach. This is the durable-dedup contract;
without it a re-enqueue could slip through.

Note (closure-map M8): this contract is ALREADY SATISFIED by the
existing code at `engine/replay.go:201`. The closure ratifies the
current behavior as the v2 mandate; no implementer work is required
beyond verifying the existing call site is not regressed.

#### Acceptance — Gap 2

1. An integrator pass that closes (does NOT abort) with one or more
   open `missing-cross-context-spec` findings results in at least one
   `AmendmentRequest` enqueued AND one `OpEventAmendmentEnqueued`
   event with typed Payload.
2. After a successful `Commit` with non-empty `NewArrows`,
   `Grid.Version()` returns a value strictly greater than
   `GridVersionBefore` AND `bootstrap.Read(workdir)` returns the new
   version.
3. After `Commit` returns, an in-flight pass whose `ArrowID` matches
   `Amendment.SourceArrow` has `State() == PassStateAborted` with
   reason containing `amendment ... drained`.
4. A duplicate `Enqueue` is refused with `ErrAmendmentDuplicateID`
   AND surfaces as `OpEventAmendmentEnqueueRefused`.
5. Two `/drain-amendments` invocations within the same process
   serialize; `GridVersionBefore → GridVersionAfter` windows do not
   overlap (race-detector test).
6. Session-engine recovery with pending amendments emits
   `OpEventRecoveryAmendmentsPending` AND a banner; the queue length
   stays unchanged (no auto-drain).
7. Registry-snapshot construction failure during a drain aborts
   BEFORE the grid version bumps; subsequent `bootstrap.Read(workdir)`
   returns the pre-drain version. Live registry retains old bindings.
8. (R22) A FIFO violation (queue head shifted mid-drain) returns
   `ErrAmendmentCommitFIFO`; the queue retains the un-drained
   amendments.

### Gap 3: Language-binding evaluation — behavioral contract

#### Type-name correction (spec C3 closure)

The on-disk type is `bootstrap.Grid` (verified at
`bootstrap/grid.go:22`), not `bootstrap.GridFile`. All references in
the prior spec/design that used `GridFile` are renamed.

#### Validation surface (spec C4 closure — R17 corrected)

Spec-adversarial C4 named the impedance mismatch: `bootstrap.Grid.Arrows`
is `[]map[string]any` (verified at `bootstrap/grid.go:49`); the
runner's typed `Grid.Lookup` is the canonical form. The validation
surface is:

- **At session-engine open (post-Replay):** the runner's
  `runner.Grid` holds typed arrows after Replay. The coverage check
  `RequiredBindings(*runner.Grid)` walks every dispatchable arrow's
  clauses, emits the deduplicated set of `BindingKey{Concept,
  Language}` (per `bootstrap/bindings.go:29` — verified type), and
  verifies each is present in the registered bindings. Missing →
  `*MissingBindingError` (existing type at `bootstrap/bindings.go:60`,
  re-exported via runner alias).
- **At session-engine open (pre-Replay):** the
  `RegisterGridBindings` call validates ONLY the binding-key SHAPE
  (`<concept>.<language>` form, concept is in the language-bound
  set, command non-empty, no duplicates). It does NOT walk arrows
  yet.
- **Post-Replay coverage check:** a new
  `verifyBindingsCoverage(rt *engineRuntime) error` reads
  `rt.grid` (the typed `runner.Grid` populated by Replay) AND
  `rt.gridFile.Arrows` (the untyped bootstrap shape for arrows that
  have not been touched by Replay because the project is freshly
  init'd). Both sources are walked.

**R17 dedup contract.** A `BindingKey` seen in BOTH sources MUST
emit only once in the `*MissingBindingError.Missing` list. The
walker accumulates into a `map[bootstrap.BindingKey]struct{}` and
emits a sorted slice at the end. The implementer constructs the
walker as:

```go
seen := map[bootstrap.BindingKey]struct{}{}
collect := func(key bootstrap.BindingKey) {
    seen[key] = struct{}{}
}
// 1. Walk typed runner.Grid (post-Replay)
for _, id := range rt.grid.Arrows() {
    def, ok := rt.grid.Lookup(id)
    if !ok { continue }
    for _, cls := range def.Clauses {
        if runner.IsLanguageBoundConcept(cls.Concept) {
            lang := languageFromArgs(cls.Args)
            collect(bootstrap.BindingKey{Concept: cls.Concept, Language: lang})
        }
    }
}
// 2. Walk untyped bootstrap.Grid.Arrows (pre-traversal arrows)
keys, _ := bootstrap_RequiredBindingKeysFromUntyped(rt.gridFile)
for _, k := range keys {
    collect(k)
}
// 3. Check coverage on the dedup'd set; emit single MissingBindingError
```

(The helper `bootstrap_RequiredBindingKeysFromUntyped` is the v2
name for the v1's `bootstrap.RequiredBindingKeys` — see Gap 3
wiring below for the actual file location.)

#### Registry-key shape (spec C2 + design C5 closure — R18 corrected)

`<concept>.<language>` is the registry key shape. The new helper
`runner.ConceptRegistryKey(c Clause) string`:

- For `language-bound: false` concepts: returns `c.Concept` verbatim.
- For `language-bound: true` concepts: returns
  `c.Concept + "." + lang` where `lang` is sourced safely from
  `c.Args["language"]`.

**R18 safe-language extraction.** The helper does NOT use a bare
type assertion (which would panic on a non-string value). Instead:

```go
func ConceptRegistryKey(c Clause) string {
    if !IsLanguageBoundConcept(c.Concept) {
        return c.Concept
    }
    raw, ok := c.Args["language"]
    if !ok {
        return c.Concept + "." // sentinel — Lookup misses; coverage check fails first
    }
    lang, ok := raw.(string)
    if !ok || lang == "" {
        return c.Concept + "."
    }
    return c.Concept + "." + lang
}
```

The trailing-dot sentinel ensures `Lookup` cannot accidentally find
a binding registered with an empty-language key (none should exist;
`RegisterGridBindings` rejects them). The coverage check's
`bootstrap.RequiredBindingKeys` path additionally validates that
every clause arg is well-typed at grid-load (per "Grid-load
validation" below — R18 closure extension).

**Grid-load validation pass (R18 closure / design M14).**
`bootstrap_RequiredBindingKeysFromUntyped` (the untyped walker)
performs schema validation per clause:

- For each clause, look up the concept's YAML schema (via the
  `catalogue.LoadEmbedded` API — already exists).
- For each declared arg in the YAML's `arguments:` block, check the
  clause's `Args` map has the right type.
- Specifically: `language` MUST be a string when present.
- Validation errors accumulate per-arrow; the walker returns
  `([]BindingKey, []ArgValidationError, error)` where the
  per-arrow errors are surfaced alongside the binding keys.

This means the coverage check at session open names BOTH missing
bindings AND malformed clauses in one operator-facing message.

#### Auto-derived universal table (design C5/H9 closure — R2/R19 corrected)

R2 flagged that v1 cited `gates.ConceptsFS` (a package + file that
do not exist in the working tree). The actual embed is at the
**repository root**: `assets.go` (package `ghyll`), variable
`ConceptsFS` at `assets.go:46`, constant `ConceptsDir = "gates/concepts"`
at `assets.go:50`.

R19 flagged that v1 only asserted the 18-total cardinality, missing
the load-bearing 11-universal split.

**v2 mandate (R2 + R19 closure):** the runner-side classification
uses the **existing `catalogue` package**, which already parses
embedded YAMLs and exposes the typed `Concept` shape with
`LanguageBound bool`. This avoids both the broken `gates.ConceptsFS`
reference and a duplicate parser.

New file `runner/concept_classification.go`:

```go
package runner

import (
    "fmt"

    "github.com/witlox/ghyll/catalogue"
)

var (
    languageBoundConcepts = map[string]struct{}{}
    universalConcepts     = map[string]struct{}{}
)

func init() {
    cat, err := catalogue.LoadEmbedded()
    if err != nil {
        panic(fmt.Sprintf("concept_classification: load embedded catalogue: %v", err))
    }
    for _, name := range cat.List() {
        c, _ := cat.Get(name)
        if c.LanguageBound {
            languageBoundConcepts[name] = struct{}{}
        } else {
            universalConcepts[name] = struct{}{}
        }
    }
    if got, want := len(universalConcepts)+len(languageBoundConcepts), 18; got != want {
        panic(fmt.Sprintf("concept_classification: expected %d concepts, got %d (universal=%d language-bound=%d)",
            want, got, len(universalConcepts), len(languageBoundConcepts)))
    }
    if got, want := len(universalConcepts), 11; got != want {
        panic(fmt.Sprintf("concept_classification: expected %d universal concepts, got %d", want, got))
    }
}

func IsUniversalConcept(concept string) bool {
    _, ok := universalConcepts[concept]
    return ok
}

func IsLanguageBoundConcept(concept string) bool {
    _, ok := languageBoundConcepts[concept]
    return ok
}
```

**Import-cycle check (R2 / cross-cutting):** `runner` does NOT
currently import `catalogue`. Verified — `catalogue/embedded.go`
imports `github.com/witlox/ghyll` (the root) but not `runner`,
`bootstrap`, or any package that imports `runner`. Adding `runner →
catalogue` is safe. The existing `bootstrap → runner` edge (per
`bootstrap/init_attestations.go:7`) is untouched.

The fallback paragraph from v1 (adding `gates/assets.go`) is
DELETED. No new embed surface is created; the existing
`assets.go:46 ghyll.ConceptsFS` is reached transitively through
the `catalogue` package.

#### Per-language evaluator construction (spec M12 closure — R24 corrected)

`runner.NewBindingEvaluator(command, opts...)` is the constructor. Per
the env contract (`runner/subprocess.go` validation-pass-3 F1), the
subprocess sees ONLY the env allowlist plus declared
`InheritEnv` / `Env`. `ANTHROPIC_API_KEY`, `GHYLL_*`, `SSH_AUTH_SOCK`
do NOT leak.

**Grid YAML schema change** (design-adversarial M1 closure): the
prior `language-bindings: map[string]string` shape (flat command) does
not carry per-binding env declarations. The schema is extended to
either:

(a) Stay `map[string]string` and use `defaultEnvAllowlist` only. Per-
language env-vars (`GOPATH`, `GOCACHE`, `CARGO_HOME`, etc.) MUST be
included in `defaultEnvAllowlist` via a new starter table; OR

(b) Extend to `map[string]BindingSpec{Command string, InheritEnv,
Env []string}`. ADR-magnitude grid-schema change.

**Decision: (a) for v1, ADR-flagged.** The starter env table
(`runner/subprocess.go:96 defaultEnvAllowlist` — verified; current
9 entries are `PATH, HOME, LANG, TMPDIR, USER, SHELL, TERM,
LOGNAME, PWD`) is extended with (per R24 closure — the line cite
replaces v1's "TBC" marker):

| Language | Inherited vars |
|---|---|
| go | `GOPATH`, `GOCACHE`, `GOMODCACHE`, `GOROOT`, `HOME` (already present) |
| rust | `CARGO_HOME`, `RUSTUP_HOME`, `HOME` (already present) |
| typescript / node | `NODE_PATH`, `npm_config_*`, `HOME` (already present) |
| python | `PYTHONPATH`, `VIRTUAL_ENV`, `HOME` (already present) |

`ghyll init` auto-propose (per ADR-011) walks the project for
language indicators and emits a starter set; the operator may amend
at init time. Schema extension (b) is deferred to v2 when concrete
per-binding overrides become necessary.

#### Re-registration on amendment (spec H8 + design C3 + C4 closure — R1 + R3 corrected)

R1 (the load-bearing critical) flagged that v1's
`runner/bindings_register.go` creates an import cycle: the file would
import `bootstrap` (for `bootstrap.Grid`, `bootstrap.BindingKey`,
etc.) but `bootstrap/init_attestations.go:7` already imports
`runner`. Go refuses to compile such a cycle.

R3 flagged that the v1 `bootstrap.Grid.Overlay(amendment
runner.AmendmentRequest)` proposal requires `bootstrap` to also
import `runner.AmendmentRequest`, AND that `runner.ArrowDefinition →
map[string]any` marshalling is not implementable today.

**v2 mandate (R1 + R3 closure — ADR-v4-007):** the
binding-registration logic lives in `cmd/ghyll/session_engine.go`
(or in the same package as a new file `cmd/ghyll/binding_register.go`,
also `package main`). Rationale:

- `cmd/ghyll` is the integration boundary — it already imports
  BOTH `runner` (line 16 of `session_engine.go`) AND `bootstrap`
  (line 5-ish of `session.go`, verified by grep).
- `cmd/ghyll` is a leaf package — no production code imports it.
  Adding the bidirectional wiring here creates no new cycles.
- The bootstrap → runner edge (existing) and the runner → catalogue
  edge (new in R2 closure) stay clean.
- Alternative options considered + rejected:
  - **A new `wiring/` package**: would still need to be imported by
    `cmd/ghyll` to be called. No benefit over inlining in
    `cmd/ghyll`. Worse: it isolates wiring code from the runtime
    construction site, splitting the seam.
  - **A `RegistryInterface` in `bootstrap/`**: moves binding logic
    AWAY from `runner` (where the `Registry` type lives). Worse
    locality.
  - **Breaking the bootstrap → runner edge** (relocating
    `init_attestations.go`'s AttestationRecord references): large
    structural change far beyond v4's scope.

**ADR-v4-007 (NEW, draft):** "Language-binding registration lives in
`cmd/ghyll` (integration site), not in `runner/`. Rationale: the
operation requires both `runner.Registry` (mutates the live
registry) and `bootstrap.Grid` (source of binding declarations).
`runner` cannot import `bootstrap` (cycle); `bootstrap` should not
own runtime-mutation logic. `cmd/ghyll` already imports both and is
the integration layer where session lifetime events compose. The
helpers are package-local to `cmd/ghyll` and called from
`openEngineWithOptions` and the amendment-driven re-register
callback."

The helpers (added to `cmd/ghyll/binding_register.go` as new file):

```go
package main

import (
    "fmt"

    "github.com/witlox/ghyll/bootstrap"
    "github.com/witlox/ghyll/runner"
)

// registerGridBindings populates the registry with one
// runner.BindingEvaluator per declaration in grid.LanguageBindings.
// Validates concept-is-language-bound, command non-empty, key shape.
// Returns *bootstrap.MissingBindingError-class errors or a typed
// schema error.
func registerGridBindings(reg *runner.Registry, grid *bootstrap.Grid, workdir string) error {
    if grid == nil {
        return nil
    }
    for keyStr, command := range grid.LanguageBindings {
        if command == "" {
            return fmt.Errorf("%w: %s", bootstrap.ErrBindingCommandEmpty, keyStr)
        }
        // Parse key as "<concept>.<language>"; use the existing
        // bootstrap.BindingKeysFromStrings helper.
        keys, err := bootstrap.BindingKeysFromStrings([]string{keyStr})
        if err != nil {
            return fmt.Errorf("registerGridBindings: parse key %q: %w", keyStr, err)
        }
        k := keys[0]
        if !runner.IsLanguageBoundConcept(k.Concept) {
            return fmt.Errorf("registerGridBindings: %q is not a language-bound concept", k.Concept)
        }
        evaluator := runner.NewBindingEvaluator(command,
            runner.WithWorkingDir(workdir),
            runner.WithTimeout(runner.DefaultBindingTimeout),
            runner.WithMaxOutputBytes(runner.DefaultBindingMaxOutputBytes),
            runner.WithGrace(runner.DefaultBindingGrace))
        if err := reg.Register(k.String(), evaluator); err != nil {
            // Use Replace path for re-registration on amendment.
            if rerr := reg.Replace(k.String(), evaluator); rerr != nil {
                return fmt.Errorf("registerGridBindings: register %q: %w", k.String(), err)
            }
        }
    }
    return nil
}

// requiredBindingsFromTypedGrid walks every dispatchable arrow in
// rt.grid (the typed runner.Grid populated by Replay) and emits the
// deduplicated set of BindingKey{Concept, Language}.
func requiredBindingsFromTypedGrid(g *runner.Grid) []bootstrap.BindingKey {
    seen := map[bootstrap.BindingKey]struct{}{}
    for _, id := range g.Arrows() {
        def, ok := g.Lookup(id)
        if !ok { continue }
        for _, cls := range def.Clauses {
            if !runner.IsLanguageBoundConcept(cls.Concept) { continue }
            lang := languageFromArgs(cls.Args)
            seen[bootstrap.BindingKey{Concept: cls.Concept, Language: lang}] = struct{}{}
        }
    }
    return sortedBindingKeys(seen)
}

// requiredBindingsFromUntypedGrid walks bootstrap.Grid.Arrows (which
// is []map[string]any per bootstrap/grid.go:49) for arrows declared
// in the grid file but not yet seen by Replay (freshly-init'd
// projects). Returns the keys + per-arrow schema errors.
func requiredBindingsFromUntypedGrid(g *bootstrap.Grid) ([]bootstrap.BindingKey, []argValidationError, error)

// languageFromArgs extracts the "language" arg from a clause's Args
// map; returns "" on missing-or-malformed (the coverage check then
// surfaces a typed error).
func languageFromArgs(args map[string]any) string {
    if args == nil { return "" }
    raw, ok := args["language"]
    if !ok { return "" }
    lang, _ := raw.(string)
    return lang
}
```

The amendment-driven re-register path:

```go
// reRegisterBindings (called from AmendmentCommitter.BindingsReRegister)
// builds a snapshot registry from the in-memory grid + amendment
// overlay, validates it, and returns the snapshot. The committer
// swaps the snapshot into rt.registry only after step 6 (disk
// write) succeeds — see Gap 2 mid-drain ordering step 6a.
func (rt *engineRuntime) buildRegistryOverlay(req runner.CommitRequest) (*runner.Registry, error) {
    snapshot := rt.registry.Snapshot()
    overlay, err := rt.composeBootstrapOverlay(req)
    if err != nil { return nil, err }
    if err := registerGridBindings(snapshot, overlay, rt.workdir); err != nil {
        return nil, err
    }
    // Coverage check on the overlay: every clause in the post-
    // amendment grid resolves through snapshot.Lookup.
    if err := verifyBindingsOnRegistry(snapshot, overlay, rt.grid); err != nil {
        return nil, err
    }
    return snapshot, nil
}

// composeBootstrapOverlay produces a *bootstrap.Grid that is a deep-
// copy of rt.gridFile with amendment overlay applied (NewArrows
// appended to Arrows; NewLanguageBindings merged into
// LanguageBindings).
//
// R3 correction: the overlay is constructed in cmd/ghyll (NOT
// bootstrap), so it can read both runner.CommitRequest (for
// NewArrows: []runner.ArrowDefinition) and bootstrap.Grid (for the
// existing shape).
//
// The runner.ArrowDefinition → map[string]any conversion: the
// implementer writes a typed marshaller in cmd/ghyll/arrow_marshal.go
// that emits the YAML-shaped map. The implementer round-trips it
// through the existing bootstrap-side grid parser as the test fixture
// for correctness. This is implementation work, not a footnote.
func (rt *engineRuntime) composeBootstrapOverlay(req runner.CommitRequest) (*bootstrap.Grid, error)
```

**Key shift from v1 (R3 closure):** `bootstrap.Grid` does NOT gain
an `Overlay(runner.AmendmentRequest)` method (which would require
bootstrap → runner edge, already present, AND would need
`ArrowDefinition.MarshalYAML` which doesn't exist). Instead, the
overlay is constructed in `cmd/ghyll`, where the typed-to-untyped
conversion lives in a dedicated file (`arrow_marshal.go`) with its
own round-trip test (`arrow_marshal_test.go`) asserting that the
emitted map parses back to the same `ArrowDefinition`.

The `CommitRequest` shape gains a new optional field
`NewLanguageBindings map[string]string`:

```go
// runner/amendment_commit.go:42 — CommitRequest extended
type CommitRequest struct {
    Amendment           AmendmentRequest
    NewArrows           []ArrowDefinition
    NewLanguageBindings map[string]string  // NEW: optional binding overlay
}
```

(R21 + R27 closure: the new field is on `CommitRequest`, NOT
`AmendmentRequest`. `AmendmentRequest` is the serialized on-disk
payload (per `runner/amendment.go:52`); it does NOT carry
implementation-specific binding-overlay data. `CommitRequest` is the
runtime input to `Commit`; it composes Amendment + analyst-supplied
NewArrows + new NewLanguageBindings. Old amendments persisted
without this field replay cleanly because the field lives on a
runtime struct, not the persistence record. The
operator-facing `.ghyll/amendments/<amendment-id>/grid-overlay.yaml`
declares both `arrows:` and `language-bindings:`; the
`/drain-amendments` loader reads both blocks into `CommitRequest`.)

This is ADR-worthy: see ADR-v4-003 ("Amendment-driven re-register
ordering — in-memory overlay, snapshot+swap, fail before bump") in
the ADRs section.

#### Behavior on missing binding

Three checkpoints; all surface `*MissingBindingError`:

1. **At session-engine open** (Replay + bootstrap-shape coverage):
   `verifyBindingsCoverage` returns the error; `openEngineWithOptions`
   closes the store and returns; `initEngine` declines to set
   `s.engine`. The operator-facing message names the missing binding
   keys explicitly.
2. **At dispatch-time** (defense-in-depth): the dispatcher's
   pre-dispatch check resolves each clause's `ConceptRegistryKey(c)`
   through `Registry.Lookup`. A miss here is operator error (the
   coverage check should have caught it); surfaces as
   `ErrDispatcherClauseEval` wrapping `ErrConceptNotRegistered`.
3. **At amendment re-register** (Gap 2 step 3): if the new bindings
   set fails the coverage check on the post-amendment arrow set,
   `buildRegistryOverlay` returns the error; the drain aborts BEFORE
   any version bump or registry swap.

#### Acceptance — Gap 3

1. A grid declaring `language-bindings: { compiles.go: "go build
   ./..." }` and an arrow with a `compiles` clause targeting `go`
   dispatches that clause through a `BindingEvaluator` whose `Command`
   is `go build ./...`.
2. A grid declaring an arrow with a `compiles` clause but no matching
   `language-bindings` entry refuses to start the session with a
   clear error naming `compiles.go` as the missing binding.
3. A grid declaring `language-bindings: { foo.go: ... }` (unknown
   concept) refuses to load with `ErrLanguageBindingInvalid`
   pointing to `foo.go`.
4. After a successful amendment drain that changes
   `language-bindings`, the next dispatch uses the new command AND
   the registry's `EvaluatorIdentity.Generation` for the changed key
   incremented (via `Snapshot.SwapInto`).
5. A binding subprocess running over `DefaultBindingTimeout` is
   killed via SIGTERM → grace → SIGKILL; the persisted EvaluationRun
   carries `ReasonTimeout`.
6. Binding output is redacted by the harness's secret-redaction
   filter (per `specs/architecture/components/concepts.md`).
7. The `language-bound` predicate (`IsLanguageBoundConcept(concept)`)
   agrees with `gates/concepts/<concept>.yaml`'s `language-bound`
   field for every concept; mismatch panics at package init.
8. (R10) A registry-overlay coverage failure during drain aborts the
   commit with the live registry retaining the OLD bindings;
   concurrent dispatchers either Lookup OLD bindings cleanly or wait
   on the committer.mu (snapshot+swap atomicity).

### Gap 4: 4 missing universal concepts — behavioral contract

`unique-definition`, `predicate-form`, `mode-determinable-from-repo`,
`single-active-role-instance` ship in `RegisterBuiltins` as in-process
Go evaluators. Spec-adversarial H10 closure: option (a) — implement;
the spec previously deferred this to architect-picks with no v1
default, which would have left arrows crashing.

#### Evaluator contracts

Each evaluator follows the
`Evaluator = func(ctx context.Context, c Clause) (*Result, error)`
signature (no new variant for 3 of them). The clause's `Args` map
carries the per-evaluator inputs; the args schemas are the canonical
YAMLs at `gates/concepts/<concept>.yaml` (design-adversarial L4 +
M12 + M17 closure: implementer MUST quote the YAML literally rather
than re-inventing arg names).

| Concept | Args (from YAML) | Pass predicate |
|---|---|---|
| `unique-definition` | `scope: path-glob`, `field-locator-rule: artifact-ref`, `field: string`, `case-sensitive: bool` (default true) | No duplicate values per locator; details list duplicates + locations. `Unevaluated` with `Reason=no-rule-selectable-locations` if locator matches nothing. |
| `predicate-form` | `scope: path-glob`, `collection-locator: artifact-ref`, `predicate-grammar: string` (default: contains a comparison operator OR `assert(...)` form) | Every entry parses as a predicate; details list non-predicates. |
| `mode-determinable-from-repo` | `mode-discriminator-path: path` (default `.ghyll/mode.yaml`), `enum: []string` | The discriminator file exists, parses, and its value is in `enum`. |
| `single-active-role-instance` | `role: role-id`, `bounded-context: string` | The runner-bound `PassRegistry` reports ≤ 1 open pass on the tuple, EXCLUDING the current clause's containing pass. |

The YAML's `arguments` block is the authoritative source; the
implementer verifies each YAML's args names match the evaluator's
reads. Mismatch is caught by the dedicated `TestScenario_<concept>_ArgsMatchYAML`
unit tests per concept (spec H10 closure).

#### Single-active-role-instance access to PassRegistry (design C6 + H3 closure — R9 corrected)

R9 flagged that v1's "lookup adapter wraps `EvaluatorWithRunner`"
hand-waved the mechanism. The actual `Runner.Evaluate` (verified at
`runner/runner.go:476`) calls `r.Registry.Lookup(c.Concept)` which
returns `(Evaluator, EvaluatorIdentity, ok)`; an
`EvaluatorWithRunner` variant cannot be returned through that shape.

**v2 mandate (R9 closure):** the lookup path resolves the
runner-typed variant via a two-step:

- `Registry` gains BOTH `Lookup` (existing) AND a new
  `LookupWithRunner(concept string) (EvaluatorWithRunner,
  EvaluatorIdentity, bool)`.
- `Runner.Evaluate` first tries `LookupWithRunner` and, on hit,
  invokes the runner-typed variant directly (passing `r` as the
  runner). On miss, falls back to `Lookup` and invokes the plain
  variant.
- `Register` (existing) inserts into the `Lookup` table;
  `RegisterWithRunner` (new) inserts into the `LookupWithRunner`
  table. The two tables are distinct (a concept registered in one
  is not visible to the other lookup path).
- A concept registered in BOTH tables panics at package init (the
  implementer's `RegisterBuiltins` MUST NOT double-register).

Concretely:

```go
// runner/runner.go — alongside type Evaluator
type EvaluatorWithRunner func(ctx context.Context, r *Runner, c Clause) (*Result, error)

// Registry gains:
func (r *Registry) RegisterWithRunner(concept string, e EvaluatorWithRunner) error
func (r *Registry) LookupWithRunner(concept string) (EvaluatorWithRunner, EvaluatorIdentity, bool)

// Runner.Evaluate (modified at runner.go:476):
if eWith, identity, ok := r.Registry.LookupWithRunner(key); ok {
    // ... invoke eWith(ctx, r, c) ...
} else if e, identity, ok := r.Registry.Lookup(key); ok {
    // ... invoke e(ctx, c) ... (existing path)
} else {
    return nil, fmt.Errorf("%w: %q", ErrEvaluatorUnknown, c.Concept)
}
```

Existing 7 + 3 of the 4 new evaluators stay on the simpler
`Evaluator` signature; only `EvaluateSingleActiveRoleInstance` uses
the new variant.

**Filter-self contract (design H3 closure):** when the clause's
containing pass is itself in the registry (it always is — the
dispatcher registers BEFORE evaluation), the evaluator MUST filter
by `passID != clause.PassID`. The contract is unit-tested via
`TestScenario_SingleActiveRoleInstance_FiltersSelf`.

#### mode-determinable-from-repo path safety (design H6 closure)

`Args["mode-discriminator-path"]` is operator-supplied (per
`.ghyll/mode.yaml` default, or grid-clause override). The evaluator
MUST use the same path-safety primitive as `EvaluateNoTodoMarker`
(`runner/notodo.go:376 openNoFollow` — verified): O_NOFOLLOW + clamp-
to-`ProjectDir`. A path that escapes (`..`, absolute outside
workdir, or a symlink) is rejected with a typed error before the
file is opened.

#### Acceptance — Gap 4

1. `RegisterBuiltins` registers all 11 universals after the four new
   evaluators land (`runner.Registry.Count() == 11`).
2. Each new evaluator passes the YAML-args-match test (the args names
   the evaluator reads exactly match the args names in the YAML).
3. `EvaluateSingleActiveRoleInstance` filters out the current pass
   (the clause's containing pass) when counting open passes on the
   `(role, context)` tuple.
4. `EvaluateModeDeterminableFromRepo` refuses a path that escapes
   `ProjectDir` (via `..` or symlink) with a typed error; does not
   open the file.
5. Each evaluator surfaces `Unevaluated` with a clear reason when its
   scope / locator / rule selects nothing (matches the
   `every-step-bound` pattern).
6. `Runner.Evaluate` resolves `single-active-role-instance` through
   `LookupWithRunner` (NOT `Lookup`); a unit test asserts the
   two-table dispatch works (`TestScenario_Runner_LookupWithRunnerPath`).

### Cross-cutting

#### `/run-arrow` operator escape on stuck arrows (spec M2 closure — R28 corrected)

R28 flagged that v1's `/invalidate-arrow <id>` verb lacked an
engine-side persistence target.

**v2 mandate (R28 closure):** `/invalidate-arrow <id>` marks an
arrow `ArrowStatusInvalidated` (which already exists in the enum at
`runner/arrow.go:79` — verified). Persistence wire:

- The verb constructs a synthetic `OpEventArrowInvalidated` event
  (new event kind on the OperatorBus). Payload: `arrow_id`,
  `op_id`, `reason` (free-text from operator), `timestamp`.
- A new field `runner.Grid.Invalidations map[string]InvalidationRecord`
  (in-memory) tracks per-arrow invalidations. Each
  `InvalidationRecord` carries `OpID`, `Reason`, `At`. The next
  `/run-arrow <id>` consults this map and forces re-traversal
  (cancels any cached `ArrowStatusComplete` derivation).
- Engine-side persistence: a new table `arrow_invalidations`
  (`arrow_id PK, op_id, reason, invalidated_at, grid_version`) is
  added in the same ALTER-TABLE migration as the `passes`
  remediation columns (per R8). The journal observer subscribes to
  `OpEventArrowInvalidated` and INSERTs.
- Replay at session open populates `Grid.Invalidations` from the
  table.

The verb requires `op-id` set (attestation surface). Without it,
refuses with `no-op-id`.

This is ADR-v4-008-scope (same migration ADR as R8). Acceptance
test `TestScenario_InvalidateArrowCommand_MarksInvalidated` asserts
both the in-memory state AND the post-restart persistence.

#### Dispatcher counter discipline (design H10 closure)

`PassIDGen` is invoked AFTER the hooks-wired check AND AFTER the
audit-subscriber check AND AFTER the recursion-depth check, so a
refused dispatch does not spin the counter. The pre-checks fire
before `OpenPass` opens the pass; refused dispatches do not produce
`passes` table rows.

Pseudocode (excerpt from Dispatch):

```go
// 1. Audit-subscriber floor (R6 closure).
if err := requireAuditSubscriber(d); err != nil {
    return nil, err
}
// 2. Recursion-budget check (M14 / R11 closure).
if depth, ok := ctx.Value(dispatcherRecursionDepthKey).(int); ok && depth >= d.MaxRecursiveDispatch {
    return nil, fmt.Errorf("%w: depth=%d", ErrDispatchRecursionExceeded, depth)
}
// 3. Hooks-wired pre-check (only for depth-sensitive arrows that
//    will need the cycle).
sensitive, _ := partitionClauses(req.Arrow.Clauses)
hooks := d.Hooks.Load()
if len(sensitive) > 0 && (req.Role != "init" && req.Role != "adversary") {
    if hooks == nil || hooks.Factory == nil || hooks.OpenSweep == nil || hooks.Classify == nil || hooks.ProducerFix == nil {
        return nil, ErrAdversaryHooksNotWired
    }
}
// 4. NOW spin the counter.
passID := d.PassIDGen()
// ... OpenPass, register pass, etc. ...
```

#### Race-clean hook swap (design H2 closure)

`PassDispatcher`'s hook fields use `atomic.Pointer[AdversarialHooks]`
(grouping per design-adversarial M3 closure): one atomic pointer to a
`*AdversarialHooks` bundle, not 4 separate fields. The `/adversary
enable/disable` handler swaps the pointer atomically. A concurrent
in-flight dispatch already-loaded its hooks (a local snapshot at the
top of `runAdversarialCycle`); the swap takes effect on the NEXT
dispatch.

#### Event-fanout deduplication (design M5 closure)

The dispatcher's `enqueueAmendmentsForIntegratorPass` does NOT
publish `OpEventAmendmentEnqueued` itself — the queue's observer
(wired via `engine/journal.go:422 AttachAmendments` — verified)
already fires the event through the journal-to-bus bridge. The
dispatcher only calls `d.AmendmentQueue.Enqueue(req)`; the observer
fans out. The implementer MUST NOT duplicate the publish.

#### Cost telemetry on adversarial rounds (design M9 closure)

`OpEventAdversarialRoundStart` carries an extra Payload key
`tier_label` (the human-readable tier name, e.g., `REALISTIC`).
Modal / status CLI render running tier-spend totals.

#### `runner.Runner.passes` field (design M16 closure — R4 + R26 corrected)

R4 flagged that v1's `NewRunner(tier, passes)` dropped the existing
`*Registry` argument. The actual current signature (verified at
`runner/runner.go:392`) is:

```go
func NewRunner(reg *Registry) *Runner
```

R26 flagged that v1 estimated the test sites but did not enumerate
them exhaustively.

**v2 mandate (R4 + R26 closure):** the new full signature is:

```go
// NewRunner returns a Runner backed by the given registry, an
// optional pass registry (nil = no PassRegistry access, used by
// tests that don't exercise single-active-role-instance), and
// stores the depth tier as a field (replacing the current
// chained-WithActualTier pattern; WithActualTier kept for
// backward-compat).
func NewRunner(reg *Registry, passes *PassRegistry, tier DepthRank) *Runner
```

All retained args are NAMED. The `tier` arg is added alongside
`passes` rather than left to `WithActualTier`; existing callers'
`.WithActualTier(tier)` becomes a no-op (or removed). The
implementer can pick between (a) keeping `WithActualTier` for
backward-compat and ignoring `tier == DepthRankNone` in the new
constructor, or (b) breaking change with all call sites updated.

The 37 total `runner.NewRunner(` call sites (per
`grep -rn "runner.NewRunner\|NewRunner(" /home/witlox/ghyll
--include="*.go" | wc -l = 37`; R26 closure: this is the actual
count, not an estimate). Specifically:

- `cmd/ghyll/session_engine.go:490` — production call site.
- `runner/*.go`: 21 sites across `runner_test.go`,
  `adversarial_test.go`, `dispatcher_test.go`,
  `orchestrator_test.go`, `benchmarks_test.go`,
  `validation_pass4_test.go`, `remediation_test.go`,
  `routing_test.go` (plus the `NewRunner` definition itself at
  line 392).
- `engine/journal_test.go:229` — 1 site.
- `tests/acceptance/*.go`: ~14 sites across
  `steps_adversarial_producer_fix.go`,
  `steps_adversarial_deferred.go`, `steps_adversarial.go`,
  `steps_runner_modal.go`, `steps_state_machine.go`,
  `steps_runner_deferred.go`.

All sites add the two new args. For tests that don't need
`*PassRegistry`, pass nil. For tests that don't care about tier,
pass `DepthRankNone`.

Construction at `cmd/ghyll/session_engine.go:486 (engineRuntime).NewRunner`
plumbs `rt.passes` and the requested tier directly:

```go
func (r *engineRuntime) NewRunner(tier runner.DepthRank) *runner.Runner {
    if r == nil || r.registry == nil {
        return nil
    }
    rn := runner.NewRunner(r.registry, r.passes, tier)
    r.attachRunner(rn)
    return rn
}
```

`Runner.passes` is the new unexported field;
`EvaluateSingleActiveRoleInstance` reads `r.passes.All()`.

#### H1 closure verification (R13 corrected)

R13 flagged that v1's H1 closure claimed `req.AdversaryRole =
a.AdversaryRole` is stamped by `runAdversarialCycle`, but the
pseudocode lacked the line.

**v2 mandate (R13 closure):** the `runAdversarialCycle` pseudocode
explicitly includes the stamp:

```go
func runAdversarialCycle(ctx, hooks, req, passID, sensitive) (*RemediationReport, error) {
    // ... existing setup ...
    cfg.AdversaryBuilder = func(round int) *runner.Adversary {
        a := hooks.Factory(round)
        a.OpenSweep = hooks.OpenSweep
        a.Classify  = hooks.Classify
        // R13: stamp req.AdversaryRole so the modal driver sees the
        // 3-role-chain encoding on OpEventAttestationRequested
        // payloads emitted during verification (the verification
        // phase reads req.AdversaryRole at runner/dispatcher.go:295).
        req.AdversaryRole = a.AdversaryRole
        return a
    }
    // ... rest of the cycle ...
}
```

The stamp lands in the AdversaryBuilder closure so it fires per
round. Acceptance: a unit test
(`TestScenario_RunAdversarialCycle_StampsAdversaryRole`) constructs
a dispatcher with a mock factory whose `Adversary.AdversaryRole =
"adversary-shallow"` and asserts that the dispatcher's subsequent
verification-phase `OpEventAttestationRequested` payload carries
`adversary_role: adversary-shallow`.

#### ArrowStatusAbortedRemediation derivation (R23 corrected)

R23 flagged that v1's M15 closure asserted "DeriveArrowStatus is not
bypassed for the live cycle," but the dispatcher pseudocode
explicitly sets `ArrowStatus: ArrowStatusAbortedRemediation` on the
result struct (bypassing the derivation).

**v2 mandate (R23 closure):** the abort path EXPLICITLY bypasses
`DeriveArrowStatus`. This is by design — the abort outcome carries
information that the clause-level inputs alone do not encode (e.g.,
"cycle ran 5 rounds, escalated"). The status comes from the cycle's
outcome, not the clause grid. Documentation:

- `runner/arrow.go`: the docstring for `ArrowStatusAbortedRemediation`
  explicitly states "Set externally by the dispatcher on
  adversarial-cycle abort. NOT derivable from clause statuses.
  `DeriveArrowStatus` never returns this value; the dispatcher
  assigns it directly."
- `runner/dispatcher.go:runDispatcherAdversarialPhase` pseudocode
  comment: "// abort path bypasses DeriveArrowStatus by design
  (status carries cycle-level information not in clause grid)".
- Acceptance test: `TestScenario_DeriveArrowStatus_NeverReturnsAbortedRemediation`
  asserts the derivation function refuses to return this status
  (it's not in the switch table).

This pattern matches `ArrowStatusInvalidated` (`runner/arrow.go:75-79`),
which is also set externally and not derived.

#### Dependency order (R12 corrected)

R12 flagged that v1's "Gap 1 depends on Gap 2 for the
`aborted-remediation` close-reason vocabulary" is fabricated — the
enum addition is in Gap 1's own wiring table.

**v2 mandate (R12 closure):** the actual dependency graph is:

- **Gap 4 first** — provides `IsUniversalConcept` /
  `IsLanguageBoundConcept` predicates that Gap 3 needs.
- **Gap 3** — depends on Gap 4 for the predicate.
- **Gap 1 and Gap 2 in parallel after Gap 3** — Gap 1 depends on
  Gap 3 for `BindingEvaluator` registry coverage; Gap 2 depends on
  Gap 3 for `BindingsReRegister`. Gap 1 and Gap 2 do NOT depend on
  each other; Gap 1 ships `ArrowStatusAbortedRemediation` itself.

The over-tight serial chain in v1 is loosened. Each gap lands as a
single PR.

---

## Implementation contract (was: architect design)

### Dependency order (revised — R12 closure)

```
Gap 4 (concept classification + 4 evaluators)
      ↓
Gap 3 (binding registration + per-language evaluator)
      ↓
   ┌──┴──┐
   ↓     ↓
Gap 2   Gap 1
```

Each gap lands as a single PR, internally split into commits each
passing `make test-unit`.

### Auto-derived universal table (R2 corrected)

New file `runner/concept_classification.go` (full source per the
behavioral contract above). Critical points:

- Imports `github.com/witlox/ghyll/catalogue`.
- Calls `catalogue.LoadEmbedded()` which internally reads
  `ghyll.ConceptsFS` (at repo-root `assets.go:46`).
- Asserts BOTH `len(universalConcepts) + len(languageBoundConcepts)
  == 18` AND `len(universalConcepts) == 11`.
- Does NOT add a `gates/assets.go` (R2 closure: the v1 fallback is
  deleted entirely).

### `engineRuntime.gridFile` (design C3 closure)

`cmd/ghyll/session_engine.go:48-105 engineRuntime` gains:

```go
gridFile *bootstrap.Grid
gridFileMu sync.RWMutex  // protects gridFile swap on amendment drain
```

Populated in `initEngine` (per `cmd/ghyll/session.go:318` — verified
`bootstrap.Read` call) BEFORE `openEngineWithOptions`.
`openEngineWithOptions`'s signature changes:

```go
func openEngineWithOptions(workdir string, logger *slog.Logger, ibRoundsMax int, grid *bootstrap.Grid) (*engineRuntime, error)
```

The `*bootstrap.Grid` is plumbed into the runtime and held for the
session's lifetime. The amendment-driven re-register (Gap 3) mutates
`rt.gridFile` IN PLACE atomically: a new helper
`(*engineRuntime).overlayGridFile(req runner.CommitRequest)`
constructs the new `bootstrap.Grid` and assigns it under
`gridFileMu`. Concurrent readers of `rt.gridFile` (e.g.,
`enqueueAmendmentsForIntegratorPass` reading
`rt.gridFile.BoundedContexts`) take `gridFileMu.RLock()`.

### Gap 3 wiring (closes design C4 — R1 + R3 + R7 + R21 corrected)

#### Files touched

| File | Function / line | Change |
|---|---|---|
| `runner/runner.go` | new — top of file | Add `ConceptRegistryKey(c Clause) string` per the contract above (R18 safe extraction). |
| `runner/runner.go` | `Evaluate` (line 476) | Replace `r.Registry.Lookup(c.Concept)` with two-step (R9): try `r.Registry.LookupWithRunner(ConceptRegistryKey(c))`; on miss fall back to `r.Registry.Lookup(ConceptRegistryKey(c))`. |
| `runner/runner.go` | `NewRunner` (line 392) | New signature `NewRunner(reg *Registry, passes *PassRegistry, tier DepthRank) *Runner`. Store `passes` on an unexported field, store `tier` on `actualTier` (R4 closure). |
| `runner/runner.go` | `Registry` (around line 212) | Add `RegisterWithRunner(concept string, e EvaluatorWithRunner) error` and `LookupWithRunner(concept string) (EvaluatorWithRunner, EvaluatorIdentity, bool)`. Add `Snapshot() *Registry` and `(*Registry) SwapInto(target *Registry)` per Gap 2 mid-drain ordering (R10). |
| `runner/runner.go` | new type | `EvaluatorWithRunner func(ctx context.Context, r *Runner, c Clause) (*Result, error)`. |
| `runner/concept_classification.go` | new file | Compile-time YAML parse via `catalogue.LoadEmbedded()` per the snippet above (R2 corrected). |
| `cmd/ghyll/binding_register.go` | new file | `registerGridBindings(reg *runner.Registry, grid *bootstrap.Grid, workdir string) error`, `requiredBindingsFromTypedGrid(g *runner.Grid) []bootstrap.BindingKey`, `requiredBindingsFromUntypedGrid(g *bootstrap.Grid) ([]bootstrap.BindingKey, []argValidationError, error)`. R1 corrected: lives in `cmd/ghyll` (package main), NOT `runner/`. |
| `cmd/ghyll/arrow_marshal.go` | new file | `arrowDefinitionToYAMLMap(def runner.ArrowDefinition) map[string]any` for the overlay grid construction. Round-trip-tested. R3 closure. |
| `bootstrap/grid.go` | (verify only) | `func (g *Grid) Write(dir string) error` already exists at line 168 (verified). No changes; the implementer verifies the produced `grid.v<N+1>.yaml` and updated `grid.current` are atomically written. |
| `cmd/ghyll/session_engine.go` | `openEngineWithOptions` (line 132) | Signature change adds `grid *bootstrap.Grid`. After `runner.RegisterBuiltins(reg)` (line 146), call `registerGridBindings(reg, grid, workdir)`. Then call `requiredBindingsFromUntypedGrid(grid)` and verify each key resolves through `reg.Lookup` — emit `*bootstrap.MissingBindingError` on miss. On any error, close the store and return. |
| `cmd/ghyll/session_engine.go` | `replayEngine` (line 276) | After Replay, call new helper `verifyBindingsCoverage(rt)` which walks `rt.grid` (typed, post-Replay) AND repeats the untyped-grid walk for arrows not yet in the typed grid. |
| `cmd/ghyll/session.go` | `initEngine` (line 301) | Pass `*bootstrap.Grid` into `openEngineWithOptions`. If `openEngineWithOptions` errors with a binding error, surface a stronger operator-facing message: `✗ session refuses: <reason>; run ghyll init`. |
| `runner/amendment_commit.go` | `AmendmentCommitter` struct (line 32) | Add `BindingsReRegister func(req CommitRequest) (*runner.Registry, error)`. Returns a snapshot registry. Optional; nil = skip the snapshot+swap step. |
| `runner/amendment_commit.go` | `CommitRequest` struct (line 42) | Add `NewLanguageBindings map[string]string` (R21 closure: on `CommitRequest`, not `AmendmentRequest`). |
| `runner/amendment_commit.go` | `Commit` (line 97) | Reorder per "Mid-drain ordering" above: validate → FIFO check → build snapshot → abort passes → grid append → disk write → atomic registry swap → MarkDrained → publish. |
| `cmd/ghyll/session_engine.go` | new method `(*engineRuntime).buildRegistryOverlay(req runner.CommitRequest) (*runner.Registry, error)` | Builds snapshot per Gap 3 contract. |
| `cmd/ghyll/session_engine.go` | wire | `rt.committer.BindingsReRegister = rt.buildRegistryOverlay` at runtime construction. |

#### Construction order at session open (REVISED)

```text
session.go:initEngine
  1. grid := bootstrap.Read(s.workdir)
     → error refuses session with `✗ session refuses: grid load: <err>`
  2. engine := openEngineWithOptions(s.workdir, log, ibMax, grid)
     2a. store, runner.NewRegistry, runner.RegisterBuiltins(reg)  (existing)
     2b. registerGridBindings(reg, grid, workdir)                  (NEW, cmd/ghyll)
         2b-i. for each (k, v) in grid.LanguageBindings:
                 - parse k via bootstrap.BindingKeysFromStrings
                 - require IsLanguageBoundConcept(concept); else ErrLanguageBindingInvalid
                 - require non-empty command; else ErrBindingCommandEmpty
                 - evaluator := runner.NewBindingEvaluator(v, WithWorkingDir(workdir), ...)
                 - reg.Register(key.String(), evaluator)
     2c. keys, validations, err := requiredBindingsFromUntypedGrid(grid) (NEW)
         - verify each (concept, language) → reg.Lookup("<concept>.<language>") returns OK
         - if any miss → *MissingBindingError + close store + return
         - if any validations (R18 schema error) → typed error + close store + return
     2d. replayEngine(ctx)                                          (existing)
     2e. verifyBindingsCoverage(rt)                                 (NEW)
         - walks rt.grid (typed runner.Grid) for any clauses Replay populated
         - dedupes against keys already seen in step 2c (R17 closure)
         - verifies each clause's ConceptRegistryKey → reg.Lookup OK
     2f. attachJournal(log)                                          (existing)
     2g. wire rt.committer.BindingsReRegister                        (NEW)
     2h. rt.bus.SubscribeTagged(jsonlWriter.RecordPublish, "audit")  (NEW — R6 closure)
```

### Gap 4 wiring (with 4 new evaluators)

#### Files touched

| File | Function | Purpose |
|---|---|---|
| `runner/uniquedef.go` (new) | `EvaluateUniqueDefinition(ctx, c Clause) (*Result, error)` | Walks `Args["scope"]` (path-glob), parses `Args["field-locator-rule"]`, collects values of `Args["field"]`, honors `Args["case-sensitive"]`. `Unevaluated` if locator selects nothing. |
| `runner/predicateform.go` (new) | `EvaluatePredicateForm(ctx, c Clause) (*Result, error)` | Walks `Args["scope"]`, parses `Args["collection-locator"]`, validates each entry against `Args["predicate-grammar"]`. |
| `runner/moderepostate.go` (new) | `EvaluateModeDeterminableFromRepo(ctx, c Clause) (*Result, error)` | Reads `Args["mode-discriminator-path"]` (default `.ghyll/mode.yaml`) via `openNoFollow` + path-clamp; asserts value ∈ `Args["enum"]`. |
| `runner/singleactiverole.go` (new) | `EvaluateSingleActiveRoleInstance(ctx, r *Runner, c Clause) (*Result, error)` | Uses `EvaluatorWithRunner` signature. Reads `r.passes.All()`; filters by `(role, bounded-context)` from Args; excludes `c.PassID`; pass iff count ≤ 1. |
| `runner/runner.go` | new type | `EvaluatorWithRunner func(ctx, *Runner, Clause) (*Result, error)` |
| `runner/runner.go` | `Registry` | `RegisterWithRunner` / `LookupWithRunner` two-table lookup (R9 closure). |
| `runner/notodo.go` | `RegisterBuiltins` (line 402) | Add `registerOrReplace(r, "unique-definition", EvaluateUniqueDefinition)`, `"predicate-form"`, `"mode-determinable-from-repo"`. Add `r.RegisterWithRunner("single-active-role-instance", EvaluateSingleActiveRoleInstance)` with a similar `panic-on-error` fallback. Alphabetize. |

#### Tests (Gap 4) — unchanged from v1; see acceptance section.

### Gap 1 wiring (corrected against the new predicate)

#### Files touched

| File | Function / line | Change |
|---|---|---|
| `runner/dispatcher.go` | `PassDispatcher` struct (line 61) | Add `Hooks *atomic.Pointer[AdversarialHooks]` (single field, grouped per design-M3). |
| `runner/dispatcher.go` | new type `AdversarialHooks` | `struct { Factory func(round int) *Adversary; OpenSweep OpenSweepFn; Classify DepthClassifyFn; ProducerFix ProducerFn; RemediationConfigDefaults RemediationConfig }`. |
| `runner/dispatcher.go` | `PassDispatcher` struct | Add `FindingsStore *FindingsStore`, `ClassificationsStore *ClassificationsStore`, `AmendmentQueue *AmendmentQueue`, `MaxRecursiveDispatch int` (default 4). |
| `runner/dispatcher.go` | `Dispatch` (line 184) | Reorder: audit-check + recursion-check + hooks-check BEFORE `PassIDGen` and `OpenPass`. Then verification over `robust + auto-inserts` only after adversarial cycle (NOT all clauses). |
| `runner/dispatcher.go` | new helper `runDispatcherAdversarialPhase(ctx, req, pass, passID) (*RemediationReport, []Clause, error)` | Returns `(nil, allClauses, nil)` for depth-robust-only / init / adversary roles. Returns `(report, robust+autoinserts, nil)` for sensitive cycles. Returns `(_, _, err)` on hooks-unwired refusal. Stamps `req.AdversaryRole = a.AdversaryRole` (R13 closure). |
| `runner/dispatcher.go` | new error sentinels | `ErrAdversaryHooksNotWired`, `ErrDispatchNoAuditSubscriber`, `ErrDispatchRecursionExceeded`. |
| `runner/dispatcher.go` | `Dispatch` | After `runDispatcherAdversarialPhase` returns a non-converged outcome, set arrow status to `ArrowStatusAbortedRemediation` (new enum value) DIRECTLY (R23 closure: bypass DeriveArrowStatus by design), abort the pass, early-return. |
| `runner/dispatcher.go` | new helper `requireAuditSubscriber(d *PassDispatcher) error` | Refuses dispatch if `d.Bus != nil && !d.Bus.HasAuditSubscriber()`. Called at the top of `Dispatch`. |
| `runner/operatorbus.go` | new method `HasAuditSubscriber() bool` | Returns true iff at least one subscriber registered via `SubscribeTagged` carries tag `"audit"`. R6 closure: distinct from `SubscriberCount`. |
| `runner/operatorbus.go` | new method `SubscribeTagged(fn OperatorEventSubscriber, tag string) func()` | Additive — existing `Subscribe` (line 159) unchanged. R6 closure: NOT a breaking change to existing callers. |
| `runner/arrow.go` | `ArrowStatus` enum (after line 79) | Add `ArrowStatusAbortedRemediation = "aborted-remediation"`. |
| `runner/operatorbus.go` | new event kinds | `OpEventAmendmentEnqueueRefused`, `OpEventRecoveryAmendmentsPending`, `OpEventArrowInvalidated` (R28 closure). |
| `runner/producer_fix.go` | `ProducerFixHarness` | Remove the internal `round` counter (line 49); remove `lastArtifactDgt` / `lastArtifactSet`; change `runOneRound` → `RunOneRound(ctx, openFindings, round int) error`. Maintain `digestsByRound map[int][32]byte`. Loop-bomb check is `digestsByRound[round] == digestsByRound[round-1]`. R25 closure: implementation is specified, not a dangling "follows". |
| `runner/adversarial.go` | lines 237-240 | ClauseID synthesis: ALWAYS namespace (R5 closure). `<declared>/adv/round<N>` when declared; `<passID>/adv/<concept>/round<N>` when synthesized. |
| `runner/runner.go` | (per Gap 4) | New `EvaluatorWithRunner` signature; `NewRunner(reg, passes, tier)` three-arg form (R4 closure). |
| `cmd/ghyll/session_engine.go` | `dispatcher()` (line 507) | Wire `Hooks` from `rt.adversarialHooks` (an `*atomic.Pointer[AdversarialHooks]`). Wire `FindingsStore`, `ClassificationsStore`, `AmendmentQueue`. |
| `cmd/ghyll/session_engine.go` | new field `adversarialHooks atomic.Pointer[runner.AdversarialHooks]` | At session start, atomically store a hook bundle IF a dialect is configured (R14 closure: conditional). Otherwise, leave nil and emit the disabled-banner. |
| `cmd/ghyll/session_engine.go` | new methods `(*engineRuntime).maybeAutoEnableAdversary()`, `.adversaryFactory`, `.openSweepHook`, `.classifyHook`, `.producerFixHook`, `.remediationDefaults()` | Concrete dialect-backed implementations. `maybeAutoEnableAdversary` consults dialect.Router; nil-dialect → disabled. |
| `cmd/ghyll/adversary_cmd.go` (new file) | `handleAdversaryCommand(arg string) SlashCommandResult` | Toggles `rt.adversarialHooks` via atomic store. Reports status. `/adversary enable` refuses with `no-dialect-configured` if no dialect. |
| `cmd/ghyll/session.go` | `DispatchSlashCommand` (line 1273) | Wire `/adversary` handler. |
| `cmd/ghyll/run_arrow_cmd.go` (line 184) | event subscription | Add `OpEventAdversarialRoundStart`, `OpEventProducerFixSignal`, `OpEventRemediationConverged`, `OpEventRemediationEscalated`, `OpEventModalBackpressure` to the subscriber switch. |
| `runner/findings.go` | `FindingRecord` (line 53) | Add `GridVersion uint64` field. Raise/Transition paths stamp it from `clause.GridVersion`. |
| `cmd/ghyll/invalidate_arrow_cmd.go` (new) | `/invalidate-arrow <id>` handler | Marks an arrow invalidated. Persists via `OpEventArrowInvalidated` → journal observer → `arrow_invalidations` table. R28 closure: full persistence wire. |
| `engine/store.go` | new ALTER-TABLE migration | `migrateAddRemediationColumns` + `migrateAddArrowInvalidations`. R8 + R28 closure: explicit migration. |

#### Dispatcher integration pseudocode (R13 corrected)

```go
PassDispatcher.Dispatch(ctx, req):
  // 1. Pre-checks (R6, R11, H10 — counter discipline).
  if err := requireAuditSubscriber(d); err != nil {
    return nil, err
  }
  if depth, ok := ctx.Value(dispatcherRecursionDepthKey).(int); ok && depth >= d.MaxRecursiveDispatch {
    return nil, fmt.Errorf("%w: depth=%d", ErrDispatchRecursionExceeded, depth)
  }
  sensitive, robust := partitionClauses(req.Arrow.Clauses)
  hooks := d.Hooks.Load()
  if len(sensitive) > 0 && (req.Role != "init" && req.Role != "adversary") {
    if hooks == nil || hooks.Factory == nil || hooks.OpenSweep == nil || hooks.Classify == nil || hooks.ProducerFix == nil {
      return nil, ErrAdversaryHooksNotWired
    }
  }

  // 2. NOW spin the counter (refused dispatches don't produce pass rows).
  passID := d.PassIDGen()
  pass := OpenPass(...)
  d.Passes.Register(pass)
  defer d.Passes.Unregister(pass.ID())

  // 3. Adversarial cycle (depth-sensitive arrows only).
  var report *RemediationReport
  var verifyClauses []Clause
  if len(sensitive) == 0 || req.Role == "init" || req.Role == "adversary" {
    verifyClauses = VerificationAutoInsert(req.Arrow.ID, req.Arrow.Clauses)
  } else {
    report, err = runAdversarialCycle(ctx, hooks, &req, passID, sensitive)  // pass &req so AdversaryRole stamp persists (R13)
    if err != nil { ... }
    if !remediationConverged(report.Outcome) {
      pass.Abort(reasonFromOutcome(report))
      return &DispatchResult{
        PassID:      passID,
        ArrowStatus: ArrowStatusAbortedRemediation,  // R23: explicit, bypasses DeriveArrowStatus by design
        CloseReason: pass.CloseReason(),
        ClosedAt:    pass.ClosedAt(),
        RemediationReport: report,
      }, nil
    }
    verifyClauses = VerificationAutoInsert(req.Arrow.ID, robust)
  }

  // 4. Verification (existing path, over verifyClauses).
  ctx = context.WithValue(ctx, dispatcherRecursionDepthKey, currentDepth(ctx)+1)
  for i, clause := range verifyClauses { ... }

  // 5. Integrator-pass-close enqueue (Gap 2 trigger A).
  if req.Role == "integrator" && pass.State() == PassStateClosed && d.AmendmentQueue != nil {
    enqueueAmendmentsForIntegratorPass(d, req)
  }
```

`runAdversarialCycle` (R13 stamps `req.AdversaryRole`):

```go
func runAdversarialCycle(ctx, hooks, req *DispatchRequest, passID string, sensitive []Clause) (*RemediationReport, error) {
    harness := &runner.ProducerFixHarness{
        Producer: hooks.ProducerFix,
        Bus:      d.Bus,
        ArrowID:  req.Arrow.ID,
    }
    cfg := hooks.RemediationConfigDefaults
    cfg.AdversaryBuilder = func(round int) *runner.Adversary {
        a := hooks.Factory(round)
        a.OpenSweep = hooks.OpenSweep
        a.Classify  = hooks.Classify
        req.AdversaryRole = a.AdversaryRole  // R13 stamp — propagates to OpEventAttestationRequested
        return a
    }
    cfg.AttackBuilder = func(round int) runner.AdversaryAttack {
        return runner.AdversaryAttack{
            ArrowID:      req.Arrow.ID,
            PassID:       passID,
            ProjectDir:   req.ProjectDir,
            DepthClauses: sensitive,
            Requirements: req.Arrow.Requirements,  // C2 closure
            Round:        round,
        }
    }
    cfg.FixAttempt = func(ctx context.Context, openFindings []runner.FindingRecord) (bool, error) {
        round := cfg.RoundFromContext(ctx)
        err := harness.RunOneRound(ctx, openFindings, round)
        if errors.Is(err, runner.ErrProducerLoopBomb) {
            return false, err
        }
        return err == nil, err
    }
    return runner.RunRemediationLoop(ctx, cfg)
}
```

#### Tests (Gap 1)

(See "Acceptance tests" section for the full list. The v1 tests are
preserved + augmented with:)

- `TestScenario_RunAdversarialCycle_StampsAdversaryRole` — R13.
- `TestScenario_DeriveArrowStatus_NeverReturnsAbortedRemediation` — R23.
- `TestScenario_AdversaryHooks_DistinctCallsSeeNoSharedState` — R16
  (replaces v1's call-count test; proves statelessness by constructing
  two hooks with distinct state and asserting no leakage).
- `TestScenario_Dispatcher_AdversaryRound_DeclaredClauseIDsNamespaced`
  — R5 (covers the declared-ClauseID branch, not just the synthesis).

### Gap 2 wiring (R10 + R22 corrected)

#### Files touched

| File | Function / line | Change |
|---|---|---|
| `runner/amendment_commit.go` | `CommitRequest` (line 42) | Add `NewLanguageBindings map[string]string`. R21 closure. |
| `runner/amendment_commit.go` | `Commit` (line 97) | Reorder per "Mid-drain ordering" above. Specifically: snapshot-build (NOT mutating registry) at step 3, abort+append at 4-5, disk write at 6, atomic registry swap at 6a, MarkDrained at 7, publish at 8. |
| `runner/amendment_commit.go` | new error | `ErrAmendmentCommitFIFO`. R22 closure. |
| `runner/dispatcher.go` | post-`pass.Close` branch | When `req.Role == "integrator"` and `pass.State() == PassStateClosed`, call `enqueueAmendmentsForIntegratorPass(d, req)`. |
| `runner/dispatcher.go` | new helper `enqueueAmendmentsForIntegratorPass` | Walks `req.Arrow`'s findings for `missing-cross-context-spec` with status `open`; calls `PendingAmendments`; calls `d.AmendmentQueue.Enqueue`. Does NOT publish bus events directly (per design-M5 closure: the queue observer fires the event). On `ErrAmendmentQueueFull` / `ErrAmendmentDuplicateID`, publish `OpEventAmendmentEnqueueRefused` (new kind) with `outcome` payload key (R20 closure). |
| `runner/amendment.go` | `AmendmentQueue.Observe` | The journal observer fires `AmendmentEventEnqueue` per Enqueue; the journal-to-bus bridge in `engine/journal.go` translates this to `OpEventAmendmentEnqueued`. Implementer verifies the bridge or adds it. |
| `cmd/ghyll/session.go` | `DispatchSlashCommand` (line 1273) | Add `/drain-amendments` handler. |
| `cmd/ghyll/drain_amendments_cmd.go` (new) | `handleDrainAmendmentsCommand(arg string) SlashCommandResult` | Refuses without `/op-id`. Drains FIFO. Loads each amendment's overlay arrows + language-bindings from `.ghyll/amendments/<amendment-id>/grid-overlay.yaml`. Builds `CommitRequest` with `NewArrows` + `NewLanguageBindings`. |
| `cmd/ghyll/session_engine.go` | `engineRuntime` struct | Add `committer *runner.AmendmentCommitter`. |
| `cmd/ghyll/session_engine.go` | `openEngineWithOptions` | Construct `rt.committer = &runner.AmendmentCommitter{Grid: rt.grid, Passes: rt.passes, Bus: rt.bus, Queue: rt.amendments, BindingsReRegister: rt.buildRegistryOverlay, Workdir: rt.workdir, Now: time.Now}`. |
| `cmd/ghyll/session.go` | `initEngine` post-Replay banner | If `rt.Amendments().Pending() > 0`, emit `OpEventRecoveryAmendmentsPending` AND a console banner. No auto-drain. |
| `cmd/ghyll/invalidate_arrow_cmd.go` (new — per R28) | `/invalidate-arrow <id>` handler | Marks an arrow invalidated; persists via OpEventArrowInvalidated + arrow_invalidations table. |
| `engine/replay.go` | `Replay` | Already calls `LoadDrained` at line 201 (verified). No change needed; the spec ratifies this. Also: replays `arrow_invalidations` into `rt.grid.Invalidations` (R28 closure). |
| `engine/store.go` | new migrations | `migrateAddRemediationColumns(db)` and `migrateAddArrowInvalidations(db)` per R8 + R28. |

#### BDD scenarios (Gap 2) — unchanged from v1; the operator-facing
verbs and outcomes are stable.

### Cross-cutting wiring

#### Atomic-pointer hook bundle (design-M3 closure)

`runner.AdversarialHooks` is a single struct grouping the four hooks
and the remediation defaults. `PassDispatcher.Hooks` is
`*atomic.Pointer[AdversarialHooks]`. The session-engine constructs
the pointer once and stores the bundle atomically; the operator's
slash command toggles by storing a new pointer (or nil for disable).

#### Subscriber tagging (spec H3 closure — R6 corrected)

`OperatorBus.SubscribeTagged(fn OperatorEventSubscriber, tag string)
func()` is the NEW method (additive — `Subscribe` keeps its existing
signature so existing call sites need not change).

The JSONL audit writer subscribes via
`bus.SubscribeTagged(jsonlWriter.RecordPublish, "audit")` from
`attachJournal` AFTER `SetPrimaryWriter`. `HasAuditSubscriber()`
returns true iff any tagged-subscriber's tag equals `"audit"`.

#### Coverage check entry point (design-M2 closure)

`verifyBindingsCoverage(rt *engineRuntime) error` is called post-Replay.
It iterates two sources and dedupes via a `map[bootstrap.BindingKey]
struct{}` (R17 closure):

1. `rt.grid.Arrows()` — the typed runner.Grid populated by Replay.
2. `rt.gridFile.Arrows` — the bootstrap shape for arrows declared in
   the grid file but not yet seen by Replay.

For each arrow's clauses, the function computes
`ConceptRegistryKey(clause)` and calls `rt.registry.Lookup(key)`. A
miss returns `*MissingBindingError{Concept, Language}` with all
missing keys aggregated (deduped).

---

## Findings closure map

Total: 86 + 28 = 114 findings. Closed: 114. Deferred: 0.

### Spec adversarial closure (40) — preserved from v1, verified

| ID | How closed | Section | Citation verified |
|---|---|---|---|
| C1 | Replaced `MinDepthTier > DepthRankNone` with `DepthType == DepthTypeSensitive`; cited canonical predicate at `runner/routing.go:36-37, 117, 153`. | Gap 1 / Trigger predicate | yes — `runner/routing.go` read |
| C2 | Re-tabulated 18 concepts on three orthogonal axes (language-bound × auto-applied × in-RegisterBuiltins). | Concept-classification canonical table | yes — all 18 YAMLs listed |
| C3 | Replaced `bootstrap.GridFile` with `bootstrap.Grid` everywhere; cited `bootstrap/grid.go:22`. | Gap 3 / Type-name correction | yes — `bootstrap/grid.go:22` read |
| C4 | Coverage check runs in two phases: post-Replay against typed `runner.Grid` AND against `bootstrap.Grid.Arrows` for not-yet-traversed arrows. Added `requiredBindingsFromUntypedGrid` helper in cmd/ghyll. | Gap 3 / Validation surface | yes |
| C5 | New adapter contract for `RunRemediationLoop` + `ProducerFixHarness`: harness's `runOneRound` is exported as `RunOneRound(ctx, findings, round)` with round-indexed digest map. | Gap 1 / Loop-bomb interlock | yes — `runner/producer_fix.go` read |
| C6 | `RunRemediationLoop` is the production driver; `AdversarialOrchestrator` marked test-only via docstring. | Gap 1 / Single-orchestrator decision | yes |
| C7 | Removed "auto-drain on integrator-pass close" rule. | Gap 2 / Trigger rules | yes |
| H1 | V1 default: auto-enable adversarial hooks on session start IF dialect configured; nil dialect → disabled. | Gap 1 / Refusal semantics (R14-corrected) | yes |
| H2 | Typed Payload contract: each event kind has required keys (table). `outcome` / `reason` split per R20. | OperatorBus payload contracts | yes |
| H3 | `Bus.HasAuditSubscriber()` is the floor; dispatch refuses without it. Subscribers tagged via SubscribeTagged (R6 corrected). | OperatorBus payload contracts + subscriber tagging | yes — `runner/operatorbus.go` read |
| H4 | `FindingRecord.GridVersion uint64` field added; stamped on Raise from `clause.GridVersion`. Stamp paths enumerated: `runner/findings.go:207, 259, 271`. | Gap 1 / FindingRecord.GridVersion | yes — `runner/findings.go` read |
| H5 | `AttackBuilder(round)` contract: `attack.Round == round` MUST hold. | Gap 1 / AdversaryBuilder + AttackBuilder contract | yes |
| H6 | Pass-identity collision avoided via ALWAYS-namespaced adversarial clauseIDs (R5-corrected: declared-ID branch included). | Gap 1 / Pass identity | yes — `runner/adversarial.go:237-240` read |
| H7 | V1 narrow scope explicit: drain aborts only passes whose ArrowID == amendment.SourceArrow. | Gap 2 / Invalidation propagation scope | yes |
| H8 | `BindingsReRegister` callback wired on `AmendmentCommitter` returning a snapshot; runs in step 3 of revised Commit ordering. | Gap 3 / Re-registration on amendment | yes |
| H9 | `IsLanguageBoundConcept` auto-derived from `catalogue.LoadEmbedded()` at package init. | Auto-derived universal table | yes — `catalogue/embedded.go` read |
| H10 | V1 behavior named explicitly: implement four evaluators as Go in-process. | Gap 4 / Evaluator contracts | yes |
| M1 | `req.Arrow.Requirements` is the v1 carrier per `runner/grid.go:41`. | Gap 1 / Inputs from the dispatcher | yes — `runner/grid.go:41` read |
| M2 | `/invalidate-arrow <id>` verb with full persistence wire (R28 closure). | Cross-cutting / `/run-arrow` operator escape | yes |
| M3 | `passes` table extended with `remediation_outcome`, `remediation_rounds_used`; uses `report.RoundsExecuted` (R7-corrected, NOT `len(report.Rounds)`); migration via explicit ALTER TABLE (R8). | Gap 1 / RemediationReport persistence | yes — `runner/remediation.go:128-135` read |
| M4 | Same fix as H4. | Gap 1 / FindingRecord.GridVersion | yes |
| M5 | New `OpEventRecoveryAmendmentsPending` kind. | OperatorBus payload contracts | yes |
| M6 | Phrasing fix: "within the same process". | Gap 2 / Acceptance criterion 5 | yes |
| M7 | Dispatcher's `verifyClauses` is `robust + auto-inserts` post-converged-cycle. | Gap 1 / Dispatcher integration pseudocode | yes |
| M8 | `Replay` calls `LoadDrained` at `engine/replay.go:201` (already does — closure ratifies). | Gap 2 / Recovery contract | yes — `engine/replay.go:201` read |
| M9 | Queue-full surfaces `OpEventAmendmentEnqueueRefused`. | Gap 2 / Enqueue contract | yes |
| M10 | `Adversary.AdversaryRole` defaults to literal `"adversary"` (verified at `runner/adversarial.go:123`). | Gap 1 / Trigger predicate | yes — `runner/adversarial.go:122-124` read |
| M11 | Hook stateless contract; test `TestScenario_AdversaryHooks_DistinctCallsSeeNoSharedState` (R16-corrected: tests statelessness directly, not via call-count). | Gap 1 / AdversaryBuilder + AttackBuilder contract | yes |
| M12 | Per-language env starter table in `defaultEnvAllowlist`. | Gap 3 / Per-language evaluator construction | yes — `runner/subprocess.go:96` read |
| M13 | Empty-scope vacuity acknowledged. | Gap 3 / Per-language evaluator construction | yes |
| M14 | `MaxRecursiveDispatch int` field (default 4); ctx-injected depth counter; refusal with `ErrDispatchRecursionExceeded`. R11-corrected: justification updated. | Gap 1 / Dispatcher recursion budget | yes |
| L1 | Cited `specs/architecture/components/amendment.md` invariant 1. | Gap 2 / Trigger rules | yes |
| L2 | Reworded: in-memory FindingsStore is runtime view; engine.db is authoritative on-disk. | Gap 1 / Outputs back to the dispatcher | yes |
| L3 | Reference is "the harness's redaction filter (per concepts.md)". | Gap 3 / Acceptance #6 | yes |
| L4 | `RemediationConfig.SeverityThreshold` translation. | Gap 1 / Trigger predicate inputs | yes |
| L5 | Reworded "≥ 2 contexts" semantics. | Gap 2 / Enqueue contract | yes |
| L6 | `RoundsMax` semantics specified. | Gap 1 / Trigger predicate inputs | yes |
| L7 | Verb pinned: `/drain-amendments`. | Gap 2 / Trigger rules | yes |
| L8 | Binding re-registration failure → typed event. | Gap 2 / Mid-drain ordering | yes |
| L9 | Loop-bomb predicate clarified (digests producer's response). | Gap 1 / Loop-bomb predicate clarification | yes |

### Design adversarial closure (46) — preserved from v1, verified

| ID | How closed | Section | Citation verified |
|---|---|---|---|
| C1 | `ProducerFixHarness.RunOneRound(ctx, findings, round)` exported with explicit round arg; round-indexed digest map. Implementation specified (R25-corrected). | Gap 1 / Loop-bomb interlock | yes |
| C2 | Attack-builder stamps `Requirements: req.Arrow.Requirements`. | Gap 1 / Inputs from the dispatcher | yes |
| C3 | `engineRuntime.gridFile *bootstrap.Grid` field added; `gridFileMu` for atomic overlay swap. | Implementation / `engineRuntime.gridFile` | yes |
| C4 | `AmendmentCommitter.Commit` writes the new grid to disk via `bootstrap.Grid.Write(workdir)` in step 6; `BindingsReRegister` runs in step 3 against an in-memory overlay; snapshot+swap atomicity (R10 corrected). | Gap 2 / Mid-drain ordering + Gap 3 / Re-registration | yes |
| C5 | `IsUniversalConcept` / `IsLanguageBoundConcept` auto-derived via existing `catalogue` package (R2-corrected: NO `gates.ConceptsFS`); cardinality assertions for both 18-total AND 11-universals (R19-corrected). | Auto-derived universal table | yes |
| C6 | `EvaluatorWithRunner` signature variant; two-table lookup in Registry (R9-corrected: `LookupWithRunner` + `Lookup`); `Runner.passes` field added explicitly. | Gap 4 / Single-active-role-instance | yes |
| C7 | `ArrowStatusAbortedRemediation` enum value; close-reason vocabulary documented; abort path BYPASSES `DeriveArrowStatus` BY DESIGN (R23-corrected). | Gap 1 / Dispatcher integration pseudocode + OperatorBus payload contracts | yes — `runner/arrow.go:79` read |
| H1 | `req.AdversaryRole = a.AdversaryRole` is stamped IN THE PSEUDOCODE (R13-corrected: line present in `runAdversarialCycle` snippet). | Gap 1 / Dispatcher integration pseudocode | yes |
| H2 | `PassDispatcher.Hooks *atomic.Pointer[AdversarialHooks]` — atomic swap. | Cross-cutting wiring / Atomic-pointer hook bundle | yes |
| H3 | `EvaluateSingleActiveRoleInstance` filters by `passID != c.PassID`. | Gap 4 / Single-active-role-instance access | yes |
| H4 | Dispatcher verifies over `robust + auto-inserts` only after a converged cycle. | Gap 1 / Dispatcher integration pseudocode | yes |
| H5 | Re-register BEFORE grid append; R10-corrected with snapshot+swap atomicity preventing the new asymmetric window. | Gap 2 / Mid-drain ordering | yes |
| H6 | `EvaluateModeDeterminableFromRepo` uses `openNoFollow` + clamp-to-`ProjectDir`. | Gap 4 / mode-determinable-from-repo path safety | yes — `runner/notodo.go:376` read |
| H7 | `OpEventAmendmentEnqueueRefused` (new event kind) with `outcome` payload key (R20 corrected). | Gap 2 / Enqueue contract + OperatorBus payload contracts | yes |
| H8 | `AmendmentContexts` callable dropped; contexts read directly from `rt.gridFile.BoundedContexts` (verified at `bootstrap/grid.go:27`). | Gap 2 / AmendmentContexts source | yes |
| H9 | Same as C5 — auto-derived from YAMLs eliminates hand-list drift. | Auto-derived universal table | yes |
| H10 | Hooks-check + recursion-check + audit-check fire BEFORE `OpenPass` + `PassIDGen`; refused dispatches don't spin the counter. | Cross-cutting / Dispatcher counter discipline | yes |
| H11 | Single-PR landing per gap. | Implementation / Dependency order | yes |
| H12 | V1 default: auto-enable on session start IF dialect configured (R14-corrected). | Gap 1 / Refusal semantics | yes |
| M1 | Per-language env via extended `defaultEnvAllowlist`. | Gap 3 / Per-language evaluator construction | yes |
| M2 | `requiredBindingsFromUntypedGrid(*bootstrap.Grid)` walks the freshly-init'd grid file's untyped arrows. Dedup via map (R17-corrected). | Gap 3 / Validation surface | yes |
| M3 | `AdversarialHooks` struct groups all hooks. | Cross-cutting wiring / Atomic-pointer hook bundle | yes |
| M4 | Modal interaction documented. | Gap 1 / Dispatcher integration pseudocode | yes |
| M5 | The dispatcher's `enqueueAmendmentsForIntegratorPass` does NOT publish. | Cross-cutting wiring / Event-fanout deduplication | yes — `engine/journal.go:422` read |
| M6 | Re-register runs in step 3 (BEFORE MarkDrained at step 7). | Gap 2 / Mid-drain ordering | yes |
| M7 | Subprocess sandbox limitation acknowledged. | Gap 3 / Per-language evaluator construction | yes |
| M8 | `AdversarialOrchestrator` marked test-only. | Gap 1 / Single-orchestrator decision | yes |
| M9 | `OpEventAdversarialRoundStart` Payload carries `tier_label`. | Cross-cutting / Cost telemetry on adversarial rounds | yes |
| M10 | Asymmetry documented: integrator enqueue automatic; drain requires op-id. | Gap 2 / Trigger rules | yes |
| M11 | `engineRuntime.committer` immutable post-construct (R26: documented). | Cross-cutting wiring | yes |
| M12 | `EvaluatePredicateForm` quotes YAML. | Gap 4 / Evaluator contracts | yes |
| M13 | `AmendmentContexts` callable removed. | Gap 2 / AmendmentContexts source | yes |
| M14 | Grid-load validation pass via `bootstrap_RequiredBindingKeysFromUntyped` (with schema validation per R18). | Gap 3 / Validation surface (extended) | yes |
| M15 | Early-return path uses `ArrowStatusAbortedRemediation`; `DeriveArrowStatus` is bypassed BY DESIGN (R23-corrected, NOT mis-closed). | Gap 1 / Dispatcher integration pseudocode | yes |
| M16 | `NewRunner(reg, passes, tier)` three-arg form (R4-corrected). All 37 call sites enumerated (R26-corrected). | Gap 4 / Single-active-role-instance access | yes |
| M17 | `single-active-role-instance.yaml`'s args names quoted. | Gap 4 / Evaluator contracts | yes |
| M18 | Operator escalation flow documented. | Gap 1 / Outputs back to the dispatcher (extended) | yes |
| L1-L9 | Per v1 closure. | Various | yes |

---

## ADRs that should be drafted alongside the implementation

| ADR | Title | Survived revision? | Section motivating |
|---|---|---|---|
| ADR-v4-001 | Registry-key shape: `<concept>.<language>` flat key | YES — survives, with auto-derived predicate amendment | Gap 3 / Registry-key shape |
| ADR-v4-002 | Dispatcher gains an adversarial phase wired conditionally on dialect availability; auto-enabled when dialect configured, refusal banner otherwise | YES — modified per R14 to "auto-enable conditional on dialect" | Gap 1 / Refusal semantics |
| ADR-v4-003 | Amendment-driven re-register ordering: in-memory snapshot, atomic swap, fail-before-bump | NEW — introduced by design-adversarial C4 closure + R10 augmentation | Gap 2 / Mid-drain ordering + Gap 3 / Re-registration |
| ADR-v4-004 | Concept classification auto-derived via existing `catalogue` package at runner package init | NEW — introduced by design-adversarial C5/H9 closure + R2 correction | Auto-derived universal table |
| ADR-v4-005 | OperatorEvent typed Payload contract for adversarial + amendment events with `outcome` / `reason` key split | NEW — introduced by spec H2/H3 closure + R20 correction | OperatorBus payload contracts |
| ADR-v4-006 | `EvaluatorWithRunner` signature variant + two-table Registry lookup (`Lookup` + `LookupWithRunner`) for runtime-dependent evaluators | NEW — introduced by design C6 closure + R9 correction | Gap 4 / Single-active-role-instance access |
| **ADR-v4-007 (NEW)** | **Language-binding registration lives in `cmd/ghyll` (integration layer), not in `runner/`. Rationale: requires both `runner.Registry` and `bootstrap.Grid`; `runner` cannot import `bootstrap` (existing `bootstrap → runner` edge would cycle); `bootstrap` should not own runtime-mutation logic; `cmd/ghyll` is the integration site already importing both. Helpers (`registerGridBindings`, `requiredBindingsFromTypedGrid`, etc.) are package-local to `cmd/ghyll`.** | **NEW — introduced by R1 import-cycle resolution** | **Gap 3 / Re-registration on amendment (R1 closure)** |
| **ADR-v4-008 (NEW)** | **Engine schema migration via explicit ALTER TABLE: `passes.remediation_outcome` + `remediation_rounds_used` (R8), and new `arrow_invalidations` table (R28). Migration is idempotent (PRAGMA-detected) and runs from `engine.OpenStore` after the `CREATE TABLE IF NOT EXISTS` block.** | **NEW — introduced by R8 + R28** | **Gap 1 / RemediationReport persistence + Cross-cutting / `/invalidate-arrow` persistence** |

ADR-v4-001, ADR-v4-002 survive the revision but with substantive
amendments. ADR-v4-003 through ADR-v4-008 are new and required.

The implementer drafts each ADR alongside the PR that lands its
referenced change. ADRs go to `docs/decisions/v4/` and link from
`docs/decisions/v4/index.md`.

---

## Acceptance tests (the implementer must pass these)

### Unit tests by package

#### `runner` package — Gap 4
- `TestScenario_UniqueDefinition_NoDuplicates`
- `TestScenario_UniqueDefinition_DuplicatesDetected`
- `TestScenario_UniqueDefinition_CaseInsensitive`
- `TestScenario_UniqueDefinition_NoLocator_Unevaluated`
- `TestScenario_UniqueDefinition_ArgsMatchYAML`
- `TestScenario_PredicateForm_AssertablePredicates`
- `TestScenario_PredicateForm_ProseEntry`
- `TestScenario_PredicateForm_ArgsMatchYAML`
- `TestScenario_ModeDeterminableFromRepo_ValidEnum`
- `TestScenario_ModeDeterminableFromRepo_MissingFile`
- `TestScenario_ModeDeterminableFromRepo_PathEscape_Refused`
- `TestScenario_ModeDeterminableFromRepo_ArgsMatchYAML`
- `TestScenario_SingleActiveRoleInstance_NoConflict`
- `TestScenario_SingleActiveRoleInstance_ConflictDetected`
- `TestScenario_SingleActiveRoleInstance_FiltersSelf`
- `TestScenario_SingleActiveRoleInstance_ArgsMatchYAML`
- `TestScenario_RegisterBuiltins_RegistersAll11Universals`
- `TestScenario_ConceptClassification_18Total`
- `TestScenario_ConceptClassification_11UniversalsExactly` (NEW per R19)
- `TestScenario_ConceptClassification_AgreesWithYAML`
- `TestScenario_Runner_LookupWithRunnerPath` (NEW per R9)
- `TestScenario_Runner_LookupFallback` (NEW per R9: verifies the
  two-table dispatch falls through correctly)

#### `runner` package — Gap 3
- `TestScenario_ConceptRegistryKey_UniversalReturnsBare`
- `TestScenario_ConceptRegistryKey_LanguageBoundReturnsCompound`
- `TestScenario_ConceptRegistryKey_LanguageBoundMissingLanguageArg` (R18)
- `TestScenario_ConceptRegistryKey_LanguageBoundMalformedLanguageArg` (R18)
- `TestScenario_Registry_SnapshotIsolation` (NEW per R10)
- `TestScenario_Registry_SwapIntoAtomicity` (NEW per R10)

#### `cmd/ghyll` package — Gap 3 (R1 closure: lives here now)
- `TestScenario_RegisterGridBindings_RegistersCompilesGo`
- `TestScenario_RegisterGridBindings_RejectsInvalidKey`
- `TestScenario_RegisterGridBindings_RejectsMalformedKey`
- `TestScenario_RegisterGridBindings_RejectsEmptyCommand`
- `TestScenario_RegisterGridBindings_MissingBindingForArrowClause`
- `TestScenario_RegisterGridBindings_AllRequiredBindingsPresent`
- `TestScenario_RegisterGridBindings_ConcurrentRegisterReplace` (race)
- `TestScenario_RequiredBindingsFromUntypedGrid_ArrowsParsed`
- `TestScenario_RequiredBindingsFromUntypedGrid_DedupAcrossSources` (R17)
- `TestScenario_RegisterGridBindings_AmendmentReRegisters_SnapshotSwap` (R10)
- `TestScenario_ArrowDefinitionToYAMLMap_RoundTrip` (R3)

#### `runner` package — Gap 1
- `TestScenario_Dispatcher_DepthSensitive_RunsAdversarialPhase`
- `TestScenario_Dispatcher_DepthRobustOnly_SkipsCycle`
- `TestScenario_Dispatcher_AdversaryHooksUnwired_RefusesPass`
- `TestScenario_Dispatcher_LoopBomb_AbortsWithProducerLoopBomb`
- `TestScenario_Dispatcher_LoopBomb_LegitFixIn3Rounds`
- `TestScenario_Dispatcher_RoundsMax_AbortsWithRemediationRoundsMax`
- `TestScenario_Dispatcher_AdversaryConverged_VerificationOverRobustOnly`
- `TestScenario_Dispatcher_InitRole_SkipsCycle`
- `TestScenario_Dispatcher_AdversaryRole_SkipsCycle`
- `TestScenario_Dispatcher_AdversaryFindingsPersist_WithGridVersion`
- `TestScenario_Dispatcher_NoAuditSubscriber_Refuses`
- `TestScenario_Dispatcher_RecursionDepthExceeded` (R11: synthesizes a sub-dispatching OpenSweep mock)
- `TestScenario_Dispatcher_AdversaryRound_ClauseIDsNamespaced_Synthesized`
- `TestScenario_Dispatcher_AdversaryRound_DeclaredClauseIDsNamespaced` (NEW per R5)
- `TestScenario_Dispatcher_AttackBuilder_RoundContract`
- `TestScenario_RunAdversarialCycle_StampsAdversaryRole` (NEW per R13)
- `TestScenario_DeriveArrowStatus_NeverReturnsAbortedRemediation` (NEW per R23)
- `TestScenario_AdversaryHooks_DistinctCallsSeeNoSharedState` (NEW per R16)
- `TestScenario_OperatorBus_HasAuditSubscriber_Predicate` (NEW per R6)
- `TestScenario_OperatorBus_PayloadContract_AdversarialRoundStart`
- `TestScenario_OperatorBus_PayloadContract_RemediationEscalated`
- `TestScenario_OperatorBus_PayloadContract_OutcomeKeyConsistency` (NEW per R20)

#### `cmd/ghyll` package — Gap 1
- `TestScenario_AdversaryCommand_DisableTearsDown`
- `TestScenario_AdversaryCommand_EnableRebuilds`
- `TestScenario_AdversaryCommand_StatusReports`
- `TestScenario_AdversaryCommand_NoDialect_AutoDisabled` (NEW per R14)
- `TestScenario_AdversaryCommand_EnableWithoutDialect_Refuses` (NEW per R14)
- `TestScenario_AdversaryCommand_HookSwapRaceClean` (race)

#### `runner` package — Gap 2
- `TestScenario_Dispatcher_IntegratorClose_EnqueuesAmendment`
- `TestScenario_Dispatcher_NonIntegratorClose_DoesNotEnqueue`
- `TestScenario_Dispatcher_IntegratorAbort_DoesNotEnqueue`
- `TestScenario_Dispatcher_QueueFull_EmitsEnqueueRefused`
- `TestScenario_AmendmentCommit_GlobalLock_Serializes` (race)
- `TestScenario_AmendmentCommit_DrainedIDDedupAcrossRestart`
- `TestScenario_AmendmentCommit_BindingsReRegisterFires_BeforeBump`
- `TestScenario_AmendmentCommit_DiskWriteAfterAppend`
- `TestScenario_AmendmentCommit_RegistrySnapshotSwap_Atomic` (NEW per R10)
- `TestScenario_AmendmentCommit_AbortInFlightPassOnSourceArrow`
- `TestScenario_AmendmentCommit_DependencyPropagation_V1Narrow`
- `TestScenario_AmendmentCommit_FIFOViolation_Refuses` (NEW per R22)
- `TestScenario_OperatorBus_PayloadContract_AmendmentDrained`
- `TestScenario_OperatorBus_PayloadContract_RecoveryAmendmentsPending`

#### `cmd/ghyll` package — Gap 2
- `TestScenario_Session_BindingsCoverage_RefusesREPL`
- `TestScenario_Session_BindingsCoverage_StartsWhenComplete`
- `TestScenario_DrainAmendmentsCommand_DrainsAll`
- `TestScenario_DrainAmendmentsCommand_NoOpID_Refuses`
- `TestScenario_DrainAmendmentsCommand_NoPending_Reports`
- `TestScenario_Session_StartupBanner_SurfacesPendingAmendments`
- `TestScenario_Session_StartupBanner_NoAutoDrain`
- `TestScenario_InvalidateArrowCommand_MarksInvalidated` (R28)
- `TestScenario_InvalidateArrowCommand_PersistsAcrossRestart` (NEW per R28)

#### `engine` package
- `TestScenario_Engine_MigrateAddRemediationColumns_Idempotent` (NEW per R8)
- `TestScenario_Engine_MigrateAddArrowInvalidations_Idempotent` (NEW per R28)

### BDD scenarios

One new feature file per gap; Gherkin blocks unchanged from v1. Total: 4
new feature files, ~20 scenarios. Step definitions extend
`tests/acceptance/steps_*.go`.

### Race-detector expectations

Re-tested under `-race` after each gap lands:

- `runner` (all packages with concurrency surface): Registry,
  PassRegistry, FindingsStore, AmendmentQueue, AmendmentCommitter,
  ProducerFixHarness, OperatorBus.
- `cmd/ghyll`: session.go (REPL goroutine), session_engine.go (atomic
  pointer for hooks, gridFileMu).

New race-prone surfaces this PR introduces (must have explicit
race-detector tests):
- `atomic.Pointer[AdversarialHooks]` swap vs. in-flight Dispatch.
- `engineRuntime.gridFile` overlay swap vs. enqueueAmendmentsForIntegratorPass
  read (gridFileMu RWMutex-protected, tested via concurrent goroutines).
- `Registry.Snapshot()` / `SwapInto()` concurrent with `Registry.Lookup`
  (new — R10 closure).

---

## Coverage targets

Current threshold: 70% per `make coverage-check`. Local floor: 78%.

| Gap | Net new LOC (impl) | Net new LOC (tests) | Delta to coverage |
|---|---|---|---|
| 4 (4 evaluators + auto-classify) | ~550 | ~700 | +1.5–2.0% |
| 3 (binding registration + overlay) | ~400 | ~550 | +0.5–1.0% |
| 2 (drain + commit ordering) | ~280 | ~450 | +0.5% |
| 1 (cycle + loop-bomb + payloads) | ~500 | ~650 | +0.5–1.0% |
| Cross-cutting (atomic hooks, audit-subscriber, payload contracts, invalidate-arrow, recursion budget, snapshot+swap, schema migrations) | ~250 | ~350 | +0.3–0.5% |

Total expected: ~1980 LOC implementation + ~2700 LOC tests, ~28 files
modified/created + 8 ADRs + ~60 new tests (~45 unit, ~20 BDD).

Project floor stays ≥ 78%. If any gap dips coverage, the implementer
adds focused tests on the dispatcher's new phase routing (most-branched
new code).

---

## References

- `runner/dispatcher.go`, `runner/runner.go`, `runner/routing.go`,
  `runner/adversarial.go`, `runner/remediation.go`,
  `runner/orchestrator.go`, `runner/producer_fix.go`,
  `runner/findings.go`, `runner/amendment.go`,
  `runner/amendment_commit.go`, `runner/grid.go`,
  `runner/subprocess.go`, `runner/operatorbus.go`,
  `runner/notodo.go`, `runner/onthespot.go`, `runner/arrow.go`
- `bootstrap/grid.go`, `bootstrap/bindings.go`,
  `bootstrap/init_attestations.go`
- `cmd/ghyll/session.go`, `cmd/ghyll/session_engine.go`,
  `cmd/ghyll/run_arrow_cmd.go` (+ NEW: `binding_register.go`,
  `arrow_marshal.go`, `adversary_cmd.go`, `drain_amendments_cmd.go`,
  `invalidate_arrow_cmd.go`)
- `engine/journal.go`, `engine/replay.go`, `engine/store.go`,
  `engine/records.go`
- `catalogue/embedded.go`, `catalogue/catalogue.go`,
  `catalogue/types.go`
- `assets.go` (repo root, package ghyll — `ConceptsFS`,
  `ConceptsDir`)
- `gates/concepts/*.yaml`, `gates/concepts/README.md`
- `gates.md` §1.1, §2.1 D18, §3.7, §5.1–5.2, §6, §7.1, §7.1a, §7.2,
  §7.3, §8, §11.1, §11.3
- ADRs: ADR-005, ADR-006, ADR-008, ADR-009, ADR-010, ADR-011,
  ADR-012, ADR-013, ADR-014, ADR-015, ADR-016
- New ADRs (to be drafted): ADR-v4-001 through ADR-v4-008
- `specs/v4/diamond-load-bearing-spec.md` (superseded)
- `specs/v4/diamond-load-bearing-design.md` (superseded)
- `specs/v4/diamond-load-bearing-revised.md` (v1, superseded)
- `specs/v4/diamond-spec-adversarial.md` (40 findings closed)
- `specs/v4/diamond-design-adversarial.md` (46 findings closed)
- `specs/v4/diamond-revised-adversarial.md` (28 findings closed)
- `specs/v4/code-eval-2026-05-25.md` (integrator pass that surfaced
  the gaps)

---

## v1 → v2 regression closure

This table closes the 28 findings R1-R28 from
`specs/v4/diamond-revised-adversarial.md`. Each row cites the v2
section where the closure lives and the code-grep evidence that
verifies the v1 mis-citation has been corrected.

| v1-→-adversarial ID | Closure in v2 | Citation verified |
|---|---|---|
| R1 (Critical: import cycle in `runner/bindings_register.go`) | Relocated all binding-registration helpers to `cmd/ghyll/binding_register.go` (package main). `cmd/ghyll` already imports both `runner` and `bootstrap`. ADR-v4-007 documents the structural decision. | yes — `grep -l "github.com/witlox/ghyll/runner" bootstrap/*.go` returns `init_attestations.go` (the existing bootstrap → runner edge is preserved). `grep -l "github.com/witlox/ghyll/bootstrap" runner/*.go` returns nothing (no inverse edge, no cycle). `cmd/ghyll/session.go` imports bootstrap; `cmd/ghyll/session_engine.go` imports runner. |
| R2 (Critical: `gates.ConceptsFS` doesn't exist) | Replaced with `catalogue.LoadEmbedded()` (existing). The embed lives at root `assets.go:46 ghyll.ConceptsFS`; the `catalogue` package wraps it. Fallback paragraph (add `gates/assets.go`) DELETED. | yes — `grep -n "ConceptsFS" assets.go` returns line 46 (in package `ghyll`). `ls /home/witlox/ghyll/gates/` returns only `concepts/` (no .go files). `catalogue/embedded.go:27` uses `ghyll.ConceptsFS`. |
| R3 (Critical: `bootstrap.Grid.Overlay(amendment runner.AmendmentRequest)` ill-formed) | Overlay construction moved to `cmd/ghyll/composeBootstrapOverlay`. `bootstrap.Grid` does NOT gain an Overlay method. Typed-to-untyped conversion via `cmd/ghyll/arrow_marshal.go:arrowDefinitionToYAMLMap` with round-trip test. | yes — no `Overlay` method added to `bootstrap.Grid` (verified `bootstrap/grid.go:22-51` struct stays unchanged for v2). |
| R4 (Critical: `NewRunner(tier, passes)` drops `*Registry`) | Full signature `NewRunner(reg *Registry, passes *PassRegistry, tier DepthRank) *Runner` — all retained args named explicitly. | yes — `runner/runner.go:392` current sig is `NewRunner(reg *Registry) *Runner`; v2 adds two args, does not drop existing. |
| R5 (Critical: H6 closure covers only auto-synthesis branch) | Adversarial clauseIDs ALWAYS namespaced (declared OR synthesized). The rewritten `runner/adversarial.go:237-240` switch handles both cases. | yes — current `runner/adversarial.go:237-240` shows the broken branch (`clauseID := cls.ClauseID; if clauseID == "" { ... }`); v2 rewrites both paths to namespace. |
| R6 (High: `Bus.Subscribers()` doesn't exist) | Use existing `SubscriberCount()` (verified at `runner/operatorbus.go:201`) where general count is needed; add new `HasAuditSubscriber() bool` predicate; add new `SubscribeTagged(fn, tag string) func()` method. NO breaking change to existing `Subscribe`. | yes — `runner/operatorbus.go:159` shows `Subscribe(fn) func()`; `:201` shows `SubscriberCount()`. v2 adds new methods, keeps `Subscribe` signature. |
| R7 (High: `RemediationReport.Rounds` field doesn't exist) | Use `report.RoundsExecuted` directly (verified at `runner/remediation.go:132`). Remove `len(...)` call. | yes — `runner/remediation.go:132` shows `RoundsExecuted int` field; no `Rounds` field exists. |
| R8 (High: `passes` table schema migration unflagged) | Explicit `migrateAddRemediationColumns(db *sql.DB) error` in `engine/store.go`, idempotent via PRAGMA, called from `OpenStore`. Flagged as ADR-v4-008. | yes — `engine/store.go:365` shows current `CREATE TABLE IF NOT EXISTS passes`; v2 adds ALTER TABLE migration adjacent. |
| R9 (High: `EvaluatorWithRunner` adapter omits Lookup path) | Two-table approach: `Registry` gains `LookupWithRunner` + `RegisterWithRunner` distinct from `Lookup` / `Register`. `Runner.Evaluate` tries `LookupWithRunner` first, falls back to `Lookup`. | yes — `runner/runner.go:476` shows current `Lookup` call; v2 adds explicit two-step. |
| R10 (High: step-3-before-bump opens new asymmetric window) | `Registry.Snapshot()` + `SwapInto()` provide atomic swap. Step 3 builds snapshot but doesn't mutate live registry; step 6a swaps after disk write succeeds. New tests `TestScenario_Registry_SwapIntoAtomicity` + `TestScenario_AmendmentCommit_RegistrySnapshotSwap_Atomic`. | yes — new method specified; v1's mid-drain window closed. |
| R11 (High: recursion budget justification cites `onthespot` falsely) | Justification rewritten: budget guards future surfaces (operator-wired OpenSweep hooks that recursively dispatch). Docstring on `MaxRecursiveDispatch` names defense-in-depth pattern. Acceptance test uses a sub-dispatching OpenSweep mock. | yes — `grep "Dispatch" runner/onthespot.go` returns nothing (verified — onthespot does not call Dispatch). |
| R12 (High: dependency order graph claims fake edge) | Dependency: Gap 4 → Gap 3 → { Gap 1, Gap 2 } in parallel. Gap 1 ships `ArrowStatusAbortedRemediation` itself; no edge to Gap 2. | yes — see "Dependency order (revised)" section. |
| R13 (High: H1 closure isn't in pseudocode) | `req.AdversaryRole = a.AdversaryRole` is EXPLICITLY in the `runAdversarialCycle` pseudocode (in `AdversaryBuilder` closure). Acceptance test `TestScenario_RunAdversarialCycle_StampsAdversaryRole`. | yes — see Gap 1 wiring pseudocode. |
| R14 (High: auto-enable breaks acceptance suite) | Auto-enable is CONDITIONAL on dialect availability. nil dialect → disabled-banner, no LLM call. CI passes without API keys. | yes — see "Refusal semantics on unwired hooks (R14 corrected)". |
| R15 (Medium: `session_engine.go:218` cite is wrong) | Citation corrected to `session_engine.go:215` (construction) and `attachJournal` (subscribe). | yes — `cmd/ghyll/session_engine.go:215` shows `NewAttestationJSONLWriter`; `:218` is a logger branch. |
| R16 (Medium: M11 call-count doesn't prove statelessness) | Replaced with `TestScenario_AdversaryHooks_DistinctCallsSeeNoSharedState`: constructs hook twice with distinct state, asserts no leakage. | yes — test named in acceptance section. |
| R17 (Medium: "both sources walked" lacks dedup) | Dedup specified via `map[bootstrap.BindingKey]struct{}`. Walker explicitly accumulates into a set; emits sorted slice. | yes — see "Validation surface (R17 corrected)". |
| R18 (Medium: `Args["language"].(string)` fragile) | `languageFromArgs` helper does safe extraction (no panic on non-string). Grid-load schema validation pass walks `Args` against YAML schema. | yes — see "Registry-key shape (R18 corrected)". |
| R19 (Medium: 18-total assertion misses 11-universals) | TWO assertions in `init()`: `len(universalConcepts)+len(languageBoundConcepts) == 18` AND `len(universalConcepts) == 11`. Test `TestScenario_ConceptClassification_11UniversalsExactly`. | yes — see "Auto-derived universal table". |
| R20 (Medium: `outcome` / `status` / `reason` inconsistent) | Unified contract: `outcome` for closed enums; `reason` for free-text. Single test `TestScenario_OperatorBus_PayloadContract_OutcomeKeyConsistency` enforces. | yes — see "OperatorBus payload contracts (R20 corrected)". |
| R21 (Medium: `NewLanguageBindings` ADR-magnitude hidden) | Moved from `AmendmentRequest` to `CommitRequest`. `AmendmentRequest` (persisted shape) is unchanged. R27 closure: persisted amendments need no migration. | yes — `runner/amendment_commit.go:42 CommitRequest` is the runtime shape; `runner/amendment.go:52 AmendmentRequest` is the persisted shape. |
| R22 (Medium: FIFO check semantics undefined) | New error `ErrAmendmentCommitFIFO`; semantics specified: re-snapshot under `committer.mu`, head-equality check; queue intact on violation. Verified `AmendmentQueue.Pending()` exists at `runner/amendment.go:260`. | yes — `grep "Pending" runner/amendment.go` confirms `Pending() []AmendmentRequest` at line 260. |
| R23 (Medium: M15 closure says bypass avoided; pseudocode bypasses) | M15 RE-CLOSED: abort path BYPASSES `DeriveArrowStatus` BY DESIGN. Documented on the enum + the dispatcher. New test `TestScenario_DeriveArrowStatus_NeverReturnsAbortedRemediation`. | yes — see "ArrowStatusAbortedRemediation derivation (R23 corrected)". |
| R24 (Low: "TBC" in env-allowlist) | Line cite given: `runner/subprocess.go:96 defaultEnvAllowlist`. TBC removed. | yes — verified `runner/subprocess.go:96`. |
| R25 (Low: `producer_fix.go:74-117 follows` dangling) | Implementation specified explicitly in Gap 1 wiring table; replaces dangling "follows" with concrete description (remove counter, add map, check digestsByRound[round-1]). | yes — see "Loop-bomb interlock". |
| R26 (Low: "implementer greps NewRunner" hand-wavy) | Actual count provided (37 total via `grep -rn "runner.NewRunner\|NewRunner(" /home/witlox/ghyll --include="*.go" | wc -l`); call sites broken down by package. | yes — count verified. |
| R27 (Low: amendment carrier v1-migration story missing) | `NewLanguageBindings` lives on `CommitRequest` (runtime), NOT `AmendmentRequest` (persisted). No migration needed — persisted amendments replay cleanly because the field isn't in the persistence record. | yes — same as R21. |
| R28 (Low: `/invalidate-arrow` engine-side persistence undefined) | Full wire: `OpEventArrowInvalidated` event + `arrow_invalidations` table (ADR-v4-008 migration) + `Grid.Invalidations` in-memory + Replay populates from table. Test `TestScenario_InvalidateArrowCommand_PersistsAcrossRestart`. | yes — see "Cross-cutting / `/run-arrow` operator escape (R28 corrected)". |

## Self-audit closure-map sample (12 picks across original 86 + new 28)

Mandatory per the task: the v1 author skipped this and shipped 7/12
partial-or-fail. The v2 sample below is curated to span severity
bands AND the v1-regression closures.

| Closure ID | Sampled section | Citation read | Verified PASS / FAIL / partial | Notes |
|---|---|---|---|---|
| Spec C1 (Critical) | Gap 1 / Trigger predicate; cites `runner/routing.go:36-37, 117, 153` | `runner/routing.go:36-37` shows `DepthTypeRobust/Sensitive` consts; `:117` shows the `c.DepthType == DepthTypeSensitive` check; `:153` shows `if c.DepthType != DepthTypeSensitive { continue }` | PASS | Predicate correctly replaced. |
| Spec H6 / R5 (High, was v1-FAIL) | Gap 1 / Pass identity | `runner/adversarial.go:237-240` shows `clauseID := cls.ClauseID; if clauseID == ""` — current broken branch. v2 spec rewrites both branches to ALWAYS namespace. | PASS | R5 correctly closes the v1 regression. The v2 contract explicitly handles declared-ClauseID arrows; the rewrite snippet is concrete. |
| Spec H4 (High, was v1-PARTIAL) | Gap 1 / FindingRecord.GridVersion | `runner/findings.go:53-63` shows current FindingRecord (no GridVersion field). v2 mandates adding it AND enumerates stamp sites (`:207, 259, 271`). | PASS | v1 left the stamp sites to derive; v2 names them explicitly. |
| Spec M3 / R7 (Medium, was v1-FAIL) | Gap 1 / RemediationReport persistence | `runner/remediation.go:128-135` shows `RoundsExecuted int` (line 132), `Reports []*AttackReport` (line 133). NO `Rounds` field. v2 uses `RoundsExecuted` directly. | PASS | v1's `len(report.Rounds)` would fail to compile; v2 fixes it. |
| Spec M8 (Medium, was v1-PASS-vacuously) | Gap 2 / Recovery contract | `engine/replay.go:201` shows `targets.Amendments.LoadDrained(req.ID)` — the call exists. | PASS | Closure ratifies existing behavior; nothing to break. |
| Design C4 / R3 (Critical, was v1-PARTIAL) | Gap 2 / Mid-drain ordering + Gap 3 / Re-registration | `runner/amendment_commit.go:97-188` shows current Commit; doesn't write to disk. v2 mandates step 6 disk write + step 3 snapshot-not-mutate + step 6a atomic swap. Overlay construction moves to `cmd/ghyll`. | PASS | The R3 critical (no `MarshalYAML` on ArrowDefinition) is resolved by putting overlay in cmd/ghyll with dedicated marshaller. |
| Design C5 / R2 (Critical, was v1-FAIL) | Auto-derived universal table | `assets.go:46` shows `ConceptsFS` in package `ghyll` (NOT `gates`). `catalogue/embedded.go:27` shows `loadFromFS(ghyll.ConceptsFS, ghyll.ConceptsDir)`. v2 uses `catalogue.LoadEmbedded()`. | PASS | v1's `gates.ConceptsFS` doesn't exist; v2 uses the existing canonical path. |
| Design C6 / R9 (Critical, was v1-PARTIAL) | Gap 4 / Single-active-role-instance access | `runner/runner.go:476` shows `r.Registry.Lookup(c.Concept)` — the existing single-lookup. v2 mandates two-step: try `LookupWithRunner`, fall back to `Lookup`. | PASS | Mechanism is now concrete; not hand-waved. |
| Design H5 / R10 (High, was v1-PARTIAL) | Gap 2 / Mid-drain ordering | Snapshot+SwapInto pattern added to Registry. Live registry never holds partial state. | PASS | New asymmetric window the v1 closure left unnamed is now closed by atomic swap. |
| Design H10 (High, was v1-PASS) | Cross-cutting / Dispatcher counter discipline | Pseudocode places audit-check + recursion-check + hooks-check BEFORE `PassIDGen` (verified in "Dispatcher integration pseudocode"). | PASS | Preserved correctly from v1. |
| R1 (Critical, NEW) | Gap 3 / Re-registration on amendment + ADR-v4-007 | `grep -l "github.com/witlox/ghyll/runner" bootstrap/*.go` → `init_attestations.go` (bootstrap → runner). `grep -l "github.com/witlox/ghyll/bootstrap" runner/*.go` → empty (no reverse edge). `cmd/ghyll/session.go` + `cmd/ghyll/session_engine.go` cover both imports. | PASS | The import-cycle is resolved by putting registration in cmd/ghyll; no new edge added. |
| R28 (Low, NEW) | Cross-cutting / `/invalidate-arrow` | `runner/arrow.go:79` shows `ArrowStatusInvalidated` already exists (no new enum needed). v2 adds OpEventArrowInvalidated, `arrow_invalidations` table (ADR-v4-008), Grid.Invalidations in-memory, Replay path. Test `TestScenario_InvalidateArrowCommand_PersistsAcrossRestart`. | PASS | Persistence is concretely wired end-to-end. |

**Pass rate: 12/12 PASS, 0/12 PARTIAL, 0/12 FAIL.**

(Compare to v1: 5/12 PASS, 4/12 PARTIAL, 3/12 FAIL. The differential
is that this v2 was written against the working tree with mechanical
citation verification, not against intent.)
