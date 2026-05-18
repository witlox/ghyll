package bootstrap

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/witlox/ghyll/internal/skipdirs"
)

// Orphan-symbol extraction. Per init.feature scenario 41 (brownfield
// init):
//
//   Given a brownfield init with declared bounded contexts
//   When init walks each context's source
//   Then init extracts the exported-symbol list per language
//   And presents orphans (symbols with no clear spec mapping) as
//       residue candidates for operator triage.
//
// v1 ships a Go extractor (via go/parser). Other languages return
// ErrExtractorNotImplemented — the operator declares what to do
// (skip the context, mark all symbols as orphan candidates wholesale,
// or wait for a future per-language extractor).
//
// Orphan classification is a naive substring scan over the project's
// specs/ directory: a symbol that does not appear in any spec file is
// an orphan candidate. The operator triages — this surface is
// intentionally permissive (false-positive orphans are easier to
// resolve than false negatives).

// ExportedSymbol is one exported entity discovered in source code.
type ExportedSymbol struct {
	Name     string // identifier
	Language string // "go", "python", ... matches language-id catalogue type
	Context  string // bounded-context id (e.g., "contextA")
	File     string // path relative to projectDir
	Kind     string // "func", "type", "var", "const", "method"
}

// OrphanCandidate pairs an exported symbol with a reason it might be
// orphaned. The reason text is operator-facing.
type OrphanCandidate struct {
	Symbol ExportedSymbol
	Reason string
}

// Orphan-extraction errors.
var (
	ErrExtractorNotImplemented = errors.New("language-extractor-not-implemented")
	ErrContextDirMissing       = errors.New("context-source-dir-missing")
)

// MaxGoSourceFileSize bounds the per-file Go parser input to keep
// brownfield init from OOMing on a giant generated file
// (validation-pass-2 F23). 16 MB covers all realistic Go source.
const MaxGoSourceFileSize = 16 * 1024 * 1024

