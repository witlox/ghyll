package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Init sub-phase A: project profile + context discovery.
// Per ADR-011 sub-phase A:
//
//   Init interrogates the operator (or scans the repo in brownfield
//   mode) to determine: bounded contexts, languages used, refusal-or-
//   proceed.
//
// ProfileRepo implements the machine-determinable portion: greenfield
// vs brownfield detection + context proposal from directory layout +
// language detection from file extensions. Operator-declared contexts
// extend the proposal via DeclareContext.

// Mode is the project profile's mode discriminator (per the
// mode-determinable-from-repo catalogue concept). Either the harness
// can find code in the repo (brownfield) or it cannot (greenfield).
type Mode string

const (
	ModeGreenfield Mode = "greenfield"
	ModeBrownfield Mode = "brownfield"
)

// BoundedContext (declared in grid.go) is one project context. In
// brownfield, contexts are proposed from src/<id>/ subdirectories and
// the operator confirms or refines. In greenfield, contexts are
// operator-supplied via DeclareContext on the profile.

// ProjectProfile is the result of init sub-phase A.
//
// Mode is determined first (greenfield if no source code exists,
// brownfield otherwise). BoundedContexts is operator-confirmed in
// brownfield (initially populated by directory scan), operator-supplied
// in greenfield (initially empty). Languages is detected from file
// extensions in brownfield and empty in greenfield until operator
// declaration.
//
// DiamondRoles is fixed per ADR-003 (the four-role diamond). The
// arrow set the harness ships with is derived from DiamondRoles.
type ProjectProfile struct {
	Mode            Mode
	BoundedContexts []BoundedContext
	Languages       []string
	DiamondRoles    []string

	// projectDir is retained so further interrogation steps (orphan
	// extraction, language confirmation) can re-scan without the
	// caller threading it back through.
	projectDir string
}

// FixedDiamondRoles is the four-role diamond per ADR-003. The diamond
// is structural: every project ships these roles regardless of
// language or domain. Roles are runtime entities; their order here
// matches the upstream-to-downstream flow.
var FixedDiamondRoles = []string{"analyst", "architect", "implementer", "integrator"}

// fileExtensionToLanguage maps a recognized source extension to its
// language id (matching the language-id type in the catalogue). Only
// languages we have at least one binding pattern documented for are
// listed; encountering an unrecognized extension does NOT add an
// entry (the file is ignored for language detection — the operator
// can declare additional languages explicitly).
var fileExtensionToLanguage = map[string]string{
	".go":   "go",
	".py":   "python",
	".rs":   "rust",
	".ts":   "typescript",
	".tsx":  "typescript",
	".js":   "javascript",
	".jsx":  "javascript",
	".java": "java",
	".rb":   "ruby",
	".cs":   "csharp",
	".kt":   "kotlin",
}

// dirsToSkipForProfile is the set of directory names ProfileRepo will
// not descend into when looking for source code or contexts. These
// are harness-owned, build, or non-source areas.
var dirsToSkipForProfile = map[string]struct{}{
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
	"specs":         {},
	"docs":          {},
	"tests":         {},
	"test":          {},
	".idea":         {},
	".vscode":       {},
	"__pycache__":   {},
	".pytest_cache": {},
}

// Profile errors.
var (
	ErrProfileNilDir         = errors.New("profile-project-dir-empty")
	ErrProfileContextEmpty   = errors.New("profile-context-id-empty")
	ErrProfileContextDup     = errors.New("profile-context-id-duplicate")
	ErrProfileContextInvalid = errors.New("profile-context-id-invalid")
)

