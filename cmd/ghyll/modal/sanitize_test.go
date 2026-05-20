package modal

import "testing"

func TestScenario_SanitizeLine_StripsControlBytes(t *testing.T) {
	cases := map[string]string{
		"\x1b[2J\x1b[Hattack": "?[2J?[Hattack",
		"OSC \x07bell":        "OSC ?bell",
		"plain text":          "plain text",
		"\nleading newline":   "?leading newline",
		"trailing\x00null":    "trailing?null",
		"":                    "",
	}
	for in, want := range cases {
		got := SanitizeLine(in)
		if got != want {
			t.Errorf("SanitizeLine(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestScenario_SanitizeLine_ReplacesNonASCII(t *testing.T) {
	// RTL override U+202E becomes its 3-byte UTF-8 sequence
	// 0xE2 0x80 0xAE — all >= 0x80, all replaced. Use the
	// explicit \u escape rather than a literal to keep
	// staticcheck ST1018 happy.
	in := "alice\u202e.gpj"
	got := SanitizeLine(in)
	for _, c := range []byte(got) {
		if c >= 0x80 {
			t.Errorf("SanitizeLine left non-ASCII byte 0x%02x in %q", c, got)
		}
	}
}
