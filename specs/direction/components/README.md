# Component specs

Component-level specs for v2 ghyll. The schema in `gates.md` is
abstract (state machines, types, contracts); these documents fill in
*how each component operates* in domain terms (entities, invariants,
features, failure modes, cross-component interactions).

Each spec follows the same outline (per `roles/analyst.md`):

- Scope
- Domain model
- Invariants
- Behaviors (Gherkin-style features)
- Failure modes
- Cross-component interactions
- Assumptions
- Open questions

## Read order (build-order aligned)

| # | Component | Scope |
|---|---|---|
| 1 | [concepts.md](concepts.md) | The 17 catalogue concepts. Each gets arguments, evaluator contract, default cost, language-binding pattern. The future `gates/concepts/*.yaml` files are implementations against this design. |
| 2 | [init.md](init.md) | Project initialization. The must-have step-one when ghyll is invoked. Auto-propose + operator-confirm flow that turns v0 into the project's v1. |
| 3 | [runner.md](runner.md) | The enforcement spine. Invokes machine evaluators, coordinates with attestation flow, derives arrow status, refuses transitions. |
| 4 | [state-machine.md](state-machine.md) | The four state machines (clause, arrow, finding, pass) operationalized: persistence, derivation, query interface. |
| 5 | [adversarial.md](adversarial.md) | The per-arrow adversarial phase: fresh-context spawn, three sub-activities (clause-falsification, open sweep, depth classification), remediation loop. |
| 6 | [amendment.md](amendment.md) | Grid amendment serialization: the project-wide write-lock, FIFO queue, in-flight dependency check, atomic v(N+1) commit. |
| 7 | [attestation.md](attestation.md) | Operator-attestation coordination: op-id session lifecycle, hint presentation, verdict capture, JSONL record append, N-round `insufficient-basis` escalation. |

## Status

All 7 component specs are **design intent**. Subject to refinement
once the first component is implemented and exercises its contract.

The schema (`gates.md`) is the contract; the component specs are how
each part of the system implements it. Future cold-read validation
should walk the component specs end-to-end the same way
`validation-pass-2.md` walked the schema.
