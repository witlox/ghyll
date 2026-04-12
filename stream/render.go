package stream

import (
	"fmt"
	"io"
	"strings"
)

// Renderer handles terminal output for streaming responses.
type Renderer struct {
	w io.Writer
}

// NewRenderer creates a terminal renderer.
func NewRenderer(w io.Writer) *Renderer {
	return &Renderer{w: w}
}

// RenderDelta writes a content delta to the terminal in real time.
func (r *Renderer) RenderDelta(delta string) {
	_, _ = fmt.Fprint(r.w, delta)
}

// RenderComplete writes a final newline after a complete response.
func (r *Renderer) RenderComplete() {
	_, _ = fmt.Fprintln(r.w)
}

// RenderToolCall displays a tool call being executed.
func (r *Renderer) RenderToolCall(name string, args string) {
	_, _ = fmt.Fprintf(r.w, "  ▶ %s", name)
	// Show truncated args for context
	if args != "" {
		display := args
		if len(display) > 80 {
			display = display[:80] + "..."
		}
		// Clean up JSON for display
		display = strings.ReplaceAll(display, "\n", " ")
		_, _ = fmt.Fprintf(r.w, " %s", display)
	}
	_, _ = fmt.Fprintln(r.w)
}

// RenderToolResult displays the output of a tool execution.
func (r *Renderer) RenderToolResult(output string, err string, timedOut bool) {
	if timedOut {
		_, _ = fmt.Fprintln(r.w, "  ⚠ timed out")
		return
	}
	if err != "" {
		_, _ = fmt.Fprintf(r.w, "  ✗ %s\n", truncateLines(err, 5))
		return
	}
	if output != "" {
		lines := truncateLines(output, 10)
		_, _ = fmt.Fprintf(r.w, "  %s\n", lines)
	}
}

// RenderWarning displays a warning message.
func (r *Renderer) RenderWarning(msg string) {
	_, _ = fmt.Fprintf(r.w, "⚠ %s\n", msg)
}

// RenderInfo displays an informational message.
func (r *Renderer) RenderInfo(msg string) {
	_, _ = fmt.Fprintf(r.w, "ℹ %s\n", msg)
}

// RenderError displays an error message.
func (r *Renderer) RenderError(msg string) {
	_, _ = fmt.Fprintf(r.w, "✗ %s\n", msg)
}

// RenderModelSwitch displays a model switch indicator.
func (r *Renderer) RenderModelSwitch(from, to string, checkpoint int) {
	_, _ = fmt.Fprintf(r.w, "⟳ switched to %s, loaded from checkpoint %d\n", to, checkpoint)
}

func truncateLines(s string, maxLines int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return strings.TrimRight(s, "\n")
	}
	result := strings.Join(lines[:maxLines], "\n")
	return result + fmt.Sprintf("\n  ... (%d more lines)", len(lines)-maxLines)
}
