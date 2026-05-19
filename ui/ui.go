// Package ui centralizes user-facing terminal output for the ghyll CLI.
//
// Library code (runner/, engine/, memory/, tool/, dialect/) emits
// structured diagnostics via log/slog. Anything the operator reads in
// the terminal during a session — version banner, status reports,
// usage text, REPL prompts, error messages — goes through this
// package so that there is one place to evolve color, structured
// output, and TTY handling.
package ui

import (
	"fmt"
	"io"
	"os"
)

var (
	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr
)

// SetOutput overrides the package-level stdout and stderr writers.
// Tests use it to capture output; production code does not call it.
//
// Caveat for downstream callers that resolve the writer once at
// construction time (notably stream.NewRenderer, which caches the
// writer it is given): SetOutput called AFTER such a constructor
// has run will not redirect that consumer's writes. To capture
// renderer output in a test, call SetOutput before constructing
// the Session, or accept that renderer output bypasses the capture.
func SetOutput(out, err io.Writer) {
	stdout = out
	stderr = err
}

// Stdout returns the current user-facing stdout writer. Callers that
// build their own formatters (tabwriter, table renderers) write to
// this so test capture and color stripping continue to work.
func Stdout() io.Writer { return stdout }

// Stderr returns the current user-facing stderr writer.
func Stderr() io.Writer { return stderr }

// Info prints a line to stdout. A trailing newline is added.
func Info(format string, args ...any) {
	_, _ = fmt.Fprintf(stdout, format+"\n", args...)
}

// Status prints a status line with the given symbol prefix
// (typically "ℹ", "⚠", or "✗"). A space separates symbol and message;
// a trailing newline is added.
func Status(symbol, format string, args ...any) {
	_, _ = fmt.Fprintf(stdout, symbol+" "+format+"\n", args...)
}

// Print writes a line to stdout without any trailing newline. Used
// for interactive prompts where the cursor must stay on the line.
func Print(s string) {
	_, _ = fmt.Fprint(stdout, s)
}

// Errorf prints an error to stderr prefixed with "ghyll: ". A
// trailing newline is added.
func Errorf(format string, args ...any) {
	_, _ = fmt.Fprintf(stderr, "ghyll: "+format+"\n", args...)
}

// Usage prints lines to stderr unprefixed. Used for top-level usage
// banners where the "ghyll:" prefix would be redundant.
func Usage(lines ...string) {
	for _, l := range lines {
		_, _ = fmt.Fprintln(stderr, l)
	}
}
