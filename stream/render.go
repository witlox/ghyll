package stream

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Renderer handles terminal output for streaming responses.
type Renderer struct {
	w io.Writer

	// Spinner state. Lifecycle: StartSpinner → goroutine draws frames
	// at spinnerInterval → StopSpinner (also called by RenderDelta /
	// RenderToolCall on first output to clear the line) → wait for
	// goroutine. spinMu serializes start/stop; spinDone signals exit
	// to the goroutine; spinDead is closed by the goroutine on exit
	// so StopSpinner can synchronously wait, preventing a late frame
	// from racing with the following Render call.
	spinMu   sync.Mutex
	spinDone chan struct{}
	spinDead chan struct{}
}

// spinnerInterval controls frame redraw cadence. 80ms ≈ 12 fps —
// the eye reads it as smooth without spamming SSH terminals.
const spinnerInterval = 80 * time.Millisecond

// spinnerFrames are Unicode braille glyphs. Single column (one cell
// per frame) so the cursor doesn't visually shift. ASCII fallback
// kicks in via the isTTY guard — non-TTY writers skip the goroutine
// entirely so log captures and CI runs stay clean.
var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// NewRenderer creates a terminal renderer.
func NewRenderer(w io.Writer) *Renderer {
	return &Renderer{w: w}
}

// StartSpinner begins drawing a single-line spinner with `label`
// (e.g. "kimi is thinking"). No-op when the writer isn't a TTY,
// when a spinner is already running, or when called concurrently
// with a stop. Idempotent at the start of every model turn — the
// first output (StopSpinner via RenderDelta or RenderToolCall)
// clears it.
func (r *Renderer) StartSpinner(label string) {
	if !isTTY(r.w) {
		return
	}
	r.spinMu.Lock()
	defer r.spinMu.Unlock()
	if r.spinDone != nil {
		return
	}
	done := make(chan struct{})
	dead := make(chan struct{})
	r.spinDone = done
	r.spinDead = dead

	go func() {
		defer close(dead)
		ticker := time.NewTicker(spinnerInterval)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-done:
				// Clear the line on exit: CR + erase-to-end-of-line.
				// Cheap on any VT100-compatible terminal (xterm, screen,
				// tmux, modern Windows Terminal). No-op on a non-TTY
				// would land here only via a buggy isTTY, so worst case
				// we leak two harmless bytes per turn.
				_, _ = fmt.Fprint(r.w, "\r\033[K")
				return
			case <-ticker.C:
				_, _ = fmt.Fprintf(r.w, "\r  %c %s", spinnerFrames[i], label)
				i = (i + 1) % len(spinnerFrames)
			}
		}
	}()
}

// StopSpinner signals the spinner goroutine to exit and waits for
// it to finish (so a stray frame can't be drawn after the next
// Render call writes its content). Idempotent — calling it twice
// or before StartSpinner is a no-op. Centralized in RenderDelta /
// RenderToolCall so callers don't need to bracket every output path.
func (r *Renderer) StopSpinner() {
	r.spinMu.Lock()
	done := r.spinDone
	dead := r.spinDead
	r.spinDone = nil
	r.spinDead = nil
	r.spinMu.Unlock()

	if done == nil {
		return
	}
	close(done)
	<-dead
}

// isTTY reports whether w writes to a character device (terminal).
// Stdlib-only check: cast to *os.File, stat, look for ModeCharDevice.
// io.Writer wrappers (bytes.Buffer in tests, file redirects, pipes)
// fail the cast or stat → spinner is skipped, output stays plain.
func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// RenderDelta writes a content delta to the terminal in real time.
// First call also stops the spinner — by definition we now have
// content to show, so the "thinking" indicator has served its
// purpose.
func (r *Renderer) RenderDelta(delta string) {
	r.StopSpinner()
	_, _ = fmt.Fprint(r.w, delta)
}

// RenderComplete writes a final newline after a complete response.
func (r *Renderer) RenderComplete() {
	_, _ = fmt.Fprintln(r.w)
}

// RenderToolCall displays a tool call being executed.
func (r *Renderer) RenderToolCall(name string, args string) {
	r.StopSpinner()
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
