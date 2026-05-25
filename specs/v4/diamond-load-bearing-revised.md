# Load-bearing wiring — consolidated spec + design (post-adversarial)

## Date: 2026-05-25

## Closes: spec adversarial (40) + design adversarial (46) = 86 findings, no deferrals.

This document supersedes `specs/v4/diamond-load-bearing-spec.md` and
`specs/v4/diamond-load-bearing-design.md`. It folds the analyst
behavioral contract and the architect implementation contract into a
single artifact and rewrites the seams the two adversarial passes
broke. Citation form is `pkg/file.go:line`, against the working tree
at HEAD on 2026-05-25.

The originals stay on disk for traceability but are NOT inputs to the
implementer. Where the spec and design contradicted each other (because
the design built on the wrong spec) the spec finding wins — fixed at
the spec layer first, then re-derived downstream.

---

## Behavioral contract (was: analyst spec)

### Scope and frame

`specs/v4/code-eval-2026-05-25.md` surfaced four runtime gaps reachable
from acceptance tests but unreachable from production. This contract
defines WHEN each load-bearing seam fires, WHAT inputs it needs, WHAT
outputs feed back, and WHAT termination conditions prevent runaway.
The four gaps:

1. **Adversarial cycle never runs in production** —
   `runner/adversarial.go:Adversary.Attack`,
   `runner/remediation.go:RunRemediationLoop`, and
   `runner/orchestrator.go:AdversarialOrchestrator` are reachable only
   from tests. `runner/dispatcher.go:184 Dispatch` runs verification
   clauses and returns; it never enters the §11 adversarial phase.
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
`gates/concepts/README.md`'s auto-applied groupings — is:

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

The 11 `language-bound: false` concepts MUST resolve to an in-process
Go evaluator before the first dispatch (7 already do; 4 land in Gap 4).
The 7 `language-bound: true` concepts MUST resolve to a project-
declared `BindingEvaluator` (Gap 3).

This table is the runtime predicate — see "Auto-derived universal
table" in the implementation contract for how the runner stays
synchronized with the YAMLs at compile time.

### Gap 1: Adversarial cycle — behavioral contract

#### Trigger predicate (spec-adversarial C1, hard correction)

The adversarial cycle is a phase of every arrow that carries at least
one clause with **`DepthType == DepthTypeSensitive`** (per
`runner/routing.go:117` and `runner/routing.go:153`). It is NOT
`MinDepthTier > DepthRankNone` — a depth-robust clause is allowed to
carry `MinDepthTier == NONE`, while a depth-sensitive clause MUST
carry `MinDepthTier >= SHALLOW` (per `runner/routing.go:122`). Using
`MinDepthTier` as the partitioning predicate misclassifies a
depth-sensitive clause that mis-declared its tier as depth-robust and
skips the cycle.

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

`runner.AdversaryAttack` (per `runner/adversarial.go:64-69`) is
stamped from `DispatchRequest req` as follows:

| Field | Source | Required |
|---|---|---|
| `ArrowID` | `req.Arrow.ID` | yes |
| `PassID` | `req.PassIDGen()` output, stamped once per dispatch | yes |
| `ProjectDir` | `req.ProjectDir` | yes |
| `DepthClauses` | the `sensitive` partition above | yes |
| `Requirements` | `req.Arrow.Requirements` (per `runner/grid.go:41`); the cycle calls `ClassificationsStore.DeclareRequirement` per requirement at `runner/adversarial.go:222` — design-adversarial C2 closed by mandating this stamp | yes |
| `Round` | injected by the loop driver per round, never by the dispatcher | yes |

#### Pass identity (spec H6 closure)

To prevent `(PassID, ClauseID)` collisions between the adversarial and
verification phases, clauseIDs are namespace-prefixed:

- Adversarial phase: `fmt.Sprintf("%s/adv/%s/round%d", passID, concept, round)`
  (matches current `runner/adversarial.go:239` modulo the new `adv/`
  segment).
- Verification phase: `fmt.Sprintf("%s/verify/%s", passID, concept)`.

One pass-id per arrow per traversal (per ADR-008); namespacing is at
the clauseID layer. The implementer MUST update
`runner/adversarial.go:239` to emit `<passID>/adv/<concept>/round<N>`
verbatim.

#### Single-orchestrator decision (spec C6 closure)

`runner/remediation.go:RunRemediationLoop` is the production driver.
`runner/orchestrator.go:AdversarialOrchestrator` is retained as
**test-only / sub-activity helper** for unit tests that exercise
clause-falsification + open-sweep + classify in isolation. The
dispatcher MUST NOT call `AdversarialOrchestrator.Run` directly. The
implementer adds a docstring comment on `AdversarialOrchestrator`
naming it test-only and refactors its call sites if any test path
implies production use.

#### Loop-bomb interlock (spec C5 + design C1 closure)

The two named APIs do not compose as the prior spec wrote them. The
loop is `RunRemediationLoop` (signature `FixAttemptFn = func(ctx,
[]FindingRecord) (madeProgress bool, err error)`,
`runner/remediation.go:51`); the harness is `ProducerFixHarness` (its
inner `ProducerFn = func(ctx, []FindingRecord, round int) ([]byte,
error)`, `runner/producer_fix.go:57`); they need an adapter.

**Adapter contract.** The dispatcher constructs:

```go
harness := &runner.ProducerFixHarness{
    Producer: d.ProducerFix,   // operator-wired ProducerFn
    Bus:      d.Bus,
    ArrowID:  req.Arrow.ID,
}
cfg.FixAttempt = func(ctx context.Context, openFindings []FindingRecord) (bool, error) {
    err := harness.RunOneRound(ctx, openFindings)   // new exported method
    if errors.Is(err, ErrProducerLoopBomb) {
        return false, err   // madeProgress=false; surfaces hook-error budget
    }
    return err == nil, err
}
```

`runner/producer_fix.go:74 runOneRound` MUST be exported as
`RunOneRound` so the dispatcher can call it directly without round
the
through the `ProducerRemediate()` closure (which targets
`AdversarialOrchestrator`'s shape, not `RunRemediationLoop`'s).

**Round-counter alignment (design C1).** `ProducerFixHarness`
maintains its own monotonic round counter (`producer_fix.go:83`). The
remediation loop also has a round counter. To prevent the
false-positive `ErrProducerLoopBomb` design-adversarial C1 named,
`RunOneRound` MUST accept an explicit `round int` argument:

```go
func (h *ProducerFixHarness) RunOneRound(ctx context.Context, openFindings []FindingRecord, round int) error
```

Internal `h.round` is removed; the caller (the loop) owns the round
number. `lastArtifactDgt` becomes a per-round map
(`map[int][32]byte`) so the loop-bomb check is "this round's digest
equals the previous round's digest" — index by absolute round, not by
"the harness's idea of round counter." Implementation rewrite at
`producer_fix.go:74-117` follows.

#### AdversaryBuilder + AttackBuilder contract (spec H5 closure)

The loop config holds:

```go
cfg.AdversaryBuilder = func(round int) *Adversary  // fresh per round, per ADR-014
cfg.AttackBuilder    = func(round int) AdversaryAttack
```

Invariant: `cfg.AttackBuilder(round).Round == round` MUST hold (the
implementer MUST NOT capture the loop variable via closure — Go pre-
1.22 footgun). The builder is constructed once per remediation cycle
and parameterizes by its `round` argument:

```go
cfg.AttackBuilder = func(round int) AdversaryAttack {
    return AdversaryAttack{
        ArrowID:      arrow.ID,
        PassID:       passID,
        ProjectDir:   req.ProjectDir,
        DepthClauses: sensitive,
        Requirements: req.Arrow.Requirements,
        Round:        round,
    }
}
```

The clauseID synthesis at `runner/adversarial.go:239` uses
`attack.Round`; with this contract held, per-round clauseIDs are
distinct across rounds within the same pass.

#### Refusal semantics on unwired hooks (spec H1 + design H12 closure)

The dispatcher refuses a depth-sensitive arrow when any of
`AdversaryFactory`, `OpenSweep`, `Classify`, `ProducerFix` is nil. The
v1 default is **auto-enable on session start** using the active
dialect at the arrow's max tier (design-adversarial H12 closure: the
CLI cliff is unacceptable). The operator-attestation surface is the
`/adversary` slash command:

| Form | Effect |
|---|---|
| `/adversary` (bare) | Show current state — by default `enabled (open-sweep: <model>, classify: <model>, producer-fix: <model>)` for a fresh session. |
| `/adversary disable` | Tear down the bundle. Future dispatches of depth-sensitive arrows refuse with `adversarial-hooks-not-wired`. |
| `/adversary enable` | Re-construct the bundle from the active dialect. |
| `/adversary status` | Alias for the bare form. |

On `/adversary disable`, depth-sensitive dispatches abort with
`reason: adversarial-hooks-not-wired`. The refusal is observable (an
`OperatorEvent` of kind `OpEventPassClosed` carries the reason in
`Payload["close_reason"]` per the typed-payload contract below). The
operator who disables knowingly sees the refusal at dispatch time.

#### OperatorBus payload contracts (spec H2 + H3 closure)

`OperatorEvent.Payload map[string]string` (per
`runner/operatorbus.go:32`) is the typed payload. Free-text `Detail`
remains for human reading; subscribers MUST NOT parse `Detail` for
state. The contract for each adversarial / amendment event:

| Event kind | Required Payload keys |
|---|---|
| `OpEventAdversarialRoundStart` | `arrow_id`, `pass_id`, `round`, `rounds_max`, `open_findings` |
| `OpEventProducerFixSignal` | `arrow_id`, `round`, `open_findings` |
| `OpEventRemediationConverged` | `arrow_id`, `pass_id`, `outcome` (one of `converged` / `converged-with-unevaluated`), `rounds_used` |
| `OpEventRemediationEscalated` | `arrow_id`, `pass_id`, `outcome`, `rounds_used`, `reason` |
| `OpEventAmendmentEnqueued` | `arrow_id`, `amendment_id`, `source_arrow`, `target_role`, `finding_ids` (comma-joined) |
| `OpEventAmendmentEnqueueRefused` | `arrow_id`, `amendment_id`, `reason` (one of `queue-full`, `duplicate-id`) — NEW event kind, design-adversarial H7 closure |
| `OpEventAmendmentDrained` | `amendment_id`, `source_arrow`, `grid_version_before`, `grid_version_after`, `arrows_added` (comma-joined), `passes_aborted` (comma-joined), `status` (one of `complete`, `partial-append-error`, `binding-re-register-error`) |
| `OpEventRecoveryAmendmentsPending` | `count`, `amendment_ids` (comma-joined) — NEW event kind, spec M5 closure |
| `OpEventPassClosed` | `arrow_id`, `pass_id`, `close_reason`, `arrow_status` (one of `complete`, `unevaluated`, `blocked`, `aborted-remediation`) |

`OpEventAmendmentEnqueueRefused` (new kind) and
`OpEventRecoveryAmendmentsPending` (new kind) are added to
`runner/operatorbus.go`'s constant block. The new
`arrow-status:aborted-remediation` discriminator is added to
`runner/arrow.go`'s status enum (design-adversarial M15 + C7 closure)
to distinguish a remediation-escalated pass from the existing
`blocked` status; this prevents the close-reason vocabulary churn the
adversarial pass named.

#### OperatorBus subscriber invariant (spec H3 closure)

The dispatcher MUST refuse to enter the cycle if `d.Bus.Subscribers()`
returns zero on the `audit` channel — a JSONL-audit subscriber is the
floor. `runner.OperatorBus` gains a `HasAuditSubscriber() bool` helper
(or equivalent) so the dispatcher can pre-check. Today
`session_engine.go:218` wires the JSONL audit writer; the helper
codifies the invariant so a degenerate session (e.g., CI with audit
writer construction failure) refuses dispatch rather than silently
state-mutating.

