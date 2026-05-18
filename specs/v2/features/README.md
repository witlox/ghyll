# specs/v2/features/

Gherkin feature files for v2 ghyll's acceptance suite. One file per
component, with all scenarios for that component grouped under one
`Feature:` declaration (proper Gherkin convention).

Extracted from `specs/direction/components/*.md` per round-5
reconciliation. The component spec markdown remains the design
narrative; these `.feature` files are the executable contract.

## Files

(Initial counts are from the extraction; the adversarial pass added
the items in *italics*.)

- **[init.feature](init.feature)** — Project initialization (27
  scenarios). *+grid.current ↔ grid.v<N>.yaml inconsistency,
  modify-edge-cases via Scenario Outline (raise-only across regex/
  scope/severity/unknown fields), op-id re-entry across sessions.*
- **[runner.feature](runner.feature)** — Machine-clause runner (28
  scenarios). *+evaluator process-failure modes (timeout, OOM,
  malformed JSON, stderr-noise, oversized output, zombies),
  strengthened concurrency probes (parallel timestamps, race-detector),
  strengthened successful-evaluation with proof-of-scan.*
- **[state-machine.feature](state-machine.feature)** — Status state
  machine engine (26 scenarios, includes 2 Scenario Outlines).
  *+illegal-transition matrix for clauses (12 examples),
  illegal-transition matrix for findings (6 examples), crash-recovery
  boundary cases (awaiting-attestation, split-brain, truncated
  checkpoint), grid-current missing, residue edge cases.*
- **[adversarial.feature](adversarial.feature)** — Per-arrow
  adversarial phase (18 scenarios). *+remediation-rounds-max
  boundary outline, loop-bomb prevention (producer-fix-without-change),
  strengthened full re-attack (clean-context verification,
  sub-activity markers), concrete depth-tier handling.*
- **[amendment.feature](amendment.feature)** — Grid amendment and
  global lock (17 scenarios). *+lock liveness (orphaned-lock
  recovery), waiting-on-aborted-pass-attestation, FIFO under
  contention, queue-growth alert, reader/writer race, strengthened
  atomic write with fsync ordering.*
- **[attestation.feature](attestation.feature)** — Operator
  attestation flow (24 scenarios). *+op-id adversarial input via
  Outline (path traversal, NUL, oversized, unicode RTL, JSON injection),
  oversized residue note, three-role path encoding, init-arrow path
  encoding, multi-operator near-simultaneous verdicts, session-end
  mid-attestation, strengthened JSONL atomicity + fsync ordering.*

## Naming and granularity

One `.feature` per component, multiple `Scenario:` blocks within
each file. The internal F-N sub-groupings in the component specs
become comment headers (`# ---- F-N name ----`) within the
`Feature:` block — useful for navigation but not Gherkin-syntactic.

## Validation history

- **Initial extraction (3e60a55)** — ~103 scenarios extracted from
  the component-design markdown's primary-behavior sections.
  Captured happy-path coverage; lacked adversarial scrutiny.
- **Adversarial pass (this commit)** — cold-context attack on the
  initial extraction. 18 findings (3 critical, 7 high, 6 medium,
  2 low), verdict "not ready to wire" — a stub clearing happy-path
  assertions would have passed ~60% of the suite. Findings recorded
  in [`validation-adversarial-pass.md`](validation-adversarial-pass.md).
- **Additions (this commit)** — strengthened 10 weakly-asserted
  scenarios; added ~37 new scenarios across the five missing
  categories (evaluator process failures, crash recovery between
  component boundaries, adversarial operator input, concurrency
  liveness, illegal-transition matrices).

Total: 140 scenario declarations across 6 files (~1,400 lines of
Gherkin). Scenario Outlines expand to additional test cases (the
illegal-transition matrices alone add 18 effective cases).

## Status

These features are the **specification**, not yet wired to step
definitions or an acceptance runner. The corresponding step files
will live at `tests/v2/` (or wherever the v2 implementation places
them) once the v2 code lands.

The v1 acceptance suite at `tests/acceptance/` remains in place
running against `specs/features/` (v1 BDD scenarios). v1 and v2
test suites coexist until v2 reaches feature parity.
