package runner

import (
	"fmt"
	"strings"
)

// Shared sanitizer for operator-supplied free-text fields that get
// serialized into wire-stable outputs (Result.Details, finding
// descriptions, amendment summaries, etc.). Per validation-pass-4
// F43 + validation-pass-5 F11: embedded newlines / control chars
// in operator narrative can forge structured fields in line-based
// log parsers.
//
// Length caps per validation-pass-5 F21: an LLM hook can return
// megabytes of text. Caps keep memory bounded and log lines readable.

const (
	// maxFreeTextLen bounds operator-supplied free-text fields
	// (Description, Evidence). Excess is truncated with a marker.
	maxFreeTextLen = 8 * 1024
)

// sanitizeOneLine replaces newlines, carriage returns, tabs, and
// other control characters with escape sequences and truncates
// strings that exceed maxFreeTextLen. Use for any operator-supplied
// text that flows into a single-line output or a wire-stable detail
// field.
func sanitizeOneLine(s string) string {
	if len(s) > maxFreeTextLen {
		s = s[:maxFreeTextLen] + "... (truncated)"
	}
	r := strings.NewReplacer(
		"\n", "\\n",
		"\r", "\\r",
		"\t", "\\t",
		"\x00", "\\x00",
	)
	out := r.Replace(s)
	var b strings.Builder
	b.Grow(len(out))
	for _, ch := range out {
		if ch < 0x20 || ch == 0x7f {
			fmt.Fprintf(&b, "\\x%02x", ch)
			continue
		}
		b.WriteRune(ch)
	}
	return b.String()
}
