# ADR-006: Per-concept typed argument schemas

**Status:** Accepted (2026-05-18; D13)

**Context:**

Phase-3 architect-lens finding #1: "Per-arrow instances reference
catalogue concepts by name and supply arguments, but no concept's
argument signature is specified." Without typed signatures, the
catalogue is uncallable — an implementer cannot write
`Concept.evaluate(args)` because they don't know what `args` looks
like.

The choice was between:
- Per-concept schema files (introspectable, validatable).
- Hand-written signatures in harness source code (typed but not
  introspectable from outside).
- Uniform `args: map[string]any` blob with per-concept validation
  (ergonomic, not typed).

**Decision:**

Each of the 17 catalogue concepts has a **typed schema file** at
`gates/concepts/<concept-name>.yaml`. Schemas are shipped with the
harness and are introspectable.

Schema shape (illustrative):

```yaml
# gates/concepts/mutation-score.yaml
concept: mutation-score
description: Mutation testing reports a score above a declared threshold
arguments:
  scope:
    type: path-glob
    required: true
  threshold:
    type: number
    required: true
    range: [0.0, 1.0]
evaluator:
  contract: machine
  produces: { pass: boolean, score: number, killed: int, survived: int }
default-cost: 3
```

Per-arrow clause instances must validate against the schema; init
enforces this during auto-propose's "modify" path.

**Consequences:**

- **Rules in:** introspectable concept vocabulary (tooling can list
  all concepts and their arguments); typed evaluator-contract
  enforcement at the catalogue boundary; concept schemas as
  documentation that lives next to the implementation.
- **Rules out:** uniform-blob arguments; concept changes that bypass
  schema versioning; runtime discovery of "what arguments does
  concept X take" via reflection on Go code.
- **Maintenance burden:** each concept gets one YAML file
  (~17 files). Schema changes require harness-version coordination
  with existing project grids that reference the old schema. A
  concept-schema versioning field will likely be needed
  (`concepts.md` open question 3).
- **Type system:** common arg types — `path-glob`, `artifact-ref`,
  `language-id`, `severity`, `pass-result` — are reused across
  concepts (defined in `concepts.md` "Common types").

See `gates.md` §5.1; `specs/direction/components/concepts.md`;
`specs/v2/domain-model.md` §"Concept", §"ConceptSchema".
