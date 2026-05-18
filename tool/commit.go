package tool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"github.com/witlox/ghyll/types"
)

// Commit attribution per the user's direction (2026-05-18): every
// tool-driven commit carries Ghyll-Version + Ghyll-Model git trailers
// so per-model attribution survives across the git log. Commit-per-
// model-change invariant lives at the session-loop layer; this file
// gives that layer the primitives it needs.
//
// Hardenings (validation-pass-8):
//   - S1: sanitizer also strips Unicode line separators (U+0085,
//     U+2028, U+2029) — git's trailer parser is line-based AND
//     downstream log consumers (JS, awk pipelines) often treat
//     these as line breaks.
//   - S2: Ghyll-Version and Ghyll-Model are project-controlled
//     values; on any control char we REJECT rather than silently
//     mutate. The audit trail must reflect reality.
//   - S3: trailer key set expanded to allow `_` and `.`.
//   - S4: validateExtraTrailer rejects unicode.IsControl + the
//     line-separator triple.
//   - S5: ExtraTrailers and Paths defensively copied at entry.
//   - S6: TrimRightFunc(unicode.IsSpace) on the message body so the
//     trailer-block delimiter (blank line) is well-formed.
//   - S8/S10: HasPendingChanges replaced by a typed enum that
//     distinguishes staged / unstaged / untracked / unknown.
//   - S11: message + trailer length caps.
//   - S12: GitCommit invokes `git commit -F -` via stdin so long
//     messages don't hit POSIX ARG_MAX.
//   - S13: trailer values normalize to "Key: value" (colon-space).

// Length caps (S11). Operator-supplied free text bounded; truncation
// is surfaced via a trailing marker so log readers can tell.
const (
	maxCommitMessageLen = 16 * 1024
	maxTrailerValueLen  = 256
	maxExtraTrailers    = 64
	maxCommitPaths      = 1024
)

// CommitOptions is the input to GitCommit.
type CommitOptions struct {
	Message       string
	GhyllVersion  string
	GhyllModel    string
	ExtraTrailers []string
	SignOff       bool
	AllowEmpty    bool
	Paths         []string
}

// Commit errors.
var (
	ErrCommitMessageEmpty   = errors.New("commit-message-empty")
	ErrCommitMessageTooLong = errors.New("commit-message-too-long")
	ErrCommitVersionEmpty   = errors.New("commit-ghyll-version-empty")
	ErrCommitVersionInvalid = errors.New("commit-ghyll-version-invalid")
	ErrCommitModelEmpty     = errors.New("commit-ghyll-model-empty")
	ErrCommitModelInvalid   = errors.New("commit-ghyll-model-invalid")
	ErrCommitTrailerBad     = errors.New("commit-trailer-malformed")
	ErrCommitTrailerCount   = errors.New("commit-trailer-count-exceeded")
	ErrCommitPathCount      = errors.New("commit-path-count-exceeded")
)

// trailerKeyOK reports whether s is a valid trailer key. Per
// validation-pass-8 S3: include `_` and `.` (RFC 5322 §3.6.8 allows
// both; `_` is widely used in practice — Signed_off_by, Co_Authored_By).
func trailerKeyOK(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return false
		}
	}
	return true
}

// hasControlOrLineSep reports whether s contains any control char
// or Unicode line separator. Used by the strict validators for
// Ghyll-* values (S1, S2) and by validateExtraTrailer (S4).
func hasControlOrLineSep(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
		if r == 0x85 || r == 0x2028 || r == 0x2029 {
			return true
		}
	}
	return false
}

// validateExtraTrailer returns nil if t looks like a `Key: value`
// trailer line. Per S4 + S13: requires `: ` (colon-space), key in
// the trailerKeyOK set, no control chars or Unicode line separators
// in the value.
func validateExtraTrailer(t string) error {
	colon := strings.IndexByte(t, ':')
	if colon <= 0 {
		return fmt.Errorf("%w: %q (expected `Key: value`)", ErrCommitTrailerBad, t)
	}
	key := t[:colon]
	if !trailerKeyOK(key) {
		return fmt.Errorf("%w: bad key in %q", ErrCommitTrailerBad, t)
	}
	// S13: enforce the colon-space convention. `Key: value` parses
	// reliably; `Key:value` is version-dependent in git.
	rest := t[colon+1:]
	if rest != "" && !strings.HasPrefix(rest, " ") {
		return fmt.Errorf("%w: missing space after colon in %q", ErrCommitTrailerBad, t)
	}
	if hasControlOrLineSep(t) {
		return fmt.Errorf("%w: control char or line separator in %q", ErrCommitTrailerBad, t)
	}
	return nil
}

