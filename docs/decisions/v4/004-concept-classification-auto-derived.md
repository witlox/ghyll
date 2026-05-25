# ADR-v4-004: Concept classification auto-derived from embedded YAMLs

**Status:** Accepted (2026-05-25)

**Context:**

The runner needs to ask "is concept X language-bound?" at dispatch
time (to compute the registry key, to know whether to expect a Go
evaluator or a `BindingEvaluator`). Two implementation options:

1. A hand-maintained constant table in `runner/`.
2. Auto-derive from the embedded `gates/concepts/*.yaml` schemas at
   package init.

The hand-maintained table drifts the moment a new concept lands. v1
shipped both copies and the runner's was out of date. The v2 first
revision proposed `gates.ConceptsFS`, which doesn't exist.

**Decision:**

Auto-derive at package init via the existing
`catalogue.LoadEmbedded()` entry point (which wraps the root
`ghyll.ConceptsFS` embed). Two assertions hold:

- `len(universalConcepts) + len(languageBoundConcepts) == 18`
- `len(universalConcepts) == 11`

Either invariant failure panics at startup — loud, not silent.

The 11/7 split is load-bearing per the v4 diamond contract:

- 11 universals MUST have an in-process Go evaluator in
  `RegisterBuiltins`.
- 7 language-bound concepts MUST resolve to a project-declared
  `BindingEvaluator` (registered by `registerGridBindings` in
  `cmd/ghyll`).

**Consequences:**

- `runner → catalogue` is a new import edge. Verified safe:
  `catalogue` imports the root `ghyll` package only; no cycle.
- Adding a new concept YAML breaks the cardinality assertions until
  the implementer updates them — by design, the count is a contract
  that travels with the runtime.
- The cardinality bands cover the load-bearing 11-universals
  invariant independently of the 18-total (R19 closure): a
  hypothetical shift of one concept from universal to language-bound
  would slip past a single-count check, hence the dual assertion.
