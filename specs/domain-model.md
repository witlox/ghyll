# Domain Model

The v2 ghyll codebase's internal domain. Reconciled to the schema in
`specs/architecture/gates.md` and the seven component specs in
`specs/architecture/components/`.

This is the implementation-facing domain model — the entities,
value objects, and aggregates the harness code will work with. The
*user-facing* domain (the projects ghyll runs on) is per-project; this
document describes ghyll's own internals.

---

## Bounded contexts of v2

ghyll's codebase factors into five bounded contexts. Each owns a
subset of the entities below.

| Context | Scope | Owns |
|---|---|---|
| **Grid Management** | Project initialization, amendment serialization, on-disk grid persistence | Project, BoundedContext, Grid, Amendment, GridFile |
| **Pass Execution** | Per-arrow pass lifecycle, clause evaluation, status derivation, transition refusal | Pass, ArrowInstance, ClauseStatus, ArrowStatus, the runner-owned `(role, context)` lock |
| **Adversarial Phase** | Per-arrow adversarial orchestration, fresh-context spawn, finding generation, remediation loop | AdversaryInstance, Finding (raised), RemediationRound |
| **Operator Interaction** | Operator sessions, hint presentation, attestation capture, the operator event bus | OperatorSession, AttestationRequest, AttestationRecord, OperatorEvent |
| **Catalogue** | The 18 catalogue concepts, per-concept schemas, language bindings | Concept, ConceptSchema, LanguageBinding, EvaluatorInvocation |

Cross-context interactions are documented in `specs/cross-context.md`
(future) and the per-component specs' "Cross-component interactions"
sections.

---

## Entities

Each has an identity (a key) that persists across the entity's
lifetime.

### Project

The top-level container. One per repository ghyll is invoked on.

- **Identity:** absolute path to the repository root.
- **Attributes:** `project-id` (string derived from path), `op-ids
  ever active`, `current-grid-version`.
- **Lifecycle:** created at init; persists until the operator removes
  the `.ghyll/` directory.
- **Owned by:** Grid Management.

### BoundedContext

A DDD bounded context within a project. Declared at init.

- **Identity:** `(project-id, bounded-context-id)`.
- **Attributes:** name (string, unique within project), description,
  source-of-truth directory (file glob).
- **Lifecycle:** created at init or via grid amendment; never deleted
  (only added to residue if no longer relevant).
- **Owned by:** Grid Management.

### Grid

A versioned arrow grid for a project. Each commit produces a new
grid version.

- **Identity:** `(project-id, grid-version: int)`.
- **Attributes:** `created-at`, `created-by-op-id`,
  `bounded-contexts: [BoundedContext]`, `language-bindings:
  map[concept-name][language] → binding`, `depth-ladder-labels: [4]`,
  `severity-threshold`, `insufficient-basis-rounds-max`,
  `remediation-rounds-max`, `arrows: [Arrow]`, `residue:
  [ResidueEntry]`.
- **Lifecycle:** v1 created at init; vN+1 created by grid amendment.
  Previous versions persist on disk for audit.
- **Persistence:** `.ghyll/grid.v<N>.yaml` + `.ghyll/grid.current`
  pointer (per D31).
- **Owned by:** Grid Management.

### Arrow

A declared transition between two roles for one
`(stratum, bounded-context)` cell at a specific grid version. Arrows
are the operators in the state-transition system (`gates.md` §0).

- **Identity:** `(role-pair, stratum, bounded-context, grid-version)`.
  Immutable. A new grid version produces new arrow identities for
  every dependent cell.
- **Attributes:** `producer-role` (which role-id produces the arrow's
  output), `exit-gate-clauses: [Clause]`, `dependencies:
  [Dependency]`, `output-artifact-location`.
- **Note:** init arrow's stratum and context fields are `null`
  (project-scoped per D41); all other arrows are
  `(stratum, bounded-context)`-scoped.
- **Owned by:** Grid Management (declaration); Pass Execution
  (status during a pass).

### Pass

One iteration of an arrow — one application of the operator. Per
`gates.md` §7.1a.

- **Identity:** `pass-id` (UUID or timestamp-based, unique
  per-arrow-traversal).
- **Attributes:** `arrow-id`, `started-at`, `completed-at`,
  `pass-status` (`running` / `completed` / `aborted`),
  `aborted-reason` (if applicable), `clause-statuses: map[clause-id]
  → ClauseStatus`, `findings-raised: [finding-id]`.
- **Lifecycle:** created when the runner spawns a traversal;
  `completed` or `aborted` is terminal; flushed to checkpoint log
  on terminalization.