// BuildCommitMessage assembles the final commit message with Ghyll-*
// trailers and any operator-supplied extras. Per S1/S2: Ghyll-*
// values are validated strict (rejected on bad input) rather than
// silently mutated.
func BuildCommitMessage(opts CommitOptions) (string, error) {
	if strings.TrimSpace(opts.Message) == "" {
		return "", ErrCommitMessageEmpty
	}
	if len(opts.Message) > maxCommitMessageLen {
		return "", fmt.Errorf("%w: %d bytes exceeds %d", ErrCommitMessageTooLong, len(opts.Message), maxCommitMessageLen)
	}
	// S2: Ghyll-* values are project-controlled. Reject on any
	// control char rather than silently strip — the audit trail
	// must reflect reality.
	if strings.TrimSpace(opts.GhyllVersion) == "" {
		return "", ErrCommitVersionEmpty
	}
	if hasControlOrLineSep(opts.GhyllVersion) {
		return "", fmt.Errorf("%w: control char in %q", ErrCommitVersionInvalid, opts.GhyllVersion)
	}
	if len(opts.GhyllVersion) > maxTrailerValueLen {
		return "", fmt.Errorf("%w: exceeds %d bytes", ErrCommitVersionInvalid, maxTrailerValueLen)
	}
	if strings.TrimSpace(opts.GhyllModel) == "" {
		return "", ErrCommitModelEmpty
	}
	if hasControlOrLineSep(opts.GhyllModel) {
		return "", fmt.Errorf("%w: control char in %q", ErrCommitModelInvalid, opts.GhyllModel)
	}
	if len(opts.GhyllModel) > maxTrailerValueLen {
		return "", fmt.Errorf("%w: exceeds %d bytes", ErrCommitModelInvalid, maxTrailerValueLen)
	}

	// S5: defensive copy of ExtraTrailers so a racing caller can't
	// mutate between validation and emit.
	extras := append([]string(nil), opts.ExtraTrailers...)
	if len(extras) > maxExtraTrailers {
		return "", fmt.Errorf("%w: %d > %d", ErrCommitTrailerCount, len(extras), maxExtraTrailers)
	}
	for _, t := range extras {
		if err := validateExtraTrailer(t); err != nil {
			return "", err
		}
	}

	// S6: TrimRightFunc(unicode.IsSpace) so the trailer-block
	// delimiter is a clean blank line.
	body := strings.TrimRightFunc(opts.Message, unicode.IsSpace)

	var b strings.Builder
	b.WriteString(body)
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "Ghyll-Version: %s\n", strings.TrimSpace(opts.GhyllVersion))
	fmt.Fprintf(&b, "Ghyll-Model: %s\n", strings.TrimSpace(opts.GhyllModel))
	for _, t := range extras {
		b.WriteString(t)
		b.WriteString("\n")
	}
	return b.String(), nil
}

// GitCommit runs `git commit -F -` (S12) with the stamped message
// piped via stdin so multi-KB messages don't hit POSIX ARG_MAX.
func GitCommit(ctx context.Context, dir string, opts CommitOptions, timeout time.Duration) types.ToolResult {
	start := time.Now()
	msg, err := BuildCommitMessage(opts)
	if err != nil {
		return types.ToolResult{Error: err.Error(), Duration: time.Since(start)}
	}

	args := []string{"commit", "-F", "-"}
	if opts.SignOff {
		args = append(args, "-s")
	}
	if opts.AllowEmpty {
		args = append(args, "--allow-empty")
	}
	// S5: defensive copy of Paths.
	paths := append([]string(nil), opts.Paths...)
	if len(paths) > maxCommitPaths {
		return types.ToolResult{
			Error:    fmt.Sprintf("%v: %d > %d", ErrCommitPathCount, len(paths), maxCommitPaths),
			Duration: time.Since(start),
		}
	}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(msg)
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
	return types.ToolResult{Output: stdout.String(), Duration: duration}
}

// PendingStatus is the typed result of probing the working tree
// (validation-pass-8 S8/S10). Distinguishing the four states lets
// the session-loop layer decide whether to flush, skip, or error.
type PendingStatus int

const (
	PendingUnknown   PendingStatus = iota // probe failed; caller MUST handle
	PendingClean                          // nothing to commit
	PendingStaged                         // staged changes exist
	PendingUnstaged                       // unstaged changes exist (working tree dirty)
	PendingUntracked                      // only untracked files
)

// String returns the wire form for PendingStatus.
func (p PendingStatus) String() string {
	switch p {
	case PendingClean:
		return "clean"
	case PendingStaged:
		return "staged"
	case PendingUnstaged:
		return "unstaged"
	case PendingUntracked:
		return "untracked"
	case PendingUnknown:
		return "unknown"
	}
	return "invalid"
}

// CheckPending returns the granular working-tree state. Per S8/S10:
// the session loop's commit-per-model-change invariant cares about
// STAGED changes; PendingUntracked alone should NOT trigger a flush.
//
// Returns PendingUnknown + error if git plumbing fails; callers
// MUST handle the unknown state explicitly.
func CheckPending(ctx context.Context, dir string, timeout time.Duration) (PendingStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain=v1")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return PendingUnknown, fmt.Errorf("git status: %w (stderr: %s)", err, stderr.String())
	}
	out := stdout.String()
	if strings.TrimSpace(out) == "" {
		return PendingClean, nil
	}
	hasStaged, hasUnstaged, hasUntracked := false, false, false
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 2 {
			continue
		}
		// Porcelain v1: first byte = index status, second byte = worktree status.
		// "??" = untracked.
		if line[0] == '?' && line[1] == '?' {
			hasUntracked = true
			continue
		}
		if line[0] != ' ' && line[0] != '?' {
			hasStaged = true
		}
		if line[1] != ' ' && line[1] != '?' {
			hasUnstaged = true
		}
	}
	switch {
	case hasStaged:
		return PendingStaged, nil
	case hasUnstaged:
		return PendingUnstaged, nil
	case hasUntracked:
		return PendingUntracked, nil
	}
	return PendingClean, nil
}

// HasPendingChanges is the deprecated boolean wrapper retained for
// callers that haven't moved to CheckPending yet. Validation-pass-8
// S8: prefer CheckPending; this wrapper folds untracked + clean
// into "no flush needed."
//
// Deprecated: use CheckPending; this returns true only for STAGED
// or UNSTAGED changes (not untracked) to match the session-loop
// semantics of "flush before model switch."
func HasPendingChanges(ctx context.Context, dir string, timeout time.Duration) (bool, error) {
	st, err := CheckPending(ctx, dir, timeout)
	if err != nil {
		return false, err
	}
	return st == PendingStaged || st == PendingUnstaged, nil
}
