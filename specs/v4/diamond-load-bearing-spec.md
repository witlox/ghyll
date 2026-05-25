# Load-bearing wiring — analyst spec

## Date: 2026-05-25

## Frame

The `specs/v4/code-eval-2026-05-25.md` integrator pass surfaced three
runtime gaps where the production state machine relies on machinery
that exists in `runner/` and `bootstrap/` but is reachable only from
acceptance tests. This spec defines the behavior the production caller
must exhibit. The architect picks the call sites; the analyst is
specifying the contract the caller must satisfy.

The three gaps:

1. **Adversarial cycle never runs in production** — `Adversary.Attack`,
   `RunRemediationLoop`, `AdversarialOrchestrator` (and the
   `ProducerFixHarness` loop-bomb detector) are reachable only from
   tests. `runner/dispatcher.go:Dispatch` runs verification clauses
   and returns — it never enters the §11 adversarial phase.
2. **Amendment drain never runs in production** —
   `AmendmentCommitter.Commit` is reachable only from tests. The
   session loop constructs the `AmendmentQueue`; the journal observer
   persists `enqueue` and `drain` events, but no production path
   calls `Commit` to apply queued amendments to the live grid.
3. **Language-binding evaluation never registered** —
   `bootstrap.GridFile.LanguageBindings` is parsed into a Go map and
   never read. `RegisterBuiltins` registers seven harness-shipped
   evaluators; any arrow with a `compiles` / `lint-clean` /
   `tests-pass` / `mutation-score` / `every-step-bound` /
   `no-orphan-symbol` / `acyclic-dependency-graph` clause crashes at
   evaluate-time with `ErrConceptNotRegistered`.

This spec defines WHEN each load-bearing seam fires, WHAT inputs it
needs, WHAT outputs feed back, and WHAT termination conditions
prevent runaway behavior. Citations to `gates.md` and ADRs anchor the
constraints already in the spec.

---

## Gap 1: Adversarial cycle

### Trigger / when it fires

The adversarial cycle is a **phase of every arrow that carries at
least one `depth-sensitive` clause** (`gates.md` §11). The dispatcher's
current path — open pass → evaluate clauses → derive status — is
**verification only**. Verification is §11's third phase; adversarial
and remediation precede it.

**Firing rule.** Given a `DispatchRequest req`:

1. The arrow's clause set is partitioned into `depth-sensitive`
   clauses (`MinDepthTier > DepthRankNone` per the runner's clause
   shape) and `depth-robust` clauses.
2. If the `depth-sensitive` set is **empty**, the dispatcher runs
   verification-only (today's path). `gates.md` §11: "A purely
   `machine` / `depth-robust` arrow runs verification only."
3. If the `depth-sensitive` set is **non-empty**, the dispatcher
   **MUST** run the cycle in the order:
   - Adversarial phase (round 0 — initial attack).
   - Remediation loop (bounded by `remediation-rounds-max`).
   - Verification (today's path), with the auto-inserted clauses
     `no-open-finding` and `every-requirement-meets-min-depth`
     appended via `runner.VerificationAutoInsert`.

The adversarial cycle does **NOT** fire on:

- The initialization arrow (the synthetic `init` role-id producer per
  §1.1) — `init` clauses are `attested` against the operator, not
  adversary-attackable. The init arrow runs verification-only against
  its `attested` clauses.
- The synthetic `adversary` role-id's own passes (the adversary
  cannot attack itself — `gates.md` §1.1, §11 invariant 5).
- Arrows declared with `depth-sensitive` clauses but whose actual
  routed tier is below the minimum (the dispatcher's existing
  `unevaluated` short-circuit per §7.1 already fires; the cycle is
  skipped because attacking at the wrong tier would itself be
  `unevaluated`).

**Lifecycle position.** The cycle fires **after** `OpenPass` /
`PassRegistry.Register` succeed and **before** the first call to
`Runner.Evaluate` on the producer's verification clauses. Reason:
findings raised by the cycle become inputs to verification's
auto-inserted `no-open-finding` clause; verification needs them on
the `FindingsStore` before it runs.

### Inputs from the dispatcher

Per `runner.AdversaryAttack`, the cycle needs:

| Field | Source |
|---|---|
| `ArrowID` | `req.Arrow.ID` |
| `PassID` | the dispatcher's `PassIDGen()` output (same value as the verification phase's PassID — one pass-id per arrow per traversal, per ADR-008) |
| `ProjectDir` | `req.ProjectDir` |
| `DepthClauses` | the subset of `req.Arrow.Clauses` with `MinDepthTier > DepthRankNone` |
| `Requirements` | the arrow's declared requirements with their `MinDepth` per `gates.md` §11.1 — currently sourced from the arrow definition (architect decides the carrier shape) |
| `Round` | set by `RunRemediationLoop` per round; **not** stamped by the dispatcher |

