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
// at least one capture group; for each `from` file, the regex is
// applied against its contents, and the first non-empty capture per
// match is taken as the link target (F13). Each captured target must
// match the basename (or full relative path) of some `to` file.
// min-multiplicity and max-multiplicity bound the count of distinct
// captured targets per `from` file.

// maxCapturesPerFile bounds how many captures one `from` file may
// contribute (validation-pass-4 F15). A pathological regex on a
// large file can otherwise OOM the runner. Beyond this, additional
// matches are silently truncated and the per-file result records
// "captures-truncated".
const maxCapturesPerFile = 10000

// ctxCheckEvery is the line interval at which scanners check
// ctx.Err() (F30). Matches notodo's cadence.
const ctxCheckEvery = 1024

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
	// F16: refuse zero-width / empty-match regexes. A rule that
	// matches the empty string yields false positives at every
	// position.
	if re.MatchString("") {
		return nil, errors.New("trace-link-present: link-rule matches the empty string (zero-width); refused")
	}

	root := c.ProjectDir
	if root == "" {
		root = "."
	}

	toFiles, err := scanFilesByGlob(ctx, root, toGlob)
	if err != nil {
		return nil, fmt.Errorf("trace-link-present: scan `to`: %w", err)
	}
	// F27: empty `to` glob → Unevaluated. Operator likely misspelled
	// the glob; treating as Fail conflates config errors with real
	// findings.
	if len(toFiles) == 0 {
		return &Result{
			Unevaluated: true,
			Reason:      fmt.Sprintf("no files match `to` glob %q; cannot evaluate trace targets", toGlob),
			Details: map[string]any{
				"from-glob": fromGlob,
				"to-glob":   toGlob,
			},
		}, nil
	}
	toIndex := buildLinkIndex(toFiles)

	fromFiles, err := scanFilesByGlob(ctx, root, fromGlob)
	if err != nil {
		return nil, fmt.Errorf("trace-link-present: scan `from`: %w", err)
	}
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
		File       string              `json:"file"`
		Targets    []string            `json:"targets"`
		ResolvedTo map[string][]string `json:"resolved-to"`
		Unmet      bool                `json:"unmet"`
		Truncated  bool                `json:"truncated"`
		Reason     string              `json:"reason"`
	}
	results := make([]fromResult, 0, len(fromFiles))
	unmet := 0

	for _, fpath := range fromFiles {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		captures, truncated, err := extractRegexCaptures(ctx, fpath, re)
		if err != nil {
			return nil, fmt.Errorf("trace-link-present: extract %q: %w", fpath, err)
		}
		// Distinct captures only.
		seen := make(map[string]struct{}, len(captures))
		matched := make([]string, 0, len(captures))
		resolvedTo := make(map[string][]string, len(captures))
		for _, cap := range captures {
			if cap == "" {
				continue
			}
			if _, ok := seen[cap]; ok {
				continue
			}
			seen[cap] = struct{}{}
			if files, ok := toIndex[cap]; ok {
				matched = append(matched, cap)
				resolvedTo[cap] = files
			}
		}
		rel, _ := filepath.Rel(root, fpath)
		r := fromResult{File: rel, Targets: matched, ResolvedTo: resolvedTo, Truncated: truncated}
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

	rendered := make([]map[string]any, len(results))
	for i, r := range results {
		entry := map[string]any{
			"file":        r.File,
			"targets":     r.Targets,
			"resolved-to": r.ResolvedTo,
		}
		if r.Truncated {
			entry["captures-truncated"] = true
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
// glob. Skips harness/build dirs at root depth. Respects ctx.Cancel.
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

// buildLinkIndex maps every identifier a `to` file can be referenced
// by (rel path, basename, basename-without-extension) to the list of
// `to` files that resolve to that identifier (F14). Multi-language
// projects where `auth.go` and `auth.md` both contribute basename
// `auth` no longer silently collide — the operator sees which `to`
// file(s) each capture resolved to in the per-from result.
func buildLinkIndex(files []string) map[string][]string {
	idx := make(map[string][]string, 2*len(files))
	add := func(key, file string) {
		if key == "" {
			return
		}
		idx[key] = append(idx[key], file)
	}
	for _, f := range files {
		add(f, f)
		base := filepath.Base(f)
		add(base, f)
		if ext := filepath.Ext(base); ext != "" {
			trimmed := strings.TrimSuffix(base, ext)
			add(trimmed, f)
		}
	}
	return idx
}

// extractRegexCaptures returns the first non-empty capture (F13)
// for every match of re in the file at path. Refuses non-regular
// files and symlinks (O_NOFOLLOW). Per-file size cap. Per-file
// capture cap (F15). Checks ctx.Err() every ctxCheckEvery lines
// (F30).
//
// Returns (captures, truncated, error) where truncated reports
// whether the capture cap was hit.
func extractRegexCaptures(ctx context.Context, path string, re *regexp.Regexp) ([]string, bool, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if isSymlinkOpenError(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, nil
	}
	if info.Size() > maxTodoScanFileSize {
		return nil, false, nil
	}
	var captures []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNum := 0
	truncated := false
	for scanner.Scan() {
		lineNum++
		if lineNum%ctxCheckEvery == 0 {
			if err := ctx.Err(); err != nil {
				return captures, truncated, err
			}
		}
		matches := re.FindAllStringSubmatch(scanner.Text(), -1)
		for _, m := range matches {
			cap := firstNonEmptyCapture(m)
			if cap == "" {
				continue
			}
			captures = append(captures, cap)
			if len(captures) >= maxCapturesPerFile {
				truncated = true
				break
			}
		}
		if truncated {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return captures, truncated, nil
		}
		return nil, false, err
	}
	return captures, truncated, nil
}

// firstNonEmptyCapture returns the first non-empty capture group
// (m[1:]) for one match. Per F13: regex with alternation across
// capture groups was returning only m[1], silently losing the
// alternative branch's capture.
func firstNonEmptyCapture(m []string) string {
	for i := 1; i < len(m); i++ {
		if m[i] != "" {
			return m[i]
		}
	}
	return ""
}