- **Owned by:** Pass Execution (in-memory while `running`),
  checkpoint log (post-terminalization).

### Clause

An exit-gate clause on an arrow. Declared at init (auto-proposed from
role files) or at on-the-spot arrow creation.

- **Identity:** `(arrow-id, clause-id)`. The `clause-id` is from the
  role file (e.g., `analyst.G1`) or operator-assigned for extensions.
- **Attributes:** `description`, `concept-name` (machine) or
  `judgement-prose` (attested), `arguments`, `evaluation-type`
  (`machine` / `attested`), `depth-type` (`depth-robust` /
  `depth-sensitive`), `default-cost`, `per-arrow-cost-override`.
- **Per-pass status:** stored against `(pass-id, clause-id)` in the
  Pass aggregate. Independent of the clause's static declaration.
- **Owned by:** Grid Management (declaration), State Machine (status
  per pass).

### Finding

A defect raised by the adversarial phase or by a producer against
itself. Lives on the arrow artifact, not on a clause.

- **Identity:** `finding-id` (UUID).
- **Attributes:** `pass-id`, `arrow-id`, `type` (`clause-falsification`
  / `open-sweep` / `depth-below-min` / `unable-to-hint`),
  `severity` (`info | low | medium | high | critical` or
  `unevaluated`), `status` (`open` / `running` / `resolved` /
  `accepted-risk` / `unevaluated`), `target-clause` (if
  `clause-falsification`), `target-requirement` (if
  `depth-below-min`), `basis` (the rule by which the finding was
  raised), `locations`, `evidence`, `grid-version` (the version
  under which the finding was raised — needed for D22 preservation
  across invalidation aborts).
- **Lifecycle:** raised by adversary or self-finding; transitions per
  `gates.md` §7.3.
- **Owned by:** Adversarial Phase (creation), State Machine
  (transitions).

### Amendment

A request to change the arrow grid, triggered by an integrator's
`missing-cross-context-spec` finding.

- **Identity:** `amendment-id` (UUID).
- **Attributes:** `triggering-pass-id` (the integrator pass that
  raised it), `target-spec-artifacts: [artifact-ref]`,
  `target-grid-version: int` (the v(N+1) this amendment will
  produce), `proposed-changes`, `status` (`queued` / `committing` /
  `committed` / `failed`), `affected-arrow-ids: [arrow-id]`,
  `created-by-op-id`.
- **Lifecycle:** enqueued by the integrator; commits FIFO; producing
  v(N+1) is terminal.
- **Owned by:** Grid Management.

### OperatorSession

A logical session bound to an `op-id`.

- **Identity:** `(session-id, op-id)`.
- **Attributes:** `op-id`, `started-at`, `ended-at`, `verdict-count`.
- **Lifecycle:** created when the operator declares identity at init
  or session resume; ends explicitly or on harness termination.
- **Owned by:** Operator Interaction.

### AttestationRequest

A pending operator decision: a hint awaiting verdict.

- **Identity:** `(pass-id, clause-id, request-emission-time)`.
- **Attributes:** `clause-id`, `pass-id`, `hint`
  (`{locations, basis, residue}`), `severity-context`,
  `insufficient-basis-round-counter`.
- **Lifecycle:** created when the runner reaches an `attested` clause
  for a pass; resolved when the operator returns a verdict; expires
  on pass abort.
- **Owned by:** Operator Interaction.

### AttestationRecord

One JSONL line in a per-pass attestation file. Append-only.

- **Identity:** `(grid-version, bounded-context, stratum, role-pair,
  pass-id, line-number)`.
- **Attributes:** `unit` (`confirm` / `record-locations-inspected` /
  `write-residue-note`), `clause`, `verdict`, `ts`, `op-id`,
  `inspected` (if unit ≥ `record-locations-inspected`),
  `residue-note` (if unit = `write-residue-note`).
- **Persistence:** `attestations/v<N>/<context>/stratum-<S>/<role-pair>/<pass-id>.jsonl`
  with `__` separating role-ids in the path (per D37).
- **Owned by:** Operator Interaction.

### Concept

A catalogue entry. Closed at the concept layer (language-agnostic).

- **Identity:** `concept-name` (string from the closed 18-entry set).
- **Attributes:** `arguments-schema`, `evaluator-contract`,
  `default-cost`, `language-bound: bool`.
- **Persistence:** shipped with the harness as
  `gates/concepts/<concept-name>.yaml`.
- **Owned by:** Catalogue.

### LanguageBinding

Per-concept per-language evaluator configuration. Declared at init.

