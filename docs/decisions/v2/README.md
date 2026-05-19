# Architecture Decision Records — v2

The structural decisions behind v2 ghyll, distilled from
`specs/archive/direction/operator-decisions-round-1..5.md` (D1–D44) and
the validation passes archived under `specs/archive/validation-passes/`.

These ADRs are **decision records**, not analysis. Each captures:

- **Status** (Accepted; decisions are settled, code awaiting).
- **Context** — the constraint, problem, or motivating finding.
- **Decision** — the choice made.
- **Consequences** — what this decision rules in and rules out.

For full deliberation (alternatives considered, validation history,
operator interaction), see the per-round
`specs/archive/direction/operator-decisions-round-N.md` docs.

## Index

| # | Title | Source decisions |
|---|---|---|
| [ADR-001](001-v2-pivot.md) | The v2 pivot: correctness over speed and breadth | Whole project |
| [ADR-002](002-state-space-frame.md) | State-space-iteration as the conceptual frame | D14 |
| [ADR-003](003-four-role-diamond.md) | Four-role diamond; no standalone adversary or auditor | round 4 Q1 |
| [ADR-004](004-synthetic-role-ids.md) | Synthetic role-ids (`init`, `adversary`) distinct from role files | D21 |
| [ADR-005](005-concept-catalogue.md) | Concept-named catalogue with per-language bindings | D1, D2, D18 |
| [ADR-006](006-per-concept-schemas.md) | Per-concept typed argument schemas | D13 |
| [ADR-007](007-hybrid-artifact-ids.md) | Hybrid artifact IDs (path-default + manual where needed) | D11 |
| [ADR-008](008-arrow-pass-identity.md) | Arrow identity is structural; passes are iterations | D12, D29 |
| [ADR-009](009-three-locks.md) | Three locks, three owners | D34, D35 |
| [ADR-010](010-versioned-grid-files.md) | Versioned grid files + `grid.current` pointer | D31 |
| [ADR-011](011-init-auto-propose.md) | Initialization is auto-propose + operator-confirm | D20, D41 |
| [ADR-012](012-amendment-serialization.md) | Global write-lock with FIFO amendment queue | D22 |
