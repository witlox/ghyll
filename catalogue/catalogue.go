package catalogue

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// MaxSchemaFileSize is the largest catalogue YAML file we'll read.
// 256 KB is well above the size of any well-formed concept schema
// (the largest shipped schema is ~50 lines / ~2 KB) and below the
// threshold where a hostile file could exhaust memory
// (validation-pass-1 finding #8).
const MaxSchemaFileSize = 256 * 1024

// Catalogue is the loaded set of concept schemas. Construct via Load.
// The concepts map is populated by Load and then never written to,
// so concurrent reads via Get/List/Count/Validate are safe by
// construction (validation-pass-1 finding #21). If Catalogue ever
// gains a mutation API, a sync.RWMutex must guard the map.
type Catalogue struct {
	concepts map[string]Concept
}

// universalBaseSet is the set of concepts auto-applied to every arrow
// per gates.md §5.2 (ADR-005, ADR-006).
var universalBaseSet = map[string]struct{}{
	"compiles":         {},
	"lint-clean":       {},
	"no-todo-marker":   {},
	"every-step-bound": {},
}

// autoInsertedSet is the set of concepts auto-inserted on adversarial
// arrows during verification per gates.md §11.3 (D26, D33).
var autoInsertedSet = map[string]struct{}{
	"no-open-finding":                   {},
	"every-requirement-meets-min-depth": {},
}

// IsUniversalBase reports whether the named concept is auto-applied
// to every arrow. See gates.md §5.2.
func IsUniversalBase(name string) bool {
	_, ok := universalBaseSet[name]
	return ok
}

// IsAutoInserted reports whether the named concept is auto-inserted
// by the verification phase on adversarial arrows. See gates.md §11.3.
func IsAutoInserted(name string) bool {
	_, ok := autoInsertedSet[name]
	return ok
}

// Load reads every *.yaml file in dir as a Concept, validates
// self-consistency, and returns the Catalogue.
//
// Validation:
//   - Files are stat'd via Lstat (no symlink follow per
//     validation-pass-1 finding #8); symlinks in the catalogue
//     directory are rejected outright. Catalogue data is harness-
//     owned; symlinks should never appear.
//   - Files exceeding MaxSchemaFileSize are rejected (memory-bomb guard).
//   - YAML is parsed in strict mode (yaml.v3 Decoder with
//     KnownFields(true)); unknown fields or duplicate keys are
//     rejected (validation-pass-1 finding #7).
//   - A concept's name field must be non-empty.
//   - The filename must equal "<concept-name>.yaml".
//   - Evaluator field must be present (validation-pass-1 finding #6).
//   - Evaluator.Contract must be the literal string "machine".
//   - Two files cannot declare the same concept name.
func Load(dir string) (*Catalogue, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("catalogue: read dir %q: %w", dir, err)
	}

	cat := &Catalogue{concepts: make(map[string]Concept)}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, entry.Name())

		// Lstat (no symlink follow). Symlinks in the catalogue
		// directory could redirect to arbitrary files (e.g.,
		// /etc/passwd) and leak the path back through a YAML
		// parse-error message.
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("catalogue: lstat %q: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("catalogue: %q is a symlink (refused; catalogue data is harness-owned)", path)
		}
		if info.Size() > MaxSchemaFileSize {
			return nil, fmt.Errorf("catalogue: %q exceeds max size %d bytes (got %d)",
				path, MaxSchemaFileSize, info.Size())
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("catalogue: read %q: %w", path, err)
		}

		c, err := parseStrictYAML(data)
		if err != nil {
			return nil, fmt.Errorf("catalogue: parse %q: %w", path, err)
		}

		if c.Name == "" {
			return nil, fmt.Errorf("catalogue: %q has empty concept name", path)
		}
		expected := c.Name + ".yaml"
		if entry.Name() != expected {
			return nil, fmt.Errorf("catalogue: %q has concept name %q but filename should be %q", path, c.Name, expected)
		}
		// Distinguish missing-evaluator from non-machine-contract
		// (validation-pass-1 finding #6). Zero-value EvaluatorContract
		// has empty Contract and nil Produces map; that's the missing
		// case. A present-but-wrong contract is the other case.
		if c.Evaluator.Contract == "" && c.Evaluator.Produces == nil {
			return nil, fmt.Errorf("catalogue: %q is missing the evaluator: section", path)
		}
		if c.Evaluator.Contract != "machine" {
			return nil, fmt.Errorf("catalogue: %q has unsupported evaluator contract %q (only \"machine\" supported)", path, c.Evaluator.Contract)
		}
		if _, ok := cat.concepts[c.Name]; ok {
			return nil, fmt.Errorf("catalogue: duplicate concept name %q (second file: %q)", c.Name, path)
		}
		cat.concepts[c.Name] = c
	}

	return cat, nil
}

// parseStrictYAML decodes a concept schema with yaml.v3 in strict mode:
// unknown fields are rejected (KnownFields). Duplicate keys at the
// top level are also detected. validation-pass-1 finding #7.
func parseStrictYAML(data []byte) (Concept, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var c Concept
	if err := dec.Decode(&c); err != nil {
		return Concept{}, err
	}
	// Reject any trailing documents — concept files must contain
	// exactly one YAML document.
	err := dec.Decode(&Concept{})
	switch {
	case err == nil:
		return Concept{}, errors.New("multiple YAML documents in one file (expected exactly one)")
	case errors.Is(err, io.EOF):
		// Single document — expected.
	default:
		return Concept{}, fmt.Errorf("trailing YAML content: %w", err)
	}
	return c, nil
}

// Get returns the named concept and true, or a zero Concept and false
// if no concept by that name is loaded.
func (c *Catalogue) Get(name string) (Concept, bool) {
	concept, ok := c.concepts[name]
	return concept, ok
}

// List returns the names of all loaded concepts in lexicographic order.
func (c *Catalogue) List() []string {
	names := make([]string, 0, len(c.concepts))
	for name := range c.concepts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Count returns the number of loaded concepts.
func (c *Catalogue) Count() int {
	return len(c.concepts)
}
