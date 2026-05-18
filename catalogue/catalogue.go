package catalogue

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Catalogue is the loaded set of concept schemas. Construct via Load.
// Catalogue is immutable after construction.
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
// self-consistency (filename matches concept name, no duplicates,
// required fields present), and returns the Catalogue.
//
// Errors:
//   - The directory cannot be read.
//   - Any file fails to parse as YAML.
//   - A concept's name field is empty or doesn't match its filename.
//   - Two files declare the same concept name.
//   - A concept's Evaluator.Contract is not "machine" (the only
//     supported contract per ADR-006).
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
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("catalogue: read %q: %w", path, err)
		}
		var c Concept
		if err := yaml.Unmarshal(data, &c); err != nil {
			return nil, fmt.Errorf("catalogue: parse %q: %w", path, err)
		}
		if c.Name == "" {
			return nil, fmt.Errorf("catalogue: %q has empty concept name", path)
		}
		expected := c.Name + ".yaml"
		if entry.Name() != expected {
			return nil, fmt.Errorf("catalogue: %q has concept name %q but filename should be %q", path, c.Name, expected)
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
