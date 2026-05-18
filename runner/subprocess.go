package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Subprocess evaluator. The harness invokes operator-declared
// language bindings (lint-clean.go = "staticcheck && go vet";
// mutation-score.rust = "cargo-mutants") by spawning the command in
// its own process group, passing the clause args as JSON on stdin,
// and parsing {pass, details} JSON from stdout.
//
// Defenses:
//   - timeout via context.WithTimeout; on expiry the runner sends
//     SIGTERM to the process group, waits GraceSeconds, then SIGKILL.
//     Process-group kill reaps the binding's child processes too —
//     no zombies.
//   - output size cap (MaxOutputBytes). Stdout exceeding the cap is
//     a fail with reason "evaluator-output-oversized"; the process
//     is killed once the cap trips.
//   - malformed JSON is fail with reason "evaluator-output-malformed";
//     raw output (truncated to 16KB) is preserved on the run record
//     for forensic inspection.
//   - stderr is captured as metadata; it does NOT influence pass/fail.
//
// Sandbox-external: the binding runs as exec.Cmd. SRT / bubblewrap
// at the kernel level handles security. The runner does not enforce
// permissions, network restrictions, or filesystem isolation — that
// is the sandbox's job.

// Defaults for the binding evaluator. These are conservative; the
// operator can tune per-binding via BindingOption.
const (
	// DefaultBindingTimeout is the wall-clock cap for the whole
	// subprocess (and its process group). Beyond this, SIGTERM →
	// 5s → SIGKILL.
	DefaultBindingTimeout = 5 * time.Minute

	// DefaultBindingMaxOutputBytes caps stdout. A binding that
	// floods stdout past this is killed.
	DefaultBindingMaxOutputBytes = 16 * 1024 * 1024 // 16 MiB

	// DefaultBindingGrace is the SIGTERM-to-SIGKILL window.
	DefaultBindingGrace = 5 * time.Second

	// maxRawOutputForForensics caps the raw-output blob attached to
	// run records on malformed-output failures. 16 KiB is enough
	// for a JSON syntax-error context window without bloating the
	// pass log.
	maxRawOutputForForensics = 16 * 1024
)

// Result-detail reason codes surfaced by the subprocess evaluator.
// Kept as constants so callers can errors.Is-style compare on the
// reason text (no separate error types — the failure flows through
// Result.Details["error"]).
const (
	ReasonTimeout         = "evaluator-timeout"
	ReasonMalformedOutput = "evaluator-output-malformed"
	ReasonOversizedOutput = "evaluator-output-oversized"
	ReasonOOMKilled       = "evaluator-killed: oom"
	ReasonSpawnFailed     = "evaluator-spawn-failed"
)

// BindingEvaluator is the evaluator implementation for language-
// bound concepts. Construct via NewBindingEvaluator; the returned
// Evaluator is suitable for Registry.Register or Registry.Replace.
type BindingEvaluator struct {
	// Command is the shell command the binding expanded to (e.g.,
	// "go-mutesting" or "staticcheck && go vet"). Run via
	// `sh -c <command>` so the operator can use shell features.
	Command string

	// Timeout is the wall-clock cap. Zero means use
	// DefaultBindingTimeout.
	Timeout time.Duration

	// MaxOutputBytes caps stdout. Zero means use
	// DefaultBindingMaxOutputBytes.
	MaxOutputBytes int64

	// Grace is the SIGTERM-to-SIGKILL window. Zero means use
	// DefaultBindingGrace.
	Grace time.Duration

	// Env overlays extra environment on the subprocess inheriting
	// from the parent. Empty means inherit-only.
	Env []string

	// WorkingDir is the subprocess's working directory. Empty means
	// inherit the parent's.
	WorkingDir string
}

// BindingOption mutates a BindingEvaluator at construction time.
// Used for per-binding tuning without re-stating the whole struct.
type BindingOption func(*BindingEvaluator)

// WithTimeout sets the binding's wall-clock cap.
func WithTimeout(d time.Duration) BindingOption {
	return func(b *BindingEvaluator) { b.Timeout = d }
}

// WithMaxOutputBytes sets the binding's stdout cap.
func WithMaxOutputBytes(n int64) BindingOption {
	return func(b *BindingEvaluator) { b.MaxOutputBytes = n }
}

// WithGrace sets the SIGTERM-to-SIGKILL grace period.
func WithGrace(d time.Duration) BindingOption {
	return func(b *BindingEvaluator) { b.Grace = d }
}

// WithEnv adds environment-variable overlays.
func WithEnv(env ...string) BindingOption {
	return func(b *BindingEvaluator) { b.Env = append(b.Env, env...) }
}

// WithWorkingDir sets the subprocess working directory.
func WithWorkingDir(dir string) BindingOption {
	return func(b *BindingEvaluator) { b.WorkingDir = dir }
}

