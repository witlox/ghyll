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

	// heartbeatOverride lets tests substitute a short tick interval
	// for the non-TTY heartbeat without sleeping real seconds. Zero
	// (the production case) selects the heartbeatInterval const.
	heartbeatOverride time.Duration
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

// SetHeartbeatInterval overrides the non-TTY heartbeat tick cadence.
// Production code does not call this — the heartbeatInterval const
// is the operator-facing default. Acceptance / unit tests use it to
// avoid sleeping real production seconds while asserting on tick
// output. A zero or negative duration restores the default.
func (r *Renderer) SetHeartbeatInterval(d time.Duration) {
	r.spinMu.Lock()
	r.heartbeatOverride = d
	r.spinMu.Unlock()
}

// heartbeatInterval is the cadence of the non-TTY proof-of-life
// tick. 5s is long enough that a fast turn produces zero noise but
// short enough that an operator on a slow gateway sees activity
// before they wonder if ghyll is hung.
const heartbeatInterval = 5 * time.Second

// StartSpinner begins drawing a single-line spinner with `label`
// (e.g. "kimi is thinking"). When the writer is a TTY this is an
// animated single-line braille spinner; when the writer is NOT a
// TTY (pipe, sandboxed wrapper that captures stdout, log redirect),
// it falls back to a heartbeat: one initial status line, then a
// `… {elapsed}s` tick line every heartbeatInterval until StopSpinner.
// Either form is idempotent — start-while-running is a no-op —
// and is cleared/halted by the next StopSpinner.
func (r *Renderer) StartSpinner(label string) {
	r.spinMu.Lock()
	defer r.spinMu.Unlock()
	if r.spinDone != nil {
		return
	}
	done := make(chan struct{})
	dead := make(chan struct{})
	r.spinDone = done
	r.spinDead = dead

	if isTTY(r.w) {
		go r.runTTYSpinner(label, done, dead)
	} else {
		// Resolve the heartbeat interval under the lock so the
		// goroutine sees a stable value even if SetHeartbeatInterval
		// runs concurrently (defensive: production callers set the
		// override only before StartSpinner, but the race detector
		// still flags unsynchronized field reads from a goroutine).
		interval := r.heartbeatOverride
		if interval <= 0 {
			interval = heartbeatInterval
		}
		go r.runHeartbeat(label, interval, done, dead)
	}
}

// runTTYSpinner draws the animated spinner. CR + erase-to-EOL on
// exit clears the line so subsequent output starts clean.
func (r *Renderer) runTTYSpinner(label string, done, dead chan struct{}) {
	defer close(dead)
	ticker := time.NewTicker(spinnerInterval)
	defer ticker.Stop()
	i := 0
	for {
		select {
		case <-done:
			_, _ = fmt.Fprint(r.w, "\r\033[K")
			return
		case <-ticker.C:
			_, _ = fmt.Fprintf(r.w, "\r  %c %s", spinnerFrames[i], label)
			i = (i + 1) % len(spinnerFrames)
		}
	}
}

// runHeartbeat emits one initial status line plus periodic tick
// lines while the model dispatch is in flight. Each tick is a new
// line (no CR overwrite) because the non-TTY target may be a log
// file or a wrapper that doesn't honor cursor control. Operators
// see a growing list of `… 5s … 10s …` ticks, which is enough to
// distinguish "ghyll is alive but waiting" from "ghyll is hung".
func (r *Renderer) runHeartbeat(label string, interval time.Duration, done, dead chan struct{}) {
	defer close(dead)
	_, _ = fmt.Fprintf(r.w, "ℹ %s\n", label)
	start := time.Now()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			elapsed := time.Since(start).Round(time.Second)
			_, _ = fmt.Fprintf(r.w, "  … %s\n", elapsed)
		}
	}
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
