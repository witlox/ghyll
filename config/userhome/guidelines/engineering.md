<!-- ghyll bias — edit/delete as needed. -->
# Engineering bias

## BDD-then-TDD, with depth

- **BDD red first.** Gherkin scenarios scope what's next. They MUST assert on observable artifacts a stub would not produce (file rows, bus events, persisted records). If the scenario passes against a no-op, it doesn't test depth.
- **TDD inside.** For each step needing new code: `TestScenario_<context>_<behavior>` → red → minimal code → green → refactor.
- **BDD green closes.** If the Gherkin still fails after the TDD cycles, the assumed architecture is wrong — revisit the spec, don't paper over.

## Standing rules

- **No deferrals on adversarial/audit/integrator findings.** Critical + High close in-phase; Medium/Low file a GH issue, don't leave TODO/FIXME in code.
- **No new shims for old shapes.** Pre-prod, simplify aggressively — drop migration paths, prefer manual constants for simple version state.
- **Sandbox-only execution.** Tools run direct OS calls; the sandbox is the security boundary. No in-process permission gates.
