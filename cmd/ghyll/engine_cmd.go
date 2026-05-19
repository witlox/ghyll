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
)

// cmdEngineMain dispatches the `ghyll engine ...` subcommands.
// Subcommands:
//
//	status [--dir path]  — summarize what's persisted
//	replay [--dir path]  — replay from disk and report counts/errors
//
// Both default to the cwd's `.ghyll/engine.db`. Read-only — neither
// command writes through the engine.
//
// Validation-pass-10 hardenings:
//   - C1: Count* methods replace ListX-truncation. Counts are
//     accurate even on a 100k-row engine.
//   - C2: per-row error dump caps at 50.
//   - C3: terminal-control bytes stripped from per-row error text.
//   - C4: sqlite-internal error text not leaked verbatim.
//   - C5: replay runs under a 60s timeout (overrideable via
//     --timeout).
//   - C6: engine.db being a directory surfaces as a typed error.
//   - C7: schema_version mismatch reported via engine.OpenStore.
//   - C8: misleading "unknown flag" for trailing positionals.
//   - C9: --dir cap at 4096 bytes.
//   - C10: partial-counts on replay error.
//   - C11/C15: structured first-line token on missing-DB so scripts
//     can distinguish "no engine" from "engine empty" without
//     parsing free text.
const engineUsage = "usage: ghyll engine status|replay [--dir <path>] [--timeout <seconds>] [--verbose]"

func cmdEngineMain(args []string) error {
	if len(args) < 1 {
		return errors.New(engineUsage)
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

// engineFlags is the parsed form of an `engine` subcommand's flags.
type engineFlags struct {
	DBPath  string
	Timeout time.Duration
	Verbose bool
}

const (
	maxEngineDirLen        = 4096
	defaultEngineCLITimout = 60 * time.Second
	maxCLIErrorsShown      = 50
)

// parseEngineFlags reads `--dir <path>` from args. Returns the
// resolved engine.db path + timeout. Per C8: any positional
// argument is rejected with a distinct message. Per C9: --dir
// length capped at maxEngineDirLen.
func parseEngineFlags(args []string) (engineFlags, error) {
	dir := "."
	timeout := defaultEngineCLITimout
	verbose := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dir":
			if i+1 >= len(args) {
				return engineFlags{}, errors.New("--dir requires a path")
			}
			val := args[i+1]
			if len(val) > maxEngineDirLen {
				return engineFlags{}, fmt.Errorf("--dir exceeds %d bytes (%d)", maxEngineDirLen, len(val))
			}
			dir = val
			i++
		case "--timeout":
			if i+1 >= len(args) {
				return engineFlags{}, errors.New("--timeout requires seconds")
			}
			var secs int
			if _, err := fmt.Sscanf(args[i+1], "%d", &secs); err != nil || secs <= 0 {
				return engineFlags{}, fmt.Errorf("invalid --timeout %q (positive integer seconds required)", args[i+1])
			}
			timeout = time.Duration(secs) * time.Second
			i++
		case "--verbose":
			verbose = true
		default:
			if strings.HasPrefix(args[i], "--") {
				return engineFlags{}, fmt.Errorf("unknown flag %q", args[i])
			}
			// C8: distinguish from "unknown flag".
			return engineFlags{}, fmt.Errorf("unexpected positional argument %q", args[i])
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return engineFlags{}, fmt.Errorf("abs %q: %w", dir, err)
	}
	return engineFlags{
		DBPath:  filepath.Join(abs, ".ghyll", "engine.db"),
		Timeout: timeout,
		Verbose: verbose,
	}, nil
}

// missingEngineLine is the structured first-line marker emitted
// when the engine.db does not exist (C11/C15). Scripts that key on
// the first whitespace-separated token can distinguish this from
// "engine empty" without parsing the free-text remainder.
const missingEngineLine = "ghyll-engine-status: missing"

// emptyEngineMarker is the structured first-line token for the
// "engine exists but has no rows" case so scripts can tell the two
// apart.
const emptyEngineMarker = "ghyll-engine-status: empty"

// presentEngineMarker is emitted when at least one row is present.
const presentEngineMarker = "ghyll-engine-status: present"

// cmdEngineStatus opens the store read-only and prints per-category
// counts via engine.CountX methods (C1). Honors missing-db with a
// structured first-line token (C11).
func cmdEngineStatus(args []string) error {
	fl, err := parseEngineFlags(args)
	if err != nil {
		return err
	}
	dbPath := fl.DBPath
	if err := preflightDBPath(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// C11/C15: emit a structured marker first so scripts can
			// distinguish missing-engine from any other failure.
			fmt.Printf("%s\nghyll engine: no store at %s (project has not initialized v2 yet)\n", missingEngineLine, dbPath)
			return nil
		}
		return classifyCLIError(err, fl.Verbose)
	}
	store, err := engine.OpenStoreReadOnly(dbPath)
	if err != nil {
		return classifyCLIError(fmt.Errorf("open %s: %w", dbPath, err), fl.Verbose)
	}
	defer func() { _ = store.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), fl.Timeout)
	defer cancel()

	arrows, err := store.CountArrows(ctx)
	if err != nil {
		return classifyCLIError(err, fl.Verbose)
	}
	findings, err := store.CountFindings(ctx)
	if err != nil {
		return classifyCLIError(err, fl.Verbose)
	}
	reqs, err := store.CountRequirements(ctx)
	if err != nil {
		return classifyCLIError(err, fl.Verbose)
	}
	cls, err := store.CountClassifications(ctx)
	if err != nil {
		return classifyCLIError(err, fl.Verbose)
	}
	pendingAm, drainedAm, err := store.CountAmendments(ctx)
	if err != nil {
		return classifyCLIError(err, fl.Verbose)
	}
	runs, err := store.CountEvaluationRuns(ctx)
	if err != nil {
		return classifyCLIError(err, fl.Verbose)
	}

	total := arrows + findings + reqs + cls + pendingAm + drainedAm + runs
	header := presentEngineMarker
	if total == 0 {
		header = emptyEngineMarker
	}
	fmt.Printf("%s\nengine: %s\n", header, dbPath)
	fmt.Printf("  arrows:           %d\n", arrows)
	fmt.Printf("  findings:         %d\n", findings)
	fmt.Printf("  requirements:     %d\n", reqs)
	fmt.Printf("  classifications:  %d\n", cls)
	fmt.Printf("  amendments:       %d pending, %d drained\n", pendingAm, drainedAm)
	fmt.Printf("  evaluation runs:  %d\n", runs)
	// NOTE: output format above is NOT a wire contract. C15:
	// machine consumers should use a future `--format json` once
	// it lands.
	return nil
}

