package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/text/unicode/norm"

	"github.com/witlox/ghyll/internal/skipdirs"
)

// MaxProfileWalkFiles bounds the per-ProfileRepo walk so a
// pathological repo (millions of files, accidentally-checked-in
// dataset) cannot stall init indefinitely. Validation-pass-2 F11.
const MaxProfileWalkFiles = 100_000

// MaxBoundedContexts bounds the number of bounded contexts a single
// profile can hold. Validation-pass-2 F38: prevents O(n²) dup-scan
// on DeclareContext and unbounded YAML output.
const MaxBoundedContexts = 256

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
//
// ProjectProfile is safe for concurrent use. All mutation paths
// (DeclareContext / ProposeRefusal / AcceptRefusal / OverrideRefusal)
// and all readers go through the internal mutex
// (validation-pass-2 F9). Callers may observe a consistent
// point-in-time view via the typed accessors while another goroutine
// is mutating.
type ProjectProfile struct {
	// Public fields are populated once at construction (ProfileRepo)
	// and effectively immutable for Mode, Languages, DiamondRoles.
	// BoundedContexts is the exception — DeclareContext appends to
	// it; readers should use BoundedContextsSnapshot() to get a
	// race-safe view.
	Mode            Mode
	BoundedContexts []BoundedContext
	Languages       []string
	DiamondRoles    []string

	// projectDir is retained so further interrogation steps (orphan
	// extraction, language confirmation) can re-scan without the
	// caller threading it back through.
	projectDir string

	// mu guards BoundedContexts, risk, refusal. All exported methods
	// that read or write any of these acquire mu.
	mu sync.Mutex

	// risk + refusal are set by ProposeRefusal / AcceptRefusal /
	// OverrideRefusal in risk.go. Kept unexported so callers reach
	// them through the typed accessors.
	risk    RiskAssessment
	refusal *RefusalOutcome
}

// BoundedContextsSnapshot returns a deep copy of the current
// bounded-context list. Race-safe alternative to reading the public
// slice directly. Validation-pass-2 F9.
func (p *ProjectProfile) BoundedContextsSnapshot() []BoundedContext {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]BoundedContext, len(p.BoundedContexts))
	copy(out, p.BoundedContexts)
	return out
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

// dirsToSkipForProfile delegates to internal/skipdirs.IsSourceWalkSkip.
// Validation-pass-2 F39 unified the previously-duplicated sets across
// bootstrap and runner.
func dirsToSkipForProfile(name string) bool {
	return skipdirs.IsSourceWalkSkip(name)
}

// Profile errors.
var (
	ErrProfileNilDir         = errors.New("profile-project-dir-empty")
	ErrProfileContextEmpty   = errors.New("profile-context-id-empty")
	ErrProfileContextDup     = errors.New("profile-context-id-duplicate")
	ErrProfileContextInvalid = errors.New("profile-context-id-invalid")
)

// Profile errors (in addition to the ones declared elsewhere).
var (
	ErrProfileWalkBudgetExceeded = errors.New("profile-walk-budget-exceeded")
	ErrProfileTooManyContexts    = errors.New("profile-too-many-bounded-contexts")
)

// ProfileRepo scans projectDir and returns a profile describing the
// repo state at init time.
//
// Wraps ProfileRepoContext with context.Background. Prefer
// ProfileRepoContext from callers that have a context already.
func ProfileRepo(projectDir string) (*ProjectProfile, error) {
	return ProfileRepoContext(context.Background(), projectDir)
}

