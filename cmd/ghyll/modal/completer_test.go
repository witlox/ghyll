package modal

import (
	"strings"
	"testing"
)

// TestScenario_SlashCompleter_PrefixMatch — typing "/li<Tab>"
// completes "list-arrows". Verifies the prefix slice math: the
// returned suggestion replaces only the partial prefix, not the
// whole line.
func TestScenario_SlashCompleter_PrefixMatch(t *testing.T) {
	c := &slashCompleter{getCommands: func() []string {
		return []string{"list-arrows", "run-arrow", "drain-amendments"}
	}}
	line := []rune("/li")
	matches, prefixLen := c.Do(line, len(line))
	if prefixLen != 2 { // "li" is 2 runes
		t.Errorf("prefixLen = %d, want 2", prefixLen)
	}
	if len(matches) != 1 {
		t.Fatalf("matches = %d, want 1: %v", len(matches), matchesToStrings(matches))
	}
	if string(matches[0]) != "st-arrows" {
		t.Errorf("match = %q, want %q", string(matches[0]), "st-arrows")
	}
}

// TestScenario_SlashCompleter_MultipleMatches — "/" with no further
// chars returns all commands; "/a" returns both "attest" and
// "adversary"; readline displays the menu.
func TestScenario_SlashCompleter_MultipleMatches(t *testing.T) {
	c := &slashCompleter{getCommands: func() []string {
		return []string{"attest", "adversary", "exit"}
	}}
	matches, prefixLen := c.Do([]rune("/a"), 2)
	if prefixLen != 1 {
		t.Errorf("prefixLen = %d, want 1", prefixLen)
	}
	if len(matches) != 2 {
		t.Errorf("matches = %d, want 2: %v", len(matches), matchesToStrings(matches))
	}
}

// TestScenario_SlashCompleter_IgnoresNonSlash — Tab on a non-slash
// prompt (model input, modal verdict) returns no suggestions. The
// REPL and TermModal share the LineReader, so the completer firing
// on a verdict prompt would suggest "/exit" mid-typing and confuse
// the operator.
func TestScenario_SlashCompleter_IgnoresNonSlash(t *testing.T) {
	c := &slashCompleter{getCommands: func() []string { return []string{"exit"} }}
	matches, _ := c.Do([]rune("hello"), 5)
	if len(matches) != 0 {
		t.Errorf("non-slash input should not complete, got %v", matchesToStrings(matches))
	}
}

// TestScenario_SlashCompleter_IgnoresAfterSpace — once the operator
// has typed a space (entering args), Tab no longer completes the
// command name. /attest A-... should not auto-complete A-... into
// some other built-in.
func TestScenario_SlashCompleter_IgnoresAfterSpace(t *testing.T) {
	c := &slashCompleter{getCommands: func() []string {
		return []string{"attest", "exit"}
	}}
	line := []rune("/attest a")
	matches, _ := c.Do(line, len(line))
	if len(matches) != 0 {
		t.Errorf("post-space input should not complete, got %v", matchesToStrings(matches))
	}
}

// TestScenario_SlashCompleter_EmptyCommandList — defensive: a
// session with no workflow + no builtins still returns cleanly.
func TestScenario_SlashCompleter_EmptyCommandList(t *testing.T) {
	c := &slashCompleter{getCommands: func() []string { return nil }}
	matches, _ := c.Do([]rune("/foo"), 4)
	if matches != nil {
		t.Errorf("empty command list should yield nil matches, got %v", matchesToStrings(matches))
	}
}

// TestScenario_NewLineReader_HeadlessStillWorks — bytes.Buffer is
// not a TTY; NewInteractiveLineReader must fall back to the scanner
// path. Confirms backward compatibility for tests and pipes.
func TestScenario_NewLineReader_HeadlessFallback(t *testing.T) {
	in := strings.NewReader("hello\nworld\n")
	r := NewInteractiveLineReader(in, InteractiveOpts{
		CompleteCommands: func() []string { return []string{"exit"} },
	})
	defer r.Close()

	if r.IsInteractive() {
		t.Errorf("non-TTY reader should not be interactive")
	}
	// SetPrompt is a no-op in headless mode but must not panic.
	r.SetPrompt("> ")
}

func matchesToStrings(m [][]rune) []string {
	out := make([]string, len(m))
	for i, r := range m {
		out[i] = string(r)
	}
	return out
}
