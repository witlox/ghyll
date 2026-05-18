# Documentation Maintenance

## Required Documentation Per Project

1. **README.md** — purpose, quick-start, architecture overview, license
2. **CONTRIBUTING.md** — dev setup, coding standards, PR process, testing
3. **CLAUDE.md** (root) — project state for AI assistants: phase, scope, conventions
4. **.claude/CLAUDE.md** — workflow router: role routing, escalations, mode detection

## Spec Documents (specs/)

- `ubiquitous-language.md` — domain glossary, kept in sync with code
- `domain-model.md` — entities, value objects, aggregates
- `invariants.md` — numbered list of system invariants
- `features/*.feature` — Gherkin behavioral specs
- `fidelity/INDEX.md` — test depth per invariant (THOROUGH/MODERATE/SHALLOW/NONE)
- `cross-context/` — integration points between bounded contexts
- `failure-modes.md` — known failure scenarios and handling

## Architecture Decision Records

- Stored in `docs/decisions/`
- Record context, decision, consequences
- Append-only: supersede with new ADRs, do not edit
- Number sequentially (001, 002, ...)

## Inline Documentation

- Godoc on every exported identifier
- Package-level doc comment in `doc.go` or top of primary file
- Comments explain WHY (constraints, invariants); not WHAT
- Comments only where logic isn't self-evident

## Mdbook (docs/)

- `book.toml` at repo root
- `make docs-serve` for local preview
- Published via CI to GitHub Pages on main

## Keeping Docs Current

- README updated as part of any PR that changes setup/build/test
- ADRs written when decisions are made, not retroactively
- Fidelity index updated after every test sweep
- Stale docs are worse than no docs — delete rather than mislead
