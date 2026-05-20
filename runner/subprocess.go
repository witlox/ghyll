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
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
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
//   - Env allowlist (validation-pass-3 F1). The subprocess receives
//     a minimal env (PATH, HOME, LANG, LC_*, TMPDIR, USER, SHELL,
//     TERM) by default. Operators opt secrets in explicitly via
//     WithInheritEnv or WithEnv. Default behavior does NOT leak
//     ANTHROPIC_API_KEY, GHYLL_*, SSH_AUTH_SOCK, etc.
//   - Timeout: SIGTERM → grace → SIGKILL via process group. The
//     process group kill reaps children that stay in the group
//     (setsid escape requires sandbox containment — see F41 below).
//   - Output size cap: when captureBuf overflows, the subprocess is
//     killed immediately (F2 — previously the cap only checked
//     after Wait returned).
//   - Forensic redaction: stderr/stdout blobs attached to fail
//     records pass through a redaction filter that masks common
//     secret patterns (Bearer tokens, sk-/api-/key prefixes,
//     anything matching ^[A-Z_]*_(KEY|TOKEN|SECRET)=). F33.
//
// Sandbox-external: the binding runs as exec.Cmd. SRT / bubblewrap
// at the kernel level handles security (FS isolation, network
// restrictions, session containment). The runner enforces process-
// level boundaries (env, timeout, output cap, kill); the sandbox
// enforces system-level boundaries.

// Defaults for the binding evaluator.
const (
	// DefaultBindingTimeout is the wall-clock cap for the whole
	// subprocess (and its process group). Beyond this, SIGTERM →
	// grace → SIGKILL.
	DefaultBindingTimeout = 5 * time.Minute

	// DefaultBindingMaxOutputBytes caps stdout. A binding that
	// floods stdout past this is killed.
	DefaultBindingMaxOutputBytes = 16 * 1024 * 1024 // 16 MiB

	// DefaultBindingGrace is the SIGTERM-to-SIGKILL window.
	DefaultBindingGrace = 5 * time.Second

	// minBindingGrace is the floor for the grace period. A grace of
	// 0 (or negative) skips the SIGTERM step entirely (F34).
	minBindingGrace = 100 * time.Millisecond

	// maxRawOutputForForensics caps the raw-output blob attached
	// to run records on malformed-output failures.
	maxRawOutputForForensics = 16 * 1024

	// maxStderrCapture is the upper bound on stderr the runner
	// retains as metadata. Larger than the forensic cap because
	// stderr is typically a log stream.
	maxStderrCapture = 32 * 1024

	// maxDetailsJSONDepth bounds the recursive depth of the
	// Details payload (F35). Limits ballooning by hostile bindings
	// that emit deeply nested structures inside the stdout cap.
	maxDetailsJSONDepth = 8
)

// Result-detail reason codes surfaced by the subprocess evaluator.
const (
	ReasonTimeout         = "evaluator-timeout"
	ReasonCancelled       = "evaluator-cancelled" // F40
	ReasonMalformedOutput = "evaluator-output-malformed"
	ReasonOversizedOutput = "evaluator-output-oversized"
	ReasonKilledBySignal  = "evaluator-killed-by-signal" // F42 (was OOMKilled)
	ReasonSpawnFailed     = "evaluator-spawn-failed"
)

// defaultEnvAllowlist is the minimal env the subprocess inherits.
// Bindings that need more (e.g., a build-tool needs CARGO_HOME)
// declare it via WithInheritEnv at registration time. Validation-
// pass-3 F1.
var defaultEnvAllowlist = []string{
	"PATH", "HOME", "LANG", "TMPDIR", "USER", "SHELL", "TERM",
	"LOGNAME", "PWD",
}

// defaultEnvAllowlistPrefixes match anything starting with these
// (LC_* covers all locale variables).
var defaultEnvAllowlistPrefixes = []string{"LC_"}

// secretRedactRE matches common secret-like strings. Used by the
// forensic-redaction filter (F33).
var secretRedactRE = regexp.MustCompile(
	`(?i)(bearer\s+[A-Za-z0-9._-]+|sk-[A-Za-z0-9_-]{16,}|` +
		`[A-Z][A-Z0-9_]*_(KEY|TOKEN|SECRET|PASSWORD|PASSWD)=[^\s]+|` +
		`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----)`)

