package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/witlox/ghyll/engine"
	"github.com/witlox/ghyll/runner"
)

// Phase-10 engine wiring. The session owns the v2 in-memory caches
// (FindingsStore, ClassificationsStore, Grid, AmendmentQueue) and
// the engine.Store + Journal that persist them.
//
// Lifecycle:
//
//  1. openEngine — open sqlite at .ghyll/engine.db; construct fresh
//     in-memory caches.
//  2. replayEngine — load persisted entities into the caches BEFORE
//     attaching the journal (otherwise replay writes back to the
//     same rows it just read).
//  3. attachJournal — register observers + EvaluationRun hook so
//     subsequent mutations journal.
//  4. closeEngine — close the journal (drains the consumer
//     goroutine) then the store.
//
// Validation-pass-10 W6: replayDone / journalAttached flags enforce
// the ordering invariant at runtime — replayEngine errors if the
// journal is already attached; attachJournal errors if replay has
// not run. W1: attachJournal is idempotent (returns an error on a
// second call rather than leaking goroutines).

// engineRuntime bundles every v2 surface a session needs.
type engineRuntime struct {
	store           *engine.Store
	journal         *engine.Journal
	findings        *runner.FindingsStore
	classifications *runner.ClassificationsStore
	grid            *runner.Grid
	amendments      *runner.AmendmentQueue
	registry        *runner.Registry

	dbPath string

	replayDone      bool
	journalAttached bool
}

// Engine-runtime errors. Surfaced so callers can switch on them.
var (
	ErrEngineReplayBeforeAttach = errors.New("engine: replay must run before attachJournal")
	ErrEngineAttachTwice        = errors.New("engine: attachJournal called twice")
	ErrEngineReplayAfterAttach  = errors.New("engine: replayEngine called after attachJournal")
)

// openEngine creates the engine.Store + fresh in-memory caches.
// Does NOT attach the journal — the caller must call replayEngine
// first, then attachJournal.
func openEngine(workdir string, logger *log.Logger) (*engineRuntime, error) {
	_ = logger
	dbPath, err := defaultEngineDBPath(workdir)
	if err != nil {
		return nil, fmt.Errorf("engine path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("engine dir: %w", err)
	}
	store, err := engine.OpenStore(dbPath)
	if err != nil {
		return nil, fmt.Errorf("engine open %s: %w", dbPath, err)
	}
	reg := runner.NewRegistry()
	runner.RegisterBuiltins(reg)
	return &engineRuntime{
		store:           store,
		findings:        runner.NewFindingsStore(),
		classifications: runner.NewClassificationsStore(),
		grid:            runner.NewGrid(),
		amendments:      runner.NewAmendmentQueue(),
		registry:        reg,
		dbPath:          dbPath,
	}, nil
}

// defaultEngineDBPath returns the project-local engine path
// ($workdir/.ghyll/engine.db). Per validation-pass-10 W7 the
// resolved path is required to stay under the absolute workdir —
// `..` traversal and symlink escapes are rejected.
func defaultEngineDBPath(workdir string) (string, error) {
	if workdir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		workdir = cwd
	}
	absDir, err := filepath.Abs(workdir)
	if err != nil {
		return "", fmt.Errorf("abs %q: %w", workdir, err)
	}
	target := filepath.Join(absDir, ".ghyll", "engine.db")
	// W7: ensure the resolved path lives under absDir. If the
	// parent dir exists already, evaluate symlinks to catch
	// escapes; if it doesn't, the path is freshly being created
	// and join-containment is sufficient.
	if _, err := os.Stat(filepath.Dir(target)); err == nil {
		resolved, err := filepath.EvalSymlinks(filepath.Dir(target))
		if err == nil {
			resolved = filepath.Clean(resolved)
			abs := filepath.Clean(absDir)
			rel, err := filepath.Rel(abs, resolved)
			if err != nil || strings.HasPrefix(rel, "..") {
				return "", fmt.Errorf("engine path %q escapes workdir %q", resolved, abs)
			}
		}
	}
	return target, nil
}

// replayEngine loads every persisted entity into the in-memory
// caches. Per phase-9 J9: per-row errors accumulate. Per W6: errors
// if the journal is already attached (recursive journaling would
// follow).
func (r *engineRuntime) replayEngine(ctx context.Context) (engine.ReplayCounts, error) {
	if r == nil {
		return engine.ReplayCounts{}, errors.New("engine: nil runtime")
	}
	if r.journalAttached {
		return engine.ReplayCounts{}, ErrEngineReplayAfterAttach
	}
	counts, err := engine.Replay(ctx, r.store, engine.ReplayTargets{
		Findings:        r.findings,
		Classifications: r.classifications,
		Grid:            r.grid,
		Amendments:      r.amendments,
	})
	if err == nil {
		r.replayDone = true
	}
	return counts, err
}

// attachJournal wires the journal to every observer surface. Per
// W1: returns ErrEngineAttachTwice on second call rather than
// leaking observers + goroutines. Per W6: returns
// ErrEngineReplayBeforeAttach if replay has not run.
func (r *engineRuntime) attachJournal(logger *log.Logger) error {
	if r == nil {
		return errors.New("engine: nil runtime")
	}
	if r.journalAttached {
		return ErrEngineAttachTwice
	}
	if !r.replayDone {
		return ErrEngineReplayBeforeAttach
	}
	r.journal = engine.NewJournal(r.store, logger)
	r.journal.AttachFindings(r.findings)
	r.journal.AttachClassifications(r.classifications)
	r.journal.AttachGrid(r.grid)
	r.journal.AttachAmendments(r.amendments)
	r.journalAttached = true
	return nil
}

// attachRunner registers the engine's EvaluationRun observer on a
// runner. Call per-Runner; the session creates Runners on demand
// (one per arrow pass) and wires them through this helper.
func (r *engineRuntime) attachRunner(rn *runner.Runner) {
	if r.journal != nil {
		r.journal.AttachRunner(rn)
	}
}

// closeEngine drains the journal and closes the store. Safe to call
// when never opened (nil receiver). Per W10: a 30s overall deadline
// would require an async drain — current Journal.Close is synchronous
// with a per-event 5s timeout, so we surface dropped-events count
// (W11) at shutdown rather than force-cancel mid-drain.
func (r *engineRuntime) closeEngine() {
	if r == nil {
		return
	}
	if r.journal != nil {
		r.journal.Close()
	}
	if r.store != nil {
		_ = r.store.Close()
	}
}

// NewRunner returns a fresh Runner from the engine's registry at
// the given tier. Per W2: tier is a required parameter — passing
// DepthRankNone here disables the §6/§7.1 short-circuit, so the
// dispatcher MUST supply the actual depth tier it is running at.
func (r *engineRuntime) NewRunner(tier runner.DepthRank) *runner.Runner {
	if r == nil || r.registry == nil {
		return nil
	}
	rn := runner.NewRunner(r.registry).WithActualTier(tier)
	r.attachRunner(rn)
	return rn
}
