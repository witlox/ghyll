package runner

import (
	"errors"
	"fmt"
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
	openedAt    time.Time
	closedAt    time.Time
	state       PassState
	closeReason string

	// Lock token returned by the RoleContextLockTable. Released
	// in Close / Abort.
	lockToken RoleContextLockToken

	// Optional event bus. Nil = silent (still functional).
	bus *OperatorBus
}

// PassOptions configures Open.
type PassOptions struct {
	PassID    string
	Role      string
	Context   string
	ArrowID   string
	LockTable *RoleContextLockTable
	LockTTL   time.Duration // 0 = no TTL
	Bus       *OperatorBus  // optional
	Now       func() time.Time
}

// ErrPassRoleEmpty etc. — explicit sentinels for invalid Open.
var (
	ErrPassIDEmpty      = errors.New("pass-id-empty")
	ErrPassRoleEmpty    = errors.New("pass-role-empty")
	ErrPassContextEmpty = errors.New("pass-context-empty")
	ErrPassArrowEmpty   = errors.New("pass-arrow-empty")
	ErrPassLockTableNil = errors.New("pass-lock-table-nil")
	ErrPassNotOpen      = errors.New("pass-not-open")
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
		id:        opts.PassID,
		role:      opts.Role,
		context:   opts.Context,
		arrowID:   opts.ArrowID,
		openedAt:  now(),
		state:     PassStateOpen,
		lockToken: tok,
		bus:       opts.Bus,
	}
	if p.bus != nil {
		p.bus.Publish(OperatorEvent{
			Kind:    OpEventPassOpened,
			ArrowID: p.arrowID,
			PassID:  p.id,
			Role:    p.role,
			Detail:  p.context,
		})
	}
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

func (p *Pass) closeWith(reason string, finalState PassState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != PassStateOpen {
		return
	}
	p.state = finalState
	p.closeReason = reason
	p.closedAt = time.Now()
	p.lockToken.Release()
	if p.bus != nil {
		p.bus.Publish(OperatorEvent{
			Kind:    OpEventPassClosed,
			ArrowID: p.arrowID,
			PassID:  p.id,
			Role:    p.role,
			Detail:  fmt.Sprintf("%s:%s", finalState, reason),
		})
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
