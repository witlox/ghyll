# specs/direction

Design intent for ghyll's v2 pivot — from "Claude Code for self-hosted
models" to a correctness/gate-enforcement coding agent.

**This is design, not code.** Nothing in this directory is implemented.
Per `direction.md` §7, treat it as a hypothesis and validate cold before
building.

## Read in this order

1. **[direction.md](direction.md)** — what changes, what stays, what is
   unproven. The pivot rationale.
2. **[gates.md](gates.md)** — the harness-wide gate schema. Evaluation
   types, depth types, arrows, the arrow grid, routing, attestation.
3. **[roles/analyst.md](roles/analyst.md)** — the one role contract
   reconciled to `gates.md` so far. Pattern for the other five.
4. **[build-notes.md](build-notes.md)** — what is designed vs. what is
   deliberately not yet built. Read before any implementation work.

## Scope note

The roles described here (analyst, architect, adversary, implementer,
auditor, integrator) are **ghyll's runtime roles** — the fixed set ghyll
will embed and enforce when it runs as a coding agent on a user's
project.

They are NOT the `.claude/roles/*.md` files used by Claude Code to build
ghyll itself. Those stay as-is.
