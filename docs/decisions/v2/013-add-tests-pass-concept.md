# ADR-013: Add `tests-pass` to the catalogue (catalogue grows to 18)

**Status:** Accepted (2026-05-18; cold-validation pass on role files)

**Context:**

While implementing the v2 init component's auto-propose loop, the
role-file parser surfaced that `implementer.md` clause G1 ("All test
suites pass") referenced an informal cell — `(test-runner check;
per-language binding)` — rather than a catalogue concept. Inspection
of the 17-concept closed catalogue confirmed the gap:

- `compiles` covers parse/build, not test execution.
- `mutation-score` measures the *quality* of tests that already pass;
  it presupposes tests run.
- `kill-server-fails-integration` checks that integration tests
  would notice a dependency outage, not that they currently pass.
- `lint-clean`, `no-todo-marker`, `every-step-bound` — none touch
  test results.

The catalogue genuinely lacked an abstraction for "the project's
test suite runs and is green". Since implementer's exit gate is the
strongest gate against shipping broken behavior, leaving its G1
informal would mean the harness cannot machine-verify the most
obvious quality signal it has.

ADR-005's "Catalogue maintenance" consequence explicitly anticipates
this case: *"new concepts require deliberate harness changes. Adding
to it is a versioned harness release."* This ADR is that deliberate
addition.

**Decision:**

Add a single new concept, `tests-pass`, to the catalogue. The closed
set is now **18 concepts**. The new concept's category is
**per-arrow declared at init** (the same category as `mutation-score`,
`lint-clean`, etc. — not auto-applied, not auto-inserted).

Schema highlights (full schema in `gates/concepts/tests-pass.yaml`):

- `language-bound: true` — each project supplies a per-language test
  runner binding (e.g., `tests-pass.go = go test ./...`).
- Arguments: `scope` (path-glob of tests), `language` (id), `timeout`
  (duration, default 5m).
- Evaluator contract: `machine`. Produces `pass: boolean` plus
  `passed/failed/skipped/timed-out` counts and a truncated failure
  list.
- `default-cost: 2`.

`implementer.md` G1 is updated to use the new concept:
`` `tests-pass`(scope, language) ``.

**Consequences:**

- **Rules in:** test-run signal becomes a first-class machine clause;
  implementer's exit gate is now fully machine-verifiable at the
  required-clauses tier.
- **Rules out:** future role files referencing "tests pass" via
  prose instead of the concept; the parser refuses informal cells.
- **Binding obligation:** every project that uses an implementer
  arrow now declares a `tests-pass.<lang>` binding alongside its
  `lint-clean.<lang>`, `compiles.<lang>`, etc. bindings. Missing
  binding triggers init re-entry per ADR-005 + D18.
- **Edge case (vacuous pass):** a scope matching zero tests returns
  `pass=false`. Vacuous truth ("we have no tests, therefore all
  tests pass") was the failure mode that motivated this concept;
  the schema refuses it explicitly.
- **Flakiness deferred:** `tests-pass` records the first result; it
  does not retry. Flake mitigation lives in a future ADR or in the
  binding (which may wrap a retry).

This addition does not weaken ADR-005's closure model — the
catalogue remains closed, just one entry larger. Future additions
follow the same harness-versioned path.

See `gates/concepts/tests-pass.yaml`,
`specs/architecture/roles/implementer.md` G1,
`docs/decisions/v2/005-concept-catalogue.md`.