// ProfileRepo scans projectDir and returns a profile describing the
// repo state at init time.
//
// Mode is determined by whether the directory contains any
// recognized source code:
//
//   - Brownfield: at least one source file exists (.go, .py, .rs,
//     .ts, etc.) anywhere outside the skip-dir set. Contexts are
//     proposed from src/<id>/ subdirectories. Languages reflects the
//     observed file extensions.
//   - Greenfield: no source files found. BoundedContexts and Languages
//     are empty; the operator supplies them via DeclareContext (and a
//     future DeclareLanguage).
//
// ProfileRepo does not write to disk or invoke external commands; it
// is read-only over the directory tree.
func ProfileRepo(projectDir string) (*ProjectProfile, error) {
	if projectDir == "" {
		return nil, ErrProfileNilDir
	}
	info, err := os.Stat(projectDir)
	if err != nil {
		return nil, fmt.Errorf("ProfileRepo: stat %q: %w", projectDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("ProfileRepo: %q is not a directory", projectDir)
	}

	languages, hasSource, err := scanLanguages(projectDir)
	if err != nil {
		return nil, fmt.Errorf("ProfileRepo: scan languages: %w", err)
	}

	mode := ModeGreenfield
	var contexts []BoundedContext
	if hasSource {
		mode = ModeBrownfield
		contexts, err = scanBrownfieldContexts(projectDir)
		if err != nil {
			return nil, fmt.Errorf("ProfileRepo: scan contexts: %w", err)
		}
	}

	roles := make([]string, len(FixedDiamondRoles))
	copy(roles, FixedDiamondRoles)

	return &ProjectProfile{
		Mode:            mode,
		BoundedContexts: contexts,
		Languages:       languages,
		DiamondRoles:    roles,
		projectDir:      projectDir,
	}, nil
}

// scanLanguages walks the project tree (skipping the dirsToSkipForProfile
// set) and returns the deduplicated, lexicographically sorted set of
// detected language ids. hasSource reports whether any recognized
// source file was found at all (drives greenfield vs brownfield).
func scanLanguages(projectDir string) ([]string, bool, error) {
	seen := make(map[string]struct{})
	err := filepath.WalkDir(projectDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path == projectDir {
				return nil
			}
			if _, skip := dirsToSkipForProfile[d.Name()]; skip {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if lang, ok := fileExtensionToLanguage[ext]; ok {
			seen[lang] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	out := make([]string, 0, len(seen))
	for lang := range seen {
		out = append(out, lang)
	}
	sort.Strings(out)
	return out, len(out) > 0, nil
}

// scanBrownfieldContexts proposes one bounded context per directory
// at projectDir/src/<name>/. If src/ is absent or empty, returns nil
// (the operator must declare contexts manually even in brownfield —
// directory-layout convention is a hint, not a requirement).
//
// Contexts are returned in lexicographic order by ID for determinism.
func scanBrownfieldContexts(projectDir string) ([]BoundedContext, error) {
	srcDir := filepath.Join(projectDir, "src")
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []BoundedContext
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Skip hidden / system directories under src/.
		if strings.HasPrefix(name, ".") {
			continue
		}
		out = append(out, BoundedContext{
			ID:          name,
			Description: fmt.Sprintf("Proposed from src/%s/", name),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// DeclareContext adds a bounded context to the profile. Used in
// greenfield mode (where the operator supplies all contexts) or in
// brownfield mode to add a context the directory scan didn't find
// (e.g., a context spanning multiple directories).
//
// The id must be non-empty after trimming and must not duplicate an
// existing context's id. id must contain only ASCII letters, digits,
// hyphens, and underscores — the bounded-context-id type in the
// catalogue (gates.md §E).
func (p *ProjectProfile) DeclareContext(id, description string) error {
	if p == nil {
		return errors.New("DeclareContext: nil ProjectProfile")
	}
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return ErrProfileContextEmpty
	}
	if !isValidContextID(trimmed) {
		return fmt.Errorf("%w: %q", ErrProfileContextInvalid, trimmed)
	}
	for _, c := range p.BoundedContexts {
		if c.ID == trimmed {
			return fmt.Errorf("%w: %q", ErrProfileContextDup, trimmed)
		}
	}
	p.BoundedContexts = append(p.BoundedContexts, BoundedContext{
		ID:          trimmed,
		Description: description,
	})
	return nil
}

// isValidContextID enforces the bounded-context-id format: non-empty
// string of ASCII letters, digits, hyphens, and underscores. Must
// start with a letter (so it can be used as an identifier in
// downstream artifacts without escaping).
func isValidContextID(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		case r == '-' || r == '_':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// NeedsContextInterrogation reports whether init must interrogate the
// operator to identify bounded contexts. True in greenfield (always)
// and true in brownfield if the directory scan returned no contexts.
func (p *ProjectProfile) NeedsContextInterrogation() bool {
	if p == nil {
		return false
	}
	return len(p.BoundedContexts) == 0
}

// IsGreenfield reports whether the profile's mode is greenfield.
func (p *ProjectProfile) IsGreenfield() bool {
	return p != nil && p.Mode == ModeGreenfield
}

// IsBrownfield reports whether the profile's mode is brownfield.
func (p *ProjectProfile) IsBrownfield() bool {
	return p != nil && p.Mode == ModeBrownfield
}
