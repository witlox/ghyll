Spec consistency check. Validates that specs, architecture, and code stay aligned.

1. **Ubiquitous language drift**: grep all Go source files for exported type
   names. Compare against `specs/ubiquitous-language.md`. Flag types that
   don't match a defined term and terms with no corresponding type.

2. **Invariant enforcement coverage**: read `specs/architecture/enforcement-map.md`.
   For each invariant, check if the enforcement point exists in code
   (file/function). Report: ENFORCED / UNIMPLEMENTED / UNKNOWN.

3. **Scenario coverage**: for each `specs/features/*.feature`, check if a
   corresponding test exists in `tests/acceptance/`.
   Report: COVERED / PARTIAL / NONE per feature file.

4. **ADR compliance**: for each `docs/decisions/*.md`, check if the
   decision is reflected in code. Flag ADRs that appear violated.

Report summary table with pass/fail per check category.
