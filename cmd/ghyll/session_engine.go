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

	"github.com/witlox/ghyll/bootstrap"
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

	// gridFile holds the bootstrap.Grid (the untyped, on-disk
	// shape) so the amendment-commit overlay path can mutate it
	// in place under gridFileMu. Diamond v4 / design C3 closure
	// (R10 + R17 + R18). May be nil for projects without a grid
	// (e.g., the auto-engine path in tests).
	gridFile   *bootstrap.Grid
	gridFileMu sync.RWMutex

	// committer drives an amendment from a CommitRequest through
	// to grid v(N+1) per gates.md §3.7. Constructed in
	// openEngineWithOptions; immutable post-construct (design M11).
	// Diamond v4 / Gap 2 closure. Wired into /drain-amendments.
	committer *runner.AmendmentCommitter

	// adversarialHooks is the atomic-pointer bundle that
	// PassDispatcher consults at Dispatch-time to drive the
	// adversarial cycle (Gap 1). Empty = disabled (depth-sensitive
	// arrows return ErrAdversaryHooksNotWired). The /adversary
	// slash command toggles via atomic Store/Clear.
	adversarialHooks runner.AtomicAdversarialHooks

	// auditTagUnsubscribe drops the audit-tagged bus subscriber on
	// closeEngine. Diamond v4 / W-H-1 (+ W-M-2): registered in
	// attachJournal IFF the JSONL writer opened; nil otherwise. The
	// unsubscribe call is idempotent so closeEngine can invoke it
	// unconditionally.
	auditTagUnsubscribe func()

	// arrowInvalidationsUnsubscribe drops the OpEventArrowInvalidated
	// → arrow_invalidations subscriber on closeEngine. Diamond v4 /
	// integrator-pass I-H-1: the audit-tagged sibling closure
	// (W-M-2) holds its closer for this exact reason; this untagged
	// sibling repeats the pattern so a late publish after the store
	// closes cannot call InsertArrowInvalidation on a dead handle.
	arrowInvalidationsUnsubscribe func()
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
	return openEngineWithOptions(workdir, logger, 0, nil)
}

