package tool

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/witlox/ghyll/types"
)

// ReadFile reads a file and returns its contents.
// Invariant 16: timeout enforced via context.
func ReadFile(ctx context.Context, path string, timeout time.Duration) types.ToolResult {
	start := time.Now()

	done := make(chan types.ToolResult, 1)
	go func() {
		data, err := os.ReadFile(path)
		if err != nil {
			done <- types.ToolResult{
				Error:    err.Error(),
				Duration: time.Since(start),
			}
			return
		}
		done <- types.ToolResult{
			Output:   string(data),
			Duration: time.Since(start),
		}
	}()

	select {
	case result := <-done:
		return result
	case <-time.After(timeout):
		return types.ToolResult{
			Error:    fmt.Sprintf("file read timed out after %s", timeout),
			TimedOut: true,
			Duration: time.Since(start),
		}
	case <-ctx.Done():
		return types.ToolResult{
			Error:    fmt.Sprintf("file read cancelled: %v", ctx.Err()),
			TimedOut: true,
			Duration: time.Since(start),
		}
	}
}

// WriteFile writes content to a file.
// Invariant 16: timeout enforced via context.
func WriteFile(ctx context.Context, path string, content string, timeout time.Duration) types.ToolResult {
	start := time.Now()

	done := make(chan types.ToolResult, 1)
	go func() {
		err := os.WriteFile(path, []byte(content), 0644)
		if err != nil {
			done <- types.ToolResult{
				Error:    err.Error(),
				Duration: time.Since(start),
			}
			return
		}
		done <- types.ToolResult{
			Output:   fmt.Sprintf("wrote %d bytes to %s", len(content), path),
			Duration: time.Since(start),
		}
	}()

	select {
	case result := <-done:
		return result
	case <-time.After(timeout):
		return types.ToolResult{
			Error:    fmt.Sprintf("file write timed out after %s", timeout),
			TimedOut: true,
			Duration: time.Since(start),
		}
	case <-ctx.Done():
		return types.ToolResult{
			Error:    fmt.Sprintf("file write cancelled: %v", ctx.Err()),
			TimedOut: true,
			Duration: time.Since(start),
		}
	}
}