// ProfileRepoContext is the context-aware variant.
//
// Mode is determined by whether the directory contains any
// recognized source code:
//
//   - Brownfield: at least one source file exists (.go, .py, .rs,
//     .ts, etc.) anywhere outside the skip-dir set. Contexts are
//     proposed from src/<id>/ subdirectories. Languages reflects the
//     observed file extensions.
//   - Greenfield: no source files found. BoundedContexts and Languages
//     are empty; the operator supplies them via DeclareContext.
//
// ProfileRepoContext does not write to disk or invoke external
// commands; it is read-only over the directory tree.
//
// File-count cap: the walk visits at most MaxProfileWalkFiles entries
// before returning ErrProfileWalkBudgetExceeded — protects against
// pathological repos (dataset trees, accidentally-checked-in
// node_modules outside the skip list). Validation-pass-2 F11.
//
// Symlinks: per-entry symlinks in src/ are refused so that a malicious
// or careless `src/foo -> /etc` does not become a proposed bounded
// context. Validation-pass-2 F8.
func ProfileRepoContext(ctx context.Context, projectDir string) (*ProjectProfile, error) {
	if projectDir == "" {
		return nil, ErrProfileNilDir
	}
	info, err := os.Lstat(projectDir)
	if err != nil {
		return nil, fmt.Errorf("ProfileRepoContext: lstat %q: %w", projectDir, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("ProfileRepoContext: %q is a symlink (refused)", projectDir)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("ProfileRepoContext: %q is not a directory", projectDir)
	}

	languages, hasSource, err := scanLanguages(ctx, projectDir)
	if err != nil {
		return nil, fmt.Errorf("ProfileRepoContext: scan languages: %w", err)
	}

	mode := ModeGreenfield
	var contexts []BoundedContext
	if hasSource {
		mode = ModeBrownfield
		contexts, err = scanBrownfieldContexts(projectDir)
		if err != nil {
			return nil, fmt.Errorf("ProfileRepoContext: scan contexts: %w", err)
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
//
// Per validation-pass-2 F11: respects ctx.Cancel and caps the walk at
// MaxProfileWalkFiles entries.
func scanLanguages(ctx context.Context, projectDir string) ([]string, bool, error) {
	seen := make(map[string]struct{})
	visited := 0
	err := filepath.WalkDir(projectDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		visited++
		if visited > MaxProfileWalkFiles {
			return fmt.Errorf("%w: visited %d (cap %d) under %q",
				ErrProfileWalkBudgetExceeded, visited, MaxProfileWalkFiles, projectDir)
		}
		if d.IsDir() {
			if path == projectDir {
				return nil
			}
			if dirsToSkipForProfile(d.Name()) {
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
// Validation-pass-2 F8: rejects symlinks. A `src/foo -> /etc` would
// otherwise propose a bounded context named "foo" pointing outside
// the project tree.
//
// Validation-pass-2 F33: applies isValidContextID to each directory
// name; names that don't pass (spaces, leading digits, Unicode that
// might confuse downstream display layers) are silently skipped.
// The operator can DeclareContext explicitly to add a context the
// auto-scan rejected.
//
// Post-prod-readiness adversarial M-A: enforces MaxBoundedContexts
// at scan time so a pathological repo with thousands of src/<n>/
// directories cannot synthesize an unbounded grid. Mirrors the cap
// DeclareContext enforces on the operator-driven path. Returns
// ErrProfileTooManyContexts when exceeded.
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
		// Lstat to refuse symlinks (DirEntry.Type reflects lstat,
		// not stat).
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Skip hidden / system directories under src/.
		if strings.HasPrefix(name, ".") {
			continue
		}
		// Skip names that don't satisfy bounded-context-id format.
		// Silent skip is intentional: the operator may have
		// intentionally placed an unrelated tool dir under src/.
		if !isValidContextID(name) {
			continue
		}
		out = append(out, BoundedContext{
			ID:          name,
			Description: fmt.Sprintf("Proposed from src/%s/", name),
		})
		if len(out) > MaxBoundedContexts {
			return nil, fmt.Errorf("%w: scanned %d candidates under %s (cap %d)",
				ErrProfileTooManyContexts, len(out), srcDir, MaxBoundedContexts)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// DeclareContext adds a bounded context to the profile. Used in
// greenfield mode (where the operator supplies all contexts) or in
// brownfield mode to add a context the directory scan didn't find
// (e.g., a context spanning multiple directories).
//
// The id must be non-empty after trimming and NFC normalization, and
// must not duplicate an existing context's id. id must contain only
// ASCII letters, digits, hyphens, and underscores — the bounded-
// context-id type in the catalogue (gates.md §E). Validation-pass-2:
//   - F30: NFC-normalize so composed/decomposed forms canonicalize
//     before the ASCII check.
//   - F38: cap at MaxBoundedContexts entries.
//   - F9: take the profile mutex for thread safety.
func (p *ProjectProfile) DeclareContext(id, description string) error {
	if p == nil {
		return errors.New("DeclareContext: nil ProjectProfile")
	}
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return ErrProfileContextEmpty
	}
	// Validation-pass-2 F50: refuse inputs that don't already equal
	// their trimmed form. Silently normalizing " foo " → "foo"
	// surprises operators who pasted three "different" inputs and
	// got one ID.
	if trimmed != id {
		return fmt.Errorf("%w: %q has leading/trailing whitespace (use the trimmed form)",
			ErrProfileContextInvalid, id)
	}
	normalized := norm.NFC.String(trimmed)
	if !isValidContextID(normalized) {
		return fmt.Errorf("%w: %q (bounded-context-id must match [a-zA-Z][a-zA-Z0-9_-]*)",
			ErrProfileContextInvalid, normalized)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.BoundedContexts) >= MaxBoundedContexts {
		return fmt.Errorf("%w: cap %d", ErrProfileTooManyContexts, MaxBoundedContexts)
	}
	for _, c := range p.BoundedContexts {
		if c.ID == normalized {
			return fmt.Errorf("%w: %q", ErrProfileContextDup, normalized)
		}
	}
	p.BoundedContexts = append(p.BoundedContexts, BoundedContext{
		ID:          normalized,
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
