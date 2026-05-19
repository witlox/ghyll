package runner

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// kill-server-fails-integration built-in evaluator. Per
// gates/concepts/kill-server-fails-integration.yaml: when a critical
// dependency is removed, the integration test suite MUST fail.
// Defends against test suites that mock the dependency and never
// exercise the real wire.
//
// Used by integrator G3 (no all-mock pass-through).
//
// v1 protocol:
//   - The operator declares `test-command` (shell command running
//     the test suite; exit 0 = pass) alongside critical-deps.
//   - For each dep d in critical-deps, the operator declares
//     `kill-cmd.d` (and optionally `unkill-cmd.d`) shell commands.
//   - The evaluator runs test-command live (expect pass), then for
//     each dep runs kill-cmd, optionally sleeps `kill-settle-ms.d`
//     for state to propagate (F35), re-runs test-command (expect
//     FAIL), and restores via unkill-cmd. If any dep's kill causes
//     the test to STILL pass, the suite is mocking and the clause
//     fails.
//   - After each unkill, re-runs the baseline. If baseline no longer
//     passes, the evaluation is Unevaluated for remaining deps
//     (F18) — measurements would be contaminated.
//
// The catalogue concept's schema does not (yet) declare
// test-command / kill-cmd as named args; v1 reads them from
// Clause.Args under the documented keys. A future catalogue
// amendment can formalize the shape — this is the operator-side
// binding pattern.

const (
	defaultKillTestTimeout       = 5 * time.Minute
	defaultKillCmdTimeout        = 30 * time.Second
	defaultKillSettle            = 250 * time.Millisecond
	killServerSubprocessMaxBytes = 4 * 1024 * 1024
)

// depNamePattern bounds operator dep names (F34). Map keys are safe
// (no shell interpolation), but a future operator templating the
// name into the command body has no protection without this gate.
var depNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

