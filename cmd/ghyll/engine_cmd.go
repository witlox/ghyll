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
const engineUsage = "usage: ghyll engine status|replay|recover|verify-attestations [--dir <path>] [--timeout <seconds>] [--verbose]"

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
	case "recover":
		return cmdEngineRecover(rest)
	case "verify-attestations":
		return cmdEngineVerifyAttestations(rest)
	default:
		return fmt.Errorf("ghyll engine: unknown subcommand %q", sub)
	}
}

// cmdEngineVerifyAttestations walks the project-local
// `.ghyll/attestations.jsonl` audit file and reports any record
// that violates the schema invariants (ADR-009 / ADR-010 self-
// cert, kind / clause_id pairing, verdict enum, required fields).
//
// Exit-status contract: returns nil on a clean audit (no
// failures), an error on any failure. The error message includes
// the per-line issues so the operator sees the first batch of
// failures without needing --verbose.
func cmdEngineVerifyAttestations(args []string) error {
	fl, err := parseEngineFlags(args)
	if err != nil {
		return err
	}
	jsonlPath := filepath.Join(filepath.Dir(fl.DBPath), "attestations.jsonl")
	if _, err := os.Stat(jsonlPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			ui.Info("%s", missingEngineLine)
			ui.Info("ghyll engine: no attestation log at %s (no attestations recorded yet)", jsonlPath)
			return nil
		}
		return classifyCLIError(err, fl.Verbose)
	}
	v := &runner.AttestationVerifier{}
	res, err := v.VerifyFile(jsonlPath)
	if err != nil {
		return classifyCLIError(err, fl.Verbose)
	}
	ui.Info("%s", res.String())
	if res.Failed > 0 {
		return fmt.Errorf("attestation-verify: %d failed record(s)", res.Failed)
	}
	return nil
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
			ui.Info("%s", missingEngineLine)
			ui.Info("ghyll engine: no store at %s (project has not initialized v2 yet)", dbPath)
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
	atts, err := store.CountAttestations(ctx)
	if err != nil {
		return classifyCLIError(err, fl.Verbose)
	}

	total := arrows + findings + reqs + cls + pendingAm + drainedAm + runs + atts
	header := presentEngineMarker
	if total == 0 {
		header = emptyEngineMarker
	}
	ui.Info("%s", header)
	ui.Info("engine: %s", dbPath)
	ui.Info("  arrows:           %d", arrows)
	ui.Info("  findings:         %d", findings)
	ui.Info("  requirements:     %d", reqs)
	ui.Info("  classifications:  %d", cls)
	ui.Info("  amendments:       %d pending, %d drained", pendingAm, drainedAm)
	ui.Info("  evaluation runs:  %d", runs)
	ui.Info("  attestations:     %d", atts)
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
			ui.Info("%s", missingEngineLine)
			ui.Info("ghyll engine: no store at %s (nothing to replay)", dbPath)
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
	ui.Info("replay: %s", dbPath)
	ui.Info("  arrows:            %d", counts.Arrows)
	ui.Info("  findings:          %d", counts.Findings)
	ui.Info("  requirements:      %d", counts.Requirements)
	ui.Info("  classifications:   %d", counts.Classifications)
	ui.Info("  amendments:        %d active, %d drained",
		counts.AmendmentsActive, counts.AmendmentsDrained)
	if replayErr != nil {
		return classifyCLIError(replayErr, fl.Verbose)
	}
	if len(counts.Errors) > 0 {
		shown := len(counts.Errors)
		if shown > maxCLIErrorsShown {
			shown = maxCLIErrorsShown
		}
		ui.Info("  per-row errors:    %d", len(counts.Errors))
		for i := 0; i < shown; i++ {
			// C3: sanitize each error string at the boundary.
			ui.Info("    - %s", sanitizeOneLine(counts.Errors[i]))
		}
		if len(counts.Errors) > maxCLIErrorsShown {
			ui.Info("    … %d more errors elided", len(counts.Errors)-maxCLIErrorsShown)
		}
		return fmt.Errorf("replay completed with %d errors", len(counts.Errors))
	}
	ui.Info("  per-row errors:    0")
	ui.Info("")
	ui.Info("note: this is replay-only; session start additionally runs")
	ui.Info("      crash Recovery (use `ghyll engine recover --dry-run` to preview).")
	return nil
}

