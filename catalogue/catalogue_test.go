package catalogue

import (
	"os"
	"path/filepath"
	"testing"
)

// conceptsDir is the path to the shipped catalogue schemas relative to
// the test working directory (catalogue/).
const conceptsDir = "../gates/concepts"

// expectedConcepts is the closed set of 17 concept names per ADR-005.
// If this list changes, the harness has gained or lost a primitive
// vocabulary entry — and that requires deliberate consideration, not
// silent acceptance.
var expectedConcepts = []string{
	"acyclic-dependency-graph",
	"arrow-artifact-present",
	"cardinality-check",
	"compiles",
	"every-requirement-meets-min-depth",
	"every-step-bound",
	"kill-server-fails-integration",
	"lint-clean",
	"mode-determinable-from-repo",
	"mutation-score",
	"no-open-finding",
	"no-orphan-symbol",
	"no-todo-marker",
	"predicate-form",
	"single-active-role-instance",
	"trace-link-present",
	"unique-definition",
}

// loadShipped is a test helper for the common case of loading the
// shipped 17 concepts from gates/concepts/.
func loadShipped(t *testing.T) *Catalogue {
	t.Helper()
	cat, err := Load(conceptsDir)
	if err != nil {
		t.Fatalf("Load(%q) failed: %v", conceptsDir, err)
	}
	return cat
}

func TestLoad_ShippedConceptCount(t *testing.T) {
	cat := loadShipped(t)
	if got, want := cat.Count(), len(expectedConcepts); got != want {
		t.Errorf("Count() = %d; want %d (the closed catalogue per ADR-005)", got, want)
	}
}

func TestLoad_AllShippedConceptsPresent(t *testing.T) {
	cat := loadShipped(t)
	for _, name := range expectedConcepts {
		if _, ok := cat.Get(name); !ok {
			t.Errorf("Get(%q): concept missing from catalogue", name)
		}
	}
}

func TestList_ReturnsSortedNames(t *testing.T) {
	cat := loadShipped(t)
	names := cat.List()
	if len(names) != len(expectedConcepts) {
		t.Fatalf("List() length = %d; want %d", len(names), len(expectedConcepts))
	}
	for i, name := range names {
		if name != expectedConcepts[i] {
			t.Errorf("List()[%d] = %q; want %q (sorted order)", i, name, expectedConcepts[i])
		}
	}
}

func TestGet_UnknownConcept(t *testing.T) {
	cat := loadShipped(t)
	if _, ok := cat.Get("does-not-exist"); ok {
		t.Error("Get(\"does-not-exist\") returned ok=true; want false")
	}
}

func TestLoad_CompilesConceptDetails(t *testing.T) {
	cat := loadShipped(t)
	c, ok := cat.Get("compiles")
	if !ok {
		t.Fatal("compiles concept not loaded")
	}
	if !c.LanguageBound {
		t.Error("compiles should be language-bound")
	}
	if c.DefaultCost != 0 {
		t.Errorf("compiles default-cost = %d; want 0", c.DefaultCost)
	}
	if _, ok := c.Arguments["scope"]; !ok {
		t.Error("compiles should declare 'scope' argument")
	}
	if _, ok := c.Arguments["language"]; !ok {
		t.Error("compiles should declare 'language' argument")
	}
	if c.Evaluator.Contract != "machine" {
		t.Errorf("compiles evaluator contract = %q; want \"machine\"", c.Evaluator.Contract)
	}
}

func TestLoad_MutationScoreDefaultCost(t *testing.T) {
	cat := loadShipped(t)
	c, ok := cat.Get("mutation-score")
	if !ok {
		t.Fatal("mutation-score concept not loaded")
	}
	if c.DefaultCost != 3 {
		t.Errorf("mutation-score default-cost = %d; want 3 (per ADR-005 cost table)", c.DefaultCost)
	}
	if !c.LanguageBound {
		t.Error("mutation-score should be language-bound")
	}
	// Threshold argument should declare required=true.
	thresh, ok := c.Arguments["threshold"]
	if !ok {
		t.Fatal("mutation-score should declare 'threshold' argument")
	}
	if !thresh.Required {
		t.Error("mutation-score.threshold should be required")
	}
}