// EvaluateKillServerFailsIntegration is the built-in for the
// kill-server-fails-integration concept.
func EvaluateKillServerFailsIntegration(ctx context.Context, c Clause) (*Result, error) {
	testCommand, ok := c.Args["test-command"].(string)
	if !ok || strings.TrimSpace(testCommand) == "" {
		return &Result{
			Unevaluated: true,
			Reason:      "operator must supply `test-command` (shell command that runs the integration suite)",
			Details:     map[string]any{},
		}, nil
	}
	criticalDepsRaw, err := coerceStringList(c.Args["critical-deps"])
	if err != nil {
		return nil, fmt.Errorf("kill-server-fails-integration: critical-deps: %w", err)
	}
	// F33: filter empty/whitespace-only entries.
	criticalDeps := make([]string, 0, len(criticalDepsRaw))
	for _, d := range criticalDepsRaw {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		// F34: validate dep name shape.
		if !depNamePattern.MatchString(d) {
			return &Result{
				Unevaluated: true,
				Reason:      fmt.Sprintf("dep name %q must match %s", d, depNamePattern.String()),
				Details:     map[string]any{"invalid-dep-name": d},
			}, nil
		}
		criticalDeps = append(criticalDeps, d)
	}
	if len(criticalDeps) == 0 {
		return &Result{
			Unevaluated: true,
			Reason:      "critical-deps is empty after filtering; nothing to attempt-kill",
			Details:     map[string]any{},
		}, nil
	}

	// Per-dep kill / unkill commands and optional settle window.
	killCmds := map[string]string{}
	unkillCmds := map[string]string{}
	settle := map[string]time.Duration{}
	for _, dep := range criticalDeps {
		if v, ok := c.Args["kill-cmd."+dep].(string); ok {
			killCmds[dep] = v
		}
		if v, ok := c.Args["unkill-cmd."+dep].(string); ok {
			unkillCmds[dep] = v
		}
		if v, ok := c.Args["kill-settle-ms."+dep]; ok {
			ms, err := coerceInt64(v)
			if err != nil {
				return nil, fmt.Errorf("kill-server-fails-integration: kill-settle-ms.%s: %w", dep, err)
			}
			if ms < 0 {
				return nil, fmt.Errorf("kill-server-fails-integration: kill-settle-ms.%s must be >= 0", dep)
			}
			settle[dep] = time.Duration(ms) * time.Millisecond
		} else {
			settle[dep] = defaultKillSettle
		}
	}
	// Every dep needs a kill command.
	var missing []string
	for _, dep := range criticalDeps {
		if killCmds[dep] == "" {
			missing = append(missing, dep)
		}
	}
	if len(missing) > 0 {
		return &Result{
			Unevaluated: true,
			Reason: fmt.Sprintf("operator must supply `kill-cmd.<dep>` for each dep; missing: %s",
				strings.Join(missing, ", ")),
			Details: map[string]any{"missing-kill-cmds": missing},
		}, nil
	}

	// Step 1: baseline run. Tests must pass live.
	baseline, err := runShellWithCap(ctx, c.ProjectDir, testCommand, defaultKillTestTimeout)
	if err != nil {
		// F32: surface spawn errors (sh not on PATH, etc.) as
		// Unevaluated rather than treating them as kill-cmd success.
		return &Result{
			Unevaluated: true,
			Reason:      fmt.Sprintf("harness-spawn-failed: %v", err),
			Details:     map[string]any{},
		}, nil
	}
	if baseline.timedOut {
		return &Result{
			Unevaluated: true,
			Reason:      "baseline test run timed out; cannot measure kill-causes-fail",
			Details: map[string]any{
				"baseline-stderr": redactSecrets(truncateForForensics(baseline.stderr)),
			},
		}, nil
	}
	if !baseline.ok {
		return &Result{
			Unevaluated: true,
			Reason:      "baseline test run failed; cannot measure kill-causes-fail without a passing live baseline",
			Details: map[string]any{
				"baseline-exit":   baseline.exitCode,
				"baseline-stderr": redactSecrets(truncateForForensics(baseline.stderr)),
			},
		}, nil
	}
	// F20: warn if baseline produced no output (likely tautological
	// pass like `go test` on empty dir).
	var baselineWarning string
	if len(strings.TrimSpace(string(baseline.stdout))) == 0 {
		baselineWarning = "baseline test command produced empty stdout — gate may be tautological (no tests ran?)"
	}

	// Step 2: per-dep kill + re-run.
	type depResult struct {
		Dep          string `json:"dep"`
		KilledPassed bool   `json:"killed-passed"`
		KillExit     int    `json:"kill-exit"`
		TestExit     int    `json:"test-exit"`
		TestTimedOut bool   `json:"test-timed-out,omitempty"`
		TestSignal   int    `json:"test-signal,omitempty"`
		UnkillExit   int    `json:"unkill-exit"`
		UnkillOK     bool   `json:"unkill-ok"`
		KillStderr   string `json:"kill-stderr,omitempty"`
		TestStderr   string `json:"test-stderr,omitempty"`
		Skipped      bool   `json:"skipped,omitempty"`
		SkipReason   string `json:"skip-reason,omitempty"`
	}
	results := make([]depResult, 0, len(criticalDeps))
	failed := []string{}
	skippedDeps := []string{}

	for _, dep := range criticalDeps {
		r := depResult{Dep: dep, UnkillOK: true}
		killRun, killErr := runShellWithCap(ctx, c.ProjectDir, killCmds[dep], defaultKillCmdTimeout)
		if killErr != nil {
			// F32: spawn failure — skip this dep with Unevaluated
			// reason; do not credit "kill failed" as proper integration.
			r.Skipped = true
			r.SkipReason = fmt.Sprintf("harness-spawn-failed: %v", killErr)
			skippedDeps = append(skippedDeps, dep)
			results = append(results, r)
			continue
		}
		r.KillExit = killRun.exitCode
		r.KillStderr = redactSecrets(truncateForForensics(killRun.stderr))
		if !killRun.ok {
			r.KillStderr = "kill-cmd exited non-zero: " + r.KillStderr
		}

		// F35: settle window between kill and test rerun.
		if d := settle[dep]; d > 0 {
			select {
			case <-time.After(d):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		testAfter, testErr := runShellWithCap(ctx, c.ProjectDir, testCommand, defaultKillTestTimeout)
		if testErr != nil {
			r.Skipped = true
			r.SkipReason = fmt.Sprintf("harness-spawn-failed during test-after-kill: %v", testErr)
			skippedDeps = append(skippedDeps, dep)
			// best-effort unkill before continuing
			if cmd := unkillCmds[dep]; cmd != "" {
				_, _ = runShellWithCap(ctx, c.ProjectDir, cmd, defaultKillCmdTimeout)
			}
			results = append(results, r)
			continue
		}
		r.TestExit = testAfter.exitCode
		r.TestStderr = redactSecrets(truncateForForensics(testAfter.stderr))
		r.TestTimedOut = testAfter.timedOut
		r.TestSignal = testAfter.signal

		// F19: distinguish timeout / signal kill from clean fail.
		// Timeout/signal after kill is Unevaluated for this dep —
		// we can't claim "kill caused fail" if the test never
		// returned.
		if testAfter.timedOut {
			r.Skipped = true
			r.SkipReason = "test-after-kill timed out; cannot conclude kill-causes-fail"
			skippedDeps = append(skippedDeps, dep)
		} else if testAfter.signal != 0 {
			r.Skipped = true
			r.SkipReason = fmt.Sprintf("test-after-kill killed by signal %d; inconclusive", testAfter.signal)
			skippedDeps = append(skippedDeps, dep)
		} else {
			r.KilledPassed = testAfter.ok
			if r.KilledPassed {
				failed = append(failed, dep)
			}
		}

		// Always attempt unkill (if supplied) before recording.
		// F18: track unkill success; if it fails, re-establish
		// baseline before continuing to next dep.
		if cmd := unkillCmds[dep]; cmd != "" {
			unkillRun, unkillErr := runShellWithCap(ctx, c.ProjectDir, cmd, defaultKillCmdTimeout)
			if unkillErr != nil || !unkillRun.ok {
				r.UnkillOK = false
				if unkillRun.exitCode != 0 {
					r.UnkillExit = unkillRun.exitCode
				}
			}
		}
		results = append(results, r)

		// F18: re-check baseline after unkill. If it fails, abort
		// the rest of the loop with Unevaluated — subsequent deps'
		// measurements would be contaminated by the lingering kill.
		if !r.UnkillOK && len(criticalDeps) > 1 {
			rebase, rebaseErr := runShellWithCap(ctx, c.ProjectDir, testCommand, defaultKillTestTimeout)
			if rebaseErr != nil || !rebase.ok {
				// mark remaining deps skipped.
				for _, remaining := range remainingDeps(criticalDeps, dep) {
					skippedDeps = append(skippedDeps, remaining)
					results = append(results, depResult{
						Dep:        remaining,
						Skipped:    true,
						SkipReason: fmt.Sprintf("aborted: unkill-failed on prior dep %q contaminated baseline", dep),
					})
				}
				break
			}
		}
	}

	rendered := make([]map[string]any, len(results))
	for i, r := range results {
		entry := map[string]any{
			"dep":           r.Dep,
			"killed-passed": r.KilledPassed,
			"kill-exit":     r.KillExit,
			"test-exit":     r.TestExit,
			"unkill-exit":   r.UnkillExit,
			"unkill-ok":     r.UnkillOK,
		}
		if r.TestTimedOut {
			entry["test-timed-out"] = true
		}
		if r.TestSignal != 0 {
			entry["test-signal"] = r.TestSignal
		}
		if r.Skipped {
			entry["skipped"] = true
			entry["skip-reason"] = r.SkipReason
		}
		if r.KillStderr != "" {
			entry["kill-stderr"] = r.KillStderr
		}
		if r.TestStderr != "" {
			entry["test-stderr"] = r.TestStderr
		}
		rendered[i] = entry
	}

	// If any dep was skipped, the whole evaluation is Unevaluated
	// (the gate cannot conclude pass/fail with incomplete data).
	if len(skippedDeps) > 0 {
		details := map[string]any{
			"deps-tested":     len(criticalDeps),
			"deps-skipped":    skippedDeps,
			"per-dep-results": rendered,
		}
		if baselineWarning != "" {
			details["baseline-warning"] = baselineWarning
		}
		return &Result{
			Unevaluated: true,
			Reason: fmt.Sprintf("%d dep(s) skipped due to harness errors / inconclusive runs: %s",
				len(skippedDeps), strings.Join(skippedDeps, ", ")),
			Details: details,
		}, nil
	}

	pass := len(failed) == 0
	details := map[string]any{
		"deps-tested":     len(criticalDeps),
		"deps-mocked":     len(failed),
		"mocked-deps":     failed,
		"per-dep-results": rendered,
	}
	if baselineWarning != "" {
		details["baseline-warning"] = baselineWarning
	}
	if !pass {
		details["error"] = fmt.Sprintf("%d dep(s) appear to be mocked: %s",
			len(failed), strings.Join(failed, ", "))
	}
	return &Result{Pass: pass, Details: details}, nil
}

// remainingDeps returns the slice of deps after the named one (used
// to mark deps skipped when an unkill failure contaminates baseline).
func remainingDeps(all []string, after string) []string {
	var out []string
	past := false
	for _, d := range all {
		if past {
			out = append(out, d)
			continue
		}
		if d == after {
			past = true
		}
	}
	return out
}

// shellRunResult is the captured result of one shell-command run.
//
// timedOut distinguishes a timeout from a signal-kill from a
// clean non-zero exit (F19). signal carries the kill signal (if
// any).
type shellRunResult struct {
	ok       bool
	exitCode int
	stdout   []byte
	stderr   []byte
	timedOut bool
	signal   int
}

// runShellWithCap runs `command` via `sh -c` with the given timeout,
// the runner's standard env allowlist, output cap with kill-on-
// overflow (F17), and process-group kill on timeout. Returns the
// result + an operational error (only when the runner itself can't
// spawn; non-zero exits are NOT errors).
//
// F32: callers MUST not discard the error. A spawn failure means the
// kill / test command never ran; treating it as success crediting
// kill-cmd is unsound.
func runShellWithCap(ctx context.Context, workDir, command string, timeout time.Duration) (shellRunResult, error) {
	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.Command("sh", "-c", command)
	if workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Env = filteredParentEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var reaped atomic.Bool
	var killOnce sync.Once
	doKill := func() {
		killOnce.Do(func() {
			killProcessGroup(cmd, DefaultBindingGrace, &reaped)
		})
	}
	stdoutCap := newCaptureBuf(killServerSubprocessMaxBytes, doKill)
	stderrCap := newCaptureBuf(killServerSubprocessMaxBytes, doKill)
	cmd.Stdout = stdoutCap
	cmd.Stderr = stderrCap

	if err := cmd.Start(); err != nil {
		return shellRunResult{}, fmt.Errorf("spawn: %w", err)
	}
	waitCh := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		reaped.Store(true)
		waitCh <- err
	}()
	var waitErr error
	select {
	case waitErr = <-waitCh:
	case <-subCtx.Done():
		doKill()
		<-waitCh
		return shellRunResult{
			ok:       false,
			exitCode: -1,
			stdout:   stdoutCap.bytes(),
			stderr:   stderrCap.bytes(),
			timedOut: errors.Is(subCtx.Err(), context.DeadlineExceeded),
		}, nil
	}
	r := shellRunResult{
		ok:     waitErr == nil,
		stdout: stdoutCap.bytes(),
		stderr: stderrCap.bytes(),
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		ws, _ := exitErr.Sys().(syscall.WaitStatus)
		r.exitCode = ws.ExitStatus()
		if ws.Signaled() {
			r.signal = int(ws.Signal())
		}
	}
	return r, nil
}
