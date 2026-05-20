package dialect

import "testing"

// Tier 3 coverage push — exercise stripANSI's escape-stripping
// branches.

func TestScenario_stripANSI_RemovesEscapeSequences(t *testing.T) {
	cases := map[string]string{
		"plain text":               "plain text",
		"\x1b[31mred\x1b[0m":       "red",
		"\x1b[1;33;44mfancy\x1b[m": "fancy",
		"no esc here":              "no esc here",
		"\x1b[42mcolor at start":   "color at start",
		"end with esc\x1b[K":       "end with esc",
		"":                         "",
	}
	for in, want := range cases {
		got := stripANSI(in)
		if got != want {
			t.Errorf("stripANSI(%q) = %q; want %q", in, got, want)
		}
	}
}

// TestScenario_stripANSI_FastPath verifies the no-ESC fast path
// returns the input string unchanged (no allocation).
func TestScenario_stripANSI_FastPath(t *testing.T) {
	in := "no escapes here at all"
	if got := stripANSI(in); got != in {
		t.Errorf("fast path mutated: %q", got)
	}
}
