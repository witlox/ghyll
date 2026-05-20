package dialect

import (
	"strings"
	"testing"
)

func TestScenario_GLMCompactionPrompt_NotEmpty(t *testing.T) {
	p := GLMCompactionPrompt()
	if p == "" {
		t.Error("GLMCompactionPrompt returned empty string")
	}
	if !strings.Contains(strings.ToLower(p), "summary") &&
		!strings.Contains(strings.ToLower(p), "summarize") &&
		!strings.Contains(strings.ToLower(p), "compact") {
		t.Errorf("prompt lacks compaction keyword: %q", p)
	}
}

func TestScenario_MinimaxCompactionPrompt_NotEmpty(t *testing.T) {
	p := MinimaxCompactionPrompt()
	if p == "" {
		t.Error("MinimaxCompactionPrompt returned empty string")
	}
}
