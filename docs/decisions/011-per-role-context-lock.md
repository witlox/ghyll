# ADR-011: Per-(role, context) lock — single-active-role-instance

Date: 2026-05-19
Status: Accepted

## Context

`specs/invariants.md` inv 19 declares the
`single-active-role-instance` invariant: at most one active pass per
(role-id, bounded-context-id) tuple at any time. Two adversarial
phases on the same arrow (FM-23) and two implementer instances on
the same context are both forbidden by this rule.

ADR-009 in `docs/decisions/v2/009-three-locks.md` names the
mechanism as a "Per-(role, context) lock" owned by the runner,
acquired at pre-spawn, released on pass termination.

The mechanism has been designed but not implemented in code. The
runner has an `obsMu sync.RWMutex` for observer-slice safety (M1
from integrator pass), but no per-(role, context) locking.

## Decision

Implement `runner.RoleContextLockTable`:

```go
type RoleContextLockTable struct {
    mu    sync.Mutex
    held  map[roleContextKey]*roleContextLock
}

type roleContextKey struct{ Role, Context string }

type roleContextLock struct {
    passID    string
    acquiredAt time.Time
    expiresAt  time.Time // optional; zero = no expiry
}
```

Interface:

- `TryAcquire(role, ctx, passID string, ttl time.Duration) (token, error)`
  — succeeds if no holder, else returns
  `ErrRoleContextBusy` carrying the existing holder's `passID`.
- `Release(token)` — releases iff the token still owns the lock.
- `ExpireOlderThan(now time.Time)` — sweeps stale entries (crash
  recovery; entries whose `expiresAt` is in the past).

Wiring:

- `Runner.Evaluate(...)` enters with `TryAcquire(role, ctx, passID,
  defaultPassTTL)`. On busy, the runner returns
  `ErrRoleContextBusy` instead of starting the pass.
- `Runner.Evaluate` defers `Release(token)` so a panic or early
  return still drops the lock.
- `engine.Replay` calls `ExpireOlderThan(time.Now())` after replay
  so stale locks from a crashed previous session don't permanently
  block a (role, context).

## Rationale

The runtime has many small mutexes already (one per store, journal
backpressure, observer slice). Adding a dedicated per-(role, context)
lock table localizes the invariant to one place: the runner. The
alternative — scattering acquire/release across every spawn site —
would leak invariants and miss recovery cases.

The TTL is conservative: an `expiresAt = zero` token never auto-
expires (caller is responsible for `Release`). Most callers pass a
non-zero TTL so a crashed/abandoned pass doesn't poison the table
forever. Expiration is checked lazily on next `TryAcquire` and
eagerly by `ExpireOlderThan` after replay.

The lock is per `(role, context)`, not per arrow. Two arrows
sharing the same (role, context) tuple cannot run simultaneously —
that is the invariant, not "two passes on one arrow." Two passes on
disjoint contexts of the same role *can* run concurrently.

## Consequences

### Code

- New file `runner/rolelock.go`: `RoleContextLockTable` + tests.
- `runner.Runner` gains a `*RoleContextLockTable` field; passed via
  constructor option.
- `Runner.Evaluate` acquires at entry, releases at exit (defer).
- New sentinel `ErrRoleContextBusy` carrying the conflicting
  `passID`.

### Tests

- Concurrent `TryAcquire` from two goroutines on the same key — one
  succeeds, one gets `ErrRoleContextBusy`.
- `Release` is idempotent: a second `Release` on the same token
  succeeds but does not affect a re-acquired lock by a different
  passID.
- `ExpireOlderThan` clears stale entries; entries with zero
  `expiresAt` are untouched.
- `Runner.Evaluate` returns `ErrRoleContextBusy` when the
  (role, context) is already held.
- Disjoint (role, context) tuples acquire independently.

### Crash recovery

After `engine.Replay`, the runner calls
`ExpireOlderThan(time.Now())`. Locks from the crashed previous
session expire iff they had a non-zero TTL. Operators who set
zero-TTL locks intentionally (e.g., long-running interactive
verification) must clear them manually via the engine CLI —
documented in CLAUDE.md.

### Performance

`TryAcquire` and `Release` take the lock-table `sync.Mutex` for the
critical section. The critical section is a single map lookup plus
optionally a write — sub-microsecond. Not a bottleneck.

## Alternatives considered

1. **`sync.Map` per key with one-shot atomic CAS.** Simpler API but
   harder to surface "who holds the lock" on contention. Rejected:
   the holder identity is part of `ErrRoleContextBusy`.
2. **DB-based lock table.** Survives full process crash. Rejected
   for v1.0.0: replay handles crash recovery, and DB locks add
   round-trip latency to the hot path.
3. **Coarser arrow-level lock.** Locks a whole arrow. Rejected:
   spec invariant is `(role, context)`, not arrow. Coarsening
   blocks legitimate concurrent passes on disjoint contexts of the
   same role.

## Related

- `specs/invariants.md` inv 19, inv 23
- `specs/failure-modes.md` FM-23
- `docs/decisions/v2/009-three-locks.md` — three-lock topology
- `runner/runner.go` — entry point for the new acquire/release
