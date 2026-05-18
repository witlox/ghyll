package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
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
//     each dep runs kill-cmd, re-runs test-command (expect FAIL),
//     and restores via unkill-cmd. If any dep's kill causes the
//     test to STILL pass, the suite is mocking and the clause
//     fails.
//
// The catalogue concept's schema does not (yet) declare
// test-command / kill-cmd as named args; v1 reads them from
// Clause.Args under the documented keys. A future catalogue
// amendment can formalize the shape — this is the operator-side
// binding pattern.

const (
	defaultKillTestTimeout = 5 * time.Minute
	defaultKillCmdTimeout  = 30 * time.Second
)

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
	criticalDeps, err := coerceStringList(c.Args["critical-deps"])
	if err != nil {
		return nil, fmt.Errorf("kill-server-fails-integration: critical-deps: %w", err)
	}
	if len(criticalDeps) == 0 {
		return &Result{
			Unevaluated: true,
			Reason:      "critical-deps is empty; nothing to attempt-kill",
			Details:     map[string]any{},
		}, nil
	}

	// Per-dep kill / unkill commands from Args.
	killCmds := map[string]string{}
	unkillCmds := map[string]string{}
	for _, dep := range criticalDeps {
		if v, ok := c.Args["kill-cmd."+dep].(string); ok {
			killCmds[dep] = v
		}
		if v, ok := c.Args["unkill-cmd."+dep].(string); ok {
			unkillCmds[dep] = v
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
		return nil, fmt.Errorf("kill-server-fails-integration: baseline error: %w", err)
	}
	if !baseline.ok {
		// Tests fail with all deps live — setup is broken. The
		// evaluator can't measure "kill causes fail" if "live
		// passes" isn't established.
		return &Result{
			Unevaluated: true,
			Reason:      "baseline test run failed; cannot measure kill-causes-fail without a passing live baseline",
			Details: map[string]any{
				"baseline-exit":   baseline.exitCode,
				"baseline-stderr": redactSecrets(truncateForForensics(baseline.stderr)),
			},
		}, nil
	}

	// Step 2: per-dep kill + re-run.
	type depResult struct {
		Dep          string `json:"dep"`
		KilledPassed bool   `json:"killed-passed"`
		KillExit     int    `json:"kill-exit"`
		TestExit     int    `json:"test-exit"`
		UnkillExit   int    `json:"unkill-exit"`
		KillStderr   string `json:"kill-stderr,omitempty"`
		TestStderr   string `json:"test-stderr,omitempty"`
	}
	results := make([]depResult, 0, len(criticalDeps))
	failed := []string{}
	for _, dep := range criticalDeps {
		r := depResult{Dep: dep}
		killRun, _ := runShellWithCap(ctx, c.ProjectDir, killCmds[dep], defaultKillCmdTimeout)
		r.KillExit = killRun.exitCode
		r.KillStderr = redactSecrets(truncateForForensics(killRun.stderr))
		if !killRun.ok {
			// kill-cmd itself failed — surface but continue (the
			// operator may have a non-zero-exit kill command;
			// what matters is the next step).
			r.KillStderr = "kill-cmd exited non-zero: " + r.KillStderr
		}
		testAfter, _ := runShellWithCap(ctx, c.ProjectDir, testCommand, defaultKillTestTimeout)
		r.TestExit = testAfter.exitCode
		r.TestStderr = redactSecrets(truncateForForensics(testAfter.stderr))
		r.KilledPassed = testAfter.ok
		// Always run unkill (if supplied) before recording result —
		// best-effort restore.
		if cmd := unkillCmds[dep]; cmd != "" {
			unkillRun, _ := runShellWithCap(ctx, c.ProjectDir, cmd, defaultKillCmdTimeout)
			r.UnkillExit = unkillRun.exitCode
		}
		if r.KilledPassed {
			// Tests STILL passed after killing the dep — the
			// suite is mocking the dep. This is the failure case
			// the concept exists to detect.
			failed = append(failed, dep)
		}
		results = append(results, r)
	}

	// Marshalable details.
	rendered := make([]map[string]any, len(results))
	for i, r := range results {
		entry := map[string]any{
			"dep":           r.Dep,
			"killed-passed": r.KilledPassed,
			"kill-exit":     r.KillExit,
			"test-exit":     r.TestExit,
			"unkill-exit":   r.UnkillExit,
		}
		if r.KillStderr != "" {
			entry["kill-stderr"] = r.KillStderr
		}
		if r.TestStderr != "" {
			entry["test-stderr"] = r.TestStderr
		}
		rendered[i] = entry
	}

	pass := len(failed) == 0
	details := map[string]any{
		"deps-tested":     len(criticalDeps),
		"deps-mocked":     len(failed),
		"mocked-deps":     failed,
		"per-dep-results": rendered,
	}
	if !pass {
		details["error"] = fmt.Sprintf("%d dep(s) appear to be mocked: %s",
			len(failed), strings.Join(failed, ", "))
	}
	return &Result{Pass: pass, Details: details}, nil
}

// shellRunResult is the captured result of one shell-command run.
type shellRunResult struct {
	ok       bool
	exitCode int
	stdout   []byte
	stderr   []byte
}

// runShellWithCap runs `command` via `sh -c` with the given timeout,
// the runner's standard env allowlist, and process-group kill on
// timeout. Returns the result + an operational error (only when
// the runner itself can't spawn; non-zero exits are NOT errors).
func runShellWithCap(ctx context.Context, workDir, command string, timeout time.Duration) (shellRunResult, error) {
	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.Command("sh", "-c", command)
	if workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Env = filteredParentEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return shellRunResult{}, fmt.Errorf("spawn: %w", err)
	}
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-waitCh:
	case <-subCtx.Done():
		killProcessGroup(cmd, DefaultBindingGrace)
		<-waitCh
		return shellRunResult{
			ok:       false,
			exitCode: -1,
			stdout:   stdout.Bytes(),
			stderr:   stderr.Bytes(),
		}, nil
	}
	r := shellRunResult{
		ok:     waitErr == nil,
		stdout: stdout.Bytes(),
		stderr: stderr.Bytes(),
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		ws, _ := exitErr.Sys().(syscall.WaitStatus)
		r.exitCode = ws.ExitStatus()
	}
	return r, nil
}
