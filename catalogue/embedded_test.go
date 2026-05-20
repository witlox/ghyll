package catalogue

import (
	"bytes"
	"io/fs"
	"path"
	"strings"
	"testing"

	ghyll "github.com/witlox/ghyll"
	"gopkg.in/yaml.v3"
)

// TestLoadEmbedded_AllSchemasParseAndNameMatchesFilename walks the
// embedded ghyll.ConceptsFS directly and asserts:
//   - the embedded set is non-empty (we shipped something)
//   - every *.yaml entry parses with the strict YAML decoder
//   - the parsed concept name matches the filename stem
//
// This is the binary-layer counterpart to TestLoad_AllShippedConceptsPresent:
// it proves that what's embedded into the binary is the same well-formed
// closed vocabulary the disk-backed loader validates, so a release binary
// run outside the source checkout still has a consistent catalogue
// (integrator finding H-1).
func TestLoadEmbedded_AllSchemasParseAndNameMatchesFilename(t *testing.T) {
	entries, err := fs.ReadDir(ghyll.ConceptsFS, ghyll.ConceptsDir)
	if err != nil {
		t.Fatalf("fs.ReadDir(ghyll.ConceptsFS, %q): %v", ghyll.ConceptsDir, err)
	}

	yamlCount := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		yamlCount++

		data, err := fs.ReadFile(ghyll.ConceptsFS, path.Join(ghyll.ConceptsDir, entry.Name()))
		if err != nil {
			t.Errorf("fs.ReadFile(%q): %v", entry.Name(), err)
			continue
		}

		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		var c Concept
		if err := dec.Decode(&c); err != nil {
			t.Errorf("parse %q: %v", entry.Name(), err)
			continue
		}

		if c.Name == "" {
			t.Errorf("%q: parsed concept has empty name", entry.Name())
			continue
		}
		want := c.Name + ".yaml"
		if entry.Name() != want {
			t.Errorf("%q: concept name %q implies filename %q", entry.Name(), c.Name, want)
		}
	}

	if yamlCount == 0 {
		t.Fatal("embedded ghyll.ConceptsFS contains zero *.yaml entries; build is shipping an empty catalogue")
	}
}

// TestLoadEmbedded_ReturnsShippedCatalogue is the higher-level
// integration check: LoadEmbedded() must produce a Catalogue whose
// concept names exactly match the closed vocabulary in expectedConcepts.
// This guards against drift between what's on disk in the source
// checkout and what's compiled into the binary.
func TestLoadEmbedded_ReturnsShippedCatalogue(t *testing.T) {
	cat, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("LoadEmbedded() failed: %v", err)
	}
	if got, want := cat.Count(), len(expectedConcepts); got != want {
		t.Errorf("LoadEmbedded().Count() = %d; want %d", got, want)
	}
	for _, name := range expectedConcepts {
		if _, ok := cat.Get(name); !ok {
			t.Errorf("LoadEmbedded() missing concept %q", name)
		}
	}
}