- **Identity:** `(concept-name, language-id)`.
- **Attributes:** for `compiles`/`lint-clean`-style: `command`. For
  `no-orphan-symbol`-style: `{extractor, index, mapper}` per D44.
- **Persistence:** in the project's grid file as part of
  `language-bindings`.
- **Owned by:** Catalogue (definition); Pass Execution (runtime
  invocation).

---

## Value objects

Defined entirely by attributes; no identity.

### Stratum

One of six uniform layers per `gates.md` §3.1.

```
1 = Structure
2 = Invariants
3 = Behavior
4 = Composition
5 = Failure
6 = Assumptions/risk
```

### Severity

Enum: `info | low | medium | high | critical`. Plus `unevaluated`
when severity assignment depth-below-required.

### DepthTier

Integer `0..3` with project-configured labels (default: `NONE` /
`SHALLOW` / `MOCKED` / `REALISTIC` per `gates.md` §11.1).

### EvaluationType

Enum: `machine | attested`. Per `gates.md` §5.

### DepthType

Enum: `depth-robust | depth-sensitive`. Per `gates.md` §6.

### ClauseStatus

Enum (per `gates.md` §7.1):
`pending | running | pass | fail | awaiting-attestation | insufficient-basis | unevaluated`.

`unevaluated` carries a `reason` field:
`depth-below-required | no-rule-selectable-locations | producer-no-response`.

### ArrowStatus

Enum (per `gates.md` §7.2, derived per precedence):
`complete | provisional | unevaluated | blocked | invalidated`.

### FindingStatus

Enum (per `gates.md` §7.3):
`open | running | resolved | accepted-risk | unevaluated`.

### PassStatus

Enum (per `gates.md` §7.1a):
`running | completed | aborted`.

`aborted` carries a `reason` field:
`invalidated | operator-interrupt | crash | manual-stop | requires-deeper-artifact`.

### OperatorActionUnit

Three-element enum with associated cost (per `gates.md` §10.1):

| Unit | Cost (action units) |
|---|---|
| `confirm` | 1 |
| `record-locations-inspected` | 3 |
| `write-residue-note` | 5 |

### Hint

Structured pointer: `{clause-id, locations: [file:line-range],
basis: string, residue: string}` per `gates.md` §9. Emitted by
producer roles for each `attested` clause.

### Dependency

A declared dependency from an arrow to an upstream spec artifact.
Per D16:

```
{ artifact: <path>, granularity: file|section|clause-id, on-change: invalidate|reevaluate-named-clauses }
```

### ResidueEntry

A declared `(stratum, bounded-context)` cell the operator chose not
to declare an arrow for.

```
{ cell: {stratum, bounded-context}, reason: string, imputed-cost: int }
```

### OperatorEvent

A typed message on the operator event bus (per D43):
- `attestation-request: {pass-id, clause-id, hint}`
- `escalation-request: {pass-id, clause-id, reason}`
- `refusal-prompt: {project-profile, rationale}`
- `pass-aborted: {pass-id, reason}`
- `amendment-conflict: {grid-versions}`
- `init-prompt: {step, question}`

### ProducerMessage

Three typed messages between producer roles and the adversarial
orchestrator (per D40):
- `hint-request: {pass-id, clause-id, upstream-artifact-ref}`
- `producer-fix-signal: {pass-id, addressed-findings}`
- `accepted-risk-proposal: {pass-id, finding-id, rationale, inspected-context}`

---

## Aggregates

A consistency boundary: operations on the aggregate are atomic and
must preserve its invariants.

### Grid (aggregate root)

`Grid` → `[Arrow]` → `[Clause]`.

- The grid is written atomically on commit (D31 — temp + rename +
  pointer update).
- All arrows within a grid version share that version. A new version
  produces new arrow identities.
- Invariant: every `(stratum, context)` cell either has at least one
  arrow OR a residue entry. No silent omissions.

### Pass (aggregate root)

`Pass` → `map[clause-id] → ClauseStatus` → `[finding-id-raised]`.

- A pass's clause statuses are updated via the state-machine engine
  (which owns the per-clause transition lock).
- A pass terminalizes in one operation (writes to checkpoint log
  atomically; pass-status becomes `completed` or `aborted`).
- Invariant: clause statuses can only transition along the §7.1
  state machine; illegal transitions are rejected.

### FindingSet (per arrow, across passes)

The set of findings raised against one arrow's traversals.

- Updated by the adversarial phase (creation) and the state machine
  (transitions).
- Findings preserve their `grid-version` even across `invalidated`
  aborts (D22).

