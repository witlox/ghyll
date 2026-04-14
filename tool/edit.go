package tool

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/witlox/ghyll/types"
)

// EditFile applies a surgical string replacement to a file.
// Invariant 33: CAS via SHA256 content hash — read, match, write to temp, verify hash, rename.
// Invariant 34: old_string must match exactly once (ambiguity is an error).
func EditFile(ctx context.Context, path, oldString, newString string, timeout time.Duration) types.ToolResult {
	start := time.Now()

	// Check context before starting
	if ctx.Err() != nil {
		return types.ToolResult{
			Error:    fmt.Sprintf("edit cancelled: %v", ctx.Err()),
			TimedOut: true,
			Duration: time.Since(start),
		}
	}

	done := make(chan types.ToolResult, 1)
	go func() {
		done <- editFileImpl(path, oldString, newString)
	}()

	select {
	case result := <-done:
		result.Duration = time.Since(start)
		return result
	case <-time.After(timeout):
		return types.ToolResult{
			Error:    fmt.Sprintf("edit timed out after %s", timeout),
			TimedOut: true,
			Duration: time.Since(start),
		}
	case <-ctx.Done():
		return types.ToolResult{
			Error:    fmt.Sprintf("edit cancelled: %v", ctx.Err()),
			TimedOut: true,
			Duration: time.Since(start),
		}
	}
}

func editFileImpl(path, oldString, newString string) types.ToolResult {
	// Step 1: Read file and compute content hash
	data, err := os.ReadFile(path)
	if err != nil {
		return types.ToolResult{Error: fmt.Sprintf("file not found: %v", err)}
	}
	content := string(data)
	originalHash := sha256.Sum256(data)

	// Step 2: Get original file permissions
	info, err := os.Stat(path)
	if err != nil {
		return types.ToolResult{Error: fmt.Sprintf("stat failed: %v", err)}
	}
	perm := info.Mode().Perm()

	// Step 3: Check match count (invariant 34)
	count := strings.Count(content, oldString)
	if count == 0 {
		return types.ToolResult{Error: "old_string not found in file"}
	}
	if count > 1 {
		return types.ToolResult{Error: fmt.Sprintf("old_string matches %d locations, must be exactly 1", count)}
	}

	// Step 4: If old == new, no-op success
	if oldString == newString {
		return types.ToolResult{Output: fmt.Sprintf("no change needed in %s", path)}
	}

	// Step 5: Apply replacement
	replaced := strings.Replace(content, oldString, newString, 1)

	// Step 6: Write to temp file in same directory (for atomic rename)
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".ghyll-edit-*")
	if err != nil {
		return types.ToolResult{Error: fmt.Sprintf("create temp file: %v", err)}
	}
	tmpPath := tmp.Name()

	if _, err := tmp.WriteString(replaced); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return types.ToolResult{Error: fmt.Sprintf("write temp file: %v", err)}
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return types.ToolResult{Error: fmt.Sprintf("close temp file: %v", err)}
	}

	// Step 7: Set permissions on temp file to match original
	if err := os.Chmod(tmpPath, perm); err != nil {
		_ = os.Remove(tmpPath)
		return types.ToolResult{Error: fmt.Sprintf("chmod temp file: %v", err)}
	}

	// Step 8: CAS check — re-read original, compare SHA256 (invariant 33).
	// Note: a narrow TOCTOU window exists between hash check and rename (microseconds).
	// True atomicity would require flock(), but hash-based CAS is a best-effort guard
	// that catches the vast majority of concurrent modifications.
	recheck, err := os.ReadFile(path)
	if err != nil {
		_ = os.Remove(tmpPath)
		return types.ToolResult{Error: fmt.Sprintf("re-read for CAS check: %v", err)}
	}
	recheckHash := sha256.Sum256(recheck)
	if originalHash != recheckHash {
		_ = os.Remove(tmpPath)
		return types.ToolResult{Error: "file modified during edit"}
	}

	// Step 9: Atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return types.ToolResult{Error: fmt.Sprintf("rename failed: %v", err)}
	}

	return types.ToolResult{
		Output: fmt.Sprintf("edited %s: replaced 1 occurrence", path),
	}
}
