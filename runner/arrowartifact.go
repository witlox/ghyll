package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// arrow-artifact-present built-in evaluator. Per
// gates/concepts/arrow-artifact-present.yaml: checks that the
// arrow's declared output artifact exists at its declared location
// and is non-empty. Optional schema-check command validates the
// artifact's structure.
//
// Integrator G6 uses this (the integration report exists at its
// declared location). Analyst G6 (coverage-claim) and architect
// arrows use it too.
//
// IMPORTANT — `schema-check` is FULL SHELL (validation-pass-4 F12).
// The string is interpolated into `sh -c` verbatim. The artifact
// path is appended single-quoted, but the operator's command body
// itself can contain `;`, `&&`, `>`, etc. Operators must treat
// schema-check as a trust boundary; an LLM-suggested schema-check
// command is as dangerous as any other shell-execution path.

const (
	// defaultArtifactMinSize is the default minimum file size.
	defaultArtifactMinSize int64 = 1

	// schemaCheckDefaultTimeout caps the optional schema-check
	// subprocess. Short — it's a validator, not a build step.
	schemaCheckDefaultTimeout = 30 * time.Second

	// schemaCheckMaxOutputBytes caps stdout/stderr of the schema-
	// check subprocess. F11: bare bytes.Buffer was an unbounded
	// RAM-DoS path; now we use captureBuf with kill-on-overflow.
	schemaCheckMaxOutputBytes int64 = 4 * 1024 * 1024
)

// EvaluateArrowArtifactPresent is the built-in for arrow-artifact-present.
func EvaluateArrowArtifactPresent(ctx context.Context, c Clause) (*Result, error) {
	artifactPath, err := requireStringArg(c.Args, "artifact-path")
	if err != nil {
		return nil, fmt.Errorf("arrow-artifact-present: %w", err)
	}
	minSize := defaultArtifactMinSize
	if v, ok := c.Args["min-size-bytes"]; ok {
		n, err := coerceInt64(v)
		if err != nil {
			return nil, fmt.Errorf("arrow-artifact-present: min-size-bytes: %w", err)
		}
		if n < 1 {
			return nil, fmt.Errorf("arrow-artifact-present: min-size-bytes must be >= 1, got %d", n)
		}
		minSize = n
	}
	var schemaCheck string
	if v, ok := c.Args["schema-check"]; ok {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("arrow-artifact-present: schema-check must be string, got %T", v)
		}
		schemaCheck = s
	}

	// F3 + F4: resolve operator path through the containment helper.
	// Refuses `..`, absolute paths, and intermediate symlinks in
	// parent components.
	resolved, err := ResolveProjectPath(c.ProjectDir, artifactPath)
	if err != nil {
		return &Result{
			Pass: false,
			Details: map[string]any{
				"exists":        false,
				"artifact-path": artifactPath,
				"error":         err.Error(),
			},
		}, nil
	}

	info, err := os.Lstat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return &Result{
				Pass: false,
				Details: map[string]any{
					"exists":        false,
					"artifact-path": artifactPath,
					"error":         "artifact does not exist",
				},
			}, nil
		}
		return nil, fmt.Errorf("arrow-artifact-present: lstat %q: %w", resolved, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return &Result{
			Pass: false,
			Details: map[string]any{
				"exists":        false,
				"artifact-path": artifactPath,
				"error":         "artifact is a symlink (refused)",
			},
		}, nil
	}
	if !info.Mode().IsRegular() {
		return &Result{
			Pass: false,
			Details: map[string]any{
				"exists":        false,
				"artifact-path": artifactPath,
				"error":         "artifact is not a regular file",
			},
		}, nil
	}
	if info.Size() < minSize {
		return &Result{
			Pass: false,
			Details: map[string]any{
				"exists":        true,
				"artifact-path": artifactPath,
				"size":          info.Size(),
				"min-size":      minSize,
				"error":         fmt.Sprintf("artifact too small: %d bytes (need >= %d)", info.Size(), minSize),
			},
		}, nil
	}

	if schemaCheck != "" {
		out, err := runSchemaCheck(ctx, c.ProjectDir, schemaCheck, resolved)
		if err != nil {
			return &Result{
				Pass: false,
				Details: map[string]any{
					"exists":             true,
					"artifact-path":      artifactPath,
					"size":               info.Size(),
					"schema-check-error": err.Error(),
					"schema-check-out":   truncateForForensics(out),
					"error":              "schema-check failed",
				},
			}, nil
		}
	}

	return &Result{
		Pass: true,
		Details: map[string]any{
			"exists":        true,
			"artifact-path": artifactPath,
			"size":          info.Size(),
		},
	}, nil
}