// BindingEvaluator is the evaluator implementation for language-
// bound concepts.
type BindingEvaluator struct {
	// Command is the shell command the binding expanded to.
	Command string

	// Timeout is the wall-clock cap. Zero means default.
	Timeout time.Duration

	// MaxOutputBytes caps stdout. Zero means default.
	MaxOutputBytes int64

	// Grace is the SIGTERM-to-SIGKILL window. Zero means default.
	// Clamped to a minBindingGrace floor at kill time so a
	// direct-struct construction with Grace: 0 doesn't skip
	// SIGTERM (F34).
	Grace time.Duration

	// Env is additional env to overlay on the allowlist. Each
	// entry is "KEY=VALUE".
	Env []string

	// InheritEnv is a list of additional parent env-var names to
	// pass through to the subprocess (F1). The default allowlist
	// (PATH/HOME/locale/etc.) is always included; this is opt-in
	// for tool-specific extras (CARGO_HOME, GOPATH, etc.).
	InheritEnv []string

	// WorkingDir is the subprocess working directory.
	WorkingDir string
}

// BindingOption mutates a BindingEvaluator at construction time.
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

// WithEnv adds environment-variable overlays (each "KEY=VALUE").
func WithEnv(env ...string) BindingOption {
	return func(b *BindingEvaluator) { b.Env = append(b.Env, env...) }
}

// WithInheritEnv adds parent env-var names to inherit (F1).
// Default allowlist (PATH/HOME/locale/etc.) is always included;
// this opts in tool-specific extras.
func WithInheritEnv(keys ...string) BindingOption {
	return func(b *BindingEvaluator) { b.InheritEnv = append(b.InheritEnv, keys...) }
}

// WithWorkingDir sets the subprocess working directory.
func WithWorkingDir(dir string) BindingOption {
	return func(b *BindingEvaluator) { b.WorkingDir = dir }
}

// NewBindingEvaluator constructs a BindingEvaluator and returns
// an Evaluator function. command is run via `sh -c`.
func NewBindingEvaluator(command string, opts ...BindingOption) Evaluator {
	b := &BindingEvaluator{Command: command}
	for _, opt := range opts {
		opt(b)
	}
	return b.Evaluate
}

// ensureDefaults fills in default values for unset fields. Called
// at the top of Evaluate so direct-struct construction also gets
// the defaults applied (previously NewBindingEvaluator did this,
// but a `&BindingEvaluator{Command: ...}` direct construction
// bypassed the defaults — F34).
func (b *BindingEvaluator) ensureDefaults() {
	if b.Timeout == 0 {
		b.Timeout = DefaultBindingTimeout
	}
	if b.MaxOutputBytes == 0 {
		b.MaxOutputBytes = DefaultBindingMaxOutputBytes
	}
	if b.Grace < minBindingGrace {
		b.Grace = DefaultBindingGrace
	}
}

// buildEnv constructs the env passed to the subprocess: the
// default allowlist (PATH, HOME, locale, etc.), the binding's
// explicit InheritEnv extras, then the binding's explicit Env
// overlays last (so they win). Validation-pass-3 F1.
func (b *BindingEvaluator) buildEnv() []string {
	parent := os.Environ()
	parentMap := make(map[string]string, len(parent))
	for _, kv := range parent {
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			parentMap[kv[:eq]] = kv[eq+1:]
		}
	}
	want := func(key string) bool {
		for _, allow := range defaultEnvAllowlist {
			if key == allow {
				return true
			}
		}
		for _, prefix := range defaultEnvAllowlistPrefixes {
			if strings.HasPrefix(key, prefix) {
				return true
			}
		}
		for _, opt := range b.InheritEnv {
			if key == opt {
				return true
			}
		}
		return false
	}
	out := make([]string, 0, len(defaultEnvAllowlist)+len(b.InheritEnv)+len(b.Env))
	for k, v := range parentMap {
		if want(k) {
			out = append(out, k+"="+v)
		}
	}
	out = append(out, b.Env...)
	return out
}

