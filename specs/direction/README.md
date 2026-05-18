# specs/direction

Design intent for ghyll's v2 pivot — from "Claude Code for self-hosted
models" to a correctness/gate-enforcement coding agent.

**This is design, not code.** Nothing in this directory is implemented.
Per `direction.md` §7, treat it as a hypothesis and validate cold before
building.

## Read in this order

1. **[direction.md](direction.md)** — what changes, what stays, what is
   unproven. The pivot rationale.
2. **[gates.md](gates.md)** — the harness-wide gate schema. Project
   initialization, the arrow grid, evaluation/depth types, the
   clause/arrow/finding state machines, routing, attestation, arrow
   phases (incl. the depth ladder).
3. **[roles/](roles/)** — the four role contracts (analyst,
   architect, implementer, integrator) reconciled to `gates.md`.
4. **[components/](components/)** — the seven component-level specs
   (concepts, init, runner, state-machine, adversarial, amendment,
   attestation). Domain model, features, failure modes per
   component.
5. **[build-notes.md](build-notes.md)** — what is designed vs. what is
   deliberately not yet built. Read before any implementation work.

Supporting documents:

- **[validation-pass-1.md](validation-pass-1.md)** — cold-read findings
  against the pre-reconciliation schema.
- **[operator-decisions-round-1.md](operator-decisions-round-1.md)** —
  D1–D7 (catalogue, strata, weight, v0 grid, residue).
- **[operator-decisions-round-2.md](operator-decisions-round-2.md)** —
  D8–D10 (`unable-to-hint`, entry preconditions as upstream exit
  clauses, invalidation hybrid).
- **[phase-3-architect-findings.md](phase-3-architect-findings.md)** —
  architect-lens pass findings + phase-3 concrete proposals.
- **[operator-decisions-round-3.md](operator-decisions-round-3.md)** —
  D11–D20 (artifact IDs, arrow/pass identity, per-concept schemas,
  state-space framing, severity enum, dependency granularity,
  mid-phase invalidation, language-binding init policy, attestation
  records, init auto-propose).
- **[validation-pass-2.md](validation-pass-2.md)** — end-to-end
  coherence findings (17 items) against the round-3-reconciled
  schema.
- **[operator-decisions-round-4.md](operator-decisions-round-4.md)** —
  D21–D29 (synthetic role-ids, amendment serialization, terminal
  routing, op-id, init bootstrap, depth-ladder gating, residue
  imputation, role-instance scope, aborted reasons).

## Scope note

The four roles described here (analyst, architect, implementer,
integrator) are **ghyll's runtime roles** — the fixed set ghyll
will embed and enforce when it runs as a coding agent on a user's
project. There is no standalone adversary role and no standalone
auditor role; adversarial scrutiny and depth classification are
phases of every depth-sensitive arrow per `gates.md` §11.

These role files are NOT the `.claude/roles/*.md` files used by Claude
Code to build ghyll itself. Those stay as-is.
