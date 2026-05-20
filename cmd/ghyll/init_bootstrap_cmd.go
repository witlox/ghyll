package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/witlox/ghyll/bootstrap"
	"github.com/witlox/ghyll/catalogue"
	"github.com/witlox/ghyll/ui"
)

// ghyll init — production driver for the bootstrap pipeline
// (integrator finding C-1). Before this command existed, the
// pipeline (bootstrap.ProfileRepo / BuildProposal / BuildInitGrid /
// Grid.Write) had zero non-test callers — a fresh user could not
// produce `.ghyll/grid.v1.yaml`, so the gate-and-arrow runtime
// stayed dormant.
//
// The command runs the diamond-roles bootstrap end-to-end:
//
//  1. Profile the project directory (greenfield vs brownfield,
//     detect bounded contexts, detect languages).
//  2. Load the embedded concept catalogue.
//  3. For each of the four diamond role-pair arrows
//     (init→analyst, analyst→architect, architect→implementer,
//     implementer→integrator) and for each bounded context,
//     build a proposal from the upstream role's exit-gate clauses.
//  4. Auto-accept (VerdictConfirm) every clause whose default args
//     fully satisfy the catalogue concept schema; auto-skip the
//     rest with a residue note documenting the missing args. This
//     is the v1 non-interactive bootstrap — the modal-driven
//     operator verdict loop is downstream territory.
//  5. Assemble the grid via BuildInitGrid and persist via Grid.Write.
//
// The op-id is the operator's stable identifier (email or username).
// It is required because the grid records the bootstrapping
// operator in `created-by-op-id`. Validation is shared with
// `ghyll init attest` via validateOpID.

// noteAutoSkippedRequiredArgs is the residue reason recorded for
// clauses auto-skipped because their concept schema has required
// arguments that have no default and the v1 non-interactive
// bootstrap path has no way to gather them. The wire form is
// machine-parseable (the prefix is fixed; the suffix names the
// missing args) so downstream operator tooling can surface a
// "fill in these arguments and re-confirm" prompt against the
// recorded residue.
const initAutoSkipResidueReasonPrefix = "init-v1: auto-skipped (required args without defaults)"

