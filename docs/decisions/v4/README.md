# v4 ADRs (diamond load-bearing wiring)

The nine v4 ADRs document the structural decisions taken to close
the four load-bearing wiring gaps surfaced by
`specs/v4/code-eval-2026-05-25.md`, plus the Kimi 2.5/2.6 dialect
landing (ADR-v4-009). The implementation contract is
`specs/v4/diamond-load-bearing-revised-v2.md`.

**Integrator-pass status (2026-05-25):** All eight ADRs now ship a
complete producer→bus→consumer chain. The post-substrate integrator
pass surfaced one structural incompleteness (I-C-1: ADR-v4-008's
`arrow_invalidations` table had a full consumer chain but no
producer) which was closed by shipping the `/invalidate-arrow`
operator slash command. Three High findings (I-H-1 unsubscribe
parity, I-H-2 `/run-arrow` cycle-event filter, I-H-3 TOCTOU on
`AdversarialHooks.Load`) and three Medium findings (I-M-1 amendment
swap-before-abort ordering, I-M-2 unified-membership registry
predicate, I-M-3 typed Payload on partial-append) were closed in the
same diamond-close pass. ADR-v4-008 is now `accepted` (no longer
`partial`).

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
| [ADR-v4-009](009-reasoning-content-excluded-from-checkpoint-hash.md) | ReasoningContent excluded from canonical checkpoint hash (Kimi dialect landing) |