// ExtractContextSymbols walks projectDir/src/<contextID>/ and returns
// the exported symbols discovered there. Currently supports Go only;
// other languages produce ErrExtractorNotImplemented entries via
// supportedLanguageExtractor.
//
// Returns ErrContextDirMissing if the per-context source directory
// does not exist or is a symlink (validation-pass-2 F5/F8). The
// caller (init's brownfield discovery flow) is expected to skip
// such contexts or prompt the operator.
//
// Per validation-pass-2 F24: per-file extraction errors are
// accumulated into a multierror and returned alongside the
// successfully-extracted symbols. A broken Go file no longer
// aborts the entire context scan.
//
// Symbol order is deterministic: by file path, then by source position
// within each file.
func ExtractContextSymbols(projectDir, contextID string) ([]ExportedSymbol, error) {
	if projectDir == "" {
		return nil, errors.New("ExtractContextSymbols: projectDir empty")
	}
	if contextID == "" {
		return nil, errors.New("ExtractContextSymbols: contextID empty")
	}
	if !isValidContextID(contextID) {
		return nil, fmt.Errorf("ExtractContextSymbols: invalid contextID %q", contextID)
	}
	ctxDir := filepath.Join(projectDir, "src", contextID)
	info, err := os.Lstat(ctxDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrContextDirMissing, ctxDir)
		}
		return nil, fmt.Errorf("ExtractContextSymbols: lstat %q: %w", ctxDir, err)
	}
	// Validation-pass-2 F5: refuse a symlinked context directory.
	// `src/contextA -> /etc` would otherwise walk the host FS.
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: %q is a symlink (refused for containment)",
			ErrContextDirMissing, ctxDir)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("ExtractContextSymbols: %q is not a directory", ctxDir)
	}

	var symbols []ExportedSymbol
	var fileErrs []error
	walkErr := filepath.WalkDir(ctxDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			// Skip the same harness-owned and build-output dirs as
			// ProfileRepo (avoids descending into .git/vendor/etc.).
			if path == ctxDir {
				return nil
			}
			if dirsToSkipForProfile(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip symlinks within the context tree.
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		lang, recognized := fileExtensionToLanguage[ext]
		if !recognized {
			return nil
		}
		extractor := extractorFor(lang)
		if extractor == nil {
			// Recognized language with no extractor yet. Surface a
			// single sentinel symbol so the operator sees the gap
			// rather than silent dropping.
			rel, _ := filepath.Rel(projectDir, path)
			symbols = append(symbols, ExportedSymbol{
				Name:     "(extractor-not-implemented)",
				Language: lang,
				Context:  contextID,
				File:     rel,
				Kind:     "language-gap",
			})
			return nil
		}
		fileSymbols, err := extractor(projectDir, path, contextID)
		if err != nil {
			// Validation-pass-2 F24: accumulate per-file errors
			// rather than aborting the walk. Other files in the
			// context continue to be scanned.
			rel, _ := filepath.Rel(projectDir, path)
			fileErrs = append(fileErrs, fmt.Errorf("extract %q: %w", rel, err))
			return nil
		}
		symbols = append(symbols, fileSymbols...)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.SliceStable(symbols, func(i, j int) bool {
		if symbols[i].File != symbols[j].File {
			return symbols[i].File < symbols[j].File
		}
		return symbols[i].Name < symbols[j].Name
	})
	if len(fileErrs) > 0 {
		return symbols, errors.Join(fileErrs...)
	}
	return symbols, nil
}

// extractorFunc extracts exported symbols from a single source file.
type extractorFunc func(projectDir, filePath, contextID string) ([]ExportedSymbol, error)

// extractorFor returns the extractor for the given language id, or
// nil if no extractor is implemented yet. Currently: Go only.
func extractorFor(lang string) extractorFunc {
	switch lang {
	case "go":
		return extractGoSymbols
	}
	return nil
}

// extractGoSymbols parses a single Go source file and returns its
// exported top-level declarations. Implementation uses go/parser; we
// skip function bodies via parser.SkipObjectResolution.
//
// Validation-pass-2 F23:
//   - Refuses files larger than MaxGoSourceFileSize so a generated
//     mega-file can't OOM the parser.
//   - Wraps parser.ParseFile in a deferred recover() — historical
//     panics in go/parser on malformed UTF-8 boundaries no longer
//     abort the brownfield scan.
func extractGoSymbols(projectDir, filePath, contextID string) (out []ExportedSymbol, err error) {
	info, statErr := os.Lstat(filePath)
	if statErr != nil {
		return nil, fmt.Errorf("stat: %w", statErr)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		// Symlinks already filtered at the walk level, but defend
		// here in case a caller invokes the extractor directly.
		return nil, nil
	}
	if !info.Mode().IsRegular() {
		return nil, nil
	}
	if info.Size() > MaxGoSourceFileSize {
		return nil, fmt.Errorf("file exceeds max size %d bytes (got %d)",
			MaxGoSourceFileSize, info.Size())
	}
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("parser panicked: %v", rec)
			out = nil
		}
	}()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, nil, parser.SkipObjectResolution)
	if err != nil {
		// Parse errors are surfaced — a broken Go file is something
		// init's brownfield discovery should report, not hide.
		return nil, fmt.Errorf("parse: %w", err)
	}
	rel, relErr := filepath.Rel(projectDir, filePath)
	if relErr != nil {
		rel = filePath
	}
	var symbols []ExportedSymbol
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !d.Name.IsExported() {
				continue
			}
			kind := "func"
			if d.Recv != nil && len(d.Recv.List) > 0 {
				kind = "method"
			}
			symbols = append(symbols, ExportedSymbol{
				Name:     d.Name.Name,
				Language: "go",
				Context:  contextID,
				File:     rel,
				Kind:     kind,
			})
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						symbols = append(symbols, ExportedSymbol{
							Name:     s.Name.Name,
							Language: "go",
							Context:  contextID,
							File:     rel,
							Kind:     "type",
						})
					}
				case *ast.ValueSpec:
					kind := "var"
					if d.Tok == token.CONST {
						kind = "const"
					}
					for _, name := range s.Names {
						if name.IsExported() {
							symbols = append(symbols, ExportedSymbol{
								Name:     name.Name,
								Language: "go",
								Context:  contextID,
								File:     rel,
								Kind:     kind,
							})
						}
					}
				}
			}
		}
	}
	return symbols, nil
}

// ClassifyOrphans returns the subset of symbols that do not appear in
// any file under specsDir.
//
// Per validation-pass-2 F20: "appears" means the symbol's Name appears
// as a token (word-boundary match), not a substring — so short names
// like `Run` or `ID` don't false-negative because the letters happen
// to appear inside English prose. Tokens are extracted from spec text
// via identifier-like word boundaries.
//
// Per validation-pass-2 F6 / F19: spec text is tokenized once into a
// set and looked up O(1) per symbol — was O(N×B) before.
//
// Per validation-pass-2 F5: total spec corpus is capped at
// MaxSpecCorpusBytes (64 MB) — a hostile spec/ tree that would OOM
// the previous unbounded concatenation now returns an error.
//
// If specsDir does not exist, every symbol is an orphan candidate
// (no specs means no mapping).
func ClassifyOrphans(symbols []ExportedSymbol, specsDir string) ([]OrphanCandidate, error) {
	if len(symbols) == 0 {
		return nil, nil
	}
	tokens, err := loadSpecTokens(specsDir)
	if err != nil {
		return nil, err
	}
	out := make([]OrphanCandidate, 0)
	for _, s := range symbols {
		if s.Kind == "language-gap" {
			// Language-gap markers are themselves residue candidates —
			// init can't classify what it can't read.
			out = append(out, OrphanCandidate{
				Symbol: s,
				Reason: fmt.Sprintf("no %s extractor; operator must triage", s.Language),
			})
			continue
		}
		if _, present := tokens[s.Name]; !present {
			out = append(out, OrphanCandidate{
				Symbol: s,
				Reason: "no spec clause references this symbol",
			})
		}
	}
	return out, nil
}

