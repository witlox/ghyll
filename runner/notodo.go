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
	"unicode/utf8"

	"github.com/witlox/ghyll/internal/pathglob"
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

// defaultTodoMarkers matches gates/concepts/no-todo-marker.yaml's
// `markers` default.
var defaultTodoMarkers = []string{"TODO", "TBD", "???", "FIXME", "XXX"}

// EvaluateNoTodoMarker is the built-in evaluator for the
// no-todo-marker concept.
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
		// Validation-pass-3 F3 (Critical): refuse zero-length
		// elements. An empty-string marker silently matches every
		// line of every file, failing every no-todo-marker clause
		// universally.
		for i, m := range markers {
			if m == "" {
				return nil, fmt.Errorf("no-todo-marker: markers[%d] is empty (would match every line)", i)
			}
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
	skipped := []map[string]any{}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Validation-pass-3 F44: filter recoverable errors so a
			// permission-denied subdir doesn't abort the whole scan.
			if errors.Is(walkErr, fs.ErrPermission) || errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			// Validation-pass-3 F30: only skip at IMMEDIATE root depth.
			// Otherwise `src/cli/build/release.go` would be hidden
			// because basename "build" is in the skip set.
			if path != root && filepath.Dir(path) == root && skipdirs.IsBuildOrHarness(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if !pathglob.Match(scope, rel) {
			return nil
		}
		fileHits, fileSkip, err := scanFileForMarkers(ctx, path, rel, matcher)
		if err != nil {
			return err
		}
		if fileSkip != nil {
			skipped = append(skipped, fileSkip)
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
	// Validation-pass-3 F32: a file the scanner couldn't read
	// completely (>1MB line, e.g.) returns Unevaluated so the
	// operator triages rather than the clause silently passing.
	if len(skipped) > 0 && len(hits) == 0 {
		return &Result{
			Unevaluated: true,
			Reason:      fmt.Sprintf("%d file(s) had unscannable content (e.g., minified single-line); see details.skipped", len(skipped)),
			Details:     map[string]any{"skipped": skipped, "hits": hits},
		}, nil
	}
	details := map[string]any{"hits": hits}
	if len(skipped) > 0 {
		details["skipped"] = skipped
	}
	return &Result{
		Pass:    !matched,
		Details: details,
	}, nil
}

// requireStringArg returns args[key] as a string or an error.
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
// returns the canonical []string form.
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
// as a substring. Both case-sensitive and case-insensitive matchers
// return the file's actual matched substring so the hit report
// reflects what was found, not what was configured.
//
// Validation-pass-3 F12: case-insensitive matcher previously sliced
// the original line using byte offsets from the lowered line.
// strings.ToLower can change byte length (U+0130 "İ" → "i"), so the
// slice ended on a wrong byte boundary, producing mojibake. Fixed
// by walking the original line via strings.EqualFold over rune
// substrings.
func compileMarkerMatcher(markers []string, caseSensitive bool) markerMatcher {
	if caseSensitive {
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
	// Case-insensitive: find the longest matching marker via
	// EqualFold-based substring scan over the original line. The
	// scan walks rune positions in the original so the returned
	// slice is on rune boundaries.
	return func(line string) (string, bool) {
		bestStart, bestEnd := -1, -1
		// For each rune-start position in line:
		runeStarts := runeBoundaries(line)
		for _, start := range runeStarts {
			tail := line[start:]
			for _, m := range markers {
				// Find m as a case-insensitive prefix of tail.
				end := caseInsensitivePrefixEnd(tail, m)
				if end < 0 {
					continue
				}
				absEnd := start + end
				// Prefer the longest match found at the earliest
				// position. Earliest position dominates; for ties,
				// longest wins.
				if bestStart < 0 || (start < bestStart) ||
					(start == bestStart && absEnd > bestEnd) {
					bestStart = start
					bestEnd = absEnd
				}
			}
		}
		if bestStart < 0 {
			return "", false
		}
		return line[bestStart:bestEnd], true
	}
}

// runeBoundaries returns the byte offset of each rune-start in s.
func runeBoundaries(s string) []int {
	out := make([]int, 0, len(s))
	for i := range s {
		out = append(out, i)
	}
	return out
}

// caseInsensitivePrefixEnd returns the byte offset in s where the
// case-insensitive prefix matching marker ends, or -1 if no match.
// Uses strings.EqualFold over a prefix scanned rune-by-rune.
func caseInsensitivePrefixEnd(s, marker string) int {
	if marker == "" {
		return -1 // defensive — empty markers rejected at evaluator entry
	}
	// Walk runes in marker; for each, consume one rune from s
	// and EqualFold-compare the rune pair.
	si := 0
	mi := 0
	for mi < len(marker) {
		if si >= len(s) {
			return -1
		}
		sRune, sSize := nextRune(s, si)
		mRune, mSize := nextRune(marker, mi)
		if !runeEqualFold(sRune, mRune) {
			return -1
		}
		si += sSize
		mi += mSize
	}
	return si
}

// nextRune decodes the rune at byte offset i in s and returns the
// rune + its encoded byte size.
func nextRune(s string, i int) (rune, int) {
	return utf8.DecodeRuneInString(s[i:])
}

// runeEqualFold reports whether r1 and r2 are equal under Unicode
// simple case folding.
func runeEqualFold(r1, r2 rune) bool {
	return strings.EqualFold(string(r1), string(r2))
}

// scanFileForMarkers scans one file line-by-line. Returns:
//   - hits: matched lines.
//   - skipped: a record describing why the file wasn't fully
//     scanned (e.g., a >1MB single line). nil if the scan was
//     complete. Validation-pass-3 F32 — line-too-long no longer
//     produces a fake "hit" that fails the clause; instead the
//     file is flagged for operator triage as Unevaluated.
//
// Files exceeding maxTodoScanFileSize are skipped silently (size
// is a hard binary/generated-artifact filter, not a triage
// signal).
//
// Validation-pass-2 F17: opens with O_NOFOLLOW (where supported).
// validation-pass-3 F31: accepts EMLINK as a portable symlink-on-
// open error in addition to ELOOP.
//
// F12: refuses non-regular files. F40: ctx.Err() checked per
// 1024 lines.
func scanFileForMarkers(ctx context.Context, absPath, relPath string, matcher markerMatcher) ([]map[string]any, map[string]any, error) {
	f, err := openNoFollow(absPath)
	if err != nil {
		if isSymlinkOpenError(err) {
			return nil, nil, nil
		}
		// Validation-pass-3 F44: permission-denied at file-open is
		// recoverable too. Skip the file silently.
		if errors.Is(err, fs.ErrPermission) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, nil
	}
	if info.Size() > maxTodoScanFileSize {
		return nil, nil, nil
	}

	var hits []map[string]any
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		if line%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
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
		if errors.Is(err, bufio.ErrTooLong) {
			// File contains a line longer than 1 MB (minified,
			// generated, etc.). Return what we have plus a skipped
			// record. The evaluator surfaces this as Unevaluated
			// per F32.
			return hits, map[string]any{
				"file":   relPath,
				"reason": "line-too-long",
				"detail": "file contains a line longer than 1MB; partial scan",
			}, nil
		}
		return nil, nil, fmt.Errorf("scan %q: %w", absPath, err)
	}
	return hits, nil, nil
}

// maxTodoScanFileSize bounds the per-file scan.
const maxTodoScanFileSize = 4 * 1024 * 1024

// openNoFollow opens a file refusing to traverse a final-component
// symlink. Validation-pass-3 F31: clamped to platforms with
// syscall.O_NOFOLLOW (Linux/BSD/macOS — Windows would need a
// different approach not currently in scope).
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}

// isSymlinkOpenError reports whether err is one of the platform-
// specific "symlink encountered with O_NOFOLLOW" errnos. ELOOP on
// Linux; EMLINK on some BSDs (validation-pass-3 F31).
func isSymlinkOpenError(err error) bool {
	var pe *os.PathError
	if !errors.As(err, &pe) {
		return false
	}
	var errno syscall.Errno
	if !errors.As(pe.Err, &errno) {
		return false
	}
	return errno == syscall.ELOOP || errno == syscall.EMLINK
}

// RegisterBuiltins adds the in-process evaluators the runner
// ships with. These are language-agnostic concepts that don't
// require a subprocess binding (no-todo-marker, trace-link-present,
// arrow-artifact-present, cardinality-check). Language-bound
// concepts (compiles, lint-clean, tests-pass, mutation-score,
// kill-server-fails-integration) get registered separately via
// BindingEvaluator at init time from grid.LanguageBindings.
func RegisterBuiltins(r *Registry) {
	registerOrReplace(r, "no-todo-marker", EvaluateNoTodoMarker)
	registerOrReplace(r, "trace-link-present", EvaluateTraceLinkPresent)
	registerOrReplace(r, "arrow-artifact-present", EvaluateArrowArtifactPresent)
	registerOrReplace(r, "cardinality-check", EvaluateCardinalityCheck)
	registerOrReplace(r, "no-open-finding", EvaluateNoOpenFinding)
	registerOrReplace(r, "kill-server-fails-integration", EvaluateKillServerFailsIntegration)
	registerOrReplace(r, "every-requirement-meets-min-depth", EvaluateEveryRequirementMeetsMinDepth)
}

// registerOrReplace tries Register; on ErrConceptAlreadyRegistered
// falls back to Replace. Other Register errors propagate via the
// fallback's Replace call (which will fail with ErrConceptNotRegistered
// if Register's failure was something other than dup-registration —
// fail-loud rather than silent per validation-pass-3 F10).
func registerOrReplace(r *Registry, concept string, e Evaluator) {
	regErr := r.Register(concept, e)
	if regErr == nil {
		return
	}
	if !errors.Is(regErr, ErrConceptAlreadyRegistered) {
		// Truly unexpected — surface via panic so the operator
		// sees the misuse at startup.
		panic(fmt.Sprintf("RegisterBuiltins: %v", regErr))
	}
	if err := r.Replace(concept, e); err != nil {
		panic(fmt.Sprintf("RegisterBuiltins: Replace fallback: %v", err))
	}
}
