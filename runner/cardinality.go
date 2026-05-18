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
// Semantic (validation-pass-4 F29): cardinality counts MATCHES, not
// lines. A regex `\bTODO\b` on a line "TODO and TODO" yields 2 —
// composable with regex. Both no-capture-group and capture-group
// regexes use the same counting path.
//
// Arguments:
//   query        : regex over file content. The query MUST be
//                  read-only — the evaluator doesn't validate
//                  this, but a side-effecting regex doesn't exist.
//   query-target : path-glob (files to query) OR the literal
//                  "project-state" (queries against in-memory
//                  state — not yet wired in v1; returns
//                  Unevaluated with reason).
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
		// F28: v1 doesn't support project-state; Unevaluated (not
		// runner-level error) so the gate result preserves operator
		// triage rather than a generic runner-fail.
		return &Result{
			Unevaluated: true,
			Reason:      "query-target `project-state` not yet supported in v1 (use path-glob)",
			Details:     map[string]any{"query-target": target},
		}, nil
	}

	re, err := regexp.Compile(query)
	if err != nil {
		return nil, fmt.Errorf("cardinality-check: query is not a valid regex: %w", err)
	}
	// F16: refuse zero-width / empty-match regexes — every line
	// would match and the count becomes meaningless.
	if re.MatchString("") {
		return nil, errors.New("cardinality-check: query matches the empty string (zero-width); refused")
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
		matches, err := countRegexMatches(ctx, fpath, re)
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

	ok2, expectedDesc, err := matchesExpected(count, expectedRaw)
	if err != nil {
		return nil, fmt.Errorf("cardinality-check: expected: %w", err)
	}

	details := map[string]any{
		"actual":   count,
		"expected": expectedDesc,
		"hits":     hits,
	}
	if !ok2 {
		details["error"] = fmt.Sprintf("cardinality %d does not match expected %s", count, expectedDesc)
	}
	return &Result{Pass: ok2, Details: details}, nil
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
		lo, err := coerceInt64(x[0])
		if err != nil {
			return false, "", fmt.Errorf("range min: %w", err)
		}
		hi, err := coerceInt64(x[1])
		if err != nil {
			return false, "", fmt.Errorf("range max: %w", err)
		}
		if lo > hi {
			return false, "", fmt.Errorf("range inverted: min %d > max %d", lo, hi)
		}
		desc := fmt.Sprintf("[%d, %d]", lo, hi)
		a := int64(actual)
		return a >= lo && a <= hi, desc, nil
	case []int:
		if len(x) != 2 {
			return false, "", fmt.Errorf("range must have exactly 2 elements")
		}
		lo, hi := int64(x[0]), int64(x[1])
		if lo > hi {
			return false, "", fmt.Errorf("range inverted: min %d > max %d", lo, hi)
		}
		desc := fmt.Sprintf("[%d, %d]", lo, hi)
		a := int64(actual)
		return a >= lo && a <= hi, desc, nil
	}
	return false, "", fmt.Errorf("expected must be int or [min, max]; got %T", expected)
}

// countRegexMatches counts total regex matches in path. One semantic
// path for both capture-group and no-capture-group regexes
// (F29: previously the no-capture path scanned the file twice and
// counted lines, conflating two semantics). Symlink-safe via
// O_NOFOLLOW; file-size capped; ctx-aware per ctxCheckEvery (F30).
func countRegexMatches(ctx context.Context, path string, re *regexp.Regexp) (int, error) {
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
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum%ctxCheckEvery == 0 {
			if err := ctx.Err(); err != nil {
				return count, err
			}
		}
		matches := re.FindAllStringIndex(scanner.Text(), -1)
		count += len(matches)
	}
	return count, nil
}