The cycle also needs the producer's **upstream artifact** so the
adversary can attack it. Today the artifact is implicit (the
adversary sub-activity hooks `OpenSweep` and `Classify` read it from
disk under `ProjectDir`). The dispatcher MUST pass `ProjectDir`; the
hooks resolve the artifact themselves. Per `gates.md` §11 invariant 2
("clean context"), the adversary receives ONLY: the arrow artifact,
the clause definitions, the depth ladder, and the per-requirement
minimum depths.

### Cycle behavior

Per `gates.md` §11 + ADR-009 the cycle is **single-threaded against
this pass**. Concurrency is already enforced by the per-`(role,
context)` lock (`RoleContextLockTable`); the cycle runs inside that
held lock.

**Round 0 (initial attack).**

1. The dispatcher calls `runner.NewAdversary` with the live
   `FindingsStore`, `ClassificationsStore`, and a `*Runner` at the
   arrow's max-required tier.
2. The `Adversary.OpenSweep` and `Adversary.Classify` hooks MUST be
   non-nil. The default `noopOpenSweep` / `noopClassify` (returning
   `nil, nil`) silently passes any arrow that has no machine-clause
   falsifications — a false convergence. **The dispatcher MUST refuse
   to enter the cycle with default no-op hooks** and abort the pass
   with `reason: adversarial-hooks-not-wired`. Architect's job to
   pick how the hooks plug in; analyst's contract: they MUST exist.
3. `Adversary.Attack(ctx, attack)` runs the three sub-activities
   (clause-falsification, open sweep, depth classification) in order.
   Findings flow to `FindingsStore`.

**Rounds 1..N (remediation loop).**

The dispatcher MUST drive the loop via `runner.RunRemediationLoop`
with a `RemediationConfig` that:

- Sets `RoundsMax` from the grid's `remediation-rounds-max` (default
  5 per `gates.md` §2.1).
- Sets `SeverityThreshold` from the grid's `severity-threshold`
  (default `medium`, raise-only at init).
