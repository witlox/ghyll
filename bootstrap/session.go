package bootstrap

import (
	"errors"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
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

// StartSession validates the op-id, NFC-normalizes it, and returns a
// new active session bound to the normalized form.
//
// op-id must be non-empty after whitespace trimming, at most
// MaxOpIDLength bytes long when trimmed, valid UTF-8, and free of
// characters that could leak into filesystem paths or break record
// encodings:
//
//   - Path separators ('/', '\\')
//   - C0 / C1 control characters (incl. NUL, tab, line feed, CR, DEL)
//   - Unicode bidi controls per CVE-2021-42574 (LRE, RLE, PDF, LRO,
//     RLO, LRI, RLI, FSI, PDI) — broader than just RTL override
//   - Invisible / zero-width characters: ZWSP (U+200B), ZWNJ (U+200C),
//     ZWJ (U+200D), BOM / ZWNBSP (U+FEFF), word joiner (U+2060),
//     left-to-right / right-to-left marks (U+200E, U+200F)
//   - Double-dot path-traversal substring ("..")
//
// op-id is *not* used as a filesystem path component anywhere in the
// schema (D24, FM-51): it is recorded in JSON attestation records
// only. The validation above is defensive — a violation indicates an
// implementation bug elsewhere has tried to use op-id as a path.
//
// The session stores the NFC-normalized op-id (validation-pass-1
// finding #12). Two operators who typed the same logical identity
// using different Unicode encoding forms (composed vs decomposed)
// canonicalize to the same op-id. NFC does NOT fold across scripts
// (e.g., Latin 'a' vs Cyrillic 'а' remain distinct); script-confusion
// defenses would require a stricter charset restriction not yet
// imposed.
func StartSession(opID string) (*Session, error) {
	normalized, err := ValidateAndNormalizeOpID(opID)
	if err != nil {
		return nil, err
	}
	return &Session{
		opID:   normalized,
		active: true,
	}, nil
}

// ValidateOpID applies the op-id rules without creating a session.
// Returns one of the sentinel errors on failure; nil if valid.
// For the normalized form, use ValidateAndNormalizeOpID.
func ValidateOpID(opID string) error {
	_, err := ValidateAndNormalizeOpID(opID)
	return err
}

// ValidateAndNormalizeOpID applies the op-id rules and returns the
// NFC-normalized form on success.
//
// Order of operations: trim → empty check → NFC normalize →
// length check on normalized form → UTF-8 check → traversal substring
// check → per-rune scan. Length is measured on the trimmed+normalized
// form (validation-pass-1 finding #10) so leading/trailing whitespace
// does not consume the byte budget.
func ValidateAndNormalizeOpID(opID string) (string, error) {
	trimmed := strings.TrimSpace(opID)
	if trimmed == "" {
		return "", ErrOpIDRequired
	}
	// Normalize before length check so the byte budget applies to the
	// canonical form (NFC may shrink or expand by combining chars).
	normalized := norm.NFC.String(trimmed)
	if len(normalized) > MaxOpIDLength {
		return "", ErrOpIDTooLong
	}
	if !utf8.ValidString(normalized) {
		return "", ErrOpIDInvalidCharacters
	}
	if strings.Contains(normalized, "..") {
		return "", ErrOpIDInvalidCharacters
	}
	for _, r := range normalized {
		if isUnsafeOpIDRune(r) {
			return "", ErrOpIDInvalidCharacters
		}
	}
	return normalized, nil
}

// isUnsafeOpIDRune reports whether r is forbidden in an op-id.
// Forbids:
//
//   - Path separators ('/', '\\')
//   - C0 controls (U+0000..U+001F) including NUL, tab, LF, CR
//   - DEL (U+007F) and C1 controls (U+0080..U+009F)
//   - Unicode bidi controls (CVE-2021-42574 "trojan source"):
//     LRM (U+200E), RLM (U+200F), LRE (U+202A), RLE (U+202B),
//     PDF (U+202C), LRO (U+202D), RLO (U+202E), LRI (U+2066),
//     RLI (U+2067), FSI (U+2068), PDI (U+2069)
//   - Invisible / zero-width: ZWSP (U+200B), ZWNJ (U+200C),
//     ZWJ (U+200D), WJ (U+2060), BOM/ZWNBSP (U+FEFF)
//
// validation-pass-1 finding #11.
func isUnsafeOpIDRune(r rune) bool {
	if r < 0x20 || (r >= 0x7F && r < 0xA0) {
		// C0 and C1 control ranges (incl. DEL).
		return true
	}
	switch r {
	case '/', '\\':
		return true
	case 0x200B, 0x200C, 0x200D: // ZWSP / ZWNJ / ZWJ
		return true
	case 0x200E, 0x200F: // LRM / RLM
		return true
	case 0x202A, 0x202B, 0x202C, 0x202D, 0x202E: // LRE / RLE / PDF / LRO / RLO
		return true
	case 0x2060: // WORD JOINER
		return true
	case 0x2066, 0x2067, 0x2068, 0x2069: // LRI / RLI / FSI / PDI
		return true
	case 0xFEFF: // BOM / ZWNBSP
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
