package catalogue

import (
	"fmt"
	"io/fs"
	"path"
	"strings"

	ghyll "github.com/witlox/ghyll"
)

// LoadEmbedded returns a Catalogue populated from the concept schemas
// embedded into the binary at build time (ghyll.ConceptsFS). This is
// the production entry point for the closed concept vocabulary — it
// removes the runtime dependency on the source checkout's on-disk
// layout (integrator finding H-1) and is what `ghyll init` and the
// runner should call at session start.
//
// Validation is identical to Load except for the symlink check, which
// is unnecessary: an embed.FS is sealed at compile time and cannot
// contain symlinks. The size guard is still applied as defense in
// depth in case future tooling adds large files to the embedded set.
//
// Custom-data scenarios (loading bespoke schemas from a tempdir for
// tests, ad-hoc operator tooling, etc.) should keep using Load.
func LoadEmbedded() (*Catalogue, error) {
	return loadFromFS(ghyll.ConceptsFS, ghyll.ConceptsDir)
}

// loadFromFS is the fs.FS-shaped sibling of Load. It is the loader
// used by LoadEmbedded and is suitable for synthetic-FS tests
// (fstest.MapFS) that want to exercise the parser without writing to
// disk.
//
// Unlike Load, this path does not perform a symlink check: fs.FS has
// no symlink concept at the interface level, and the only production
// caller is an embed.FS that cannot contain them.
func loadFromFS(fsys fs.FS, dir string) (*Catalogue, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("catalogue: read embedded dir %q: %w", dir, err)
	}

	cat := &Catalogue{concepts: make(map[string]Concept)}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		fp := path.Join(dir, entry.Name())

		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("catalogue: stat %q: %w", fp, err)
		}
		if info.Size() > MaxSchemaFileSize {
			return nil, fmt.Errorf("catalogue: %q exceeds max size %d bytes (got %d)",
				fp, MaxSchemaFileSize, info.Size())
		}

		data, err := fs.ReadFile(fsys, fp)
		if err != nil {
			return nil, fmt.Errorf("catalogue: read %q: %w", fp, err)
		}

		c, err := parseStrictYAML(data)
		if err != nil {
			return nil, fmt.Errorf("catalogue: parse %q: %w", fp, err)
		}

		if c.Name == "" {
			return nil, fmt.Errorf("catalogue: %q has empty concept name", fp)
		}
		expected := c.Name + ".yaml"
		if entry.Name() != expected {
			return nil, fmt.Errorf("catalogue: %q has concept name %q but filename should be %q", fp, c.Name, expected)
		}
		// Distinguish missing-evaluator from non-machine-contract
		// (validation-pass-1 finding #6). Zero-value EvaluatorContract
		// has empty Contract and nil Produces map; that's the missing
		// case. A present-but-wrong contract is the other case.
		if c.Evaluator.Contract == "" && c.Evaluator.Produces == nil {
			return nil, fmt.Errorf("catalogue: %q is missing the evaluator: section", fp)
		}
		if c.Evaluator.Contract != "machine" {
			return nil, fmt.Errorf("catalogue: %q has unsupported evaluator contract %q (only \"machine\" supported)", fp, c.Evaluator.Contract)
		}
		if _, ok := cat.concepts[c.Name]; ok {
			return nil, fmt.Errorf("catalogue: duplicate concept name %q (second file: %q)", c.Name, fp)
		}
		cat.concepts[c.Name] = c
	}

	return cat, nil
}
