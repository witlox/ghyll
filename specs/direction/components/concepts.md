# Component: catalogue concepts

Per-concept specs for the 17 machine-clause concepts named in
`gates.md` §5.1. The harness ships these concepts as the closed
language-agnostic vocabulary of the gate system; per-arrow clause
instances reference them by name and supply typed arguments.

This file is the **design** for the 17 schema files that will ship
as `gates/concepts/<concept-name>.yaml` (the YAML files are
implementation; this is the spec).

> Status: design intent. Subject to refinement once the first
> evaluator is implemented and exercises the contract.

## Concept categories

- **Structural** (artifact shape, no domain reasoning) — `compiles`,
  `lint-clean`, `no-todo-marker`, `every-step-bound`,
  `predicate-form`, `arrow-artifact-present`, `unique-definition`,
  `acyclic-dependency-graph`
- **Coverage** (tracing / orphan detection) — `no-orphan-symbol`,
  `trace-link-present`, `cardinality-check`
- **Depth / mutation** — `mutation-score`,
  `every-requirement-meets-min-depth`
- **Integration** — `kill-server-fails-integration`
- **State / coordination** — `no-open-finding`,
  `mode-determinable-from-repo`, `single-active-role-instance`

## Universal base set (auto-applied to every arrow, §5.2)

`compiles`, `lint-clean`, `no-todo-marker`, `every-step-bound`.

## Auto-inserted on adversarial arrows (§11)

`no-open-finding`, `every-requirement-meets-min-depth`.

## Common types

- **`path-glob`** — POSIX glob string (e.g., `"src/**/*.go"`).
- **`artifact-ref`** — path or path+section or `clause-id` (per
  hybrid IDs, D11).
- **`language-id`** — `"go"`, `"rust"`, `"typescript"`, `"python"`,
  etc. Used as the suffix in language-bound concepts
  (`compiles.<lang>`, `lint-clean.<lang>`, …).
- **`severity`** — `info | low | medium | high | critical` (§7.3).
- **`pass-result`** — `{ pass: boolean, details: object }`. All
  evaluators return this shape; `details` is concept-specific.

---

## Concepts

### `compiles`

**Purpose.** The artifact compiles, builds, or parses without error.

**Arguments.**

| Name | Type | Required | Description |
|---|---|---|---|
| `scope` | path-glob | yes | Files to compile / parse |
| `language` | language-id | yes | Selects the binding |

**Evaluator.** Runs the binding's compile command over `scope`.
Returns `pass = true` if exit code is 0 and no compiler errors are
emitted on stderr. `details.errors` is a list of `{file, line, msg}`
on failure.

**Default cost.** `0` (cheap; the build runs anyway).

**Language binding.** Project-declared at init (D18).

| Language | Binding example |
|---|---|
| go | `go build ./...` |
| rust | `cargo check` |
| typescript | `tsc --noEmit` |
| gherkin | A gherkin-parser CLI (any) |
| python | `python -m py_compile` or `mypy --no-error-summary` |

**Edge cases.**

- Empty `scope` → `pass = true` (vacuously). Init should warn during
  auto-propose if scope resolves to empty.
- Warnings (non-error compiler output) do NOT fail; only errors do.
  Warning-level enforcement is `lint-clean`'s job.
- A binding that emits errors to stdout instead of stderr is a
  binding bug, not a concept bug — bindings must conform to the
  evaluator contract.

---

### `lint-clean`

**Purpose.** The artifact passes the language's lint toolchain with no
findings above the declared severity threshold.

**Arguments.**

| Name | Type | Required | Description |
|---|---|---|---|
| `scope` | path-glob | yes | Files to lint |
| `language` | language-id | yes | Selects the binding |
| `severity-threshold` | severity | no | Default `medium`. Findings strictly below this are allowed. |

**Evaluator.** Runs binding's lint command. `pass = true` if every
emitted finding has severity below threshold. `details.findings` is
list of `{file, line, severity, msg, rule}`.

**Default cost.** `0`.

**Language binding.** Project-declared at init.

| Language | Binding example |
|---|---|
| go | `staticcheck ./... && go vet ./...` |
| rust | `clippy -- -D warnings` |
| typescript | `eslint --format json` |
| python | `ruff check --format=json` |

**Edge cases.**

- Different lint tools use different severity vocabularies. The
  binding's job is to map tool-native severity → the canonical
  `info/low/medium/high/critical` enum.
- A lint binding that always returns 0 findings is suspicious; init
  should warn during auto-propose.

---

### `no-todo-marker`