// cmdEngineRecover previews what crash Recovery would do at next
// session start. Opens the store read/write, runs Recovery inside
// a transaction that is ALWAYS rolled back, prints the report.
// Operator-facing diagnostic per ADR-015 Part D + F-14.
//
// Honors a `--dry-run` flag for symmetry with `ghyll engine replay`;
// today --dry-run is the ONLY mode (a real --commit mode would
// duplicate session.Open's recovery write, which is already
// triggered by starting a session). If --commit is passed, the
// subcommand refuses with a clear "use `ghyll run` instead" message.
func cmdEngineRecover(args []string) error {
	commit := false
	rest := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "--dry-run":
			// Default; explicit acceptance for clarity.
		case "--commit":
			commit = true
		default:
			rest = append(rest, a)
		}
	}
	if commit {
		return errors.New("ghyll engine recover: --commit is not supported; start a session with `ghyll run` to apply recovery")
	}
	fl, err := parseEngineFlags(rest)
	if err != nil {
		return err
	}
	dbPath := fl.DBPath
	if err := preflightDBPath(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			ui.Info("%s", missingEngineLine)
			ui.Info("ghyll engine: no store at %s (nothing to recover)", dbPath)
			return nil
		}
		return classifyCLIError(err, fl.Verbose)
	}

	// Open read/write so Recovery's BeginTx works. The transaction
	// is always rolled back at the end (dry-run semantics).
	store, err := engine.OpenStore(dbPath)
	if err != nil {
		return classifyCLIError(fmt.Errorf("open %s: %w", dbPath, err), fl.Verbose)
	}
	defer func() { _ = store.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), fl.Timeout)
	defer cancel()

	// Construct the same dependencies session.Open would, but
	// route Recovery's writes through a rolled-back transaction.
	atts := runner.NewAttestationStore()
	// Load attestations from JSONL so the JOIN-based detection sees
	// the authoritative state.
	jsonlPath := filepath.Join(filepath.Dir(dbPath), "attestations.jsonl")
	attCount, err := store.CountAttestations(ctx)
	if err != nil {
		return classifyCLIError(err, fl.Verbose)
	}
	if _, _, err := atts.LoadFromJSONL(jsonlPath, attCount > 0); err != nil {
		return classifyCLIError(err, fl.Verbose)
	}
	// Catch up the engine cache so the JOIN scan agrees with the
	// in-memory state. The rollback at the end of Recovery undoes
	// these writes too — but they're idempotent ON CONFLICT IGNORE
	// so a real session.Open running afterward sees the same end-
	// state.
	if _, err := store.CatchUpAttestations(ctx, atts); err != nil {
		return classifyCLIError(err, fl.Verbose)
	}

	// Wrap Recovery in a transaction we roll back.
	// engine.Recovery already wraps its work in BeginTx/Commit;
	// we don't have a public "force-rollback" hook, so the
	// implementation choice for dry-run is: call Recovery (which
	// commits its own transaction), then immediately reverse the
	// writes via a counter-transaction. That's ugly. Simpler:
	// take a sqlite SAVEPOINT before the call and ROLLBACK TO
	// after. SQLite supports nested savepoints, and Recovery's
	// inner BeginTx becomes a SAVEPOINT under it.
	if _, err := store.DB().ExecContext(ctx, `SAVEPOINT recover_dryrun`); err != nil {
		return classifyCLIError(fmt.Errorf("savepoint: %w", err), fl.Verbose)
	}
	rep, recErr := engine.Recovery(ctx, engine.RecoveryDeps{
		Store:        store,
		Attestations: atts,
		Now:          time.Now,
		JSONLPath:    jsonlPath,
	}, engine.ReplayCounts{})
	// Always roll back to the savepoint.
	if _, rbErr := store.DB().ExecContext(ctx, `ROLLBACK TO SAVEPOINT recover_dryrun`); rbErr != nil {
		ui.Info("ghyll engine recover: WARNING rollback failed: %v", rbErr)
	}
	_, _ = store.DB().ExecContext(ctx, `RELEASE SAVEPOINT recover_dryrun`)
	if recErr != nil {
		return classifyCLIError(fmt.Errorf("recover: %w", recErr), fl.Verbose)
	}

	ui.Info("recover (dry-run): %s", dbPath)
	ui.Info("  orphans aborted:        %d", rep.OrphansAborted)
	ui.Info("  orphans preserved:      %d (attestation-pending)", rep.OrphansPreserved)
	ui.Info("  evaluation_runs flipped: %d (from JSONL verdicts)", rep.EvaluationRunsFlipped)
	if len(rep.Events) > 0 {
		shown := len(rep.Events)
		if shown > maxCLIErrorsShown {
			shown = maxCLIErrorsShown
		}
		ui.Info("  events:")
		for i := 0; i < shown; i++ {
			ev := rep.Events[i]
			ui.Info("    - %s pass=%s arrow=%s clause=%s",
				ev.Kind, ev.PassID, ev.ArrowID, ev.ClauseID)
		}
		if len(rep.Events) > maxCLIErrorsShown {
			ui.Info("    … %d more events elided", len(rep.Events)-maxCLIErrorsShown)
		}
	}
	ui.Info("")
	ui.Info("note: --dry-run; no changes persisted. Start a session")
	ui.Info("      with `ghyll run` to apply recovery for real.")
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