### AttestationFile (per pass)

`AttestationFile` → `[AttestationRecord]`.

- Append-only.
- One file per pass at the §10.2 path.
- Invariant: every record has a non-empty `op-id` and matches a
  declared unit's shape (D44 verification).

### Catalogue (project-wide constant + per-project bindings)

`Catalogue` → `[Concept]` (constant, ships with harness).
`Catalogue` → `[LanguageBinding]` (per-project, declared at init).

- Concepts are immutable after harness compile.
- Bindings can be added at init re-entry (D18) but never removed
  mid-project.

---

## Roles

ghyll runs four diamond roles plus two synthetic role-ids.

### Diamond roles (have role files in `specs/architecture/roles/`)

| Role | Role file | Produces |
|---|---|---|
| analyst | `roles/analyst.md` | Specs (domain model, invariants, features, etc.) for one bounded context |
| architect | `roles/architect.md` | Architecture (modules, interfaces, contracts, error model) for one bounded context |
| implementer | `roles/implementer.md` | Code + tests for one bounded context |
| integrator | `roles/integrator.md` | Integration findings (typed: local-bug or missing-cross-context-spec); may trigger grid amendments |

### Synthetic role-ids (no role files; identities for paths/attestation)

| Role-id | Use |
|---|---|
| `init` | Producer for the initialization arrow's clauses. Bound to a fresh harness instance at project start. |
| `adversary` | Per-arrow adversarial phase producer. Bound to a fresh model instance with clean context per round. |

The role-id is what appears in attestation-path encoding (`__`
separator per D37): e.g., `init__analyst`,
`analyst__adversary__architect`.

---

## Catalogue (18 concepts)

Per `specs/architecture/components/concepts.md`, the closed
language-agnostic catalogue (ADR-005 + ADR-013):

**Universal base set** (auto-applied to every arrow):
`compiles`, `lint-clean`, `no-todo-marker`, `every-step-bound`.

**Auto-inserted on adversarial arrows** (verification phase):
`no-open-finding`, `every-requirement-meets-min-depth`.

**Per-arrow declared at init:**
`no-orphan-symbol`, `mutation-score`, `tests-pass`,
`kill-server-fails-integration`, `trace-link-present`,
`acyclic-dependency-graph`, `unique-definition`, `predicate-form`,
`arrow-artifact-present`, `cardinality-check`,
`mode-determinable-from-repo`, `single-active-role-instance`.

Each concept has a typed schema at `gates/concepts/<name>.yaml`
(shipping with the harness; design in `components/concepts.md`).

---

## Locks (concurrency primitives)

Three locks, three owners. Per D34.

| Lock | Owner | Scope | Used by |
|---|---|---|---|
| Per-clause transition lock | State Machine engine | `(pass-id, clause-id)` | All callers proposing clause-status transitions |
| Per-`(role, context)` lock | Runner | `(role-id, bounded-context-id)`; expires on pass termination | Pre-spawn check enforcing `single-active-role-instance` |
| Project-wide grid write-lock | Amendment component | Project | Held during grid v(N+1) commit; init holds at end of init for v1 write |

---

## State-space model (reference)

ghyll's runtime is a state-transition system over an extensible grid
per `gates.md` §0:

- **Cells** = `(stratum, bounded-context)` pairs holding clause
  statuses, findings, derived arrow status.
- **Operators** = arrows (the transition functions).
- **Iterations** = passes (one application of an operator).
- **Invalidation** = an operator that resets cells.
- **Grid amendment** = an operator that extends the state space
  (adds dimensions / cells).
- **Fixed point** = every cell in vN at arrow status `complete`,
  R=0, C=0.

The domain model above is the data-shape view; the state-space frame
is the *semantic* view. Both must agree at every point.

---

## File layout (on disk)

```
.ghyll/
├── grid.v1.yaml             # grid versions; immutable after write
├── grid.v2.yaml
├── ...
├── grid.current             # one line: "v<N>" (the active version)
├── attestations/
│   └── v<N>/
│       └── <context>/
│           └── stratum-<S>/
│               └── <role-pair-with-__-separator>/
│                   └── <pass-id>.jsonl
├── checkpoint-log/          # Merkle DAG checkpoint chain (v1 infra reused)
│   └── ...
└── lock/                    # coordination state (D34)
    ├── grid-write.lock
    ├── role-context-<role>-<context>.lock
    └── clause-<pass-id>-<clause-id>.lock
```

The harness ships `gates/concepts/*.yaml` separately (inside the
binary or alongside it; not in `.ghyll/`).