**Purpose.** No `TODO` / `TBD` / `???` / `FIXME` / `XXX` strings in the
named scope. Captures the work-not-done signal that prose can hide.

**Arguments.**

| Name | Type | Required | Description |
|---|---|---|---|
| `scope` | path-glob | yes | Files to scan |
| `markers` | list of string | no | Default `["TODO", "TBD", "???", "FIXME", "XXX"]` |

**Evaluator.** Recursive text search. Case-insensitive by default.
`pass = true` if no occurrence in `scope`. `details.hits` is list of
`{file, line, marker, surrounding-text}`.

**Default cost.** `0`.

**Language binding.** None — concept is text-only.

**Edge cases.**

- Markers in comments inside string literals or test fixtures
  legitimately appear. The concept does not distinguish — the
  operator should narrow `scope` to exclude test-fixture
  directories.
- A line-comment that *describes* a TODO marker (e.g., "explain how
  TODO works") would false-positive. Project-init may declare a
  comment prefix to exclude (e.g., `// docs:` ignores).

---

### `every-step-bound`

**Purpose.** Every step / case / branch / clause in the artifact is
structurally bound — no dangling cases, no fall-through gaps.

**Arguments.**

| Name | Type | Required | Description |
|---|---|---|---|
| `scope` | path-glob | yes | Files to check |
| `language` | language-id | yes | Selects the binding |

**Evaluator.** Language-specific. For Gherkin: every Given/When/Then
has a step definition; no `pending` steps. For Go: every `case` in
a `switch` has a body; every type-switch has a `default` or covers
all types. For TypeScript: every `switch` covers exhaustively (with
`never`-check), every async path resolves or rejects.

`pass = true` if no unbound step found. `details.unbound` is list
of `{file, line, kind, description}`.

**Default cost.** `0`.

**Language binding.** Project-declared at init.

**Edge cases.**

- Exhaustiveness over an open enum (e.g., a string-typed dimension)
  is impossible by definition; the binding should accept a
  `default`-branch as sufficient closure.
- Deliberately incomplete behavior (e.g., `// fallthrough: handled
  by caller`) should be declared as accepted-risk, not silently
  passed.

---

### `no-orphan-symbol`

**Purpose.** Every exported symbol in the scope traces to a declared
spec clause; orphans (exported symbols with no spec entry) are listed
in the arrow's coverage-claim residue.

**Arguments.**

| Name | Type | Required | Description |
|---|---|---|---|
| `scope` | path-glob | yes | Source files holding exported symbols |
| `spec-index` | artifact-ref | yes | Spec artifact containing the clause-ID index |
| `language` | language-id | yes | Selects the extractor |

**Evaluator.** Runs binding's `extractor` over `scope` to produce
the exported-symbol list. Runs binding's `mapper` to find each
symbol's referenced spec clause-IDs. `pass = true` if every symbol
maps to at least one clause-ID present in `spec-index`.
`details.orphans` is list of unmapped symbols.

**Default cost.** `1`.

**Language binding.** Project-declared at init. Shape:

```yaml
no-orphan-symbol.<lang>:
  extractor: <cmd producing exported-symbol list, one per line>
  mapper:    <cmd or regex producing symbol → [clause-id] map>
```

| Language | Extractor example | Mapper example |
|---|---|---|
| go | `go list -json` filtered to exported | comment-tag regex `// SPEC: <id>` |
| rust | `cargo doc --no-deps --output-format=json` | doc-comment regex |
| typescript | `tsc --listFiles --declaration` | JSDoc `@spec <id>` |

**Edge cases.**

- A symbol exported but unused externally (a method on an interface
  no one implements) still requires a spec tie. Init should warn
  during auto-propose.
- Internal/test-only symbols are out of scope; the `extractor`
  command must exclude them.
- A spec clause that is referenced but does not exist in
  `spec-index` is an error, not an orphan — distinct `details`
  field.

---

### `mutation-score`

**Purpose.** Mutation-testing reports a kill rate at or above the
declared threshold over the named scope. Defends against shallow
tests (tests that run code but assert little).

**Arguments.**

| Name | Type | Required | Description |
|---|---|---|---|
| `scope` | path-glob | yes | Source files to mutate |
| `test-scope` | path-glob | yes | Tests to run against mutations |
| `threshold` | number (0.0–1.0) | yes | Minimum kill rate |
| `language` | language-id | yes | Selects the binding |
| `timeout-per-mutation` | duration | no | Default `30s` |

**Evaluator.** Runs binding's mutation tool over `scope`, scoring
killed / survived ratio against `threshold`.
`pass = true` if `killed / (killed + survived) >= threshold` (and
no `timeout` mutations exceed a separate timeout-tolerance).
`details = { score, killed, survived, timed-out, equivalent }`.

**Default cost.** `3`.

**Language binding.** Project-declared.

| Language | Binding example |
|---|---|
| go | `go-mutesting` |
| rust | `cargo-mutants` |
| typescript | `stryker-mutator` |
| python | `mutmut` or `cosmic-ray` |

**Edge cases.**

- Mutation testing is slow. Init should set a realistic
  `timeout-per-mutation` based on the project's test runtime.
- Equivalent mutations (mutations that don't change behavior, e.g.,
  swapping `==` for `===` in JS when both sides are typed) are
  reported separately; do NOT count against `survived`.
- A scope with no executable code (all interfaces / types) returns
  `pass = true` vacuously. Init should warn.

---

### `kill-server-fails-integration`

**Purpose.** When a critical dependency is removed (server killed,
network blocked), the integration test suite fails. Defends against
test suites that mock the dependency and never exercise the real
wire.

**Arguments.**

| Name | Type | Required | Description |
|---|---|---|---|
| `test-suite` | path-glob | yes | Integration tests to run |
| `critical-deps` | list of dependency-id | yes | Dependencies whose absence must fail tests |
| `kill-strategy` | string | no | How to remove dependency (`stop-process`, `block-network`, `null-config`). Default `block-network`. |

**Evaluator.** Two-phase: (1) run `test-suite` with deps available
— must pass. (2) For each `critical-dep`, apply `kill-strategy`
and re-run; the suite must fail. `pass = true` only if BOTH happen.
`details = { suite-passes-with-deps, dep-results: [{dep, killed, suite-failed}] }`.

**Default cost.** `3`.

**Language binding.** None at concept level; `kill-strategy` is a
shell-script-or-cmd reference declared per-project.

**Edge cases.**

- The kill must be reversible — after the evaluator runs, deps
  return. Init's kill-strategy must include an `unkill` step.
- A dep that is mocked via dependency injection rather than network
  cannot be killed externally; the binding must hook into the test
  runner's config, not the system.
- Some test suites pass-by-default when deps fail (e.g., skipping
  integration tests). This is a binding bug — the binding must
  ensure tests run, not skip.

---

### `trace-link-present`

**Purpose.** A declared link between two artifacts exists. Used for
spec→implementation traces, requirement→test traces,
context→domain-model traces, etc.

**Arguments.**

| Name | Type | Required | Description |
|---|---|---|---|
| `from` | artifact-ref | yes | Source side of the link |
| `to` | artifact-ref | yes | Destination side |
| `link-rule` | string | yes | How to recognize the link (regex / xpath / clause-ID convention) |
| `min-multiplicity` | int | no | Default `1`. Minimum links from each `from` entry. |
| `max-multiplicity` | int | no | Default unlimited. Maximum links per `from` entry. |

**Evaluator.** Reads `from` entries, applies `link-rule` to find
linked `to` entries, validates multiplicity. `pass = true` if every
`from` entry has multiplicities within bounds.
`details = { violations: [{from-entry, count, expected-range}] }`.

**Default cost.** `1`.

**Language binding.** None — concept is artifact-level.

**Edge cases.**

- A `from` entry with no link is the primary failure case.
  `details.violations` includes the from-entry's clause-ID for
  operator triage.
- Multiple links from one `from` to the same `to` count as one
  link (deduplicated). `max-multiplicity` measures distinct
  destinations.
- Path-based addressing makes `link-rule` brittle to file
  refactors; prefer clause-IDs (D11) for high-stability links.

---

### `acyclic-dependency-graph`

**Purpose.** The dependency graph of the named scope is acyclic.

**Arguments.**

| Name | Type | Required | Description |
|---|---|---|---|
| `scope` | path-glob | yes | Modules / packages to analyze |
| `language` | language-id | yes | Selects the dependency-extractor binding |
| `kind` | enum | no | `import-graph` (default), `call-graph`, `type-graph` |

**Evaluator.** Builds the dependency graph per `kind` over `scope`.
Detects cycles. `pass = true` if no cycle present.
`details.cycles` is list of cycle-paths.

**Default cost.** `0`.

**Language binding.** Project-declared (extractor cmd per language).

**Edge cases.**

- Some languages (TypeScript with re-export-only files, Go with
  internal packages) have legitimate near-cycles. The binding's
  extractor decides what counts as an edge.
- An import that is only used at type-time vs at run-time may be a
  cycle in one `kind` but not another. Init should pick the `kind`
  per arrow.

---

### `unique-definition`

**Purpose.** A named field's values are unique across the artifact.
Used for clause-ID uniqueness, term uniqueness in
ubiquitous-language, etc.

**Arguments.**

| Name | Type | Required | Description |
|---|---|---|---|
| `scope` | path-glob | yes | Files to scan |
| `field` | string | yes | The field/attribute whose values must be unique |
| `field-locator-rule` | string | yes | How to find the field (regex / yaml-path / column-name) |

**Evaluator.** Extracts all values of `field` from `scope` using
`field-locator-rule`. `pass = true` if no value appears twice.
`details.duplicates = [{value, locations}]`.

**Default cost.** `0`.

**Language binding.** None — concept is artifact-level.

**Edge cases.**

- Case sensitivity: default is case-sensitive comparison. Init may
  declare case-insensitive for human-vocabulary fields.
- Empty `field` value is treated as missing-field, not as a value;
  multiple empty fields are not duplicates.

---

### `predicate-form`

**Purpose.** Each entry in a named collection is written as an
assertable predicate, not prose narrative. Defends against invariant
documents that read as wishes ("the system should be available")
rather than statements that can be checked ("`uptime >= 0.999` over
any 30-day window").

**Arguments.**

| Name | Type | Required | Description |
|---|---|---|---|
| `scope` | path-glob | yes | Files to scan |
| `collection-locator` | string | yes | How to find the collection (yaml-path / regex / markdown-section-name) |
| `predicate-grammar` | string | no | Default: contains at least one comparison operator OR is in `assert(...)` form OR matches a project-declared grammar |

**Evaluator.** Per entry in the located collection, applies
`predicate-grammar` to check assertability. `pass = true` if every
entry parses as a predicate.
`details.non-predicates = [{entry, location, hint}]`.

**Default cost.** `1`.

**Language binding.** None — concept is text-shape.

**Edge cases.**

- An entry that combines prose AND a predicate ("uptime is
  critical; we need `99.9%`") parses as predicate-bearing. Init may
  raise threshold to "entry is *primarily* predicate."
- Math expressions (`x > y`) qualify; English-sentence statements
  with comparison words ("uptime higher than 99.9%") do NOT, unless
  the grammar is declared loose at init.

---

### `arrow-artifact-present`

**Purpose.** The arrow's declared output artifact exists at its
declared location and is non-empty.

**Arguments.**

| Name | Type | Required | Description |
|---|---|---|---|
| `artifact-path` | string | yes | Where the artifact is expected |
| `min-size-bytes` | int | no | Default `1`. Minimum file size. |
| `schema-check` | optional command | no | If declared, runs and must exit 0. |

**Evaluator.** Checks existence, size, and (if `schema-check`)
schema validity. `pass = true` if all checks succeed.
`details = { exists, size-bytes, schema-valid }`.

**Default cost.** `0`.

**Language binding.** None.

**Edge cases.**

- An empty file (0 bytes) does not pass even if `min-size-bytes`
  is 0 — `min-size-bytes >= 1` is the floor.
- A file that exists but is malformed (e.g., not valid Gherkin
  for a `.feature` file) requires a `schema-check` to catch; the
  bare concept only checks presence.

---

### `no-open-finding`

**Purpose.** All findings on the arrow are `resolved` or
`accepted-risk` (none `open` or `unevaluated`-severity). Auto-inserted
on every adversarial arrow (§11).

**Arguments.**

| Name | Type | Required | Description |
|---|---|---|---|
| `arrow-id` | arrow-id | yes | The arrow to check |
| `severity-threshold` | severity | no | Default `medium`. Findings strictly below threshold do not need disposal. |

**Evaluator.** Reads the arrow's finding log. `pass = true` if no
finding at or above `severity-threshold` is `open` AND no finding has
`unevaluated`-severity. `details.blocking-findings = [{id, severity,
status}]`.

**Default cost.** `1`.

**Language binding.** None.

**Edge cases.**

- A finding at exactly `severity-threshold` IS blocking (the
  threshold is inclusive).
- A finding tagged with an old `grid-version` (carried over from an
  invalidated pass per D22) is treated normally — its severity and
  status apply regardless of the version tag.

---

### `cardinality-check`

**Purpose.** A named query returns exactly the declared cardinality.
Used for "exactly one bounded context" checks, "at most three open
findings of severity `high`", etc.

**Arguments.**

| Name | Type | Required | Description |
|---|---|---|---|
| `query` | string | yes | The query (regex / yaml-path / SQL-like over project state) |
| `query-target` | path-glob or `project-state` | yes | What to run query over |
| `expected` | int OR `[min, max]` | yes | Expected cardinality |

**Evaluator.** Runs `query` against `query-target`. `pass = true`
if the result count equals `expected` (or is within `[min, max]`).
`details = { actual, expected, results }`.

**Default cost.** `1`.

**Language binding.** None.

**Edge cases.**

- A query returning 0 results when `expected = 1` is the canonical
  failure mode. `details.actual = 0` and `results = []`.
- Queries with side effects are not allowed (the evaluator must be
  idempotent). Init should validate the query is read-only.

---

### `mode-determinable-from-repo`

**Purpose.** A named mode discriminator is determinable from the
current repo state without operator input. Used for
greenfield/brownfield detection at init.

**Arguments.**

| Name | Type | Required | Description |
|---|---|---|---|
| `discriminator` | string | yes | Identifier for the mode being checked (e.g., `"greenfield-vs-brownfield"`) |
| `rule` | command-or-script | yes | Returns mode value (string) on stdout |

**Evaluator.** Runs `rule`. `pass = true` if stdout is non-empty
and matches one of a declared set of valid modes.
`details.mode` is the determined value.

**Default cost.** `1`.

**Language binding.** None at concept level; `rule` is
project-declared as a shell command or script.

**Edge cases.**

- An `unknown` or empty result is failure, not a third mode. Init
  must declare a comprehensive rule.
- The rule must not require operator input; if it does, the mode
  is operator-attested, not machine-determined.

---

### `single-active-role-instance`

**Purpose.** No other active role traversal exists for the same
`(role, bounded-context)` tuple. Enforces D28 scope.

**Arguments.**

| Name | Type | Required | Description |
|---|---|---|---|
| `role` | role-id | yes | The role (incl. synthetic role-ids per §1.1) |
| `context` | bounded-context-id | yes | The bounded context |

**Evaluator.** Queries the harness's coordination layer (the lock /
lease registry). `pass = true` if no other pass exists with
matching `(role, context)` in `running` status.
`details.conflicting-pass-ids = [...]` if any.

**Default cost.** `0`.

**Language binding.** None — concept is harness-state.

**Edge cases.**

- A crashed pass that never recorded `aborted` could leave a stale
  lock. The coordination layer is expected to expire leases on
  liveness-check failure; this is implementation work (architect
  finding #11).
- Cross-context: a `(role, contextA)` pass and a `(role, contextB)`
  pass are NOT conflicting under this concept.

---

### `every-requirement-meets-min-depth`

**Purpose.** Every requirement on the arrow was classified at or
above its declared minimum depth by the adversarial phase's
depth-classification sub-activity (§11.1). Auto-inserted on every
adversarial arrow (D26).

**Arguments.**

| Name | Type | Required | Description |
|---|---|---|---|
| `arrow-id` | arrow-id | yes | The arrow under verification |

**Evaluator.** Reads the depth-classification output from the
adversarial phase. `pass = true` if every requirement's classified
depth >= its declared minimum.
`details.below-min = [{requirement-id, classified, minimum}]`.

**Default cost.** `0`.

**Language binding.** None — concept is adversarial-phase output.

**Edge cases.**

- If a requirement's classified depth is `unevaluated` (adversarial
  ran below required tier), the concept itself returns
  `unevaluated`, not `fail`. The arrow's status precedence (§7.2)
  handles this: an `unevaluated` clause makes the arrow
  `unevaluated`.
- A requirement with no declared minimum is treated as `min = 0`
  (NONE) — vacuously satisfied. Init should warn if many
  requirements have no declared minimum.

---

## Open questions

The following are deferred to implementation:

- **Tooling for `mutation-score` over `.feature` files.** Mutating
  Gherkin is non-standard. Likely punt: `mutation-score` is
  language-specific and not declared for Gherkin scopes; depth via
  `every-requirement-meets-min-depth` is the defense for Gherkin.
- **Cross-binding aggregation.** A scope spanning Go AND TypeScript
  needs to invoke both bindings and aggregate. Per-arrow clause
  instances can be split by language at init (one
  `mutation-score.go(...)`, one `mutation-score.typescript(...)`),
  but uniform aggregation would simplify init.
- **Concept versioning.** As the catalogue evolves, project's
  declared instances may reference older argument shapes. A
  concept-schema version field in `gates/concepts/<concept>.yaml`
  is likely needed.
