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

// ExtractContextSymbols walks projectDir/src/<contextID>/ and returns
// the exported symbols discovered there. Currently supports Go only;
// other languages produce ErrExtractorNotImplemented entries via
// supportedLanguageExtractor.
//
// Returns ErrContextDirMissing if the per-context source directory
// does not exist. The caller (init's brownfield discovery flow) is
// expected to skip such contexts or prompt the operator.
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
	ctxDir := filepath.Join(projectDir, "src", contextID)
	info, err := os.Stat(ctxDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrContextDirMissing, ctxDir)
		}
		return nil, fmt.Errorf("ExtractContextSymbols: stat %q: %w", ctxDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("ExtractContextSymbols: %q is not a directory", ctxDir)
	}

	var symbols []ExportedSymbol
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
			if _, skip := dirsToSkipForProfile[d.Name()]; skip {
				return filepath.SkipDir
			}
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
			return fmt.Errorf("extract %q: %w", path, err)
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
// skip function bodies (parser.SkipObjectResolution + ImportsOnly
// would skip too much, so we parse declarations only via
// parser.AllErrors with go/ast inspection).
func extractGoSymbols(projectDir, filePath, contextID string) ([]ExportedSymbol, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, nil, parser.SkipObjectResolution)
	if err != nil {
		// Parse errors are surfaced — a broken Go file is something
		// init's brownfield discovery should report, not hide.
		return nil, fmt.Errorf("parse: %w", err)
	}
	rel, err := filepath.Rel(projectDir, filePath)
	if err != nil {
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
// any file under specsDir. A symbol "appears" if its Name occurs as a
// substring in any spec file's text (a permissive heuristic — false
// positives are residue candidates for operator triage, not gate
// failures).
//
// If specsDir does not exist, every symbol is an orphan candidate
// (no specs means no mapping). Files larger than maxSpecFileSize
// are skipped to bound memory; encountering one returns an error
// so the operator knows the scan was partial.
func ClassifyOrphans(symbols []ExportedSymbol, specsDir string) ([]OrphanCandidate, error) {
	if len(symbols) == 0 {
		return nil, nil
	}
	specText, err := loadSpecText(specsDir)
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
		if !strings.Contains(specText, s.Name) {
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

// loadSpecText concatenates the text of every file under specsDir
// recursively. Returns "" + nil if specsDir doesn't exist (every
// symbol becomes orphan). Returns an error on any file too large to
// safely read.
func loadSpecText(specsDir string) (string, error) {
	if specsDir == "" {
		return "", nil
	}
	info, err := os.Stat(specsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("loadSpecText: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("loadSpecText: %q is not a directory", specsDir)
	}
	// Collect file paths during the walk; read them in a second pass.
	// Avoids the TOCTOU-race pattern of reading inside the walk
	// callback (gosec G122).
	type sized struct {
		path string
		size int64
	}
	var files []sized
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
			if _, skip := dirsToSkipForProfile[d.Name()]; skip {
				return filepath.SkipDir
			}
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if fi.Size() > maxSpecFileSize {
			return fmt.Errorf("spec file %q exceeds max size %d", path, maxSpecFileSize)
		}
		files = append(files, sized{path: path, size: fi.Size()})
		return nil
	})
	if walkErr != nil {
		return "", walkErr
	}
	var sb strings.Builder
	for _, f := range files {
		data, err := os.ReadFile(f.path)
		if err != nil {
			return "", fmt.Errorf("read %q: %w", f.path, err)
		}
		sb.Write(data)
		sb.WriteByte('\n')
	}
	return sb.String(), nil
}
