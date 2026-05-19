# specs/

Authoritative behavioral specification for ghyll. Post-D-3 consolidation
the v1↔v2 split is collapsed — there is one tree.

## Top-level documents

The five canonical narrative docs (v2 framing — v1 originals are preserved
in `archive/v1-superseded/`):

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
  `v2-design.md` (the pivot rationale), `gates.md` (gate schema),
  `roles/` (the four diamond role contracts), `components/` (per-component
  designs).
- [`features/`](features/) — Gherkin acceptance scenarios, the BDD layer.
  v1-inherited surface features plus the v2-only ones (init, runner,
  state-machine, attestation, amendment, adversarial, runner-step3).
- [`fidelity/`](fidelity/) — fidelity sweep records.
- [`findings/`](findings/) — audit findings.
- [`integration/`](integration/) — integration notes.
- [`archive/`](archive/) — historical artifacts preserved for context:
  - `direction/` — earlier draft round-decisions and architect findings.
  - `validation-passes/` — cold-read validation transcripts.
  - `v1-superseded/` — pre-v2 versions of the top-level narrative docs.

## Working documents

- [`v2-final-plan.md`](v2-final-plan.md) — the six-phase consolidation
  plan that produced this tree.
