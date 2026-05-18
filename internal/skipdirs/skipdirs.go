// Package skipdirs is the canonical set of directory names that
// project-aware filesystem walks (init profile, runner evaluators,
// orphan extraction) skip. Centralized here to prevent drift across
// callers — validation-pass-2 F39.
//
// Two layers:
//   - Source-aware (IsBuildOrHarness): directories that should not
//     be treated as source code (vendored deps, build outputs,
//     harness state, editor metadata). Both bootstrap and runner
//     skip these.
//   - Init-only (IsSpecOrDoc): directories that are project-level
//     non-source (specs, docs). Bootstrap's ProfileRepo skips these
//     during mode detection (a docs-only repo is greenfield); the
//     runner does NOT skip them (an operator may declare a
//     no-todo-marker clause that scopes specs/).
package skipdirs

// buildOrHarness is the set of directory names that universally
// mean "not part of the project's source code under review".
var buildOrHarness = map[string]struct{}{
	".git":          {},
	".ghyll":        {},
	".github":       {},
	"node_modules":  {},
	"vendor":        {},
	"target":        {},
	"bin":           {},
	"build":         {},
	"dist":          {},
	"out":           {},
	".idea":         {},
	".vscode":       {},
	"__pycache__":   {},
	".pytest_cache": {},
}

// specOrDoc is the set of directory names that are project-level
// non-source documentation. Used only by callers that want to
// distinguish "no source here" from "no anything here" — e.g.,
// bootstrap.ProfileRepo's greenfield/brownfield discriminator.
var specOrDoc = map[string]struct{}{
	"specs": {},
	"docs":  {},
	"tests": {},
	"test":  {},
}

// IsBuildOrHarness reports whether dirName is a build-output or
// harness-owned directory. Should be skipped by any walk that wants
// to read project source.
func IsBuildOrHarness(dirName string) bool {
	_, ok := buildOrHarness[dirName]
	return ok
}

// IsSpecOrDoc reports whether dirName is a project-level
// documentation directory. Bootstrap's mode detection skips these
// (a project containing only specs/ + docs/ is greenfield), but
// runner-side evaluators may legitimately need to scan them.
func IsSpecOrDoc(dirName string) bool {
	_, ok := specOrDoc[dirName]
	return ok
}

// IsSourceWalkSkip reports whether dirName should be skipped during
// a *source-discovery* walk: returns true for both build/harness
// dirs and spec/doc dirs. Equivalent to bootstrap's previous
// dirsToSkipForProfile set.
func IsSourceWalkSkip(dirName string) bool {
	return IsBuildOrHarness(dirName) || IsSpecOrDoc(dirName)
}
