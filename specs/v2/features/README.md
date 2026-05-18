# specs/v2/features/

Gherkin feature files for v2 ghyll's acceptance suite. One file per
component, with all scenarios for that component grouped under one
`Feature:` declaration (proper Gherkin convention).

Extracted from `specs/direction/components/*.md` per round-5
reconciliation. The component spec markdown remains the design
narrative; these `.feature` files are the executable contract.

## Files

- **[init.feature](init.feature)** — Project initialization
  (~20 scenarios across greenfield, brownfield, re-init on missing
  binding, refusal, auto-propose+confirm, atomic grid write, op-id
  session declaration).
- **[runner.feature](runner.feature)** — Machine-clause runner
  (~22 scenarios across single-clause evaluation, attested-clause
  coordination, arrow-status derivation, transition refusal,
  verification phase, concurrent execution, checkpoint emission).
- **[state-machine.feature](state-machine.feature)** — Status state
  machine engine (~19 scenarios across clause/arrow/finding/pass
  lifecycles, project-level status query, snapshot/replay).
- **[adversarial.feature](adversarial.feature)** — Per-arrow
  adversarial phase (~16 scenarios across initial attack,
  clause-falsification, open sweep, depth classification,
  remediation loop, phase exit).
- **[amendment.feature](amendment.feature)** — Grid amendment and
  global lock (~12 scenarios across single-amendment commit,
  dependency-check, atomic write, concurrent amendments, grid
  version visibility).
- **[attestation.feature](attestation.feature)** — Operator
  attestation flow (~15 scenarios across session lifecycle, hint
  presentation, insufficient-basis escalation, accepted-risk for
  findings, verifier-driven replay).

## Naming and granularity

One `.feature` per component, multiple `Scenario:` blocks within
each file. The internal F-N sub-groupings in the component specs
become comment headers (`# ---- F-N name ----`) within the
`Feature:` block — useful for navigation but not Gherkin-syntactic.

## Status

These features are the **specification**, not yet wired to step
definitions or an acceptance runner. The corresponding step files
will live at `tests/v2/` (or wherever the v2 implementation places
them) once the v2 code lands.

The v1 acceptance suite at `tests/acceptance/` remains in place
running against `specs/features/` (v1 BDD scenarios). v1 and v2
test suites coexist until v2 reaches feature parity.
