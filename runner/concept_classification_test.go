package runner

import (
	"testing"

	"github.com/witlox/ghyll/catalogue"
)

// TestScenario_ConceptClassification_18Total verifies the load-bearing
// 18-total invariant holds against the embedded YAMLs.
func TestScenario_ConceptClassification_18Total(t *testing.T) {
	t.Parallel()
	got := len(universalConcepts) + len(languageBoundConcepts)
	if got != 18 {
		t.Fatalf("expected 18 concepts, got %d (universal=%d, language-bound=%d)",
			got, len(universalConcepts), len(languageBoundConcepts))
	}
}

// TestScenario_ConceptClassification_11UniversalsExactly verifies the
// R19 closure: the 11-universal count is asserted independently from
// the 18-total. v1 only asserted the total.
func TestScenario_ConceptClassification_11UniversalsExactly(t *testing.T) {
	t.Parallel()
	if got := len(universalConcepts); got != 11 {
		t.Fatalf("expected 11 universal concepts, got %d", got)
	}
}

// TestScenario_ConceptClassification_AgreesWithYAML verifies the
// runner-side predicate agrees with the catalogue's LanguageBound
// field for every concept.
func TestScenario_ConceptClassification_AgreesWithYAML(t *testing.T) {
	t.Parallel()
	cat, err := catalogue.LoadEmbedded()
	if err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	for _, name := range cat.List() {
		c, _ := cat.Get(name)
		if c.LanguageBound {
			if !IsLanguageBoundConcept(name) {
				t.Errorf("%q: YAML says language-bound:true but IsLanguageBoundConcept=false", name)
			}
			if IsUniversalConcept(name) {
				t.Errorf("%q: cannot be both language-bound and universal", name)
			}
		} else {
			if !IsUniversalConcept(name) {
				t.Errorf("%q: YAML says language-bound:false but IsUniversalConcept=false", name)
			}
			if IsLanguageBoundConcept(name) {
				t.Errorf("%q: cannot be both universal and language-bound", name)
			}
		}
	}
}

// TestScenario_RegisterBuiltins_RegistersAll11Universals verifies that
// after RegisterBuiltins the registry holds exactly 11 entries (one
// per universal concept). Language-bound concepts are not registered
// here; they come from grid bindings.
func TestScenario_RegisterBuiltins_RegistersAll11Universals(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	RegisterBuiltins(reg)
	if got := reg.Count(); got != 11 {
		t.Fatalf("expected 11 registered evaluators after RegisterBuiltins, got %d", got)
	}
	// Spot-check the four new evaluators are present.
	for _, name := range []string{"unique-definition", "predicate-form", "mode-determinable-from-repo"} {
		if _, _, ok := reg.Lookup(name); !ok {
			t.Errorf("%q not registered in plain table", name)
		}
	}
	if _, _, ok := reg.LookupWithRunner("single-active-role-instance"); !ok {
		t.Errorf("single-active-role-instance not registered in runner-typed table")
	}
}
