# ADR-v4-001: Registry key shape — flat `<concept>.<language>` form

**Status:** Accepted (2026-05-25)

**Context:**

The runner's evaluator Registry maps a string key to an evaluator
function. Universal (language-bound:false) concepts have one
evaluator regardless of project language; language-bound:true
concepts ship one evaluator per declared language binding (one for
`compiles.go`, another for `compiles.rust`, etc.). Two shapes were
on the table for the key:

1. Flat `<concept>.<language>` string, with universal concepts using
   the bare concept name.
2. Nested registry: `map[concept]map[language]Evaluator`.

**Decision:**

Flat key. `ConceptRegistryKey(c Clause) string` returns:

- `c.Concept` verbatim for `language-bound:false` concepts.
- `c.Concept + "." + lang` for `language-bound:true` concepts,
  where `lang` is sourced safely from `c.Args["language"]` (no bare
  type assertion — non-string values return a sentinel that
  guarantees `Lookup` misses cleanly).

The auto-derived classification (`IsLanguageBoundConcept` /
`IsUniversalConcept` in `runner/concept_classification.go`) is the
source of truth for which branch applies.

**Consequences:**

- One `*Registry` instance covers both kinds of concepts. No
  branching at the Registry layer.
- The bootstrap-side `BindingKey{Concept, Language}` type stays the
  on-disk authoring shape; `BindingKey.String()` produces the same
  flat key the Registry uses.
- The grid YAML's `language-bindings:` map (`<concept>.<language>:
  <command>`) parses directly into the registry-ready keys. No
  schema migration today.
