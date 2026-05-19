package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/witlox/ghyll/engine"
	"github.com/witlox/ghyll/runner"
	"github.com/witlox/ghyll/ui"
)

// cmdArrowMain dispatches `ghyll arrow <subcommand>`. Today the
// only subcommand is `show <arrow-id>` — surface one arrow's
// definition, current findings, and any recorded attestations.
//
// The arrow CLI is the operator-facing inverse of the runtime:
// where the dispatcher takes an arrow and runs it, `ghyll arrow
// show` takes an arrow id and renders what the runtime knows.
const arrowUsage = "usage: ghyll arrow show <arrow-id> [--dir <path>]"

func cmdArrowMain(args []string) error {
	if len(args) < 1 {
		return errors.New(arrowUsage)
	}
	switch args[0] {
	case "show":
		return cmdArrowShow(args[1:])
	default:
		return fmt.Errorf("ghyll arrow: unknown subcommand %q", args[0])
	}
}

func cmdArrowShow(args []string) error {
	if len(args) < 1 {
		return errors.New("ghyll arrow show: arrow-id required")
	}
	arrowID := strings.TrimSpace(args[0])
	rest := args[1:]
	if arrowID == "" || strings.HasPrefix(arrowID, "--") {
		return errors.New("ghyll arrow show: arrow-id must be the first positional argument")
	}

	dir := "."
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--dir":
			if i+1 >= len(rest) {
				return errors.New("--dir requires a value")
			}
			dir = rest[i+1]
			i++
		default:
			return fmt.Errorf("unknown flag %q", rest[i])
		}
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("abs %q: %w", dir, err)
	}
	dbPath := filepath.Join(abs, ".ghyll", "engine.db")

	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			ui.Info("%s", missingEngineLine)
			ui.Info("ghyll arrow: no store at %s (project has not initialized v2 yet)", dbPath)
			return nil
		}
		return fmt.Errorf("stat %s: %w", dbPath, err)
	}

	store, err := engine.OpenStoreReadOnly(dbPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer func() { _ = store.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Replay into a fresh set of in-memory caches so we have a
	// consistent view. Per the Tier-0 replay-targets contract,
	// passing all of them populates a coherent snapshot.
	caches := runner.NewFindingsStore()
	classifications := runner.NewClassificationsStore()
	grid := runner.NewGrid()
	amendments := runner.NewAmendmentQueue()
	attestations := runner.NewAttestationStore()
	if _, err := engine.Replay(ctx, store, engine.ReplayTargets{
		Findings:        caches,
		Classifications: classifications,
		Grid:            grid,
		Amendments:      amendments,
		Attestations:    attestations,
	}); err != nil {
		return fmt.Errorf("replay: %w", err)
	}

	def, ok := grid.Lookup(arrowID)
	if !ok {
		return fmt.Errorf("arrow %q not in grid (grid version=%d, %d total arrows)",
			arrowID, grid.Version(), len(grid.Arrows()))
	}

	// --- Render ---
	ui.Info("arrow: %s", def.ID)
	ui.Info("  source-role:  %s", def.SourceRole)
	ui.Info("  target-role:  %s", def.TargetRole)
	ui.Info("  stratum:      %s", def.Stratum)
	ui.Info("  context:      %s", def.Context)
	ui.Info("  clauses:      %d", len(def.Clauses))
	for i, c := range def.Clauses {
		ui.Info("    [%d] %s (id=%s, depth=%s, min-tier=%d)",
			i, c.Concept, displayOrFallback(c.ClauseID, "<unset>"),
			displayOrFallback(string(c.DepthType), "depth-robust"),
			c.MinDepthTier)
	}
	ui.Info("  requirements: %d", len(def.Requirements))
	for i, r := range def.Requirements {
		ui.Info("    [%d] %s (min-depth=%d)", i, r.ID, r.MinDepth)
	}

	// Findings on this arrow.
	findings := caches.ForArrow(arrowID)
	ui.Info("  findings:     %d", len(findings))
	for _, f := range findings {
		ui.Info("    %s  type=%s  severity=%d  status=%s",
			f.ID, f.Type, f.Severity, findingStatusName(f.Status))
	}

	// Attestations on this arrow.
	atts := attestations.ForArrow(arrowID)
	ui.Info("  attestations: %d", len(atts))
	for _, a := range atts {
		clause := a.ClauseID
		if clause == "" {
			clause = "<arrow-scope>"
		}
		ui.Info("    %s  kind=%s  clause=%s  verdict=%s  op=%s",
			a.ID, a.Kind, clause, a.Verdict, a.OpID)
	}

	return nil
}

func displayOrFallback(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func findingStatusName(s runner.FindingStatus) string {
	switch s {
	case runner.FindingStatusOpen:
		return "open"
	case runner.FindingStatusRunning:
		return "running"
	case runner.FindingStatusResolved:
		return "resolved"
	case runner.FindingStatusAcceptedRisk:
		return "accepted-risk"
	case runner.FindingStatusUnevaluated:
		return "unevaluated"
	default:
		return fmt.Sprintf("status-%d", s)
	}
}
