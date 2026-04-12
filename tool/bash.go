package tool

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/witlox/ghyll/types"
)

// Bash executes a shell command and returns captured output.
// Invariant 16: timeout enforced via context.
func Bash(ctx context.Context, command string, timeout time.Duration) types.ToolResult {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	duration := time.Since(start)

	if ctx.Err() == context.DeadlineExceeded {
		return types.ToolResult{
			Error:    fmt.Sprintf("command timed out after %s", timeout),
			TimedOut: true,
			Duration: duration,
		}
	}

	if err != nil {
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