// Evaluate is the Evaluator function. Spawns the binding, feeds
// the clause args as JSON on stdin, reads {pass, details} JSON
// from stdout. Honors ctx for caller-initiated cancellation in
// addition to its own Timeout.
func (b *BindingEvaluator) Evaluate(ctx context.Context, c Clause) (*Result, error) {
	if strings.TrimSpace(b.Command) == "" {
		// F38: whitespace-only command is operator misconfiguration,
		// not a malformed-output failure.
		return failResult(ReasonSpawnFailed, "binding command is empty (after whitespace trim)", nil), nil
	}
	b.ensureDefaults()

	// We use exec.Command (not CommandContext) and route ctx through
	// our own kill path so exec's built-in ctx-kill doesn't race with
	// killProcessGroup (F15). Ctx cancellation cancels via subCtx
	// below.
	subCtx, cancel := context.WithTimeout(ctx, b.Timeout)
	defer cancel()

	cmd := exec.Command("sh", "-c", b.Command)
	cmd.Env = b.buildEnv()
	if b.WorkingDir != "" {
		// No project-root containment: ghyll is sandbox-only;
		// path containment is the sandbox's policy. A binding
		// that sets WorkingDir intentionally hits exactly the
		// dir specified.
		cmd.Dir = b.WorkingDir
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// stdin: JSON clause input. Owned pump so a binding that
	// partial-reads doesn't deadlock Wait (F16).
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
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("BindingEvaluator: stdin pipe: %w", err)
	}

	// Kill closure passed to captureBuf so cap-overflow triggers
	// immediate process-group termination (F2). One-shot via
	// sync.Once.
	//
	// CI race fix: `reaped` is set by the wait goroutine AFTER
	// cmd.Wait returns. killProcessGroup consults this flag instead
	// of cmd.ProcessState so the kill path never races the Wait
	// path's writes to cmd internals.
	var reaped atomic.Bool
	var killOnce sync.Once
	doKill := func() {
		killOnce.Do(func() {
			killProcessGroup(cmd, b.Grace, &reaped)
		})
	}

	stdoutCap := newCaptureBuf(b.MaxOutputBytes, doKill)
	stderrCap := newCaptureBuf(maxStderrCapture, nil) // stderr overflow doesn't trigger kill
	cmd.Stdout = stdoutCap
	cmd.Stderr = stderrCap

	if err := cmd.Start(); err != nil {
		return failResult(ReasonSpawnFailed, fmt.Sprintf("spawn: %v", err), nil), nil
	}

	// stdin pump: write payload, close write-end, ignore EPIPE
	// (binding may have exited or partial-read).
	go func() {
		_, _ = stdinPipe.Write(stdinPayload)
		_ = stdinPipe.Close()
	}()

	// Wait in a goroutine so we can race against ctx + cap-kill.
	// CI race fix: set `reaped` before sending so killProcessGroup
	// observes Wait's completion without reading cmd.ProcessState.
	waitErrCh := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		reaped.Store(true)
		waitErrCh <- err
	}()

	var waitErr error
	select {
	case waitErr = <-waitErrCh:
		// Subprocess finished on its own (possibly killed by
		// captureBuf overflow).
	case <-subCtx.Done():
		// Caller cancelled or our timeout fired.
		doKill()
		<-waitErrCh
		switch {
		case errors.Is(ctx.Err(), context.Canceled):
			return failResult(ReasonCancelled,
				"caller cancelled the evaluation", &subprocessMetadata{
					Stderr: stderrCap.bytes(),
					Stdout: stdoutCap.bytes(),
				}), nil
		case errors.Is(subCtx.Err(), context.DeadlineExceeded):
			return failResult(ReasonTimeout,
				fmt.Sprintf("evaluator did not return within %s; sent SIGTERM then SIGKILL", b.Timeout),
				&subprocessMetadata{
					Stderr:        stderrCap.bytes(),
					Stdout:        stdoutCap.bytes(),
					TimedOutAfter: b.Timeout,
				}), nil
		default:
			return failResult(ReasonCancelled,
				fmt.Sprintf("subCtx: %v", subCtx.Err()),
				&subprocessMetadata{
					Stderr: stderrCap.bytes(),
					Stdout: stdoutCap.bytes(),
				}), nil
		}
	}

	// Stdout cap tripped? captureBuf has already killed if it had
	// a doKill closure; the wait above caught the kill. Report
	// oversized.
	if stdoutCap.overflowed() {
		return failResult(ReasonOversizedOutput,
			fmt.Sprintf("stdout exceeded %d bytes", b.MaxOutputBytes),
			&subprocessMetadata{
				Stderr:   stderrCap.bytes(),
				Stdout:   stdoutCap.bytes(),
				Oversize: true,
			}), nil
	}

	// Exit error: non-zero exit incl. signal kill.
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			ws, _ := exitErr.Sys().(syscall.WaitStatus)
			if ws.Signaled() {
				// Any signal-kill (operator kill, OOM, sandbox,
				// our own grace-expired SIGKILL) lands here. We
				// no longer claim "likely OOM" — we report the
				// signal and let the operator triage (F42).
				return failResult(ReasonKilledBySignal,
					fmt.Sprintf("subprocess killed by signal %d (%s); no graceful exit",
						ws.Signal(), ws.Signal()),
					&subprocessMetadata{
						Stderr: stderrCap.bytes(),
						Stdout: stdoutCap.bytes(),
						Signal: int(ws.Signal()),
					}), nil
			}
			// Other non-zero exits flow through to parse — a
			// binding may legitimately exit non-zero AND produce
			// pass=false JSON.
		}
	}

	// Parse stdout as {pass, details}.
	out := stdoutCap.bytes()
	if len(strings.TrimSpace(string(out))) == 0 {
		return failResult(ReasonMalformedOutput,
			"stdout was empty; expected {pass, details} JSON",
			&subprocessMetadata{
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
			&subprocessMetadata{
				Stderr: stderrCap.bytes(),
				Stdout: out,
			}), nil
	}
	// F35: cap details depth.
	if depthExceeds(parsed.Details, maxDetailsJSONDepth) {
		return failResult(ReasonMalformedOutput,
			fmt.Sprintf("details payload exceeds max depth %d", maxDetailsJSONDepth),
			&subprocessMetadata{
				Stderr: stderrCap.bytes(),
				Stdout: out,
			}), nil
	}
	if parsed.Details == nil {
		parsed.Details = map[string]any{}
	}
	// Attach stderr as metadata (non-failure signal); redact secrets.
	if len(stderrCap.bytes()) > 0 {
		parsed.Details["stderr"] = redactSecrets(truncateForForensics(stderrCap.bytes()))
	}
	return &Result{Pass: parsed.Pass, Details: parsed.Details}, nil
}

