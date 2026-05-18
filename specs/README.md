# specs/

Two distinct bodies of spec live here.

## v1 — current code (continuity infrastructure)

Describes the ghyll v1 codebase that ships today: dialects, drift-aware
memory, streaming, Merkle DAG checkpoints, team memory. Per the v2
pivot, v1 is **continuity infrastructure** rather than the
correctness mechanism, but the code still does what these files
describe.

Top-level v1 files (each carries a one-line banner):

- [`domain-model.md`](domain-model.md) — entities, aggregates,
  bounded contexts for v1.
- [`ubiquitous-language.md`](ubiquitous-language.md) — v1 vocabulary.
- [`invariants.md`](invariants.md) — what must always be true in v1.
- [`assumptions.md`](assumptions.md) — explicit assumptions v1 rests on.
- [`failure-modes.md`](failure-modes.md) — how v1 components fail.

Sub-directories:

- [`architecture/`](architecture/) — v1 architecture documents
- [`cross-context/`](cross-context/) — v1 cross-context interactions
- [`features/`](features/) — v1 BDD scenarios (Gherkin)
- [`fidelity/`](fidelity/) — v1 fidelity sweep records
- [`findings/`](findings/) — v1 audit findings
- [`integration/`](integration/) — v1 integration notes

## v2 — design intent (gate enforcement)

The v2 pivot is in [`direction/`](direction/). v2 is **not yet
built**; the design is the work that has happened so far.

Read order in `direction/`:

1. `direction.md` — the v2 positioning rationale.
2. `gates.md` — the harness-wide gate schema (the meta-design).
3. `roles/{analyst,architect,implementer,integrator}.md` — the four
   role contracts.
4. `build-notes.md` — what is designed vs. not yet built.

Supporting docs (validation passes + operator-decision rounds 1-4)
are listed in [`direction/README.md`](direction/README.md).

Component-level specs for v2 (init, machine-clause runner, etc.) are
in progress under `direction/components/`.
