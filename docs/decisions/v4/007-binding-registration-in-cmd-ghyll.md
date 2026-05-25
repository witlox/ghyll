# ADR-v4-007: Language-binding registration lives in `cmd/ghyll` (integration layer)

**Status:** Accepted (2026-05-25)

**Context:**

`registerGridBindings(reg *runner.Registry, grid *bootstrap.Grid,
workdir string) error` is the operation that populates the
evaluator registry from a grid's `language-bindings:` declarations.
It needs:

- `runner.Registry` (mutates the live registry).
- `bootstrap.Grid` (source of binding declarations).
- `runner.NewBindingEvaluator` (constructs the subprocess
  evaluator).
- `bootstrap.BindingKeysFromStrings` (parses the
  `<concept>.<language>` keys).

The natural location would be `runner/`, but `bootstrap` already
imports `runner` (`bootstrap/init_attestations.go` references
`runner.AttestationRecord`). Adding the inverse edge — `runner →
bootstrap` — creates a Go import cycle. R1 of the v2 adversarial
flagged this as a critical compile-time failure.

**Decision:**

`registerGridBindings` and its sibling helpers
(`requiredBindingsFromTypedGrid`, `requiredBindingsFromUntypedGrid`,
`buildRegistryOverlay`, `composeBootstrapOverlay`) live in
`cmd/ghyll/binding_register.go` (package `main`). Rationale:

- `cmd/ghyll` is the integration boundary — it already imports
  BOTH `runner` AND `bootstrap`.
- `cmd/ghyll` is a leaf package — no production code imports it,
  so the new edges cannot cycle.
- The helpers are package-local; the runtime construction site
  (`openEngineWithOptions` and the amendment-driven re-register
  callback `buildRegistryOverlay`) lives in the same package.

Alternatives considered + rejected:

- **New `wiring/` package**: would still need to be imported by
  `cmd/ghyll` to be called. Splits the seam without resolving the
  cycle.
- **`RegistryInterface` in `bootstrap/`**: moves binding logic
  AWAY from `runner` where the `Registry` type lives. Worse
  locality.
- **Breaking the bootstrap → runner edge** (relocating
  `init_attestations.go`'s AttestationRecord references): large
  structural change far beyond v4's scope.

**Consequences:**

- The integration site is the single place where binding-registration
  policy lives. A future refactor that wants to make this reusable
  outside `cmd/ghyll` lifts both helpers and their tests as a unit.
- The arrow-overlay conversion
  (`runner.ArrowDefinition → map[string]any` for the in-memory
  bootstrap.Grid overlay) ships as `cmd/ghyll/arrow_marshal.go` with
  a round-trip test (R3 closure).
- The amendment-driven re-register callback
  (`AmendmentCommitter.BindingsReRegister`) is a function-typed
  field on the committer; the integration site wires it to
  `engineRuntime.buildRegistryOverlay`.
