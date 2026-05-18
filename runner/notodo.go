package runner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// no-todo-marker built-in evaluator. Per gates/concepts/no-todo-marker.yaml:
//
//   scope:           path-glob, required
//   markers:         list of strings, default [TODO, TBD, "???", FIXME, XXX]
//   case-sensitive:  bool, default false
//
// The evaluator scans every line of every file under `scope` for any
// marker. A hit becomes a fail with details listing file/line/marker
// /surrounding-text per the concept's `produces` shape.
//
// Universal-base concepts (compiles, lint-clean, no-todo-marker,
// every-step-bound) ship with built-in evaluators. no-todo-marker is
// the simplest of the four — pure regex scan, no language binding.

// defaultTodoMarkers matches gates/concepts/no-todo-marker.yaml's
// `markers` default. Kept in sync manually; if the schema changes,
// this list does too (the schema is the canonical source).
var defaultTodoMarkers = []string{"TODO", "TBD", "???", "FIXME", "XXX"}

// EvaluateNoTodoMarker is the built-in evaluator for the
// no-todo-marker concept. Exposed for the runner's Registry but
// callable directly in tests.
func EvaluateNoTodoMarker(ctx context.Context, c Clause) (*Result, error) {
	scope, err := requireStringArg(c.Args, "scope")
	if err != nil {
		return nil, fmt.Errorf("no-todo-marker: %w", err)
	}
	markers := defaultTodoMarkers
	if v, ok := c.Args["markers"]; ok {
		markers, err = coerceStringList(v)
		if err != nil {
			return nil, fmt.Errorf("no-todo-marker: markers: %w", err)
		}
		if len(markers) == 0 {
			return nil, errors.New("no-todo-marker: markers list is empty")
		}
	}
	caseSensitive := false
	if v, ok := c.Args["case-sensitive"]; ok {
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("no-todo-marker: case-sensitive must be bool, got %T", v)
		}
		caseSensitive = b
	}

	matcher := compileMarkerMatcher(markers, caseSensitive)

	root := c.ProjectDir
	if root == "" {
		root = "."
	}
	hits := []map[string]any{}
	matched := false

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			// Skip harness/build dirs to avoid scanning vendored or
			// generated TODOs. Mirrors bootstrap.dirsToSkipForProfile;
			// we duplicate the smaller relevant set here to keep
			// runner from importing bootstrap.
			if path != root && isSkippedDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if !matchesScope(scope, rel) {
			return nil
		}
		fileHits, err := scanFileForMarkers(path, rel, matcher)
		if err != nil {
			return err
		}
		if len(fileHits) > 0 {
			matched = true
			hits = append(hits, fileHits...)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("no-todo-marker: walk %q: %w", root, err)
	}
	return &Result{
		Pass:    !matched,
		Details: map[string]any{"hits": hits},
	}, nil
}

// requireStringArg returns args[key] as a string or an error if the
// key is missing or the value is not a string.
func requireStringArg(args map[string]any, key string) (string, error) {
	v, ok := args[key]
	if !ok {
		return "", fmt.Errorf("required arg %q missing", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("arg %q must be string, got %T", key, v)
	}
	return s, nil
}

// coerceStringList accepts []string, []any (yaml-decoded), or a
// single string and returns the canonical []string form.
func coerceStringList(v any) ([]string, error) {
	switch x := v.(type) {
	case []string:
		out := make([]string, len(x))
		copy(out, x)
		return out, nil
	case []any:
		out := make([]string, 0, len(x))
		for i, item := range x {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("list[%d] not a string: %T", i, item)
			}
			out = append(out, s)
		}
		return out, nil
	case string:
		return []string{x}, nil
	}
	return nil, fmt.Errorf("not a list of strings: %T", v)
}

// markerMatcher tests whether a line contains any of the markers.
type markerMatcher func(line string) (marker string, ok bool)

