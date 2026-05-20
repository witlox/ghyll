package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
//
// Integrator-pass H1: lifecycleMu protects the replayDone /
// journalAttached transition so the flag-check + journal-create
// is atomic. Today's session.go calls these on a single goroutine,
// but the lock makes the invariant durable against future
// concurrent NewSession-like call sites.
type engineRuntime struct {
	store           *engine.Store
	journal         *engine.Journal
	findings        *runner.FindingsStore
	classifications *runner.ClassificationsStore
	grid            *runner.Grid
	amendments      *runner.AmendmentQueue
	registry        *runner.Registry

	// Phase-11 production wiring (Tier 0 of prod-readiness plan).
	// Each component is constructed in openEngine, replayed (if it
	// has persisted state) in replayEngine, then attached to the
	// journal in attachJournal.
	attestations *runner.AttestationStore
	roleLocks    *runner.RoleContextLockTable
	bus          *runner.OperatorBus
	passes       *runner.PassRegistry
	ibTracker    *runner.InsufficientBasisTracker
	jsonlWriter  *runner.AttestationJSONLWriter
	treeWriter   *runner.AttestationTreeWriter

	dbPath string
	// jsonlPath is the path to .ghyll/attestations.jsonl (resolved
	// once at openEngine + reused at LoadFromJSONL).
	jsonlPath string
	// workdir is the resolved project root; needed by the JSONL
	// writer to land .ghyll/attestations.jsonl alongside engine.db.
	workdir string
	// ibRoundsMax is the configured `insufficient-basis-rounds-max`
	// threshold (bootstrap.GridFile). Zero disables escalation.
	ibRoundsMax int

	// recoveryReport captures the engine.Recovery output for
	// session.Open to surface to the operator on chat-loop start.
	// Tier 1 (ADR-015 Part D). Empty when recovery was a no-op.
	recoveryReport engine.RecoveryReport
	// jsonlTruncated is set by openEngine's LoadFromJSONL when the
	// audit file's trailing line was partial (F-6 lenient mode).
	// attachJournal calls AttestationJSONLWriter.TruncateAt after
	// the journal is wired so the next Record overwrites the bad
	// bytes cleanly.
	jsonlTruncated bool
	// catchUpOverrideEvents captures content-conflict overrides
	// surfaced by CatchUpAttestations (H-3 / G2-F-7). session.Open
	// folds these into the recovery banner.
	catchUpOverrideEvents []runner.OperatorEvent

	lifecycleMu     sync.Mutex
	replayDone      bool
	journalAttached bool

	// passIDSeq is the monotonic counter behind dispatcher's
	// PassIDGen. atomic.Uint64 so concurrent dispatchers (a
	// theoretical future where one runtime backs multiple
	// concurrent passes — only legal under disjoint
	// (role, context) tuples) don't collide on IDs.
	passIDSeq atomic.Uint64
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
//
// Tier-0 wiring: also constructs the v2 runtime components
// (RoleContextLockTable, AttestationStore, OperatorBus,
// PassRegistry, InsufficientBasisTracker) and opens the
// AttestationJSONLWriter. ibRoundsMax is loaded by the caller from
// the grid file's `insufficient-basis-rounds-max` setting; pass 0
// to disable escalation.
func openEngine(workdir string, logger *slog.Logger) (*engineRuntime, error) {
	return openEngineWithOptions(workdir, logger, 0)
}

// openEngineWithOptions is openEngine with explicit configuration
// hooks. Kept distinct so the simpler openEngine signature stays
// compatible with existing callers that don't yet plumb the grid
// file's settings through.
func openEngineWithOptions(workdir string, logger *slog.Logger, ibRoundsMax int) (*engineRuntime, error) {
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

	// Tier-0: v2 runtime components. Bus is constructed first; the
	// tracker takes a reference so it can publish escalation events.
	bus := runner.NewOperatorBus()
	rt := &engineRuntime{
		store:           store,
		findings:        runner.NewFindingsStore(),
		classifications: runner.NewClassificationsStore(),
		grid:            runner.NewGrid(),
		amendments:      runner.NewAmendmentQueue(),
		registry:        reg,
		attestations:    runner.NewAttestationStore(),
		roleLocks:       runner.NewRoleContextLockTable(),
		bus:             bus,
		passes:          runner.NewPassRegistry(),
		ibTracker:       runner.NewInsufficientBasisTracker(ibRoundsMax, bus),
		dbPath:          dbPath,
		workdir:         workdir,
		ibRoundsMax:     ibRoundsMax,
	}
	rt.jsonlPath = filepath.Join(filepath.Dir(dbPath), "attestations.jsonl")

	// Tier 1 (ADR-015 Part C): JSONL is source of truth. Load
	// records from the audit file BEFORE engine.Replay so the
	// in-memory attestations cache is authoritative; Replay then
	// catches up the engine table from the in-memory state.
	// engineHasRows tells LoadFromJSONL whether a missing file is
	// fresh (count=0 OK) or fatal (count>0 → audit lost).
	attCount, attErr := store.CountAttestations(context.Background())
	if attErr != nil {
		_ = store.Close()
		return nil, fmt.Errorf("count attestations: %w", attErr)
	}
	loaded, truncated, loadErr := rt.attestations.LoadFromJSONL(rt.jsonlPath, attCount > 0)
	if loadErr != nil {
		_ = store.Close()
		return nil, fmt.Errorf("load attestation jsonl: %w", loadErr)
	}
	rt.jsonlTruncated = truncated
	if logger != nil && (loaded > 0 || truncated) {
		logger.Info("engine: attestation jsonl loaded",
			"path", rt.jsonlPath, "loaded", loaded, "truncated", truncated)
	}
	// Engine cache catches up from JSONL before Replay/Recovery
	// (ADR-015 Part C). Recovery's JOIN-based detection consults
	// the engine table, so the catch-up MUST happen before
	// Recovery runs. H-3 (G2-F-7): wrapped in a transaction; on
	// content conflict JSONL wins; override events surfaced via
	// the recoveryReport on session.Open.
	_, overrideEvents, err := store.CatchUpAttestations(context.Background(), rt.attestations)
	if err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("catch-up attestations: %w", err)
	}
	rt.catchUpOverrideEvents = overrideEvents

	// JSONL audit writer is CONSTRUCTED here but NOT subscribed
	// yet — subscription happens in attachJournal AFTER replay so
	// replayed Record calls don't double-append to the audit file.
	//
	// Failure to construct the writer is non-fatal (the engine
	// table is the source of truth per ADR-010); we surface via
	// slog and continue with the JSONL observer disabled.
	jw, jerr := runner.NewAttestationJSONLWriter(rt.jsonlPath)
	if jerr == nil {
		rt.jsonlWriter = jw.WithBus(rt.bus)
	} else if logger != nil {
		logger.Warn("engine: attestation JSONL writer unavailable",
			"path", rt.jsonlPath, "err", jerr)
	}

	// Per-role-pair JSONL tree per the operator-attestation spec.
	// Subscribed alongside the flat writer in attachJournal.
	// Failure to open is non-fatal — same contract as the flat
	// writer.
	treeRoot := filepath.Join(filepath.Dir(dbPath), "attestations")
	tw, terr := runner.NewAttestationTreeWriter(treeRoot)
	if terr == nil {
		rt.treeWriter = tw.WithBus(rt.bus)
	} else if logger != nil {
		logger.Warn("engine: attestation tree writer unavailable",
			"root", treeRoot, "err", terr)
	}

	return rt, nil
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
	r.lifecycleMu.Lock()
	if r.journalAttached {
		r.lifecycleMu.Unlock()
		return engine.ReplayCounts{}, ErrEngineReplayAfterAttach
	}
	r.lifecycleMu.Unlock()
	// Replay itself can take many seconds on a large DB; do not hold
	// the lock during the disk I/O. The journalAttached check above
	// + the lock around the replayDone write below is sufficient
	// because attachJournal also takes the lock and refuses if
	// replayDone is false.
	counts, err := engine.Replay(ctx, r.store, engine.ReplayTargets{
		Findings:        r.findings,
		Classifications: r.classifications,
		Grid:            r.grid,
		Amendments:      r.amendments,
		Attestations:    r.attestations,
	})
	if err != nil {
		return counts, err
	}
	// Tier 1 (ADR-015 Part D): run Recovery immediately after a
	// clean Replay, BEFORE attachJournal. Recovery's writes go
	// directly to the store; without observers attached, they don't
	// re-journal. F-13: Recovery refuses on dirty replay (counts.Errors
	// non-empty).
	report, recErr := engine.Recovery(ctx, engine.RecoveryDeps{
		Store:        r.store,
		Passes:       r.passes,
		Attestations: r.attestations,
		LockTable:    r.roleLocks,
		Bus:          r.bus, // G2-I-5: resumed passes bridge to bus
		IBTracker:    r.ibTracker,
		JSONLPath:    r.jsonlPath,
		Now:          time.Now,
	}, counts)
	if recErr != nil {
		return counts, fmt.Errorf("engine recovery: %w", recErr)
	}
	// C-4 / G2-F-4: surface JSONL truncation through RecoveryReport
	// so session.Open's banner picks it up; the bus has zero
	// subscribers at this point (F-18 invariant).
	if r.jsonlTruncated {
		report.JSONLTruncatedSkipped = 1 // event count; the byte offset is in the writer
		report.Events = append(report.Events, runner.OperatorEvent{
			Kind:   runner.OpEventRecoveryJSONLTruncated,
			Detail: "trailing partial line in " + r.jsonlPath + " skipped at load",
		})
	}
	r.recoveryReport = report

	r.lifecycleMu.Lock()
	r.replayDone = true
	r.lifecycleMu.Unlock()
	return counts, nil
}

