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

// TestRenderer_Spinner_NonTTY — bytes.Buffer is not a TTY, so
// StartSpinner must be a no-op. Verifies the isTTY guard short-
// circuits before any goroutine starts. If this regressed, every
// test that writes to a buffer would race the goroutine.
func TestRenderer_Spinner_NonTTY(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf)
	r.StartSpinner("kimi is thinking…")
	r.StopSpinner() // idempotent on no-op
	if buf.Len() != 0 {
		t.Errorf("non-TTY spinner should not write, got %q", buf.String())
	}
}

// TestRenderer_Spinner_StopBeforeStart — StopSpinner without a
// prior StartSpinner is a no-op. Defensive: a future caller might
// stop in a defer without checking start succeeded.
func TestRenderer_Spinner_StopBeforeStart(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf)
	r.StopSpinner() // must not deadlock or panic
}

// TestRenderer_Spinner_DoubleStart — StartSpinner twice in a row
// without an intervening StopSpinner is a no-op for the second
// call. Prevents a duplicate goroutine from leaking on a retry.
func TestRenderer_Spinner_DoubleStart(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf)
	r.StartSpinner("first")
	r.StartSpinner("second") // no-op
	r.StopSpinner()
}

// TestRenderer_Delta_StopsSpinner — RenderDelta calls StopSpinner
// before writing. On a non-TTY this is degenerate but exercises
// the centralization contract. The buffer should contain ONLY the
// delta — no spinner clear sequence (because spinner never ran).
func TestRenderer_Delta_StopsSpinner(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf)
	r.StartSpinner("thinking…")
	r.RenderDelta("hello")
	if !strings.Contains(buf.String(), "hello") {
		t.Errorf("delta missing from output: %q", buf.String())
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