#### Outputs back to the dispatcher

The cycle produces `*RemediationReport` (per
`runner/remediation.go:131 Outcome`):

| Outcome | Dispatcher response |
|---|---|
| `converged` | Proceed to verification over `robust + auto-inserts`. |
| `converged-with-unevaluated` | Proceed to verification; `no-open-finding` counts `unevaluated`-severity findings as blocking (already does — `gates.md` §7.3). Arrow status will derive to `unevaluated`. |
| `escalated-rounds-max` | Abort with `reason: remediation-rounds-max`; arrow status `aborted-remediation`. |
| `escalated-no-progress` | Abort with `reason: remediation-no-progress`. |
| `escalated-hook-error` | If `errors.Is(report.FinalErr, ErrProducerLoopBomb)`, abort with `reason: producer-loop-bomb`; else `reason: producer-fix-error`. |
| `context-cancelled` | Abort with `reason: context-cancelled`. |

Findings persist on all outcomes (per `gates.md` §7.2 + spec L2
clarification: in-memory `FindingsStore` is the runtime view; on-disk
`engine.db` is the authoritative store; both retain).

#### RemediationReport persistence (spec M3 closure)

The `RemediationReport` is persisted as a JSONL audit record. The
ADR-015 `passes` table is extended with two columns:

- `remediation_outcome TEXT NULL` — the `Outcome` value.
- `remediation_rounds_used INTEGER NULL` — `len(report.Rounds)`.

The full structured report goes to JSONL (one line per cycle, kind
`adversarial-cycle-report`, with `pass_id`, `arrow_id`, outcome,
rounds, per-round findings count, escalation reason). The JSONL writer
is the source of truth (per ADR-015); the columns are search
shortcuts.

#### FindingRecord.GridVersion (spec H4 + M4 closure)

`runner.FindingRecord` (per `runner/findings.go:53`) gains a
`GridVersion uint64` field. The runner stamps it on Raise from
`clause.GridVersion` (already plumbed). After an amendment drain
(Gap 2) the FindingsStore retains findings tagged with their original
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

#### Dispatcher recursion budget (spec M14 closure)

