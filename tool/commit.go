package tool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/witlox/ghyll/types"
)

// Commit attribution per the user's direction (2026-05-18): every
// tool-driven commit carries Ghyll-Version + Ghyll-Model git trailers
// so per-model attribution survives across the git log. Commit-per-
// model-change invariant lives at the session-loop layer; this file
// gives that layer the primitives it needs.
//
// The trailers are machine-parseable via:
//
//   git log --format='%(trailers:key=Ghyll-Model)'
//
// Co-Authored-By: trailers (for hybrid human+agent commits) remain
// available; callers can pass extra trailers via CommitOptions.

// CommitOptions is the input to GitCommit.
type CommitOptions struct {
	// Message is the primary commit message. Required.
	Message string

	// GhyllVersion is the running ghyll version (cmd/ghyll's
	// `version` var). Required for the stamp.
	GhyllVersion string

	// GhyllModel is the model identifier in
	// `family-variant[-quant]@endpoint` form. Required.
	GhyllModel string

	// ExtraTrailers are additional `Key: value` trailer lines
	// appended after the Ghyll-* trailers (e.g., Co-Authored-By).
	ExtraTrailers []string

	// SignOff invokes `git commit -s` so the user's git identity is
	// added as a Signed-off-by trailer. Default false.
	SignOff bool

	// AllowEmpty passes `--allow-empty` (rare; the session loop
	// avoids empty marker commits per the F design).
	AllowEmpty bool

	// Paths, if non-nil, scopes the commit to these paths (the
	// caller is responsible for staging them first; the runner does
	// not run `git add` on the caller's behalf).
	Paths []string
}

// Commit errors.
var (
	ErrCommitMessageEmpty = errors.New("commit-message-empty")
	ErrCommitVersionEmpty = errors.New("commit-ghyll-version-empty")
	ErrCommitModelEmpty   = errors.New("commit-ghyll-model-empty")
	ErrCommitTrailerBad   = errors.New("commit-trailer-malformed")
)

// trailerPattern: any non-empty `Key: value` line. Strict-ish — the
// key is letters/digits/dashes only (git trailer convention).
var trailerKeyOK = func(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return false
		}
	}
	return true
}

// validateExtraTrailer returns nil if t looks like a `Key: value`
// trailer line. Empty value is allowed (some trailers are flags).
func validateExtraTrailer(t string) error {
	colon := strings.IndexByte(t, ':')
	if colon <= 0 {
		return fmt.Errorf("%w: %q (expected `Key: value`)", ErrCommitTrailerBad, t)
	}
	if !trailerKeyOK(t[:colon]) {
		return fmt.Errorf("%w: bad key in %q", ErrCommitTrailerBad, t)
	}
	if strings.ContainsAny(t, "\n\r") {
		return fmt.Errorf("%w: embedded newline in %q", ErrCommitTrailerBad, t)
	}
	return nil
}

// BuildCommitMessage assembles the final commit message with Ghyll-*
// trailers and any operator-supplied extras. Exposed so callers can
// inspect (or sign) the exact bytes git will see.
func BuildCommitMessage(opts CommitOptions) (string, error) {
	if strings.TrimSpace(opts.Message) == "" {
		return "", ErrCommitMessageEmpty
	}
	if strings.TrimSpace(opts.GhyllVersion) == "" {
		return "", ErrCommitVersionEmpty
	}
	if strings.TrimSpace(opts.GhyllModel) == "" {
		return "", ErrCommitModelEmpty
	}
	for _, t := range opts.ExtraTrailers {
		if err := validateExtraTrailer(t); err != nil {
			return "", err
		}
	}

	// Normalize: ensure the message body ends with exactly one blank
	// line before the trailer block. git's interpret-trailers is the
	// authoritative parser; we feed it a clean message.
	body := strings.TrimRight(opts.Message, "\n")

	var b strings.Builder
	b.WriteString(body)
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "Ghyll-Version: %s\n", sanitizeTrailerValue(opts.GhyllVersion))
	fmt.Fprintf(&b, "Ghyll-Model: %s\n", sanitizeTrailerValue(opts.GhyllModel))
	for _, t := range opts.ExtraTrailers {
		b.WriteString(t)
		b.WriteString("\n")
	}
	return b.String(), nil
}

// sanitizeTrailerValue strips embedded newlines / control chars so a
// hostile model identifier can't forge a multi-line trailer block.
func sanitizeTrailerValue(s string) string {
	s = strings.TrimSpace(s)
	// Strip control chars (incl. CR/LF/tab).
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// GitCommit runs `git commit` with the stamped message. The caller
// must have staged the changes (e.g., via tool.Git with `git add`).
// Returns the standard ToolResult; the commit hash is available in
// stdout via `git rev-parse HEAD` if the caller needs it.
func GitCommit(ctx context.Context, dir string, opts CommitOptions, timeout time.Duration) types.ToolResult {
	start := time.Now()
	msg, err := BuildCommitMessage(opts)
	if err != nil {
		return types.ToolResult{
			Error:    err.Error(),
			Duration: time.Since(start),
		}
	}

	args := []string{"commit", "-m", msg}
	if opts.SignOff {
		args = append(args, "-s")
	}
	if opts.AllowEmpty {
		args = append(args, "--allow-empty")
	}
	if len(opts.Paths) > 0 {
		args = append(args, "--")
		args = append(args, opts.Paths...)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	duration := time.Since(start)

	if ctx.Err() == context.DeadlineExceeded {
		return types.ToolResult{
			Error:    fmt.Sprintf("git commit timed out after %s", timeout),
			TimedOut: true,
			Duration: duration,
		}
	}
	if runErr != nil {
		return types.ToolResult{
			Output:   stdout.String(),
			Error:    stderr.String(),
			Duration: duration,
		}
	}
	return types.ToolResult{
		Output:   stdout.String(),
		Duration: duration,
	}
}

// HasPendingChanges returns true if the working tree has staged or
// unstaged changes. Used by the session loop's commit-per-model-
// change check: before applying a router escalate/de-escalate, if
// there are pending changes the loop flushes them with the OLD
// model's stamp.
//
// Returns (false, err) on git plumbing errors so the caller can
// distinguish "no pending" from "couldn't tell."
func HasPendingChanges(ctx context.Context, dir string, timeout time.Duration) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("git status: %w (stderr: %s)", err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()) != "", nil
}
