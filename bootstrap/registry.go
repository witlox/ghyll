package bootstrap

import (
	"fmt"
	"sync"
)

// Session registry. Per attestation.md F-1 + init.feature 205, 212:
// at most one operator session is active at a time. A second
// declaration is refused with ErrSessionAlreadyActive; the error
// message names the currently-active op-id so the caller (init's
// op-id prompt, the runtime's session check) can report it back to
// whoever tried to start the second session.
//
// init re-entry (D18: missing-binding suspend) uses the same active
// session; the operator is not re-prompted. Sessions span init's
// entire lifecycle including any number of re-entries until End is
// called.

// SessionRegistry holds the active operator session for one ghyll
// process. Construct via NewSessionRegistry; threaded through code
// that needs to start or query the active session.
//
// SessionRegistry is safe for concurrent use; declare/close/active
// reads share an internal mutex. The session itself is not
// concurrent-safe (per its own contract) — concurrent access must
// be serialized at a higher level.
type SessionRegistry struct {
	mu      sync.Mutex
	active  *Session
	history []*Session // closed sessions, in order of End()
}

// NewSessionRegistry returns an empty registry.
func NewSessionRegistry() *SessionRegistry {
	return &SessionRegistry{}
}

// Declare starts a new session bound to opID. If another session is
// already active, Declare refuses with an error that wraps
// ErrSessionAlreadyActive and names the currently-active op-id (so
// the operator who tried to start the second session can resolve the
// conflict: ask the holder to End, or take over via an explicit
// handoff once that's specified).
//
// opID is validated by StartSession (NFC-normalized, length/format
// checks, etc.); registry-level errors are layered on top.
func (r *SessionRegistry) Declare(opID string) (*Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active != nil && r.active.Active() {
		return nil, fmt.Errorf("%w: %s", ErrSessionAlreadyActive, r.active.opID)
	}
	sess, err := StartSession(opID)
	if err != nil {
		return nil, err
	}
	r.active = sess
	return sess, nil
}

// Active returns the currently-active session or nil if none. The
// returned pointer is the registry's own; callers that mutate it
// (via End) drive the registry's state, intentionally.
func (r *SessionRegistry) Active() *Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active != nil && r.active.Active() {
		return r.active
	}
	return nil
}

// ActiveOpID returns the active session's op-id, or "" if no session
// is active. Convenience for error messages and audit records.
func (r *SessionRegistry) ActiveOpID() string {
	if s := r.Active(); s != nil {
		return s.OpID()
	}
	return ""
}

// Close ends the active session (if any) and moves it to history.
// After Close, Declare succeeds again with a new op-id.
//
// Close is idempotent — calling it without an active session is a
// no-op. End() called on the session directly (bypassing the
// registry) eventually drives the same effect: Active() will return
// nil on the next call.
func (r *SessionRegistry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		return
	}
	r.active.End()
	r.history = append(r.history, r.active)
	r.active = nil
}

// History returns a snapshot of all sessions that have been Closed,
// in End-order. Useful for audit / attestation records that include
// every op-id that touched the project across re-entries.
func (r *SessionRegistry) History() []*Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Session, len(r.history))
	copy(out, r.history)
	return out
}
