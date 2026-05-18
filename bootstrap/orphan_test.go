package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractContextSymbols_GoFile(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "src/contextA/main.go", `package contextA

// Exported function.
func ExportedFunc() {}

// Unexported function — should NOT be reported.
func unexportedFunc() {}

// Exported type.
type ExportedType struct{}

// Exported var + const.
var ExportedVar = 42
const ExportedConst = "hi"

// Method on exported type.
func (e *ExportedType) ExportedMethod() {}
`)

	symbols, err := ExtractContextSymbols(dir, "contextA")
	if err != nil {
		t.Fatalf("ExtractContextSymbols: %v", err)
	}
	want := map[string]string{
		"ExportedFunc":   "func",
		"ExportedType":   "type",
		"ExportedVar":    "var",
		"ExportedConst":  "const",
		"ExportedMethod": "method",
	}
	if len(symbols) != len(want) {
		t.Fatalf("len(symbols) = %d; want %d: %+v", len(symbols), len(want), symbols)
	}
	for _, s := range symbols {
		gotKind, ok := want[s.Name]
		if !ok {
			t.Errorf("unexpected symbol %q (%s)", s.Name, s.Kind)
			continue
		}
		if s.Kind != gotKind {
			t.Errorf("%s.Kind = %q; want %q", s.Name, s.Kind, gotKind)
		}
		if s.Language != "go" {
			t.Errorf("%s.Language = %q; want go", s.Name, s.Language)
		}
		if s.Context != "contextA" {
			t.Errorf("%s.Context = %q; want contextA", s.Name, s.Context)
		}
	}
}

func TestExtractContextSymbols_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "src/contextA/a.go", `package contextA
func AlphaFunc() {}`)
	mustWrite(t, dir, "src/contextA/sub/b.go", `package sub
func BetaFunc() {}`)
	symbols, err := ExtractContextSymbols(dir, "contextA")
	if err != nil {
		t.Fatal(err)
	}
	if len(symbols) != 2 {
		t.Fatalf("len = %d; want 2", len(symbols))
	}
	// Sorted by file path; a.go before sub/b.go.
	if symbols[0].Name != "AlphaFunc" {
		t.Errorf("symbols[0].Name = %q; want AlphaFunc", symbols[0].Name)
	}
	if symbols[1].Name != "BetaFunc" {
		t.Errorf("symbols[1].Name = %q; want BetaFunc", symbols[1].Name)
	}
}

func TestExtractContextSymbols_SkipsBuildDirs(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "src/contextA/main.go", `package contextA
func RealFunc() {}`)
	mustWrite(t, dir, "src/contextA/vendor/lib/lib.go", `package lib
func VendorFunc() {}`)
	mustWrite(t, dir, "src/contextA/node_modules/pkg/foo.go", `package pkg
func NodeFunc() {}`)
	symbols, err := ExtractContextSymbols(dir, "contextA")
	if err != nil {
		t.Fatal(err)
	}
	if len(symbols) != 1 {
		t.Fatalf("len = %d; want 1 (vendor + node_modules should be skipped)", len(symbols))
	}
	if symbols[0].Name != "RealFunc" {
		t.Errorf("symbols[0].Name = %q", symbols[0].Name)
	}
}

func TestExtractContextSymbols_UnrecognizedExtension(t *testing.T) {
	// .txt is not a recognized source extension; should be ignored.
	dir := t.TempDir()
	mustWrite(t, dir, "src/contextA/notes.txt", "ExportedThing")
	mustWrite(t, dir, "src/contextA/main.go", `package contextA
func RealFunc() {}`)
	symbols, err := ExtractContextSymbols(dir, "contextA")
	if err != nil {
		t.Fatal(err)
	}
	if len(symbols) != 1 || symbols[0].Name != "RealFunc" {
		t.Errorf("symbols = %+v; want only RealFunc", symbols)
	}
}

func TestExtractContextSymbols_RecognizedLanguageNoExtractor(t *testing.T) {
	// .py is recognized but no extractor yet. Should surface a
	// language-gap marker, not silently drop.
	dir := t.TempDir()
	mustWrite(t, dir, "src/contextA/main.py", `def some_func(): pass`)
	symbols, err := ExtractContextSymbols(dir, "contextA")
	if err != nil {
		t.Fatal(err)
	}
	if len(symbols) != 1 {
		t.Fatalf("len = %d; want 1 (language-gap marker)", len(symbols))
	}
	if symbols[0].Kind != "language-gap" {
		t.Errorf("Kind = %q; want language-gap", symbols[0].Kind)
	}
	if symbols[0].Language != "python" {
		t.Errorf("Language = %q; want python", symbols[0].Language)
	}
}