// cmdEngineReplay opens the store, runs replay into a fresh set of
// in-memory caches, and prints the result. Per C5: bounded by
// --timeout (default 60s). Per C2/C3/C10: per-row errors are
// sanitized + capped, partial counts are printed even when replay
// errors.
func cmdEngineReplay(args []string) error {
	fl, err := parseEngineFlags(args)
	if err != nil {
		return err
	}
	dbPath := fl.DBPath
	if err := preflightDBPath(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Printf("%s\nghyll engine: no store at %s (nothing to replay)\n", missingEngineLine, dbPath)
			return nil
		}
		return classifyCLIError(err, fl.Verbose)
	}
	store, err := engine.OpenStoreReadOnly(dbPath)
	if err != nil {
		return classifyCLIError(fmt.Errorf("open %s: %w", dbPath, err), fl.Verbose)
	}
	defer func() { _ = store.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), fl.Timeout)
	defer cancel()

	targets := engine.ReplayTargets{
		Findings:        runner.NewFindingsStore(),
		Classifications: runner.NewClassificationsStore(),
		Grid:            runner.NewGrid(),
		Amendments:      runner.NewAmendmentQueue(),
	}
	counts, replayErr := engine.Replay(ctx, store, targets)
	// C10: print whatever partial counts we got before any error.
	fmt.Printf("replay: %s\n", dbPath)
	fmt.Printf("  arrows:            %d\n", counts.Arrows)
	fmt.Printf("  findings:          %d\n", counts.Findings)
	fmt.Printf("  requirements:      %d\n", counts.Requirements)
	fmt.Printf("  classifications:   %d\n", counts.Classifications)
	fmt.Printf("  amendments:        %d active, %d drained\n",
		counts.AmendmentsActive, counts.AmendmentsDrained)
	if replayErr != nil {
		return classifyCLIError(replayErr, fl.Verbose)
	}
	if len(counts.Errors) > 0 {
		shown := len(counts.Errors)
		if shown > maxCLIErrorsShown {
			shown = maxCLIErrorsShown
		}
		fmt.Printf("  per-row errors:    %d\n", len(counts.Errors))
		for i := 0; i < shown; i++ {
			// C3: sanitize each error string at the boundary.
			fmt.Printf("    - %s\n", sanitizeOneLine(counts.Errors[i]))
		}
		if len(counts.Errors) > maxCLIErrorsShown {
			fmt.Printf("    … %d more errors elided\n", len(counts.Errors)-maxCLIErrorsShown)
		}
		return fmt.Errorf("replay completed with %d errors", len(counts.Errors))
	}
	fmt.Println("  per-row errors:    0")
	return nil
}

// preflightDBPath verifies the DB path exists and is a regular file
// (C6). Returns os.ErrNotExist for the missing case so callers can
// detect and emit the structured "missing" marker.
func preflightDBPath(dbPath string) error {
	info, err := os.Stat(dbPath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("engine path %s is a directory (expected sqlite file)", dbPath)
	}
	return nil
}

// classifyCLIError translates internal errors to operator-facing
// messages (C4). In --verbose mode the raw error is preserved so a
// developer can still see the sqlite error text.
func classifyCLIError(err error, verbose bool) error {
	if err == nil {
		return nil
	}
	if verbose {
		return err
	}
	if errors.Is(err, engine.ErrEngineSchemaMismatch) {
		return errors.New("ghyll engine: db was written by a newer version of ghyll; upgrade the binary or set --verbose for details")
	}
	low := strings.ToLower(err.Error())
	switch {
	case strings.Contains(low, "no such table"),
		strings.Contains(low, "no such column"):
		return errors.New("ghyll engine: schema mismatch — db may be corrupt or from a different ghyll version (--verbose for details)")
	case strings.Contains(low, "database is locked"),
		strings.Contains(low, "busy"):
		return errors.New("ghyll engine: database is in use by another process (--verbose for details)")
	case strings.Contains(low, "context deadline exceeded"):
		return errors.New("ghyll engine: timed out — use --timeout <seconds> to allow more time (--verbose for details)")
	default:
		// Strip newlines + control chars so a verbatim error doesn't
		// corrupt the terminal.
		return errors.New("ghyll engine: " + sanitizeOneLine(err.Error()))
	}
}
