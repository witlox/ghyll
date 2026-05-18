package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Containment helpers for evaluator paths. Validation-pass-4 F3/F4:
// operator-supplied paths (artifact-path, etc.) must be resolved
// against the project dir AND verified to stay inside it. Naive
// `projectDir + "/" + p` allows `..` traversal; `os.Lstat` on the
// final component ignores symlinks in parent directories.

// ErrPathEscapesProject indicates a resolved operator path falls
// outside the declared project directory.
var ErrPathEscapesProject = errors.New("path escapes project directory")

// ErrPathIntermediateSymlink indicates a parent directory of the
// resolved operator path is a symlink — refused to prevent escape
// through symlink-redirected parents.
var ErrPathIntermediateSymlink = errors.New("path contains intermediate symlink")

// ResolveProjectPath cleans an operator-supplied path against the
// project directory and verifies the result stays inside that
// directory (no `..` escape, no platform-specific absolute-path
// bypass) and that no parent component on the way is a symlink.
//
// Returns the canonical absolute path on success.
//
// Behavior:
//   - If projectDir is empty, the path is returned as-is after
//     Clean (legacy callers; tests still expect this).
//   - filepath.IsAbs(p) is rejected: operator paths are project-local.
//   - The final component itself may legitimately be a regular file
//     or a non-existent target — only PARENT components are required
//     to be non-symlinks. Final-component handling (symlink refusal,
//     existence) is the caller's job, via os.Lstat.
func ResolveProjectPath(projectDir, p string) (string, error) {
	if p == "" {
		return "", errors.New("empty path")
	}
	if filepath.IsAbs(p) {
		return "", fmt.Errorf("%w: absolute path %q not permitted", ErrPathEscapesProject, p)
	}
	// On Windows, also reject paths starting with backslash or volume
	// designator that filepath.IsAbs may miss in cross-platform tests.
	if strings.HasPrefix(p, "\\") || strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("%w: rooted path %q not permitted", ErrPathEscapesProject, p)
	}

	if projectDir == "" {
		// No project dir to root against; return the cleaned form.
		// This is a legacy concession; production callers should
		// always pass a project dir.
		return filepath.Clean(p), nil
	}

	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		return "", fmt.Errorf("project-dir abs: %w", err)
	}
	joined := filepath.Join(absProject, p)
	cleaned := filepath.Clean(joined)

	// Containment check using filepath.Rel: a path inside absProject
	// has a Rel result that does not start with "..".
	rel, err := filepath.Rel(absProject, cleaned)
	if err != nil {
		return "", fmt.Errorf("filepath.Rel %q under %q: %w", p, absProject, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: %q resolves outside project", ErrPathEscapesProject, p)
	}

	// Walk parent components from absProject outward and refuse any
	// intermediate symlink. The final component is the caller's job
	// to handle (it may be a non-existent file the caller is about
	// to create, or a regular file the caller will Lstat).
	if err := refuseIntermediateSymlinks(absProject, cleaned); err != nil {
		return "", err
	}

	return cleaned, nil
}

// refuseIntermediateSymlinks Lstats each component between (but not
// including) absProject and the final component of cleaned. Returns
// ErrPathIntermediateSymlink if any intermediate is a symlink.
//
// absProject is itself trusted (the harness's working directory);
// the final component is excluded since the caller will Lstat it.
func refuseIntermediateSymlinks(absProject, cleaned string) error {
	if cleaned == absProject {
		return nil
	}
	rel, err := filepath.Rel(absProject, cleaned)
	if err != nil {
		return err
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	if len(parts) <= 1 {
		return nil
	}
	// Walk all parts except the final one.
	cur := absProject
	for _, part := range parts[:len(parts)-1] {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				// A non-existent intermediate is not a symlink; the
				// caller will surface "missing" at the final lstat.
				return nil
			}
			return fmt.Errorf("intermediate lstat %q: %w", cur, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s", ErrPathIntermediateSymlink, cur)
		}
	}
	return nil
}