// runSchemaCheck runs the optional schema-check command. The command
// receives the artifact path as its single argument (appended,
// single-quoted) so the operator can write generic validators like
// `jq -e .`.
//
// The subprocess inherits the same env allowlist as BindingEvaluator
// (no parent secrets) and uses captureBuf with kill-on-overflow
// (F11), mirroring the binding evaluator's defense.
//
// SECURITY NOTE: command is full shell (F12). See file-header
// comment.
func runSchemaCheck(ctx context.Context, projectDir, command, artifactPath string) ([]byte, error) {
	subCtx, cancel := context.WithTimeout(ctx, schemaCheckDefaultTimeout)
	defer cancel()
	cmd := exec.Command("sh", "-c", command+" "+shellQuote(artifactPath))
	if projectDir != "" {
		cmd.Dir = projectDir
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
	stdoutCap := newCaptureBuf(schemaCheckMaxOutputBytes, doKill)
	stderrCap := newCaptureBuf(schemaCheckMaxOutputBytes, doKill)
	cmd.Stdout = stdoutCap
	cmd.Stderr = stderrCap

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn: %w", err)
	}
	waitCh := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		reaped.Store(true)
		waitCh <- err
	}()
	select {
	case err := <-waitCh:
		out := append(stdoutCap.bytes(), stderrCap.bytes()...)
		if stdoutCap.overflowed() || stderrCap.overflowed() {
			return out, fmt.Errorf("schema-check output exceeded %d bytes (killed)", schemaCheckMaxOutputBytes)
		}
		if err != nil {
			return out, err
		}
		return out, nil
	case <-subCtx.Done():
		doKill()
		<-waitCh
		out := append(stdoutCap.bytes(), stderrCap.bytes()...)
		return out, fmt.Errorf("schema-check timed out after %s", schemaCheckDefaultTimeout)
	}
}

// shellQuote escapes a string for inclusion in a sh-c command.
// Single-quotes the input and escapes embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// filteredParentEnv returns the parent process env filtered through
// the default allowlist (PATH, HOME, LANG, LC_*, etc.). Mirrors
// BindingEvaluator.buildEnv's default behavior so schema-check
// commands run with the same minimal env.
//
// Drift hazard (F31): BindingEvaluator.buildEnv supports per-binding
// InheritEnv; this helper does not. Schema-check has no opt-in for
// tool-specific extras like VIRTUAL_ENV. Documented limitation;
// remediating fully requires routing schema-check through
// BindingEvaluator which is a larger refactor.
func filteredParentEnv() []string {
	parent := os.Environ()
	out := make([]string, 0, 16)
	for _, kv := range parent {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		key := kv[:eq]
		keep := false
		for _, allow := range defaultEnvAllowlist {
			if key == allow {
				keep = true
				break
			}
		}
		if !keep {
			for _, prefix := range defaultEnvAllowlistPrefixes {
				if strings.HasPrefix(key, prefix) {
					keep = true
					break
				}
			}
		}
		if keep {
			out = append(out, kv)
		}
	}
	return out
}

// coerceInt64 returns the argument as int64, accepting int, int64,
// float64 (yaml numbers may decode as float). Rejects NaN/Inf with
// a clearer message than "non-integer float" (F42).
func coerceInt64(v any) (int64, error) {
	switch x := v.(type) {
	case int:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case int64:
		return x, nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0, fmt.Errorf("expected finite integer, got non-finite float %v", x)
		}
		if x != float64(int64(x)) {
			return 0, fmt.Errorf("expected integer, got non-integer float %v", x)
		}
		return int64(x), nil
	case float32:
		return coerceInt64(float64(x))
	}
	return 0, fmt.Errorf("not an integer: %T", v)
}

// io.Writer compile-time check.
var _ io.Writer = (*bytes.Buffer)(nil)
