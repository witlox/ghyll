# ADR-005: Concept-named catalogue with per-language bindings

**Status:** Accepted (2026-05-18; D1, D2, D18)

**Context:**

The initial machine-clause catalogue contained nine names including
`clippy-clean` — a Rust-specific tool. For a multi-language coding
agent, baking specific instrument names into the catalogue
contradicts the "language-agnostic" claim. The catalogue needed
to separate *concept* (what property is asserted) from *instrument*
(what tool decides the property).

Additionally, the original analyst role file's machine clauses
referenced operations not in the catalogue (mode-determinable-from-repo,
no-open-divergence, arrow-artifact-present, unique-definition), so
the catalogue had to grow to accommodate them.

**Decision:**

The catalogue is **closed at the concept layer** and
**language-agnostic**. Concepts name *what property* is asserted;
per-language **instrument bindings** decide *what tool runs the
check*.

The closed set of 17 concepts:

**Universal base** (auto-applied):
`compiles`, `lint-clean`, `no-todo-marker`, `every-step-bound`.

**Auto-inserted on adversarial arrows**:
`no-open-finding`, `every-requirement-meets-min-depth`.

**Per-arrow declared at init**:
`no-orphan-symbol`, `mutation-score`,
`kill-server-fails-integration`, `trace-link-present`,
`acyclic-dependency-graph`, `unique-definition`, `predicate-form`,
`arrow-artifact-present`, `cardinality-check`,
`mode-determinable-from-repo`, `single-active-role-instance`.

Language bindings:

- The harness ships **NO language bindings**.
- Each project declares its bindings at init time
  (`lint-clean.go = staticcheck && go vet`,
  `mutation-score.rust = cargo-mutants`, etc.).
- If a needed binding is absent at any later point, the harness
  suspends and re-enters init to obtain it (D18).

**Consequences:**

- **Rules in:** language-agnostic concept vocabulary; per-language
  customization that is operator-declared (not harness-assumed);
  growth path for the catalogue is harness-version-bumped, not
  per-project-extended.
- **Rules out:** instrument-specific concept names; silent fallback
  when a binding is absent; per-project catalogue extensions (no
  custom concepts without harness changes).
- **Friction:** every project pays the binding-declaration tax at
  init. No "works out of the box for Go" shortcut. This is
  deliberate — declared bindings make assumptions visible.
- **Catalogue maintenance:** new concepts require deliberate
  harness changes. Catalogue is the harness's primitive vocabulary;
  adding to it is a versioned harness release.

See `gates.md` §5.1, §5.2; `specs/v2/domain-model.md` §"Catalogue";
`specs/v2/ubiquitous-language.md` §E.
