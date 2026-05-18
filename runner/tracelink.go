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

	"github.com/witlox/ghyll/internal/pathglob"
	"github.com/witlox/ghyll/internal/skipdirs"
)

// trace-link-present built-in evaluator. Per
// gates/concepts/trace-link-present.yaml: verifies a declared link
// between two artifacts exists with the declared multiplicity.
//
// Used by analyst G6 (coverage-claim trace), implementer G3
// (feature→test trace), integrator G1 (L4-interaction → integration-
// test) + G2 (integration-test → L4-spec).
//
// v1 implementation: the `from` and `to` arguments are path-globs
// pointing at files. The `link-rule` is a Go regular expression with
// one capture group; for each `from` file, the regex is applied
// against its contents, and the captured strings are taken as link
// targets. Each captured target must match the basename (or full
// relative path) of some `to` file. min-multiplicity and
// max-multiplicity bound the count of distinct captured targets per
// `from` file.

// EvaluateTraceLinkPresent is the built-in for trace-link-present.
func EvaluateTraceLinkPresent(ctx context.Context, c Clause) (*Result, error) {
	fromGlob, err := requireStringArg(c.Args, "from")
	if err != nil {
		return nil, fmt.Errorf("trace-link-present: %w", err)
	}
	toGlob, err := requireStringArg(c.Args, "to")
	if err != nil {
		return nil, fmt.Errorf("trace-link-present: %w", err)
	}
	linkRule, err := requireStringArg(c.Args, "link-rule")
	if err != nil {
		return nil, fmt.Errorf("trace-link-present: %w", err)
	}
	minMult := int64(1)
	if v, ok := c.Args["min-multiplicity"]; ok {
		n, err := coerceInt64(v)
		if err != nil {
			return nil, fmt.Errorf("trace-link-present: min-multiplicity: %w", err)
		}
		if n < 0 {
			return nil, fmt.Errorf("trace-link-present: min-multiplicity must be >= 0, got %d", n)
		}
		minMult = n
	}
	maxMult := int64(-1) // -1 = unlimited
	if v, ok := c.Args["max-multiplicity"]; ok {
		n, err := coerceInt64(v)
		if err != nil {
			return nil, fmt.Errorf("trace-link-present: max-multiplicity: %w", err)
		}
		if n < minMult {
			return nil, fmt.Errorf("trace-link-present: max-multiplicity (%d) < min-multiplicity (%d)", n, minMult)
		}
		maxMult = n
	}

	re, err := regexp.Compile(linkRule)
	if err != nil {
		return nil, fmt.Errorf("trace-link-present: link-rule is not a valid regex: %w", err)
	}
	if re.NumSubexp() < 1 {
		return nil, errors.New("trace-link-present: link-rule must declare one capture group (the link target)")
	}

	root := c.ProjectDir
	if root == "" {
		root = "."
	}

	// Index the `to` side: collect every file matching `toGlob` and
	// build a set of normalized identifiers we can match captures
	// against. v1 indexes by both relative path AND file basename
	// (operators tend to use either form).
	toFiles, err := scanFilesByGlob(ctx, root, toGlob)
	if err != nil {
		return nil, fmt.Errorf("trace-link-present: scan `to`: %w", err)
	}
	toIndex := buildLinkIndex(toFiles)

	// Walk the `from` side: for each file, extract captures, count
	// distinct ones that resolve to a `to` entry. Record per-file
	// results.
	fromFiles, err := scanFilesByGlob(ctx, root, fromGlob)
	if err != nil {
		return nil, fmt.Errorf("trace-link-present: scan `from`: %w", err)
	}
	// Vacuous-pass guard: if no `from` files match, the result is
	// undecidable, not "passes by default". Return Unevaluated so
	// the operator triages the empty scope.
	if len(fromFiles) == 0 {
		return &Result{
			Unevaluated: true,
			Reason:      fmt.Sprintf("no files match `from` glob %q", fromGlob),
			Details: map[string]any{
				"from-glob": fromGlob,
				"to-glob":   toGlob,
			},
		}, nil
	}

	type fromResult struct {
		File    string   `json:"file"`
		Targets []string `json:"targets"`
		Unmet   bool     `json:"unmet"`
		Reason  string   `json:"reason"`
	}
	results := make([]fromResult, 0, len(fromFiles))
	unmet := 0

	for _, fpath := range fromFiles {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		captures, err := extractRegexCaptures(fpath, re)
		if err != nil {
			return nil, fmt.Errorf("trace-link-present: extract %q: %w", fpath, err)
		}
		// Distinct captures only.
		seen := make(map[string]struct{}, len(captures))
		matched := make([]string, 0, len(captures))
		for _, cap := range captures {
			if _, ok := seen[cap]; ok {
				continue
			}
			seen[cap] = struct{}{}
			if _, ok := toIndex[cap]; ok {
				matched = append(matched, cap)
			}
		}
		rel, _ := filepath.Rel(root, fpath)
		r := fromResult{File: rel, Targets: matched}
		count := int64(len(matched))
		switch {
		case count < minMult:
			r.Unmet = true
			r.Reason = fmt.Sprintf("count %d < min-multiplicity %d", count, minMult)
			unmet++
		case maxMult >= 0 && count > maxMult:
			r.Unmet = true
			r.Reason = fmt.Sprintf("count %d > max-multiplicity %d", count, maxMult)
			unmet++
		}
		results = append(results, r)
	}

	// Marshalable detail records.
	rendered := make([]map[string]any, len(results))
	for i, r := range results {
		entry := map[string]any{
			"file":    r.File,
			"targets": r.Targets,
		}
		if r.Unmet {
			entry["unmet"] = true
			entry["reason"] = r.Reason
		}
		rendered[i] = entry
	}

	return &Result{
		Pass: unmet == 0,
		Details: map[string]any{
			"from-count": len(fromFiles),
			"to-count":   len(toFiles),
			"unmet":      unmet,
			"results":    rendered,
		},
	}, nil
}

