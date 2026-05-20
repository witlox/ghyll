package dialect

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/witlox/ghyll/types"
)

// cleanWorkdir strips control characters and ANSI escape sequences
// from the workdir before it lands in a system prompt. Per
// validation-pass-8 D6: a workdir from CLI flag / cwd could contain
// embedded newlines or ANSI from a hostile parent process; the
// dialect MUST NOT inject those into the system prompt verbatim.
//
// Returns the safe form. An empty input returns ".".
func cleanWorkdir(workdir string) string {
	workdir = strings.TrimSpace(workdir)
	if workdir == "" {
		return "."
	}
	// Strip ANSI escape sequences (CSI sequences specifically; the
	// most common attack vector).
	workdir = stripANSI(workdir)
	// Then strip every control char + non-printable; replace with
	// underscore to keep the path roughly recognizable.
	var b strings.Builder
	b.Grow(len(workdir))
	for _, r := range workdir {
		if r == utf8.RuneError {
			b.WriteRune('_')
			continue
		}
		if unicode.IsControl(r) {
			b.WriteRune('_')
			continue
		}
		// Reject Unicode line separators that may surprise downstream
		// log parsers.
		if r == 0x85 || r == 0x2028 || r == 0x2029 {
			b.WriteRune('_')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// maxHandoffSummaryBytes caps the operator-controlled Summary
// embedded in HandoffSummary system prompts. Tier 3 / SR C-2.
const maxHandoffSummaryBytes = 8 * 1024

// sanitizeHandoffField scrubs a checkpoint-derived field before it
// lands in the system prompt. Tier 3 / SR C-2:
//
//   - Cap length at maxHandoffSummaryBytes (otherwise a poisoned
//     checkpoint could exhaust the context window).
//   - Strip ANSI escape sequences via stripANSI.
//   - Drop control bytes and Unicode line separators
//     (U+2028 / U+2029) that some terminals/parsers treat as
//     newlines — but preserve \n / \r / \t for legibility.
//   - Replace dialect system-prompt header markers
//     ("--- SYSTEM", "=== SYSTEM", "\nsystem:", "\nassistant:",
//     "\nuser:") with [REDACTED-HEADER] so a poisoned summary
//     can't smuggle "SYSTEM OVERRIDE" into the prompt.
func sanitizeHandoffField(s string) string {
	if s == "" {
		return ""
	}
	if len(s) > maxHandoffSummaryBytes {
		s = s[:maxHandoffSummaryBytes]
	}
	s = stripANSI(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == 0x2028 || r == 0x2029 || r == 0x85:
			b.WriteRune(' ')
		case unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t':
			// drop
		default:
			b.WriteRune(r)
		}
	}
	cleaned := b.String()
	for _, marker := range []string{
		"--- SYSTEM",
		"=== SYSTEM",
		"\nsystem:",
		"\nassistant:",
		"\nuser:",
	} {
		cleaned = strings.ReplaceAll(cleaned, marker, "[REDACTED-HEADER]")
	}
	return cleaned
}

// stripANSI removes CSI escape sequences (the ESC [ ... letter form)
// from s. Not exhaustive — does not handle DCS / OSC / etc. — but
// covers the common terminal-corruption vectors.
func stripANSI(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] == 0x1b && i+1 < len(runes) && runes[i+1] == '[' {
			// Skip until a final byte in 0x40-0x7E (per ECMA-48).
			j := i + 2
			for j < len(runes) {
				r := runes[j]
				if r >= 0x40 && r <= 0x7E {
					j++
					break
				}
				j++
			}
			i = j - 1
			continue
		}
		b.WriteRune(runes[i])
	}
	return b.String()
}

// buildOpenAIMessages is the shared message-shape builder used by
// all four dialects per validation-pass-8 D8. ADR-001 keeps the
// public seven-function shape; the shared helper eliminates the
// four-way drift hazard between the family files.
func buildOpenAIMessages(msgs []types.Message, systemPrompt string) []map[string]any {
	result := make([]map[string]any, 0, len(msgs)+1)
	result = append(result, map[string]any{
		"role":    "system",
		"content": systemPrompt,
	})
	for _, msg := range msgs {
		m := map[string]any{
			"role":    msg.Role,
			"content": msg.Content,
		}
		if len(msg.ToolCalls) > 0 {
			m["tool_calls"] = msg.ToolCalls
		}
		if msg.ToolCallID != "" {
			m["tool_call_id"] = msg.ToolCallID
		}
		if msg.Name != "" {
			m["name"] = msg.Name
		}
		result = append(result, m)
	}
	return result
}

// runeAwareTokenCount estimates token count for msgs using rune-
// based counting per validation-pass-8 D2. ASCII content tokenizes
// at ~`runesPerToken` chars-per-token; multibyte content (CJK,
// emoji) tokenizes at closer to 1 token per rune.
//
// The estimator computes max(rune_count/runesPerToken,
// non_ascii_rune_count) so multibyte content doesn't silently
// undercount. Conservative direction: better to compact early than
// to overflow the model's context.
//
// constOverhead per message + perToolCall constants come from
// OpenAI's published per-message accounting (4 / 10 respectively).
// They're approximations — operators with hot routing should tune.
func runeAwareTokenCount(msgs []types.Message, runesPerToken int) int {
	if runesPerToken < 1 {
		runesPerToken = 4
	}
	total := 0
	for _, msg := range msgs {
		runes := utf8.RuneCountInString(msg.Content)
		ascii := countASCII(msg.Content)
		nonAscii := runes - ascii
		// One token per non-ASCII rune (lower bound for BPE on CJK);
		// runesPerToken for the ASCII portion.
		contentTokens := ascii/runesPerToken + nonAscii
		if contentTokens < 0 {
			contentTokens = 0
		}
		total += contentTokens
		for _, tc := range msg.ToolCalls {
			nameR := utf8.RuneCountInString(tc.Function.Name)
			argsR := utf8.RuneCountInString(tc.Function.Arguments)
			total += nameR/runesPerToken + argsR/runesPerToken + 10
		}
		total += 4
	}
	return total
}

// countASCII returns the number of ASCII runes (r < 0x80) in s.
// Used together with utf8.RuneCountInString to split content into
// ASCII / non-ASCII halves for tier-appropriate token estimation.
func countASCII(s string) int {
	n := 0
	for _, r := range s {
		if r < 0x80 {
			n++
		}
	}
	return n
}

// isZeroCheckpoint reports whether cp is the zero-value Checkpoint
// signalling "no real checkpoint to resume from." HandoffSummary
// funcs use this to skip the "Continuing from checkpoint..." framing
// when the session-loop layer accidentally passed a sentinel.
func isZeroCheckpoint(turn int, summary string) bool {
	return turn == 0 && strings.TrimSpace(summary) == ""
}
