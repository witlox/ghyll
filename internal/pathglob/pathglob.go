// Package pathglob is the canonical path-glob matcher used across
// the harness. Both bootstrap's modify-rule narrowing check and the
// runner's no-todo-marker scope filter delegate here to prevent
// drift (validation-pass-3 F13 — the two implementations had
// diverged on multi-`**` patterns).
//
// Match semantics:
//   - `*` matches zero or more characters within a single path
//     segment (does not cross `/`).
//   - `**` matches zero or more complete path segments (crosses `/`).
//   - `?` matches a single character within a segment.
//   - `[...]` character classes follow path.Match semantics.
//
// Returns false on malformed patterns (path.ErrBadPattern). The
// callers are safety gates, not glob compilers; an unclosed bracket
// is interpreted conservatively as "doesn't match" rather than a
// reported error.
package pathglob

import (
	"path"
	"strings"
)

// Match reports whether pattern matches name. Supports `**` as a
// multi-segment wildcard. Empty pattern matches only empty name.
func Match(pattern, name string) bool {
	if pattern == name {
		return true
	}
	if !strings.Contains(pattern, "**") {
		ok, err := path.Match(pattern, name)
		return err == nil && ok
	}
	return matchRecursive(pattern, name)
}

// matchRecursive handles patterns containing `**`. Splits on the
// first `**`; the suffix may itself contain further `**` (recursive
// case).
func matchRecursive(pattern, name string) bool {
	idx := strings.Index(pattern, "**")
	prefix := strings.TrimSuffix(pattern[:idx], "/")
	suffix := strings.TrimPrefix(pattern[idx+2:], "/")

	var segments []string
	if name != "" {
		segments = strings.Split(name, "/")
	}
	for i := 0; i <= len(segments); i++ {
		prefixCandidate := strings.Join(segments[:i], "/")
		if !matchSegments(prefix, prefixCandidate) {
			continue
		}
		for j := i; j <= len(segments); j++ {
			suffixCandidate := strings.Join(segments[j:], "/")
			if strings.Contains(suffix, "**") {
				if matchRecursive(suffix, suffixCandidate) {
					return true
				}
				continue
			}
			if matchSegments(suffix, suffixCandidate) {
				return true
			}
		}
	}
	return false
}

// matchSegments matches a no-`**` pattern against the name. Empty
// pattern matches only an empty name.
func matchSegments(pattern, name string) bool {
	if pattern == "" {
		return name == ""
	}
	ok, err := path.Match(pattern, name)
	return err == nil && ok
}
