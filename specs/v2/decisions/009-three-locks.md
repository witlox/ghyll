# ADR-009: Three locks, three owners

**Status:** Accepted (2026-05-18; D34, D35)

**Context:**

Validation-pass-2 finding #9 surfaced inconsistent concurrency
primitives across the component specs: `runner.md` declared a
"per-clause lock," `state-machine.md` mentioned "if concurrent
updates somehow reach the engine," `attestation.md` had "its own
serialization." Three different locks were alluded to with no
single owner. An implementer would invent locks that conflict.

**Decision:**

Three locks. Three owners. No others.

| Lock | Owner | Scope | Used by |
|---|---|---|---|
| **Per-clause transition lock** | State Machine engine | `(pass-id, clause-id)` | All callers proposing clause-status transitions |
| **Per-`(role, context)` lock** | Runner | `(role-id, bounded-context-id)`; expires on pass termination | Enforces `single-active-role-instance` at pre-spawn |
| **Project-wide grid write-lock** | Amendment component | Project | Held during grid v(N+1) commit; init takes it at end-of-init for v1 write (D35) |

Boot recovery order on harness restart is also fixed:

1. **State Machine engine recovers first** — reads checkpoint log,
   reconstructs running-pass state, marks orphan `running` passes
   as `aborted` with `reason: crash`.
2. **Amendment component recovers second** — validates
   `.ghyll/grid.current` matches an existing grid file; alerts on
   divergence; cleans up orphaned temp files; releases any
   crashed-while-held lock.
3. **Runner becomes ready third** — after the prior two are
   reconciled, accepts new pass starts.

**Consequences:**

- **Rules in:** single source of truth per lock; structural enforcement
  of single-active-role-instance; deadlock-free boot recovery
  (sequential phases, not concurrent).
- **Rules out:** multiple owners of the same lock; ad-hoc locks
  invented by future components; "if concurrent updates somehow
  reach the engine" — they don't, because the per-clause lock owns
  transitions.
- **Concurrency contract:** components that need transitions go
  through the engine (which acquires its own lock); components
  that spawn passes go through the runner (which acquires
  per-(role, context)); components that commit grid changes go
  through the amendment component (which acquires the project-wide
  lock).
- **Init special case:** init writes the v1 grid by acquiring the
  amendment component's project-wide lock at end-of-init. The lock
  is uncontested at that point (no other arrow has run yet).
- **Orphaned-lock recovery:** the amendment component detects crashed
  lockholders via dead-PID-check; releases the lock as part of
  boot recovery (FM-27). No operator action required.

See `gates.md` §5.1 (`single-active-role-instance`);
`specs/direction/components/state-machine.md`,
`runner.md`, `amendment.md`;
`specs/v2/cross-context.md` §"Locks and concurrency".
