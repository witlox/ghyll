package tool

import (
	"strings"
	"testing"
)

// TestScenario_parseSearchResults_FiltersDangerousSchemes verifies
// Tier 3 / SR L-7: data:/file:/javascript: URLs are dropped.
func TestScenario_parseSearchResults_FiltersDangerousSchemes(t *testing.T) {
	html := `
<a href="https://safe.example.com/article">safe</a>
<a href="data:text/html,evil">data url</a>
<a href="file:///etc/passwd">file url</a>
<a href="javascript:alert(1)">js url</a>
<a href="http://other.example.com/x">http</a>
`
	got := parseSearchResults(html, 10)
	if strings.Contains(got, "data:") {
		t.Error("data: URL slipped through")
	}
	if strings.Contains(got, "file:") {
		t.Error("file: URL slipped through")
	}
	if strings.Contains(got, "javascript:") {
		t.Error("javascript: URL slipped through")
	}
	if !strings.Contains(got, "safe.example.com") {
		t.Error("https URL filtered (false positive)")
	}
}

// TestScenario_stripDangerousRunes_RemovesRTLOverride verifies
// Tier 3 / SR M-1: U+202E and other format characters are
// scrubbed.
func TestScenario_stripDangerousRunes_RemovesRTLOverride(t *testing.T) {
	in := "alice\u202e.gpj"
	got := stripDangerousRunes(in)
	for _, r := range got {
		if r == '\u202e' || r == '\u202d' || r == '\u200b' {
			t.Errorf("format rune survived: %q", got)
		}
	}
}

// TestScenario_htmlToMarkdown_CapsInputSize verifies Tier 3 / SR
// M-2: htmlToMarkdown truncates input before regex stripping so
// hostile multi-MB HTML doesn't CPU-stall.
func TestScenario_htmlToMarkdown_CapsInputSize(t *testing.T) {
	// 1 MiB of `<script` with no close tag — would backtrack
	// indefinitely if not capped.
	huge := "<script" + strings.Repeat("x", 1024*1024) + "</script>"
	got := htmlToMarkdown(huge)
	if len(got) > maxHTMLBytesForRegex+1024 {
		t.Errorf("output length %d; expected near %d cap", len(got), maxHTMLBytesForRegex)
	}
}
