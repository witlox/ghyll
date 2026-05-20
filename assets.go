// Package ghyll holds repo-root embedded assets that production code
// needs to access at runtime from a released binary (where the source
// tree is no longer present).
//
// Why a root-level package? Go's //go:embed directive can only reach
// files at-or-below the embedding source file's directory — it cannot
// escape upward with `..`. The canonical role contracts live at
// specs/architecture/roles/*.md (ADR-008) and the canonical concept
// schemas live at gates/concepts/*.yaml; both trees sit at the repo
// root. Placing the embed directive in any subpackage (bootstrap/,
// gates/, etc.) would require duplicating the files. By embedding
// here, the spec tree remains the single source of truth.
//
// Downstream packages import this package as `assets` (the package
// name is `ghyll`, but call-sites typically alias it) and read from
// the exported FS variables.
package ghyll

import "embed"

// RolesFS embeds the four role contracts (analyst, architect,
// implementer, integrator) defined by ADR-008. bootstrap.ParseRoleFileEmbedded
// reads from this FS so the parser works inside a released binary.
//
// Paths inside the FS retain the source-tree layout, e.g.
//
//	specs/architecture/roles/analyst.md
//
//go:embed specs/architecture/roles/*.md
var RolesFS embed.FS

// ConceptsFS embeds the closed concept-schema vocabulary
// (gates/concepts/*.yaml) defined by ADR-005 / ADR-006. The released
// binary depends on this FS, not on the source checkout's on-disk
// layout, so `ghyll init` and the runner can construct the catalogue
// at session start regardless of where the binary lives (integrator
// finding H-1).
//
// Paths inside the FS retain the source-tree layout, e.g.
//
//	gates/concepts/compiles.yaml
//
// catalogue.LoadEmbedded is the canonical reader.
//
//go:embed gates/concepts/*.yaml
var ConceptsFS embed.FS

// ConceptsDir is the directory prefix inside ConceptsFS that holds
// the schema YAMLs. Exposed so consumers don't hard-code the string.
const ConceptsDir = "gates/concepts"
