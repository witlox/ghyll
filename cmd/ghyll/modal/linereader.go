package modal

import (
	"bufio"
	"context"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/chzyer/readline"
)

// LineReader is a session-scoped, single-goroutine line reader
// over an io.Reader. One reader goroutine consumes the underlying
// scanner; callers pull lines via Next(ctx).
//
// Two modes:
//   - Headless: bufio.Scanner-driven. Used when src isn't a TTY or
//     the caller didn't request an interactive reader (tests, piped
//     input, godog scenarios). Prompts are printed by the caller via
//     ui.Print; SetPrompt is a no-op.
//   - Interactive: chzyer/readline-driven. Used in production when
//     stdin is a TTY and the caller wired a completer. Provides
//     tab completion of slash commands, arrow-key history recall
//     (saved to ~/.ghyll/history), and Ctrl+R reverse search.
//
// Gate-2 CONC-C-1/C-2: the previous TermModal constructed a fresh
// bufio.Scanner per PresentVerdict/PresentEscalation call. Two
// scanners over the same stdin lost buffered bytes when ctx
// cancelled mid-read AND leaked the reader goroutine (it kept
// blocking on Scan and consumed the next stdin byte on its own).
//
// LineReader fixes both: exactly one scanner + one goroutine per
// reader, and ctx-cancel just stops the caller's receive — the
// goroutine continues reading and buffers the next line for the
// next consumer. No bytes lost; no goroutines leaked.
type LineReader struct {
	lines     chan string
	closeOnce sync.Once
	done      chan struct{}

	// Interactive-only state. nil for headless mode.
	rl       *readline.Instance
	promptMu sync.Mutex
	prompt   string
}

// defaultLineMaxBytes caps a single operator-typed line at 64
// KiB. Gate-2 SEC-M-1: the previous 1 MiB cap let the scanner
// buffer pre-validation residue notes much larger than the
// downstream ValidateUnitPayload cap (typically 16 KiB). Tighten
// at the reader so the rejection fires earlier (no megabyte
// allocations for an over-cap residue).
const defaultLineMaxBytes = 64 * 1024

// historyLimit caps the persisted history at 1000 lines per the
// readline norm. Cheap on disk (~64 KB worst case), avoids
// unbounded growth, and matches what an operator would reasonably
// scroll back to find.
const historyLimit = 1000

// InteractiveOpts configures the readline-backed reader.
type InteractiveOpts struct {
	// HistoryPath persists the input history (read on start, append
	// on each accepted line). Empty disables history.
	HistoryPath string

	// CompleteCommands returns the current set of slash-command
	// names (WITHOUT leading slash). Called at each Tab press so a
	// session can advertise dynamically-loaded workflow commands.
	// Empty / nil disables tab completion.
	CompleteCommands func() []string
}

// NewLineReader constructs a headless LineReader over src.
// Compatibility-preserving — existing tests and non-TTY callers
// land here. The reader goroutine runs until src returns EOF or
// Close() is called. Caller MUST call Close() before src is closed.
func NewLineReader(src io.Reader) *LineReader {
	r := &LineReader{
		lines: make(chan string, 16),
		done:  make(chan struct{}),
	}
	go r.readLoopScanner(src)
	return r
}

// NewInteractiveLineReader constructs a readline-backed LineReader.
// Falls back to a headless scanner when src isn't a TTY, when
// readline initialization fails, or when opts is degenerate (no
// completer, no history path) — operators on weird pty setups get
// a working (if plain) REPL instead of a hard error.
//
// The returned reader's SetPrompt method drives the readline prompt
// for subsequent Next calls. Caller is expected to SetPrompt before
// every Next so a multi-line prompt (model name, plan-mode marker,
// etc.) stays current.
func NewInteractiveLineReader(src io.Reader, opts InteractiveOpts) *LineReader {
	r := &LineReader{
		lines: make(chan string, 16),
		done:  make(chan struct{}),
	}

	if !isTTY(src) {
		go r.readLoopScanner(src)
		return r
	}

	cfg := &readline.Config{
		Prompt:                 "", // set per-call via SetPrompt
		HistoryFile:            opts.HistoryPath,
		HistoryLimit:           historyLimit,
		DisableAutoSaveHistory: false,
		InterruptPrompt:        "^C",
		EOFPrompt:              "exit",
		// HistorySearchFold makes Ctrl+R case-insensitive — the only
		// behavior anyone actually wants when recalling a previous
		// `/attest a-...` line.
		HistorySearchFold: true,
	}
	if opts.CompleteCommands != nil {
		cfg.AutoComplete = &slashCompleter{getCommands: opts.CompleteCommands}
	}
	rl, err := readline.NewEx(cfg)
	if err != nil {
		// readline init can fail on exotic terminal configs (stty
		// disabled, missing TERM, etc.). Fall back to the scanner so
		// the REPL still works, just without line editing.
		go r.readLoopScanner(src)
		return r
	}
	r.rl = rl
	go r.readLoopReadline()
	return r
}

