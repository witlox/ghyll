package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/witlox/ghyll/bootstrap"
	"github.com/witlox/ghyll/engine"
	"github.com/witlox/ghyll/runner"
	"github.com/witlox/ghyll/ui"
)

// ghyll init — Tier 3 / gate-2 CORR-A-18 production producer for
// init AttestationRecords. The session's normal modal flow records
// operator verdicts; this CLI escape hatch is the bootstrap-time
// equivalent: when init completes, the operator runs
// `ghyll init attest --op-id <id>` once to write one on-the-spot
// attestation per arrow asserting "init bootstrapped this arrow."
//
// The records land in the AttestationStore + tree writer + flat
// JSONL via the standard Record path, so they're indistinguishable
// from modal-flow records once on disk.

const initUsage = "usage: ghyll init attest --op-id <id> [--dir <path>]"

func cmdInitMain(args []string) error {
	if len(args) < 1 {
		return errors.New(initUsage)
	}
	switch args[0] {
	case "attest":
		return cmdInitAttest(args[1:])
	default:
		return fmt.Errorf("ghyll init: unknown subcommand %q", args[0])
	}
}

func cmdInitAttest(args []string) error {
	opID := ""
	dir := "."
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--op-id":
			if i+1 >= len(args) {
				return errors.New("--op-id requires a value")
			}
			opID = args[i+1]
			i++
		case "--dir":
			if i+1 >= len(args) {
				return errors.New("--dir requires a value")
			}
			dir = args[i+1]
			i++
		default:
			return fmt.Errorf("unknown flag %q", args[i])
		}
	}
	if opID == "" {
		return errors.New("ghyll init attest: --op-id is required")
	}
	if err := validateOpID(opID); err != nil {
		return fmt.Errorf("ghyll init attest: %w", err)
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("abs %q: %w", dir, err)
	}
	grid, err := bootstrap.Read(abs)
	if err != nil {
		return fmt.Errorf("ghyll init attest: read grid: %w", err)
	}
	if grid == nil || len(grid.Arrows) == 0 {
		return fmt.Errorf("ghyll init attest: grid has no arrows; nothing to attest")
	}

	recs, err := bootstrap.EmitInitAttestations(grid, opID)
	if err != nil {
		return fmt.Errorf("ghyll init attest: emit: %w", err)
	}

	// Open the engine + wire the tree writer so records land in
	// both the JSONL audit + the engine table. Reuses the session
	// wiring conventions (.ghyll/ subdirectory layout).
	dbPath := filepath.Join(abs, ".ghyll", "engine.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("ensure .ghyll: %w", err)
	}
	store, err := engine.OpenStore(dbPath)
	if err != nil {
		return fmt.Errorf("open engine: %w", err)
	}
	defer func() { _ = store.Close() }()

	atts := runner.NewAttestationStore()
	treeRoot := filepath.Join(filepath.Dir(dbPath), "attestations")
	tw, twerr := runner.NewAttestationTreeWriter(treeRoot)
	if twerr == nil {
		atts.SetPrimaryWriter(tw.PrimaryWriter())
		defer func() { _ = tw.Close() }()
	}

	written := 0
	for _, rec := range recs {
		// Idempotent: if the record already exists with identical
		// content, Record returns nil silently.
		if err := atts.Record(rec); err != nil {
			ui.Info("⚠ %s: %v", rec.ID, err)
			continue
		}
		written++
	}
	// Sync to engine cache.
	if _, _, err := store.CatchUpAttestations(context.Background(), atts); err != nil {
		return fmt.Errorf("engine catch-up: %w", err)
	}

	ui.Info("ghyll init attest: %d init attestations recorded for op-id=%s (grid v%d, %d arrows)",
		written, sanitizeOneLine(opID), grid.GridVersion, len(grid.Arrows))
	if written < len(recs) {
		ui.Info("  %d skipped (likely already recorded)", len(recs)-written)
	}
	return nil
}

// keepImports anchors strings as referenced; the package needs
// the import for future flag-value normalization.
var _ = strings.TrimSpace
