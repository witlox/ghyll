# ADR-013: Pass entity owns role-lock and registry membership

Date: 2026-05-19
Status: Accepted

## Context

The runtime needs an object that represents "one role's traversal
of one arrow on one (role, context) tuple." The components that
need to observe or coordinate around a live pass are:

- The `RoleContextLockTable` (ADR-011) holds the per-(role,
  context) lock. Acquired at pass entry, released at termination.
- The engine-status CLI needs to enumerate open passes.
- The `AmendmentCommitter` needs to identify passes on a soon-
  to-be-invalidated source arrow so it can abort them.
- The `OperatorBus` (ADR-012) consumes pass-opened / pass-closed
  events.

Tier-1 of the prod-readiness rollout shipped `runner.Pass` and
`runner.PassRegistry`. This ADR pins the design.

## Decision

### 1. `runner.Pass` is the unit of single-active-role-instance

A `Pass` instance owns:

- The lock token from `RoleContextLockTable.TryAcquire`. Released
  in `Close` / `Abort`.
- Identity (`passID`, `role`, `context`, `arrowID`).
- Lifecycle state (`Open` / `Closed` / `Aborted`).
- Open-at and close-at timestamps + close reason.
- An optional `OperatorBus` for pass-opened / pass-closed events.

`OpenPass` is the only constructor; it takes a `PassOptions`
struct and acquires the role-lock atomically with the
construction. `Close` / `Abort` are idempotent.

A pass is single-goroutine by contract: the dispatcher creates a
pass on one goroutine, invokes `Runner.Evaluate` sequentially,
then closes. The internal mutex protects `State()` /
`ClosedAt()` / `CloseReason()` from racing monitoring code.

### 2. `runner.PassRegistry` tracks live passes

The registry is an in-memory map keyed by `passID`. Register at
`OpenPass` success (typically by the dispatcher, not the Pass
itself — Pass doesn't know about the registry), Unregister at
`Close` / `Abort`. The engine-status CLI's `/passes` operator
command and the AmendmentCommitter's pass-abort loop iterate the
registry.

Crash recovery: the registry is in-memory and process-local. A
crashed previous process leaves nothing for the new process to
recover. Cross-session pass tracking is intentionally out of
scope (ADR-006 makes each session the unit of work).

### 3. Pass IS NOT a Runner

The dispatcher wraps `Pass` + `Runner.Evaluate` calls. A pass
runs many clauses; each Evaluate is a single-clause execution.
The runner doesn't know about passes; the dispatcher orchestrates
the pass lifecycle around per-clause Evaluate calls. This
separation (per ADR-011) lets `Runner.Evaluate` stay clause-
scoped and lets the dispatcher own pass-scope concerns.

## Consequences

### Code

`runner/pass.go` (~210 LOC) implements `Pass`, `PassOptions`,
`OpenPass`, `Close` / `Abort`, accessor methods. `runner/
projectstatus.go` adds `PassRegistry`.

### Test impact

Eight tests on `Pass` (acquire/release roundtrip, lock release on
Close + Abort, idempotent Close, busy returns
`*ErrRoleContextBusy`, input validation, bus-optional, custom
clock). Three tests on `PassRegistry` (register / unregister
roundtrip, snapshot, len). The dispatcher tests
(`runner/dispatcher_test.go`) exercise the integrated Pass +
RoleContextLockTable + PassRegistry trio.

### Alternatives considered

1. **Pass with embedded Runner.** Each pass owns its own runner.
   Rejected: runners are cheap, but coupling them to passes makes
   testing harder (you can't unit-test Evaluate without a Pass)
   and conflates two roles.

2. **Pass auto-registers itself with the registry on Open.**
   Rejected: the registry isn't a singleton; tests use disposable
   registries. The dispatcher knows which registry to register
   with; pushing that responsibility into OpenPass would either
   add a registry field to PassOptions or assume a global, both
   worse.

3. **Channel-based pass-lifecycle signaling instead of registry.**
   The dispatcher subscribes to a channel for "new pass" /
   "closed pass" events. Rejected: the registry is a simpler API
   for snapshot-style queries; the channel-style is solved by the
   bus (pass-opened / pass-closed events) which is also published.

## Related

- ADR-011 (per-(role, context) lock) — the lock Pass owns
- ADR-012 (OperatorEvent bus) — where lifecycle events publish
- `runner/pass.go`, `runner/projectstatus.go`,
  `runner/dispatcher.go`
