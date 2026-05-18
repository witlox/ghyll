package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
)

// Session registry. Per attestation.md F-1 + init.feature 205, 212:
// at most one operator session is active at a time. A second
// declaration is refused with ErrSessionAlreadyActive; the error
// message names a HASH of the currently-active op-id so the caller
// can recognize "someone holds the lock" without exposing PII to
// less-trusted callers (validation-pass-2 F12).
//
// init re-entry (D18: missing-binding suspend) uses the same active
// session; the operator is not re-prompted. Sessions span init's
// entire lifecycle including any number of re-entries until End is
// called.

// MaxSessionHistory bounds the in-memory closed-session history so a
// long-running process doesn't accumulate unbounded PII residue.
// validation-pass-2 F35. The persistent audit log is the source of
// truth for the long-term record.
const MaxSessionHistory = 1024

// SessionRegistry holds the active operator session for one ghyll
// process. Construct via NewSessionRegistry; threaded through code
// that needs to start or query the active session.
//
// SessionRegistry is safe for concurrent use; the internal mutex
// guards active + history + the validation fast-path. Validation
// (StartSession's NFC + length checks) is invoked OUTSIDE the lock
// (validation-pass-2 F34) so a slow validation cannot stall
// concurrent Active() / Close() readers.
type SessionRegistry struct {
	mu      sync.Mutex
	active  *Session
	history []*Session // closed sessions, in order of End() (ring-buffer-bounded)
}

// NewSessionRegistry returns an empty registry.
func NewSessionRegistry() *SessionRegistry {
	return &SessionRegistry{}
}

// Declare starts a new session bound to opID. If another session is
// already active, Declare refuses with an error that wraps
// ErrSessionAlreadyActive and a HASHED-and-truncated form of the
// currently-active op-id (validation-pass-2 F12). The hash lets
// callers detect "same operator's still holding it" across retries
// without revealing the underlying string.
//
// Op-id validation (NFC normalize, length, format) is performed via
// ValidateAndNormalizeOpID BEFORE the registry mutex is taken
// (validation-pass-2 F34), so a slow validation cannot stall
// concurrent readers.
func (r *SessionRegistry) Declare(opID string) (*Session, error) {
	// Validate outside the lock so the critical section is short.
	normalized, err := ValidateAndNormalizeOpID(opID)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active != nil && r.active.Active() {
		return nil, fmt.Errorf("%w: held-by op-id-hash %s",
			ErrSessionAlreadyActive, hashedOpID(r.active.opID))
	}
	sess := &Session{opID: normalized, active: true}
	r.active = sess
	return sess, nil
}

// Active returns the currently-active session or nil if none. The
// returned pointer is the registry's own; callers should treat OpID()
// as a snapshot — a concurrent Close() may flip Active() to false on
// the next read (validation-pass-2 F55 — race window documented).
// Use ActiveSnapshot for a captured (opID, hash, active) tuple.
func (r *SessionRegistry) Active() *Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active != nil && r.active.Active() {
		return r.active
	}
	return nil
}

// SessionSnapshot is a captured view of the active session at one
// moment in time. Unlike Active(), the snapshot's fields cannot
// change under the caller; suitable for audit records and error
// messages constructed after the registry has moved on.
type SessionSnapshot struct {
	OpID     string
	OpIDHash string
	Active   bool
}

// ActiveSnapshot returns a captured view of the active session under
// the registry lock. If no session is active, returns a zero
// SessionSnapshot (Active=false). Validation-pass-2 F55.
func (r *SessionRegistry) ActiveSnapshot() SessionSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil || !r.active.Active() {
		return SessionSnapshot{}
	}
	return SessionSnapshot{
		OpID:     r.active.opID,
		OpIDHash: hashedOpID(r.active.opID),
		Active:   true,
	}
}

// ActiveOpID returns the active session's op-id, or "" if no session
// is active. Convenience for in-process error messages and audit
// records where the caller is trusted to see the full op-id.
//
// For error messages crossing trust boundaries (e.g., surfaced to a
// less-privileged caller via Declare's refusal), prefer
// hashedOpID — the registry uses it directly.
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
//
// History is bounded at MaxSessionHistory entries (ring buffer);
// older entries are dropped (validation-pass-2 F35).
func (r *SessionRegistry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		return
	}
	r.active.End()
	r.history = append(r.history, r.active)
	if len(r.history) > MaxSessionHistory {
		// Drop the oldest entry to keep memory bounded.
		r.history = r.history[len(r.history)-MaxSessionHistory:]
	}
	r.active = nil
}

// History returns a snapshot of all in-memory closed sessions, in
// End-order. Bounded at MaxSessionHistory; for the complete record,
// consult the persistent audit log.
func (r *SessionRegistry) History() []*Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Session, len(r.history))
	copy(out, r.history)
	return out
}

// HistoryLen returns the count of in-memory history entries without
// the cost of copying the slice. Useful when a caller only wants to
// know "have N+1 ops touched this project?".
func (r *SessionRegistry) HistoryLen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.history)
}

// hashedOpID returns a 12-hex-character truncated SHA-256 of opID.
// Used in cross-trust-boundary error messages (validation-pass-2 F12)
// so the active op-id isn't leaked verbatim. 12 hex chars = 48 bits
// of entropy — collisions are astronomically unlikely for any
// realistic operator population, and the operator who took the lock
// can verify by hashing their own op-id locally.
func hashedOpID(opID string) string {
	sum := sha256.Sum256([]byte(opID))
	return hex.EncodeToString(sum[:6])
}
