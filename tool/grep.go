package tool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/witlox/ghyll/types"
)

// Grep searches for a pattern in a path. Prefers ripgrep (rg) if available,
// falls back to standard grep.
// Invariant 16: timeout enforced via context.
func Grep(ctx context.Context, pattern string, path string, timeout time.Duration) types.ToolResult {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if rgPath, err := exec.LookPath("rg"); err == nil {
		cmd = exec.CommandContext(ctx, rgPath, pattern, path)
	} else {
		cmd = exec.CommandContext(ctx, "grep", "-rn", pattern, path)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	duration := time.Since(start)

	if ctx.Err() == context.DeadlineExceeded {
		return types.ToolResult{
			Error:    fmt.Sprintf("grep timed out after %s", timeout),
			TimedOut: true,
			Duration: duration,
		}
	}

	// grep/rg exit code 1 = no matches (not an error)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return types.ToolResult{
				Output:   "",
				Duration: duration,
			}
		}
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

// GrepWithPath is like Grep but uses a specific PATH for executable lookup.
// Used for testing the fallback from ripgrep to standard grep.
func GrepWithPath(ctx context.Context, pattern string, path string, timeout time.Duration, searchPath string) types.ToolResult {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Always use standard grep when a restricted PATH is provided
	cmd := exec.CommandContext(ctx, "grep", "-rn", pattern, path)
	cmd.Env = append(cmd.Environ(), "PATH="+searchPath)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	duration := time.Since(start)

	if ctx.Err() == context.DeadlineExceeded {
		return types.ToolResult{
			Error:    fmt.Sprintf("grep timed out after %s", timeout),
			TimedOut: true,
			Duration: duration,
		}
	}

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return types.ToolResult{Output: "", Duration: duration}
		}
		return types.ToolResult{Output: stdout.String(), Error: stderr.String(), Duration: duration}
	}

	return types.ToolResult{Output: stdout.String(), Duration: duration}
}
