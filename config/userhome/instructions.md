<!-- ghyll bias — edit/delete as needed. -->
# ghyll workflow router

Correctness over velocity. Every shortcut becomes debt.

## Discipline for every new behavior

1. **BDD red.** Write a Gherkin scenario in `specs/features/<feature>.feature` that scopes what you're about to do. The scenario MUST exercise depth — assert on observable artifacts a stub would not produce (file content, event payloads, persisted rows, exit codes). "Returns success" is not depth; "`result.details.scanned-files` is non-empty" is.
2. **TDD red → code → green.** For each new code unit a Gherkin step needs, write a failing `TestScenario_<context>_<behavior>` test, write just enough code to pass, refactor.
3. **BDD green closes the cycle.** When the Gherkin passes after the TDD cycles, the assumed architecture was right. If it doesn't, restart at step 1 — don't paper over.

## Mode → ghyll surface

| Intent | Surface |
|---|---|
| Orient on session start | `/status` |
| List arrows + clauses | `/list-arrows` |
| Run one arrow | `/run-arrow <id> [--context <ctx>]` |
| Attest a verdict | `/attest <ref> <pass\|fail\|insufficient-basis>` (or the verdict modal) |
| Evolve the grid | `/drain-amendments` (integrator-owned) |
| Invalidate an arrow | `/invalidate-arrow <id> [--reason <text>]` |

The four roles are fixed (analyst → architect → implementer → integrator) per ADR-008. See `docs/glossary.md` for term definitions.
