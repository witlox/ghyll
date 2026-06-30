package stream

import (
	"bytes"
	"strings"
	"testing"
	"time"
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
// StartSpinner falls back to the heartbeat: a single initial
// status line, then ticks every heartbeatInterval. We stop
// immediately, so only the initial line should be visible.
// Pre-ADR-018-followup this was a silent no-op; the new behavior
// gives operators on sandboxed wrappers proof-of-life output.
func TestRenderer_Spinner_NonTTY(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf)
	r.StartSpinner("kimi is thinking…")
	r.StopSpinner()
	got := buf.String()
	if !strings.Contains(got, "ℹ kimi is thinking…") {
		t.Errorf("expected initial heartbeat line, got %q", got)
	}
	if strings.Contains(got, "…") && strings.Contains(got, "s\n  …") {
		t.Errorf("no tick lines expected on immediate stop, got %q", got)
	}
}

// TestRenderer_Heartbeat_Ticks — when StartSpinner runs longer than
// the heartbeat interval, periodic `… {elapsed}s` lines appear.
// Operators see a growing list confirming ghyll is alive while the
// gateway is slow. Uses heartbeatOverride to avoid sleeping real
// production seconds.
func TestRenderer_Heartbeat_Ticks(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf)
	r.heartbeatOverride = 20 * time.Millisecond
	r.StartSpinner("waiting on gateway")
	time.Sleep(80 * time.Millisecond) // ≥ 3 ticks expected
	r.StopSpinner()
	out := buf.String()
	if !strings.Contains(out, "ℹ waiting on gateway") {
		t.Fatalf("missing initial line: %q", out)
	}
	tickCount := strings.Count(out, "  … ")
	if tickCount < 2 {
		t.Errorf("expected ≥2 tick lines after 80ms with 20ms interval, got %d in %q", tickCount, out)
	}
}

// TestRenderer_Heartbeat_StopHaltsTicks — once StopSpinner returns,
// no further tick lines may be written. Without this, a late tick
// could interleave with the next phase's output (tool result,
// next prompt).
func TestRenderer_Heartbeat_StopHaltsTicks(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf)
	r.heartbeatOverride = 10 * time.Millisecond
	r.StartSpinner("thinking")
	time.Sleep(15 * time.Millisecond)
	r.StopSpinner()
	before := buf.Len()
	time.Sleep(50 * time.Millisecond) // would emit ≥4 more ticks if not halted
	if buf.Len() != before {
		t.Errorf("heartbeat continued after Stop: +%d bytes (%q)", buf.Len()-before, buf.String()[before:])
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