func TestExtractContextSymbols_MissingContext(t *testing.T) {
	dir := t.TempDir()
	_, err := ExtractContextSymbols(dir, "missing")
	if !errors.Is(err, ErrContextDirMissing) {
		t.Errorf("err = %v; want ErrContextDirMissing", err)
	}
}

func TestExtractContextSymbols_EmptyArgs(t *testing.T) {
	if _, err := ExtractContextSymbols("", "x"); err == nil {
		t.Error("empty projectDir should error")
	}
	if _, err := ExtractContextSymbols("/tmp", ""); err == nil {
		t.Error("empty contextID should error")
	}
}

func TestClassifyOrphans_FindsSymbolsNotInSpecs(t *testing.T) {
	dir := t.TempDir()
	// Spec mentions ExportedFunc but not ExportedType.
	mustWrite(t, dir, "specs/architecture.md", "The ExportedFunc handles requests.")
	symbols := []ExportedSymbol{
		{Name: "ExportedFunc", Language: "go", Context: "contextA", File: "src/contextA/a.go", Kind: "func"},
		{Name: "ExportedType", Language: "go", Context: "contextA", File: "src/contextA/a.go", Kind: "type"},
	}
	specsDir := filepath.Join(dir, "specs")
	orphans, err := ClassifyOrphans(symbols, specsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 {
		t.Fatalf("orphans len = %d; want 1 (only ExportedType)", len(orphans))
	}
	if orphans[0].Symbol.Name != "ExportedType" {
		t.Errorf("orphan name = %q; want ExportedType", orphans[0].Symbol.Name)
	}
	if orphans[0].Reason == "" {
		t.Error("Reason should be non-empty")
	}
}

func TestClassifyOrphans_NoSpecsDir(t *testing.T) {
	// Every symbol becomes a candidate when specsDir doesn't exist.
	symbols := []ExportedSymbol{
		{Name: "ExportedFunc", Language: "go", File: "src/contextA/a.go", Kind: "func"},
	}
	orphans, err := ClassifyOrphans(symbols, "/no/such/dir/ever")
	if err != nil {
		t.Fatalf("missing specsDir should not error; got %v", err)
	}
	if len(orphans) != 1 {
		t.Errorf("orphans len = %d; want 1 (no specs → all orphan)", len(orphans))
	}
}

func TestClassifyOrphans_LanguageGapMarker(t *testing.T) {
	// language-gap markers are themselves residue candidates.
	dir := t.TempDir()
	mustWrite(t, dir, "specs/foo.md", "nothing")
	symbols := []ExportedSymbol{
		{Name: "(extractor-not-implemented)", Language: "python", Kind: "language-gap"},
	}
	orphans, err := ClassifyOrphans(symbols, filepath.Join(dir, "specs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 {
		t.Fatalf("len = %d; want 1", len(orphans))
	}
	if orphans[0].Symbol.Kind != "language-gap" {
		t.Errorf("Kind = %q; want language-gap", orphans[0].Symbol.Kind)
	}
}

func TestClassifyOrphans_EmptyInput(t *testing.T) {
	orphans, err := ClassifyOrphans(nil, "/anywhere")
	if err != nil {
		t.Fatal(err)
	}
	if orphans != nil {
		t.Errorf("orphans = %v; want nil for empty input", orphans)
	}
}

func TestClassifyOrphans_TooLargeSpecFile(t *testing.T) {
	dir := t.TempDir()
	// Write a file just over the limit.
	big := make([]byte, maxSpecFileSize+1)
	for i := range big {
		big[i] = 'a'
	}
	if err := os.MkdirAll(filepath.Join(dir, "specs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "specs", "huge.md"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	symbols := []ExportedSymbol{{Name: "X", Language: "go", Kind: "func"}}
	_, err := ClassifyOrphans(symbols, filepath.Join(dir, "specs"))
	if err == nil {
		t.Error("expected error for oversized spec file")
	}
}

func TestExtractGoSymbols_ParseError(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "src/contextA/broken.go", `package contextA func {`)
	_, err := ExtractContextSymbols(dir, "contextA")
	if err == nil {
		t.Error("expected parse error to propagate")
	}
}