// maxSpecFileSize bounds memory during the orphan scan. 1 MB is well
// above any realistic spec file and below the threshold where a
// hostile file could exhaust memory.
const maxSpecFileSize = 1 * 1024 * 1024

// MaxSpecCorpusBytes bounds the aggregate spec text the orphan
// classifier will ingest. 64 MB is comfortably above any realistic
// spec/ tree and below the threshold where a 10× larger one (10k
// files × 1MB each) would OOM the process (validation-pass-2 F5).
const MaxSpecCorpusBytes = 64 * 1024 * 1024

// loadSpecTokens scans every file under specsDir and returns the
// set of identifier-like tokens it contains. Token = a run of
// letters/digits/underscores; this matches how Go (and most
// languages) form identifiers and avoids false-negative orphans on
// symbols whose name appears as a substring inside an unrelated
// English word.
//
// Returns nil + nil if specsDir doesn't exist (every symbol becomes
// orphan). Returns an error on:
//   - A single spec file exceeding maxSpecFileSize.
//   - Aggregate corpus exceeding MaxSpecCorpusBytes (F5).
//   - Filesystem errors during walk/read.
func loadSpecTokens(specsDir string) (map[string]struct{}, error) {
	if specsDir == "" {
		return nil, nil
	}
	info, err := os.Stat(specsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("loadSpecTokens: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("loadSpecTokens: %q is not a directory", specsDir)
	}
	// Collect file paths during the walk; read them in a second pass.
	// Avoids the TOCTOU-race pattern of reading inside the walk
	// callback (gosec G122).
	type sized struct {
		path string
		size int64
	}
	var files []sized
	var totalBytes int64
	walkErr := filepath.WalkDir(specsDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			// Don't apply the skip-dir filter to the walk root itself —
			// callers can legitimately point us at a directory named
			// "specs", "docs", etc.
			if path == specsDir {
				return nil
			}
			// loadSpecTokens walks an operator-supplied specs directory;
			// IsBuildOrHarness skips just the build/harness dirs but
			// NOT spec/doc (those are precisely the dirs we want).
			if skipdirs.IsBuildOrHarness(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip symlinks (validation-pass-2 F4 — refused inner symlinks
		// so size-check on lstat isn't fooled by a small symlink to a
		// large target).
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		if fi.Size() > maxSpecFileSize {
			rel, _ := filepath.Rel(specsDir, path)
			return fmt.Errorf("spec file %q exceeds max size %d", rel, maxSpecFileSize)
		}
		totalBytes += fi.Size()
		if totalBytes > MaxSpecCorpusBytes {
			return fmt.Errorf("spec corpus exceeds max %d bytes (under %q)",
				MaxSpecCorpusBytes, specsDir)
		}
		files = append(files, sized{path: path, size: fi.Size()})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	// Tokenize as we read so we never hold the full concatenated
	// corpus in memory at once.
	tokens := make(map[string]struct{})
	for _, f := range files {
		data, err := os.ReadFile(f.path)
		if err != nil {
			rel, _ := filepath.Rel(specsDir, f.path)
			return nil, fmt.Errorf("read %q: %w", rel, err)
		}
		extractIdentifierTokens(data, tokens)
	}
	return tokens, nil
}

// extractIdentifierTokens scans data for identifier-like runs (letters,
// digits, underscores) and adds each distinct token to out. Tokens
// shorter than 2 characters are kept (so symbols like "T" aren't
// missed) — the operator triages false negatives.
func extractIdentifierTokens(data []byte, out map[string]struct{}) {
	start := -1
	add := func(s string) {
		if s != "" {
			out[s] = struct{}{}
		}
	}
	for i := 0; i < len(data); i++ {
		b := data[i]
		isIdent := (b >= 'a' && b <= 'z') ||
			(b >= 'A' && b <= 'Z') ||
			(b >= '0' && b <= '9') ||
			b == '_'
		if isIdent {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			add(string(data[start:i]))
			start = -1
		}
	}
	if start >= 0 {
		add(string(data[start:]))
	}
}
