# specs/archive/

Historical artifacts preserved for context. Nothing here is the
authoritative current spec; canonical specs live one level up.

## Sub-directories

- `direction/` — earlier v2-direction working documents:
  the five `operator-decisions-round-N.md` debate transcripts,
  `phase-3-architect-findings.md`, and the iterative `build-notes.md`.
  Distilled outputs live in `docs/decisions/v2/` (the v2 ADR series)
  and in `specs/architecture/` (the consolidated design).
- `validation-passes/` — cold-read validation transcripts:
  `validation-pass-{1,2,3}.md` (design-phase) and
  `validation-impl-pass-{1..10}.md` (per-implementation-phase). Each
  pass is a snapshot of how the design or implementation looked at a
  point in time; remediations from these passes were folded into the
  code before the next phase shipped.
- `v1-superseded/` — pre-v2 versions of the top-level narrative docs
  (`domain-model.md`, `invariants.md`, `ubiquitous-language.md`,
  `failure-modes.md`, `cross-context-interactions.md`). The v2
  versions at `specs/` root replace these; the v1 originals are kept
  here because the v1 code surfaces they describe still ship as
  continuity infrastructure.