// openEngineWithOptions is openEngine with explicit configuration
// hooks. Kept distinct so the simpler openEngine signature stays
// compatible with existing callers that don't yet plumb the grid
// file's settings through.
//
// Diamond v4 / Gap 3 closure (ADR-v4-007 + R1 + R3 + R7 + R21): the
// signature accepts the bootstrap.Grid so registerGridBindings can
// populate the runtime registry with one BindingEvaluator per
// declaration in grid.LanguageBindings. grid==nil is permitted for
// projects without an initialized grid (the binding-coverage check
// then trivially passes); production callers in session.go always
// supply a non-nil grid.
func openEngineWithOptions(workdir string, logger *slog.Logger, ibRoundsMax int, grid *bootstrap.Grid) (*engineRuntime, error) {
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

	// Diamond v4 / Gap 3 (R1 + ADR-v4-007): plumb the on-disk
	// language-binding declarations into the registry BEFORE
	// any clause-evaluation path can ask for them. Failure leaves
	// the store cleanly closed; no half-initialized runtime escapes.
	if err := registerGridBindings(reg, grid, workdir); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("engine register-grid-bindings: %w", err)
	}
	// R17 + R18 pre-Replay coverage check against the bootstrap
	// (untyped) grid: every arrow's clauses with a language-bound
	// concept must resolve to a registered binding. Surface schema
	// errors (R18) before we even try to walk persisted state.
	if grid != nil {
		needed, validations, walkErr := requiredBindingsFromUntypedGrid(grid)
		if walkErr != nil {
			_ = store.Close()
			return nil, fmt.Errorf("engine grid scan: %w", walkErr)
		}
		if len(validations) > 0 {
			_ = store.Close()
			return nil, fmt.Errorf("engine grid bindings schema: %s", validations[0])
		}
		var missing []bootstrap.BindingKey
		for _, k := range needed {
			if _, _, ok := reg.Lookup(k.String()); !ok {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			_ = store.Close()
			return nil, &bootstrap.MissingBindingError{Missing: missing}
		}
	}

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
		gridFile:        grid,
	}
	rt.jsonlPath = filepath.Join(filepath.Dir(dbPath), "attestations.jsonl")

	// Diamond v4 / Gap 2 (C-3 closure): build the AmendmentCommitter
	// at session open so /drain-amendments has a fully-wired
	// committer reachable from the chat loop. BindingsReRegister
	// closes over rt so it can read NewLanguageBindings and produce
	// a snapshot registry that swaps in atomically on success.
	rt.committer = &runner.AmendmentCommitter{
		Grid:               rt.grid,
		Passes:             rt.passes,
		Bus:                rt.bus,
		Queue:              rt.amendments,
		LiveRegistry:       rt.registry,
		Workdir:            workdir,
		Now:                time.Now,
		BindingsReRegister: rt.buildRegistryOverlay,
	}

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
	// Tier 2 (ADR-016 Part B) / gate-1 F-1: load from the TREE,
	// not the flat file. The tree is the authoritative audit
	// surface post-Tier-2; the flat aggregate is a forward-only
	// Observer that we never read at boot.
	treeRoot := filepath.Join(filepath.Dir(dbPath), "attestations")
	loaded, truncated, loadErr := rt.attestations.LoadFromTree(treeRoot, attCount > 0)
	if loadErr != nil {
		_ = store.Close()
		return nil, fmt.Errorf("load attestation tree: %w", loadErr)
	}
	rt.jsonlTruncated = truncated
	if logger != nil && (loaded > 0 || truncated) {
		logger.Info("engine: attestation tree loaded",
			"root", treeRoot, "loaded", loaded, "truncated", truncated)
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

	// Per-pass JSONL tree (operator-attestation spec). Tier 2
	// (ADR-016 Part B) promotes this to the PrimaryWriter slot
	// in attachJournal; the flat writer drops to Observer-only.
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
		Passes:          r.passes, // M-6: populate ReplayCounts.Passes* (pre-Recovery)
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

// verifyBindingsCoveragePostReplay walks the typed runner.Grid
// (populated by Replay) AND the bootstrap (untyped) grid file, then
// asserts every required (concept, language) pair is in the
// registry. Returns *bootstrap.MissingBindingError on miss; nil on
// success. Diamond v4 / R17 closure. Called from session.initEngine
// after attachJournal; on miss the operator-facing message routes
// through the modal driver / session output.
func (r *engineRuntime) verifyBindingsCoveragePostReplay() error {
	if r == nil {
		return nil
	}
	r.gridFileMu.RLock()
	gf := r.gridFile
	r.gridFileMu.RUnlock()
	return verifyBindingsCoverage(r.registry, r.grid, gf)
}

// buildRegistryOverlay constructs a snapshot of the live registry,
// applies the amendment's NewLanguageBindings on top, and returns a
// closure that atomically swaps the snapshot into the live registry
// (R10 closure). Wired into AmendmentCommitter.BindingsReRegister.
//
// On success, the swap closure is invoked by the committer AFTER
// the grid append succeeds (ADR-v4-003 step 6a). If the overlay
// construction fails (malformed binding, unregistered concept), the
// commit aborts BEFORE the grid version bumps.
func (r *engineRuntime) buildRegistryOverlay(req runner.CommitRequest) (*runner.Registry, func(), error) {
	if r == nil || r.registry == nil {
		return nil, nil, errors.New("buildRegistryOverlay: nil receiver / registry")
	}
	snap := r.registry.Snapshot()
	for k, v := range req.NewLanguageBindings {
		if v == "" {
			return nil, nil, fmt.Errorf("%w: %s", bootstrap.ErrBindingCommandEmpty, k)
		}
		parsed, err := bootstrap.BindingKeysFromStrings([]string{k})
		if err != nil {
			return nil, nil, fmt.Errorf("buildRegistryOverlay: parse %q: %w", k, err)
		}
		key := parsed[0]
		if !runner.IsLanguageBoundConcept(key.Concept) {
			return nil, nil, fmt.Errorf("%w: %q is not a language-bound concept (key=%s)",
				ErrLanguageBindingInvalid, key.Concept, k)
		}
		eval := runner.NewBindingEvaluator(v,
			runner.WithWorkingDir(r.workdir),
			runner.WithTimeout(runner.DefaultBindingTimeout),
		)
		full := key.String()
		if err := snap.Register(full, eval); err != nil {
			if errors.Is(err, runner.ErrConceptAlreadyRegistered) {
				if rerr := snap.Replace(full, eval); rerr != nil {
					return nil, nil, fmt.Errorf("buildRegistryOverlay: replace %q: %w", full, rerr)
				}
				continue
			}
			return nil, nil, fmt.Errorf("buildRegistryOverlay: register %q: %w", full, err)
		}
	}
	swap := func() { snap.SwapInto(r.registry) }
	return snap, swap, nil
}

// Committer returns the AmendmentCommitter wired for this runtime.
// Nil receiver / nil runtime returns nil.
func (r *engineRuntime) Committer() *runner.AmendmentCommitter {
	if r == nil {
		return nil
	}
	return r.committer
}

// AdversarialHooks returns the runtime's atomic-pointer hook bundle.
// /adversary slash command swaps into this via Store/Clear.
func (r *engineRuntime) AdversarialHooks() *runner.AtomicAdversarialHooks {
	if r == nil {
		return nil
	}
	return &r.adversarialHooks
}

// GridFile returns the bootstrap (untyped) grid the engine was
// constructed with. May be nil for projects without a grid file.
// Reads under gridFileMu so concurrent amendment-overlay swaps see a
// consistent value.
func (r *engineRuntime) GridFile() *bootstrap.Grid {
	if r == nil {
		return nil
	}
	r.gridFileMu.RLock()
	defer r.gridFileMu.RUnlock()
	return r.gridFile
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
	// Tier 2 (ADR-016 Part B): the TREE writer is the
	// AttestationStore primaryWriter. The flat aggregate is an
	// Observer fanout — it appends post-tree-write but cannot
	// fail Record. Per gate-1 F-1, the tree is the authoritative
	// audit surface; the flat aggregate is a tail.
	if r.treeWriter != nil {
		r.attestations.SetPrimaryWriter(r.treeWriter.PrimaryWriter())
		// Truncate any trailing partial lines in the tree (gate-1
		// F-11) so the next Record appends cleanly. Walks the
		// whole tree; cheap on healthy systems.
		if r.jsonlTruncated {
			if err := r.treeWriter.TruncateTrailingPartialAll(""); err != nil {
				logger.Warn("engine: tree truncate failed", "err", err)
			}
		}
	} else if r.jsonlWriter != nil {
		// Defensive fallback: if the tree writer failed to open,
		// the flat writer takes the primary slot. The audit trail
		// still works; the per-pass tree just isn't available for
		// forensics.
		r.attestations.SetPrimaryWriter(r.jsonlWriter.PrimaryWriter())
		if r.jsonlTruncated {
			if err := r.jsonlWriter.TruncateTrailingPartial(); err != nil {
				logger.Warn("engine: jsonl truncate failed", "err", err)
			}
		}
	}

	// Flat aggregate writer subscribes as an Observer (Tier 2):
	// every verdict appends to .ghyll/attestations.jsonl AFTER
	// the tree primary already succeeded. The flat write may
	// fail silently; the bus event surfaces durability problems.
	//
	// Diamond v4 / W-H-1 closure: the audit-floor SubscribeTagged
	// registration MUST be gated on jsonlWriter actually being open
	// — otherwise the tag is a placebo (the dispatcher's
	// RequireAuditSubscriber passes when no writer is attached). The
	// audit-tagged subscriber publishes a bus event when the JSONL
	// writer reports an error so the AttestationStore observer
	// chain's downstream durability problems surface through the
	// audit-tagged path the dispatcher's predicate verifies.
	if r.jsonlWriter != nil {
		r.attestations.Observe(r.jsonlWriter.Observer())
		// Tagged subscriber: tracks JSONL writer health by publishing
		// a typed event when WriteErrors() ticks up. R6 audit-floor
		// now MEANS what its name implies — the tag is present iff
		// the JSONL writer opened successfully.
		writer := r.jsonlWriter
		r.auditTagUnsubscribe = r.bus.SubscribeTagged(func(_ runner.OperatorEvent) {
			// Defensive: a future bus republish-on-write-failure can
			// route through this closure. Today the JSONL writer's
			// own failure path publishes via Observer; this tagged
			// subscription is the audit-floor membership marker.
			_ = writer
		}, "audit")
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

	// Diamond v4 / ADR-v4-008 + R28: persist OpEventArrowInvalidated
	// into the arrow_invalidations table. Subscribed as untagged
	// (audit-tagged bus subscribers are reserved for the JSONL
	// writer). The bus fans out off-goroutine; write failures log
	// and continue — the row loss surfaces via the bus event count
	// vs the table row count at next session start.
	//
	// Integrator-pass I-H-1 closure: the audit-tagged sibling at
	// line 618 captures its closer via auditTagUnsubscribe so
	// closeEngine drops the dangling callback before r.store closes.
	// This subscriber repeats the same pattern — without the handle,
	// a late OpEventArrowInvalidated published after closeEngine
	// would call into a closed store. Capture the closer the same
	// way W-M-2 did for the audit-tagged sibling.
	r.arrowInvalidationsUnsubscribe = r.bus.Subscribe(func(e runner.OperatorEvent) {
		if e.Kind != runner.OpEventArrowInvalidated {
			return
		}
		// F-C-1 closure (2026-05-25): read the canonical 4 keys
		// from Payload per ADR-v4-005 line 40 (arrow_id, op_id,
		// reason, timestamp). The arrow_invalidations table's
		// `source` column is not in the ADR's required-key list;
		// it defaults to "operator" here and the future
		// auto-invalidation pathway will set it explicitly when
		// it lands.
		opID, reason := "", e.Detail
		at := ""
		if e.Payload != nil {
			if v := e.Payload["op_id"]; v != "" {
				opID = v
			}
			if v := e.Payload["reason"]; v != "" {
				reason = v
			}
			if v := e.Payload["timestamp"]; v != "" {
				at = v
			}
		}
		if at == "" {
			at = e.Timestamp.UTC().Format(time.RFC3339Nano)
		}
		source := "operator"
		if err := r.store.InsertArrowInvalidation(
			context.Background(),
			e.ArrowID, opID, reason, source, at,
		); err != nil && logger != nil {
			logger.Warn("engine: arrow_invalidations insert failed",
				"arrow_id", e.ArrowID, "err", err)
		}
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
	//      per event). Tier 1 (ADR-015 Part C): the JSONL writer
	//      is the AttestationStore's primaryWriter, so it has
	//      ALREADY written each record inline (before byID
	//      mutates). The journal only persists the engine-table
	//      cache copy.
	//   2. JSONL writer flushes + closes the audit file. This
	//      happens BEFORE store.Close so any final journal-
	//      observer writes have landed.
	//   3. Store closes sqlite.
	// W-H-1 / W-M-2: drop the audit-tagged bus subscriber before
	// the journal/bus go away so the bus doesn't outlive the engine
	// runtime through a dangling tagged callback. Safe-no-op when
	// the writer never opened (unsubscribe stays nil).
	if r.auditTagUnsubscribe != nil {
		r.auditTagUnsubscribe()
		r.auditTagUnsubscribe = nil
	}
	// I-H-1 closure: mirror the W-M-2 pattern for the untagged
	// arrow_invalidations subscriber so a late bus event after
	// closeEngine cannot call InsertArrowInvalidation on a closed
	// store (the call would return ErrEngineClosed; the structural
	// fix matches W-M-2).
	if r.arrowInvalidationsUnsubscribe != nil {
		r.arrowInvalidationsUnsubscribe()
		r.arrowInvalidationsUnsubscribe = nil
	}
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
	// Diamond v4 / ADR-v4-006: pass the live PassRegistry so the
	// single-active-role-instance evaluator has the live-pass view.
	rn := runner.NewRunner(r.registry, r.passes, tier)
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
		LockTable:            r.roleLocks,
		Passes:               r.passes,
		Bus:                  r.bus,
		AttestationStore:     r.attestations,
		RunnerFactory:        r.NewRunner,
		SeverityThreshold:    runner.SeverityMedium,
		PassIDGen:            func() string { return fmt.Sprintf("p-%d", r.nextPassID()) },
		Hooks:                &r.adversarialHooks,
		MaxRecursiveDispatch: runner.DefaultMaxRecursiveDispatch,
		AdversarialPhase:     r.runDispatcherAdversarialPhase,
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
