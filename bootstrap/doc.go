// Package bootstrap implements v2 project initialization
// (gates.md §2, ADR-011, specs/direction/components/init.md).
//
// Project initialization is the mandatory step-one when ghyll is
// invoked on a new project. It turns the harness's v0 baseline into
// the project's v1 arrow grid through an auto-propose + operator-confirm
// flow.
//
// This package is named "bootstrap" rather than "init" because Go's
// `init()` function name collides with package-name use of `init`
// (every package can have init functions; calling the package "init"
// makes imports awkward). The user-facing label remains "init" — the
// CLI subcommand, the synthetic role-id, the schema sections.
//
// Components shipped so far (incremental v2 build):
//
//   - Session: operator-session lifecycle + op-id validation.
//
// Coming:
//
//   - Project profile and context discovery (sub-phase A).
//   - Auto-propose loop reading from roles/*.md + catalogue (sub-phase B).
//   - Grid file writer (.ghyll/grid.v1.yaml + grid.current).
//   - Refusal flow.
package bootstrap
