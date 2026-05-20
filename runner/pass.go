package runner

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// PassState is the lifecycle state of one Pass instance.
type PassState string

const (
	PassStateOpen    PassState = "open"
	PassStateClosed  PassState = "closed"
	PassStateAborted PassState = "aborted" // closed via Abort with an error
)

// Pass represents one role's traversal of one arrow on one
// (role, context) tuple. Owns:
//
//   - The per-(role, context) lock token from
//     RoleContextLockTable. Released at Close/Abort.
//   - Pass-scoped metadata: passID, role, context, arrowID,
//     opened-at, closed-at.
//   - Optional reference to an OperatorBus for emitting
//     pass-lifecycle events.
//
// A Pass is the unit at which single-active-role-instance is
// enforced (ADR-011). Within one Pass, the dispatcher invokes
// Runner.Evaluate for each clause on the arrow; the passID
// stays stable across those calls.
//
// Pass is single-threaded by contract: the dispatcher creates a
// Pass on one goroutine, invokes Evaluate sequentially, then
// closes. The mutex protects state inspection from racing
// monitoring code (e.g., engine status CLI reading State() while
// the dispatcher is in flight).
type Pass struct {
	mu sync.Mutex

	id          string
	role        string
	context     string
	arrowID     string
	gridVersion uint64
	openedAt    time.Time
	closedAt    time.Time
	recoveredAt time.Time
	state       PassState
	closeReason string

	// Lock token returned by the RoleContextLockTable. Released
	// in Close / Abort.
	lockToken RoleContextLockToken

	// Optional event bus. Nil = silent (still functional).
	bus *OperatorBus

	// Registry that owns this pass. Set by PassRegistry.Register
	// or PassRegistry.Resume. closeWith calls registry.emit AFTER
	// the lock token release (F-4: lock order p.mu → release → emit).
	registry *PassRegistry
}

// PassOptions configures Open.
type PassOptions struct {
	PassID      string
	Role        string
	Context     string
	ArrowID     string
	GridVersion uint64
	LockTable   *RoleContextLockTable
	LockTTL     time.Duration // 0 = no TTL
	Bus         *OperatorBus  // optional
	Now         func() time.Time
}

// ErrPassRoleEmpty etc. — explicit sentinels for invalid Open.
var (
	ErrPassIDEmpty            = errors.New("pass-id-empty")
	ErrPassRoleEmpty          = errors.New("pass-role-empty")
	ErrPassContextEmpty       = errors.New("pass-context-empty")
	ErrPassArrowEmpty         = errors.New("pass-arrow-empty")
	ErrPassLockTableNil       = errors.New("pass-lock-table-nil")
	ErrPassNotOpen            = errors.New("pass-not-open")
	ErrPassResumeInvalidState = errors.New("pass-resume-invalid-state")
)