// compileMarkerMatcher builds a matcher that finds any of the markers
// as a substring. Case-insensitive matching lowercases both sides
// before comparison.
func compileMarkerMatcher(markers []string, caseSensitive bool) markerMatcher {
	if caseSensitive {
		// Build a single regex with word-boundary-ish matching. We
		// keep it permissive (substring) to match the concept's
		// "captures the work-not-done signal that prose can hide"
		// description — strict word-boundary would miss "TODO:".
		alts := make([]string, len(markers))
		for i, m := range markers {
			alts[i] = regexp.QuoteMeta(m)
		}
		re := regexp.MustCompile(strings.Join(alts, "|"))
		return func(line string) (string, bool) {
			loc := re.FindStringIndex(line)
			if loc == nil {
				return "", false
			}
			return line[loc[0]:loc[1]], true
		}
	}
	lowerMarkers := make([]string, len(markers))
	for i, m := range markers {
		lowerMarkers[i] = strings.ToLower(m)
	}
	return func(line string) (string, bool) {
		lower := strings.ToLower(line)
		for i, m := range lowerMarkers {
			if strings.Contains(lower, m) {
				return markers[i], true
			}
		}
		return "", false
	}
}

// matchesScope reports whether rel (a path under the project root)
// matches the scope glob. Uses the same recursive-glob matcher
// bootstrap.modify uses for path-glob comparison so both sides
// agree on what "src/**" means.
func matchesScope(scope, rel string) bool {
	if scope == rel {
		return true
	}
	if !strings.Contains(scope, "**") {
		ok, err := filepath.Match(scope, rel)
		return err == nil && ok
	}
	// ** expansion: split scope at the first **, enumerate segment
	// substitutions in `rel`, and try each candidate.
	idx := strings.Index(scope, "**")
	prefix := strings.TrimSuffix(scope[:idx], "/")
	suffix := strings.TrimPrefix(scope[idx+2:], "/")
	segments := strings.Split(rel, "/")
	for i := 0; i <= len(segments); i++ {
		for j := i; j <= len(segments); j++ {
			var middle string
			if j > i {
				middle = strings.Join(segments[i:j], "/")
			}
			parts := []string{}
			if prefix != "" {
				parts = append(parts, prefix)
			}
			if middle != "" {
				parts = append(parts, middle)
			}
			if suffix != "" {
				parts = append(parts, suffix)
			}
			candidate := strings.Join(parts, "/")
			ok, err := filepath.Match(candidate, rel)
			if err == nil && ok {
				return true
			}
		}
	}
	return false
}

// isSkippedDir reports whether a directory name is in the harness/
// build skip set. Kept in sync with bootstrap.dirsToSkipForProfile
// for the subset relevant to source scans.
func isSkippedDir(name string) bool {
	switch name {
	case ".git", ".ghyll", ".github", "node_modules", "vendor",
		"target", "bin", "build", "dist", "out",
		".idea", ".vscode", "__pycache__", ".pytest_cache":
		return true
	}
	return false
}

// scanFileForMarkers scans one file line-by-line, returning a hit
// record per matched line. Each record carries file (relative path),
// line number, the matched marker, and the surrounding text.
//
// Files exceeding maxTodoScanFileSize are skipped (returns nil,
// nil) — large binaries / generated artifacts would slow the scan
// without producing useful signal. A skipped file is silent; the
// operator's coverage residue catches it.
func scanFileForMarkers(absPath, relPath string, matcher markerMatcher) ([]map[string]any, error) {
	info, err := os.Lstat(absPath)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, nil // skip symlinks
	}
	if info.Size() > maxTodoScanFileSize {
		return nil, nil
	}
	f, err := os.Open(absPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var hits []map[string]any
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		text := scanner.Text()
		marker, ok := matcher(text)
		if !ok {
			continue
		}
		hits = append(hits, map[string]any{
			"file":             relPath,
			"line":             line,
			"marker":           marker,
			"surrounding-text": text,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %q: %w", absPath, err)
	}
	return hits, nil
}

// maxTodoScanFileSize bounds the per-file scan to keep memory safe
// when projects include vendored binaries or generated artifacts in
// scope. 4 MB covers all realistic source files.
const maxTodoScanFileSize = 4 * 1024 * 1024

// RegisterBuiltins adds the universal-base evaluators implemented
// in the runner package (currently: no-todo-marker; compiles,
// lint-clean, every-step-bound will follow). Idempotent.
func RegisterBuiltins(r *Registry) {
	r.Register("no-todo-marker", EvaluateNoTodoMarker)
}