// RecoveryReport returns the captured Recovery output from the
// most recent replayEngine call. Empty when Recovery was a no-op
// or has not run.
func (r *engineRuntime) RecoveryReport() engine.RecoveryReport {
	if r == nil {
		return engine.RecoveryReport{}
	}
	return r.recoveryReport
}

// attachJournal wires the journal to every observer surface. Per
// W1: returns ErrEngineAttachTwice on second call rather than
// leaking observers + goroutines. Per W6: returns
// ErrEngineReplayBeforeAttach if replay has not run.
func (r *engineRuntime) attachJournal(logger *slog.Logger) error {
	if r == nil {
		return errors.New("engine: nil runtime")
	}
	// H-1 (G2-F-5): hoist the nil-logger guard so every subsequent
	// logger.X call in attachJournal is safe even when callers
	// (session.go:281) pass nil.
	if logger == nil {
		logger = slog.Default()
	}
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	if r.journalAttached {
		return ErrEngineAttachTwice
	}
	if !r.replayDone {
		return ErrEngineReplayBeforeAttach
	}
	// Construct the Journal AFTER replayDone but BEFORE flipping
	// journalAttached so no observer can fire on a half-initialized
	// Journal even if a concurrent NewRunner sneaks in. The
	// AttachX calls themselves take the runner's write lock so
	// observer registration is serialized with the runner.
	r.journal = engine.NewJournal(r.store, logger)
	r.journal.AttachFindings(r.findings)
	r.journal.AttachClassifications(r.classifications)
	r.journal.AttachGrid(r.grid)
	r.journal.AttachAmendments(r.amendments)
	// Tier-0: attestations persist via the journal observer pattern
	// (ADR-010). Attach AFTER replay so replayed Record calls don't
	// re-enqueue rows that are already in the table.
	r.journal.AttachAttestations(r.attestations)
	// Tier 1: passes persist via PassRegistry observer.
	r.journal.AttachPasses(r.passes)

	// Tier 1 (ADR-015 Part C): JSONL is source of truth. Set the
	// primaryWriter so AttestationStore.Record blocks on a JSONL
	// fsync failure (ErrAttestationAuditWriteFailed) instead of
	// silently mutating byID with a missing audit row. Failure
	// modes covered: disk full, fsync error, writer closed.
	if r.jsonlWriter != nil {
		r.attestations.SetPrimaryWriter(r.jsonlWriter.PrimaryWriter())
		// Truncate the trailing partial line (if Load detected one)
		// so the next Record overwrites it cleanly (F-6).
		if r.jsonlTruncated {
			if err := r.jsonlWriter.TruncateTrailingPartial(); err != nil {
				logger.Warn("engine: jsonl truncate failed", "err", err)
			}
		}
	}

	// Per-role-pair tree writer: same post-replay subscription
	// rule. Tree files complement the flat JSONL — operators can
	// drill into one pass's verdicts under
	//   <workdir>/.ghyll/attestations/v<N>/<context>/stratum-<S>/
	//   <role-pair>/<pass-id>.jsonl
	if r.treeWriter != nil {
		r.attestations.Observe(r.treeWriter.Observer())
	}

	// InsufficientBasisTracker subscribes to attestation events so
	// every operator verdict pulses the counter. Three consecutive
	// insufficient-basis verdicts on the same clause (or whatever
	// the grid's max is) fire OpEventInsufficientBasisRoundsExceeded.
	r.attestations.Observe(func(e runner.AttestationEvent) {
		if e.Kind != runner.AttestationEventRecord {
			return
		}
		r.ibTracker.Record(e.Record.ArrowID, e.Record.ClauseID, e.Record.Verdict)
	})

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
	// Teardown order:
	//   1. Journal drains pending events (synchronous up to 5s
	//      per event). Last attestation Record events publish to
	//      the AttestationStore Observer, which writes to the
	//      JSONL file.
	//   2. JSONL writer flushes + closes the audit file. Doing
	//      this BEFORE store.Close ensures any final events that
	//      the journal sent to the AttestationStore (which the
	//      JSONL writer subscribes to) have landed on disk.
	//   3. Store closes sqlite.
	if r.journal != nil {
		r.journal.Close()
	}
	if r.jsonlWriter != nil {
		_ = r.jsonlWriter.Close()
	}
	if r.treeWriter != nil {
		_ = r.treeWriter.Close()
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

// dispatcher returns a fully-configured PassDispatcher that drives
// arrow execution through this engine runtime. The dispatcher
// owns: lock acquisition, pass registration, Runner construction
// at the right tier, clause iteration, and arrow-status
// derivation. ADR-011 + ADR-010 are the spec.
//
// The PassIDGen is process-monotonic — every Dispatch call gets a
// fresh ID derived from the runtime's atomic counter so collisions
// across concurrent dispatchers (if a future code path ever spawns
// more than one) are impossible.
//
// Returns nil if the runtime is nil or has not been opened.
func (r *engineRuntime) dispatcher() *runner.PassDispatcher {
	if r == nil || r.registry == nil {
		return nil
	}
	return &runner.PassDispatcher{
		LockTable:         r.roleLocks,
		Passes:            r.passes,
		Bus:               r.bus,
		AttestationStore:  r.attestations,
		RunnerFactory:     r.NewRunner,
		SeverityThreshold: runner.SeverityMedium,
		PassIDGen:         func() string { return fmt.Sprintf("p-%d", r.nextPassID()) },
	}
}

// nextPassID returns a fresh monotonic pass-id counter value.
// Safe for concurrent callers (uses atomic.Uint64).
func (r *engineRuntime) nextPassID() uint64 {
	return r.passIDSeq.Add(1)
}

// RunArrow is the session-level entry point for executing one
// arrow's clauses through the gate-and-arrow runtime. Wraps the
// dispatcher so callers (chat-loop, future `ghyll arrow run` CLI,
// BDD harness) have one production-grade invocation.
//
// Returns the dispatcher's *DispatchResult on success, or an
// error if the engine is nil / arrow is invalid / lock is held by
// another pass. Surfaces *runner.ErrRoleContextBusy directly so
// callers can branch on busy vs other errors.
func (r *engineRuntime) RunArrow(ctx context.Context, role, ctxName string, def runner.ArrowDefinition, tier runner.DepthRank) (*runner.DispatchResult, error) {
	if r == nil {
		return nil, errors.New("engine: nil runtime")
	}
	d := r.dispatcher()
	if d == nil {
		return nil, errors.New("engine: dispatcher unavailable")
	}
	return d.Dispatch(ctx, runner.DispatchRequest{
		Role:        role,
		Context:     ctxName,
		Arrow:       def,
		ActualTier:  tier,
		GridVersion: r.grid.Version(),
		ProjectDir:  r.workdir,
	})
}

// --- Tier-0 accessors -------------------------------------------------
//
// Expose the v2 runtime components so the dispatcher (chat-loop +
// pass driver) and the engine-status CLI can read them. Returning
// nil from these on a nil receiver lets callers handle a "no
// engine attached" project gracefully.

// AttestationStore returns the in-memory attestation cache.
func (r *engineRuntime) AttestationStore() *runner.AttestationStore {
	if r == nil {
		return nil
	}
	return r.attestations
}

// RoleLocks returns the per-(role, context) lock table.
func (r *engineRuntime) RoleLocks() *runner.RoleContextLockTable {
	if r == nil {
		return nil
	}
	return r.roleLocks
}

// Bus returns the operator event bus.
func (r *engineRuntime) Bus() *runner.OperatorBus {
	if r == nil {
		return nil
	}
	return r.bus
}

// Passes returns the live-pass registry.
func (r *engineRuntime) Passes() *runner.PassRegistry {
	if r == nil {
		return nil
	}
	return r.passes
}

// InsufficientBasisTracker returns the consecutive-rounds counter.
func (r *engineRuntime) InsufficientBasisTracker() *runner.InsufficientBasisTracker {
	if r == nil {
		return nil
	}
	return r.ibTracker
}

// Findings returns the in-memory FindingsStore. Exposed so the
// dispatcher + status CLI can read finding state.
func (r *engineRuntime) Findings() *runner.FindingsStore {
	if r == nil {
		return nil
	}
	return r.findings
}

// Grid returns the in-memory Grid.
func (r *engineRuntime) Grid() *runner.Grid {
	if r == nil {
		return nil
	}
	return r.grid
}

// Amendments returns the in-memory AmendmentQueue.
func (r *engineRuntime) Amendments() *runner.AmendmentQueue {
	if r == nil {
		return nil
	}
	return r.amendments
}

// Classifications returns the in-memory ClassificationsStore.
func (r *engineRuntime) Classifications() *runner.ClassificationsStore {
	if r == nil {
		return nil
	}
	return r.classifications
}

// Store returns the persistent engine.Store. Used by the status
// CLI for direct queries that don't need to round-trip through the
// in-memory caches.
func (r *engineRuntime) Store() *engine.Store {
	if r == nil {
		return nil
	}
	return r.store
}