// OpenPass acquires the per-(role, context) lock via opts.LockTable
// and returns a Pass in state Open. Returns *ErrRoleContextBusy
// (lifted from RoleContextLockTable.TryAcquire) if the tuple is
// already held by another pass.
//
// On success the Pass owns the lock token; the caller MUST call
// Close or Abort to release. A `defer p.Close("done")` is the
// idiomatic pattern.
func OpenPass(opts PassOptions) (*Pass, error) {
	if strings.TrimSpace(opts.PassID) == "" {
		return nil, ErrPassIDEmpty
	}
	if strings.TrimSpace(opts.Role) == "" {
		return nil, ErrPassRoleEmpty
	}
	if strings.TrimSpace(opts.Context) == "" {
		return nil, ErrPassContextEmpty
	}
	if strings.TrimSpace(opts.ArrowID) == "" {
		return nil, ErrPassArrowEmpty
	}
	if opts.LockTable == nil {
		return nil, ErrPassLockTableNil
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	tok, err := opts.LockTable.TryAcquire(opts.Role, opts.Context, opts.PassID, opts.LockTTL)
	if err != nil {
		return nil, err
	}
	p := &Pass{
		id:          opts.PassID,
		role:        opts.Role,
		context:     opts.Context,
		arrowID:     opts.ArrowID,
		gridVersion: opts.GridVersion,
		openedAt:    now(),
		state:       PassStateOpen,
		lockToken:   tok,
		bus:         opts.Bus,
	}
	// N-1 / N-2: the duplicate bus.Publish(OpEventPassOpened) is
	// removed. PassRegistry.Register is the single audit path
	// (emits PassEventOpen); the journal observer bridges to the
	// bus if a downstream subscriber needs it.
	return p, nil
}

// Close releases the lock and marks the pass closed. Idempotent:
// a second Close is a no-op. The reason string is recorded for
// status/telemetry.
func (p *Pass) Close(reason string) {
	p.closeWith(reason, PassStateClosed)
}

// Abort releases the lock and marks the pass aborted (used when
// the pass ends with an error). The reason should describe what
// failed.
func (p *Pass) Abort(reason string) {
	p.closeWith(reason, PassStateAborted)
}

// closeWith follows the F-4 lock-ordering rule:
//
//  1. Acquire p.mu. Mutate state + capture payload.
//  2. Release p.mu.
//  3. Release the lock token.
//  4. Emit on the registry (unlocked observer fanout).
//
// Steps 2–4 happen with NO mutex held, so a registry.emit
// observer that needs to call p.State() (which takes p.mu)
// does not deadlock. N-1: the duplicate bus.Publish is removed;
// PassEvent is the single audit path.
func (p *Pass) closeWith(reason string, finalState PassState) {
	p.mu.Lock()
	if p.state != PassStateOpen {
		p.mu.Unlock()
		return
	}
	p.state = finalState
	p.closeReason = reason
	p.closedAt = time.Now()
	payload := PassEvent{
		Kind:        kindFromFinalState(finalState),
		PassID:      p.id,
		Role:        p.role,
		Context:     p.context,
		ArrowID:     p.arrowID,
		GridVersion: p.gridVersion,
		State:       p.state,
		OpenedAt:    p.openedAt,
		ClosedAt:    p.closedAt,
		CloseReason: p.closeReason,
		RecoveredAt: p.recoveredAt,
		At:          p.closedAt,
	}
	registry := p.registry
	p.mu.Unlock()
	p.lockToken.Release()
	if registry != nil {
		registry.emit(payload)
	}
}

// kindFromFinalState maps the terminal PassState to the matching
// observer event kind.
func kindFromFinalState(s PassState) PassEventKind {
	switch s {
	case PassStateAborted:
		return PassEventAbort
	default:
		return PassEventClose
	}
}

// ID returns the pass identifier.
func (p *Pass) ID() string { return p.id }

// Role returns the role this pass runs as.
func (p *Pass) Role() string { return p.role }

// Context returns the bounded context.
func (p *Pass) Context() string { return p.context }

// ArrowID returns the arrow this pass traverses.
func (p *Pass) ArrowID() string { return p.arrowID }

// State returns the current lifecycle state.
func (p *Pass) State() PassState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// OpenedAt returns the timestamp at which Open succeeded.
func (p *Pass) OpenedAt() time.Time { return p.openedAt }

// ClosedAt returns the timestamp at which Close / Abort fired.
// Zero if the pass is still open.
func (p *Pass) ClosedAt() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closedAt
}

// CloseReason returns the reason captured at Close / Abort. Empty
// if the pass is still open.
func (p *Pass) CloseReason() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closeReason
}

// GridVersion returns the grid generation under which the pass
// was opened. Zero when not stamped.
func (p *Pass) GridVersion() uint64 { return p.gridVersion }

// RecoveredAt returns the timestamp at which engine.Recovery
// preserved this pass (attestation-pending exception). Zero on
// passes that never went through recovery.
func (p *Pass) RecoveredAt() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.recoveredAt
}

// markRecovered stamps recoveredAt + emits PassEventRecover.
// Called by PassRegistry.Resume after a successful lock
// re-acquisition. The mutate-then-unlocked-emit pattern matches
// closeWith (F-4).
func (p *Pass) markRecovered(at time.Time) {
	p.mu.Lock()
	p.recoveredAt = at
	payload := PassEvent{
		Kind:        PassEventRecover,
		PassID:      p.id,
		Role:        p.role,
		Context:     p.context,
		ArrowID:     p.arrowID,
		GridVersion: p.gridVersion,
		State:       p.state,
		OpenedAt:    p.openedAt,
		RecoveredAt: at,
		At:          at,
	}
	registry := p.registry
	p.mu.Unlock()
	if registry != nil {
		registry.emit(payload)
	}
}
