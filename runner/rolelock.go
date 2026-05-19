package runner

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrRoleContextBusy is returned when TryAcquire finds an existing
// holder for the (role, context) tuple. The error carries the
// conflicting passID so the dispatcher can surface it to the
// operator.
//
// Per ADR-011, the lock is acquired by the dispatcher at pass
// entry, not by Runner.Evaluate. A busy error means "another pass
// already owns this (role, context) tuple"; the caller decides
// whether to fail, queue, or retry.
type ErrRoleContextBusy struct {
	Role        string
	Context     string
	HoldingPass string
	AcquiredAt  time.Time
}

func (e *ErrRoleContextBusy) Error() string {
	return fmt.Sprintf("role-context-busy: (%s, %s) held by pass %s since %s",
		e.Role, e.Context, e.HoldingPass, e.AcquiredAt.UTC().Format(time.RFC3339))
}

// roleContextKey is the (role, context) tuple keying the table.
type roleContextKey struct {
	Role    string
	Context string
}

// roleContextLock records the holder of a (role, context) slot.
type roleContextLock struct {
	passID     string
	acquiredAt time.Time
	expiresAt  time.Time // zero = no TTL
}

// RoleContextLockToken is returned by TryAcquire and required by
// Release. The token bundles the key + holder identity so a stale
// Release cannot drop a re-acquired lock by a different passID.
//
// Idiomatic use: `defer token.Release()`. Release is a silent
// no-op if the token no longer owns the lock (idempotent +
// stale-token-safe).
type RoleContextLockToken struct {
	key    roleContextKey
	passID string
	table  *RoleContextLockTable
}

// Release drops the token's lock IFF the table still holds it
// under this passID. Idempotent; silent no-op after a different
// passID has re-acquired the lock. Silent no-op is the right
// semantic for `defer tok.Release()` idiom — an error return
// would force callers to wrap the defer.
func (t RoleContextLockToken) Release() {
	if t.table == nil {
		return
	}
	t.table.releaseToken(t)
}

// PassID returns the holder identity recorded on the token.
// Useful for telemetry; the value matches what was passed to
// TryAcquire.
func (t RoleContextLockToken) PassID() string { return t.passID }

// RoleContextLockTable enforces single-active-role-instance per
// ADR-011. The dispatcher (engine layer or test harness) acquires
// at pass entry and releases at pass termination. The runner's
// `Evaluate` method does NOT touch this table — pass granularity is
// owned by the dispatcher because Evaluate sees individual clauses,
// not pass lifecycles.
//
// The table is in-memory and process-local. Cross-process
// single-session enforcement is handled by `cmd/ghyll/lockfile.go`
// per ADR-006. There is no cross-session lock state to persist or
// recover — a crashed previous process leaves the workdir's
// lockfile stale; the new process gets an empty lock table.
type RoleContextLockTable struct {
	mu   sync.Mutex
	held map[roleContextKey]*roleContextLock
	now  func() time.Time
}

// NewRoleContextLockTable constructs an empty table.
func NewRoleContextLockTable() *RoleContextLockTable {
	return &RoleContextLockTable{
		held: make(map[roleContextKey]*roleContextLock),
		now:  time.Now,
	}
}

// WithClock overrides the time source for tests. Returns the
// receiver for chaining.
func (t *RoleContextLockTable) WithClock(clock func() time.Time) *RoleContextLockTable {
	t.now = clock
	return t
}

// TryAcquire claims the (role, context) slot for passID. Returns a
// token on success or *ErrRoleContextBusy if another passID
// already holds it.
//
// Pass a zero ttl for "no expiration" (interactive sessions where
// the operator controls timing). A positive ttl auto-expires the
// entry so a forgotten Release doesn't poison the slot forever; on
// the next contended TryAcquire whose now() is past expiresAt, the
// stale entry is swept and the new caller wins.
//
// Empty role / context / passID are programmer mistakes and return
// a non-busy error.
func (t *RoleContextLockTable) TryAcquire(role, ctx, passID string, ttl time.Duration) (RoleContextLockToken, error) {
	if role == "" {
		return RoleContextLockToken{}, errors.New("rolelock: role must be non-empty")
	}
	if ctx == "" {
		return RoleContextLockToken{}, errors.New("rolelock: context must be non-empty")
	}
	if passID == "" {
		return RoleContextLockToken{}, errors.New("rolelock: passID must be non-empty")
	}
	if ttl < 0 {
		return RoleContextLockToken{}, fmt.Errorf("rolelock: ttl must be >= 0; got %s", ttl)
	}
	k := roleContextKey{Role: role, Context: ctx}
	now := t.now()

	t.mu.Lock()
	defer t.mu.Unlock()

	if existing, ok := t.held[k]; ok {
		if isExpired(now, existing.expiresAt) {
			delete(t.held, k)
		} else {
			return RoleContextLockToken{}, &ErrRoleContextBusy{
				Role:        role,
				Context:     ctx,
				HoldingPass: existing.passID,
				AcquiredAt:  existing.acquiredAt,
			}
		}
	}

	var exp time.Time
	if ttl > 0 {
		exp = now.Add(ttl)
	}
	t.held[k] = &roleContextLock{
		passID:     passID,
		acquiredAt: now,
		expiresAt:  exp,
	}
	return RoleContextLockToken{key: k, passID: passID, table: t}, nil
}

// isExpired returns true iff the entry has a non-zero expiresAt and
// now is at or past it. Zero expiresAt (no TTL) never expires. Used
// by both TryAcquire and ExpireOlderThan so the two paths share one
// "expired" definition (inclusive boundary: now == expiresAt is
// expired).
func isExpired(now, expiresAt time.Time) bool {
	if expiresAt.IsZero() {
		return false
	}
	return !now.Before(expiresAt)
}

// releaseToken is called via Token.Release. Drops the entry only
// if the table still holds it under the token's passID.
func (t *RoleContextLockTable) releaseToken(tok RoleContextLockToken) {
	t.mu.Lock()
	defer t.mu.Unlock()
	existing, ok := t.held[tok.key]
	if !ok {
		return
	}
	if existing.passID != tok.passID {
		return
	}
	delete(t.held, tok.key)
}

// InspectHolder returns the passID currently holding
// (role, context) and whether anyone holds it. The result is
// **racy by construction** — another goroutine may Release and
// re-acquire between this call and the caller acting on the
// result. Use for monitoring (engine status CLI, telemetry,
// logging), NOT for decisions. Decisions should call TryAcquire
// and handle *ErrRoleContextBusy.
func (t *RoleContextLockTable) InspectHolder(role, ctx string) (passID string, held bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if h, ok := t.held[roleContextKey{Role: role, Context: ctx}]; ok {
		return h.passID, true
	}
	return "", false
}

// ExpireOlderThan sweeps entries whose expiresAt is past cutoff.
// Entries with zero expiresAt (no TTL) are NOT touched — those are
// caller-managed.
//
// Useful for periodic in-session hygiene (e.g., a long-running
// daemon that wants to reap forgotten short-TTL locks). NOT needed
// for cross-session recovery: the table is in-memory and the
// process boundary handles crash recovery.
func (t *RoleContextLockTable) ExpireOlderThan(cutoff time.Time) (expired int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k, v := range t.held {
		if isExpired(cutoff, v.expiresAt) {
			delete(t.held, k)
			expired++
		}
	}
	return expired
}

// Len returns the number of held slots. Useful for tests and
// metrics.
func (t *RoleContextLockTable) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.held)
}