// subprocessMetadata accompanies a fail Result on subprocess errors.
type subprocessMetadata struct {
	Stderr        []byte
	Stdout        []byte
	TimedOutAfter time.Duration
	Oversize      bool
	Signal        int
}

// failResult builds a Result encoding a subprocess failure as
// pass=false with details.error + reason. Forensic blobs are
// redacted (F33).
func failResult(reason, detail string, meta *subprocessMetadata) *Result {
	details := map[string]any{
		"error":  reason,
		"detail": detail,
	}
	if meta != nil {
		if len(meta.Stderr) > 0 {
			details["stderr"] = redactSecrets(truncateForForensics(meta.Stderr))
		}
		if len(meta.Stdout) > 0 {
			details["stdout"] = redactSecrets(truncateForForensics(meta.Stdout))
		}
		if meta.TimedOutAfter > 0 {
			details["timed-out-after"] = meta.TimedOutAfter.String()
		}
		if meta.Oversize {
			details["stdout-oversize"] = true
		}
		if meta.Signal > 0 {
			details["signal"] = meta.Signal
		}
	}
	return &Result{Pass: false, Details: details}
}

// truncateForForensics caps a byte blob at maxRawOutputForForensics.
func truncateForForensics(b []byte) string {
	if int64(len(b)) <= maxRawOutputForForensics {
		return string(b)
	}
	return string(b[:maxRawOutputForForensics]) + "\n... (truncated)"
}

// redactSecrets replaces common secret patterns with "[REDACTED]".
// Validation-pass-3 F33 — forensic blobs were durably persisted
// into EvaluationRun and then synced to the orphan branch; this
// filter is best-effort, not exhaustive.
func redactSecrets(s string) string {
	return secretRedactRE.ReplaceAllString(s, "[REDACTED]")
}

