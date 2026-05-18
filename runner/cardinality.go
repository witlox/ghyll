package runner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
)

// newLineScanner returns a bufio.Scanner configured for the runner's
// per-line scan cap. Centralized so all readers use the same
// settings.
func newLineScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return s
}

// cardinality-check built-in evaluator. Per
// gates/concepts/cardinality-check.yaml: a named query returns
// exactly the declared cardinality.
//
// Used by analyst G6 / integrator G4 ("findings.type values not in
// enum" — actual must be 0). The schema says queries can be regex,
// yaml-path, or SQL-like; v1 supports regex.
//
// Arguments:
//   query        : regex over file content. The query MUST be
//                  read-only — the evaluator doesn't validate
//                  this, but a side-effecting regex doesn't exist.
//   query-target : path-glob (files to query) OR the literal
//                  "project-state" (queries against in-memory
//                  state — not yet wired; returns an error).
//   expected     : int (exact match) OR [min, max] (range).

// EvaluateCardinalityCheck is the built-in for cardinality-check.
func EvaluateCardinalityCheck(ctx context.Context, c Clause) (*Result, error) {
	query, err := requireStringArg(c.Args, "query")
	if err != nil {
		return nil, fmt.Errorf("cardinality-check: %w", err)
	}
	target, err := requireStringArg(c.Args, "query-target")
	if err != nil {
		return nil, fmt.Errorf("cardinality-check: %w", err)
	}
	expectedRaw, ok := c.Args["expected"]
	if !ok {
		return nil, errors.New("cardinality-check: required arg `expected` missing")
	}

	if target == "project-state" {
		return nil, errors.New("cardinality-check: project-state target not yet supported (v1 supports path-glob only)")
	}

	re, err := regexp.Compile(query)
	if err != nil {
		return nil, fmt.Errorf("cardinality-check: query is not a valid regex: %w", err)
	}

	root := c.ProjectDir
	if root == "" {
		root = "."
	}
	files, err := scanFilesByGlob(ctx, root, target)
	if err != nil {
		return nil, fmt.Errorf("cardinality-check: scan %q: %w", target, err)
	}

	count := 0
	hits := []map[string]any{}
	for _, fpath := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		matches, err := countRegexMatches(fpath, re)
		if err != nil {
			return nil, fmt.Errorf("cardinality-check: count %q: %w", fpath, err)
		}
		if matches > 0 {
			count += matches
			hits = append(hits, map[string]any{
				"file":    fpath,
				"matches": matches,
			})
		}
	}

	ok, expectedDesc, err := matchesExpected(count, expectedRaw)
	if err != nil {
		return nil, fmt.Errorf("cardinality-check: expected: %w", err)
	}

	details := map[string]any{
		"actual":   count,
		"expected": expectedDesc,
		"hits":     hits,
	}
	if !ok {
		details["error"] = fmt.Sprintf("cardinality %d does not match expected %s", count, expectedDesc)
	}
	return &Result{Pass: ok, Details: details}, nil
}

// matchesExpected reports whether `actual` satisfies the expected
// cardinality (int or [min, max] inclusive range). Returns the
// human-readable form of `expected` for the details payload.
func matchesExpected(actual int, expected any) (bool, string, error) {
	if n, err := coerceInt64(expected); err == nil {
		desc := fmt.Sprintf("exactly %d", n)
		return int64(actual) == n, desc, nil
	}
	switch x := expected.(type) {
	case []any:
		if len(x) != 2 {
			return false, "", fmt.Errorf("range must have exactly 2 elements; got %d", len(x))
		}
		min, err := coerceInt64(x[0])
		if err != nil {
			return false, "", fmt.Errorf("range min: %w", err)
		}
		max, err := coerceInt64(x[1])
		if err != nil {
			return false, "", fmt.Errorf("range max: %w", err)
		}
		if min > max {
			return false, "", fmt.Errorf("range inverted: min %d > max %d", min, max)
		}
		desc := fmt.Sprintf("[%d, %d]", min, max)
		a := int64(actual)
		return a >= min && a <= max, desc, nil
	case []int:
		if len(x) != 2 {
			return false, "", fmt.Errorf("range must have exactly 2 elements")
		}
		min, max := int64(x[0]), int64(x[1])
		desc := fmt.Sprintf("[%d, %d]", min, max)
		a := int64(actual)
		return a >= min && a <= max, desc, nil
	}
	return false, "", fmt.Errorf("expected must be int or [min, max]; got %T", expected)
}

// countRegexMatches counts how many lines of the file match re.
// Multiple matches per line count as one (each line is a distinct
// "row" in the cardinality sense). Files larger than the size cap
// are skipped (returns 0, no error).
func countRegexMatches(path string, re *regexp.Regexp) (int, error) {
	captures, err := extractRegexCapturesRaw(path, re)
	if err != nil {
		return 0, err
	}
	return captures, nil
}

// extractRegexCapturesRaw counts the number of matched lines (one
// per line that has at least one match). Symlink-safe via
// O_NOFOLLOW; file-size capped.
func extractRegexCapturesRaw(path string, re *regexp.Regexp) (int, error) {
	hits := 0
	// Reuse tracelink's per-file scan pattern.
	captures, err := extractRegexCaptures(path, regexp.MustCompile(re.String()))
	if err != nil {
		return 0, err
	}
	// extractRegexCaptures returns ONE capture group per match.
	// For cardinality-check we want a count of matches.
	hits = len(captures)
	// If the regex has no capture group, fall back to FindAll-style
	// counting. We've already required NumSubexp>=1 in tracelink; for
	// cardinality, a no-capture regex is legitimate (we just count
	// matches). Re-run with full match if NumSubexp==0.
	if re.NumSubexp() == 0 {
		hits, err = countLinesMatching(path, re)
		if err != nil {
			return 0, err
		}
	}
	return hits, nil
}

// countLinesMatching counts lines in path where re finds at least
// one match. Used by cardinality-check when the regex has no
// capture group.
func countLinesMatching(path string, re *regexp.Regexp) (int, error) {
	f, err := openNoFollow(path)
	if err != nil {
		if isSymlinkOpenError(err) {
			return 0, nil
		}
		return 0, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxTodoScanFileSize {
		return 0, nil
	}
	count := 0
	scanner := newLineScanner(f)
	for scanner.Scan() {
		if re.MatchString(scanner.Text()) {
			count++
		}
	}
	return count, nil
}