// scanFilesByGlob returns regular-file paths under root matching the
// glob (using internal/pathglob's `**`-aware matcher). Skips
// harness/build dirs at root depth. Respects ctx.Cancel.
func scanFilesByGlob(ctx context.Context, root, glob string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrPermission) || errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && filepath.Dir(path) == root && skipdirs.IsBuildOrHarness(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if !pathglob.Match(glob, rel) {
			return nil
		}
		// Refuse non-regular files at file-open time too (defense
		// in depth — entry.Type only reflects lstat).
		info, err := os.Lstat(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// buildLinkIndex builds a set of identifier strings that capture-
// extractions can be matched against. Each `to` file contributes
// two entries: its relative path AND its basename without
// extension. Operators write link rules referring to either form.
func buildLinkIndex(files []string) map[string]struct{} {
	idx := make(map[string]struct{}, 2*len(files))
	for _, f := range files {
		idx[f] = struct{}{}
		base := filepath.Base(f)
		idx[base] = struct{}{}
		// Without extension.
		if ext := filepath.Ext(base); ext != "" {
			idx[strings.TrimSuffix(base, ext)] = struct{}{}
		}
	}
	return idx
}

// extractRegexCaptures returns capture-group-1 for every match of
// re in the file at path. Refuses non-regular files and symlinks
// (O_NOFOLLOW). Capped at maxTodoScanFileSize to keep memory
// bounded.
func extractRegexCaptures(path string, re *regexp.Regexp) ([]string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
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
		return nil, nil
	}
	if info.Size() > maxTodoScanFileSize {
		return nil, nil
	}
	var captures []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		matches := re.FindAllStringSubmatch(scanner.Text(), -1)
		for _, m := range matches {
			if len(m) > 1 {
				captures = append(captures, m[1])
			}
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			// Partial scan — return what we have.
			return captures, nil
		}
		return nil, err
	}
	return captures, nil
}
