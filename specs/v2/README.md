# specs/v2/

Implementation specs for ghyll v2 (the gate-enforcement coding agent).

Distinct from:

- **`specs/`** (root) — v1 specs that document the **currently
  shipping** ghyll codebase (dialects, drift-aware memory, streaming).
  Will be retired as v2 reaches feature parity.
- **`specs/direction/`** — v2 design history: the gate schema, role
  contracts, component specs, validation passes, operator-decision
  rounds (D1–D44). The *why* behind v2.

This directory is the v2 *what*: domain model, invariants, features,
failure modes — the analyst-role outputs that the v2 implementation
will be built and tested against.

## Files

- **[domain-model.md](domain-model.md)** — v2 internal domain
  (entities, value objects, aggregates, the five internal bounded
  contexts, the catalogue, the role identities, on-disk layout).
- **[invariants.md](invariants.md)** — 64 invariants consolidated
  from gates.md + component specs, sourced and grouped by topic.
- **[failure-modes.md](failure-modes.md)** — 57 failure modes from
  the component-spec FM tables, grouped by topic (process, I/O,
  concurrency, state-machine, adversarial, operator, schema).
- **[ubiquitous-language.md](ubiquitous-language.md)** — v2 glossary
  with disambiguation tables for confusable pairs.
- **[cross-context.md](cross-context.md)** — inter-component
  interaction graph; per-component interfaces; shared substrates;
  cross-component invariants; concurrency resolutions.
- **[features/](features/)** — Gherkin scenarios for the v2
  acceptance suite (~140 scenarios across 6 components), with the
  adversarial-pass findings and additions recorded.
- **[decisions/](decisions/)** — 12 ADRs distilling the 44 operator
  decisions into structural records.

## Path forward

v2 specs are being written incrementally. When the corresponding v2
*code* lands and reaches feature parity with v1, the cleanup of v1
specs/tests will be a separate, careful operation — not a one-shot
deletion.
