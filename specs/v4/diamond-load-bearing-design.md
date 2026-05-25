# Load-bearing wiring — architect design
## Date: 2026-05-25

## Frame

This document is the architect's response to
`specs/v4/diamond-load-bearing-spec.md`. The analyst spec defines the
contract three load-bearing surfaces must satisfy in production; this
document picks the call sites, signatures, registry keys, lock
discipline, event payloads, and test surfaces the implementer will
follow.

Substrate read alongside the analyst spec:
- `runner/dispatcher.go` (production caller of `Runner.Evaluate`)
- `runner/runner.go`, `runner/notodo.go:402` (`RegisterBuiltins`)
- `runner/subprocess.go` (`BindingEvaluator`, `NewBindingEvaluator`)
- `runner/amendment.go`, `runner/amendment_commit.go`
- `runner/orchestrator.go`, `runner/adversarial.go`,
  `runner/remediation.go`, `runner/producer_fix.go`
- `runner/operatorbus.go`
- `cmd/ghyll/session_engine.go` (`openEngine`, `dispatcher()`,
  `RunArrow`)
- `cmd/ghyll/session.go` (REPL slash-dispatch +
  `DrainModalPending`)
- `cmd/ghyll/run_arrow_cmd.go` (per-call event subscription pattern)
- `bootstrap/grid.go`, `bootstrap/bindings.go`
- `gates/concepts/README.md` + `gates/concepts/*.yaml`

ADRs depended on:
- ADR-005: catalogue concepts + closed vocabulary
- ADR-006: per-concept YAML schema shape
- ADR-008: one pass-id per arrow per traversal
- ADR-009: three locks, three owners
- ADR-010: versioned grid files immutable; bump on amendment
- ADR-011: init flow, depth ladder, language bindings at init
- ADR-013: `tests-pass` language-bound concept added
- ADR-014: Adversarial Orchestrator (per-round fresh adversary,
  loop-bomb interlock)
- ADR-015: pass persistence + JSONL source of truth
- ADR-016: Tier-2 modal surface; bus is shared driver substrate
- V2-ADR-012: amendment FIFO + project-wide grid write-lock

---

## Dependency order

Land in this order. Each step's tests must be green before the next
step's first edit lands.

1. **Gap 3 (binding evaluation)** — must land first. Gap 1 needs a
   real evaluator for the depth-sensitive clause set the
   adversarial cycle actually attacks (`compiles`, `lint-clean`,
   `tests-pass`, `no-orphan-symbol`, `every-step-bound`,
   `mutation-score`, `acyclic-dependency-graph`). Without bindings,
   gap 1's tests would have to mock through `Runner.Evaluate` and
   would fail to exercise the production wire.
2. **Gap 4 (4 missing universal concepts)** — alongside gap 3 in the
   same PR. The four added universals share the
   `RegisterBuiltins` registration site touched by gap 3; landing
   them together avoids two churns to `notodo.go:402`. Decision
   below: option (a) — implement as Go evaluators.
3. **Gap 1 (adversarial cycle)** — depends on gaps 3+4 so the
   dispatcher's cycle dispatches against a fully-populated registry.
   Lands second because it introduces a new dispatcher phase
   (ADR-worthy structural change — flagged below).
4. **Gap 2 (amendment drain)** — independent of gaps 1/3/4. Can
   land in parallel with gap 1, but lefthook can only stamp one
   commit at a time, so we land it after gap 1 to keep the
   adversarial-cycle PR small and easy to revert if the new phase
   needs rework.

---

## Gap 3: Language-binding evaluation wiring

### Registry key shape (chosen + rationale)

**Decision: `<concept>.<language>` flat registry key** (matches the
YAML key in `grid.LanguageBindings` verbatim). Per-clause language
lookup is the alternative; the flat key wins for four reasons:

1. **Round-trip fidelity** — `grid.LanguageBindings` is already
   keyed `<concept>.<language>`; the registry mirrors that
   verbatim. `bootstrap/bindings.go:BindingKey.String()` is the
   canonical encoder for this form. No new normalization layer is
   introduced; the binding-load path becomes one `for k, v := range
   grid.LanguageBindings` loop with no key surgery.
2. **Failure-locality** — at dispatch time, a clause carrying
   `concept=compiles, args.language=go` resolves with a single
   `reg.Lookup("compiles.go")` call. A miss is one error message
   (`runner-concept-not-registered: compiles.go`) that names exactly
   the binding the operator must declare. With per-clause lookup
   the failure mode is "compiles registered, but go variant
   missing" — two levels of indirection for the operator to walk.
3. **Generation discipline** — `Registry.Replace` bumps the
   `EvaluatorIdentity.Generation` per concept; with the flat key,
   replacing `compiles.go` after an amendment drain bumps only
   that key's generation, leaving `compiles.rust` untouched.
   Per-clause lookup would either bump a single shared generation
   on every binding amendment (over-broad) or grow a
   per-language nested registry.