A clause that triggers an on-the-spot arrow creation (`runner/onthespot.go`)
can re-enter `Dispatch` on a different `(role, context)` tuple. The
adversarial-cycle path is re-entrant. To prevent unbounded recursion
(matching ADR-004's tool-depth pattern), `PassDispatcher` gains a
`MaxRecursiveDispatch int` field (default 4); the dispatcher carries
the current depth in `ctx` via a new value key
`dispatcherRecursionDepthKey`. Exceeding the cap aborts with
`reason: dispatch-recursion-exceeded`.

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
  findings with status `open` and calls `runner.PendingAmendments`.
- `AmendmentRequest.Validate()` enforces non-empty ID / Reason /
  SourceArrow / TargetRole / ≥1 FindingID; for
  `missing-cross-context-spec` the contexts list MUST name ≥ 2 contexts
  (the minimum for a cross-context gap to exist).
- Queue is bounded (`DefaultAmendmentQueueMaxLen = 1024`). Overflow:
  `ErrAmendmentQueueFull` is surfaced as a new
  `OpEventAmendmentEnqueueRefused` event (spec M9 + design H7
  closure), NOT as a new finding. The operator-facing remedy is
  drain-then-re-enqueue or raise the queue cap.
- Drained-ID dedup is durable. `seenIDs` (per `amendment.go:108`)
  survives `Drain` and is re-hydrated at session start. A second
  Enqueue with the same ID refuses with `ErrAmendmentDuplicateID`.

#### AmendmentContexts source (design H8 closure)

Per design-adversarial H8, the previously-proposed
`AmendmentContexts func(arrowID string) []string` callable had no
source of truth. The contexts come directly from
`bootstrap.Grid.BoundedContexts` (per `bootstrap/grid.go:27`). The
implementer wires this from `engineRuntime.gridFile.BoundedContexts`
(see "Construction order" in the implementation contract); no callable
is needed.

#### Global-lock contract

`runner/amendment_commit.go:97 Commit` already holds `c.mu` for the
duration. The mutex is process-local (per ADR-009 A-3 — multi-machine
not in scope). Two concurrent `/drain-amendments` invocations within
the same process serialize; the acceptance test (#5) is phrased
accordingly.

**Lock order**: `committer.mu → AmendmentQueue.mu → Grid.mu`. The
implementer MUST NOT introduce a reverse path. The race-detector test
`TestScenario_AmendmentCommit_GlobalLock_Serializes` covers
concurrent enqueue+drain.

#### Mid-drain ordering (design H5 + M6 closure)

`AmendmentCommitter.Commit` ordering, REVISED per design-adversarial
H5 closure (re-register before grid append, not after):

1. Acquire `committer.mu`.
2. Validate `req.Amendment.ID == queue.Pending()[i].ID` (FIFO check).
3. **Re-register bindings** if `BindingsReRegister` is wired. This
   step is now BEFORE the grid append so a re-register failure aborts
   the drain without bumping the version (resolves
   design-adversarial H5: bumped-grid-with-stale-bindings is
   unrecoverable).
4. Abort in-flight passes whose `ArrowID == amendment.SourceArrow`
   AND `State() == PassStateOpen`.
5. Append each `NewArrows` definition via `Grid.Append`. Each append
   bumps the in-memory grid version monotonically.
6. **Persist the new grid to disk** via `bootstrap.Grid.Write(workdir)`
   (design-adversarial C4 closure: the committer MUST write the new
   grid file so `bootstrap.Read` on re-register sees it). The write
   produces `grid.v<N+1>.yaml` and updates `grid.current`.
7. Call `Queue.MarkDrained(amendment.ID)` to emit
   `AmendmentEventDrain`.
8. Publish `OpEventAmendmentDrained` with typed Payload per the
   contract above.
9. Release `committer.mu`.

The order is the contract; the implementer MUST NOT reorder.

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

`engine.Replay` (in `engine/replay.go`) MUST call
`AmendmentQueue.LoadDrained(id)` for every amendment with a non-null
`drained_at` in the persisted amendments table BEFORE journal-attach.
This is the durable-dedup contract; without it a re-enqueue could
slip through.

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
7. `BindingsReRegister` failure during a drain aborts BEFORE the grid
   version bumps; subsequent `bootstrap.Read(workdir)` returns the
   pre-drain version.

### Gap 3: Language-binding evaluation — behavioral contract

#### Type-name correction (spec C3 closure)

The on-disk type is `bootstrap.Grid` (per `bootstrap/grid.go:22`), not
`bootstrap.GridFile`. All references in the prior spec/design that used
`GridFile` are renamed.

#### Validation surface (spec C4 closure)

Spec-adversarial C4 named the impedance mismatch: `bootstrap.Grid.Arrows`
is `[]map[string]any` (per `bootstrap/grid.go:49`); the runner's
typed `Grid.Lookup` is the canonical form. The validation surface is:

- **At session-engine open (post-Replay):** the runner's
  `runner.Grid` holds typed arrows after Replay. The coverage check
  `RequiredBindings(*runner.Grid)` walks every dispatchable arrow's
  clauses, emits the deduplicated set of `BindingKey{Concept,
  Language}`, and verifies each is present in the registered
  bindings. Missing → `*MissingBindingError` (already a
  `bootstrap` type, re-exported via runner alias).
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
  init'd). Both sources are walked. Per design-adversarial M2: a
  newly-init'd project's arrows live ONLY in `gridFile.Arrows` until
  the first traversal — the coverage check MUST consult the bootstrap
  shape too.

For the bootstrap-shape walk, a new helper
`bootstrap.RequiredBindingKeys(g *Grid) ([]BindingKey, error)` parses
`g.Arrows` per-arrow and emits the binding keys. The parser is
intentionally permissive (returns an error per malformed arrow rather
than crashing) so the coverage check fails-soft when an arrow is
malformed: it reports BOTH the missing-binding AND the malformed-arrow
classes.

#### Registry-key shape (spec C2 + design C5 closure)

`<concept>.<language>` is the registry key shape. The new helper
`runner.ConceptRegistryKey(c Clause) string`:

- For `language-bound: false` concepts: returns `c.Concept` verbatim.
- For `language-bound: true` concepts: returns
  `c.Concept + "." + c.Args["language"].(string)`.

The "is this concept language-bound?" predicate is **auto-derived at
compile time** from `gates/concepts/*.yaml` (design-adversarial C5 +
H9 + M14 closure). Mechanism:

- `gates/concepts/` already ships with go-embed via `gates/assets.go`
  (`//go:embed concepts/*.yaml` — implementer verifies the existing
  embed surface).
- A new `runner/concept_classification.go` parses every embedded YAML
  at package init; emits two unexported maps:
  - `languageBoundConcepts map[string]struct{}` — populated when the
    YAML's `language-bound: true`.
  - `universalConcepts map[string]struct{}` — populated when the
    YAML's `language-bound: false`.
- Exported helpers `IsUniversalConcept(concept string) bool` and
  `IsLanguageBoundConcept(concept string) bool` consult these maps.
- A package init `func init() { ... }` asserts the maps' total cardinality
  equals 18 (panic-at-startup if a YAML is missing or malformed).
  Closed-vocabulary discipline per ADR-005 is enforced at compile-init.

This eliminates the hand-list drift named in design-adversarial C5,
H9, and the spec-adversarial H9.

#### Per-language evaluator construction (spec M12 closure)

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
(`runner/subprocess.go:defaultEnvAllowlist` — TBC) is extended with:

| Language | Inherited vars |
|---|---|
| go | `GOPATH`, `GOCACHE`, `GOMODCACHE`, `GOROOT`, `HOME` |
| rust | `CARGO_HOME`, `RUSTUP_HOME`, `HOME` |
| typescript / node | `NODE_PATH`, `npm_config_*`, `HOME` |
| python | `PYTHONPATH`, `VIRTUAL_ENV`, `HOME` |

`ghyll init` auto-propose (per ADR-011) walks the project for
language indicators and emits a starter set; the operator may amend
at init time. Schema extension (b) is deferred to v2 when concrete
per-binding overrides become necessary.

#### Re-registration on amendment (spec H8 + design C3 + C4 closure)

`AmendmentCommitter.Commit` (gap 2) calls `BindingsReRegister()` per
the revised ordering (step 3, BEFORE grid append). The callback is
wired by `engineRuntime`:

```go
rt.committer.BindingsReRegister = func() error {
    // Re-read the persisted grid file. AmendmentCommitter.Commit
    // writes the new grid in step 6, but BindingsReRegister fires
    // in step 3 — so the source of truth is the IN-MEMORY runner.Grid
    // plus the pending amendment's NewArrows + LanguageBindings overlay.
    newGrid := rt.gridFile.WithOverlay(rt.pendingAmendmentOverlay())
    return runner.RegisterGridBindings(rt.registry, newGrid, rt.workdir)
}
```

**Key shift from prior design (design-adversarial C4 closure):** the
prior wire read on-disk via `bootstrap.Read(workdir)` AFTER append,
which is mechanically unreachable because the committer never wrote
to disk. The corrected ordering writes to disk in step 6 (committed
above); but step 3 (re-register) needs the new bindings BEFORE the
disk write. The implementer constructs an in-memory `*bootstrap.Grid`
overlay from `rt.gridFile + amendment.NewArrows +
amendment.NewLanguageBindings` and re-registers from that. If
re-register fails, neither the disk file nor the in-memory grid has
been mutated — failure-clean.

This is ADR-worthy: see ADR-v4-003 ("Amendment-driven re-register
ordering — in-memory overlay, fail before bump") in the ADRs section.

#### Amendment NewLanguageBindings carrier

`runner.AmendmentRequest` (in `runner/amendment.go`) carries
`NewArrows []ArrowDefinition` today. To carry binding updates, it
gains `NewLanguageBindings map[string]string` (additive). The
operator's `.ghyll/amendments/<amendment-id>/grid-overlay.yaml`
declares both `arrows:` and `language-bindings:`; the
`/drain-amendments` loader reads both blocks into `AmendmentRequest`.

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
   `BindingsReRegister` returns the error; the drain aborts BEFORE
   any version bump.

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
   incremented.
5. A binding subprocess running over `DefaultBindingTimeout` is
   killed via SIGTERM → grace → SIGKILL; the persisted EvaluationRun
   carries `ReasonTimeout`.
6. Binding output is redacted by the harness's secret-redaction
   filter (per `specs/architecture/components/concepts.md`).
7. The `language-bound` predicate (`IsLanguageBoundConcept(concept)`)
   agrees with `gates/concepts/<concept>.yaml`'s `language-bound`
   field for every concept; mismatch panics at package init.

### Gap 4: 4 missing universal concepts — behavioral contract

`unique-definition`, `predicate-form`, `mode-determinable-from-repo`,
`single-active-role-instance` ship in `RegisterBuiltins` as in-process
Go evaluators. Spec-adversarial H10 closure: option (a) — implement;
the spec previously deferred this to architect-picks with no v1
default, which would have left arrows crashing.

#### Evaluator contracts

Each evaluator follows the
`Evaluator = func(ctx context.Context, c Clause) (*Result, error)`
signature (no new variant). The clause's `Args` map carries the
per-evaluator inputs; the args schemas are the canonical YAMLs at
`gates/concepts/<concept>.yaml` (design-adversarial L4 + M12 + M17
closure: implementer MUST quote the YAML literally rather than
re-inventing arg names).

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

#### Single-active-role-instance access to PassRegistry (design C6 + H3 closure)

`EvaluateSingleActiveRoleInstance` needs `*PassRegistry`. The prior
design proposed two options; both had defects (the "stamp on Args" was
misdiagnosed per design-adversarial C6; the "EvaluatorWithRunner
signature variant" is additive and clean — chosen here).

**Decision: `EvaluatorWithRunner` signature variant**, additive to the
existing `Evaluator` type:

```go
// In runner/runner.go, alongside `type Evaluator`:
type EvaluatorWithRunner func(ctx context.Context, r *Runner, c Clause) (*Result, error)
```

`Registry` gains `RegisterWithRunner(concept string, e
EvaluatorWithRunner) error` and the lookup path resolves the runner-
typed variant by wrapping it in an adapter when the runner calls
`Evaluate`. Existing 7 + 3 of the 4 new evaluators stay on the simpler
`Evaluator` signature; only `EvaluateSingleActiveRoleInstance` uses
the new variant.

**Filter-self contract (design H3 closure):** when the clause's
containing pass is itself in the registry (it always is — the
dispatcher registers BEFORE evaluation), the evaluator MUST filter by
`passID != clause.PassID`. The contract is unit-tested via
`TestScenario_SingleActiveRoleInstance_FiltersSelf`.

#### mode-determinable-from-repo path safety (design H6 closure)

`Args["mode-discriminator-path"]` is operator-supplied (per
`.ghyll/mode.yaml` default, or grid-clause override). The evaluator
MUST use the same path-safety primitive as `EvaluateNoTodoMarker`
(per `runner/notodo.go:376 openNoFollow`): O_NOFOLLOW + clamp-to-
`ProjectDir`. A path that escapes (`..`, absolute outside workdir, or
a symlink) is rejected with a typed error before the file is opened.

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

### Cross-cutting

#### `/run-arrow` operator escape on stuck arrows (spec M2 closure)

A new operator-attested verb `/invalidate-arrow <id>` marks an arrow
`invalidated` (per `gates.md` §7.2) and forces re-traversal on the
next `/run-arrow`. Without this, a stuck `escalated-rounds-max` cycle
can block all forward progress on a `(role, context)` tuple. The
verb requires `op-id` set (attestation surface).

#### Dispatcher counter discipline (design H10 closure)

`PassIDGen` is invoked AFTER the hooks-wired check and AFTER the
recursion-depth check, so a refused dispatch does not spin the
counter. The hook-check and recursion-check fire before
`OpenPass` opens the pass; refused dispatches do not produce
`passes` table rows.

#### Race-clean hook swap (design H2 closure)

`PassDispatcher`'s hook fields use `atomic.Pointer[AdversarialHooks]`
(grouping per design-adversarial M3 closure): one atomic pointer to a
`*AdversarialHooks` bundle, not 4 separate fields. The `/adversary
enable/disable` handler swaps the pointer atomically. A concurrent
in-flight dispatch already-loaded its hooks (a local snapshot at the
top of `runDispatcherAdversarialPhase`); the swap takes effect on the
NEXT dispatch.

#### Event-fanout deduplication (design M5 closure)

The dispatcher's `enqueueAmendmentsForIntegratorPass` does NOT
publish `OpEventAmendmentEnqueued` itself — the queue's observer
(wired via `engine/journal.go:421 AttachAmendments`) already fires
the event through the journal-to-bus bridge. The dispatcher only
calls `d.AmendmentQueue.Enqueue(req)`; the observer fans out. The
implementer MUST NOT duplicate the publish.

#### Cost telemetry on adversarial rounds (design M9 closure)

`OpEventAdversarialRoundStart` carries an extra Payload key
`tier_label` (the human-readable tier name, e.g., `REALISTIC`).
Modal / status CLI render running tier-spend totals.

#### `runner.Runner.passes` field (design M16 closure)

`runner.Runner` gains an unexported `passes *PassRegistry` field.
Construction at `cmd/ghyll/session_engine.go:486 NewRunner` plumbs
`rt.passes`. The four other test sites (the implementer greps
`runner.NewRunner(` to enumerate; estimated: `runner/runner_test.go`,
`runner/dispatcher_test.go`, `runner/adversarial_test.go`, and
`tests/acceptance/steps_runner.go`) thread a test-constructed
`*PassRegistry` — the new `Runner` constructor signature
`NewRunner(tier DepthRank, passes *PassRegistry) *Runner`.

---

## Implementation contract (was: architect design)

### Dependency order (revised)

The land order is unchanged from the prior design but with the spec's
correction propagated:

1. **Gap 4 first** (was: alongside Gap 3) — the four new evaluators
   AND the `concept_classification.go` compile-time YAML parse. This
   provides the `IsUniversalConcept` / `IsLanguageBoundConcept`
   predicate that Gap 3 depends on (the registry-key shape).
2. **Gap 3** — language-binding registration + amendment-driven
   re-register seam. Depends on Gap 4 for the predicate.
3. **Gap 2** — amendment drain. Depends on Gap 3 for
   `BindingsReRegister`.
4. **Gap 1** — adversarial cycle. Depends on Gap 3 for the
   `BindingEvaluator` registry coverage at depth-sensitive clause
   resolution; depends on Gap 2 for the `aborted-remediation`
   close-reason vocabulary.

Each gap lands as a single PR, internally split into commits each
passing `make test-unit`.

### Auto-derived universal table

New file `runner/concept_classification.go`:

```go
package runner

import (
    "embed"
    "fmt"
    "path"
    "strings"

    "gopkg.in/yaml.v3"

    "github.com/witlox/ghyll/gates" // exposes ConceptsFS via assets.go
)

var (
    languageBoundConcepts = map[string]struct{}{}
    universalConcepts     = map[string]struct{}{}
)

type conceptYAML struct {
    Concept       string `yaml:"concept"`
    LanguageBound bool   `yaml:"language-bound"`
}

func init() {
    entries, err := gates.ConceptsFS.ReadDir("concepts")
    if err != nil {
        panic(fmt.Sprintf("concept_classification: read concepts dir: %v", err))
    }
    for _, e := range entries {
        if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
            continue
        }
        if e.Name() == "README.md" { continue }
        data, err := gates.ConceptsFS.ReadFile(path.Join("concepts", e.Name()))
        if err != nil {
            panic(fmt.Sprintf("concept_classification: read %s: %v", e.Name(), err))
        }
        var cy conceptYAML
        if err := yaml.Unmarshal(data, &cy); err != nil {
            panic(fmt.Sprintf("concept_classification: parse %s: %v", e.Name(), err))
        }
        if cy.LanguageBound {
            languageBoundConcepts[cy.Concept] = struct{}{}
        } else {
            universalConcepts[cy.Concept] = struct{}{}
        }
    }
    if len(languageBoundConcepts)+len(universalConcepts) != 18 {
        panic(fmt.Sprintf("concept_classification: expected 18 concepts, got %d universal + %d language-bound",
            len(universalConcepts), len(languageBoundConcepts)))
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

If `gates/assets.go` doesn't already export `ConceptsFS`, the
implementer adds it as an additive change:

```go
// gates/assets.go
//go:embed concepts/*.yaml
var ConceptsFS embed.FS
```

### `engineRuntime.gridFile` (design C3 closure)

`cmd/ghyll/session_engine.go:48-105 engineRuntime` gains:

```go
gridFile *bootstrap.Grid
```

Populated in `initEngine` (per `cmd/ghyll/session.go:initEngine ~ line
318` — the existing `bootstrap.Read` call) BEFORE
`openEngineWithOptions`. `openEngineWithOptions`'s signature changes:

```go
func openEngineWithOptions(workdir string, logger *slog.Logger, ibMax int, grid *bootstrap.Grid) (*engineRuntime, error)
```

The `*bootstrap.Grid` is plumbed into the runtime and held for the
session's lifetime. The amendment-driven re-register (Gap 3) mutates
`rt.gridFile` IN PLACE atomically: a new helper
`(*engineRuntime).overlayGridFile(amendment *AmendmentRequest)`
constructs the new `bootstrap.Grid` and assigns it under an internal
mutex. Concurrent readers of `rt.gridFile` (e.g.,
`enqueueAmendmentsForIntegratorPass` reading
`rt.gridFile.BoundedContexts`) take an `RLock`.

### Gap 3 wiring (closes design C4)

#### Files touched

| File | Function / line | Change |
|---|---|---|
| `runner/runner.go` | new — top of file | Add `ConceptRegistryKey(c Clause) string` per the contract above. |
| `runner/runner.go` | `Evaluate` (~line 476) | Replace `r.Registry.Lookup(c.Concept)` with `r.Registry.Lookup(ConceptRegistryKey(c))`. |
| `runner/runner.go` | new — `NewRunner` (line 486 in session_engine.go uses this) | Signature now `NewRunner(tier DepthRank, passes *PassRegistry) *Runner`. Store `passes` on an unexported field. |
| `runner/concept_classification.go` | new file | Compile-time YAML parse per the snippet above. |
| `runner/bindings_register.go` | new file | `RegisterGridBindings(reg *Registry, grid *bootstrap.Grid, workdir string) error` — iterates `grid.LanguageBindings`, validates each key via `IsLanguageBoundConcept`, calls `NewBindingEvaluator(command, WithWorkingDir(workdir), ...)`, calls `reg.Register(key, evaluator)`. Returns the first error encountered. |
| `runner/bindings_register.go` | (same file) | `RequiredBindings(rg *runner.Grid) []bootstrap.BindingKey` walks every dispatchable arrow's clauses, emits the deduplicated set. |
| `runner/bindings_register.go` | (same file) | `RequiredBindingsFromBootstrap(g *bootstrap.Grid) ([]bootstrap.BindingKey, error)` walks `g.Arrows` (untyped) for the freshly-init'd-no-traversal case (per design-adversarial M2). Returns the keys + any per-arrow parse errors. |
| `bootstrap/grid.go` | new helper | `func (g *Grid) Write(dir string) error` already exists at line 168. Verify it produces `grid.v<N+1>.yaml` and updates `grid.current` atomically (it does — confirmed by code read). |
| `bootstrap/grid.go` | new helper | `func (g *Grid) Overlay(amendment runner.AmendmentRequest) *Grid` returns a deep-copy `*Grid` with `amendment.NewArrows` appended to `Arrows` (round-trip through `[]map[string]any` — the implementer wraps `ArrowDefinition.MarshalYAML` or a manual map build) and `amendment.NewLanguageBindings` merged into `LanguageBindings`. Used by `BindingsReRegister`. |
| `cmd/ghyll/session_engine.go` | `openEngineWithOptions` (line 132) | Signature change adds `grid *bootstrap.Grid`. After `runner.RegisterBuiltins(reg)` (line 146), call `runner.RegisterGridBindings(reg, grid, workdir)`. Then call `runner.RequiredBindingsFromBootstrap(grid)` and verify each key resolves through `reg.Lookup` — emit `*MissingBindingError` on miss. On any error, close the store and return. |
| `cmd/ghyll/session_engine.go` | `replayEngine` (line 276) | After Replay, call new helper `verifyBindingsCoverage(rt)` which walks `rt.grid` (typed, post-Replay) AND repeats the bootstrap-shape walk for arrows not yet in the typed grid. |
| `cmd/ghyll/session.go` | `initEngine` (~line 301) | Pass `*bootstrap.Grid` into `openEngineWithOptions`. If `openEngineWithOptions` errors with a binding error, surface a stronger operator-facing message: `✗ session refuses: <reason>; run ghyll init`. |
| `runner/amendment_commit.go` | `AmendmentCommitter` struct (line 32) | Add `BindingsReRegister func() error`. Optional; nil = skip the step. |
| `runner/amendment_commit.go` | `Commit` (line 97) | Reorder per "Mid-drain ordering" above: validate → re-register → abort passes → grid append → disk write → MarkDrained → publish. |
| `cmd/ghyll/session_engine.go` | new method `(*engineRuntime).reRegisterBindings() error` | Builds the overlay grid in memory; calls `runner.RegisterGridBindings(rt.registry, overlay, rt.workdir)`. On success, swaps `rt.gridFile` to the overlay. |
| `cmd/ghyll/session_engine.go` | wire | `rt.committer.BindingsReRegister = rt.reRegisterBindings` at runtime construction. |

#### Construction order at session open (REVISED)

```text
session.go:initEngine
  1. grid := bootstrap.Read(s.workdir)
     → error refuses session with `✗ session refuses: grid load: <err>`
  2. engine := openEngineWithOptions(s.workdir, log, ibMax, grid)
     2a. store, runner.NewRegistry, runner.RegisterBuiltins(reg)  (existing)
     2b. runner.RegisterGridBindings(reg, grid, workdir)          (NEW)
         2b-i. for each (k, v) in grid.LanguageBindings:
                 - split k on last '.' → (concept, language)
                 - require IsLanguageBoundConcept(concept); else ErrLanguageBindingInvalid
                 - require non-empty command; else ErrBindingCommandEmpty
                 - evaluator := runner.NewBindingEvaluator(v,
                                  WithWorkingDir(workdir),
                                  WithTimeout(runner.DefaultBindingTimeout),
                                  WithMaxOutputBytes(runner.DefaultBindingMaxOutputBytes),
                                  WithGrace(runner.DefaultBindingGrace))
                 - reg.Register(k, evaluator)
     2c. keys, err := runner.RequiredBindingsFromBootstrap(grid)   (NEW)
         - verify each (concept, language) → reg.Lookup("<concept>.<language>") returns OK
         - if any miss → *MissingBindingError + close store + return
     2d. replayEngine(ctx)                                          (existing)
     2e. verifyBindingsCoverage(rt)                                 (NEW)
         - walks rt.grid (typed runner.Grid) for any clauses Replay populated
         - verifies each clause's ConceptRegistryKey → reg.Lookup OK
     2f. attachJournal(log)                                          (existing)
     2g. wire rt.committer.BindingsReRegister                        (NEW)
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
| `runner/runner.go` | `Registry` | `RegisterWithRunner(concept string, e EvaluatorWithRunner) error`; lookup path adapts the runner-typed variant. |
| `runner/notodo.go` | `RegisterBuiltins` (line 402) | Add `registerOrReplace(r, "unique-definition", EvaluateUniqueDefinition)`, `"predicate-form"`, `"mode-determinable-from-repo"`. Add `r.RegisterWithRunner("single-active-role-instance", EvaluateSingleActiveRoleInstance)` with a similar `panic-on-error` fallback. Alphabetize. |

#### Tests (Gap 4)

| Test name | Package | Asserts |
|---|---|---|
| `TestScenario_UniqueDefinition_NoDuplicates` | `runner` | Fixture with unique values → Pass=true. |
| `TestScenario_UniqueDefinition_DuplicatesDetected` | `runner` | One duplicate → Pass=false; `Details["duplicates"]` lists value + locations. |
| `TestScenario_UniqueDefinition_CaseInsensitive` | `runner` | `case-sensitive: false` + "FOO"/"foo" → Pass=false. |
| `TestScenario_UniqueDefinition_NoLocator_Unevaluated` | `runner` | Locator empty selection → `Unevaluated`. |
| `TestScenario_UniqueDefinition_ArgsMatchYAML` | `runner` | Args names read by evaluator exactly match `gates/concepts/unique-definition.yaml`'s args block. |
| `TestScenario_PredicateForm_AssertablePredicates` | `runner` | Comparison-op entries → Pass=true. |
| `TestScenario_PredicateForm_ProseEntry` | `runner` | One prose entry → Pass=false; listed in `Details["non-predicates"]`. |
| `TestScenario_PredicateForm_ArgsMatchYAML` | `runner` | Args match YAML. |
| `TestScenario_ModeDeterminableFromRepo_ValidEnum` | `runner` | `.ghyll/mode.yaml: greenfield` ∈ enum → Pass=true. |
| `TestScenario_ModeDeterminableFromRepo_MissingFile` | `runner` | Missing → Pass=false; reason mentions path. |
| `TestScenario_ModeDeterminableFromRepo_PathEscape_Refused` | `runner` | `mode-discriminator-path` = `../../etc/passwd` → typed refusal, no file opened. |
| `TestScenario_ModeDeterminableFromRepo_ArgsMatchYAML` | `runner` | Args match YAML. |
| `TestScenario_SingleActiveRoleInstance_NoConflict` | `runner` | 0 open passes on tuple → Pass=true. |
| `TestScenario_SingleActiveRoleInstance_ConflictDetected` | `runner` | 2 open passes (excluding self) on same tuple → Pass=false; `Details["conflicting-pass-ids"]` lists both. |
| `TestScenario_SingleActiveRoleInstance_FiltersSelf` | `runner` | Self is in registry; evaluator filters by `passID != c.PassID`; reports pass=true with count 0. |
| `TestScenario_SingleActiveRoleInstance_ArgsMatchYAML` | `runner` | Args match YAML. |
| `TestScenario_RegisterBuiltins_RegistersAll11Universals` | `runner` | After `RegisterBuiltins`, the 11 universals all resolve. |
| `TestScenario_ConceptClassification_18Total` | `runner` | `len(universalConcepts) + len(languageBoundConcepts) == 18` (init-time invariant). |
| `TestScenario_ConceptClassification_AgreesWithYAML` | `runner` | For every concept name parsed from `gates/concepts/`, `IsLanguageBoundConcept(c) == (YAML.language-bound == true)`. |

#### BDD scenarios (Gap 4)

New feature file `specs/features/universal-base.feature`:

```gherkin
Feature: Universal-base evaluators run in production

  Scenario: unique-definition catches duplicates
    Given a project with arrow A1 declaring a unique-definition clause
    And the scope contains two entries sharing the same field value
    When the operator runs "/run-arrow A1"
    Then the clause status is "fail"
    And the result details list the duplicate values and locations

  Scenario: predicate-form rejects prose entries
    Given a project with arrow A2 declaring a predicate-form clause
    And one entry is a prose sentence with no comparison operator
    When the operator runs "/run-arrow A2"
    Then the clause status is "fail"
    And the result details list the non-predicate entry

  Scenario: mode-determinable-from-repo asserts enum match
    Given a project with .ghyll/mode.yaml declaring "greenfield"
    And arrow A3 declares a mode-determinable-from-repo clause with enum [greenfield, brownfield]
    When the operator runs "/run-arrow A3"
    Then the clause status is "pass"

  Scenario: single-active-role-instance excludes self
    Given a project with arrow A4 declaring a single-active-role-instance clause
    And no other open passes match the (role, context) tuple
    When the operator runs "/run-arrow A4"
    Then the clause status is "pass"
    And the open-pass count in the result details is 0

  Scenario: mode-determinable-from-repo refuses path escape
    Given arrow A5 declares mode-discriminator-path "../../etc/passwd"
    When the operator runs "/run-arrow A5"
    Then the clause status is "fail"
    And no file is opened outside the project directory
```

### Gap 1 wiring (corrected against the new predicate)

#### Files touched

| File | Function / line | Change |
|---|---|---|
| `runner/dispatcher.go` | `PassDispatcher` struct (line 61) | Add `Hooks *atomic.Pointer[AdversarialHooks]` (single field, grouped per design-M3). |
| `runner/dispatcher.go` | new type `AdversarialHooks` | `struct { Factory func(round int) *Adversary; OpenSweep OpenSweepFn; Classify DepthClassifyFn; ProducerFix ProducerFn; RemediationConfigDefaults RemediationConfig }`. |
| `runner/dispatcher.go` | `PassDispatcher` struct | Add `FindingsStore *FindingsStore`, `ClassificationsStore *ClassificationsStore`, `AmendmentQueue *AmendmentQueue`, `MaxRecursiveDispatch int` (default 4). |
| `runner/dispatcher.go` | `Dispatch` (line 184) | Reorder: hooks-check + recursion-check BEFORE `OpenPass`. Then `OpenPass` → `runDispatcherAdversarialPhase` → verification over `robust + auto-inserts` only (NOT all clauses). |
| `runner/dispatcher.go` | new helper `runDispatcherAdversarialPhase(ctx, req, pass, passID) (*RemediationReport, []Clause, error)` | Returns `(nil, allClauses, nil)` for depth-robust-only / init / adversary roles (verification runs over all clauses, no auto-insert split). Returns `(report, robust+autoinserts, nil)` for sensitive cycles (after running the cycle to convergence). Returns `(_, _, err)` on hooks-unwired refusal. |
| `runner/dispatcher.go` | new error sentinel | `ErrAdversaryHooksNotWired`. |
| `runner/dispatcher.go` | `Dispatch` | After `runDispatcherAdversarialPhase` returns a non-converged outcome, set arrow status to `ArrowStatusAbortedRemediation` (new enum value), abort the pass with the analyst-spec reason mapping, early-return. |
| `runner/dispatcher.go` | new helper `requireAuditSubscriber(d *PassDispatcher) error` | Refuses dispatch if `d.Bus.HasAuditSubscriber()` returns false. Called at the top of `Dispatch`. |
| `runner/operatorbus.go` | new method `HasAuditSubscriber() bool` | Returns true iff at least one subscriber tagged `audit` is present. Subscriber-tag is a new identifier added to the `OperatorEventSubscriber` registration shape (additive). |
| `runner/arrow.go` | `ArrowStatus` enum | Add `ArrowStatusAbortedRemediation = "aborted-remediation"`. |
| `runner/operatorbus.go` | new event kinds | `OpEventAmendmentEnqueueRefused`, `OpEventRecoveryAmendmentsPending`. |
| `runner/producer_fix.go` | `ProducerFixHarness` | Remove the internal `round` counter; change `runOneRound` → `RunOneRound(ctx, openFindings, round int) error`. Maintain `digestsByRound map[int][32]byte`. Loop-bomb check is `digestsByRound[round] == digestsByRound[round-1]`. |
| `runner/adversarial.go` | line 239 | ClauseID synthesis: `<passID>/adv/<concept>/round<N>` (add the `/adv/` namespace segment). |
| `runner/runner.go` | (per Gap 4) | New `EvaluatorWithRunner` signature; `NewRunner(tier, passes)` two-arg form. |
| `cmd/ghyll/session_engine.go` | `dispatcher()` (line 507) | Wire `Hooks` from `rt.adversarialHooks` (an `*atomic.Pointer[AdversarialHooks]`). Wire `FindingsStore`, `ClassificationsStore`, `AmendmentQueue`. |
| `cmd/ghyll/session_engine.go` | new field `adversarialHooks atomic.Pointer[runner.AdversarialHooks]` | At session start, atomically store a hook bundle constructed from the active dialect (auto-enable per spec H1 closure). |
| `cmd/ghyll/session_engine.go` | new methods `(*engineRuntime).adversaryFactory`, `.openSweepHook`, `.classifyHook`, `.producerFixHook`, `.remediationDefaults()` | Concrete dialect-backed implementations. The factory constructs a fresh `*runner.Adversary` per round. |
| `cmd/ghyll/adversary_cmd.go` (new file) | `handleAdversaryCommand(arg string) SlashCommandResult` | Toggles `rt.adversarialHooks` via atomic store. Reports status. |
| `cmd/ghyll/session.go` | `DispatchSlashCommand` (line 1273) | Wire `/adversary` handler. |
| `cmd/ghyll/run_arrow_cmd.go` (line 184) | event subscription | Add `OpEventAdversarialRoundStart`, `OpEventProducerFixSignal`, `OpEventRemediationConverged`, `OpEventRemediationEscalated`, `OpEventModalBackpressure` to the subscriber switch. |
| `runner/findings.go` | `FindingRecord` (line 53) | Add `GridVersion uint64` field. Raise/Transition paths stamp it from `clause.GridVersion`. |
| `cmd/ghyll/invalidate_arrow_cmd.go` (new) | `/invalidate-arrow <id>` handler | Marks an arrow invalidated; emits the existing `OpEventPassClosed`-equivalent or a new audit record. |

#### Dispatcher integration pseudocode

```go
PassDispatcher.Dispatch(ctx, req):
  if err := requireAuditSubscriber(d); err != nil {
    return nil, err
  }
  if depth, ok := ctx.Value(dispatcherRecursionDepthKey).(int); ok && depth >= d.MaxRecursiveDispatch {
    return nil, fmt.Errorf("%w: depth=%d", ErrDispatchRecursionExceeded, depth)
  }

  // Hooks pre-check BEFORE OpenPass (counter discipline)
  sensitive, robust := partitionClauses(req.Arrow.Clauses)
  hooks := d.Hooks.Load()
  if len(sensitive) > 0 && (req.Role != "init" && req.Role != "adversary") {
    if hooks == nil || hooks.Factory == nil || hooks.OpenSweep == nil || hooks.Classify == nil || hooks.ProducerFix == nil {
      return nil, ErrAdversaryHooksNotWired
    }
  }

  passID := d.PassIDGen()
  pass := OpenPass(...)
  d.Passes.Register(pass)
  defer d.Passes.Unregister(pass.ID())

  // Adversarial cycle (depth-sensitive arrows only)
  var report *RemediationReport
  var verifyClauses []Clause
  if len(sensitive) == 0 || req.Role == "init" || req.Role == "adversary" {
    verifyClauses = VerificationAutoInsert(req.Arrow.ID, req.Arrow.Clauses)
  } else {
    report, err = runAdversarialCycle(ctx, hooks, req, passID, sensitive)
    if err != nil { ... }
    if !remediationConverged(report.Outcome) {
      pass.Abort(reasonFromOutcome(report))
      return &DispatchResult{
        PassID:      passID,
        ArrowStatus: ArrowStatusAbortedRemediation,
        CloseReason: pass.CloseReason(),
        ClosedAt:    pass.ClosedAt(),
        RemediationReport: report,
      }, nil
    }
    verifyClauses = VerificationAutoInsert(req.Arrow.ID, robust)
  }

  // Verification (existing path, over verifyClauses)
  ctx = context.WithValue(ctx, dispatcherRecursionDepthKey, currentDepth(ctx)+1)
  for i, clause := range verifyClauses { ... }

  // Integrator-pass-close enqueue (Gap 2 trigger A)
  if req.Role == "integrator" && pass.State() == PassStateClosed && d.AmendmentQueue != nil {
    enqueueAmendmentsForIntegratorPass(d, req)
  }
```

`runAdversarialCycle` constructs the `RemediationConfig`:

```go
func runAdversarialCycle(ctx, hooks, req, passID, sensitive) (*RemediationReport, error) {
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
        round := cfg.RoundFromContext(ctx) // new helper; reads ctx-injected round
        err := harness.RunOneRound(ctx, openFindings, round)
        if errors.Is(err, runner.ErrProducerLoopBomb) {
            return false, err
        }
        return err == nil, err
    }
    return runner.RunRemediationLoop(ctx, cfg)
}
```

`RunRemediationLoop` injects the current round into `ctx` via
`cfg.RoundFromContext` (a new helper that reads
`ctx.Value(remediationRoundKey)`). The loop is modified at
`runner/remediation.go` to set this value before each `FixAttempt`
call. This is an additive change; the helper has a default of `0` if
the context value is absent (test paths).

#### Tests (Gap 1)

| Test name | Package | Asserts |
|---|---|---|
| `TestScenario_Dispatcher_DepthSensitive_RunsAdversarialPhase` | `runner` | Arrow with `DepthType=sensitive` clause; mock factory + hooks → `Adversary.Attack` invoked once; `RemediationReport.Outcome == converged`. |
| `TestScenario_Dispatcher_DepthRobustOnly_SkipsCycle` | `runner` | Arrow with only depth-robust clauses → zero Adversary constructions; verification runs over all clauses. |
| `TestScenario_Dispatcher_AdversaryHooksUnwired_RefusesPass` | `runner` | Depth-sensitive arrow + nil hooks → `ErrAdversaryHooksNotWired`; counter NOT spun (no pass row). |
| `TestScenario_Dispatcher_LoopBomb_AbortsWithProducerLoopBomb` | `runner` | Producer returns identical artifact rounds 1&2 → loop-bomb fires on round 2; `Outcome == escalated-hook-error`; `pass.CloseReason ~= "producer-loop-bomb"`. |
| `TestScenario_Dispatcher_LoopBomb_LegitFixIn3Rounds` | `runner` | Producer's response varies each round; loop-bomb does NOT fire even if rounds 1 and 3 happen to digest-collide (round-indexed map). |
| `TestScenario_Dispatcher_RoundsMax_AbortsWithRemediationRoundsMax` | `runner` | Findings never converge → `reason: remediation-rounds-max`; arrow status `aborted-remediation`. |
| `TestScenario_Dispatcher_AdversaryConverged_VerificationOverRobustOnly` | `runner` | Cycle converges; verification clause set is `robust + auto-inserts`; sensitive clauses NOT re-evaluated (no duplicate EvaluationRun per concept). |
| `TestScenario_Dispatcher_InitRole_SkipsCycle` | `runner` | `req.Role == "init"` with sensitive clauses → no Adversary constructed. |
| `TestScenario_Dispatcher_AdversaryRole_SkipsCycle` | `runner` | `req.Role == "adversary"` → no Adversary constructed. |
| `TestScenario_Dispatcher_AdversaryFindingsPersist_WithGridVersion` | `runner` | Cycle raises findings; FindingsStore.ForArrow includes them with `GridVersion` stamped. |
| `TestScenario_Dispatcher_NoAuditSubscriber_Refuses` | `runner` | `Bus.HasAuditSubscriber() == false` → dispatch refuses with typed error. |
| `TestScenario_Dispatcher_RecursionDepthExceeded` | `runner` | 5-deep recursive dispatch (default cap 4) → `ErrDispatchRecursionExceeded`. |
| `TestScenario_Dispatcher_AdversaryRound_ClauseIDsNamespaced` | `runner` | EvaluationRun records from adversarial phase use `<passID>/adv/<concept>/round<N>`; verification uses `<passID>/verify/<concept>`. |
| `TestScenario_Dispatcher_AttackBuilder_RoundContract` | `runner` | `cfg.AttackBuilder(round).Round == round` for round ∈ {0..N}. |
| `TestScenario_AdversaryCommand_DisableTearsDown` | `cmd/ghyll` | `/adversary disable` → atomic.Pointer load returns nil; next dispatch refuses. |
| `TestScenario_AdversaryCommand_EnableRebuilds` | `cmd/ghyll` | `/adversary enable` after disable → bundle re-constructed. |
| `TestScenario_AdversaryCommand_StatusReports` | `cmd/ghyll` | Fresh session → status reports `enabled (open-sweep: <model>, ...)`. |
| `TestScenario_AdversaryCommand_HookSwapRaceClean` | `cmd/ghyll` | Concurrent in-flight Dispatch + `/adversary disable` → no data race; in-flight cycle completes with the snapshot it loaded. |
| `TestScenario_OperatorBus_PayloadContract_AdversarialRoundStart` | `runner` | The event's Payload map contains `arrow_id`, `pass_id`, `round`, `rounds_max`, `open_findings`, `tier_label`. |
| `TestScenario_OperatorBus_PayloadContract_RemediationEscalated` | `runner` | Payload contains `arrow_id`, `pass_id`, `outcome`, `rounds_used`, `reason`. |

#### BDD scenarios (Gap 1)

`specs/features/adversarial-cycle.feature` (new):

```gherkin
Feature: Adversarial cycle runs in production

  Scenario: Depth-sensitive arrow runs adversarial then verification
    Given a project with arrow A1 declaring at least one depth-sensitive clause
    And the adversarial cycle is enabled
    When the operator runs "/run-arrow A1"
    Then the dispatcher fires Adversary.Attack at least once
    And the verification phase runs only the depth-robust clauses plus the auto-inserts
    And no depth-sensitive clause produces a duplicate EvaluationRun

  Scenario: Depth-robust arrow skips adversarial cycle
    Given a project with arrow A2 declaring only depth-robust clauses
    When the operator runs "/run-arrow A2"
    Then no Adversary is constructed
    And no RemediationReport is published

  Scenario: Adversarial cycle disabled refuses depth-sensitive arrow
    Given a session with the adversarial cycle disabled
    And arrow A1 declares at least one depth-sensitive clause
    When the operator runs "/run-arrow A1"
    Then the dispatch is refused with reason matching "adversarial-hooks-not-wired"
    And no pass row is created in the engine

  Scenario: Producer loop-bomb aborts cycle
    Given an arrow whose producer returns byte-identical response across rounds
    And the adversarial cycle is enabled
    When /run-arrow runs
    Then the pass aborts with reason matching "producer-loop-bomb"
    And the FindingsStore retains the findings raised during the cycle
    And the arrow status is "aborted-remediation"

  Scenario: Legitimate fix in three rounds does not trigger loop-bomb
    Given a producer whose round-1 and round-3 response digests happen to collide
    But each consecutive pair of rounds differ
    And the adversarial cycle is enabled
    When /run-arrow runs
    Then the cycle either converges or escalates on rounds-max
    But not with reason "producer-loop-bomb"
```

### Gap 2 wiring (independent — salvaged largely intact)

#### Files touched

| File | Function / line | Change |
|---|---|---|
| `runner/amendment.go` | `AmendmentRequest` | Add `NewLanguageBindings map[string]string`. Optional; nil = no binding changes. |
| `runner/amendment_commit.go` | `Commit` (line 97) | Reorder per "Mid-drain ordering" above. Specifically: re-register BEFORE grid append + disk write. |
| `runner/amendment_commit.go` | `Commit` after step 5 (grid append) | Call `c.Grid.WriteDisk(c.Workdir)` (new method) which delegates to `bootstrap.Grid.Write(workdir)` after constructing the bootstrap shape from the in-memory typed grid. Failure rolls back: marks the new arrows in-memory but keeps the grid version unchanged. (Implementer chooses the rollback granularity; simplest is a single-write that succeeds or the whole drain fails.) |
| `runner/dispatcher.go` | post-`pass.Close` branch | When `req.Role == "integrator"` and `pass.State() == PassStateClosed`, call `enqueueAmendmentsForIntegratorPass(d, req)`. |
| `runner/dispatcher.go` | new helper `enqueueAmendmentsForIntegratorPass` | Walks `req.Arrow`'s findings for `missing-cross-context-spec` with status `open`; calls `PendingAmendments`; calls `d.AmendmentQueue.Enqueue`. Does NOT publish bus events directly (per design-M5 closure: the queue observer fires the event). On `ErrAmendmentQueueFull` / `ErrAmendmentDuplicateID`, publish `OpEventAmendmentEnqueueRefused` (new kind) with the reason. |
| `runner/amendment.go` | `AmendmentQueue.Observe` | The journal observer fires `AmendmentEventEnqueue` per Enqueue; the journal-to-bus bridge in `engine/journal.go` translates this to `OpEventAmendmentEnqueued`. Implementer verifies the bridge or adds it. |
| `cmd/ghyll/session.go` | `DispatchSlashCommand` (line 1273) | Add `/drain-amendments` handler. |
| `cmd/ghyll/drain_amendments_cmd.go` (new) | `handleDrainAmendmentsCommand(arg string) SlashCommandResult` | Refuses without `/op-id`. Drains FIFO. Loads each amendment's overlay arrows from `.ghyll/amendments/<amendment-id>/grid-overlay.yaml`. |
| `cmd/ghyll/session_engine.go` | `engineRuntime` struct | Add `committer *runner.AmendmentCommitter`. |
| `cmd/ghyll/session_engine.go` | `openEngineWithOptions` | Construct `rt.committer = &runner.AmendmentCommitter{Grid: rt.grid, Passes: rt.passes, Bus: rt.bus, Queue: rt.amendments, BindingsReRegister: rt.reRegisterBindings, Workdir: rt.workdir, Now: time.Now}`. |
| `cmd/ghyll/session.go` | `initEngine` post-Replay banner | If `rt.Amendments().Pending() > 0`, emit `OpEventRecoveryAmendmentsPending` AND a console banner. No auto-drain. |
| `cmd/ghyll/invalidate_arrow_cmd.go` (new — per spec M2) | `/invalidate-arrow <id>` handler | Marks an arrow invalidated. |
| `engine/replay.go` | `Replay` | Before journal-attach, call `rt.amendments.LoadDrained(id)` for every persisted-drained amendment ID (per spec M8). |

#### Tests (Gap 2)

| Test name | Package | Asserts |
|---|---|---|
| `TestScenario_Dispatcher_IntegratorClose_EnqueuesAmendment` | `runner` | Integrator pass closes with one open `missing-cross-context-spec` finding → `AmendmentQueue.Len() == 1`. |
| `TestScenario_Dispatcher_NonIntegratorClose_DoesNotEnqueue` | `runner` | Same finding under role `implementer` → no enqueue. |
| `TestScenario_Dispatcher_IntegratorAbort_DoesNotEnqueue` | `runner` | Integrator pass aborts → no enqueue. |
| `TestScenario_Dispatcher_QueueFull_EmitsEnqueueRefused` | `runner` | Queue at MaxLen → `OpEventAmendmentEnqueueRefused` with `reason=queue-full`. |
| `TestScenario_DrainAmendmentsCommand_DrainsAll` | `cmd/ghyll` | 2 pending + `/drain-amendments` → queue length 0; grid version bumped twice; disk shows v(N+2). |
| `TestScenario_DrainAmendmentsCommand_NoOpID_Refuses` | `cmd/ghyll` | `/drain-amendments` without `/op-id` → refusal; queue untouched. |
| `TestScenario_DrainAmendmentsCommand_NoPending_Reports` | `cmd/ghyll` | Empty queue → "no pending amendments". |
| `TestScenario_Session_StartupBanner_SurfacesPendingAmendments` | `cmd/ghyll` | Session resumes with 1 pending amendment → banner + `OpEventRecoveryAmendmentsPending` fired. |
| `TestScenario_Session_StartupBanner_NoAutoDrain` | `cmd/ghyll` | After `initEngine`, queue length unchanged. |
| `TestScenario_AmendmentCommit_GlobalLock_Serializes` | `runner` (with `-race`) | Two goroutines: drain + enqueue. No race; version bumps serialize. |
| `TestScenario_AmendmentCommit_DrainedIDDedupAcrossRestart` | `runner` | Enqueue → drain → restart → re-enqueue same ID → `ErrAmendmentDuplicateID`. |
| `TestScenario_AmendmentCommit_BindingsReRegisterFires_BeforeBump` | `runner` | `BindingsReRegister` is wired and errors → Commit returns wrapped error; grid version NOT bumped; on-disk grid unchanged. |
| `TestScenario_AmendmentCommit_DiskWriteAfterAppend` | `runner` | Commit success → `bootstrap.Read(workdir)` returns `gridVersionAfter`. |
| `TestScenario_AmendmentCommit_AbortInFlightPassOnSourceArrow` | `runner` | Concurrent in-flight pass on SourceArrow → aborted after drain; reason contains "amendment ... drained". |
| `TestScenario_AmendmentCommit_DependencyPropagation_V1Narrow` | `runner` | Pass on arrow B that depends on arrow A (the source arrow) is NOT aborted (v1 narrow scope per spec H7 deferral). |
| `TestScenario_OperatorBus_PayloadContract_AmendmentDrained` | `runner` | Payload contains `amendment_id`, `source_arrow`, `grid_version_before`, `grid_version_after`, `arrows_added`, `passes_aborted`, `status`. |
| `TestScenario_OperatorBus_PayloadContract_RecoveryAmendmentsPending` | `runner` | Payload contains `count`, `amendment_ids`. |
| `TestScenario_InvalidateArrowCommand_MarksInvalidated` | `cmd/ghyll` | `/invalidate-arrow <id>` → arrow status transitions; next `/run-arrow` re-traverses. |

#### BDD scenarios (Gap 2)

`specs/features/amendment-drain.feature` (new):

```gherkin
Feature: Amendment drain mutates the live grid

  Scenario: Integrator-pass close enqueues amendment
    Given an integrator pass closes with one open missing-cross-context-spec finding
    Then the amendment queue length is 1
    And an amendment-enqueued event is published with the typed payload

  Scenario: Operator drain applies the amendment
    Given the queue length is 1
    And the operator has set /op-id
    When the operator runs "/drain-amendments"
    Then the in-memory grid version increments by 1
    And the on-disk grid file shows the new version
    And the queue length drops to 0
    And an amendment-drained event is published with status "complete"

  Scenario: Drain aborts in-flight pass on source arrow
    Given a pass is running against arrow A1
    And an amendment with SourceArrow A1 is pending
    When the operator runs "/drain-amendments"
    Then the pass on A1 is aborted with reason matching "amendment ... drained"
    And the FindingsStore retains the findings raised before abort

  Scenario: Startup banner surfaces pending amendment without auto-draining
    Given a session was killed mid-pass leaving one pending amendment
    When ghyll opens a new session
    Then the startup banner reports 1 pending amendment
    And a recovery-amendments-pending event is published
    And the queue length remains 1 (no auto-drain)

  Scenario: Drained-ID dedup survives restart
    Given an amendment was drained in a prior session
    When ghyll opens a new session
    And an enqueue with the same amendment ID is attempted
    Then enqueue refuses with ErrAmendmentDuplicateID
    And an amendment-enqueue-refused event is published with reason "duplicate-id"

  Scenario: Re-register failure aborts drain before version bump
    Given an amendment whose new language-bindings reference an unknown concept
    When the operator runs "/drain-amendments"
    Then the drain refuses with a binding-validation error
    And the in-memory grid version is unchanged
    And the on-disk grid file is unchanged

  Scenario: Queue-full enqueue surfaces refused event
    Given the amendment queue is at MaxLen
    When the integrator pass closes with another missing-cross-context-spec finding
    Then no new amendment is enqueued
    And an amendment-enqueue-refused event is published with reason "queue-full"
```

### Cross-cutting wiring

#### Atomic-pointer hook bundle (design-M3 closure)

`runner.AdversarialHooks` is a single struct grouping the four hooks
and the remediation defaults. `PassDispatcher.Hooks` is
`*atomic.Pointer[AdversarialHooks]`. The session-engine constructs
the pointer once and stores the bundle atomically; the operator's
slash command toggles by storing a new pointer (or nil for disable).

#### Subscriber tagging (spec H3 closure)

`OperatorBus.Subscribe` takes an extra `tag string` argument
(additive — existing call sites pass `""` or a project-specific name).
The JSONL audit writer subscribes with `tag = "audit"`.
`HasAuditSubscriber()` returns true iff any subscriber's tag equals
`"audit"`.

#### Coverage check entry point (design-M2 closure)

`verifyBindingsCoverage(rt *engineRuntime) error` is called post-Replay.
It iterates two sources:

1. `rt.grid.Arrows()` — the typed runner.Grid populated by Replay.
2. `rt.gridFile.Arrows` — the bootstrap shape for arrows declared in
   the grid file but not yet seen by Replay (freshly-init'd projects
   with no traversal history).

For each arrow's clauses, the function computes
`ConceptRegistryKey(clause)` and calls `rt.registry.Lookup(key)`. A
miss returns `*MissingBindingError{Concept, Language}` with all
missing keys aggregated.

---

## Findings closure map

Total: 86 findings (40 spec + 46 design). Closed: 86. Deferred: 0.

### Spec adversarial closure (40)

| ID | How closed | Section |
|---|---|---|
| C1 | Replaced `MinDepthTier > DepthRankNone` with `DepthType == DepthTypeSensitive`; cited canonical predicate at `runner/routing.go:117,153`. | Gap 1 / Trigger predicate |
| C2 | Re-tabulated 18 concepts on three orthogonal axes (language-bound × auto-applied × in-RegisterBuiltins). | Concept-classification canonical table |
| C3 | Replaced `bootstrap.GridFile` with `bootstrap.Grid` everywhere; cited `bootstrap/grid.go:22`. | Gap 3 / Type-name correction |
| C4 | Coverage check runs in two phases: post-Replay against typed `runner.Grid` AND against `bootstrap.Grid.Arrows` for not-yet-traversed arrows. Added `RequiredBindingsFromBootstrap` helper. | Gap 3 / Validation surface |
| C5 | New adapter contract for `RunRemediationLoop` + `ProducerFixHarness`: harness's `runOneRound` is exported as `RunOneRound(ctx, findings, round)` with round-indexed digest map; the adapter closure satisfies `FixAttemptFn`. | Gap 1 / Loop-bomb interlock |
| C6 | `RunRemediationLoop` is the production driver; `AdversarialOrchestrator` marked test-only via docstring; no dispatcher path calls Orchestrator. | Gap 1 / Single-orchestrator decision |
| C7 | Removed "auto-drain on integrator-pass close" rule; integrator-pass close ENQUEUES only; drain fires only on operator slash command + recovery banner. | Gap 2 / Trigger rules |
| H1 | V1 default: auto-enable adversarial hooks on session start from active dialect; `/adversary disable` opts out. | Gap 1 / Refusal semantics |
| H2 | Typed Payload contract: each event kind has required keys (table). | OperatorBus payload contracts |
| H3 | `Bus.HasAuditSubscriber()` is the floor; dispatch refuses without it. Subscribers carry tags. | OperatorBus payload contracts |
| H4 | `FindingRecord.GridVersion uint64` field added; stamped on Raise from `clause.GridVersion`. | Gap 1 / FindingRecord.GridVersion |
| H5 | `AttackBuilder(round)` contract: `attack.Round == round` MUST hold; closure captures explicit round arg (no loop-variable capture). | Gap 1 / AdversaryBuilder + AttackBuilder contract |
| H6 | Pass-identity collision avoided via clauseID namespacing: `<passID>/adv/<concept>/round<N>` vs `<passID>/verify/<concept>`. | Gap 1 / Pass identity |
| H7 | V1 narrow scope explicit: drain aborts only passes whose ArrowID == amendment.SourceArrow; broader §7.2 propagation deferred to v2. Documented in `Commit` docstring. | Gap 2 / Invalidation propagation scope |
| H8 | `BindingsReRegister` callback wired on `AmendmentCommitter`; runs in step 3 of revised Commit ordering (BEFORE grid append). | Gap 3 / Re-registration on amendment |
| H9 | `IsLanguageBoundConcept` auto-derived from `gates/concepts/*.yaml` at package init. No hand-list. | Auto-derived universal table |
| H10 | V1 behavior named explicitly: implement four evaluators as Go in-process. Gap 4 fully specified. | Gap 4 / Evaluator contracts |
| M1 | `req.Arrow.Requirements` is the v1 carrier per `runner/grid.go:41`. | Gap 1 / Inputs from the dispatcher |
| M2 | New `/invalidate-arrow <id>` verb added (operator-attested via `op-id`). | Cross-cutting / `/run-arrow` operator escape |
| M3 | `passes` table extended with `remediation_outcome`, `remediation_rounds_used`; JSONL is source of truth. | Gap 1 / RemediationReport persistence |
| M4 | Same fix as H4 — `GridVersion` field on FindingRecord covers both. | Gap 1 / FindingRecord.GridVersion |
| M5 | New `OpEventRecoveryAmendmentsPending` kind with typed Payload. | OperatorBus payload contracts |
| M6 | Phrasing fix: "within the same process" — ADR-009 A-3 cited inline. | Gap 2 / Acceptance criterion 5 |
| M7 | Dispatcher's `verifyClauses` is `robust + auto-inserts` post-converged-cycle, so sensitive clauses don't re-evaluate; the engine's per-(pass_id, clause_id) uniqueness handles any remaining edges. Aborted-pass EvaluationRuns are tagged `pass_state` via the existing engine schema. | Gap 1 / Dispatcher integration pseudocode + Gap 2 partial-failure section |
| M8 | `Replay` calls `LoadDrained` for every persisted-drained amendment before journal-attach. | Gap 2 / Recovery contract |
| M9 | Queue-full surfaces `OpEventAmendmentEnqueueRefused` (new kind); not a finding. | Gap 2 / Enqueue contract |
| M10 | `Adversary.AdversaryRole` defaults to literal `"adversary"` — v1 contract documented; variants are out of scope for v1. | Gap 1 / Trigger predicate |
| M11 | Adversary's hooks (OpenSweep / Classify) MUST be stateless across calls. Documented as hook-contract. The constructed Adversary per round closes the seam; tests fixture confirms statelessness via call-count assertions. | Gap 1 / AdversaryBuilder + AttackBuilder contract |
| M12 | Per-language env starter table in `defaultEnvAllowlist` (Go, Rust, TS/Node, Python). | Gap 3 / Per-language evaluator construction |
| M13 | Empty-scope vacuity is acknowledged in `compiles.yaml` edge-cases; the runtime treats empty-package-tree pass-true as a known false-positive class. The implementer adds a `Details["scope-empty"]` flag on the EvaluationRun when scope resolves to zero matches; status CLI surfaces a warning. | Gap 3 / Per-language evaluator construction |
| M14 | New `MaxRecursiveDispatch int` field (default 4); ctx-injected depth counter; refusal with `ErrDispatchRecursionExceeded`. | Gap 1 / Dispatcher recursion budget |
| L1 | Cited `specs/architecture/components/amendment.md` invariant 1 for "amendments infrequent" assumption (not ADR-012). | Gap 2 / Trigger rules (no longer references ADR-012 A-1) |
| L2 | Reworded: in-memory FindingsStore is runtime view; engine.db is authoritative on-disk. | Gap 1 / Outputs back to the dispatcher |
| L3 | Reference is "the harness's redaction filter (per `specs/architecture/components/concepts.md`)"; no longer pins `secretRedactRE`. | Gap 3 / Acceptance #6 |
| L4 | `RemediationConfig.SeverityThreshold` zero translates to `SeverityMedium` (the grid default per `gates.md` §2.1). Implementer-track: the dispatcher's wire MUST translate `grid.SeverityThreshold == ""` to `SeverityMedium`. | Gap 1 / Trigger predicate inputs |
| L5 | Reworded: "at least 2 contexts" → "exactly the case of a cross-context gap requires the contexts list to name ≥ 2 contexts; smaller is malformed." | Gap 2 / Enqueue contract |
| L6 | `RoundsMax`: zero → harness default; negative → grid-load error. Specified explicitly. | Gap 1 / Trigger predicate inputs |
| L7 | Verb pinned: `/drain-amendments`. | Gap 2 / Trigger rules |
| L8 | Binding re-registration failure → typed `OpEventAmendmentDrained` with `status=binding-re-register-error`; does NOT panic. Per the revised step 3 (re-register BEFORE bump), the drain itself aborts and the binding stays at the old generation. | Gap 2 / Mid-drain ordering + Gap 3 / Failure modes |
| L9 | Loop-bomb predicate clarified: it digests the producer's RESPONSE artifact (not the upstream file). The catch is "same answer twice"; documented explicitly. | Gap 1 / Loop-bomb predicate clarification |

### Design adversarial closure (46)

| ID | How closed | Section |
|---|---|---|
| C1 | `ProducerFixHarness.RunOneRound(ctx, findings, round)` exported with explicit round arg; round-indexed digest map; loop-bomb compares round vs round-1 absolute. | Gap 1 / Loop-bomb interlock |
| C2 | Attack-builder stamps `Requirements: req.Arrow.Requirements` (per `runner/grid.go:41`); `ClassificationsStore.DeclareRequirement` runs per requirement. | Gap 1 / Inputs from the dispatcher |
| C3 | `engineRuntime.gridFile *bootstrap.Grid` field added; populated in `initEngine`; plumbed into `openEngineWithOptions` signature. | Implementation / `engineRuntime.gridFile` |
| C4 | `AmendmentCommitter.Commit` now writes the new grid to disk via `bootstrap.Grid.Write(workdir)` in step 6; `BindingsReRegister` runs in step 3 against an in-memory overlay constructed from `rt.gridFile + amendment.NewArrows + amendment.NewLanguageBindings`. Fail-before-bump ordering. | Gap 2 / Mid-drain ordering + Gap 3 / Re-registration |
| C5 | `IsUniversalConcept` / `IsLanguageBoundConcept` auto-derived from `gates/concepts/*.yaml` at package init; cardinality assertion (= 18) panics on drift. | Auto-derived universal table |
| C6 | Dropped "stamp Args + blocklist" alternative. Chose `EvaluatorWithRunner` signature variant: additive type, additive `Registry.RegisterWithRunner` method, `Runner.passes` field added explicitly to "Files touched". | Gap 4 / Single-active-role-instance |
| C7 | Added `ArrowStatusAbortedRemediation` enum value; CloseReason vocabulary documented; the early-return path sets the new status explicitly (no DeriveArrowStatus bypass). | Gap 1 / Dispatcher integration pseudocode + OperatorBus payload contracts |
| H1 | `req.AdversaryRole = a.AdversaryRole` is stamped by `runAdversarialCycle` before verification. Modal driver sees the 3-role-chain encoding. | Gap 1 / Dispatcher integration pseudocode |
| H2 | `PassDispatcher.Hooks *atomic.Pointer[AdversarialHooks]` — atomic swap; in-flight dispatch snapshots locally; concurrent enable/disable is race-clean. | Cross-cutting wiring / Atomic-pointer hook bundle |
| H3 | `EvaluateSingleActiveRoleInstance` filters by `passID != c.PassID`; unit test `TestScenario_SingleActiveRoleInstance_FiltersSelf` covers it. | Gap 4 / Single-active-role-instance access |
| H4 | Dispatcher verifies over `robust + auto-inserts` only after a converged cycle; sensitive clauses don't re-evaluate. Acceptance #6 covers it. | Gap 1 / Dispatcher integration pseudocode |
| H5 | Re-register BEFORE grid append; re-register failure aborts drain without irreversible bump. | Gap 2 / Mid-drain ordering |
| H6 | `EvaluateModeDeterminableFromRepo` uses `openNoFollow` + clamp-to-`ProjectDir`; path-escape refused with typed error before file open. | Gap 4 / mode-determinable-from-repo path safety |
| H7 | `OpEventAmendmentEnqueueRefused` (new event kind) for queue-full / duplicate-id failures; `OpEventAmendmentEnqueued` reserved for successful enqueues only. | Gap 2 / Enqueue contract + OperatorBus payload contracts |
| H8 | `AmendmentContexts` callable dropped; contexts read directly from `rt.gridFile.BoundedContexts`. | Gap 2 / AmendmentContexts source |
| H9 | Same as C5 — auto-derived from YAMLs eliminates hand-list drift. | Auto-derived universal table |
| H10 | Hooks-check + recursion-check fire BEFORE `OpenPass` + `PassIDGen`; refused dispatches don't spin the counter. | Cross-cutting / Dispatcher counter discipline |
| H11 | Single-PR landing per gap; internal commits each pass `make test-unit`. RegisterGridBindings ships with a session-option gate that defaults OFF in the commit that wires it and flips ON in the commit that adds amendment re-register. By PR close, default is ON. | Implementation / Dependency order |
| H12 | V1 default: auto-enable on session start; the disabled state is opt-in via `/adversary disable`. UX cliff resolved. | Gap 1 / Refusal semantics |
| M1 | Per-language env via extended `defaultEnvAllowlist`; grid-schema extension (per-binding env) deferred to v2 with ADR. | Gap 3 / Per-language evaluator construction |
| M2 | `RequiredBindingsFromBootstrap(*bootstrap.Grid)` walks the freshly-init'd grid file's untyped arrows. The coverage check consults both the typed runner.Grid AND the bootstrap shape. | Gap 3 / Validation surface |
| M3 | `AdversarialHooks` struct groups Factory, OpenSweep, Classify, ProducerFix, RemediationConfigDefaults; single `Hooks` field on PassDispatcher. Amendment-related fields (`AmendmentQueue`, etc.) remain individually-named (fewer than 9 — group not necessary). | Cross-cutting wiring / Atomic-pointer hook bundle |
| M4 | Modal interaction documented: cycle pauses on modal-pending state (the loop's `ctx` is the same as the dispatcher's; modal driver's blocking pause propagates via ctx). New section "Modal interaction with cycle" in Gap 1's wire. The implementer adds a one-paragraph docstring on `runAdversarialCycle`. | Gap 1 / Dispatcher integration pseudocode |
| M5 | The dispatcher's `enqueueAmendmentsForIntegratorPass` does NOT publish; the queue's observer + journal-to-bus bridge fans out the event. Single publish path. | Cross-cutting wiring / Event-fanout deduplication |
| M6 | Re-register runs in step 3 (BEFORE MarkDrained at step 7); failure aborts cleanly. The drained-id stays pending. | Gap 2 / Mid-drain ordering |
| M7 | Subprocess sandbox limitation acknowledged inline; v1 ships without filesystem isolation per `gates.md` §2.1 D18; flagged as known limitation. | Gap 3 / Per-language evaluator construction (final paragraph) |
| M8 | `AdversarialOrchestrator` marked test-only; round-numbering disagreement avoided because production never drives both paths. | Gap 1 / Single-orchestrator decision |
| M9 | `OpEventAdversarialRoundStart` Payload carries `tier_label` for cost telemetry. | Cross-cutting / Cost telemetry on adversarial rounds |
| M10 | Asymmetry documented: integrator-pass enqueue is automatic (no op-id); operator drain requires op-id (operator-attested per §3.7). One-line explanation on `/drain-amendments` refusal output. | Gap 2 / Trigger rules |
| M11 | `engineRuntime.committer` is immutable post-construct; documented at field site. | Cross-cutting wiring (implicit in struct construction comment) |
| M12 | `EvaluatePredicateForm` quotes `gates/concepts/predicate-form.yaml`'s `predicate-grammar` description verbatim; the implementer references the YAML in the docstring. | Gap 4 / Evaluator contracts |
| M13 | `AmendmentContexts` callable removed; contexts come from grid field. No lock-free re-entrance concern. | Gap 2 / AmendmentContexts source |
| M14 | Grid-load validation pass walks each clause's `Args` against the YAML schema. New `bootstrap.ValidateClauseArgs(arrow, clause, conceptYAML)` helper called from `RequiredBindingsFromBootstrap`. | Gap 3 / Validation surface (extended) |
| M15 | Early-return path uses `ArrowStatusAbortedRemediation` (new enum) rather than direct assignment of an existing status; `DeriveArrowStatus` is not bypassed for the live cycle. | Gap 1 / Dispatcher integration pseudocode |
| M16 | `NewRunner(tier, passes)` two-arg form. Test sites enumerated (estimated): `runner/runner_test.go`, `runner/dispatcher_test.go`, `runner/adversarial_test.go`, `tests/acceptance/steps_runner.go`. Implementer greps `runner.NewRunner(` to confirm exhaustively. | Gap 4 / Single-active-role-instance access |
| M17 | `single-active-role-instance.yaml`'s args names quoted: `role` and `bounded-context`. Evaluator reads exactly those. Unit test enforces. | Gap 4 / Evaluator contracts |
| M18 | Operator escalation flow: post-`escalated-rounds-max` emits `OpEventEscalationPresented`; the modal driver surfaces the operator-attested verbs (`accepted-risk`, `upstream-rework`, `/invalidate-arrow`). Resolution emits `OpEventEscalationResolved`. | Gap 1 / Outputs back to the dispatcher (extended) |
| L1 | `IsUniversalConcept` map-based; closed-vocabulary; auto-derived. | Auto-derived universal table |
| L2 | `/adv-cycle` considered; kept `/adversary` because the closure-map convention is "verb matches the role it configures." Operator UX docs flag the synthetic role-id vs. command distinction. (Defensible; if implementer prefers `/adv-cycle`, that's also fine.) | Gap 1 / Refusal semantics |
| L3 | Use `errors.Is(err, ErrMissingBinding)` for the existing sentinel; do NOT add `ErrLanguageBindingInvalid` unless the shape-error and missing-binding cases need separate handling (they do — `ErrLanguageBindingInvalid` is added but as a sibling, not a wrapper). | Gap 3 / Files touched |
| L4 | Args descriptions quoted from YAML in evaluator docstrings. | Gap 4 / Evaluator contracts |
| L5 | Filename `cmd/ghyll/adversary_cmd.go` accepted. | Implementation / Files touched (Gap 1) |
| L6 | `runner.FormatAmendmentSummary` produces a multi-line string with embedded indent; banner uses formatter's own indent. | Gap 2 / Startup banner (in implementation contract) |
| L7 | `OpEventModalBackpressure` added to the `/run-arrow` subscriber list. | Gap 1 / Files touched |
| L8 | `gocontext` alias used in cmd/ghyll pseudocode where applicable. (Cosmetic; implementer-discretion.) | Implementation / Files touched |
| L9 | `RemediationConfig.CountUnevaluatedAsOpen` doc references `applyDefaults` behavior; bool semantics explicit. | Gap 1 / Trigger predicate inputs |

---

## ADRs that should be drafted alongside the implementation

| ADR | Title | Survived revision? | Section motivating |
|---|---|---|---|
| ADR-v4-001 | Registry-key shape: `<concept>.<language>` flat key | YES — survives, with auto-derived predicate amendment | Gap 3 / Registry-key shape |
| ADR-v4-002 | Dispatcher gains an adversarial phase wired unconditionally for depth-sensitive arrows; auto-enabled-on-session-start refusal semantics for unwired hooks | YES — modified from "refusal-by-default" to "auto-enable-by-default + opt-out via /adversary disable" | Gap 1 / Refusal semantics |
| ADR-v4-003 (NEW) | Amendment-driven re-register ordering: in-memory overlay, fail-before-bump | NEW — introduced by design-adversarial C4 closure | Gap 2 / Mid-drain ordering + Gap 3 / Re-registration |
| ADR-v4-004 (NEW) | Concept classification auto-derived from `gates/concepts/*.yaml` at package init | NEW — introduced by design-adversarial C5/H9 closure | Auto-derived universal table |
| ADR-v4-005 (NEW) | OperatorEvent typed Payload contract for adversarial + amendment events | NEW — introduced by spec H2/H3 closure | OperatorBus payload contracts |
| ADR-v4-006 (NEW) | `EvaluatorWithRunner` signature variant for runtime-dependent evaluators | NEW — introduced by design C6 closure | Gap 4 / Single-active-role-instance access |

ADR-v4-001 and ADR-v4-002 survive the revision but with substantive
amendments. ADR-v4-003 through ADR-v4-006 are new and required.

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
- `TestScenario_ConceptClassification_AgreesWithYAML`

#### `runner` package — Gap 3
- `TestScenario_RegisterGridBindings_RegistersCompilesGo`
- `TestScenario_RegisterGridBindings_RejectsInvalidKey`
- `TestScenario_RegisterGridBindings_RejectsMalformedKey`
- `TestScenario_RegisterGridBindings_RejectsEmptyCommand`
- `TestScenario_RegisterGridBindings_MissingBindingForArrowClause`
- `TestScenario_RegisterGridBindings_AllRequiredBindingsPresent`
- `TestScenario_RegisterGridBindings_ConcurrentRegisterReplace` (race)
- `TestScenario_ConceptRegistryKey_UniversalReturnsBare`
- `TestScenario_ConceptRegistryKey_LanguageBoundReturnsCompound`
- `TestScenario_ConceptRegistryKey_LanguageBoundMissingLanguageArg`
- `TestScenario_RequiredBindingsFromBootstrap_UntypedArrows`
- `TestScenario_RegisterGridBindings_AmendmentReRegisters_BeforeBump`

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
- `TestScenario_Dispatcher_RecursionDepthExceeded`
- `TestScenario_Dispatcher_AdversaryRound_ClauseIDsNamespaced`
- `TestScenario_Dispatcher_AttackBuilder_RoundContract`
- `TestScenario_OperatorBus_PayloadContract_AdversarialRoundStart`
- `TestScenario_OperatorBus_PayloadContract_RemediationEscalated`

#### `runner` package — Gap 2
- `TestScenario_Dispatcher_IntegratorClose_EnqueuesAmendment`
- `TestScenario_Dispatcher_NonIntegratorClose_DoesNotEnqueue`
- `TestScenario_Dispatcher_IntegratorAbort_DoesNotEnqueue`
- `TestScenario_Dispatcher_QueueFull_EmitsEnqueueRefused`
- `TestScenario_AmendmentCommit_GlobalLock_Serializes` (race)
- `TestScenario_AmendmentCommit_DrainedIDDedupAcrossRestart`
- `TestScenario_AmendmentCommit_BindingsReRegisterFires_BeforeBump`
- `TestScenario_AmendmentCommit_DiskWriteAfterAppend`
- `TestScenario_AmendmentCommit_AbortInFlightPassOnSourceArrow`
- `TestScenario_AmendmentCommit_DependencyPropagation_V1Narrow`
- `TestScenario_OperatorBus_PayloadContract_AmendmentDrained`
- `TestScenario_OperatorBus_PayloadContract_RecoveryAmendmentsPending`

#### `cmd/ghyll` package
- `TestScenario_Session_BindingsCoverage_RefusesREPL`
- `TestScenario_Session_BindingsCoverage_StartsWhenComplete`
- `TestScenario_AdversaryCommand_DisableTearsDown`
- `TestScenario_AdversaryCommand_EnableRebuilds`
- `TestScenario_AdversaryCommand_StatusReports`
- `TestScenario_AdversaryCommand_HookSwapRaceClean` (race)
- `TestScenario_DrainAmendmentsCommand_DrainsAll`
- `TestScenario_DrainAmendmentsCommand_NoOpID_Refuses`
- `TestScenario_DrainAmendmentsCommand_NoPending_Reports`
- `TestScenario_Session_StartupBanner_SurfacesPendingAmendments`
- `TestScenario_Session_StartupBanner_NoAutoDrain`
- `TestScenario_InvalidateArrowCommand_MarksInvalidated`

### BDD scenarios

One new feature file per gap; Gherkin blocks above. Total: 4 new
feature files, ~20 scenarios. Step definitions extend
`tests/acceptance/steps_*.go` as named in the implementation
contract.

### Race-detector expectations

Re-tested under `-race` after each gap lands:

- `runner` (all packages with concurrency surface): Registry,
  PassRegistry, FindingsStore, AmendmentQueue, AmendmentCommitter,
  ProducerFixHarness, OperatorBus.
- `cmd/ghyll`: session.go (REPL goroutine), session_engine.go (atomic
  pointer for hooks).

New race-prone surfaces this PR introduces (must have explicit
race-detector tests):
- `atomic.Pointer[AdversarialHooks]` swap vs. in-flight Dispatch.
- `engineRuntime.gridFile` overlay swap vs. enqueueAmendmentsForIntegratorPass
  read (RWMutex-protected, tested via concurrent goroutines).
- `runner.RegisterGridBindings` concurrent with `Registry.Replace`
  (existing RWMutex, new test).

---

## Coverage targets

Current threshold: 70% per `make coverage-check`. Local floor: 78%.

| Gap | Net new LOC (impl) | Net new LOC (tests) | Delta to coverage |
|---|---|---|---|
| 4 (4 evaluators + auto-classify) | ~550 | ~700 | +1.5–2.0% |
| 3 (binding registration + overlay) | ~350 | ~500 | +0.5–1.0% |
| 2 (drain + commit ordering) | ~250 | ~400 | +0.5% |
| 1 (cycle + loop-bomb + payloads) | ~450 | ~600 | +0.5–1.0% |
| Cross-cutting (atomic hooks, audit-subscriber, payload contracts, invalidate-arrow, recursion budget) | ~150 | ~250 | +0.3–0.5% |

Total expected: ~1750 LOC implementation + ~2450 LOC tests, ~24 files
modified/created + 6 ADRs + ~50 new tests (~35 unit, ~20 BDD).

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
  `runner/notodo.go`
- `bootstrap/grid.go`, `bootstrap/bindings.go`
- `cmd/ghyll/session.go`, `cmd/ghyll/session_engine.go`,
  `cmd/ghyll/run_arrow_cmd.go`
- `engine/journal.go`, `engine/replay.go`
- `gates/concepts/*.yaml`, `gates/concepts/README.md`, `gates/assets.go`
- `gates.md` §1.1, §2.1 D18, §3.7, §5.1–5.2, §6, §7.1, §7.1a, §7.2,
  §7.3, §8, §11.1, §11.3
- ADRs: ADR-005, ADR-006, ADR-008, ADR-009, ADR-010, ADR-011,
  ADR-012, ADR-013, ADR-014, ADR-015, ADR-016
- New ADRs (to be drafted): ADR-v4-001 through ADR-v4-006
- `specs/v4/diamond-load-bearing-spec.md` (superseded)
- `specs/v4/diamond-load-bearing-design.md` (superseded)
- `specs/v4/diamond-spec-adversarial.md` (40 findings closed)
- `specs/v4/diamond-design-adversarial.md` (46 findings closed)
- `specs/v4/code-eval-2026-05-25.md` (integrator pass that surfaced
  the gaps)
