package modal

import "strings"

// SanitizeLine strips control bytes (< 0x20, 0x7F) and non-ASCII
// bytes from s so the result is safe to render in a tty prompt.
// Gate-2 SEC-H-5: a malicious grid (or attacker-crafted clause
// concept) could embed ANSI escape sequences that smuggle terminal
// commands into the operator's session — title sets, OSC-52
// clipboard writes, cursor repositioning. SanitizeLine fires
// defense-in-depth at every TermModal prompt rendering site.
//
// Conservative: non-ASCII bytes become "?". Operators rendering
// non-ASCII identifiers will see the substitution; tighter
// allow-listing can land in a Tier 3 polish pass.
func SanitizeLine(s string) string {
	if s == "" {
		return ""
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c < 0x20 || c == 0x7F:
			out = append(out, '?')
		case c >= 0x80:
			out = append(out, '?')
		default:
			out = append(out, c)
		}
	}
	return strings.TrimSpace(string(out))
}
