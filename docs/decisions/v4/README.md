# v4 ADRs (diamond load-bearing wiring)

The eight v4 ADRs document the structural decisions taken to close
the four load-bearing wiring gaps surfaced by
`specs/v4/code-eval-2026-05-25.md`. The implementation contract is
`specs/v4/diamond-load-bearing-revised-v2.md`.

| ADR | Title |
|---|---|
| [ADR-v4-001](001-registry-key-shape.md) | Registry key shape: `<concept>.<language>` flat key |
| [ADR-v4-002](002-adversarial-phase-conditional-enablement.md) | Adversarial phase auto-enabled on dialect availability |
| [ADR-v4-003](003-amendment-driven-re-register-ordering.md) | Amendment-driven re-register: in-memory snapshot, atomic swap, fail-before-bump |
| [ADR-v4-004](004-concept-classification-auto-derived.md) | Concept classification auto-derived from embedded YAMLs |
| [ADR-v4-005](005-operator-event-typed-payload.md) | OperatorEvent typed Payload contract — `outcome` / `reason` key split |
| [ADR-v4-006](006-evaluator-with-runner-two-table-lookup.md) | `EvaluatorWithRunner` variant + two-table Registry lookup |
| [ADR-v4-007](007-binding-registration-in-cmd-ghyll.md) | Language-binding registration lives in `cmd/ghyll` (integration layer) |
| [ADR-v4-008](008-engine-schema-migrations.md) | Engine schema migrations via explicit ALTER TABLE |
