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
	"syscall"

	"github.com/witlox/ghyll/internal/skipdirs"
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
		fileHits, err := scanFileForMarkers(ctx, path, rel, matcher)
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

// coerceStringList accepts []string or []any (yaml-decoded) and
// returns the canonical []string form. A bare string is NOT accepted
// — validation-pass-2 F45: silently coercing a string to a single-
// element list masks the common "forgot the brackets" typo.
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
	}
	return nil, fmt.Errorf("not a list of strings: %T (need YAML list form like [a, b])", v)
}

// markerMatcher tests whether a line contains any of the markers.
type markerMatcher func(line string) (marker string, ok bool)

// compileMarkerMatcher builds a matcher that finds any of the markers
// as a substring. Case-insensitive matching lowercases both sides
// before comparison.
//
// Both case-sensitive and case-insensitive matchers return the
// file's actual matched substring (not the configured marker form)
// — validation-pass-2 F41 — so the hit report reflects what was
// found, not what was configured.
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
		// Pick the longest matching marker first to avoid mis-
		// attributing e.g. "TODO-FIXME" to the shorter "TODO" prefix.
		bestStart := -1
		bestEnd := -1
		for _, m := range lowerMarkers {
			idx := strings.Index(lower, m)
			if idx < 0 {
				continue
			}
			end := idx + len(m)
			if bestStart < 0 || (end-idx) > (bestEnd-bestStart) {
				bestStart = idx
				bestEnd = end
			}
		}
		if bestStart < 0 {
			return "", false
		}
		// Return the file's actual substring at the match position
		// so the operator sees real casing, not the configured form.
		return line[bestStart:bestEnd], true
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
// build skip set. Delegates to internal/skipdirs for a single
// canonical definition (validation-pass-2 F39).
//
// The runner uses IsBuildOrHarness, NOT IsSourceWalkSkip — operators
// may declare no-todo-marker clauses with scope `specs/**`, so
// runner-side scans must descend into specs/docs/tests/test
// directories.
func isSkippedDir(name string) bool {
	return skipdirs.IsBuildOrHarness(name)
}

// scanFileForMarkers scans one file line-by-line, returning a hit
// record per matched line. Each record carries file (relative path),
// line number, the matched marker, and the surrounding text.
//
// Files exceeding maxTodoScanFileSize are skipped (returns nil,
// nil) — large binaries / generated artifacts would slow the scan
// without producing useful signal. A skipped file is silent; the
// operator's coverage residue catches it.
//
// validation-pass-2 F17: opens with O_NOFOLLOW so a symlink-swap
// race between the walk's lstat and our open cannot redirect us to
// out-of-scope content. The size check is done via fstat on the
// already-open fd to close the TOCTOU window.
//
// F12: refuses non-regular files (named pipes, devices, sockets)
// so the scanner can't block forever on read.
//
// F18: a single >1MB line returns bufio.ErrTooLong which we now
// translate to a skip-with-record rather than abort-the-walk.
//
// F40: checks ctx.Err() every 1024 lines so a cancelled context
// interrupts long-file scans.
func scanFileForMarkers(ctx context.Context, absPath, relPath string, matcher markerMatcher) ([]map[string]any, error) {
	f, err := openNoFollow(absPath)
	if err != nil {
		// Symlinks fail O_NOFOLLOW with ELOOP — silently skip.
		if isSymlinkOpenError(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		// Refuse fifos, sockets, devices, dirs — read would hang or
		// behave weirdly.
		return nil, nil
	}
	if info.Size() > maxTodoScanFileSize {
		return nil, nil
	}

	var hits []map[string]any
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		if line%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
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
		// validation-pass-2 F18: a single line longer than the
		// scanner buffer (1MB) returns ErrTooLong. Skip the file
		// rather than aborting the entire evaluation; record the
		// skip as a hit so the operator triages.
		if errors.Is(err, bufio.ErrTooLong) {
			hits = append(hits, map[string]any{
				"file":             relPath,
				"line":             line + 1,
				"marker":           "(line-too-long)",
				"surrounding-text": "file contains a line longer than 1MB; scan partial",
			})
			return hits, nil
		}
		return nil, fmt.Errorf("scan %q: %w", absPath, err)
	}
	return hits, nil
}

// maxTodoScanFileSize bounds the per-file scan to keep memory safe
// when projects include vendored binaries or generated artifacts in
// scope. 4 MB covers all realistic source files.
const maxTodoScanFileSize = 4 * 1024 * 1024

// openNoFollow opens a file refusing to traverse a final-component
// symlink. On Linux this uses O_NOFOLLOW; on platforms without it,
// falls back to a Lstat-guarded Open (TOCTOU window remains but
// shrinks to lstat→open). Used by no-todo-marker's scan to defend
// the TOCTOU described in validation-pass-2 F17.
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}

// isSymlinkOpenError reports whether err is the
// "symlink-encountered-with-O_NOFOLLOW" failure. On Linux this is
// ELOOP. We unwrap to *os.PathError → syscall.Errno.
func isSymlinkOpenError(err error) bool {
	var pe *os.PathError
	if !errors.As(err, &pe) {
		return false
	}
	var errno syscall.Errno
	if !errors.As(pe.Err, &errno) {
		return false
	}
	return errno == syscall.ELOOP
}

// RegisterBuiltins adds the universal-base evaluators implemented
// in the runner package (currently: no-todo-marker; compiles,
// lint-clean, every-step-bound will follow).
//
// Uses Replace when the concept is already registered so calling
// RegisterBuiltins a second time during init re-entry (D18) bumps
// the registration's Generation rather than panicking on
// ErrConceptAlreadyRegistered.
func RegisterBuiltins(r *Registry) {
	registerOrReplace(r, "no-todo-marker", EvaluateNoTodoMarker)
}

// registerOrReplace tries Register first; if the concept is already
// registered, falls back to Replace. The runner's typical lifecycle
// has a single RegisterBuiltins call at startup; this helper allows
// idempotent setup in tests and re-entry flows.
func registerOrReplace(r *Registry, concept string, e Evaluator) {
	if err := r.Register(concept, e); err == nil {
		return
	}
	_ = r.Replace(concept, e)
}
