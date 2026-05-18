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
- **invariants.md** *(in progress)* — consolidated invariants from
  schema + component specs.
- **features/** *(in progress)* — Gherkin scenarios extracted from
  `specs/direction/components/*.md` into standalone `.feature` files
  for the v2 acceptance suite.
- **failure-modes.md** *(planned)* — consolidated failure modes.
- **ubiquitous-language.md** *(planned)* — v2 glossary.
- **cross-context.md** *(planned)* — v2 cross-component interactions.

## Path forward

v2 specs are being written incrementally. When the corresponding v2
*code* lands and reaches feature parity with v1, the cleanup of v1
specs/tests will be a separate, careful operation — not a one-shot
deletion.
