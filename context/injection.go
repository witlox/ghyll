package context

import (
	"encoding/base64"
	"strings"
	"unicode/utf8"

	"github.com/witlox/ghyll/types"
)

// InjectionSignal reports a detected prompt injection pattern.
type InjectionSignal struct {
	Turn    int
	Pattern string // "instruction_override", "base64_payload", "sensitive_path", "system_prompt_modify"
	Snippet string
}

// DetectInjectionSignals scans messages for prompt injection patterns.
// Checked at checkpoint creation time. Detection only — Tarn enforces.
func DetectInjectionSignals(msgs []types.Message, startTurn int) []InjectionSignal {
	var signals []InjectionSignal

	for i, msg := range msgs {
		// Only scan user input and tool output — not the model's own responses
		if msg.Role != "user" && msg.Role != "tool" {
			continue
		}
		turn := startTurn + i
		content := strings.ToLower(msg.Content)

		// Instruction override patterns
		overridePatterns := []string{
			"ignore previous instructions",
			"ignore all previous",
			"disregard previous",
			"forget your instructions",
			"new instructions:",
			"override system prompt",
			"you are now",
			"act as if",
		}
		for _, p := range overridePatterns {
			if strings.Contains(content, p) {
				signals = append(signals, InjectionSignal{
					Turn:    turn,
					Pattern: "instruction_override",
					Snippet: truncate(msg.Content, 100),
				})
				break
			}
		}

		// Sensitive path access
		sensitivePaths := []string{
			"~/.ssh/", "/etc/shadow", "/etc/passwd",
			".env", "credentials", "id_rsa",
			"private_key", "secret_key", "/root/",
		}
		for _, p := range sensitivePaths {
			if strings.Contains(content, p) {
				signals = append(signals, InjectionSignal{
					Turn:    turn,
					Pattern: "sensitive_path",
					Snippet: truncate(msg.Content, 100),
				})
				break
			}
		}

		// Base64 payload detection (long base64 strings)
		if containsBase64Payload(msg.Content) {
			signals = append(signals, InjectionSignal{
				Turn:    turn,
				Pattern: "base64_payload",
				Snippet: truncate(msg.Content, 100),
			})
		}

		// System prompt modification
		sysModPatterns := []string{
			"modify your system prompt",
			"change your system message",
			"update your instructions",
			"rewrite your prompt",
		}
		for _, p := range sysModPatterns {
			if strings.Contains(content, p) {
				signals = append(signals, InjectionSignal{
					Turn:    turn,
					Pattern: "system_prompt_modify",
					Snippet: truncate(msg.Content, 100),
				})
				break
			}
		}
	}

	return signals
}

func containsBase64Payload(s string) bool {
	// Look for base64-like strings of significant length (>40 chars)
	words := strings.Fields(s)
	for _, w := range words {
		if len(w) < 40 {
			continue
		}
		// Check if it looks like base64
		if isBase64Like(w) {
			// Try to decode — if it decodes to valid UTF-8, suspicious
			decoded, err := base64.StdEncoding.DecodeString(w)
			if err == nil && utf8.Valid(decoded) {
				return true
			}
			// Try URL-safe base64
			decoded, err = base64.URLEncoding.DecodeString(w)
			if err == nil && utf8.Valid(decoded) {
				return true
			}
		}
	}
	return false
}

func isBase64Like(s string) bool {
	for _, c := range s {
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '+', c == '/', c == '=', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
