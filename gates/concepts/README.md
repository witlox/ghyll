# gates/concepts/

The 17 catalogue-concept schemas that ship with the harness. Closed
vocabulary; new concepts enter only via deliberate harness changes
(per ADR-005).

Each schema is YAML. The shape is fixed per ADR-006:

- `concept` — the name (matches filename without `.yaml`).
- `description` — one-line.
- `language-bound` — boolean. If true, the project must declare a
  per-language binding at init for each language used.
- `arguments` — typed argument schema. Each arg has `type`,
  `required`, optional `default`, and an inline `description`.
- `evaluator` — contract (`machine` for all 17) and `produces`
  (the result shape, with `pass: boolean` and concept-specific
  `details`).
- `default-cost` — the operator-action cost for clauses using this
  concept (per ADR-005). 0 for cheap deterministic checks; up to
  3 for slow/expensive ones; can be raised per-arrow but not
  lowered.
- `edge-cases` — notes for implementers and operators.

## The 17 concepts, grouped

### Universal base set (auto-applied to every arrow per gates.md §5.2)

| Concept | Default cost | Language-bound |
|---|---|---|
| [compiles](compiles.yaml) | 0 | yes |
| [lint-clean](lint-clean.yaml) | 0 | yes |
| [no-todo-marker](no-todo-marker.yaml) | 0 | no |
| [every-step-bound](every-step-bound.yaml) | 0 | yes |

### Auto-inserted on adversarial arrows (verification phase per gates.md §11.3)

| Concept | Default cost | Language-bound |
|---|---|---|
| [no-open-finding](no-open-finding.yaml) | 1 | no |
| [every-requirement-meets-min-depth](every-requirement-meets-min-depth.yaml) | 0 | no |

### Per-arrow declared at init (the rest)

| Concept | Default cost | Language-bound | Purpose |
|---|---|---|---|
| [no-orphan-symbol](no-orphan-symbol.yaml) | 1 | yes | Bidirectional trace: every entity in scope traces to a counterpart in spec/code. |
| [mutation-score](mutation-score.yaml) | 3 | yes | Mutation kill rate above threshold; defends shallow tests. |
| [kill-server-fails-integration](kill-server-fails-integration.yaml) | 3 | no | Integration tests fail when critical dep removed; defends against fully-mocked suites. |
| [trace-link-present](trace-link-present.yaml) | 1 | no | A declared link from one artifact to another exists with declared multiplicity. |
| [acyclic-dependency-graph](acyclic-dependency-graph.yaml) | 0 | yes | Module / call / type graph is acyclic. |
| [unique-definition](unique-definition.yaml) | 0 | no | A field's values are unique (clause-IDs, terms in ubiquitous-language, etc.). |
| [predicate-form](predicate-form.yaml) | 1 | no | Entries are assertable predicates, not prose. |
| [arrow-artifact-present](arrow-artifact-present.yaml) | 0 | no | The arrow's declared output exists and (optionally) validates against a schema-check. |
| [cardinality-check](cardinality-check.yaml) | 1 | no | A named read-only query returns the expected cardinality (exact or range). |
| [mode-determinable-from-repo](mode-determinable-from-repo.yaml) | 1 | no | A discriminator (e.g., greenfield/brownfield) is determinable from repo state without operator input. |
| [single-active-role-instance](single-active-role-instance.yaml) | 0 | no | No other active pass exists for the same (role, bounded-context) tuple. |

## Common types referenced across schemas

- **`path-glob`** — POSIX glob string (e.g., `"src/**/*.go"`).
- **`artifact-ref`** — path, path+section, or clause-id (per D11 hybrid IDs).
- **`language-id`** — `"go"`, `"rust"`, `"typescript"`, `"python"`, `"gherkin"`, etc.
- **`severity`** — `info | low | medium | high | critical`.
- **`depth-tier`** — int 0..3 with project-overridable labels (default `NONE | SHALLOW | MOCKED | REALISTIC`).
- **`role-id`** — diamond role (analyst/architect/implementer/integrator) OR synthetic (init/adversary).
- **`bounded-context-id`** — operator-declared context name; unique within project.
- **`pass-id`** — UUID or timestamp-based unique identifier.
- **`arrow-id`** — `(role-pair, stratum, bounded-context, grid-version)` tuple.
- **`dependency-id`** — operator-declared external dependency name.
- **`finding-status`** — `open | running | resolved | accepted-risk | unevaluated`.

## Status

These schemas are **data**, not code. They are the design output of
operator-decisions rounds 1–5 (especially D1, D2, D13, D33).
Implementation of the loader / validator / dispatcher that consumes
them is the next step in the build (per `specs/direction/build-notes.md`).