// NewBindingEvaluator constructs a BindingEvaluator and returns an
// Evaluator function. The Evaluator can be registered against the
// Registry under any concept name. command is run via `sh -c`.
func NewBindingEvaluator(command string, opts ...BindingOption) Evaluator {
	b := &BindingEvaluator{Command: command}
	for _, opt := range opts {
		opt(b)
	}
	if b.Timeout == 0 {
		b.Timeout = DefaultBindingTimeout
	}
	if b.MaxOutputBytes == 0 {
		b.MaxOutputBytes = DefaultBindingMaxOutputBytes
	}
	if b.Grace == 0 {
		b.Grace = DefaultBindingGrace
	}
	return b.Evaluate
}

// Evaluate is the Evaluator function. Spawns the binding, feeds
// the clause args as JSON on stdin, reads {pass, details} JSON
// from stdout. Honors ctx for caller-initiated cancellation in
// addition to its own Timeout.
func (b *BindingEvaluator) Evaluate(ctx context.Context, c Clause) (*Result, error) {
	if b.Command == "" {
		return nil, errors.New("BindingEvaluator: empty Command")
	}

	// Combine caller ctx with our timeout.
	deadline := time.Now().Add(b.Timeout)
	subCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	// Build the subprocess.
	cmd := exec.CommandContext(subCtx, "sh", "-c", b.Command)
	cmd.Env = append(os.Environ(), b.Env...)
	if b.WorkingDir != "" {
		cmd.Dir = b.WorkingDir
	}
	// Put the subprocess in its own process group so SIGTERM/SIGKILL
	// can reach the binding's children (no zombies). On Linux,
	// SysProcAttr.Setpgid + Pgid=0 starts a new group with PGID =
	// the subprocess's PID. Killing -PGID then signals the whole
	// group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// stdin: JSON-encoded clause input.
	stdinPayload, err := json.Marshal(map[string]any{
		"clause-id":   c.ClauseID,
		"pass-id":     c.PassID,
		"concept":     c.Concept,
		"args":        c.Args,
		"project-dir": c.ProjectDir,
	})
	if err != nil {
		return nil, fmt.Errorf("BindingEvaluator: marshal clause input: %w", err)
	}
	cmd.Stdin = bytes.NewReader(stdinPayload)

	// stdout: capped reader.
	var stdoutCap captureBuf
	stdoutCap.max = b.MaxOutputBytes
	cmd.Stdout = &stdoutCap

	// stderr: capped reader (separate cap, smaller — stderr is
	// metadata, not the result channel).
	var stderrCap captureBuf
	stderrCap.max = maxRawOutputForForensics * 2
	cmd.Stderr = &stderrCap

	startErr := cmd.Start()
	if startErr != nil {
		return failResult(ReasonSpawnFailed, fmt.Sprintf("spawn: %v", startErr), nil, nil), nil
	}

	// Wait in a goroutine so we can race the subprocess against the
	// output-cap trip.
	waitErrCh := make(chan error, 1)
	go func() {
		waitErrCh <- cmd.Wait()
	}()

	var waitErr error
	select {
	case waitErr = <-waitErrCh:
		// Subprocess finished on its own.
	case <-subCtx.Done():
		// Deadline expired or caller cancelled. Send SIGTERM to the
		// process group; wait `Grace`; SIGKILL if still running.
		killProcessGroup(cmd, b.Grace)
		// Drain the wait goroutine.
		<-waitErrCh
		if errors.Is(subCtx.Err(), context.DeadlineExceeded) {
			return failResult(ReasonTimeout,
				fmt.Sprintf("evaluator did not return within %s; sent SIGTERM then SIGKILL", b.Timeout),
				nil, &subprocessMetadata{
					Stderr:        stderrCap.bytes(),
					Stdout:        stdoutCap.bytes(),
					TimedOutAfter: b.Timeout,
				}), nil
		}
		return failResult(ReasonTimeout,
			fmt.Sprintf("evaluator cancelled: %v", subCtx.Err()),
			nil, &subprocessMetadata{
				Stderr: stderrCap.bytes(),
				Stdout: stdoutCap.bytes(),
			}), nil
	}

	// Stdout cap tripped? errOversized was buffered by captureBuf.
	if stdoutCap.overflow {
		// Try to terminate if still running (should not be — Wait
		// returned — but defense-in-depth).
		killProcessGroup(cmd, b.Grace)
		return failResult(ReasonOversizedOutput,
			fmt.Sprintf("stdout exceeded %d bytes", b.MaxOutputBytes),
			nil, &subprocessMetadata{
				Stderr:   stderrCap.bytes(),
				Stdout:   stdoutCap.bytes(),
				Oversize: true,
			}), nil
	}

	// Exit error: any non-zero exit, including OOM-kill.
	if waitErr != nil {
		// Distinguish OOM (signal 9) from other failures.
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			ws, _ := exitErr.Sys().(syscall.WaitStatus)
			if ws.Signaled() && ws.Signal() == syscall.SIGKILL {
				return failResult(ReasonOOMKilled,
					"subprocess killed by signal 9 (likely OOM); no graceful exit",
					nil, &subprocessMetadata{
						Stderr: stderrCap.bytes(),
						Stdout: stdoutCap.bytes(),
					}), nil
			}
			// Other non-zero exits flow through to parse — a binding
			// may legitimately exit non-zero AND produce a valid
			// pass=false JSON. We don't presume.
		}
	}

	// Parse stdout as {pass, details}.
	out := stdoutCap.bytes()
	if len(strings.TrimSpace(string(out))) == 0 {
		return failResult(ReasonMalformedOutput,
			"stdout was empty; expected {pass, details} JSON",
			nil, &subprocessMetadata{
				Stderr: stderrCap.bytes(),
				Stdout: out,
			}), nil
	}
	var parsed struct {
		Pass    bool           `json:"pass"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return failResult(ReasonMalformedOutput,
			fmt.Sprintf("stdout is not valid JSON: %v", err),
			nil, &subprocessMetadata{
				Stderr: stderrCap.bytes(),
				Stdout: out,
			}), nil
	}
	if parsed.Details == nil {
		parsed.Details = map[string]any{}
	}
	// Attach stderr as metadata (non-failure signal).
	if len(stderrCap.bytes()) > 0 {
		parsed.Details["stderr"] = truncateForForensics(stderrCap.bytes())
	}
	return &Result{Pass: parsed.Pass, Details: parsed.Details}, nil
}

// subprocessMetadata accompanies a fail Result on subprocess errors;
// captures stderr / stdout / size flag for forensic inspection.
type subprocessMetadata struct {
	Stderr        []byte
	Stdout        []byte
	TimedOutAfter time.Duration
	Oversize      bool
}

// failResult builds a Result encoding a subprocess failure as
// pass=false with details.error + reason. Callers receive a
// non-nil Result so EvaluationRun.Result is recorded (the runner
// distinguishes "broken binding" from "real fail" via RunError,
// which the caller sets via the returned error).
func failResult(reason, detail string, _ error, meta *subprocessMetadata) *Result {
	details := map[string]any{
		"error":  reason,
		"detail": detail,
	}
	if meta != nil {
		if len(meta.Stderr) > 0 {
			details["stderr"] = truncateForForensics(meta.Stderr)
		}
		if len(meta.Stdout) > 0 {
			details["stdout"] = truncateForForensics(meta.Stdout)
		}
		if meta.TimedOutAfter > 0 {
			details["timed-out-after"] = meta.TimedOutAfter.String()
		}
		if meta.Oversize {
			details["stdout-oversize"] = true
		}
	}
	return &Result{Pass: false, Details: details}
}

// truncateForForensics caps a byte blob at maxRawOutputForForensics
// and returns the head as a string (sufficient for JSON-error
// context without bloating the pass log).
func truncateForForensics(b []byte) string {
	if int64(len(b)) <= maxRawOutputForForensics {
		return string(b)
	}
	return string(b[:maxRawOutputForForensics]) + "\n... (truncated)"
}

// killProcessGroup sends SIGTERM to the subprocess's process group,
// waits grace, then SIGKILL if the process is still alive. On Linux,
// negating PID targets the whole group, so children spawned by the
// binding are signalled too — no zombies.
func killProcessGroup(cmd *exec.Cmd, grace time.Duration) {
	if cmd.Process == nil {
		return
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		// Fall back to direct PID signalling.
		_ = cmd.Process.Signal(syscall.SIGTERM)
		time.Sleep(grace)
		_ = cmd.Process.Kill()
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	// Wait up to `grace` for the group to exit; if it doesn't,
	// SIGKILL. We can't synchronously wait on a process group from
	// outside cmd.Wait, so poll with a short interval.
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		// Send signal 0 to check if the group still exists.
		if err := syscall.Kill(-pgid, 0); err != nil {
			return // group is gone
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

// captureBuf is an io.Writer that buffers up to `max` bytes; further
// writes are dropped and the overflow flag is set. Used to cap
// subprocess stdout/stderr at a known maximum.
type captureBuf struct {
	max      int64
	buf      bytes.Buffer
	written  int64
	overflow bool
}

func (c *captureBuf) Write(p []byte) (int, error) {
	if c.overflow {
		// Pretend to accept (we already overflowed; further writes
		// don't change the state). Returning the byte count without
		// io.ErrShortWrite avoids confusing the os/exec plumbing.
		return len(p), nil
	}
	remaining := c.max - c.written
	if remaining <= 0 {
		c.overflow = true
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		_, _ = c.buf.Write(p[:remaining])
		c.written += remaining
		c.overflow = true
		return len(p), nil
	}
	n, err := c.buf.Write(p)
	c.written += int64(n)
	return n, err
}

func (c *captureBuf) bytes() []byte {
	return c.buf.Bytes()
}

// Compile-time check that captureBuf is an io.Writer.
var _ io.Writer = (*captureBuf)(nil)
