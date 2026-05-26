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

const initUsage = "usage: ghyll init [--op-id <id>] [--language <lang|auto|none>] [--force-traits] [project-dir]\n" +
	"       ghyll init attest --op-id <id> [--dir <path>]\n" +
	"\n" +
	"  --language       go|python|cpp|rust|auto|none (default: auto from profile)\n" +
	"                   comma-separated for polyglot, e.g. --language go,python\n" +
	"  --force-traits   rewrite the existing trait block in <project>/.ghyll/instructions.md\n" +
	"                   (default: leave alone if it already exists)"

// cmdInitMain dispatches the `ghyll init` family. Two production
// entry points coexist:
//
//   - `ghyll init attest`: emits one init AttestationRecord per arrow
//     in an already-written grid (existing gate-2 producer).
//   - `ghyll init` (no subcommand): runs the bootstrap pipeline that
//     PRODUCES the grid in the first place (integrator finding C-1).
//
// The router distinguishes the two by inspecting args[0]: the literal
// "attest" is the only reserved subcommand. Any other first arg (a
// flag like "--op-id" or a positional project dir) routes to the
// bootstrap path. Empty args also routes to bootstrap (the bootstrap
// path tolerates zero args and uses cwd as the project dir).
func cmdInitMain(args []string) error {
	if len(args) >= 1 && args[0] == "attest" {
		return cmdInitAttest(args[1:])
	}
	return cmdInitBootstrap(args)
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
	// H-A post-prod-readiness adversarial: normalize op-id to NFC so
	// emitted AttestationRecords carry the canonical form (matches
	// what bootstrap.Session and the grid's created-by-op-id store).
	normalizedOpID, err := validateAndNormalizeOpID(opID)
	if err != nil {
		return fmt.Errorf("ghyll init attest: %w", err)
	}
	opID = normalizedOpID

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