// isTTY reports whether src is a character device (terminal).
// Stdlib-only check via *os.File.Stat — matches the pattern used
// in stream/render.go's spinner gate.
func isTTY(src io.Reader) bool {
	f, ok := src.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// SetPrompt updates the prompt displayed on the next Next call.
// In headless mode this is a no-op (the caller prints the prompt
// via ui.Print). In interactive mode it overrides the readline
// instance's prompt so the next Readline() draws the current
// session prompt (which may include model name, plan-mode marker,
// op-id, etc.).
func (r *LineReader) SetPrompt(prompt string) {
	r.promptMu.Lock()
	r.prompt = prompt
	r.promptMu.Unlock()
	if r.rl != nil {
		r.rl.SetPrompt(prompt)
	}
}

// IsInteractive reports whether the reader is in readline mode.
// Callers (REPL) check this to decide whether to print the prompt
// themselves (headless) or let SetPrompt drive readline.
func (r *LineReader) IsInteractive() bool {
	return r.rl != nil
}

func (r *LineReader) readLoopScanner(src io.Reader) {
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 4096), defaultLineMaxBytes)
	for scanner.Scan() {
		select {
		case r.lines <- scanner.Text():
		case <-r.done:
			return
		}
	}
	// EOF or scanner.Err(): signal end-of-stream by closing the
	// channel. Callers see io.EOF from Next().
	close(r.lines)
}

func (r *LineReader) readLoopReadline() {
	for {
		// Readline blocks until the user submits a line, hits
		// Ctrl+C (returns ErrInterrupt), or EOF (Ctrl+D).
		line, err := r.rl.Readline()
		if err != nil {
			if err == readline.ErrInterrupt {
				// Ctrl+C: treat as an empty line — operator
				// likely wanted to abandon a partially-typed
				// command. Don't terminate the reader.
				select {
				case r.lines <- "":
				case <-r.done:
					return
				}
				continue
			}
			// EOF or any other error — same end-of-stream signal
			// the scanner path uses.
			close(r.lines)
			return
		}
		select {
		case r.lines <- line:
		case <-r.done:
			return
		}
	}
}

// Next blocks until a line is available, ctx cancels, or the
// underlying reader EOFs. Returns the line + nil on success;
// "" + io.EOF on stream end; "" + ctx.Err() on cancel.
func (r *LineReader) Next(ctx context.Context) (string, error) {
	select {
	case line, ok := <-r.lines:
		if !ok {
			return "", io.EOF
		}
		return line, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Close stops the reader goroutine. Idempotent. Subsequent Next
// calls return the buffered lines first then io.EOF.
func (r *LineReader) Close() {
	r.closeOnce.Do(func() {
		close(r.done)
		if r.rl != nil {
			_ = r.rl.Close()
		}
	})
}

// slashCompleter implements readline.AutoCompleter for ghyll's
// slash commands. Only suggests when the line begins with "/"; for
// any other input (model prompts, modal verdicts) Tab is a no-op.
type slashCompleter struct {
	getCommands func() []string
}

// Do is the readline completion callback. line is the full current
// input; pos is the cursor position. Returns (suggestions, prefixLen)
// where suggestions are the rune slices to APPEND at the cursor
// (not full replacements) and prefixLen is how many runes BEFORE
// the cursor make up the prefix-being-completed (so readline knows
// what to replace).
func (c *slashCompleter) Do(line []rune, pos int) ([][]rune, int) {
	// Need at least one char, and it must be '/'.
	if pos == 0 || len(line) == 0 || line[0] != '/' {
		return nil, 0
	}
	// We only complete the first token (the command name itself).
	// If there's a space, the user is typing arguments — leave
	// those alone; arg completion is dialect/runner-specific and
	// out of scope.
	cmdEnd := pos
	for i := 0; i < pos; i++ {
		if line[i] == ' ' {
			return nil, 0
		}
		cmdEnd = i + 1
	}
	prefix := string(line[1:cmdEnd]) // skip leading '/'
	prefixLen := len(prefix)

	commands := c.getCommands()
	if len(commands) == 0 {
		return nil, prefixLen
	}
	var matches [][]rune
	for _, cmd := range commands {
		if strings.HasPrefix(cmd, prefix) {
			matches = append(matches, []rune(cmd[prefixLen:]))
		}
	}
	return matches, prefixLen
}