func cmdInitBootstrap(args []string) error {
	opID := ""
	projectDir := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--op-id":
			if i+1 >= len(args) {
				return errors.New("--op-id requires a value")
			}
			opID = args[i+1]
			i++
		case "-h", "--help":
			ui.Usage(initUsage)
			return nil
		default:
			if len(args[i]) > 0 && args[i][0] == '-' {
				return fmt.Errorf("unknown flag %q", args[i])
			}
			if projectDir != "" {
				return fmt.Errorf("unexpected positional %q (project dir already set to %q)", args[i], projectDir)
			}
			projectDir = args[i]
		}
	}

	if opID == "" {
		return errors.New("ghyll init: --op-id is required")
	}
	// H-A post-prod-readiness adversarial: normalize the op-id to
	// NFC before stamping it into the grid so the grid's
	// created-by-op-id is equality-comparable with what
	// bootstrap.Session would later record.
	normalizedOpID, err := validateAndNormalizeOpID(opID)
	if err != nil {
		return fmt.Errorf("ghyll init: %w", err)
	}
	opID = normalizedOpID

	if projectDir == "" {
		projectDir = "."
	}
	absDir, err := filepath.Abs(projectDir)
	if err != nil {
		return fmt.Errorf("resolve project dir %q: %w", projectDir, err)
	}
	info, err := os.Stat(absDir)
	if err != nil {
		return fmt.Errorf("stat project dir %q: %w", absDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("project dir %q is not a directory", absDir)
	}

	// Refuse to clobber an existing grid. Per ADR-010 grid files
	// are immutable after write; the bootstrap path produces v1
	// only — amendments go through a different (yet to be wired)
	// code path that bumps the version.
	gridPath := filepath.Join(absDir, ".ghyll", "grid.v1.yaml")
	if _, err := os.Lstat(gridPath); err == nil {
		return fmt.Errorf("ghyll init: %s already exists; refusing to clobber an existing grid", gridPath)
	}

	ctx := context.Background()

	// 1. Profile the project.
	profile, err := bootstrap.ProfileRepoContext(ctx, absDir)
	if err != nil {
		return fmt.Errorf("ghyll init: profile %q: %w", absDir, err)
	}

	// Greenfield repos / brownfield repos without a src/<context>/
	// directory layout return zero bounded contexts. The v1
	// non-interactive bootstrap path cannot ask the operator for a
	// context list, so we synthesize a single "default" context
	// that the operator can rename / split later via an amendment.
	// Downstream tooling sees a usable grid; the auto-declaration is
	// recorded by the contexts list itself (description names the
	// auto-source).
	if profile.NeedsContextInterrogation() {
		if err := profile.DeclareContext("default", "auto-declared by ghyll init (no bounded contexts detected)"); err != nil {
			return fmt.Errorf("ghyll init: declare default context: %w", err)
		}
	}

	contexts := profile.BoundedContextsSnapshot()
	if len(contexts) == 0 {
		// Defensive: should never trigger after the DeclareContext
		// path above, but a future refactor that changes the
		// fallback shouldn't silently produce a zero-arrow grid.
		return errors.New("ghyll init: no bounded contexts after profile + auto-declaration (unreachable)")
	}

	// 2. Load the embedded concept catalogue.
	cat, err := catalogue.LoadEmbedded()
	if err != nil {
		return fmt.Errorf("ghyll init: load embedded catalogue: %w", err)
	}

	// 3. Build proposals for the four diamond role-pair arrows.
	rolePairs := [][2]string{
		{"init", "analyst"},
		{"analyst", "architect"},
		{"architect", "implementer"},
		{"implementer", "integrator"},
	}

	// Cache parsed role files keyed by the UPSTREAM role name. The
	// init pair uses the analyst role file (init has no role file
	// of its own; init→analyst inherits analyst's exit-gate
	// contract per ADR-008).
	upstreamRoleFile := func(upstream string) (*bootstrap.RoleFile, error) {
		roleName := upstream
		if upstream == "init" {
			roleName = "analyst"
		}
		return bootstrap.ParseRoleFileEmbedded(roleName)
	}

	var proposals []*bootstrap.ArrowProposal
	totalAutoConfirmed := 0
	totalAutoSkipped := 0
	for _, pair := range rolePairs {
		upstream, downstream := pair[0], pair[1]
		rf, err := upstreamRoleFile(upstream)
		if err != nil {
			return fmt.Errorf("ghyll init: load role %q: %w", upstream, err)
		}
		for _, bc := range contexts {
			ap, err := bootstrap.BuildProposal(rf, cat, upstream, downstream, bc.ID)
			if err != nil {
				return fmt.Errorf("ghyll init: build proposal %s→%s/%s: %w",
					upstream, downstream, bc.ID, err)
			}
			confirmed, skipped, err := autoAcceptProposal(ap, cat)
			if err != nil {
				return fmt.Errorf("ghyll init: auto-accept %s→%s/%s: %w",
					upstream, downstream, bc.ID, err)
			}
			totalAutoConfirmed += confirmed
			totalAutoSkipped += skipped
			proposals = append(proposals, ap)
		}
	}

	// 4. Assemble the grid.
	grid, err := bootstrap.BuildInitGrid(opID, profile, proposals)
	if err != nil {
		return fmt.Errorf("ghyll init: build grid: %w", err)
	}

	// 5. Persist via atomic Write (ADR-010).
	if err := grid.Write(absDir); err != nil {
		return fmt.Errorf("ghyll init: write grid: %w", err)
	}

	ui.Status("ℹ", "init complete: %d arrows across %d contexts; grid at %s",
		len(grid.Arrows), len(contexts), gridPath)
	ui.Status("ℹ", "  auto-confirmed %d clauses; auto-skipped %d clauses (recorded in residue)",
		totalAutoConfirmed, totalAutoSkipped)
	return nil
}

// autoAcceptProposal runs the v1 non-interactive verdict loop over
// every proposed clause in ap:
//
//   - Confirm (VerdictConfirm) if the clause's default args
//     satisfy the catalogue concept schema. Attested clauses
//     always satisfy validation (no schema), so they confirm.
//   - Skip (VerdictSkip) with a residue note naming the missing
//     args otherwise. The residue keeps the clause discoverable
//     so the operator can later amend the grid with the missing
//     argument values.
//
// Returns (confirmedCount, skippedCount, error). An error is
// returned only for unexpected failures (Apply errors other than
// ErrClauseArgsIncomplete). Args-incomplete is the normal path that
// triggers the skip fallback.
func autoAcceptProposal(ap *bootstrap.ArrowProposal, cat *catalogue.Catalogue) (int, int, error) {
	confirmed := 0
	skipped := 0
	for _, p := range ap.Proposed {
		err := ap.Apply(p.ID, bootstrap.Verdict{Kind: bootstrap.VerdictConfirm}, cat)
		if err == nil {
			confirmed++
			continue
		}
		if !errors.Is(err, bootstrap.ErrClauseArgsIncomplete) {
			return confirmed, skipped, fmt.Errorf("confirm %s: %w", p.ID, err)
		}
		// Fall back to Skip with a residue note carrying the
		// confirm-failure detail (which names the missing args).
		reason := fmt.Sprintf("%s: %v", initAutoSkipResidueReasonPrefix, err)
		if skipErr := ap.Apply(p.ID, bootstrap.Verdict{
			Kind:    bootstrap.VerdictSkip,
			Residue: reason,
		}, cat); skipErr != nil {
			return confirmed, skipped, fmt.Errorf("skip %s: %w", p.ID, skipErr)
		}
		skipped++
	}
	return confirmed, skipped, nil
}
