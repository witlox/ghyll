package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

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
// The runner.Runner is per-arrow-pass per gates.md §11; the session
// holds the Registry and constructs Runners on demand via NewRunner.
// This file's Session.Engine field is the integration point for
// commands that need to evaluate v2 clauses against the live caches.

// engineRuntime bundles every v2 surface a session needs.
type engineRuntime struct {
	store           *engine.Store
	journal         *engine.Journal
	findings        *runner.FindingsStore
	classifications *runner.ClassificationsStore
	grid            *runner.Grid
	amendments      *runner.AmendmentQueue
	registry        *runner.Registry

	// dbPath is recorded so close + diagnostics can reference it.
	dbPath string
}

// openEngine creates the engine.Store + fresh in-memory caches.
// Does NOT attach the journal — the caller must call replayEngine
// first, then attachJournal.
func openEngine(workdir string, logger *log.Logger) (*engineRuntime, error) {
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
// ($workdir/.ghyll/engine.db) so different projects don't share
// state. The directory is created on demand by openEngine.
func defaultEngineDBPath(workdir string) (string, error) {
	if workdir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		workdir = cwd
	}
	return filepath.Join(workdir, ".ghyll", "engine.db"), nil
}

// replayEngine loads every persisted entity into the in-memory
// caches. Per phase-9 J9: per-row errors accumulate; replay does
// NOT abort on a single malformed row. Returns the counts so the
// session can surface diagnostics.
func (r *engineRuntime) replayEngine(ctx context.Context) (engine.ReplayCounts, error) {
	return engine.Replay(ctx, r.store, engine.ReplayTargets{
		Findings:        r.findings,
		Classifications: r.classifications,
		Grid:            r.grid,
		Amendments:      r.amendments,
	})
}

// attachJournal wires the journal to every observer surface. MUST
// be called AFTER replayEngine — otherwise the replayed mutations
// write back to the same sqlite rows they came from.
//
// The Runner is the per-clause dispatcher; observers on it journal
// every EvaluationRun. The session creates fresh Runners per arrow
// pass (Phase 11+ work); they all share this engine.
func (r *engineRuntime) attachJournal(logger *log.Logger) {
	r.journal = engine.NewJournal(r.store, logger)
	r.journal.AttachFindings(r.findings)
	r.journal.AttachClassifications(r.classifications)
	r.journal.AttachGrid(r.grid)
	r.journal.AttachAmendments(r.amendments)
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
// when never opened (nil receiver).
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

// NewRunner returns a fresh Runner from the engine's registry. The
// session constructs one per arrow pass; tier is set by the
// dispatcher per gates.md §8 routing (phase-11 wires this end-to-end).
func (r *engineRuntime) NewRunner() *runner.Runner {
	if r == nil || r.registry == nil {
		return nil
	}
	rn := runner.NewRunner(r.registry)
	r.attachRunner(rn)
	return rn
}
