# ADR-v4-006: `EvaluatorWithRunner` variant + two-table Registry lookup

**Status:** Accepted (2026-05-25)

**Context:**

The `single-active-role-instance` evaluator needs read access to the
live `*PassRegistry` (to count open passes on a (role, context)
tuple). The existing `Evaluator = func(ctx, c Clause) (*Result, error)`
signature has no path for this — passing the registry through
`Clause.Args` is type-unsafe and would bypass the registry's locks.

R9 of the v2 adversarial flagged that the v1 "EvaluatorWithRunner
wraps Lookup" handwave was mechanically incorrect; `Runner.Evaluate`
calls `Registry.Lookup(c.Concept)` and the runner-typed variant
cannot be returned through that shape.

**Decision:**

Two-table Registry lookup:

- `Registry.Register(concept, Evaluator)` + `Registry.Lookup(concept)`
  is the plain table (existing).
- `Registry.RegisterWithRunner(concept, EvaluatorWithRunner)` +
  `Registry.LookupWithRunner(concept)` is the runner-typed table
  (new).
- A concept is registered in AT MOST ONE table. Registering in the
  second after the first returns `ErrConceptAlreadyRegistered`.

`Runner.Evaluate` tries `LookupWithRunner` first; on miss, falls back
to `Lookup`; on second miss, returns `ErrEvaluatorUnknown`.

Only one concept uses the new variant today:
`single-active-role-instance`. Adding a future runner-typed
evaluator means: declare the function with the new signature,
register via `RegisterWithRunner`.

**Consequences:**

- `Runner.passes *PassRegistry` is a new unexported field;
  `NewRunner(reg *Registry, passes *PassRegistry, tier DepthRank)`
  is the new constructor (replaces the prior 1-arg form).
- 37 call sites updated to pass the new args (`nil` and
  `DepthRankNone` are valid defaults for tests that don't exercise
  the runner-typed dispatch).
- `Registry.Count()` reports the union of both tables — the
  cardinality assertion in `RegisterBuiltins` counts 11 across the
  two tables.
- `Registry.Snapshot()` / `SwapInto()` (per ADR-v4-003) duplicate
  both tables in lock-step.
