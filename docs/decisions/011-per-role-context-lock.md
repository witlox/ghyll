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

The mechanism has been designed but not implemented in code.

## Pass vs. clause: the granularity decision

A "pass" is one role's traversal of one arrow on one (role, context)
tuple. A pass may run multiple clause evaluations
(`Runner.Evaluate` calls) sequentially, one per clause on the arrow.
The invariant is **one active pass per (role, context)**, not one
active clause.

Therefore the lock is **acquired around the whole pass**, not
around each `Runner.Evaluate` call. The acquire/release points are:

- Acquire — by the dispatcher (engine layer in production; test
  harness in BDD) BEFORE calling `Runner.Evaluate` for the first
  clause of the pass.
- Release — by the same dispatcher AFTER the last clause of the
  pass returns (or on early termination / panic / error).

`Runner.Evaluate` itself does **not** acquire or release the lock.
The runner doesn't know whether a given `Evaluate` call is the
first, middle, or last clause of a pass — the dispatcher does.

A consequence: `passID` is **stable across all clauses of one
logical pass**. Callers MUST use the same `passID` for every
`Runner.Evaluate` call in a single pass. The lock table uses
`passID` as the holder identity.

## Process boundary

The lock table is in-memory and **process-local**. Cross-process
single-active-role-instance is enforced by `cmd/ghyll/lockfile.go`
(ADR-006: one session per repo). The lockfile guarantees only one
`ghyll run` process is active in a workdir at a time; the
in-memory role-context lock table inside that one process is
therefore sufficient.

`AcquireLock` in `cmd/ghyll/lockfile.go` already handles stale-PID
detection so a crashed previous process does not permanently block
the workdir. When the new process starts, it constructs a fresh,
empty `RoleContextLockTable` — there is **no cross-session lock
state to recover**. The "crash recovery" concern in the original
ADR draft was overstated.

## Decision

Implement `runner.RoleContextLockTable`:

```go
type RoleContextLockTable struct {
    mu   sync.Mutex
    held map[roleContextKey]*roleContextLock
    now  func() time.Time
}

type roleContextKey struct{ Role, Context string }

type roleContextLock struct {
    passID     string
    acquiredAt time.Time
    expiresAt  time.Time // zero = no TTL
}
```

Interface:

- `TryAcquire(role, ctx, passID string, ttl time.Duration)
  (RoleContextLockToken, error)` — succeeds if no holder, returns
  `*ErrRoleContextBusy` on contention. The returned token carries
  the holder identity.
- `RoleContextLockToken.Release()` — drops the table entry iff the
  token still owns it. Idempotent: a second Release on the same
  token returns silently. A Release after a different `passID` has
  re-acquired the lock is a no-op (does NOT clobber the new
  holder). Silent no-op is the right semantic because `defer
  tok.Release()` is the idiomatic pattern; returning an error from
  Release would force every caller to wrap the defer in an
  error-handling closure.
- `InspectHolder(role, ctx) (passID string, held bool)` — **monitoring
  only**. The result is racy by construction (another goroutine may
  Release+TryAcquire between this call and the caller acting on
  the result). Use for logging, telemetry, the engine status CLI.
  For decisions, use `TryAcquire` and handle the busy error.
- `ExpireOlderThan(now time.Time) (expired int)` — sweeps entries
  whose `expiresAt` is past `now`. Zero-`expiresAt` entries are NOT
  swept. Useful for periodic in-session hygiene; not needed for
  cross-session recovery (see Process boundary).

## TTL semantics

`ttl` parameter to `TryAcquire`:

- `ttl == 0` — no auto-expiration. The caller is responsible for
  Release. Suitable for interactive sessions where the operator
  controls timing.
- `ttl > 0` — `expiresAt = now + ttl`. If the entry is still in
  the table after `expiresAt`, the next `TryAcquire` on the same
  key sees the entry as stale and overwrites it. This handles the
  case where a pass forgets to Release (a bug) — the lock unblocks
  after `ttl` elapses on the next contended TryAcquire.

In-session TTL is the only meaningful expiration; cross-session is
out of scope (Process boundary section). Zero-TTL entries that
outlive their pass due to a bug are a logical error to be caught
in code review and tests, not in production crash recovery.

## Wiring

```
engine.Dispatcher (or test harness) — owns pass lifecycle:
    token, err := runner.LockTable.TryAcquire(role, ctx, passID, ttl)
    if err != nil { return ErrRoleContextBusy ... }
    defer token.Release()

    for _, clause := range arrow.Clauses {
        runner.Evaluate(ctx, clauseID, passID, clause)
    }
```

`Runner` does NOT carry a reference to the lock table. The lock is
a dispatch-layer concern. This keeps `Runner.Evaluate` focused on a
single clause and avoids the granularity mismatch the original ADR
draft had.

## Rationale

The runtime has many small mutexes already (one per store, journal
backpressure, observer slice). Adding a dedicated per-(role, context)
lock table at the dispatch layer localizes the invariant to one
place. Pass-level granularity matches the spec invariant directly:
"one active pass per (role, context)."

Token-based Release keeps the API safe under defer + early-return
patterns. The InspectHolder rename makes the racy nature explicit;
callers reaching for decisions get pushed to TryAcquire.

## Consequences

### Code

- New file `runner/rolelock.go`: `RoleContextLockTable` and friends.
- New sentinel `ErrRoleContextBusy` carrying the conflicting
  `passID`, role, context, and acquire-time.
- Dispatcher integration in `cmd/ghyll` (and in BDD step helpers
  that simulate the dispatcher).
- Engine status CLI surfaces `InspectHolder` output if any (role,
  context) tuple is held — informational, no decision-making.

### Tests

- Concurrent `TryAcquire` from two goroutines on the same key — one
  succeeds, one gets `ErrRoleContextBusy`.
- Release is idempotent: a second Release on the same token
  succeeds; a Release after a different passID re-acquired the
  lock does not affect the new holder.
- TTL auto-sweep: a stale entry is overwritten by a new TryAcquire
  whose now() is past expiresAt.
- ExpireOlderThan: only entries with non-zero, past-deadline
  expiresAt are swept; zero-TTL entries untouched.
- Disjoint (role, context) tuples acquire independently.
- Empty role / context / passID inputs return errors (programmer
  mistakes).

### Performance

`TryAcquire` and `Release` take the table mutex for a single map
lookup + optionally a write. Sub-microsecond. Not a bottleneck.

## Alternatives considered

1. **Lock per `Runner.Evaluate` call.** Rejected: wrong granularity.
   Two passes on the same (role, context) could interleave clause
   evaluations.
2. **`sync.Map` per key with one-shot atomic CAS.** Simpler API but
   loses holder identity on contention. Rejected: `ErrRoleContextBusy`
   needs the holding passID.
3. **DB-based lock table for cross-process locking.** Rejected:
   ADR-006 (lockfile) already ensures one process; in-memory
   suffices and avoids DB latency on the hot path.
4. **Error return on stale-token Release.** Rejected: defers
   would need wrappers; silent no-op is the idiomatic Go pattern
   and a stale-token-bug is a code-review concern, not a
   runtime-recoverable failure.

## Related

- `specs/invariants.md` inv 19, inv 23
- `specs/failure-modes.md` FM-23
- `docs/decisions/v2/009-three-locks.md` — three-lock topology
- `cmd/ghyll/lockfile.go` — process-level single-session enforcement
- `runner/runner.go` — `Runner.Evaluate` (does NOT take the lock;
  the dispatcher does)