// depthExceeds reports whether v's JSON nesting depth exceeds max.
// Used to bound the details payload at parse time (F35).
func depthExceeds(v any, max int) bool {
	if max < 0 {
		return true
	}
	switch x := v.(type) {
	case map[string]any:
		for _, item := range x {
			if depthExceeds(item, max-1) {
				return true
			}
		}
	case []any:
		for _, item := range x {
			if depthExceeds(item, max-1) {
				return true
			}
		}
	}
	return false
}

// killProcessGroup sends SIGTERM to the subprocess's process group,
// waits grace, then SIGKILL if the process is still alive. On
// Linux, negating PID targets the whole group.
//
// Refuses to signal if `reaped` is set (Wait has returned, process
// is reaped — prevents PID-recycle race per validation-pass-3 F14).
//
// CI race fix: `reaped` (atomic.Bool, set by the Wait goroutine
// after cmd.Wait returns) replaces the racy `cmd.ProcessState !=
// nil` read for the F14 PID-recycle guard. For the in-grace poll
// we use `syscall.Kill(pid, 0)` — a documented liveness probe that
// returns ESRCH when the process no longer exists. This avoids
// reading cmd.ProcessState concurrently with Wait (a data race
// under `go test -race`) while still detecting process death
// promptly.
func killProcessGroup(cmd *exec.Cmd, grace time.Duration, reaped *atomic.Bool) {
	if cmd.Process == nil {
		return
	}
	// F14: if Wait already returned, do nothing (PID may have
	// been recycled to another process).
	if reaped != nil && reaped.Load() {
		return
	}
	// F34: clamp grace at floor.
	if grace < minBindingGrace {
		grace = minBindingGrace
	}
	pid := cmd.Process.Pid
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		// Fall back to direct PID signalling.
		_ = cmd.Process.Signal(syscall.SIGTERM)
		if waitForExitOrGrace(pid, reaped, grace) {
			return
		}
		_ = cmd.Process.Kill()
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	if waitForExitOrGrace(pid, reaped, grace) {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

// waitForExitOrGrace polls the target process's liveness via
// syscall.Kill(pid, 0) until either the kernel reports ESRCH (the
// process exited and was reaped — by the parent's cmd.Wait
// goroutine), the `reaped` flag flips, or the grace window
// expires. Returns true when the process is gone.
func waitForExitOrGrace(pid int, reaped *atomic.Bool, grace time.Duration) bool {
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if reaped != nil && reaped.Load() {
			return true
		}
		// kill(pid, 0) is the POSIX liveness probe — succeeds on
		// existing process (returns nil), ESRCH on missing pid.
		// This avoids touching cmd.ProcessState (which Wait
		// writes under a different lock).
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return reaped != nil && reaped.Load()
}

// captureBuf is an io.Writer with a byte cap. When the cap trips,
// kill (if non-nil) is invoked once via sync.Once so the subprocess
// is terminated immediately rather than streaming to a discarded
// sink for the full Timeout. Validation-pass-3 F2.
type captureBuf struct {
	mu      sync.Mutex
	max     int64
	buf     bytes.Buffer
	written int64
	over    bool
	kill    func()
	killOne sync.Once
}

// newCaptureBuf constructs a captureBuf. kill may be nil if the
// caller doesn't want overflow-triggered termination (e.g., stderr,
// where overflow truncates but the subprocess continues).
func newCaptureBuf(max int64, kill func()) *captureBuf {
	return &captureBuf{max: max, kill: kill}
}

func (c *captureBuf) Write(p []byte) (int, error) {
	c.mu.Lock()
	remaining := c.max - c.written
	if remaining <= 0 {
		if !c.over {
			c.over = true
			c.mu.Unlock()
			c.fireKill()
			return len(p), nil
		}
		c.mu.Unlock()
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		_, _ = c.buf.Write(p[:remaining])
		c.written += remaining
		c.over = true
		c.mu.Unlock()
		c.fireKill()
		return len(p), nil
	}
	n, err := c.buf.Write(p)
	c.written += int64(n)
	c.mu.Unlock()
	return n, err
}

func (c *captureBuf) bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]byte, c.buf.Len())
	copy(out, c.buf.Bytes())
	return out
}

func (c *captureBuf) overflowed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.over
}

func (c *captureBuf) fireKill() {
	if c.kill == nil {
		return
	}
	c.killOne.Do(c.kill)
}

// Compile-time check that captureBuf is an io.Writer.
var _ io.Writer = (*captureBuf)(nil)
