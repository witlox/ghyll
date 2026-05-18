# General Engineering Guidelines

## Commits & Branching

- Conventional commits: `feat:`, `fix:`, `docs:`, `test:`, `refactor:`, `perf:`, `chore:`, `ci:`
- One logical change per commit; reference issue numbers where applicable
- Pre-commit hooks (lefthook) enforce fmt, lint, test, vet — never skip with `--no-verify`

## Error Handling

- Wrap errors with context: `fmt.Errorf("op: %w", err)`
- Typed error types in library code; validate at system boundaries
- Trust internal code; validate external input
- No silent swallowing — every error either handled or propagated

## Code Organization

- Imports grouped: stdlib → external → internal
- One responsibility per file; keep files under 500 lines
- Pass dependencies explicitly, no globals
- No `init()` functions with side effects

## Testing

**TDD** drives internal logic: red → green → refactor at package level.

**BDD** verifies the assembled system: Gherkin scenarios in `specs/features/`,
step definitions in `tests/acceptance/`. Scenarios exercise real integrated
code paths via the running tool, not mocked dependencies.

Unit tests co-located with source. Acceptance tests separate.

## Architecture Decision Records

ADRs in `docs/decisions/`. Record context, decision, consequences.
Append-only — supersede with new ADRs, do not edit.

## Workflow Phases

1. Analyst — domain model, invariants, Gherkin scenarios
2. Architect — interfaces, contracts, ADRs
3. Adversary — challenge completeness, gate 1
4. Implementer — TDD for units, BDD for integration
5. Auditor — depth classification, gate 2
6. Integrator — cross-context validation
