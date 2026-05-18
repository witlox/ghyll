package bootstrap

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// MaxOpIDLength is the maximum byte length of an op-id.
// Per FM-51 / attestation.md F-1.
const MaxOpIDLength = 256

// Op-id validation errors. Match the strings the .feature scenarios
// expect (init.feature F-7, attestation.feature adversarial additions).
var (
	ErrOpIDRequired          = errors.New("op-id-required")
	ErrOpIDTooLong           = errors.New("op-id-too-long")
	ErrOpIDInvalidCharacters = errors.New("op-id-invalid-characters")
	ErrSessionAlreadyActive  = errors.New("session-already-active")
	ErrSessionEnded          = errors.New("session-ended")
)

// Session is an operator session bound to a non-empty op-id. Sessions
// are created by StartSession and live until End is called or the
// harness terminates. The op-id is recorded in every attestation
// emitted while the session is active (gates.md §10.2).
//
// Sessions are not safe for concurrent use; the harness assumes one
// goroutine drives a session at a time. Multi-operator coordination is
// expressed by ending one session before starting another.
type Session struct {
	opID   string
	active bool
}

// StartSession validates the op-id and returns a new active session.
//
// op-id must be non-empty after whitespace trimming, at most
// MaxOpIDLength bytes long, valid UTF-8, and free of characters
// that could leak into filesystem paths or break record encodings:
//
//   - Path separators ('/', '\\')
//   - C0 control characters (incl. NUL, tab, line feed, carriage return)
//   - Unicode right-to-left override (U+202E)
//   - Double-dot path-traversal substring ("..")
//
// op-id is *not* used as a filesystem path component anywhere in the
// schema (D24, FM-51): it is recorded in JSON attestation records
// only. The validation above is defensive — a violation indicates an
// implementation bug elsewhere has tried to use op-id as a path.
func StartSession(opID string) (*Session, error) {
	if err := ValidateOpID(opID); err != nil {
		return nil, err
	}
	return &Session{
		opID:   opID,
		active: true,
	}, nil
}

// ValidateOpID applies the op-id rules without creating a session.
// Returns one of the sentinel errors (ErrOpIDRequired,
// ErrOpIDTooLong, ErrOpIDInvalidCharacters) on failure; nil if valid.
func ValidateOpID(opID string) error {
	if strings.TrimSpace(opID) == "" {
		return ErrOpIDRequired
	}
	if len(opID) > MaxOpIDLength {
		return ErrOpIDTooLong
	}
	if !utf8.ValidString(opID) {
		return ErrOpIDInvalidCharacters
	}
	if strings.Contains(opID, "..") {
		return ErrOpIDInvalidCharacters
	}
	for _, r := range opID {
		if isUnsafeOpIDRune(r) {
			return ErrOpIDInvalidCharacters
		}
	}
	return nil
}

// isUnsafeOpIDRune reports whether r is forbidden in an op-id.
// Forbids path separators, all C0 control characters (which includes
// NUL, line feed, carriage return), and the unicode right-to-left
// override.
func isUnsafeOpIDRune(r rune) bool {
	if r < 0x20 { // C0 controls 0x00..0x1F
		return true
	}
	switch r {
	case '/', '\\':
		return true
	case 0x202E: // RIGHT-TO-LEFT OVERRIDE
		return true
	}
	return false
}

// OpID returns the session's operator identity.
func (s *Session) OpID() string {
	if s == nil {
		return ""
	}
	return s.opID
}

// Active reports whether the session is currently active. A nil
// receiver returns false (so callers can safely query an unstarted or
// failed session).
func (s *Session) Active() bool {
	return s != nil && s.active
}

// End closes the session. After End, Active reports false and OpID
// continues to return the historical op-id for audit purposes.
// End on a nil receiver is a no-op.
func (s *Session) End() {
	if s == nil {
		return
	}
	s.active = false
}
