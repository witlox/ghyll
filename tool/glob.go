package tool

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/witlox/ghyll/types"
)

// Glob returns file paths matching a glob pattern within a directory.
// Invariant 35: only existing, workspace-local paths returned. Broken symlinks
// and symlinks pointing outside the workspace are excluded.
func Glob(ctx context.Context, pattern, basePath string, timeout time.Duration) types.ToolResult {
	start := time.Now()

	if pattern == "" {
		return types.ToolResult{
			Error:    "empty pattern",
			Duration: time.Since(start),
		}
	}

	done := make(chan types.ToolResult, 1)
	go func() {
		done <- globImpl(basePath, pattern)
	}()

	select {
	case result := <-done:
		result.Duration = time.Since(start)
		return result
	case <-time.After(timeout):
		return types.ToolResult{
			Error:    fmt.Sprintf("glob timed out after %s", timeout),
			TimedOut: true,
			Duration: time.Since(start),
		}
	case <-ctx.Done():
		return types.ToolResult{
			Error:    fmt.Sprintf("glob cancelled: %v", ctx.Err()),
			TimedOut: true,
			Duration: time.Since(start),
		}
	}
}

type fileEntry struct {
	relPath string
	modTime time.Time
}

func globImpl(basePath, pattern string) types.ToolResult {
	// Verify base path exists
	info, err := os.Stat(basePath)
	if err != nil {
		return types.ToolResult{Error: fmt.Sprintf("directory not found: %v", err)}
	}
	if !info.IsDir() {
		return types.ToolResult{Error: fmt.Sprintf("not a directory: %s", basePath)}
	}

	absBase, err := filepath.Abs(basePath)
	if err != nil {
		return types.ToolResult{Error: fmt.Sprintf("resolve path: %v", err)}
	}
	// Resolve symlinks in base path itself (macOS: /var → /private/var)
	realBase, err := filepath.EvalSymlinks(absBase)
	if err != nil {
		realBase = absBase // fallback to unresolved
	}

	var entries []fileEntry

	err = filepath.WalkDir(absBase, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if path == absBase {
			return nil
		}

		relPath, relErr := filepath.Rel(absBase, path)
		if relErr != nil {
			return nil
		}

		// WalkDir reports symlinks via DirEntry.Type()
		isSymlink := d.Type()&fs.ModeSymlink != 0

		if isSymlink {
			// Resolve symlink target
			target, resolveErr := filepath.EvalSymlinks(path)
			if resolveErr != nil {
				// Broken symlink — skip entirely
				return nil
			}
			absTarget, absErr := filepath.Abs(target)
			if absErr != nil {
				return nil
			}
			// Outside workspace — skip (invariant 35)
			// Compare against realBase (symlink-resolved) to handle /var → /private/var on macOS
			if !strings.HasPrefix(absTarget, realBase+string(os.PathSeparator)) && absTarget != realBase {
				return nil
			}
			// Valid workspace symlink — stat the target to check if file or dir
			targetInfo, statErr := os.Stat(target)
			if statErr != nil {
				return nil
			}
			if targetInfo.IsDir() {
				return nil // Skip directory symlinks
			}
			// It's a file symlink within workspace — process it
			matched, matchErr := matchGlob(pattern, relPath)
			if matchErr != nil || !matched {
				return nil
			}
			entries = append(entries, fileEntry{
				relPath: relPath,
				modTime: targetInfo.ModTime(),
			})
			return nil
		}

		// Regular entry
		if d.IsDir() {
			return nil
		}

		matched, matchErr := matchGlob(pattern, relPath)
		if matchErr != nil || !matched {
			return nil
		}

		fi, statErr := os.Stat(path)
		if statErr != nil {
			return nil
		}

		entries = append(entries, fileEntry{
			relPath: relPath,
			modTime: fi.ModTime(),
		})

		return nil
	})
	if err != nil {
		return types.ToolResult{Error: fmt.Sprintf("walk: %v", err)}
	}

	// Sort by modification time, most recent first
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].modTime.After(entries[j].modTime)
	})

	if len(entries) == 0 {
		return types.ToolResult{Output: ""}
	}

	var paths []string
	for _, e := range entries {
		paths = append(paths, e.relPath)
	}

	return types.ToolResult{Output: strings.Join(paths, "\n")}
}

// matchGlob matches a path against a glob pattern supporting ** for recursive.
func matchGlob(pattern, path string) (bool, error) {
	// Handle ** (recursive directory match)
	if strings.Contains(pattern, "**") {
		// Split pattern at **
		parts := strings.SplitN(pattern, "**", 2)
		prefix := parts[0]
		suffix := ""
		if len(parts) > 1 {
			suffix = strings.TrimPrefix(parts[1], "/")
			suffix = strings.TrimPrefix(suffix, string(os.PathSeparator))
		}

		// Check prefix match
		if prefix != "" {
			prefix = strings.TrimSuffix(prefix, "/")
			prefix = strings.TrimSuffix(prefix, string(os.PathSeparator))
			if !strings.HasPrefix(path, prefix+string(os.PathSeparator)) && path != prefix {
				return false, nil
			}
		}

		// If no suffix, match everything under prefix
		if suffix == "" {
			return true, nil
		}

		// Try matching suffix against every possible subpath
		pathParts := strings.Split(path, string(os.PathSeparator))
		for i := 0; i < len(pathParts); i++ {
			subpath := strings.Join(pathParts[i:], string(os.PathSeparator))
			matched, err := filepath.Match(suffix, subpath)
			if err != nil {
				return false, err
			}
			if matched {
				return true, nil
			}
			// Also try matching just the filename
			if i == len(pathParts)-1 {
				matched, err = filepath.Match(suffix, pathParts[i])
				if err != nil {
					return false, err
				}
				if matched {
					return true, nil
				}
			}
		}
		return false, nil
	}

	// Simple pattern without ** — match against full relative path
	return filepath.Match(pattern, path)
}