- Sets `CountUnevaluatedAsOpen = true` (the default — see
  `gates.md` §7.3 "`unevaluated`-severity findings block
  no-open-finding").
- Provides a non-nil `FixAttempt` (architect picks the producer
  callback shape — see "Outputs back to the dispatcher" below).
- Provides a non-nil `AdversaryBuilder` that returns a **fresh**
  `Adversary` per round (mandatory per ADR-009 and `gates.md` §11
  invariant 1: "fresh adversary per round").
- Provides an `AttackBuilder` that returns an `AdversaryAttack` with
  the same `ArrowID` / `PassID` / `ProjectDir` / `DepthClauses` /
  `Requirements` for every round but with `Round` set to the loop's
  current round number. **The attack always re-reads the
  upstream artifact**, so the fix's effect is visible — full
  re-attack per D32 / `gates.md` §11.

**Loop-bomb interlock.** When the dispatcher wires the
`FixAttempt`, it MUST wrap it in `ProducerFixHarness` (or equivalent
sha256-digest tracking) so a producer that returns "I fixed it"
without changing the upstream artifact is caught by
`ErrProducerLoopBomb`. The loop-bomb error MUST surface as a
remediation outcome of `RemediationEscalatedHookError` — the loop
exits; the operator is notified.

### Outputs back to the dispatcher

The cycle produces a `*RemediationReport` with `Outcome` ∈ {
`converged`, `converged-with-unevaluated`, `escalated-rounds-max`,
`escalated-no-progress`, `escalated-hook-error`,
`context-cancelled` }.

| Outcome | Dispatcher response |
|---|---|
| `converged` | Proceed to verification. `FindingsStore.ForArrow(arrow)` is the authoritative state; verification's auto-inserted `no-open-finding` evaluator reads it. |
| `converged-with-unevaluated` | Proceed to verification. Verification's `no-open-finding` MUST count `unevaluated`-severity findings as blocking (already does — `gates.md` §7.3); arrow status will derive to `unevaluated`. |
| `escalated-rounds-max` | Abort the pass with `reason: remediation-rounds-max`. The pass closes; the arrow's status is whatever the latest *completed* pass established (per §7.1a abort semantics). Operator MUST attest a path forward (accepted-risk or upstream-rework). |
| `escalated-no-progress` | Abort with `reason: remediation-no-progress`. Same operator-escalation as `escalated-rounds-max`. |
| `escalated-hook-error` (including loop-bomb) | Abort with `reason: producer-loop-bomb` if `ErrProducerLoopBomb` was the cause, else `reason: producer-fix-error`. Operator MUST intervene. |
| `context-cancelled` | Abort with `reason: context-cancelled`. The pass's `Abort` path already handles this. |

**Findings persist regardless of outcome.** Per `gates.md` §7.2
"findings discovered before abort are retained" — the
`FindingsStore` is **never wiped** on remediation escalation. The
operator sees the open findings on the arrow when they re-attest or
re-traverse.

### Termination + loop-bomb detection

| Mechanism | Source | Purpose |
|---|---|---|
| `RoundsMax` cap | grid's `remediation-rounds-max` (default 5; `gates.md` §2.1) | Hard upper bound; no spin |
| `madeProgress=false` from `FixAttempt` | producer signal | Immediate escalation when the producer admits no progress |
| `MaxFixErrors` budget | `RemediationConfig.MaxFixErrors` (default 2) | Consecutive hook errors → escalation |
| `ProducerFixHarness` loop-bomb sha256 digest | wraps `FixAttempt` | Catches "I fixed it" without artifact change |
| `ctx.Err()` checks before Attack and after FixAttempt | `RunRemediationLoop` (F7) | Honors caller cancellation (operator interrupt, session shutdown) |

Loop-bomb detection is **mandatory** — the dispatcher MUST wrap the
producer callback in `ProducerFixHarness`. Without it, a producer
that wraps the upstream artifact in a stable hash and re-emits it
on every round would burn the full `RoundsMax` budget before
escalation, generating `RoundsMax × adversary-tier` cost for no
progress.

### Operator overrides

The operator's existing surfaces apply unchanged:

- **`/run-arrow <id>`** re-traverses an arrow. If a prior pass
  escalated, the operator may re-traverse after addressing the open
  findings (the new pass starts fresh per §7.2: "no resume
  mid-phase").
- **`/attest <ref> <verdict>`** with `verdict=accepted-risk` on an
  open finding transitions it to `accepted-risk`; the next pass's
  adversarial cycle does not re-raise an already-disposed finding
  (the producer may not self-accept, per §7.3 — only the operator).
- **`/exit`** during a long-running remediation cycle cancels via
  `sessionCtx`, which the loop already honors via `ctx.Err()` checks.

The operator does **NOT** have an "override the adversarial cycle"
verb. Skipping adversarial scrutiny would be the magnitude-blind
component rating its own magnitude (per `gates.md` §8); not exposed.
An operator who wants to bypass should declare the arrow's clauses
as `depth-robust` at init time — which itself requires operator
attestation (§6, "depth type is an `attested` item on the
initialization arrow").

### Acceptance

1. An arrow with at least one `depth-sensitive` clause dispatched via
   `/run-arrow` results in:
   - At least one `Adversary.Attack` invocation in the same pass.
   - A `RemediationReport.Outcome` recorded in the engine's pass
     record before verification's first clause evaluates.
2. An arrow with only `depth-robust` clauses dispatches with **no**
   `Adversary` construction and **no** `RemediationReport`.
3. A pass on an adversarial arrow where the producer returns a
   byte-identical artifact across two rounds aborts with
   `reason: producer-loop-bomb`.
4. A pass whose `RemediationOutcome` is `escalated-rounds-max`
   results in arrow status that does **not** propagate to `complete`
   on subsequent verification (because open findings remain ≥
   threshold).
5. Findings raised by the adversarial cycle are present in
   `FindingsStore.ForArrow` after the cycle, regardless of the
   cycle's outcome.

---

## Gap 2: Amendment drain

### Trigger / when it drains

Per `gates.md` §3.7 + ADR-012, amendments are produced when an
integrator pass surfaces a `missing-cross-context-spec` finding.
`PendingAmendments` already extracts the request from the store. The
question is **WHEN does the queue drain into the live grid**.

**Firing rules.** The drain fires on **any one of**:

1. **Integrator-pass close** — when a pass with role `integrator`
   closes via `pass.Close(...)`, the dispatcher checks
   `AmendmentQueue.Len()`. If non-zero AND every pending amendment's
   `SourceArrow` matches a closed pass, the dispatcher triggers a
   drain.
2. **Operator slash command** — a new `/drain-amendments` REPL
   command (architect picks the verb; the analyst-contract is that
   one MUST exist) explicitly drains the queue. Reason: §3.7 says
   the integrator→analyst edge is **operator-attested**; the operator
   has a button.
3. **Session-engine startup, after replay** — if persisted
   amendments exist in `engine.db` without `drained_at` set, the
   recovery flow surfaces them to the operator (a banner line), but
   does **NOT** auto-drain. The operator must explicitly drain. This
   matches `gates.md` §3.7's "operator-attested" semantics: the
   harness does not invent grid mutations across a restart.

The drain does **NOT** fire on:

- Any role's pass close other than integrator.
- A timer / scheduled tick. Amendments are infrequent (ADR-012
  assumption A-1); a tick is unnecessary friction and adds a
  background mutation surface the operator can't observe.

### Enqueue contract — who can enqueue, what shape

Per `runner.PendingAmendments` and `gates.md` §3.7:

- **Only the integrator role** may produce amendment requests.
  Mechanism: `PendingAmendments(store, arrowID, contexts, idGen)`
  walks `FindingsStore.ForArrow(arrow)` for findings of type
  `missing-cross-context-spec` with status `open`. Only the
  integrator's adversarial phase can raise that finding type
  (per the integrator role contract in `specs/architecture/roles/`).
- An `AmendmentRequest` MUST `Validate()` before enqueue. Validation
  enforces: non-empty ID / Reason / SourceArrow / TargetRole / at
  least one FindingID; for `missing-cross-context-spec`, at least 2
  contexts.
- **The queue is bounded** (`DefaultAmendmentQueueMaxLen = 1024`).
  Overflow returns `ErrAmendmentQueueFull` — the enqueuer surfaces a
  finding rather than silently dropping.
- **Drained-ID dedup is durable** — `seenIDs` survives Drain calls
  and is rehydrated at session-start via `LoadDrained`. An amendment
  enqueued, drained, then re-emitted (e.g., the producing integrator
  pass re-runs against an `invalidated` arrow) is **refused** with
  `ErrAmendmentDuplicateID`.

### Global-lock contract

ADR-009 names three locks; the amendment component owns the
**project-wide grid write-lock**. The drain MUST acquire it for the
duration of the drain.

- The lock is **process-local** today (a single `sync.Mutex` on
  `AmendmentCommitter`). ADR-009 A-3 acknowledges multi-machine
  isn't in scope.
- The lock is **uncontested at init** (D35): init writes v1 by
  acquiring the lock at end-of-init when nothing else is competing.
- Per-amendment drains acquire and release in FIFO order. Two
  integrator passes that both enqueue amendments serialize their
  drains via the lock.

**Mid-drain ordering** is fixed (`runner.AmendmentCommitter.Commit`
already encodes this; the spec is documenting the contract):

1. Acquire the project-wide lock.
2. Abort all in-flight passes whose `ArrowID == amendment.SourceArrow`
   AND `State() == PassStateOpen`. The lock release at pass abort is
   handled by the dispatcher's `defer Unregister`.
3. Append each `NewArrows` definition via `Grid.Append`. Each append
   bumps the grid version monotonically.
4. Call `Queue.MarkDrained(amendment.ID)` to emit
   `AmendmentEventDrain` — the journal observer at
   `engine/journal.go` writes `drained_at` to engine.db.
5. Publish `OpEventAmendmentDrained` to the bus for operator-UI
   subscribers and the status CLI.
6. Release the lock.

### Drain behavior — apply, bump version, abort in-flight passes

A drain `Commit(ctx, req)` produces a `CommitResult`:

| Field | Meaning |
|---|---|
| `GridVersionBefore` | the version held before `Commit` started — used in event detail |
| `GridVersionAfter` | the version after every NewArrow appended successfully — may equal `Before` if `NewArrows` is empty |
| `AppendedArrows` | the arrow-IDs successfully appended (in declaration order) |
| `AbortedPasses` | the pass-IDs aborted because they were running against the SourceArrow |
| `CommittedAt` | timestamp of the drain |

**Empty NewArrows is valid** per ADR-012: the analyst may re-engage
and decide the original contract holds. The grid version stays put;
affected passes still abort to re-traverse from a clean state.

**Findings preservation** (`gates.md` §7.2): findings raised on the
aborted pass MUST be retained on `FindingsStore`, tagged with their
original `grid-version`. The integrator's next pass on the new
v(N+1) arrow sees them as hints; the producer treats them as
remediation targets when re-traversing.

**State machine signal.** After a successful drain, the
`OpEventAmendmentDrained` publish fans out to:

- The JSONL audit writer (drain becomes a persistent record).
- The modal driver (operator sees the drain banner).
- The status CLI (`/passes` and `/list-arrows` reflect the new
  grid version on next query).

### Rollback on partial failure

`AmendmentCommitter.Commit` already encodes the partial-failure
contract; this spec ratifies it:

- **Pass-abort precedes grid-append.** If grid-append fails on arrow
  N of M, the passes have **already** been aborted. The aborted
  passes are NOT resurrected — the SourceArrow contract IS
  invalidated by the analyst's decision to commit, regardless of
  the mechanical append failure.
- **`drained_at` persists even on partial append.** The analyst's
  decision to drain is final. The partial append is surfaced via
  `CommitResult.AppendedArrows` (the prefix that succeeded) and the
  wrapped error; the operator triages.
- **No retry.** A partial-append failure is operator-routed: the
  operator inspects, deletes the partial arrows from the grid file
  (an out-of-band fix), and re-enqueues the amendment with a fresh
  ID. The harness does not silently retry.

### Acceptance

1. An integrator pass that closes with one or more open
   `missing-cross-context-spec` findings results in at least one
   `AmendmentRequest` enqueued.
2. After `Commit` succeeds with non-empty `NewArrows`, `Grid.Version`
   returns a value strictly greater than `GridVersionBefore`.
3. After `Commit` returns, an in-flight pass whose `ArrowID` matches
   `Amendment.SourceArrow` has `State() == PassStateAborted` with
   reason containing `amendment ... drained`.
4. Amendments with the same `ID` enqueued twice across drains are
   refused with `ErrAmendmentDuplicateID`.
5. A drain initiated while another drain holds the lock blocks until
   the first releases — no two `OpEventAmendmentDrained` events
   carry overlapping `GridVersionBefore → GridVersionAfter` windows.
6. The session-engine recovery path that detects pending amendments
   surfaces a banner to the operator but does NOT auto-drain.

---

## Gap 3: Language-binding evaluation

### What's universal vs. language-conditional

Per `gates/concepts/README.md` (the canonical classification) and
`gates.md` §5.1, the 18 catalogue concepts split as follows:

**Universal (no language binding required).**

These seven evaluators ship with the harness and are registered by
`runner.RegisterBuiltins` today. The classification is from
`gates/concepts/README.md`:

| Concept | Registered by |
|---|---|
| `no-todo-marker` | `RegisterBuiltins` (already) |
| `trace-link-present` | `RegisterBuiltins` (already) |
| `arrow-artifact-present` | `RegisterBuiltins` (already) |
| `cardinality-check` | `RegisterBuiltins` (already) |
| `no-open-finding` | `RegisterBuiltins` (already) |
| `kill-server-fails-integration` | `RegisterBuiltins` (already) |
| `every-requirement-meets-min-depth` | `RegisterBuiltins` (already) |

The README also marks four more universal concepts that are NOT
yet in `RegisterBuiltins`:

| Concept | Status |
|---|---|
| `unique-definition` | universal per concepts.md / README; **NOT** in `RegisterBuiltins` — gap |
| `predicate-form` | universal per README; **NOT** registered — gap |
| `mode-determinable-from-repo` | universal per README; **NOT** registered — gap |
| `single-active-role-instance` | universal per README; **NOT** registered — gap |

The architect MUST decide whether these four are also registered as
in-process built-ins at session-engine open, or whether they remain
unbound until a project's grid declares them. The analyst-contract:
**any concept marked `language-bound: false` in
`gates/concepts/<concept>.yaml` MUST resolve to a non-binding
evaluator** (in-process or otherwise) before the first dispatch.

**Language-conditional.**

These seven concepts MUST be backed by a per-language binding from
the grid's `language-bindings` map:

| Concept | `language-bound` |
|---|---|
| `compiles` | yes |
| `lint-clean` | yes |
| `every-step-bound` | yes |
| `no-orphan-symbol` | yes |
| `mutation-score` | yes |
| `tests-pass` | yes (added by ADR-013) |
| `acyclic-dependency-graph` | yes |

Per `gates.md` §2.1 D18 and the §5.1 catalogue: "The harness ships
**NO language defaults**; each project declares its own bindings."
This is a hard contract — the harness MUST NOT silently fall back to
a default `go build`, `staticcheck`, etc.

### Grid YAML shape for language-bindings

Current shape in `bootstrap/grid.go:28`:

```yaml
language-bindings:
  <concept>.<language>: <shell-command-string>
```

Example (illustrative — from `gates.md` §5.1):

```yaml
language-bindings:
  compiles.go: "go build ./..."
  compiles.rust: "cargo check"
  compiles.typescript: "tsc --noEmit"
  lint-clean.go: "staticcheck ./... && go vet ./..."
  lint-clean.rust: "cargo clippy -- -D warnings"
  tests-pass.go: "go test ./..."
  mutation-score.go: "go-mutesting ..."
  every-step-bound.gherkin: "godog --dry-run"
  no-orphan-symbol.go: "ghyll-orphan-extract-go"
  acyclic-dependency-graph.go: "ghyll-acyclic-go"
```

**Validation at grid-load.** When `bootstrap.Read` parses
`LanguageBindings`, it MUST validate:

1. Each key matches the pattern `<concept>.<language>` where
   `<concept>` is one of the seven language-bound concepts above.
2. `<language>` is a non-empty `language-id` string (free-form;
   per-project vocabulary — `go`, `rust`, `typescript`, `gherkin`,
   `python` are the canonical set but not exhaustive).
3. The command is a non-empty string after trim. Whitespace-only
   commands are operator misconfiguration (matches
   `BindingEvaluator.Evaluate`'s `ReasonSpawnFailed` path).
4. **No two bindings for the same `(concept, language)` pair.** Map
   ordering in YAML wouldn't allow this; explicit validation guards
   against shape errors.

A grid file with an invalid binding key MUST fail to load, surfacing
`ErrGridInconsistent` (or a new sentinel — architect's call). The
session does not start with a half-validated bindings map.

### Registration timing

**At session-engine open (`session_engine.go:openEngine`-equivalent),
after `RegisterBuiltins(reg)` and before the dispatcher accepts its
first `/run-arrow`.**

The flow:

1. Load the live grid via `bootstrap.Read(projectDir)`.
2. `runner.RegisterBuiltins(reg)` (already happens at
   `session_engine.go:146`).
3. For each entry `(concept.language, command)` in
   `grid.LanguageBindings`:
   - Construct a `BindingEvaluator` via
     `runner.NewBindingEvaluator(command, opts...)`. Options include
     the binding's working directory (`projectDir`) and any
     project-declared environment / timeout overrides (architect
     picks the carrier shape — analyst-contract: it MUST be
     declarable at init).
   - Register the evaluator under the **concept-and-language key**.
     The registry key shape is architect-pickable: either
     `"<concept>.<language>"` (matches the YAML key directly and
     lets the dispatcher select binding by clause's `language` arg)
     or `"<concept>"` with a per-clause language lookup. The
     analyst-contract: at dispatch time, a clause with
     `concept: compiles, args.language: go` MUST resolve to the
     `compiles.go` binding without error.
4. After registration, the registry's coverage MUST be checkable: a
   helper `Registry.Bindings(concept) []language` returns the set
   of languages registered for `concept`; the dispatcher's
   pre-dispatch validation refuses an arrow whose clause needs a
   binding that isn't registered.

**Re-registration on amendment.** When an amendment drains and the
new grid version carries a different `language-bindings` map, the
session MUST re-register. The contract: after
`AmendmentCommitter.Commit` returns successfully, the registry
reflects the new grid's bindings before the next dispatch.
Architect-picks the call site (likely the amendment-drained event
observer); analyst-contract: re-registration is **mandatory**, not
deferred to next session start.

### Per-language evaluator construction

`runner.NewBindingEvaluator(command, opts...)` already exists. The
construction site MUST supply:

| Option | Source | Reason |
|---|---|---|
| `WithTimeout(d)` | grid-declared default or per-binding override | bounds wall-clock; default `DefaultBindingTimeout = 5 min` |
| `WithMaxOutputBytes(n)` | grid-declared default | bounds stdout; default 16 MiB |
| `WithGrace(d)` | grid-declared default | SIGTERM→SIGKILL window; default 5s |
| `WithWorkingDir(projectDir)` | session's `projectDir` | binding runs against the project tree |
| `WithInheritEnv(...)` | grid-declared per-binding | tool-specific env (e.g., `CARGO_HOME`, `GOPATH`); MUST be explicit — no implicit inheritance |
| `WithEnv(...)` | grid-declared per-binding | tool-specific overrides (e.g., `RUSTFLAGS`) |

Per validation-pass-3 F1 (already in `subprocess.go`), the binding
receives **only** the env allowlist plus declared `InheritEnv` /
`Env`. `ANTHROPIC_API_KEY`, `GHYLL_*`, `SSH_AUTH_SOCK` do NOT leak
through. This is a hard contract.

### Behavior on missing binding

**Today.** A clause referencing `compiles` (a language-bound concept
not registered by `RegisterBuiltins`) results in
`Registry.Lookup → ErrConceptNotRegistered`, which propagates
through `Runner.Evaluate` as an error, which the dispatcher wraps as
`ErrDispatcherClauseEval` and aborts the pass.

**Tomorrow** (the analyst-contract):

1. **At grid-load**, missing-binding detection runs once. The grid
   carries declared arrows; each arrow's clauses reference concepts.
   For every clause referencing a `language-bound: true` concept,
   the grid loader MUST verify that `language-bindings` declares the
   `<concept>.<language>` key. If missing, the loader returns an
   error naming the missing binding. **This is the same hard rule as
   `gates.md` §2.1's "if a needed binding is absent at any later
   point, the harness suspends and re-enters initialization."**
2. **At session-engine open**, after grid-load + register-bindings,
   the runtime invariant is "every dispatchable arrow's clauses
   resolve to a registered evaluator". If the invariant fails, the
   session refuses to enter REPL with an error pointing to the
   missing binding (operator MUST re-run `ghyll init`).
3. **At dispatch-time**, the dispatcher's pre-dispatch check
   confirms each clause's concept (and language, where applicable)
   resolves. A miss here is a crash (the grid-load check should have
   caught it) — surfaces as `ErrDispatcherClauseEval` wrapping
   `ErrConceptNotRegistered` to the operator.

Per `gates.md` §2.1 D18 the harness MUST suspend rather than
silently route around a missing binding. The "fall through to
`compiles.<unknown> = false`" failure mode is **forbidden**.

### Acceptance

1. A grid that declares `language-bindings: { compiles.go: "go build
   ./..." }` and has at least one arrow with a `compiles` clause
   targeting Go dispatches that clause through a `BindingEvaluator`
   whose `Command` is `go build ./...`.
2. A grid that declares an arrow with a `compiles` clause but no
   matching `language-bindings` entry refuses to start the session
   (loads with a clear error).
3. A grid that declares a binding for an unknown concept (e.g.,
   `language-bindings: { foo.go: ... }`) refuses to load with a
   sentinel error pointing to the invalid key.
4. After a successful amendment drain that changes
   `language-bindings`, the next dispatch sees the updated binding
   (or refuses, if the new grid's bindings don't cover the live
   arrows).
5. A binding subprocess running over `DefaultBindingTimeout` is
   killed via SIGTERM → grace → SIGKILL; the run record carries
   `ReasonTimeout` and the operator sees a `fail` clause without
   the session hanging.
6. A binding that emits secrets in stdout / stderr has them redacted
   in the persisted EvaluationRun (`secretRedactRE`).

---

## Cross-cutting

### Test coverage requirements (what the implementer must add)

| Surface | Test type | Notes |
|---|---|---|
| Dispatcher → adversarial cycle on a depth-sensitive arrow | unit | mock `OpenSweep` raises one finding; assert one `Adversary.Attack` per round; assert exactly one `RemediationReport` per pass |
| Dispatcher → no adversarial cycle on a depth-robust-only arrow | unit | assert zero `Adversary` constructions |
| Loop-bomb detection | unit | producer callback returns identical artifact twice; assert `ErrProducerLoopBomb` flows to `Outcome=escalated-hook-error` |
| Producer fix that progresses | unit | findings transition to `resolved`; assert `Outcome=converged` |
| Amendment enqueue on integrator-pass close | unit | integrator pass with `missing-cross-context-spec` finding emits one enqueue |
| Amendment drain via slash command | unit | operator invokes the drain verb; queue length drops; grid version bumps |
| Amendment drain aborts in-flight passes on SourceArrow | unit | concurrent in-flight pass on SourceArrow ends with `state=aborted, reason ~= "amendment ... drained"` |
| Amendment drain global-lock serialization | race-detector test | two concurrent drains; no interleaved version bump |
| Drained-ID dedup across session restart | integration | enqueue → drain → restart → re-enqueue same ID refused |
| Language-binding registration at session-engine open | unit | grid declares `compiles.go`; registry returns the binding |
| Grid-load refuses missing binding | unit | grid with `compiles` clause but no `compiles.go` entry → load error |
| Grid-load refuses unknown-concept binding | unit | grid with `foo.go: ...` → load error |
| Binding re-registration after amendment | unit | drain changes bindings; new dispatch picks up new command |
| Binding timeout enforces SIGTERM → SIGKILL | unit | already covered by `subprocess_test.go`; ensure the production path uses the same options |
| Binding env allowlist | unit | secret env vars in parent process do not reach the subprocess (already covered; ensure production registration uses `defaultEnvAllowlist`) |

### BDD acceptance scenarios that must pass after the implementer is done

Per the BDD shape already used in `specs/features/`:

```gherkin
Feature: Adversarial cycle runs in production

  Scenario: Depth-sensitive arrow runs adversarial then verification
    Given a project grid declaring arrow A1 with a depth-sensitive clause
    And the operator runs "/run-arrow A1"
    When the dispatcher opens the pass
    Then the adversary attacks before the first verification clause evaluates
    And the pass result carries a remediation outcome
    And the arrow status derives from verification of the post-remediation clause set

  Scenario: Depth-robust arrow skips adversarial cycle
    Given a project grid declaring arrow A2 with only depth-robust clauses
    When "/run-arrow A2" runs
    Then no Adversary is constructed
    And the arrow status derives directly from clause verification

  Scenario: Loop-bomb producer aborts cycle
    Given an arrow whose producer returns an unchanged artifact across rounds
    When the remediation loop runs
    Then the loop aborts with reason "producer-loop-bomb"
    And the pass status is "aborted"

Feature: Amendment drain mutates the live grid

  Scenario: Integrator finding enqueues an amendment
    Given an integrator pass closes with one open missing-cross-context-spec finding
    Then the amendment queue length is 1

  Scenario: Operator drains the queue
    Given the queue length is 1
    When the operator invokes the drain verb
    Then the grid version increments by 1
    And the queue length drops to 0
    And any in-flight pass on the source arrow is aborted with reason matching "amendment drained"

  Scenario: Two drains serialize
    Given two amendments in the queue
    When the operator invokes drain
    Then the grid version increments twice
    And the audit log carries two separate amendment-drained events

Feature: Language-binding evaluators run in production

  Scenario: compiles.go runs via the declared binding
    Given a grid declaring "language-bindings: { compiles.go: 'go build ./...' }"
    And an arrow with a "compiles" clause targeting "go"
    When the operator runs "/run-arrow"
    Then the binding evaluator executes "go build ./..."
    And the clause status reflects the binding's pass / fail result

  Scenario: Missing binding refuses to start
    Given a grid declaring an arrow with a "compiles" clause but no "compiles.go" binding
    When ghyll opens a session
    Then the session refuses to enter REPL
    And the error names the missing "compiles.go" binding

  Scenario: Amendment re-registers bindings
    Given a session with "compiles.go: 'go build'"
    When an amendment drain replaces the binding with "go build -race"
    Then the next dispatch uses "go build -race"
```

---

## References

- `gates.md` §1.1 (synthetic role-ids), §2.1 D18 (language bindings
  project-declared), §3.7 (integrator→analyst amendment cycle),
  §5.1–5.2 (catalogue + universal base), §6 (depth type), §7.1
  (clause lifecycle), §7.1a (pass identity), §7.2 (arrow lifecycle +
  invalidation), §7.3 (finding lifecycle), §8 (routing rule), §11
  (arrow phases — the adversarial cycle).
- ADR-009: three locks, three owners.
- ADR-010: versioned grid files (immutable; bump on amendment).
- ADR-011: init auto-propose (language bindings declared at init).
- ADR-012: amendment serialization (FIFO + global lock).
- ADR-013: `tests-pass` added to the catalogue.
- `specs/architecture/components/adversarial.md` — full adversarial
  component design.
- `specs/architecture/components/amendment.md` — full amendment
  component design.
- `specs/architecture/components/concepts.md` — per-concept specs.
- `gates/concepts/README.md` — canonical universal vs. language-bound
  classification of the 18 concepts.
- `specs/v4/code-eval-2026-05-25.md` — the integrator pass that
  surfaced these three gaps.