func TestLoad_NoTodoMarkerNotLanguageBound(t *testing.T) {
	cat := loadShipped(t)
	c, ok := cat.Get("no-todo-marker")
	if !ok {
		t.Fatal("no-todo-marker concept not loaded")
	}
	if c.LanguageBound {
		t.Error("no-todo-marker should NOT be language-bound (it's a text-level check)")
	}
}

func TestIsUniversalBase(t *testing.T) {
	cases := map[string]bool{
		"compiles":         true,
		"lint-clean":       true,
		"no-todo-marker":   true,
		"every-step-bound": true,
		"mutation-score":   false,
		"no-open-finding":  false, // auto-inserted, not universal-base
		"":                 false,
	}
	for name, want := range cases {
		if got := IsUniversalBase(name); got != want {
			t.Errorf("IsUniversalBase(%q) = %v; want %v", name, got, want)
		}
	}
}

func TestIsAutoInserted(t *testing.T) {
	cases := map[string]bool{
		"no-open-finding":                   true,
		"every-requirement-meets-min-depth": true,
		"compiles":                          false,
		"mutation-score":                    false,
		"":                                  false,
	}
	for name, want := range cases {
		if got := IsAutoInserted(name); got != want {
			t.Errorf("IsAutoInserted(%q) = %v; want %v", name, got, want)
		}
	}
}

// Negative-path tests use a tempdir with deliberately-broken schemas.

func TestLoad_NonExistentDir(t *testing.T) {
	_, err := Load("/nonexistent/path/should/not/exist")
	if err == nil {
		t.Fatal("Load of nonexistent dir should fail")
	}
}

func TestLoad_FilenameConceptNameMismatch(t *testing.T) {
	dir := t.TempDir()
	// File is named "foo.yaml" but declares concept name "bar".
	content := []byte(`concept: bar
description: test
language-bound: false
arguments: {}
evaluator:
  contract: machine
  produces:
    pass: boolean
default-cost: 0
`)
	if err := os.WriteFile(filepath.Join(dir, "foo.yaml"), content, 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load should fail when filename does not match concept name")
	}
}

func TestLoad_DuplicateConceptName(t *testing.T) {
	dir := t.TempDir()
	content := []byte(`concept: x
description: test
language-bound: false
arguments: {}
evaluator:
  contract: machine
  produces:
    pass: boolean
default-cost: 0
`)
	// Two files declaring concept "x" with matching filenames is
	// impossible (filesystem prevents duplicate names); use one valid
	// pair and one file whose filename does match.
	if err := os.WriteFile(filepath.Join(dir, "x.yaml"), content, 0644); err != nil {
		t.Fatal(err)
	}
	// Second file: filename matches but concept name collides — actually
	// we already covered the filename mismatch case. To trigger the
	// duplicate-name path, we'd need a YAML rewriter; not feasible with
	// distinct filenames given our same-name check. Document via the
	// filename-mismatch test instead and skip the explicit duplicate test.
	t.Skip("duplicate concept name with distinct filenames is impossible by design (filename must match concept name)")
}

func TestLoad_NonMachineContract(t *testing.T) {
	dir := t.TempDir()
	content := []byte(`concept: x
description: test
language-bound: false
arguments: {}
evaluator:
  contract: attested
  produces:
    pass: boolean
default-cost: 0
`)
	if err := os.WriteFile(filepath.Join(dir, "x.yaml"), content, 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load should fail when evaluator.contract is not \"machine\"")
	}
}

func TestLoad_MalformedYAML(t *testing.T) {
	dir := t.TempDir()
	content := []byte(`concept: x
this is: not valid yaml because of:
  - mismatch [brackets
`)
	if err := os.WriteFile(filepath.Join(dir, "x.yaml"), content, 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load should fail on malformed YAML")
	}
}
