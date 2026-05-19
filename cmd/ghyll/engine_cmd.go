package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/witlox/ghyll/engine"
	"github.com/witlox/ghyll/runner"
)

// cmdEngineMain dispatches the `ghyll engine ...` subcommands.
// Subcommands:
//
//	status [--dir path]  — summarize what's persisted
//	replay [--dir path]  — replay from disk and report counts/errors
//
// Both default to the cwd's `.ghyll/engine.db`. Read-only — neither
// command writes through the engine.
func cmdEngineMain(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: ghyll engine status|replay [--dir <path>]")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "status":
		return cmdEngineStatus(rest)
	case "replay":
		return cmdEngineReplay(rest)
	default:
		return fmt.Errorf("ghyll engine: unknown subcommand %q", sub)
	}
}

// parseEngineFlags reads `--dir <path>` from args. Returns the
// resolved engine.db path.
func parseEngineFlags(args []string) (string, error) {
	dir := "."
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dir":
			if i+1 >= len(args) {
				return "", errors.New("--dir requires a path")
			}
			dir = args[i+1]
			i++
		default:
			return "", fmt.Errorf("unknown flag %q", args[i])
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("abs %q: %w", dir, err)
	}
	return filepath.Join(abs, ".ghyll", "engine.db"), nil
}

// cmdEngineStatus opens the store read-only and prints per-category
// counts. Honors a missing-db case (no engine yet) by returning a
// clean message instead of an error.
func cmdEngineStatus(args []string) error {
	dbPath, err := parseEngineFlags(args)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		fmt.Printf("ghyll engine: no store at %s (project has not initialized v2 yet)\n", dbPath)
		return nil
	}
	store, err := engine.OpenStore(dbPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	findings, err := store.ListFindings(ctx, engine.FindingFilter{MinSeverity: -1, Limit: 1000})
	if err != nil {
		return fmt.Errorf("findings: %w", err)
	}
	arrows, err := store.ListArrows(ctx, engine.ArrowFilter{Limit: 1000})
	if err != nil {
		return fmt.Errorf("arrows: %w", err)
	}
	reqs, err := store.ListRequirements(ctx, engine.RequirementFilter{Limit: 1000})
	if err != nil {
		return fmt.Errorf("requirements: %w", err)
	}
	cls, err := store.ListClassifications(ctx, engine.ClassificationFilter{Limit: 1000})
	if err != nil {
		return fmt.Errorf("classifications: %w", err)
	}
	pending := false
	pendingAm, err := store.ListAmendments(ctx, engine.AmendmentFilter{Drained: &pending, Limit: 1000})
	if err != nil {
		return fmt.Errorf("pending amendments: %w", err)
	}
	drained := true
	drainedAm, err := store.ListAmendments(ctx, engine.AmendmentFilter{Drained: &drained, Limit: 1000})
	if err != nil {
		return fmt.Errorf("drained amendments: %w", err)
	}
	runs, err := store.ListEvaluationRuns(ctx, engine.RunFilter{Limit: 1000})
	if err != nil {
		return fmt.Errorf("evaluation runs: %w", err)
	}

	fmt.Printf("engine: %s\n", dbPath)
	fmt.Printf("  arrows:           %d\n", len(arrows))
	fmt.Printf("  findings:         %d\n", len(findings))
	fmt.Printf("  requirements:     %d\n", len(reqs))
	fmt.Printf("  classifications:  %d\n", len(cls))
	fmt.Printf("  amendments:       %d pending, %d drained\n", len(pendingAm), len(drainedAm))
	fmt.Printf("  evaluation runs:  %d\n", len(runs))
	return nil
}

// cmdEngineReplay opens the store, runs replay into a fresh set of
// in-memory caches, and prints the result. Useful for verifying
// that a persisted state round-trips cleanly (catches corruption
// before it surfaces at session start).
func cmdEngineReplay(args []string) error {
	dbPath, err := parseEngineFlags(args)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		fmt.Printf("ghyll engine: no store at %s (nothing to replay)\n", dbPath)
		return nil
	}
	store, err := engine.OpenStore(dbPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer func() { _ = store.Close() }()

	targets := engine.ReplayTargets{
		Findings:        runner.NewFindingsStore(),
		Classifications: runner.NewClassificationsStore(),
		Grid:            runner.NewGrid(),
		Amendments:      runner.NewAmendmentQueue(),
	}
	counts, err := engine.Replay(context.Background(), store, targets)
	if err != nil {
		return fmt.Errorf("replay: %w", err)
	}
	fmt.Printf("replay: %s\n", dbPath)
	fmt.Printf("  arrows:            %d\n", counts.Arrows)
	fmt.Printf("  findings:          %d\n", counts.Findings)
	fmt.Printf("  requirements:      %d\n", counts.Requirements)
	fmt.Printf("  classifications:   %d\n", counts.Classifications)
	fmt.Printf("  amendments:        %d active, %d drained\n",
		counts.AmendmentsActive, counts.AmendmentsDrained)
	if len(counts.Errors) > 0 {
		fmt.Printf("  per-row errors:    %d\n", len(counts.Errors))
		for _, e := range counts.Errors {
			fmt.Printf("    - %s\n", e)
		}
		return fmt.Errorf("replay completed with %d errors", len(counts.Errors))
	}
	fmt.Println("  per-row errors:    0")
	return nil
}
