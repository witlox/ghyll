# specs/

Authoritative behavioral specification for ghyll. One tree, one suite.

## Top-level documents

The canonical narrative docs:

- [`domain-model.md`](domain-model.md) — entities, aggregates,
  bounded contexts.
- [`ubiquitous-language.md`](ubiquitous-language.md) — shared vocabulary.
- [`invariants.md`](invariants.md) — what must always hold.
- [`assumptions.md`](assumptions.md) — assumptions the design rests on.
- [`failure-modes.md`](failure-modes.md) — how components fail and how
  the system absorbs those failures.
- [`cross-context.md`](cross-context.md) — interactions across bounded
  contexts.

## Sub-directories

- [`architecture/`](architecture/) — architectural design docs. Includes
  `v2-design.md` (the design rationale for the current correctness
  mechanism), `gates.md` (gate schema), `roles/` (the four diamond
  role contracts), `components/` (per-component designs).
- [`features/`](features/) — Gherkin acceptance scenarios, the BDD
  layer. Covers session, REPL, memory sync, dialect routing, plus
  the gate-and-arrow surface (init, runner, state-machine, attestation,
  amendment, adversarial).
- [`fidelity/`](fidelity/) — fidelity sweep records.
- [`findings/`](findings/) — audit findings.
- [`integration/`](integration/) — integration notes.
- [`archive/`](archive/) — historical artifacts preserved for context:
  - `direction/` — earlier draft round-decisions and architect findings.
  - `validation-passes/` — cold-read validation transcripts.
  - `v1-superseded/` — pre-v2 versions of the top-level narrative docs.

## Historical

- [`v2-final-plan.md`](v2-final-plan.md) — the consolidation plan that
  produced this tree. Kept for reference; not authoritative state.
