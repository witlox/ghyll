// Package runner — concept classification (diamond v4, Gap 4).
//
// Concept classification is auto-derived from the embedded
// gates/concepts/*.yaml schemas at package-init time via the existing
// catalogue.LoadEmbedded() entry point (per ADR-v4-004). The
// alternative — a hand-maintained list in runner — drifts the moment
// a new concept lands. The auto-derive keeps the runner-side
// predicate honest.
//
// The init() function panics if the embedded catalogue:
//   - fails to load (any catalogue parse error);
//   - contains a total count that disagrees with the v4 contract
//     (18 = 11 universal + 7 language-bound; both invariants checked
//     independently per R19);
//
// Loud failure at startup beats a silent drift that lets unknown
// concepts slip past the dispatcher.

package runner

import (
	"fmt"

	"github.com/witlox/ghyll/catalogue"
)

// languageBoundConcepts is the set of concepts whose evaluator is
// supplied by a project-declared language binding (per the YAML's
// language-bound: true field).
var languageBoundConcepts = map[string]struct{}{}

// universalConcepts is the set of language-bound: false concepts —
// every one MUST have an in-process Go evaluator registered before
// the first dispatch. Per the v2 artifact contract, the count is
// load-bearing: exactly 11 today.
var universalConcepts = map[string]struct{}{}

func init() {
	cat, err := catalogue.LoadEmbedded()
	if err != nil {
		panic(fmt.Sprintf("concept_classification: load embedded catalogue: %v", err))
	}
	for _, name := range cat.List() {
		c, _ := cat.Get(name)
		if c.LanguageBound {
			languageBoundConcepts[name] = struct{}{}
		} else {
			universalConcepts[name] = struct{}{}
		}
	}
	// R19 closure: both invariants asserted independently. v1 only
	// asserted the 18-total; missing one half is a silent drift.
	if got, want := len(universalConcepts)+len(languageBoundConcepts), 18; got != want {
		panic(fmt.Sprintf("concept_classification: expected %d concepts, got %d (universal=%d language-bound=%d)",
			want, got, len(universalConcepts), len(languageBoundConcepts)))
	}
	if got, want := len(universalConcepts), 11; got != want {
		panic(fmt.Sprintf("concept_classification: expected %d universal concepts, got %d", want, got))
	}
}

// IsUniversalConcept reports whether the named concept is a
// language-bound: false universal (in-process evaluator required).
func IsUniversalConcept(concept string) bool {
	_, ok := universalConcepts[concept]
	return ok
}

// IsLanguageBoundConcept reports whether the named concept is a
// language-bound: true concept (project-declared BindingEvaluator
// required).
func IsLanguageBoundConcept(concept string) bool {
	_, ok := languageBoundConcepts[concept]
	return ok
}

// ConceptRegistryKey returns the Registry lookup key for a clause
// (ADR-v4-001 + ADR-v4-006). For language-bound:false concepts,
// the bare concept name. For language-bound:true concepts, the
// flat "<concept>.<language>" form, with `language` extracted
// safely from c.Args (R18 closure: no bare type assertion).
//
// On missing or non-string `language` arg, returns the sentinel
// "<concept>." form which guarantees Registry.Lookup misses
// cleanly — the operator-facing coverage check (in cmd/ghyll
// per ADR-v4-007) surfaces the gap before runtime dispatch.
func ConceptRegistryKey(c Clause) string {
	if !IsLanguageBoundConcept(c.Concept) {
		return c.Concept
	}
	raw, ok := c.Args["language"]
	if !ok {
		return c.Concept + "."
	}
	lang, ok := raw.(string)
	if !ok || lang == "" {
		return c.Concept + "."
	}
	return c.Concept + "." + lang
}