4. **No new method on `Registry`** — the existing `Register` /
   `Replace` / `Lookup` surface accepts the flat key today. Gap 3
   doesn't expand the runner's public API; it adds a helper +
   wire-up. The runtime invariant ("every dispatchable clause's
   concept-key resolves") is checkable by the dispatcher's existing
   `Registry.Lookup` path.

The clause's `Args["language"]` carries the per-clause language. The
dispatcher constructs the lookup key from `clause.Concept +
"." + clause.Args["language"].(string)` at dispatch time. For
language-unbound concepts (`Args["language"]` absent), the lookup
key is `clause.Concept` (verbatim). A new helper
`runner.ConceptRegistryKey(c Clause) string` encapsulates this so the
dispatcher and grid-validation paths agree on key shape.

### Files touched

| File | Function / line | Change |
|---|---|---|
| `runner/runner.go` | new — top of file, near `Clause` | Add `ConceptRegistryKey(c Clause) string` helper. Returns `c.Concept` if the concept is universal-base (lookup table — see below), else `c.Concept + "." + c.Args["language"]`. |
| `runner/runner.go` | `Evaluate` (~line 476) | Replace `r.Registry.Lookup(c.Concept)` with `r.Registry.Lookup(ConceptRegistryKey(c))`. Backwards-compatible because universal concepts continue to resolve under their bare names. |
| `runner/notodo.go` | `RegisterBuiltins` (line 402) | No change here for gap 3; the four new universals land via gap 4. |
| `runner/notodo.go` | new — alongside `RegisterBuiltins` | Add `IsUniversalConcept(concept string) bool` (the table consulted by `ConceptRegistryKey`). One source of truth for "this concept does NOT take a language suffix." Initialized from `gates/concepts/*.yaml` shape — hand-maintained list of 11 universals (7 already in `RegisterBuiltins` + 4 added by gap 4). |
| `runner/bindings_register.go` | new file | `RegisterGridBindings(reg *Registry, grid *bootstrap.Grid, workdir string) error` — iterates `grid.LanguageBindings`, validates each key parses to `(concept, language)`, validates `concept` is a language-bound catalogue concept, calls `NewBindingEvaluator(command, WithWorkingDir(workdir), WithTimeout(DefaultBindingTimeout), …)`, calls `reg.Register("<concept>.<language>", evaluator)` (falling back to `reg.Replace` if generation needs bumping post-amendment). Returns the first error encountered; the session refuses to enter REPL on any error. |
| `runner/bindings_register.go` | new file | `LanguageBoundConcepts() map[string]struct{}` — hand-maintained set of the seven language-bound concepts per `gates/concepts/README.md`. Used by `RegisterGridBindings` to reject `language-bindings: { foo.go: ... }` keys whose concept-part is not language-bound. |
| `runner/bindings_register.go` | new file | `RequiredBindings(grid *runner.Grid) []bootstrap.BindingKey` — walks every arrow's clauses; for each clause whose concept is language-bound, extracts `Args["language"]` and emits a `BindingKey{Concept, Language}`. Returns the deduplicated set. Used by `RegisterGridBindings` to call `bootstrap.Grid.CheckRequiredBindings` BEFORE registering — catches missing bindings before any evaluator is constructed. |
| `bootstrap/bindings.go` | `DeclareBinding` (line 96) | No change. `normalizeBindingTriple` already rejects `.` in concept/language so the registry key is unambiguous. |
| `bootstrap/grid.go` | `Read` / `ReadVersion` | No change here for the load path; binding validation lives in `RegisterGridBindings` (runner-side, called by `openEngine`). |
| `cmd/ghyll/session_engine.go` | `openEngineWithOptions` (line 146) | After `runner.RegisterBuiltins(reg)`, call `runner.RegisterGridBindings(reg, gridFile, workdir)`. `gridFile` is the `*bootstrap.Grid` already loaded by `session.go:318` (lift that load above `openEngineWithOptions` and pass it as an arg, or re-load inside — see "Construction order" below). On error, close `store` and return — the session refuses to enter REPL with `fmt.Errorf("engine open: language bindings: %w", err)`. |
| `cmd/ghyll/session.go` | `initEngine` (~line 301) | Pass the `*bootstrap.Grid` loaded at line 318 into `openEngineWithOptions` (signature change: add `grid *bootstrap.Grid` parameter). If `RegisterGridBindings` errors, `s.output` an operator-facing message naming the missing binding(s) and return without setting `s.engine` (matches current "continuing without persistence" pattern but with a stronger message: `✗ session refuses: <reason>; run ghyll init`). |
| `runner/amendment_commit.go` | `Commit` (line 97) | After step 2 (grid append) and BEFORE step 4 (bus publish), call a new `BindingsReRegisterFn` callback if set. The callback re-walks `grid.LanguageBindings` and calls `Registry.Replace` for every changed key. Wired via a new exported field `AmendmentCommitter.BindingsReRegister func() error`. Failure surfaces as `partial-append-error` in the `OpEventAmendmentDrained` event and a wrapped error from `Commit` (operator visibility — see Gap 2). |
| `cmd/ghyll/session_engine.go` | `attachJournal` or a sibling | Wire `committer.BindingsReRegister = func() error { return runner.RegisterGridBindings(reg, rt.gridFile, rt.workdir) }` so amendments that change bindings re-register them automatically. |

### Construction order at session open

In `cmd/ghyll/session.go:initEngine` (today's call site):

```text
1. bootstrap.Read(s.workdir)                          // returns *bootstrap.Grid (or nil + err)
2. openEngineWithOptions(s.workdir, log, ibMax, grid) // signature change
   2a. store, runner.NewRegistry, runner.RegisterBuiltins(reg)   // existing
   2b. runner.RegisterGridBindings(reg, grid, workdir)            // NEW
       2b-i. validate every grid.LanguageBindings key parses + concept is language-bound
       2b-ii. validate RequiredBindings(grid.runtime) ⊆ keys      // missing-binding detection
       2b-iii. for each (key, command) in grid.LanguageBindings:
                 evaluator := runner.NewBindingEvaluator(command,
                                WithWorkingDir(workdir),
                                WithTimeout(runner.DefaultBindingTimeout),
                                WithMaxOutputBytes(runner.DefaultBindingMaxOutputBytes),
                                WithGrace(runner.DefaultBindingGrace))
                 reg.Register(key, evaluator)         // Register on first session;
                                                      // Replace on amendment-driven re-register.
3. replayEngine(ctx)                                  // existing
4. attachJournal(log)                                 // existing
5. wire committer.BindingsReRegister                  // NEW (gap 2 substrate also lives here)
```

For the missing-binding detection at 2b-ii to find the live arrows,
the runner's `Grid` (`rt.grid`, populated by Replay) must be loaded
BEFORE the registration step. Two viable orderings:

**A. Pre-replay binding-key validation only.** At 2b-ii validate
that every key in `grid.LanguageBindings` is structurally valid
(`concept.language` form, concept is in `LanguageBoundConcepts()`).
Defer the per-arrow `RequiredBindings` check until after Replay, in
`attachJournal` or a new sibling helper `verifyBindingsCoverage(rt)`.

**B. Pre-replay full validation by re-reading the persistent grid
file's `arrows:` block.** `bootstrap.GridFile.Arrows` already holds
the declared arrows (untyped shape; `[]map[string]any`). A new
helper `bootstrap.RequiredBindingKeys(g *Grid) []BindingKey` walks
the raw arrows and emits the needed binding keys.

**Chosen: A.** Reason: option B duplicates the arrow parsing the
runner does at Replay (the runner's `Grid` is the canonical
in-memory shape; the bootstrap `Grid.Arrows` is an untyped pass-
through). One parser. The post-replay coverage check is a single
function that fails fast (returns the same `*MissingBindingError`)
before the dispatcher accepts its first `/run-arrow`. Failure at
this point closes the engine and refuses to enter REPL.

### Validation rules enforced at grid-load

`RegisterGridBindings` enforces:

1. Each key matches `<concept>.<language>` (split on the last `.`;
   reject if concept-or-language is empty).
2. Concept-part is one of the 7 language-bound concepts from
   `LanguageBoundConcepts()` (returns
   `ErrBindingConceptInvalid` wrapped with the offending key).
3. Command is non-empty after trim (matches `BindingEvaluator`'s
   `ReasonSpawnFailed` path — would surface at dispatch time
   anyway; better to surface at load).
4. No duplicate keys (YAML decoding already rejects this; the
   runner adds an explicit `_, exists := reg.by[key]` check before
   Register, returning `ErrConceptAlreadyRegistered` wrapped with
   the duplicate key for clarity).

Errors are wrapped with a new sentinel
`runner.ErrLanguageBindingInvalid` so callers (and the BDD step
file) can match without depending on the concrete `*MissingBindingError`
or `*BindingKeyError` types.

### Failure modes

| Failure | State left | Cleanup |
|---|---|---|
| Invalid binding key (`foo.go: ...`) | `store` opened, `reg` partially populated with builtins | `store.Close()`, return error from `openEngine`; `initEngine` surfaces via `s.output`; `s.engine` stays nil — session continues without persistence (matches existing pattern but with a directive: "run `ghyll init`"). |
| Missing required binding (clause references `compiles` but no `compiles.go`) | `store` opened, `reg` populated with valid bindings registered so far | Same as above — `RegisterGridBindings` returns `*MissingBindingError` (already a `bootstrap` type; the runner re-exports via alias for the BDD step file). Operator runs `ghyll init` to declare. |
| BindingEvaluator construction non-fatal (commands aren't validated at constructor — they're shell strings) | n/a | `NewBindingEvaluator` doesn't error today; the error path lives inside `Evaluate`'s spawn step. No new failure surface introduced at registration. |
| Amendment drain changes binding key, re-registration fails (e.g., command rejected by a future validator) | Grid version bumped, passes aborted (per Gap 2's "pass-abort precedes grid-append" contract) | `Commit` returns wrapped error; the `OpEventAmendmentDrained` event carries `status=binding-re-register-error`. Operator sees the partial state; the next `/run-arrow` re-attempts. |

### OpEvent kinds fired

No new `OpEventKind` introduced for gap 3. The existing
`OpEventAmendmentDrained` event (Gap 2 substrate) gains a `status`
discriminator that may now read `binding-re-register-error`. Adding
this status value is wire-stable (the field is free-text Detail
today, not a typed enum); no migration needed.

### Tests

| Test name | Package | Asserts |
|---|---|---|
| `TestScenario_RegisterGridBindings_RegistersCompilesGo` | `runner` | Grid with `{"compiles.go": "go build ./..."}` produces a registry lookup for `compiles.go` that returns a non-nil evaluator. |
| `TestScenario_RegisterGridBindings_RejectsInvalidKey` | `runner` | Grid with `{"foo.go": "x"}` returns `ErrLanguageBindingInvalid` wrapping the key. |
| `TestScenario_RegisterGridBindings_RejectsMalformedKey` | `runner` | Grid with `{"compiles": "x"}` (no language) returns `ErrLanguageBindingInvalid`. |
| `TestScenario_RegisterGridBindings_RejectsEmptyCommand` | `runner` | Grid with `{"compiles.go": "   "}` returns `ErrBindingCommandEmpty`. |
| `TestScenario_RegisterGridBindings_MissingBindingForArrowClause` | `runner` | Grid declares arrow with `compiles` clause targeting `go`; `LanguageBindings` empty → `*MissingBindingError` listing `compiles.go`. |
| `TestScenario_RegisterGridBindings_AllRequiredBindingsPresent` | `runner` | Arrow + binding both present → nil error. |
| `TestScenario_RegisterGridBindings_AmendmentReRegisters` | `runner` | `AmendmentCommitter.Commit` with `BindingsReRegister` wired and amendment that changes `compiles.go` command → after Commit, registry lookup returns evaluator whose Command is the new value. Generation incremented. |
| `TestScenario_ConceptRegistryKey_UniversalReturnsBare` | `runner` | `ConceptRegistryKey(Clause{Concept:"no-todo-marker"})` returns `"no-todo-marker"` (no language suffix). |
| `TestScenario_ConceptRegistryKey_LanguageBoundReturnsCompound` | `runner` | `ConceptRegistryKey(Clause{Concept:"compiles", Args:{"language":"go"}})` returns `"compiles.go"`. |
| `TestScenario_ConceptRegistryKey_LanguageBoundMissingLanguageArg` | `runner` | `ConceptRegistryKey(Clause{Concept:"compiles", Args:{}})` returns `"compiles."` — the empty-language form. Dispatch then surfaces `ErrConceptNotRegistered: compiles.` so operator sees the gap. |
| `TestScenario_Session_BindingsCoverage_RefusesREPL` | `cmd/ghyll` | Fresh project with a `compiles` arrow and no bindings → `initEngine` does NOT set `s.engine`; output names the missing binding. |
| `TestScenario_Session_BindingsCoverage_StartsWhenComplete` | `cmd/ghyll` | Project with binding declared → `s.engine` non-nil; `/list-arrows` returns the arrow. |

### BDD scenarios

`tests/acceptance/steps_bindings.go` already exists. Add scenarios
under a new feature file `specs/features/binding-evaluation.feature`:

```gherkin
Feature: Language bindings register at session open

  Scenario: compiles.go evaluator runs via declared binding
    Given a project grid with arrow A1 declaring a compiles clause targeting go
    And the grid declares language-bindings "compiles.go" → "go build ./..."
    When the operator runs "/run-arrow A1"
    Then the binding evaluator spawns "go build ./..."
    And the clause status reflects the binding's pass/fail outcome

  Scenario: Missing compiles.go binding refuses to start
    Given a project grid with arrow A1 declaring a compiles clause targeting go
    And the grid declares no language-bindings for compiles.go
    When ghyll opens a session in the project
    Then the session refuses to enter REPL
    And the error mentions "compiles.go" as a missing binding

  Scenario: Unknown-concept binding refuses to load
    Given a project grid declaring language-bindings "foo.go" → "x"
    When ghyll opens a session
    Then the session refuses to enter REPL
    And the error names "foo.go" as an invalid binding key

  Scenario: Amendment re-registers updated binding
    Given a session with language-bindings "compiles.go" → "go build"
    And an amendment that drains with new grid binding "compiles.go" → "go build -race"
    When the amendment commits
    Then a subsequent /run-arrow on a compiles clause uses "go build -race"
    And the evaluator generation for "compiles.go" incremented
```

### ADR-worthy? Yes — flag for implementer

The registry-key shape (`<concept>.<language>` flat) is a structural
choice the runner will live with. Recommend the implementer drafts
**ADR-v4-001** (one paragraph) recording:
- decision: `<concept>.<language>` flat registry keys; per-clause
  `Args["language"]` is the dispatch-time language source;
- alternatives considered: per-clause language lookup, nested
  registry, dynamic Evaluator factory per concept;
- consequence: a clause without `Args["language"]` resolves to
  `concept.` and fails at dispatch — fail-loud, not silent
  fallback.

---

## Gap 4: 4 missing universal concepts

### Decision: option (a) — implement the 4 missing universals as Go evaluators

Three options were on the table per the analyst spec; option (a) is
the right one. Reasoning:

- **Catalogue-truth alignment.** `gates/concepts/README.md` marks
  `unique-definition`, `predicate-form`,
  `mode-determinable-from-repo`, `single-active-role-instance` as
  `language-bound: false`. Per ADR-005 the catalogue is the closed
  vocabulary. Option (b) (move to language-bound) would amend the
  catalogue — a wider decision than this gap.
- **Operator-friction.** Option (c) (split as follow-up) leaves
  arrows referencing these concepts crashing with
  `ErrConceptNotRegistered` until the follow-up ships. Each of the
  four has at least one concrete arrow in the diamond-role contracts
  (`single-active-role-instance` is referenced by every role
  contract per `gates.md` §1.1 and concretely by the dispatcher's
  lock table; `unique-definition` is the bootstrap registry's
  invariant; `predicate-form` and `mode-determinable-from-repo`
  surface in the analyst's clauses).
- **Implementation cost.** All four are pure file-walk +
  pattern-match logic — same shape as the existing 7 in
  `notodo.go`. Each fits in ~80–150 LOC.

### Files touched

| File | Function | Purpose |
|---|---|---|
| `runner/uniquedef.go` (new) | `EvaluateUniqueDefinition(ctx, c Clause) (*Result, error)` | Walk `Args["scope"]` (path-glob), parse `Args["field-locator-rule"]`, collect values of `Args["field"]`, detect duplicates honoring `Args["case-sensitive"]`. Returns `Unevaluated` with `Reason=no-rule-selectable-locations` if the rule matches nothing (matches `every-step-bound` pattern). |
| `runner/predicateform.go` (new) | `EvaluatePredicateForm(ctx, c Clause) (*Result, error)` | Walk `Args["scope"]`, parse `Args["collection-locator"]`, validate each entry against `Args["predicate-grammar"]` (default: contains a comparison operator OR `assert(...)` form). |
| `runner/moderepostate.go` (new) | `EvaluateModeDeterminableFromRepo(ctx, c Clause) (*Result, error)` | Reads a project-declared discriminator file (`.ghyll/mode.yaml` or operator-supplied `Args["mode-discriminator-path"]`) and asserts the discriminator value is one of the declared enum values. |
| `runner/singleactiverole.go` (new) | `EvaluateSingleActiveRoleInstance(ctx, c Clause) (*Result, error)` | Reads from `Args["passes-registry"]` (injected by the dispatcher — see below) OR returns `Unevaluated` if the registry handle is absent. The evaluator counts open passes matching `(role, context)` from `Args`; pass iff count ≤ 1. |
| `runner/notodo.go` | `RegisterBuiltins` (line 402) | Add four `registerOrReplace(r, "<concept>", Evaluate<X>)` lines, one per evaluator. Order matters only for log readability; alphabetize. |
| `runner/notodo.go` | new helper `IsUniversalConcept(string) bool` (shared with gap 3) | Returns true for the 11 universals: the 7 existing + the 4 added by gap 4. Hand-maintained list; the closed-vocabulary nature (per ADR-005) makes this acceptable. Mirrors `LanguageBoundConcepts`. |

`EvaluateSingleActiveRoleInstance` is the awkward one — it needs
access to the live `PassRegistry`. Two options:

- **A. Plumb via Clause.Args** — the dispatcher injects
  `c.Args["_internal_passes"] = passes` before calling `Evaluate`.
  Type-asserts inside the evaluator; under-table-cloth, but minimal
  surface change.
- **B. Plumb via a runner-side dependency-injection seam** — add
  `Runner.WithPassRegistry(*PassRegistry) *Runner` and expose
  `Runner.Passes()` to the evaluator. The evaluator pulls from the
  runner via a context value or a runner-bound closure.

**Chosen: B.** Reason: under-table-cloth keys (option A) break the
`Args` map's serialization contract (it's persisted on
`EvaluationRun` via `snapshotDetails`). A `*PassRegistry` is not
serializable and would either crash JSONL encoding or require a
filter that masks `_internal_*` keys (more friction than the
explicit seam). The new `Runner.passes` field is unexported; the
evaluator accesses it via a private package-internal helper
`runner.passRegistryFromRunner(r *Runner) *PassRegistry` (lives
beside `EvaluateSingleActiveRoleInstance` for locality).

For this to work, `EvaluateSingleActiveRoleInstance` needs the
runner pointer. Today's `Evaluator` signature is
`func(ctx context.Context, c Clause) (*Result, error)` — no runner.
Adding a `Runner` arg is a public-API change that ripples through
every existing evaluator. Instead: `Runner.Evaluate` (`runner.go:469`)
stamps `c.Args["__runner_pass_registry"]` from `r.passes` JUST FOR
the `single-active-role-instance` concept (a sole exception
documented at the call site), then strips it from
`snapshotDetails`'s output before persistence (a one-key blocklist
in `snapshotDetails`). Ugly, but contained.

**Alternative if the implementer disagrees:** add a new
`EvaluatorWithRunner` signature variant + a `RegisterWithRunner`
method on `Registry`. The four extra registrations use it; the
existing seven stay on the simpler signature. This is cleaner and
public-API-stable (additive). Implementer's call; either is
defensible. Flag the choice in the PR description so reviewers see
the trade-off.

### Tests

| Test name | Package | Asserts |
|---|---|---|
| `TestScenario_UniqueDefinition_NoDuplicates` | `runner` | Fixture with unique values → Pass=true. |
| `TestScenario_UniqueDefinition_DuplicatesDetected` | `runner` | Fixture with one duplicate → Pass=false, `Details["duplicates"]` lists the value + locations. |
| `TestScenario_UniqueDefinition_CaseInsensitive` | `runner` | `case-sensitive: false` + "FOO"/"foo" duplicates → Pass=false. |
| `TestScenario_UniqueDefinition_NoLocator_Unevaluated` | `runner` | Locator matches nothing → `Unevaluated` with reason. |
| `TestScenario_PredicateForm_AssertablePredicates` | `runner` | All entries contain comparison ops → Pass=true. |
| `TestScenario_PredicateForm_ProseEntry` | `runner` | One prose entry ("uptime is critical") → Pass=false, listed in `Details["non-predicates"]`. |
| `TestScenario_ModeDeterminableFromRepo_ValidEnum` | `runner` | `.ghyll/mode.yaml: greenfield` matches declared enum → Pass=true. |
| `TestScenario_ModeDeterminableFromRepo_MissingFile` | `runner` | No mode file → Pass=false; reason mentions the missing path. |
| `TestScenario_SingleActiveRoleInstance_NoConflict` | `runner` | PassRegistry has 0 open passes on `(analyst, ctx-A)` → Pass=true. |
| `TestScenario_SingleActiveRoleInstance_ConflictDetected` | `runner` | PassRegistry has 2 open passes on the same tuple → Pass=false, `Details["conflicting-pass-ids"]` lists both. |
| `TestScenario_RegisterBuiltins_RegistersAll11Universals` | `runner` | After `RegisterBuiltins`, `Registry.Count() == 11`. |

BDD additions go to `specs/features/universal-base.feature` (new),
one scenario per concept; the implementer can copy-shape from
`specs/features/no-todo-marker.feature` if it exists or model on
`tests/acceptance/steps_runner.go`'s no-todo-marker pattern.

---

## Gap 1: Adversarial cycle wiring

### Files touched

| File | Function / line | Change |
|---|---|---|
| `runner/dispatcher.go` | `PassDispatcher` struct (line 61) | Add fields: `AdversaryFactory func(round int) *Adversary` (per ADR-014's per-round fresh adversary), `OpenSweep OpenSweepFn`, `Classify DepthClassifyFn`, `ProducerFix ProducerFn` (the harness-wrapped fix function), `RemediationConfigDefaults RemediationConfig` (carries `RoundsMax`, `MaxFixErrors`, `SeverityThreshold`, `CountUnevaluatedAsOpen` from the grid). All optional — nil triggers the refusal behavior below. |
| `runner/dispatcher.go` | `PassDispatcher` struct | Add `FindingsStore *FindingsStore`, `ClassificationsStore *ClassificationsStore` — required for `Adversary` construction; non-nil only when the adversarial cycle is wired. |
| `runner/dispatcher.go` | `Dispatch` (line 184) | After `OpenPass` succeeds and BEFORE the `for i, clause := range req.Arrow.Clauses` verification loop, insert an "adversarial phase" branch. New helper `runDispatcherAdversarialPhase(ctx, req, pass, passID) (*RemediationReport, error)` returns nil (skip cycle) for depth-robust-only arrows OR for the synthetic `init` / `adversary` role-ids; returns a `*RemediationReport` otherwise. |
| `runner/dispatcher.go` | new — `runDispatcherAdversarialPhase` | Partitions clauses (depth-sensitive vs depth-robust). If sensitive set is empty, return `(nil, nil)`. If `req.Role == "init"` or `req.Role == "adversary"`, return `(nil, nil)`. If `d.AdversaryFactory == nil` OR `d.OpenSweep == nil` OR `d.Classify == nil` OR `d.ProducerFix == nil`, abort the pass with `reason: adversarial-hooks-not-wired` and return `ErrAdversaryHooksNotWired`. Otherwise drive `RunRemediationLoop` (already in `runner/remediation.go`) with the partitioned clauses. |
| `runner/dispatcher.go` | new error sentinel | `ErrAdversaryHooksNotWired = errors.New("dispatcher: adversarial hooks not wired (refusing depth-sensitive arrow)")`. |
| `runner/dispatcher.go` | `Dispatch` | After the cycle returns, switch on `report.Outcome`. On non-converged outcomes, abort the pass with the analyst-spec-mandated reason mapping (`remediation-rounds-max`, `remediation-no-progress`, `producer-loop-bomb` / `producer-fix-error`, `context-cancelled`). On converged outcomes (`converged` / `converged-with-unevaluated`), proceed to verification — but the verification clause set is the input clauses PLUS `VerificationAutoInsert(arrowID, clauses)` from `remediation.go:300`. |
| `runner/dispatcher.go` | `Dispatch` | After verification (today's path), call `pass.Close(...)` as today, but if the remediation report was non-converged, the pass is already aborted; skip verification (early return). |
| `cmd/ghyll/session_engine.go` | `dispatcher()` (line 507) | Wire the new fields: `AdversaryFactory: r.adversaryFactory`, `OpenSweep: r.openSweepHook`, `Classify: r.classifyHook`, `ProducerFix: r.producerFixHook`, `RemediationConfigDefaults: r.remediationConfig()`, `FindingsStore: r.findings`, `ClassificationsStore: r.classifications`. |
| `cmd/ghyll/session_engine.go` | new method `(*engineRuntime).adversaryFactory(round int) *runner.Adversary` | Constructs a fresh Adversary per ADR-014; non-nil only when the operator has opted in via the new operator-attestation surface (see below). |
| `cmd/ghyll/session_engine.go` | new method `(*engineRuntime).openSweepHook`, `.classifyHook`, `.producerFixHook` | Concrete hook implementations. `openSweepHook` opens the model dialect at the arrow's tier, prompts for adversary findings, parses the response into `[]FindingRecord`. `classifyHook` similarly. `producerFixHook` drives the producer role (whoever owns `req.Arrow.SourceRole`) to address findings. |
| `cmd/ghyll/session_engine.go` | new field `adversaryHooks *adversaryHooks` | Bundles the three hooks behind a single nil-guard. nil = "default no-op", which the dispatcher refuses (per analyst spec). |
| `cmd/ghyll/session.go` | new slash command `/adversary <enable|disable|status>` (recommended verb) | Toggles `s.engine.adversaryHooks` between nil and a constructed bundle. Default is `disabled` — fresh sessions refuse depth-sensitive arrows until the operator explicitly enables. This is the "operator override" surface the analyst spec names. |
| `runner/orchestrator.go` | n/a | No change. `AdversarialOrchestrator` is not used directly by the dispatcher — `RunRemediationLoop` is. Orchestrator stays available for sub-activities + tests. |

### Dispatcher integration point

```text
PassDispatcher.Dispatch(ctx, req):
  ...
  pass := OpenPass(...)
  d.Passes.Register(pass)
  defer d.Passes.Unregister(pass.ID())

  // NEW: adversarial cycle (depth-sensitive arrows only).
  remReport, advErr := d.runDispatcherAdversarialPhase(ctx, req, pass, passID)
  if advErr != nil {
    // pass is already aborted inside the helper; surface the err.
    return nil, advErr
  }
  if remReport != nil && !remediationConverged(remReport.Outcome) {
    // pass is already aborted with the analyst-spec reason; build a
    // DispatchResult that reflects the abort + return.
    return &DispatchResult{
      PassID:      passID,
      ArrowStatus: runner.ArrowStatusBlocked, // open findings drive verification verdict
      CloseReason: pass.CloseReason(),
      ClosedAt:    pass.ClosedAt(),
    }, fmt.Errorf("adversarial cycle: %s", remReport.Outcome)
  }

  // Build clauses + auto-insert no-open-finding + every-requirement-meets-min-depth.
  clauses := req.Arrow.Clauses
  if remReport != nil {  // adversarial phase ran → verification needs auto-inserts
    clauses = VerificationAutoInsert(req.Arrow.ID, clauses)
  }

  // existing verification loop (Runner.Evaluate per clause)
  for i, clause := range clauses { ... }
  ...
```

`remediationConverged` is a one-line helper recognizing `converged`
and `converged-with-unevaluated`.

### Default no-op refusal mechanism

The analyst's contract: "The dispatcher MUST refuse to enter the
cycle with default no-op hooks." Two implementations:

- **A.** The dispatcher checks `d.AdversaryFactory == nil ||
  d.OpenSweep == nil || d.Classify == nil || d.ProducerFix == nil`
  and refuses.
- **B.** The dispatcher checks the Adversary's hook identities — if
  `a.OpenSweep` is the literal `noopOpenSweep` function pointer, the
  factory is suspect.

**Chosen: A.** Reason: option B couples the dispatcher to the
adversary's internals and breaks if a future test/diagnostic
Adversary constructs an intentionally no-op hook. Option A is
declarative: "wire all four fields, or you don't get the cycle."

The session-engine starts with all four nil. `/adversary enable`
constructs the hook bundle (using the active model dialect at the
arrow's max tier for `OpenSweep` and `Classify`, and the producer
dialect for `ProducerFix`) and assigns the dispatcher fields. The
operator sees a status line: `✓ adversarial cycle enabled
(open-sweep: <model>, classify: <model>, producer-fix: <model>)`.
`/adversary disable` resets to nil (refusal mode).

### OpEvent kinds fired

The cycle already fires events through `RunRemediationLoop` /
`AdversarialOrchestrator`:

- `OpEventAdversarialRoundStart` — at each round start
- `OpEventProducerFixSignal` — between rounds (and on each
  `ProducerFixHarness.runOneRound` per `producer_fix.go:88`)
- `OpEventRemediationConverged` — on convergence
- `OpEventRemediationEscalated` — on rounds-max

No new event kinds for gap 1. The existing
`OpEventPassClosed` carries the abort reason; the dispatcher's
`pass.Abort(reason)` already publishes through `pass.go`'s observer
chain.

The session's `/run-arrow` handler in `run_arrow_cmd.go:184` only
subscribes to `OpEventPassOpened`, `OpEventPassClosed`,
`OpEventInsufficientBasisRoundsExceeded`. Add the four adversarial
event kinds to its subscriber switch so the operator sees the cycle
inline. Format pattern (mirrors the existing pass-opened line):
`  · adv-round-start  round=N/M arrow=X`.

### Loop-bomb integration

`runner.ProducerFixHarness` already exists. The dispatcher
constructs it inside `runDispatcherAdversarialPhase`:

```go
harness := &runner.ProducerFixHarness{
    Producer: d.ProducerFix,         // operator-wired
    Bus:      d.Bus,
    ArrowID:  req.Arrow.ID,
}
cfg := d.RemediationConfigDefaults
cfg.FixAttempt = func(ctx context.Context, openFindings []FindingRecord) (bool, error) {
    err := harness.ProducerRemediate()(ctx, openFindings)
    if errors.Is(err, ErrProducerLoopBomb) {
        return false, err   // signals madeProgress=false + hook-error budget
    }
    return err == nil, err
}
cfg.AdversaryBuilder = func(round int) *Adversary { ... fresh per round ... }
cfg.AttackBuilder = func(round int) AdversaryAttack { ... }
report, _ := RunRemediationLoop(ctx, cfg)
```

The loop-bomb outcome surfaces as `RemediationEscalatedHookError`
per `remediation.go:215`. The dispatcher then maps that outcome to
abort-reason `producer-loop-bomb` (when `errors.Is(report.FinalErr,
ErrProducerLoopBomb)`) or `producer-fix-error` (other hook errors).

### Operator override slash command

Per the analyst spec ("The operator does NOT have an 'override the
adversarial cycle' verb"), `/adversary` is NOT a per-arrow skip; it
is a session-wide enable/disable for the cycle's wiring. The
analyst's contract — "skipping adversarial scrutiny would be the
magnitude-blind component rating its own magnitude" — is preserved
because the disabled state REFUSES depth-sensitive arrows rather
than letting them through unattacked. The verb:

| Form | Effect |
|---|---|
| `/adversary` | Show current state (enabled/disabled), plus a one-line description of the wired models. |
| `/adversary enable` | Construct the hook bundle from the session's active dialect. Returns acknowledgement or an error (e.g., no operator op-id, no engine). |
| `/adversary disable` | Tear down the bundle; future dispatches refuse depth-sensitive arrows with `adversarial-hooks-not-wired`. |
| `/adversary status` | Alias for the bare form. |

The handler `handleAdversaryCommand` lives in
`cmd/ghyll/adversary_cmd.go` (new file alongside
`run_arrow_cmd.go`). Wired in `session.go:DispatchSlashCommand`
between `/list-arrows` and the `switch line` block.

### Tests

| Test name | Package | Asserts |
|---|---|---|
| `TestScenario_Dispatcher_DepthSensitive_RunsAdversarialPhase` | `runner` | Arrow with one depth-sensitive clause; mock factory + hooks → `Adversary.Attack` invoked once, `RemediationReport.Outcome == converged`. |
| `TestScenario_Dispatcher_DepthRobustOnly_SkipsCycle` | `runner` | Arrow with only depth-robust clauses → zero `Adversary` constructions; verification still runs. |
| `TestScenario_Dispatcher_AdversaryHooksUnwired_RefusesPass` | `runner` | Depth-sensitive arrow + nil hooks → `pass.State() == PassStateAborted` with reason `adversarial-hooks-not-wired`; `Dispatch` returns `ErrAdversaryHooksNotWired`. |
| `TestScenario_Dispatcher_LoopBomb_AbortsWithProducerLoopBomb` | `runner` | Producer returns identical artifact across rounds → `Outcome == escalated-hook-error`; pass aborted with reason containing `producer-loop-bomb`. |
| `TestScenario_Dispatcher_RoundsMax_AbortsWithRemediationRoundsMax` | `runner` | Findings never converge; rounds exhausted → reason `remediation-rounds-max`. |
| `TestScenario_Dispatcher_AdversaryConverged_RunsVerification` | `runner` | Cycle converges → verification clause set includes `no-open-finding` + `every-requirement-meets-min-depth`. |
| `TestScenario_Dispatcher_InitRole_SkipsCycle` | `runner` | `req.Role == "init"` even with depth-sensitive clauses → no `Adversary` constructed. |
| `TestScenario_Dispatcher_AdversaryRole_SkipsCycle` | `runner` | `req.Role == "adversary"` → no `Adversary` constructed. |
| `TestScenario_Dispatcher_AdversaryFindingsPersist` | `runner` | Cycle raises findings, then escalates → `FindingsStore.ForArrow` still returns them. |
| `TestScenario_AdversaryCommand_EnableConstructsHooks` | `cmd/ghyll` | `/adversary enable` → `s.engine.adversaryHooks != nil`. |
| `TestScenario_AdversaryCommand_DisableTearsDown` | `cmd/ghyll` | `/adversary disable` → `s.engine.adversaryHooks == nil`. |
| `TestScenario_AdversaryCommand_StatusReportsDisabled` | `cmd/ghyll` | Fresh session → `/adversary` returns "disabled". |
| `TestScenario_AdversaryCommand_RunArrowRefusesDepthSensitive` | `cmd/ghyll` | Disabled + depth-sensitive arrow → `/run-arrow A1` output contains `adversarial-hooks-not-wired`. |

### BDD scenarios

```gherkin
Feature: Adversarial cycle runs in production

  Scenario: Depth-sensitive arrow runs adversarial then verification
    Given a project with arrow A1 declaring a depth-sensitive clause
    And the operator has enabled the adversarial cycle
    When the operator runs "/run-arrow A1"
    Then the dispatcher fires Adversary.Attack at least once
    And the verification phase includes "no-open-finding" and
        "every-requirement-meets-min-depth" auto-inserted clauses
    And the arrow status reflects the verification outcome

  Scenario: Depth-robust arrow skips adversarial cycle
    Given a project with arrow A2 declaring only depth-robust clauses
    When the operator runs "/run-arrow A2"
    Then no Adversary is constructed
    And no Remediation report is published

  Scenario: Adversarial cycle disabled refuses depth-sensitive arrow
    Given a session with the adversarial cycle disabled
    And arrow A1 declares a depth-sensitive clause
    When the operator runs "/run-arrow A1"
    Then the pass aborts with reason matching "adversarial-hooks-not-wired"

  Scenario: Producer loop-bomb aborts cycle
    Given an arrow whose producer returns identical artifact across rounds
    And the adversarial cycle is enabled
    When /run-arrow runs
    Then the pass aborts with reason matching "producer-loop-bomb"
    And the FindingsStore retains the findings raised during the cycle
```

Tests live in `tests/acceptance/steps_adversarial.go` (already
present) — extend with new step definitions for the
"adversarial cycle enabled" stance.

### ADR-worthy? Yes — flag for implementer

Adding a new dispatcher phase (the cycle between Open and the
verification loop) is structural. Recommend the implementer drafts
**ADR-v4-002**:
- decision: dispatcher gains an adversarial phase wired
  unconditionally for depth-sensitive arrows; refusal-by-default
  semantics for unwired hooks;
- alternative considered: orchestrate from outside the dispatcher
  (e.g., a new `RunArrow` wrapper in `engineRuntime`);
- consequence: the dispatcher is the single production caller of
  `RunRemediationLoop`. Test paths that bypassed it (constructing
  adversaries directly) are still valid for unit coverage; the
  production path is funneled through `Dispatch`.

---

## Gap 2: Amendment drain wiring

### Files touched

| File | Function / line | Change |
|---|---|---|
| `runner/amendment_commit.go` | `AmendmentCommitter` struct (line 32) | Add `BindingsReRegister func() error` (already mentioned under gap 3). Optional; nil = skip re-registration step. |
| `runner/amendment_commit.go` | `Commit` (line 97) | After step 2 (grid append) and BEFORE step 4 (bus publish), invoke `BindingsReRegister` if non-nil. Failures wrap into the partial-append error path (matches `appendErr` handling). |
| `runner/dispatcher.go` | `Dispatch` integrator-pass close branch | After `pass.Close(...)` returns and `req.Role == "integrator"`, scan the FindingsStore for `missing-cross-context-spec` findings on the arrow. For each, call `runner.PendingAmendments(d.FindingsStore, req.Arrow.ID, contexts, idGen)` and `d.AmendmentQueue.Enqueue` each result. This is **trigger A** of the drain. New field `AmendmentQueue *AmendmentQueue` on `PassDispatcher`. The integrator's pass close DOES NOT auto-Commit — it enqueues. Drain itself fires from the slash command or startup banner. |
| `runner/dispatcher.go` | `PassDispatcher` struct | Add fields: `AmendmentQueue *AmendmentQueue`, `AmendmentContexts func(arrowID string) []string` (resolves the bounded-contexts implicated by the arrow — required for `PendingAmendments`'s ≥2-contexts rule). Both optional — nil disables the enqueue step. |
| `cmd/ghyll/session.go` | `DispatchSlashCommand` (line 1273) | Add `/drain-amendments` handler. Wire BEFORE `/run-arrow` so longest-prefix dispatch is deterministic. |
| `cmd/ghyll/drain_amendments_cmd.go` (new) | `handleDrainAmendmentsCommand(arg string) SlashCommandResult` | Drains every pending amendment FIFO via `runner.AmendmentCommitter.Commit`. Asserts op-id is set (refuses with usage prompt if not). On partial drain, surfaces the per-amendment status (drained / partial-append-error). Returns `ContinueLoop: true`. |
| `cmd/ghyll/session_engine.go` | `engineRuntime` struct | Add `committer *runner.AmendmentCommitter`. |
| `cmd/ghyll/session_engine.go` | `openEngineWithOptions` | After `rt.amendments = runner.NewAmendmentQueue()` and AFTER `rt.grid = runner.NewGrid()`, construct: `rt.committer = &runner.AmendmentCommitter{Grid: rt.grid, Passes: rt.passes, Bus: rt.bus, Queue: rt.amendments, BindingsReRegister: rt.reRegisterBindings, Now: time.Now}`. |
| `cmd/ghyll/session_engine.go` | new method `(*engineRuntime).reRegisterBindings() error` | Wraps `runner.RegisterGridBindings(rt.registry, rt.gridFile, rt.workdir)` (after re-loading `rt.gridFile` from disk via `bootstrap.Read(rt.workdir)` — the on-disk grid has been replaced by the amendment write). |
| `cmd/ghyll/session.go` | `initEngine` (~line 365, after `engine replayed: ...` line) | Add the **trigger C** banner: if `rt.Amendments().Len() > 0`, output `⚠ <N> pending amendment(s); run /drain-amendments to apply` and a per-amendment summary (`runner.FormatAmendmentSummary`). No auto-drain. |
| `runner/dispatcher.go` | new helper `enqueueAmendmentsForIntegratorPass(d *PassDispatcher, req DispatchRequest)` | Walks `req.Arrow`'s findings post-close; called inside `Dispatch` only when `req.Role == "integrator"` and the pass closed (didn't abort). Returns the count enqueued; errors are non-fatal (logged via slog through `d.Bus` if available, but the pass is already closed). |

### Trigger A: integrator-pass close

Per the analyst spec ("integrator-pass close" fires the drain), the
analyst's contract is: the *enqueue* fires at integrator close; the
*Commit* fires when the operator invokes the verb (trigger B) or at
session startup banner (trigger C). The architect interpretation
that matches the contract: the dispatcher enqueues, never auto-
Commits. Per ADR-012 amendments are operator-attested.

Implementation (in `runner/dispatcher.go:Dispatch`, after
`pass.Close` returns and BEFORE the function returns):

```go
if req.Role == "integrator" && d.AmendmentQueue != nil && d.FindingsStore != nil {
    enqueueAmendmentsForIntegratorPass(d, req)
}
```

The enqueue helper:

```go
func enqueueAmendmentsForIntegratorPass(d *PassDispatcher, req DispatchRequest) {
    if d.AmendmentContexts == nil { return } // operator hasn't wired contexts
    contexts := d.AmendmentContexts(req.Arrow.ID)
    if len(contexts) < 2 { return } // PendingAmendments would refuse
    reqs, err := PendingAmendments(d.FindingsStore, req.Arrow.ID, contexts, nil)
    if err != nil || len(reqs) == 0 { return }
    for _, r := range reqs {
        if err := d.AmendmentQueue.Enqueue(r); err != nil {
            // Duplicate-ID, queue-full: not fatal here; the next
            // integrator pass will re-attempt. Bus event surfaces
            // visibility.
            if d.Bus != nil {
                d.Bus.Publish(OperatorEvent{
                    Kind:    OpEventAmendmentEnqueued,
                    ArrowID: req.Arrow.ID,
                    Detail:  fmt.Sprintf("enqueue-failed: %v", err),
                })
            }
            continue
        }
        if d.Bus != nil {
            d.Bus.Publish(OperatorEvent{
                Kind:    OpEventAmendmentEnqueued,
                ArrowID: req.Arrow.ID,
                FindingID: r.FindingIDs[0],
                Detail: fmt.Sprintf("amendment=%s reason=%s", r.ID, r.Reason),
            })
        }
    }
}
```

`OpEventAmendmentEnqueued` already exists per `operatorbus.go:46`.

### Trigger B: slash command (`/drain-amendments`)

Verb chosen: `/drain-amendments`. Alternatives considered:
`/apply-amendments`, `/commit-amendments`. `drain` matches the
existing `AmendmentQueue.Drain` and `AmendmentEventDrain` vocabulary
in `amendment.go` (the operator's mental model already uses
"drain"). The verb is plural to signal that one invocation drains
the whole pending list.

```text
handleDrainAmendmentsCommand:
  1. Refuse if engine nil OR op-id unset.
  2. snapshot := s.engine.Amendments().Pending()  // deep copy; doesn't clear
  3. for each amendment in snapshot:
     a. CommitRequest := build from amendment + NewArrows resolver
        (NewArrows comes from a per-amendment artifact: the analyst's
         response to the missing-cross-context-spec finding lives
         under .ghyll/amendments/<amendment-id>/arrows.yaml — a new
         on-disk convention; the implementer adds a loader).
        Empty arrows is valid per ADR-012; the analyst may decide
        the contract still holds.
     b. result, err := s.engine.committer.Commit(ctx, req)
     c. accumulate (committed-count, partial-count, error-count)
  4. emit per-amendment status to output: "✓ amendment <id> drained
     (v: N → M, +K arrows, aborted P passes)"
  5. on global error (e.g., committer Commit fails fast), surface
     and abort the loop.
```

Failure mode for missing `arrows.yaml`: the loader returns
`empty NewArrows` (valid per ADR-012); the Commit aborts in-flight
passes and bumps no version. This is the analyst-rejected-amendment
path.

### Trigger C: startup banner (operator confirm prompt)

`initEngine` already surfaces operator-facing lines via
`s.output(...)`. Add post-replay (after the engine-replayed line):

```go
if pending := rt.Amendments().Pending(); len(pending) > 0 {
    s.output(fmt.Sprintf("⚠ %d pending amendment(s); run /drain-amendments to apply",
        len(pending)))
    for _, a := range pending {
        s.output("  " + runner.FormatAmendmentSummary(a))
    }
}
```

The analyst spec says "operator confirm prompt"; the architect
interpretation is that the banner IS the prompt. No interactive
modal at startup — the operator confirms by typing
`/drain-amendments`. This matches the spec's "the harness does not
invent grid mutations across a restart" rule.

### Global-lock contract

`AmendmentCommitter.Commit` already holds `c.mu` for the whole
operation (`amendment_commit.go:115`). The mutex is per-committer
(one per session), and the session has one committer. Two concurrent
`/drain-amendments` invocations are impossible (REPL is
single-threaded), but a `/drain-amendments` racing with an
integrator-pass enqueue is possible: the enqueue takes the
`AmendmentQueue.mu` (`amendment.go:181`); the drain takes
`committer.mu` first, then calls `AmendmentQueue.MarkDrained` which
takes `AmendmentQueue.mu`. Lock order: committer.mu → queue.mu.
No order inversion exists today; the implementer must NOT add a
reverse path.

The race-clean test (`TestScenario_AmendmentCommit_GlobalLock_Serializes`)
fires two goroutines:
1. Enqueues a new amendment.
2. Drains the queue.
Asserts no interleaved version bump (the GridVersionBefore →
GridVersionAfter window of one drain doesn't overlap another).

### Failure / partial-failure rollback

Per the analyst spec the existing `AmendmentCommitter.Commit`
already encodes the contract:
- Pass-abort happens BEFORE grid-append. If append fails, passes
  are still aborted (contract-invalidation is the analyst's
  decision, not a mechanical retry-success).
- `drained_at` persists regardless of append outcome.
- No retry. Partial-append routes to operator triage via the
  `OpEventAmendmentDrained` event with `status=partial-append-error`
  and the wrapped error from `Commit`.

New: re-registration of bindings (Gap 3 cross-cutting) on partial
append. If grid-append succeeds but `BindingsReRegister` fails:
- The grid IS bumped (irreversible).
- Bindings on the registry reflect the old grid's bindings.
- Surfaced via `OpEventAmendmentDrained` with
  `status=binding-re-register-error`.
- Operator's next dispatch sees the old bindings (still valid for
  pre-amendment arrows); arrows that need the new binding crash with
  `ErrConceptNotRegistered` until the operator restarts the session
  (which re-runs `RegisterGridBindings`).

This is acceptable per the analyst's "no retry" rule. The
implementer should document this caveat in the
`AmendmentCommitter` docstring.

### OpEvent kinds fired

- `OpEventAmendmentEnqueued` (already exists, line 46) — at
  integrator-pass close on each `PendingAmendments` result.
- `OpEventAmendmentDrained` (already exists, line 47) — at
  Commit success. Detail field gains the
  `status=<complete|partial-append-error|binding-re-register-error>`
  discriminator (free-text Detail; backward-compatible).
- No new OpEventKinds.

The startup banner does NOT fire an event (no subscribers at that
point, per the F-18 invariant cited in `session_engine.go:323`).
The operator sees the banner via `s.output`; if a future operator-UI
needs the signal, it can subscribe to `OpEventAmendmentEnqueued`
events replayed via the journal.

### Tests

| Test name | Package | Asserts |
|---|---|---|
| `TestScenario_Dispatcher_IntegratorClose_EnqueuesAmendment` | `runner` | Integrator pass with one open `missing-cross-context-spec` finding → `AmendmentQueue.Len() == 1` after Dispatch returns. |
| `TestScenario_Dispatcher_NonIntegratorClose_DoesNotEnqueue` | `runner` | Same finding under role `implementer` → no enqueue. |
| `TestScenario_Dispatcher_IntegratorAbort_DoesNotEnqueue` | `runner` | Integrator pass aborts (clause eval error) → no enqueue (post-close-only). |
| `TestScenario_DrainAmendmentsCommand_DrainsAll` | `cmd/ghyll` | 2 pending amendments + `/drain-amendments` → queue length 0 after; grid version bumped twice. |
| `TestScenario_DrainAmendmentsCommand_NoOpID_Refuses` | `cmd/ghyll` | `/drain-amendments` without `/op-id` set → usage prompt; queue untouched. |
| `TestScenario_DrainAmendmentsCommand_NoPending_Reports` | `cmd/ghyll` | `/drain-amendments` with empty queue → "no pending amendments". |
| `TestScenario_Session_StartupBanner_SurfacesPendingAmendments` | `cmd/ghyll` | Session resumes with 1 pending amendment from persistence → startup output mentions the count + the amendment summary. |
| `TestScenario_Session_StartupBanner_NoAutoDrain` | `cmd/ghyll` | Session resumes with pending amendment → after `initEngine` returns, `s.engine.Amendments().Len() == 1` (NOT zero — confirms no auto-drain). |
| `TestScenario_AmendmentCommit_GlobalLock_Serializes` | `runner` | Two goroutines: one drains, one tries to enqueue concurrently. No `GridVersion` decreases; the version window of one drain doesn't overlap another. Run under `-race`. |
| `TestScenario_AmendmentCommit_DrainedIDDedupAcrossRestart` | `runner` | Enqueue `amend-X`, Commit, restart session (reload `AmendmentQueue` via `LoadDrained`), re-enqueue `amend-X` → `ErrAmendmentDuplicateID`. |
| `TestScenario_AmendmentCommit_BindingsReRegisterFires` | `runner` | `BindingsReRegister` is wired; Commit calls it. Fail-path: `BindingsReRegister` errors → Commit returns wrapped error; `OpEventAmendmentDrained.Detail` carries `status=binding-re-register-error`. |

### BDD scenarios

```gherkin
Feature: Amendment drain mutates the live grid

  Scenario: Integrator-pass close enqueues amendment
    Given an integrator pass closes with one open missing-cross-context-spec finding
    Then the amendment queue length is 1
    And the bus emits an amendment-enqueued event

  Scenario: Operator drain applies the amendment
    Given the queue length is 1
    And the operator has set /op-id
    When the operator runs "/drain-amendments"
    Then the grid version increments by 1
    And the queue length drops to 0
    And the bus emits an amendment-drained event

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
    And the queue length remains 1 (no auto-drain)

  Scenario: Drained-ID dedup survives restart
    Given an amendment was drained in a prior session
    When ghyll opens a new session
    And an enqueue with the same amendment ID is attempted
    Then enqueue refuses with ErrAmendmentDuplicateID
```

Tests live in `tests/acceptance/steps_amendment.go` (already
present). The dedup-across-restart scenario depends on the
amendment queue's `seenIDs` being re-hydrated at session start via
`LoadDrained` — this load already exists at `amendment.go:289`
and is called by Replay (`engine.Replay` populates the
AmendmentsDrained count); the test confirms the load happens.

### ADR-worthy? No

The amendment-drain triggers (close / slash / startup banner) and
the operator-attested commit semantics are already specified by
ADR-012 and the integrator role contract. The wiring here is
mechanical — no new structural decisions. The `/drain-amendments`
verb is a slash-command spelling, not an architectural decision; the
implementer can adjust if a clearer name emerges in review.

---

## Cross-cutting

### Race-clean checks

The CI runs `go test ./... -race`. The following packages must be
re-tested under `-race` after each gap lands:

- **Gap 3** — `runner` (`Registry.Register/Replace/Lookup` already
  uses `sync.RWMutex`; the new `RegisterGridBindings` does not add
  parallel call sites, but the amendment-driven re-register is a
  concurrent surface). New test:
  `TestScenario_RegisterGridBindings_ConcurrentRegisterReplace`.
- **Gap 4** — `runner` (the four new evaluators are stateless; race
  surface limited to `EvaluateSingleActiveRoleInstance`'s
  PassRegistry read, which uses the registry's existing read
  lock). No new race-prone surface.
- **Gap 1** — `runner` (dispatcher's new phase reads from
  `FindingsStore` + `ClassificationsStore` + `PassRegistry`
  concurrently; all three already have their locks). The Adversary
  is constructed fresh per round (no shared mutable state). The
  `ProducerFixHarness` has its own mutex (`producer_fix.go:48`).
  New surface: `d.AdversaryFactory` is read by the dispatcher and
  may be replaced by `/adversary enable|disable` from the REPL
  goroutine. Lock discipline: the `/adversary` handler must
  acquire a new `s.engine.adversaryHooksMu sync.Mutex` for the
  swap. Document at the field site.
- **Gap 2** — `runner` (the amendment-commit lock + queue lock are
  ordered committer→queue; tests already cover this. Add the
  concurrent enqueue+drain test above.).

### Coverage delta expected

Current threshold is 70% (per `make coverage-check`). The four gaps
each add new code paths. Expected delta per gap (rough estimate
based on substrate read):

| Gap | Net new LOC (impl) | Net new LOC (tests) | Delta to coverage |
|---|---|---|---|
| 3 | ~250 | ~400 | +0.5–1.0% |
| 4 | ~500 | ~600 | +1.5–2.0% |
| 1 | ~350 | ~500 | +0.5–1.0% |
| 2 | ~200 | ~350 | +0.5% |

Total: ~1300 LOC implementation + ~1850 LOC tests. The coverage
threshold should hold; if it dips, the implementer adds focused
tests on the dispatcher's new phase routing (the most-branched new
code).

### Lefthook pre-commit expectations

Lefthook runs `go fmt`, `go vet`, `make test-unit`. The four gaps
must each be split into commits passing lefthook independently:

- Gap 3 commit 1: `runner.ConceptRegistryKey` + tests (no callers
  changed yet) — passes `make test-unit`.
- Gap 3 commit 2: `runner.RegisterGridBindings` + tests — passes.
- Gap 3 commit 3: `session_engine.openEngineWithOptions` wires
  `RegisterGridBindings`. Tests in `cmd/ghyll` pass.
- Gap 3 commit 4: amendment-driven re-register +
  `AmendmentCommitter.BindingsReRegister` field. Tests pass.

Gaps 4, 1, 2 follow similar small-step shapes. The implementer
should land each gap as a single PR (4 PRs total), each PR
internally split into reviewable commits.

### Files the implementer will touch — total estimate

| Area | Files |
|---|---|
| New files | `runner/bindings_register.go`, `runner/uniquedef.go`, `runner/predicateform.go`, `runner/moderepostate.go`, `runner/singleactiverole.go`, `cmd/ghyll/adversary_cmd.go`, `cmd/ghyll/drain_amendments_cmd.go`, `specs/features/binding-evaluation.feature`, `specs/features/universal-base.feature`, `specs/features/adversarial-cycle.feature`, `specs/features/amendment-drain.feature`, ADR-v4-001, ADR-v4-002 |
| Modified files | `runner/runner.go`, `runner/notodo.go`, `runner/dispatcher.go`, `runner/amendment_commit.go`, `cmd/ghyll/session.go`, `cmd/ghyll/session_engine.go`, `cmd/ghyll/run_arrow_cmd.go` (event subscription), `tests/acceptance/steps_bindings.go`, `tests/acceptance/steps_runner.go`, `tests/acceptance/steps_adversarial.go`, `tests/acceptance/steps_amendment.go`, `docs/decisions/v4/index.md` (link to new ADRs) |
| New tests | ~25–30 new `TestScenario_*` files added to existing test files; ~15 new BDD scenarios |

Approximately **25 files modified/created** + **2 ADRs** + **~30
tests**.

---

## References

- `specs/v4/diamond-load-bearing-spec.md` — analyst spec this design
  responds to.
- ADR-005, ADR-006, ADR-008, ADR-009, ADR-010, ADR-011, ADR-013,
  ADR-014, ADR-015, ADR-016, V2-ADR-012.
- `gates.md` §1.1 (synthetic role-ids), §2.1 D18 (language
  bindings), §3.7 (amendment), §5.1–5.2 (catalogue + universal
  base), §6 (depth type), §7.1 (clause lifecycle), §7.1a (pass
  identity), §7.2 (arrow + invalidation), §7.3 (finding
  lifecycle), §8 (routing), §11 (adversarial cycle).
- `gates/concepts/README.md` — canonical universal vs language-bound
  classification.
- `specs/architecture/components/adversarial.md`,
  `specs/architecture/components/amendment.md`,
  `specs/architecture/components/concepts.md`.
- `specs/v4/code-eval-2026-05-25.md` — integrator pass that
  surfaced these three gaps.
