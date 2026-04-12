package stream

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderer_Delta(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf)
	r.RenderDelta("hello ")
	r.RenderDelta("world")
	r.RenderComplete()

	if buf.String() != "hello world\n" {
		t.Errorf("output = %q", buf.String())
	}
}

func TestRenderer_ToolCall(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf)
	r.RenderToolCall("bash", `{"command":"ls -la"}`)

	if !strings.Contains(buf.String(), "▶ bash") {
		t.Errorf("output = %q", buf.String())
	}
}

func TestRenderer_ToolResult_Output(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf)
	r.RenderToolResult("file.go\nutil.go\n", "", false)

	if !strings.Contains(buf.String(), "file.go") {
		t.Errorf("output = %q", buf.String())
	}
}

func TestRenderer_ToolResult_Error(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf)
	r.RenderToolResult("", "command not found", false)

	if !strings.Contains(buf.String(), "✗") {
		t.Errorf("output = %q", buf.String())
	}
}

func TestRenderer_ToolResult_Timeout(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf)
	r.RenderToolResult("", "", true)

	if !strings.Contains(buf.String(), "timed out") {
		t.Errorf("output = %q", buf.String())
	}
}

func TestRenderer_TruncateLines(t *testing.T) {
	long := strings.Repeat("line\n", 20)
	var buf bytes.Buffer
	r := NewRenderer(&buf)
	r.RenderToolResult(long, "", false)

	if !strings.Contains(buf.String(), "more lines") {
		t.Errorf("expected truncation indicator in:\n%s", buf.String())
	}
}

func TestRenderer_ModelSwitch(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf)
	r.RenderModelSwitch("m25", "glm5", 4)

	if !strings.Contains(buf.String(), "⟳ switched to glm5") {
		t.Errorf("output = %q", buf.String())
	}
}
