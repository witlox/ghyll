// Package catalogue is the in-memory representation of the harness's
// closed concept vocabulary (the 18 machine-clause concepts shipped at
// gates/concepts/*.yaml).
//
// Per ADR-005 and ADR-006, the catalogue is closed at the concept layer
// and language-agnostic. Per-language instrument bindings (e.g.,
// lint-clean.go = staticcheck) are project-declared at init and live
// in the grid file, not here.
//
// Public surface:
//
//   - LoadEmbedded() (*Catalogue, error) — production entry point;
//     reads the schemas embedded into the binary at build time
//     (ghyll.ConceptsFS). Use this for any session-start wiring so
//     the released binary does not depend on the source checkout's
//     on-disk layout (integrator finding H-1).
//   - Load(dir string) (*Catalogue, error) — disk-backed loader for
//     custom-data scenarios (tests with bespoke schemas, ad-hoc
//     tooling). Production code should prefer LoadEmbedded.
//   - (*Catalogue).Get(name string) (Concept, bool) — lookup by name.
//   - (*Catalogue).List() []string — enumerate concept names (sorted).
//   - (*Catalogue).Count() int — number of loaded concepts.
//   - (*Catalogue).Validate(name string, args map[string]any) error —
//     verify a clause's args against the concept's schema.
//
// Plus helpers for the two harness-fixed concept sets:
//
//   - IsUniversalBase(name) — concepts auto-applied to every arrow
//     per gates.md §5.2 (compiles, lint-clean, no-todo-marker,
//     every-step-bound).
//   - IsAutoInserted(name) — concepts auto-inserted on adversarial
//     arrows during verification per gates.md §11.3 (no-open-finding,
//     every-requirement-meets-min-depth).
package catalogue
