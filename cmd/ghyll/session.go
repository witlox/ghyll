package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	gocontext "context"
	"github.com/witlox/ghyll/bootstrap"
	"github.com/witlox/ghyll/cmd/ghyll/modal"
	"github.com/witlox/ghyll/config"
	ghyllcontext "github.com/witlox/ghyll/context"
	"github.com/witlox/ghyll/dialect"
	"github.com/witlox/ghyll/memory"
	"github.com/witlox/ghyll/runner"
	"github.com/witlox/ghyll/stream"
	"github.com/witlox/ghyll/tool"
	"github.com/witlox/ghyll/types"
	"github.com/witlox/ghyll/ui"
	"github.com/witlox/ghyll/workflow"
	"time"
)

const maxToolDepth = 50

// Session is the ghyll session state machine.
type Session struct {
	cfg          *config.Config
	store        *memory.Store
	streamClient *stream.Client
	ctxManager   *ghyllcontext.Manager
	syncer       *memory.Syncer
	vaultClient  *memory.VaultClient
	deviceKey    *memory.DeviceKey
	embedder     *memory.Embedder

	activeModel  string
	modelLocked  bool
	deepOverride bool
	planMode     bool // invariant 36: advisory, invariant 37: survives compaction
	toolDepth    int
	sessionID    string
	workdir      string

	// Dialect functions resolved for active model
	systemPrompt     func(string) string
	planModePrompt   func() string
	buildMessages    func([]types.Message, string) []map[string]any
	parseToolCalls   func(json.RawMessage) ([]types.ToolCall, error)
	compactionPrompt func() string
	tokenCount       func([]types.Message) int
	handoffSummary   func(memory.Checkpoint, []types.Message) []types.Message

	// Workflow (project instructions + slash commands only per ADR-008).
	wf *workflow.Workflow

	// Resume
	resumeRef *memory.ResumeRef // set if this session was resumed

	// Phase-10: v2 persistence engine + in-memory caches. nil if
	// engine init failed; the session degrades gracefully (the v1
	// chat-style turn loop doesn't depend on v2 state).
	engine *engineRuntime

	// version is the running ghyll binary's version stamp. Captured
	// at NewSession so handoff commits (Phase-10 slice 2) carry it
	// as Ghyll-Version trailer without re-reading the global var.
	version string

	// opID is the operator identity for attestation flow.
	// Set via /op-id <identity>; cleared by /op-id with no arg.
	// Attest commands require a non-empty opID.
	opID string

	// modalDriver bridges operator-bus events to the
	// OperatorModalPrompt. Constructed in initEngine; nil before
	// engine init or if engine init fails. The REPL drains this
	// pre-Turn (Tier 2 ADR-016 Part D / Step 8).
	modalDriver *modalDriver

	// modalPrompt is the user-supplied modal implementation. nil
	// in production falls back to TermModal sharing the session
	// LineReader; tests inject a StubModal.
	modalPrompt modal.OperatorModalPrompt

	// lines is the session-scoped shared stdin line reader (gate-2
	// CONC-C-1/C-2). REPL pulls from this instead of constructing
	// its own bufio.Scanner so the modal can interleave reads
	// without losing buffered bytes. nil when modalPrompt was
	// injected (tests don't need stdin sharing).
	lines *modal.LineReader

	// sessionCtx is the session-scoped cancellation channel for
	// long-blocking operations (currently: modal reads). /exit
	// fires sessionCancel so an in-flight PresentVerdict aborts
	// cleanly with ctx.Err() instead of hanging the shutdown
	// (gate-1 F-14).
	sessionCtx    gocontext.Context
	sessionCancel gocontext.CancelFunc

	// warnedEmptyWorkdir gates the one-shot empty-workdir warning
	// from flushStagedBeforeModelSwitch (validation-pass-10 H10).
	warnedEmptyWorkdir bool

	// Terminal rendering
	renderer *stream.Renderer
	output   func(string)
}

// SessionConfig holds init parameters for a session.
type SessionConfig struct {
	Cfg         *config.Config
	Store       *memory.Store
	Syncer      *memory.Syncer
	VaultClient *memory.VaultClient
	DeviceKey   *memory.DeviceKey
	Embedder    *memory.Embedder
	ModelFlag   string
	Resume      bool   // --resume flag
	RepoRemote  string // git remote URL for resume lookup
	GlobalDir   string // ~/.ghyll/ directory for workflow loading
	Workdir     string
	SessionID   string
	Renderer    *stream.Renderer
	Output      func(string)

	// Version is the binary's version stamp (cmd/ghyll's `version`
	// var). Surfaces as the Ghyll-Version trailer on tool-driven
	// commits. Empty string yields "dev".
	Version string

	// DisableEngine skips v2 engine wiring (used in tests that
	// don't want a sqlite file on disk).
	DisableEngine bool

	// ReplayTimeout caps engine replay-on-startup at NewSession
	// (validation-pass-10 W3). Zero falls back to defaultReplayTimeout.
	ReplayTimeout time.Duration

	// ModalPrompt overrides the production TermModal (tty
	// interactive). Tests inject a StubModal here; production
	// leaves it nil and gets the tty implementation.
	ModalPrompt modal.OperatorModalPrompt
}

// defaultReplayTimeout caps replay-on-startup so a stalled or
// pathologically large engine.db doesn't block NewSession
// indefinitely (validation-pass-10 W3).
const defaultReplayTimeout = 30 * time.Second

// NewSession creates and initializes a session.
func NewSession(sc SessionConfig) (*Session, error) {
	v := sc.Version
	if v == "" {
		v = "dev"
	}
	s := &Session{
		cfg:         sc.Cfg,
		store:       sc.Store,
		syncer:      sc.Syncer,
		vaultClient: sc.VaultClient,
		deviceKey:   sc.DeviceKey,
		embedder:    sc.Embedder,
		workdir:     sc.Workdir,
		sessionID:   sc.SessionID,
		renderer:    sc.Renderer,
		output:      sc.Output,
		version:     v,
		modalPrompt: sc.ModalPrompt,
	}
	// Gate-2 CONC-C-1/C-2: do NOT eagerly construct the shared
	// LineReader here. Tests like TestScenario_REPL_* pass a
	// strings.Reader into REPL() — if we'd already created a
	// reader over os.Stdin, the test's input would be ignored.
	// The REPL constructs the reader lazily (and wires the
	// TermModal to it) when input == os.Stdin. Sessions that
	// inject a StubModal skip this entirely.
	if s.modalPrompt == nil {
		// Lazy TermModal: Lines is left nil; REPL will wire it
		// when it constructs the shared reader.
		s.modalPrompt = &modal.TermModal{Out: os.Stdout}
	}
	s.sessionCtx, s.sessionCancel = gocontext.WithCancel(gocontext.Background())

	if s.renderer == nil {
		s.renderer = stream.NewRenderer(ui.Stdout())
	}
	if s.output == nil {
		s.output = func(msg string) { ui.Info("%s", msg) }
	}

	// Resolve active model
	s.activeModel = sc.Cfg.Routing.DefaultModel
	if sc.ModelFlag != "" {
		s.activeModel = sc.ModelFlag
		s.modelLocked = true
	}

	// Verify model exists
	if _, ok := sc.Cfg.Models[s.activeModel]; !ok {
		return nil, fmt.Errorf("model %q not configured", s.activeModel)
	}

	// Resolve dialect functions. Validation-pass-8 D3/D5: empty
	// or unknown Dialect MUST surface as an error rather than
	// silently defaulting to minimax — otherwise an operator with
	// a typo gets MiniMax dispatching to a DeepSeek endpoint.
	if err := s.resolveDialect(); err != nil {
		return nil, err
	}

	// Create stream client
	modelCfg := sc.Cfg.Models[s.activeModel]
	s.streamClient = stream.NewClient(modelCfg.Endpoint, &stream.ClientOptions{
		MaxRetries:    3,
		BaseBackoffMs: 1000,
		// KIMI-CFG-4: prefer the operator-set wire `model` literal
		// when present; fall back to Dialect for legacy configs.
		ModelName:    wireModelName(modelCfg),
		ExtraHeaders: buildAuthHeader(sc.Cfg, s.activeModel),
	})

	// Create context manager with callbacks
	s.ctxManager = ghyllcontext.NewManager(ghyllcontext.ManagerConfig{
		MaxContext:       modelCfg.MaxContext,
		PreserveTurns:    3,
		CompactThreshold: 0.9,
	}, ghyllcontext.ManagerDeps{
		TokenCount:       s.tokenCount,
		CompactionCall:   s.compactionCall,
		CreateCheckpoint: s.createCheckpoint,
	})

	// Load workflow (invariant 51: .ghyll/ first, fallback .claude/)
	globalDir := sc.GlobalDir
	if globalDir == "" {
		globalDir = filepath.Join(os.Getenv("HOME"), ".ghyll")
	}
	wf, wfErr := workflow.Load(globalDir, sc.Workdir, sc.Cfg.Workflow.FallbackFolders)
	if wfErr != nil {
		s.output(fmt.Sprintf("⚠ workflow load failed: %v", wfErr))
	} else {
		s.wf = wf
		if wf.Source != "none" {
			s.output(fmt.Sprintf("ℹ workflow loaded from .%s/", wf.Source))
		}
	}

	// Build system prompt (includes workflow instructions)
	sysPrompt := s.composedSystemPrompt()
	s.ctxManager.AddMessage(types.Message{Role: "system", Content: sysPrompt})

	// Handle --resume (invariant 42, 43)
	if sc.Resume && s.store != nil {
		repoRemote := sc.RepoRemote
		if repoRemote == "" {
			repoRemote = sc.Workdir // fallback to workdir path
		}
		prevCp, err := s.store.LatestByRepo(repoRemote)
		if err != nil {
			s.output("ℹ no previous session found, starting fresh")
		} else {
			// Inject previous session summary as backfill
			backfill := fmt.Sprintf("Resuming from previous session (turn %d, model %s):\n\n%s",
				prevCp.Turn, prevCp.ActiveModel, prevCp.Summary)
			if len(prevCp.FilesTouched) > 0 {
				backfill += fmt.Sprintf("\n\nFiles touched: %s", strings.Join(prevCp.FilesTouched, ", "))
			}
			s.ctxManager.AddMessage(types.Message{Role: "system", Content: backfill})
			s.output(fmt.Sprintf("ℹ resumed from previous session (turn %d)", prevCp.Turn))

			// Restore plan mode from checkpoint
			if prevCp.PlanMode {
				s.planMode = true
			}

			// Store resume reference for first checkpoint
			s.resumeRef = &memory.ResumeRef{
				SessionID:      prevCp.SessionID,
				CheckpointHash: prevCp.Hash,
			}
		}
	}

	// Phase-10: v2 engine wiring. Open the project-local sqlite
	// store, replay persisted state into fresh in-memory caches,
	// then attach the journal. Failure is non-fatal — the chat
	// loop continues without v2 persistence.
	if !sc.DisableEngine {
		s.initEngine(sc.ReplayTimeout)
	}

	return s, nil
}

// initEngine performs the open + replay + attach lifecycle. Each
// step's failure is non-fatal — the chat loop continues without v2
// persistence. Per W3: replay runs under a bounded context so a
// stalled or oversized DB doesn't block startup indefinitely.
func (s *Session) initEngine(replayTimeout time.Duration) {
	if replayTimeout <= 0 {
		replayTimeout = defaultReplayTimeout
	}
	// Load the grid file (if any) to thread the
	// `insufficient-basis-rounds-max` config into the engine
	// runtime. A missing or unparseable grid file is non-fatal
	// here — escalation is disabled (max=0) and the runtime
	// proceeds.
	// Gate-2 CORR-A-23: when bootstrap.Read fails (no grid file
	// yet — common in fresh non-init projects), fall back to the
	// built-in defaults instead of leaving everything at zero.
	// IB rounds = 0 silently disables escalation; 64-entry modal
	// queue + 16 KiB residue cap give sensible safety bounds.
	ibRoundsMax := 3
	modalPendingMaxLen := bootstrap.DefaultModalPendingMaxLen
	residueNoteMaxBytes := bootstrap.DefaultResidueNoteMaxBytes
	var gridFile *bootstrap.Grid
	if g, gerr := bootstrap.Read(s.workdir); gerr == nil && g != nil {
		ibRoundsMax = g.InsufficientBasisRoundsMax
		modalPendingMaxLen = g.ModalPendingMaxLen
		residueNoteMaxBytes = g.ResidueNoteMaxBytes
		gridFile = g
	}
	rt, err := openEngineWithOptions(s.workdir, nil, ibRoundsMax, gridFile)
	if err != nil {
		// Diamond v4 / Gap 3: a binding-coverage failure at session
		// open is a hard refusal — the operator gets a stronger
		// directed message so they know to /ghyll init or fix the
		// grid. Other engine-open failures continue degraded.
		var mbe *bootstrap.MissingBindingError
		if errors.As(err, &mbe) {
			s.output(fmt.Sprintf("✗ session refuses: %v; run `ghyll init` or fix the grid",
				sanitizeOneLine(mbe.Error())))
			return
		}
		s.output(fmt.Sprintf("⚠ engine open failed (continuing without persistence): %v", err))
		return
	}
	s.output("ℹ replaying engine state…")
	ctx, cancel := gocontext.WithTimeout(gocontext.Background(), replayTimeout)
	defer cancel()
	counts, err := rt.replayEngine(ctx)
	if err != nil {
		s.output(fmt.Sprintf("⚠ engine replay failed: %v (continuing)", err))
		rt.closeEngine()
		return
	}
	if err := rt.attachJournal(nil); err != nil {
		s.output(fmt.Sprintf("⚠ engine attachJournal failed: %v (continuing)", err))
		rt.closeEngine()
		return
	}
	s.engine = rt
	// Tier 2 (ADR-016 Part D / Step 7+8): construct the modal
	// driver now that the engine + bus are live. arrowResolver
	// closes over the live grid; opIDProvider reads s.opID at
	// call-time (gate-1 F-20).
	//
	// Diamond v4 / W-H-2 closure: construct + subscribe the modal
	// driver BEFORE the binding-coverage check so the
	// OpEventBindingMissing publish below has a subscriber. Pre-fix
	// the publish raced the construction at session.go:385 and the
	// event was silently dropped (modal-driver ring buffer never saw
	// it). With the driver constructed here first, the dispatch arm
	// (modal_driver.go:155) consistently observes the event.
	s.modalDriver = newModalDriver(
		s.modalPrompt,
		rt.AttestationStore(),
		rt.Passes(),
		rt.Bus(),
		rt.InsufficientBasisTracker(),
		func() string { return s.opID },
		s.buildArrowResolver(rt),
		modalPendingMaxLen,
	)
	s.modalDriver.output = s.output
	s.modalDriver.residueNoteMaxBytes = residueNoteMaxBytes
	// Diamond v4 / Gap 3 (R17 closure): post-Replay binding-coverage
	// check. Walks both the typed runner.Grid and the untyped
	// bootstrap.Grid; a miss surfaces via OpEventBindingMissing AND
	// a console warning. Non-fatal at this seam — the binding-bound
	// clauses simply fail at evaluate-time with ErrConceptNotRegistered;
	// the operator sees the warning and either runs `ghyll init` or
	// fixes the grid manually.
	//
	// W-H-2: must publish AFTER s.modalDriver is constructed (and the
	// modal driver's bus.Subscribe has attached at newModalDriver:144)
	// so the event lands in the dispatch arm's ring buffer.
	if cerr := rt.verifyBindingsCoveragePostReplay(); cerr != nil {
		var mbe *bootstrap.MissingBindingError
		keys := ""
		if errors.As(cerr, &mbe) {
			keys = mbe.Error()
		} else {
			keys = cerr.Error()
		}
		s.output(fmt.Sprintf("⚠ binding coverage incomplete: %s", sanitizeOneLine(keys)))
		if rt.Bus() != nil {
			rt.Bus().Publish(runner.OperatorEvent{
				Kind:   runner.OpEventBindingMissing,
				Detail: keys,
				Payload: map[string]string{
					"detail": keys,
				},
			})
		}
	}
	// Mirror the cap onto the AttestationStore so Record-time
	// validation (gate-2 CORR-A-4 / SEC-H-1) sees the same value
	// the modal driver applies. atomic.Int64 on the store side,
	// safe to set from any goroutine.
	rt.AttestationStore().SetResidueNoteMaxBytes(residueNoteMaxBytes)
	if total := counts.Arrows + counts.Findings + counts.Requirements +
		counts.AmendmentsActive + counts.AmendmentsDrained; total > 0 {
		s.output(fmt.Sprintf("ℹ engine replayed: %d arrows, %d findings, %d requirements, %d amendments (%d drained)",
			counts.Arrows, counts.Findings, counts.Requirements,
			counts.AmendmentsActive, counts.AmendmentsDrained))
	}
	if len(counts.Errors) > 0 {
		// W8: surface up to 10 sanitized error strings so an operator
		// can triage without grepping logs. The rest are summarized.
		const maxShow = 10
		shown := len(counts.Errors)
		if shown > maxShow {
			shown = maxShow
		}
		s.output(fmt.Sprintf("⚠ engine replay had %d per-row errors (continuing)", len(counts.Errors)))
		for i := 0; i < shown; i++ {
			s.output("  - " + sanitizeOneLine(counts.Errors[i]))
		}
		if len(counts.Errors) > maxShow {
			s.output(fmt.Sprintf("  … %d more errors elided", len(counts.Errors)-maxShow))
		}
	}

	// C-3 (G2-F-3 / G2-I-3): surface Recovery's report. The bus has
	// no subscribers at recovery time (F-18 invariant); the report
	// is session.Open's responsibility to render.
	report := rt.RecoveryReport()
	// Tier 2 (gate-1 F-4): feed Recovery's republished
	// attestation events into the modal driver so the first turn
	// after restart re-presents pending verdicts.
	if s.modalDriver != nil && len(report.Events) > 0 {
		s.modalDriver.EnqueueFromRecovery(report.Events)
	}
	if report.OrphansAborted+report.OrphansPreserved+
		report.EvaluationRunsFlipped+report.JSONLTruncatedSkipped > 0 ||
		len(rt.catchUpOverrideEvents) > 0 {
		s.output(fmt.Sprintf(
			"⚠ crash recovery: %d orphan(s) aborted, %d preserved (attestation-pending), %d run(s) reconciled from JSONL",
			report.OrphansAborted, report.OrphansPreserved, report.EvaluationRunsFlipped))
		const maxRecoveryEvents = 10
		shown := len(report.Events)
		if shown > maxRecoveryEvents {
			shown = maxRecoveryEvents
		}
		for i := 0; i < shown; i++ {
			ev := report.Events[i]
			s.output(fmt.Sprintf("  - %s pass=%s arrow=%s clause=%s %s",
				ev.Kind, ev.PassID, ev.ArrowID, ev.ClauseID, sanitizeOneLine(ev.Detail)))
		}
		if len(report.Events) > maxRecoveryEvents {
			s.output(fmt.Sprintf("  … %d more recovery events elided", len(report.Events)-maxRecoveryEvents))
		}
		for _, ev := range rt.catchUpOverrideEvents {
			s.output(fmt.Sprintf("  - %s %s", ev.Kind, sanitizeOneLine(ev.Detail)))
		}
	}

	// Diamond v4 / W-C-1 closure (ADR-v4-002): auto-enable the
	// adversarial cycle if a dialect is configured at session start.
	// CI paths (no API key / no endpoint) see the disabled banner and
	// never call an LLM-backed hook. Production paths with a resolvable
	// dialect get the cycle wired without an operator command.
	s.autoEnableAdversarial()

	// Diamond v4 / Gap 2 (H-1 closure): surface pending amendments
	// recovered from disk. Replay loads drained amendments; any with
	// no drained_at remain in the queue and require an operator
	// /drain-amendments. Banner + typed bus event so subscribers
	// (modal driver / future status CLI) can react.
	if pending := rt.Amendments().Pending(); len(pending) > 0 {
		ids := make([]string, 0, len(pending))
		for _, am := range pending {
			ids = append(ids, am.ID)
		}
		s.output(fmt.Sprintf(
			"⚠ %d pending amendment(s) — run `/drain-amendments` to apply: %s",
			len(pending), sanitizeOneLine(strings.Join(ids, ", "))))
		if rt.Bus() != nil {
			rt.Bus().Publish(runner.OperatorEvent{
				Kind:   runner.OpEventRecoveryAmendmentsPending,
				Detail: fmt.Sprintf("count=%d", len(pending)),
				Payload: map[string]string{
					"count":         fmt.Sprintf("%d", len(pending)),
					"amendment_ids": strings.Join(ids, ","),
				},
			})
		}
	}
}

// buildArrowResolver closes over the engine runtime + Grid so the
// modalDriver can fill SourceRole/TargetRole/Context/Stratum/
// GridVersion at record-construction time. Mirrors the /attest CLI's
// arrow lookup path (handleAttestCommand).
func (s *Session) buildArrowResolver(rt *engineRuntime) arrowResolverFn {
	return func(arrowID string) (arrowResolved, bool) {
		grid := rt.Grid()
		if grid == nil {
			return arrowResolved{}, false
		}
		def, ok := grid.Lookup(arrowID)
		if !ok {
			return arrowResolved{}, false
		}
		return arrowResolved{
			SourceRole:  def.SourceRole,
			TargetRole:  def.TargetRole,
			Context:     def.Context,
			Stratum:     def.Stratum,
			GridVersion: grid.Version(),
		}, true
	}
}

// Close releases session resources. Idempotent (W12); safe to call
// from multiple defer chains.
func (s *Session) Close() {
	if s == nil {
		return
	}
	// Cancel any in-flight modal read (gate-1 F-14). Belt-and-
	// braces; /exit already calls cancel but Close may also be
	// invoked via signal handler or test cleanup.
	if s.sessionCancel != nil {
		s.sessionCancel()
	}
	// Gate-2 CONC-H-4: drop the modal driver's bus subscription
	// BEFORE the engine + bus go away. Without Stop the bus
	// retains a callback pointing at a torn-down driver.
	//
	// Gate-2 CONC-M-3: also drain any in-flight modal requests
	// via the cancelled sessionCtx — DrainPending sees
	// context.Canceled and returns immediately, re-queuing items
	// for next-session. No write-after-close on the tree.
	if s.modalDriver != nil {
		if s.sessionCtx != nil {
			_ = s.modalDriver.DrainPending(s.sessionCtx)
		}
		s.modalDriver.Stop()
	}
	// Gate-2 CONC-C-1/C-2: close the shared stdin reader so its
	// goroutine exits cleanly.
	if s.lines != nil {
		s.lines.Close()
		s.lines = nil
	}
	if s.engine == nil {
		return
	}
	// W11: surface dropped events at shutdown so an operator can
	// tell if persistence held up under load.
	if s.engine.journal != nil {
		if dropped := s.engine.journal.Dropped(); dropped > 0 {
			s.output(fmt.Sprintf("ℹ journal dropped %d events at shutdown", dropped))
		}
	}
	s.engine.closeEngine()
	s.engine = nil
}

// sanitizeOneLine strips control characters from operator-facing
// output so journal-replay error strings can't smuggle ANSI escapes
// or terminal-control sequences into the session UI. Mirrors the
// runner-package helper but lives here so cmd/ghyll doesn't import
// the runner sanitize.go internals.
func sanitizeOneLine(s string) string {
	if len(s) > 4096 {
		s = s[:4096] + "… (truncated)"
	}
	var b []byte
	for _, r := range s {
		if r == '\n' {
			b = append(b, '\\', 'n')
			continue
		}
		if r == '\r' {
			b = append(b, '\\', 'r')
			continue
		}
		if r == '\t' {
			b = append(b, '\\', 't')
			continue
		}
		if r < 0x20 || r == 0x7f || r == 0x85 || r == 0x2028 || r == 0x2029 {
			b = append(b, fmt.Sprintf("\\x%02x", r)...)
			continue
		}
		b = append(b, string(r)...)
	}
	return string(b)
}

// flushStagedBeforeModelSwitch implements the phase-8 F /
// phase-10 slice-2 invariant: every git commit attributed to a
// specific model. If staged changes exist at model-switch time,
// commit them with the OLD model's stamp BEFORE the dialect
// resolves to the new one.
//
// Validation-pass-10 hardenings:
//   - H1: exhaustive switch on PendingStatus (Unknown surfaces).
//   - H2/H15: independent timeouts (configurable) per call.
//   - H5: Unstaged changes refuse the flush — operator must commit
//     or stash; we never silently re-attribute them to the next
//     model.
//   - H6: model stamp uses StampLabel/name only (no endpoint).
//   - H10: empty workdir emits a one-shot warning rather than a
//     silent no-op.
//   - H14: decision.Reason is validated before interpolation.
func (s *Session) flushStagedBeforeModelSwitch(prevModel string, decision dialect.RoutingDecision) error {
	if s.workdir == "" {
		// H10: surface explicitly. The one-shot guard is the
		// session-scoped flag below.
		if !s.warnedEmptyWorkdir {
			s.output("⚠ commit stamping disabled: session has no workdir")
			s.warnedEmptyWorkdir = true
		}
		return nil
	}
	checkTimeout := time.Duration(s.cfg.Tools.GitCheckTimeoutSeconds) * time.Second
	commitTimeout := time.Duration(s.cfg.Tools.GitCommitTimeoutSeconds) * time.Second
	if checkTimeout <= 0 {
		checkTimeout = 5 * time.Second
	}
	if commitTimeout <= 0 {
		commitTimeout = 30 * time.Second
	}

	// H2: independent ctx for the status probe.
	checkCtx, checkCancel := gocontext.WithTimeout(gocontext.Background(), checkTimeout)
	defer checkCancel()
	status, err := tool.CheckPending(checkCtx, s.workdir, checkTimeout)
	if err != nil {
		return fmt.Errorf("check pending: %w", err)
	}
	// H1: exhaustive switch on every PendingStatus value.
	switch status {
	case tool.PendingClean, tool.PendingUntracked:
		return nil
	case tool.PendingUnstaged:
		// H5: emit a clear warning and refuse the switch attribution
		// (caller treats the returned error as advisory; current
		// handleHandoff logs and proceeds rather than blocking).
		return fmt.Errorf("unstaged changes present; cannot attribute to %s — commit or stash before model switch",
			prevModel)
	case tool.PendingStaged:
		// fall through to commit
	case tool.PendingUnknown:
		return fmt.Errorf("pending status unknown (git plumbing returned no signal)")
	default:
		return fmt.Errorf("pending status %v unhandled (programming error)", status)
	}

	// H14: validate Reason before placing in commit body. Sanitize
	// model names too (H8) so a malicious or buggy router can't
	// corrupt the commit message body.
	safeReason := sanitizeOneLine(string(decision.Reason))
	safeTarget := sanitizeOneLine(decision.TargetModel)
	modelStamp := buildModelStamp(prevModel, s.cfg)
	commitMsg := fmt.Sprintf("chore: flush before model switch (%s → %s, reason: %s)",
		prevModel, safeTarget, safeReason)

	// H2: independent ctx for the commit.
	commitCtx, commitCancel := gocontext.WithTimeout(gocontext.Background(), commitTimeout)
	defer commitCancel()
	res := tool.GitCommit(commitCtx, s.workdir, tool.CommitOptions{
		Message:      commitMsg,
		GhyllVersion: s.version,
		GhyllModel:   modelStamp,
	}, commitTimeout)
	if res.Error != "" {
		return &handoffFlushError{stage: "commit", inner: errors.New(sanitizeOneLine(res.Error))}
	}
	s.output(fmt.Sprintf("ℹ flushed staged changes under %s before handoff to %s", modelStamp, safeTarget))
	return nil
}

// handoffFlushError marks a flush failure that MUST block the model
// switch (commit failed, unstaged changes, unknown git state).
// Wraps so handleHandoff can distinguish from advisory errors.
type handoffFlushError struct {
	stage string
	inner error
}

func (e *handoffFlushError) Error() string {
	return fmt.Sprintf("%s: %v", e.stage, e.inner)
}

func (e *handoffFlushError) Unwrap() error { return e.inner }

// isHandoffBlockingFlushError reports whether the flush error
// should abort the model switch (vs. log advisory and continue).
// Per integrator M3 — commit-failed and unstaged-refused both block.
func isHandoffBlockingFlushError(err error) bool {
	var f *handoffFlushError
	if errors.As(err, &f) {
		return true
	}
	// Unstaged refusal returns a plain error from
	// flushStagedBeforeModelSwitch; recognize by its message.
	return strings.Contains(err.Error(), "unstaged changes present") ||
		strings.Contains(err.Error(), "pending status unknown")
}

// buildModelStamp returns the Ghyll-Model trailer value for a given
// configured model. Per H6 the default is the bare model name (no
// endpoint URL) so internal infrastructure DNS does not leak into
// `git log`. Operators with multiple endpoints serving the same
// model can override via `cfg.Models[name].StampLabel`.
func buildModelStamp(name string, cfg *config.Config) string {
	if cfg == nil {
		return name
	}
	if mc, ok := cfg.Models[name]; ok && strings.TrimSpace(mc.StampLabel) != "" {
		return mc.StampLabel
	}
	return name
}

// wireModelName returns the literal model id sent on the
// chat/completions request body's `model` field. KIMI-CFG-4 fix:
// gateways like CSCS route on the wire `model` field; an operator
// who needs `moonshotai/Kimi-K2.6` literal verbatim sets
// `model = "moonshotai/Kimi-K2.6"` in their [models.<name>] block.
// Empty Model falls back to Dialect (legacy behaviour preserved for
// configs that don't set the new field).
func wireModelName(mc config.ModelConfig) string {
	if strings.TrimSpace(mc.Model) != "" {
		return mc.Model
	}
	return mc.Dialect
}

func (s *Session) resolveDialect() error {
	d := s.cfg.Models[s.activeModel].Dialect
	family, err := normalizeDialect(d)
	if err != nil {
		return fmt.Errorf("model %q: %w", s.activeModel, err)
	}
	switch family {
	case "glm":
		s.systemPrompt = dialect.GLMSystemPrompt
		s.planModePrompt = dialect.GLMPlanModePrompt
		s.buildMessages = dialect.GLMBuildMessages
		s.parseToolCalls = dialect.GLMParseToolCalls
		s.compactionPrompt = dialect.GLMCompactionPrompt
		s.tokenCount = dialect.GLMTokenCount
		s.handoffSummary = dialect.GLMHandoffSummary
	case "deepseek":
		s.systemPrompt = dialect.DeepSeekSystemPrompt
		s.planModePrompt = dialect.DeepSeekPlanModePrompt
		s.buildMessages = dialect.DeepSeekBuildMessages
		s.parseToolCalls = dialect.DeepSeekParseToolCalls
		s.compactionPrompt = dialect.DeepSeekCompactionPrompt
		s.tokenCount = dialect.DeepSeekTokenCount
		s.handoffSummary = dialect.DeepSeekHandoffSummary
	case "qwen":
		s.systemPrompt = dialect.QwenSystemPrompt
		s.planModePrompt = dialect.QwenPlanModePrompt
		s.buildMessages = dialect.QwenBuildMessages
		s.parseToolCalls = dialect.QwenParseToolCalls
		s.compactionPrompt = dialect.QwenCompactionPrompt
		s.tokenCount = dialect.QwenTokenCount
		s.handoffSummary = dialect.QwenHandoffSummary
	case "minimax":
		s.systemPrompt = dialect.MinimaxSystemPrompt
		s.planModePrompt = dialect.MinimaxPlanModePrompt
		s.buildMessages = dialect.MinimaxBuildMessages
		s.parseToolCalls = dialect.MinimaxParseToolCalls
		s.compactionPrompt = dialect.MinimaxCompactionPrompt
		s.tokenCount = dialect.MinimaxTokenCount
		s.handoffSummary = dialect.MinimaxHandoffSummary
	case "kimi":
		s.systemPrompt = dialect.KimiSystemPrompt
		s.planModePrompt = dialect.KimiPlanModePrompt
		s.buildMessages = dialect.KimiBuildMessages
		s.parseToolCalls = dialect.KimiParseToolCalls
		s.compactionPrompt = dialect.KimiCompactionPrompt
		s.tokenCount = dialect.KimiTokenCount
		s.handoffSummary = dialect.KimiHandoffSummary
	default:
		return fmt.Errorf("dialect family %q unsupported (post-normalization)", family)
	}
	return nil
}

// errUnknownDialect is the sentinel error returned by normalizeDialect
// for unrecognized family names.
var errUnknownDialect = errors.New("unknown dialect family")

// normalizeDialect maps wire-form dialect strings to canonical family
// names. Per validation-pass-8 D3/D4: empty or unknown returns an
// error (no silent fall-through to minimax).
//
// KIMI-CFG-1 / KIMI-CFG-6 / CONFIG-1: this now delegates to the
// single source of truth in config (config.CanonicalDialectFamily +
// config.KnownDialectFamiliesList). The two whitelists cannot drift
// because there is now only one whitelist. Adding a new alias is a
// one-line append in config/dialect_families.go.
//
// K-ADV-4: bare `kimi-k2.5` / `kimi-k2.6` (short forms operators
// would naturally paste) are accepted at both layers — previously
// they passed normalizeDialect but failed config.Load.
//
// The previous prefix-based detection (HasPrefix("glm"), …) has
// been replaced by exact-alias lookup. A new variant must be
// registered explicitly rather than match by accident — this
// avoids the kimino-coder over-match class of bug.
func normalizeDialect(d string) (string, error) {
	if strings.TrimSpace(d) == "" {
		return "", fmt.Errorf("%w: dialect field is empty", errUnknownDialect)
	}
	fam, ok := config.CanonicalDialectFamily(d)
	if !ok {
		return "", fmt.Errorf("%w: %q (known families: %s)",
			errUnknownDialect, d, config.KnownDialectFamiliesList())
	}
	return fam, nil
}

// SessionContext returns the session-scoped cancellation context.
// REPL + other long-blocking operations honor this so /exit can
// abort cleanly (gate-1 F-14). Falls back to Background if the
// session predates the ctx wiring (test fixtures that construct
// Session directly without NewSession).
func (s *Session) SessionContext() gocontext.Context {
	if s == nil || s.sessionCtx == nil {
		return gocontext.Background()
	}
	return s.sessionCtx
}

// DrainModalPending blocks until every queued operator verdict /
// escalation has been answered. Called pre-Turn by the REPL so the
// operator sees the modal before the next model dispatch (ADR-016
// Part D / Step 8). No-op if the engine isn't wired or no driver
// exists.
//
// On ErrModalDrainCapExceeded surfaces the diagnostic to the user
// and continues; the pending overflow gets a OpEventModalBackpressure
// event on the bus for operator visibility.
func (s *Session) DrainModalPending(ctx gocontext.Context) {
	if s == nil || s.modalDriver == nil {
		return
	}
	if err := s.modalDriver.DrainPending(ctx); err != nil {
		if errors.Is(err, gocontext.Canceled) || errors.Is(err, gocontext.DeadlineExceeded) {
			return
		}
		s.output(fmt.Sprintf("⚠ modal drain: %v", err))
	}
}

// Turn executes one turn: send user input, get response, execute tools.
func (s *Session) Turn(userInput string) (string, error) {
	// Add user message
	s.ctxManager.AddMessage(types.Message{Role: "user", Content: userInput})
	s.toolDepth = 0

	// Pre-turn check (may trigger compaction)
	endpoint := s.cfg.Models[s.activeModel].Endpoint
	prompt := s.compactionPrompt()
	result := s.ctxManager.PreTurnCheck(s.activeModel, endpoint, prompt)
	if result.CompactionTriggered {
		s.output(fmt.Sprintf("ℹ compacted context (%d tokens)", result.TokenCount))
	}

	// Routing decision
	decision := dialect.Evaluate(dialect.RouterInputs{
		ContextDepth: s.tokenCount(s.ctxManager.Messages()),
		ToolDepth:    s.toolDepth,
		ModelLocked:  s.modelLocked,
		DeepOverride: s.deepOverride,
		ActiveModel:  s.activeModel,
		Config:       s.cfg.Routing,
	})

	switch decision.Action {
	case dialect.ActionEscalate, dialect.ActionDeEscalate:
		if err := s.handleHandoff(decision); err != nil {
			s.output(fmt.Sprintf("⚠ handoff failed: %v", err))
		}
	case dialect.ActionGateUnsatisfiable:
		// H3 fix: §7.1 — depth-sensitive gate's MinTier exceeds every
		// available tier. The dispatcher MUST NOT launder it through
		// an insufficient model. Return before sendAndProcess.
		msg := fmt.Sprintf("⚠ gate-unsatisfiable: arrow needs tier higher than DeepModel; route to operator attestation (active model unchanged: %s)",
			s.activeModel)
		s.output(msg)
		return attestationPendingResponse(decision, msg), nil
	case dialect.ActionGateLockedConflict:
		// H3 fix: §7.1 — operator's --model lock prevents escalation
		// the gate requires. Never silently dispatch on the locked
		// model; route to attestation.
		msg := fmt.Sprintf("⚠ gate-locked-conflict: --model lock on %s prevents escalation a gate requires; route to operator attestation",
			s.activeModel)
		s.output(msg)
		return attestationPendingResponse(decision, msg), nil
	case dialect.ActionInvalid:
		// H3 fix + H11: surface RejectedFloor so operator can triage
		// without reading source. Never dispatch on invalid input.
		msg := fmt.Sprintf("⚠ routing: invalid GateFloor=%d (out of 0..3); check engine wiring", decision.RejectedFloor)
		s.output(msg)
		return attestationPendingResponse(decision, msg), nil
	case dialect.ActionNone:
		// Steady state — fall through to dispatch.
	default:
		// H4: any new Action constant added without a session-loop
		// handler must surface, not silently fall through.
		s.output(fmt.Sprintf("⚠ routing: unhandled action %q (programming error); skipping dispatch", decision.Action))
		return attestationPendingResponse(decision, "unhandled routing action"), nil
	}

	// Send to model
	return s.sendAndProcess()
}

// attestationPendingResponse formats a structured chat-loop reply
// for §7.1 outcomes that must NOT dispatch (H3 fix). The string is
// returned to the REPL as the turn's content; the caller can log /
// surface it without confusing it with a model response.
func attestationPendingResponse(d dialect.RoutingDecision, detail string) string {
	return fmt.Sprintf("[attestation-pending] reason=%s action=%s detail=%s",
		d.Reason, d.Action, detail)
}

func (s *Session) sendAndProcess() (string, error) {
	// Finding 1: guard against unbounded tool call recursion
	if s.toolDepth > maxToolDepth {
		return "", fmt.Errorf("tool call depth exceeded (%d), stopping", maxToolDepth)
	}

	sysPrompt := s.composedSystemPrompt()
	messages := s.buildMessages(s.ctxManager.Messages(), sysPrompt)
	// Progress indicator: spinner runs from request-send through the
	// first SSE token (or tool call). RenderDelta / RenderToolCall
	// stop it; the explicit Stop after SendStream covers the error
	// path where no callback fires. Label shows the active model so
	// the operator sees which backend is serving the turn.
	s.renderer.StartSpinner(fmt.Sprintf("%s is thinking…", s.activeModel))
	resp, err := s.streamClient.SendStream(messages, func(delta string) {
		s.renderer.RenderDelta(delta)
	})
	s.renderer.StopSpinner()

	if err != nil {
		var sErr *stream.StreamError
		if stream.AsStreamError(err, &sErr) && sErr.ContextTooLong {
			endpoint := s.cfg.Models[s.activeModel].Endpoint
			if cErr := s.ctxManager.ReactiveCompact(s.activeModel, endpoint, s.compactionPrompt()); cErr != nil {
				return "", fmt.Errorf("reactive compaction failed: %w", cErr)
			}
			messages = s.buildMessages(s.ctxManager.Messages(), sysPrompt)
			s.renderer.StartSpinner(fmt.Sprintf("%s is thinking… (post-compaction)", s.activeModel))
			resp, err = s.streamClient.SendStream(messages, func(delta string) {
				s.renderer.RenderDelta(delta)
			})
			s.renderer.StopSpinner()
			if err != nil {
				return "", err
			}
		} else {
			return "", err
		}
	}

	// Add assistant response to context.
	// ADR-v4-009 inbound half: propagate dialect-side reasoning
	// trace (Kimi 2.5/2.6 emits delta.reasoning_content) onto the
	// appended message so the next outbound assistant turn carries
	// it back to the model. Other dialects accumulate "" → omitempty
	// keeps the wire surface unchanged.
	s.ctxManager.AddMessage(types.Message{
		Role:             "assistant",
		Content:          resp.Content,
		ReasoningContent: resp.ReasoningContent,
		ToolCalls:        resp.ToolCalls,
	})

	// Finish the streaming line
	if resp.Content != "" {
		s.renderer.RenderComplete()
	}

	if resp.Partial {
		s.renderer.RenderWarning("stream interrupted")
		return resp.Content, nil
	}

	// K-ADV-1 / KIMI-CFG-3 / WIRE-2: enforce the dialect's tool-call
	// id contract on the live streaming path. parseSSEStream
	// accumulates ToolCalls dialect-agnostically; here we re-marshal
	// the accumulated entries and run them through the dialect's
	// parseToolCalls validator. For Kimi this enforces the
	// `functions.<name>:<index>` id shape — a non-conformant id
	// (the documented sentinel of a wrong-version backend) surfaces
	// ErrParseToolCall with an operator-facing diagnostic naming the
	// offending shape, rather than silently dispatching against an
	// unparseable id.
	if len(resp.ToolCalls) > 0 && s.parseToolCalls != nil {
		rawTCs, mErr := json.Marshal(resp.ToolCalls)
		if mErr == nil {
			if _, perr := s.parseToolCalls(rawTCs); perr != nil {
				if errors.Is(perr, dialect.ErrParseToolCall) {
					diag := fmt.Sprintf("⚠ dialect-level tool_call parse refused dispatch: %v", perr)
					s.renderer.RenderWarning(diag)
					s.output(diag)
					return diag, nil
				}
				// Non-sentinel parse error — surface but still refuse
				// dispatch (defence-in-depth).
				diag := fmt.Sprintf("⚠ tool_call parse error: %v", perr)
				s.renderer.RenderWarning(diag)
				s.output(diag)
				return diag, nil
			}
		}
	}

	// Execute tool calls
	if len(resp.ToolCalls) > 0 {
		for _, tc := range resp.ToolCalls {
			s.renderer.RenderToolCall(tc.Function.Name, tc.Function.Arguments)
			toolResult := s.executeTool(tc)
			s.renderer.RenderToolResult(toolResult.Output, toolResult.Error, toolResult.TimedOut)
			// Surface error to model if output is empty (finding 23)
			content := toolResult.Output
			if content == "" && toolResult.Error != "" {
				content = toolResult.Error
			}
			// Per-result byte cap. Without this a single `find`
			// on a deep tree adds 50+ KB to context per turn and
			// blows past gateway body limits within a handful of
			// inspection turns. The renderer already truncates
			// for display; this caps the SAME content as it
			// enters the model's message history.
			content = capToolResult(content, s.cfg.Tools.MaxResultBytes)
			s.ctxManager.AddMessage(types.Message{
				Role:       "tool",
				Content:    content,
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
			})
			s.toolDepth++
		}
		// Continue with model (tool results need processing)
		return s.sendAndProcess()
	}

	// Checkpoint check
	turn := s.ctxManager.Turn()
	interval := s.cfg.Memory.CheckpointIntervalTurns
	if interval > 0 && turn > 0 && turn%interval == 0 {
		_ = s.createCheckpoint(ghyllcontext.CheckpointRequest{
			SessionID:   s.sessionID,
			Turn:        turn,
			ActiveModel: s.activeModel,
			Summary:     fmt.Sprintf("turn %d", turn),
			Messages:    s.ctxManager.Messages(),
			Reason:      "interval",
		})
	}

	return resp.Content, nil
}

func (s *Session) executeTool(tc types.ToolCall) types.ToolResult {
	ctx := gocontext.Background()
	bashTimeout := time.Duration(s.cfg.Tools.BashTimeoutSeconds) * time.Second
	fileTimeout := time.Duration(s.cfg.Tools.FileTimeoutSeconds) * time.Second

	// Finding 5: don't execute with empty args on parse failure
	var args struct {
		Command   string `json:"command"`
		Path      string `json:"path"`
		Content   string `json:"content"`
		Pattern   string `json:"pattern"`
		Args      string `json:"args"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
		URL       string `json:"url"`
		Query     string `json:"query"`
		Limit     int    `json:"limit"` // memory_search result cap
		Task      string `json:"task"`
		Reason    string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		return types.ToolResult{Error: fmt.Sprintf("failed to parse tool arguments: %v", err)}
	}

	webTimeout := time.Duration(s.cfg.Tools.WebTimeoutSeconds) * time.Second
	if webTimeout == 0 {
		webTimeout = 30 * time.Second
	}
	webMaxTokens := s.cfg.Tools.WebMaxResponseTokens
	if webMaxTokens == 0 {
		webMaxTokens = 10000
	}

	switch tc.Function.Name {
	case "bash", "execute_bash":
		// `execute_bash` alias: Kimi K2.x (and other models trained on
		// Anthropic-style tool catalogs) reach for `execute_bash` as a
		// trained prior. Accepting the alias saves three round-trips
		// of "unknown tool" → fallback per session. Same handler — the
		// tool semantics are identical.
		return tool.Bash(ctx, args.Command, bashTimeout)
	case "read_file":
		return tool.ReadFile(ctx, args.Path, fileTimeout)
	case "write_file":
		return tool.WriteFile(ctx, args.Path, args.Content, fileTimeout)
	case "edit_file":
		return tool.EditFile(ctx, args.Path, args.OldString, args.NewString, fileTimeout)
	case "git":
		gitArgs := strings.Fields(args.Args)
		return tool.Git(ctx, s.workdir, gitArgs, bashTimeout)
	case "grep":
		return tool.Grep(ctx, args.Pattern, args.Path, bashTimeout)
	case "glob":
		path := args.Path
		if path == "" {
			path = s.workdir
		}
		return tool.Glob(ctx, args.Pattern, path, bashTimeout)
	case "web_fetch":
		return tool.WebFetch(ctx, args.URL, webMaxTokens, webTimeout)
	case "web_search":
		backend := s.cfg.Tools.WebSearchBackend
		if backend == "" {
			backend = "https://html.duckduckgo.com"
		}
		return tool.WebSearch(ctx, args.Query, backend, 10, webTimeout)
	case "memory_search":
		// Operator-visible memory: lets the model recall prior
		// sessions' decisions / fixes / open work. Backed by the
		// same sqlite checkpoint store the CLI `ghyll memory log`
		// walks. Accepts a free-text query (substring match against
		// summaries, half-overlap rule) OR a hex hash prefix
		// (>=6 chars). Default limit 5 keeps the tool result inside
		// the model's per-call budget.
		return s.memorySearchTool(args.Query, args.Limit)
	case "agent":
		return RunSubAgent(s, args.Task)
	case "enter_plan_mode":
		s.planMode = true
		return types.ToolResult{Output: "plan mode activated"}
	case "exit_plan_mode":
		s.planMode = false
		return types.ToolResult{Output: "plan mode deactivated"}
	default:
		return types.ToolResult{Error: fmt.Sprintf("unknown tool: %s", tc.Function.Name)}
	}
}

// Finding 2: handoff now creates checkpoint, uses HandoffSummary, preserves context
func (s *Session) handleHandoff(decision dialect.RoutingDecision) error {
	prevModel := s.activeModel

	// Phase-10 slice 2: commit-per-model-change. Before switching
	// dialect, flush any staged changes with the OLD model's stamp
	// so the per-model attribution invariant holds. Untracked-only
	// changes are NOT flushed (CheckPending filters them out).
	//
	// H7: a flush failure records a distinct checkpoint Reason so
	// `ghyll memory log` can show that the audit trail's "handed
	// off" did NOT include a clean staged-changes flush.
	//
	// Integrator M3: a hard commit failure (not "nothing to commit"
	// — that returns nil) MUST abort the switch. Otherwise the
	// staging area carries the old model's work into the new
	// model's first commit and per-model attribution silently
	// breaks. Unstaged-changes refusal (H5) is also a hard abort.
	flushReason := "handoff"
	if err := s.flushStagedBeforeModelSwitch(prevModel, decision); err != nil {
		if isHandoffBlockingFlushError(err) {
			s.output(fmt.Sprintf("⚠ handoff blocked: flush before model switch failed: %v", err))
			return fmt.Errorf("handoff: flush failed and switch blocked: %w", err)
		}
		s.output(fmt.Sprintf("⚠ flush before handoff (advisory): %v", err))
		flushReason = "handoff-flush-failed"
	}

	// H8 + H9: sanitize model names, surface decision.Reason in the
	// checkpoint summary so ghyll memory log shows WHY the handoff
	// fired.
	safePrev := sanitizeOneLine(prevModel)
	safeTarget := sanitizeOneLine(decision.TargetModel)
	safeReason := sanitizeOneLine(string(decision.Reason))
	summary := fmt.Sprintf("handoff: %s → %s (reason: %s)", safePrev, safeTarget, safeReason)

	// Create handoff checkpoint on current model (invariant 10)
	_ = s.createCheckpoint(ghyllcontext.CheckpointRequest{
		SessionID:   s.sessionID,
		Turn:        s.ctxManager.Turn(),
		ActiveModel: s.activeModel,
		Summary:     summary,
		Messages:    s.ctxManager.Messages(),
		Reason:      flushReason,
	})

	// Get recent turns for handoff summary
	msgs := s.ctxManager.Messages()
	preserveN := 3
	if preserveN > len(msgs) {
		preserveN = len(msgs)
	}
	recentTurns := msgs[len(msgs)-preserveN:]

	// Get the checkpoint we just created for the summary
	var cp memory.Checkpoint
	if s.store != nil {
		if latest, err := s.store.LatestBySession(s.sessionID); err == nil {
			cp = *latest
		}
	}

	// Switch dialect. resolveDialect can only error on a dialect
	// the config validator already accepted, so failure here is a
	// programming error — surface it via s.output rather than
	// silently continuing on the wrong dispatch table.
	s.activeModel = decision.TargetModel
	if err := s.resolveDialect(); err != nil {
		s.output(fmt.Sprintf("⚠ dialect resolve after handoff: %v", err))
	}

	// Format handoff context using target dialect's HandoffSummary
	handoffMsgs := s.handoffSummary(cp, recentTurns)

	// Update stream client endpoint. ExtraHeaders are resolved
	// fresh against the new target model so each handoff uses its
	// own api_key (operator may configure distinct keys per
	// endpoint, e.g. CSCS gateway + local dev).
	modelCfg := s.cfg.Models[s.activeModel]
	s.streamClient = stream.NewClient(modelCfg.Endpoint, &stream.ClientOptions{
		MaxRetries:    3,
		BaseBackoffMs: 1000,
		// KIMI-CFG-4: prefer the operator-set wire `model` literal.
		ModelName:    wireModelName(modelCfg),
		ExtraHeaders: buildAuthHeader(s.cfg, s.activeModel),
	})

	// Create new context manager with handoff messages
	s.ctxManager = ghyllcontext.NewManager(ghyllcontext.ManagerConfig{
		MaxContext:       modelCfg.MaxContext,
		PreserveTurns:    3,
		CompactThreshold: 0.9,
	}, ghyllcontext.ManagerDeps{
		TokenCount:       s.tokenCount,
		CompactionCall:   s.compactionCall,
		CreateCheckpoint: s.createCheckpoint,
	})

	// Re-inject composed system prompt with workflow instructions + role + plan mode
	// (INT-1: these were lost when creating a fresh context manager)
	sysPrompt := s.composedSystemPrompt()
	s.ctxManager.AddMessage(types.Message{Role: "system", Content: sysPrompt})

	// Populate with handoff summary
	for _, msg := range handoffMsgs {
		s.ctxManager.AddMessage(msg)
	}

	s.output(fmt.Sprintf("⟳ switched to %s from %s", s.activeModel, prevModel))
	return nil
}

func (s *Session) compactionCall(req ghyllcontext.CompactionRequest) (string, error) {
	msgs := []map[string]any{
		{"role": "system", "content": req.CompactionPrompt},
	}
	for _, m := range req.TurnsToSummarize {
		msgs = append(msgs, map[string]any{
			"role":    m.Role,
			"content": m.Content,
		})
	}

	// AUTH-W-004: pin the resolution to the Session's CURRENT
	// active model rather than trusting req.ModelName. ADR-005
	// mandates compaction reuses the active endpoint and key; we
	// honour that invariant here regardless of what the caller
	// populated. If both are present and agree, behaviour is
	// unchanged; if req.ModelName is empty, the closure picks up
	// the right model from session state (no silent auth-drop).
	modelName := s.activeModel
	if req.ModelName != "" {
		modelName = req.ModelName
	}

	// AUTH-W-010: ModelName must also flow into the request body
	// so CSCS-style gateways that route on `model` reach the
	// correct backend (previously fell through to "default").
	// KIMI-CFG-4: prefer the literal wire `model` if configured.
	wireName := wireModelName(s.cfg.Models[modelName])
	// ADR-005: compaction runs on the SAME endpoint as the active
	// dialect, so the same api_key applies.
	client := stream.NewClient(req.ModelEndpoint, &stream.ClientOptions{
		MaxRetries:    1,
		BaseBackoffMs: 500,
		ModelName:     wireName,
		ExtraHeaders:  buildAuthHeader(s.cfg, modelName),
	})
	resp, err := client.Send(msgs)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

func (s *Session) createCheckpoint(req ghyllcontext.CheckpointRequest) error {
	if s.store == nil || s.deviceKey == nil {
		return nil
	}

	parentHash := "0000000000000000000000000000000000000000000000000000000000000000"
	if latest, err := s.store.LatestBySession(s.sessionID); err == nil {
		parentHash = latest.Hash
	}

	cp := &memory.Checkpoint{
		Version:      2,
		ParentHash:   parentHash,
		DeviceID:     s.deviceKey.DeviceID,
		AuthorID:     s.deviceKey.DeviceID,
		Timestamp:    time.Now().UnixNano(),
		SessionID:    req.SessionID,
		Turn:         req.Turn,
		ActiveModel:  req.ActiveModel,
		PlanMode:     s.planMode,
		ResumedFrom:  s.resumeRef,
		Summary:      req.Summary,
		FilesTouched: req.FilesTouched,
		ToolsUsed:    req.ToolsUsed,
		InjectionSig: req.InjectionSig,
	}
	// resumeRef is cleared after first checkpoint creation (regardless of reason).
	// This ensures only the first checkpoint of a resumed session carries the link.
	if s.resumeRef != nil {
		s.resumeRef = nil
	}

	memory.SignCheckpoint(cp, s.deviceKey.PrivateKey)
	return s.store.Append(cp)
}

// composedSystemPrompt returns the system prompt with workflow instructions,
// active role overlay, and plan mode overlay.
// Invariant 46: instructions survive compaction (system-level).
// Invariant 47: global first, project last (project has last word).
func (s *Session) composedSystemPrompt() string {
	prompt := s.systemPrompt(s.workdir)

	// Append workflow instructions with budget enforcement (invariant 47, 48).
	// Two-phase truncation: project fits → drop global; project exceeds → truncate project from end.
	if s.wf != nil {
		budget := s.cfg.Workflow.InstructionBudgetTokens
		global := s.wf.GlobalInstructions
		project := s.wf.ProjectInstructions

		// Combine and check budget. ADR-008: roles are fixed v2 data,
		// not runtime-loaded; only global + project instructions
		// participate in the prompt-budget calculation.
		combined := joinNonEmpty("\n\n", global, project)
		if budget > 0 && s.tokenCount != nil && combined != "" {
			tokens := s.tokenCount([]types.Message{{Role: "system", Content: combined}})
			if tokens > budget {
				// Phase 1: try dropping global
				withoutGlobal := project
				tokensWithout := s.tokenCount([]types.Message{{Role: "system", Content: withoutGlobal}})
				if tokensWithout <= budget {
					combined = withoutGlobal
					s.output("⚠ global instructions dropped to fit budget")
				} else {
					// Phase 2: truncate project from end
					// Approximate: cut chars proportionally
					ratio := float64(budget) / float64(tokensWithout)
					maxChars := int(float64(len(project)) * ratio)
					if maxChars > 0 && maxChars < len(project) {
						project = project[:maxChars]
					}
					combined = project
					s.output("⚠ instructions truncated to fit budget")
				}
			}
		}

		if combined != "" {
			prompt += "\n\n" + combined
		}
	}

	// Append plan mode overlay (invariant 36: advisory only)
	if s.planMode && s.planModePrompt != nil {
		prompt += "\n\n" + s.planModePrompt()
	}

	return prompt
}

// joinNonEmpty joins non-empty strings with a separator.
func joinNonEmpty(sep string, parts ...string) string {
	var nonEmpty []string
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	return strings.Join(nonEmpty, sep)
}

// PlanMode returns whether plan mode is active.
func (s *Session) PlanMode() bool {
	return s.planMode
}

// SetPlanMode sets plan mode on or off and returns the new state.
func (s *Session) SetPlanMode(active bool) {
	s.planMode = active
}

// DeepOverride reports whether the operator's /deep override is active.
func (s *Session) DeepOverride() bool {
	return s.deepOverride
}

// SetDeepOverride flips the /deep override flag. Refused when the
// model is locked via --model.
func (s *Session) SetDeepOverride(active bool) {
	if s.modelLocked {
		return
	}
	s.deepOverride = active
}

// ModelLocked reports whether --model was used to lock the model.
func (s *Session) ModelLocked() bool {
	return s.modelLocked
}

// ComposedSystemPrompt is the exported wrapper for composedSystemPrompt,
// used by tests and any caller that needs to inspect the prompt
// composition for a given (workflow + role + plan-mode) configuration.
func (s *Session) ComposedSystemPrompt() string {
	return s.composedSystemPrompt()
}

// SlashCommandResult reports what DispatchSlashCommand did with a
// given input line. Handled is false when the line was not a known
// slash command (the caller should treat it as user input).
type SlashCommandResult struct {
	Handled       bool   // true if a slash command was recognized
	Output        string // text to surface to the operator (status, ack, etc.)
	ContinueLoop  bool   // when true, REPL should continue (not exit)
	ExitRequested bool   // /exit was typed
}

// DispatchSlashCommand handles the built-in slash commands /deep,
// /plan, /fast, /status, /exit, /op-id, /attest, /attestations,
// /passes. Returns Handled=false when the line is not a built-in
// (callers route to workflow-defined commands or model dispatch).
//
// The REPL uses this method so the BDD layer can exercise the same
// dispatch logic without standing up the full input loop.
func (s *Session) DispatchSlashCommand(line string) SlashCommandResult {
	// Operator commands take leading-word dispatch; the rest of
	// the line is the argument. Empty-arg variants fall through
	// to the no-arg branch and surface usage.
	if strings.HasPrefix(line, "/op-id") {
		return s.handleOpIDCommand(strings.TrimSpace(strings.TrimPrefix(line, "/op-id")))
	}
	if strings.HasPrefix(line, "/attest ") || line == "/attest" {
		return s.handleAttestCommand(strings.TrimSpace(strings.TrimPrefix(line, "/attest")))
	}
	if line == "/attestations" || strings.HasPrefix(line, "/attestations ") {
		return s.handleAttestationsCommand(strings.TrimSpace(strings.TrimPrefix(line, "/attestations")))
	}
	if line == "/passes" {
		return s.handlePassesCommand()
	}
	if strings.HasPrefix(line, "/passes ") {
		return s.handlePassByIDCommand(strings.TrimSpace(strings.TrimPrefix(line, "/passes ")))
	}
	// /run-arrow + /list-arrows wire the dispatcher / engineRuntime.RunArrow
	// into the operator's REPL surface (integrator finding C-3).
	if strings.HasPrefix(line, "/run-arrow ") || line == "/run-arrow" {
		return s.handleRunArrowCommand(strings.TrimSpace(strings.TrimPrefix(line, "/run-arrow")))
	}
	if line == "/list-arrows" {
		return s.handleListArrowsCommand()
	}
	// Diamond v4 / Gap 2: /drain-amendments drains the pending
	// AmendmentQueue through the live AmendmentCommitter. No arg.
	if line == "/drain-amendments" || strings.HasPrefix(line, "/drain-amendments ") {
		return s.handleDrainAmendmentsCommand(strings.TrimSpace(strings.TrimPrefix(line, "/drain-amendments")))
	}
	// Diamond v4 / Gap 1: /adversary {enable|disable|status} toggles
	// the AtomicAdversarialHooks bundle that PassDispatcher consults.
	if line == "/adversary" || strings.HasPrefix(line, "/adversary ") {
		return s.handleAdversaryCommand(strings.TrimSpace(strings.TrimPrefix(line, "/adversary")))
	}
	// Diamond v4 / I-C-1 closure: /invalidate-arrow produces
	// OpEventArrowInvalidated; the observer wired in attachJournal
	// persists the row to .ghyll/engine.db arrow_invalidations
	// (ADR-v4-008). Refuses without an op-id since the audit row
	// carries operator identity.
	if line == "/invalidate-arrow" || strings.HasPrefix(line, "/invalidate-arrow ") {
		return s.handleInvalidateArrowCommand(strings.TrimSpace(strings.TrimPrefix(line, "/invalidate-arrow")))
	}

	switch line {
	case "/exit":
		// Gate-1 F-14: cancel any in-flight modal read so the
		// shutdown doesn't hang on a blocked PresentVerdict.
		// Idempotent — Close() also calls cancel.
		if s.sessionCancel != nil {
			s.sessionCancel()
		}
		return SlashCommandResult{Handled: true, ExitRequested: true}
	case "/deep":
		if s.modelLocked {
			return SlashCommandResult{
				Handled: true, ContinueLoop: true,
				Output: "ℹ /deep ignored, model locked via --model flag",
			}
		}
		s.deepOverride = true
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "switched to deep tier",
		}
	case "/plan":
		if s.planMode {
			return SlashCommandResult{
				Handled: true, ContinueLoop: true,
				Output: "plan mode already active",
			}
		}
		s.planMode = true
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "plan mode activated",
		}
	case "/fast":
		if s.modelLocked {
			return SlashCommandResult{
				Handled: true, ContinueLoop: true,
				Output: "ℹ /fast ignored, model locked via --model flag",
			}
		}
		s.deepOverride = false
		s.planMode = false
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "auto-routing restored, plan mode off",
		}
	case "/status":
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: fmt.Sprintf(
				"model: %s (locked: %v, deep: %v, plan: %v)\nturn: %d, tool_depth: %d",
				s.activeModel, s.modelLocked, s.deepOverride, s.planMode,
				s.ctxManager.Turn(), s.toolDepth,
			),
		}
	}
	return SlashCommandResult{Handled: false}
}

// ActiveModel returns the current model name.
func (s *Session) ActiveModel() string {
	return s.activeModel
}

// Prompt returns the terminal prompt string.
func (s *Session) Prompt() string {
	return fmt.Sprintf("ghyll [%s] %s ▸ ", s.activeModel, s.workdir)
}

// --- Operator command handlers (Tier-1) ------------------------------
//
// These wire the operator-facing surface for gate-and-arrow flow:
//   /op-id <id>           declare operator identity for the session
//   /op-id                show the current op-id; clear with /op-id none
//   /attest <ref> <verdict> [reason]
//                         record an attestation verdict for the given
//                         depth-type-attestation-ref. verdict ∈
//                         {pass, fail, insufficient-basis}.
//   /attestations [arrow] list recorded attestations (optionally
//                         filtered by arrow id).
//   /passes               list currently-open passes.
//
// Each handler returns a SlashCommandResult with ContinueLoop=true
// so the REPL stays interactive.

func (s *Session) handleOpIDCommand(arg string) SlashCommandResult {
	if arg == "" {
		if s.opID == "" {
			return SlashCommandResult{
				Handled: true, ContinueLoop: true,
				Output: "ℹ no op-id set; use /op-id <identity> to declare",
			}
		}
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: fmt.Sprintf("op-id: %s", s.opID),
		}
	}
	if arg == "none" || arg == "clear" {
		s.opID = ""
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "op-id cleared",
		}
	}
	// Post-prod-readiness adversarial M-C: route the per-session
	// `/op-id` path through the same shim ghyll-init uses so the
	// recorded form is NFC-normalized. Three validators previously
	// existed in cmd/ghyll (validateOpID), bootstrap
	// (ValidateAndNormalizeOpID), and this REPL handler; the shim
	// validateAndNormalizeOpID is now the single canonical entry
	// point inside cmd/ghyll. The strict cmd/ghyll rules
	// (leading-dot/dash, trailing-dot rejection) still apply via
	// validateOpID inside the shim.
	normalized, err := validateAndNormalizeOpID(arg)
	if err != nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: fmt.Sprintf("✗ invalid op-id: %v", err),
		}
	}
	s.opID = normalized
	return SlashCommandResult{
		Handled: true, ContinueLoop: true,
		Output: fmt.Sprintf("op-id set: %s", normalized),
	}
}

// validateOpID hardens the operator identity against being smuggled
// into filesystem paths or log lines (gate-1 F-13). The op-id is
// stamped on every AttestationRecord and shows up in JSONL audit
// rows; bad characters there break parsing or escape the audit
// tree.
//
// Rules:
//   - non-empty after trim, ≤ 256 bytes
//   - no whitespace, no control bytes (< 0x20 or 0x7F)
//   - no path separators ('/', '\\', NUL)
//   - no ".." substring (path-traversal guard)
//   - cannot start with '.' or '-' (no dotfiles or flag-likes)
const maxOpIDBytes = 256

// op-id wire-form sentinel errors (gate-2 CORR-A-12). The BDD
// spec at specs/features/attestation.feature:175-186 requires the
// wire forms "op-id-required" / "op-id-too-long" /
// "op-id-invalid-characters" so downstream tooling can parse on
// error names rather than English strings.
var (
	ErrOpIDRequired          = errors.New("op-id-required")
	ErrOpIDTooLong           = errors.New("op-id-too-long")
	ErrOpIDInvalidCharacters = errors.New("op-id-invalid-characters")
)

func validateOpID(id string) error {
	if id == "" {
		return ErrOpIDRequired
	}
	if len(id) > maxOpIDBytes {
		return fmt.Errorf("%w: %d > %d bytes", ErrOpIDTooLong, len(id), maxOpIDBytes)
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c < 0x20 || c == 0x7F:
			return fmt.Errorf("%w: control byte at offset %d (0x%02x)", ErrOpIDInvalidCharacters, i, c)
		case c == '/' || c == '\\' || c == 0:
			return fmt.Errorf("%w: path-separator at offset %d (%q)", ErrOpIDInvalidCharacters, i, string(c))
		case c == ' ' || c == '\t':
			return fmt.Errorf("%w: whitespace at offset %d", ErrOpIDInvalidCharacters, i)
		}
	}
	if strings.Contains(id, "..") {
		return fmt.Errorf(`%w: contains ".." (path-traversal guard)`, ErrOpIDInvalidCharacters)
	}
	if id[0] == '.' || id[0] == '-' {
		return fmt.Errorf("%w: must not start with %q", ErrOpIDInvalidCharacters, string(id[0]))
	}
	if strings.HasSuffix(id, ".") {
		return fmt.Errorf("%w: trailing '.' rejected", ErrOpIDInvalidCharacters)
	}
	// Gate-2 CORR-A-21 / SEC-L-1: reject any rune in the Unicode
	// Format or Control class. RTL override U+202E and ZWSP U+200B
	// pass the byte-level checks (their UTF-8 encoding is
	// 0xE2/0x80/0xAE etc. — all >= 0x80) but render as
	// invisible-or-direction-changing characters, enabling
	// operator-impersonation phishing.
	for _, r := range id {
		if unicode.IsControl(r) || isFormatRune(r) {
			return fmt.Errorf("%w: contains Unicode format/control rune U+%04X", ErrOpIDInvalidCharacters, r)
		}
	}
	return nil
}

// isFormatRune reports whether r is in the Unicode Format
// (Cf) category — RTL overrides, zero-width joiners, etc.
func isFormatRune(r rune) bool {
	return unicode.Is(unicode.Cf, r)
}

// validateAndNormalizeOpID applies the cmd/ghyll-local validateOpID
// rules (which are STRICTER than bootstrap's — they also reject
// leading '.'/'-' and trailing '.'), then NFC-normalizes the result
// via bootstrap.ValidateAndNormalizeOpID so the stored form matches
// what bootstrap.Session would store.
//
// Returns the normalized form on success. This is the SINGLE
// canonical entry point inside cmd/ghyll: callers that persist an
// op-id (ghyll init, ghyll init attest) and the per-session
// `/op-id` REPL handler all route through here so the grid's
// created-by-op-id, the JSONL attestations, and the live session
// state are equality-comparable across surfaces (H-A + M-C
// post-prod-readiness adversarial findings).
//
// validateOpID remains exported in-package for unit-test addressing
// of the strict-rule set in isolation; do not call it directly from
// production code paths.
func validateAndNormalizeOpID(id string) (string, error) {
	if err := validateOpID(id); err != nil {
		return "", err
	}
	// bootstrap.ValidateAndNormalizeOpID re-runs its (looser)
	// validation, NFC-normalizes, and returns the canonical form.
	// We discard its possible refusals because validateOpID has
	// already rejected the stricter cases; what we want is the
	// normalized form. If bootstrap somehow refuses (e.g., a future
	// rule diverges), surface that as the canonical wire-form
	// error so downstream tooling sees a consistent shape.
	normalized, err := bootstrap.ValidateAndNormalizeOpID(id)
	if err != nil {
		return "", err
	}
	return normalized, nil
}

// stripControlBytes drops < 0x20 (except \n/\t/\r preserved as
// space) and 0x7F. Gate-2 SEC-M-3 helper for operator-supplied
// free-form fields headed for JSONL audit + UI render.
func stripControlBytes(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7F {
			if c == '\n' || c == '\t' || c == '\r' {
				out = append(out, ' ')
			}
			continue
		}
		out = append(out, c)
	}
	return string(out)
}

// handleAttestCommand parses `/attest <ref> <verdict> [reason]` and
// records an AttestationRecord through the runtime AttestationStore.
// Per ADR-009 the SourceRole/TargetRole on the record drive §12.2
// enforcement; we look them up from the grid arrow the ref points at.
func (s *Session) handleAttestCommand(arg string) SlashCommandResult {
	if s.engine == nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "✗ engine not initialized; /attest unavailable",
		}
	}
	if s.opID == "" {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "✗ /op-id required before /attest",
		}
	}
	parts := strings.SplitN(arg, " ", 3)
	if len(parts) < 2 {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "usage: /attest <attestation-id> <pass|fail|insufficient-basis> [reason]",
		}
	}
	ref := strings.TrimSpace(parts[0])
	verdictStr := strings.TrimSpace(parts[1])
	reason := ""
	if len(parts) == 3 {
		reason = strings.TrimSpace(parts[2])
		// Gate-2 SEC-M-3: cap reason at 4 KiB + strip control
		// bytes so an oversized or smuggled-control reason can't
		// flow into JSONL audit + UI rendering paths.
		const maxReasonBytes = 4 * 1024
		if len(reason) > maxReasonBytes {
			reason = reason[:maxReasonBytes]
		}
		reason = stripControlBytes(reason)
	}

	verdict, err := parseOperatorVerdict(verdictStr)
	if err != nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: fmt.Sprintf("✗ %s", err.Error()),
		}
	}

	// Decode the ref to extract arrow + clause + version.
	parsed, err := parseAttestationRef(ref)
	if err != nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: fmt.Sprintf("✗ invalid attestation-id %q: %v", ref, err),
		}
	}

	// Look up the arrow so we can record source/target roles for
	// the §12.2 self-cert audit.
	gridArrow, ok := s.engine.Grid().Lookup(parsed.arrowID)
	if !ok {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: fmt.Sprintf("✗ arrow %q not in grid", parsed.arrowID),
		}
	}

	// AttestedByRole defaults to "operator" — the operator's
	// claimed identity is captured in OpID. AttestedByRole MUST
	// NOT match source or target per §12.2; "operator" is the
	// safe synthetic role that bypasses both diamond roles.
	//
	// Tier 2 (gate-1 F-6): PassID is required for the tree
	// writer's per-pass path encoding. The /attest escape hatch
	// looks up the live pass-id from evaluation_runs JOIN on
	// depth_type_attestation_ref = ref. If no in-flight pass
	// owns the ref, the verdict is refused with a typed message.
	passID, lookupErr := s.lookupPassIDForRef(ref)
	if lookupErr != nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: fmt.Sprintf("✗ /attest: no live pass owns %q: %v (use the modal flow during a session)", ref, lookupErr),
		}
	}
	// Gate-2 CORR-A-11: synthesize the Tier 2 Unit + payload from
	// the verdict + reason. The /attest CLI is a power-user escape
	// hatch — the reason string carries both the human-readable
	// note AND (for fail / insufficient-basis) the typed payload:
	//
	//   pass               → Unit=confirm, empty payload
	//   fail               → Unit=record-locations-inspected, reason
	//                        becomes a singleton Inspected entry
	//                        (real modal flow collects comma-separated
	//                        locations; /attest treats reason as one
	//                        location)
	//   insufficient-basis → Unit=write-residue-note, reason becomes
	//                        the Residue text
	unit, payload := synthesizeAttestUnitPayload(verdict, reason)

	rec := runner.AttestationRecord{
		ID:             ref,
		Kind:           parsed.kind,
		ArrowID:        parsed.arrowID,
		ClauseID:       parsed.clauseID,
		OpID:           s.opID,
		AttestedByRole: "operator",
		SourceRole:     gridArrow.SourceRole,
		TargetRole:     gridArrow.TargetRole,
		Verdict:        verdict,
		Reason:         reason,
		Timestamp:      time.Now().UnixNano(),
		GridVersion:    parsed.gridVersion,
		// Tier 2 fields:
		PassID:      passID,
		Context:     gridArrow.Context,
		Stratum:     gridArrow.Stratum,
		Unit:        unit,
		UnitPayload: payload,
		// HintJSON stays at the default '{}'; the /attest CLI is
		// a power-user replay path, no modal interaction.
		HintJSON: "{}",
	}
	// Marshal the typed payload to UnitPayloadJSON so the JSONL
	// audit line and engine row carry the canonical serialization
	// (matches the modal driver's buildRecord behavior).
	if payloadJSON, jerr := json.Marshal(payload); jerr == nil {
		rec.UnitPayloadJSON = string(payloadJSON)
	}
	if err := s.engine.AttestationStore().Record(rec); err != nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: fmt.Sprintf("✗ Record: %v", err),
		}
	}
	return SlashCommandResult{
		Handled: true, ContinueLoop: true,
		Output: fmt.Sprintf("✓ attestation %s recorded: verdict=%s by op-id=%s",
			ref, verdict, s.opID),
	}
}

// synthesizeAttestUnitPayload picks the Tier 2 Unit + typed
// payload for the /attest CLI based on the operator's verdict.
// Mirrors the modal driver's per-verdict shape so /attest-recorded
// rows are indistinguishable from modal-flow records once on
// disk. Gate-2 CORR-A-11.
func synthesizeAttestUnitPayload(verdict runner.AttestationVerdict, reason string) (runner.VerdictUnit, runner.VerdictUnitPayload) {
	switch verdict {
	case runner.AttestationPass:
		return runner.VerdictUnitConfirm, runner.VerdictUnitPayload{}
	case runner.AttestationFail:
		inspected := []string{}
		if reason != "" {
			inspected = append(inspected, reason)
		}
		return runner.VerdictUnitRecordLocationsInspected,
			runner.VerdictUnitPayload{Inspected: inspected}
	case runner.AttestationInsufficientBasis:
		return runner.VerdictUnitWriteResidueNote,
			runner.VerdictUnitPayload{Residue: reason}
	default:
		return "", runner.VerdictUnitPayload{}
	}
}

// handleAttestationsCommand lists recorded attestations. With no
// arg, lists every attestation in the store. With an arrow-id
// arg, filters to that arrow.
func (s *Session) handleAttestationsCommand(arrowArg string) SlashCommandResult {
	if s.engine == nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "✗ engine not initialized; /attestations unavailable",
		}
	}
	var recs []runner.AttestationRecord
	if arrowArg == "" {
		recs = s.engine.AttestationStore().All()
	} else {
		recs = s.engine.AttestationStore().ForArrow(arrowArg)
	}
	if len(recs) == 0 {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "no attestations recorded",
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "attestations (%d):\n", len(recs))
	for _, r := range recs {
		clause := r.ClauseID
		if clause == "" {
			clause = "<arrow-scope>"
		}
		// Gate-2 SEC-H-2: every operator-controlled field (ID, OpID,
		// Reason) flows through sanitizeOneLine so a tampered JSONL
		// row with ANSI escapes / control bytes can't smuggle them
		// into the operator's terminal via /attestations output.
		fmt.Fprintf(&b, "  %s  arrow=%s clause=%s verdict=%s op=%s\n",
			sanitizeOneLine(r.ID),
			sanitizeOneLine(r.ArrowID),
			sanitizeOneLine(clause),
			sanitizeOneLine(string(r.Verdict)),
			sanitizeOneLine(r.OpID))
	}
	return SlashCommandResult{
		Handled: true, ContinueLoop: true,
		Output: strings.TrimRight(b.String(), "\n"),
	}
}

// lookupPassIDForRef queries evaluation_runs for the pass-id
// associated with a depth_type_attestation_ref. Tier 2 (gate-1
// F-6): the /attest CLI escape hatch needs a non-empty PassID to
// satisfy the tree writer's path encoder. The pass-id lives on
// the evaluation_runs row that the dispatcher persisted when the
// clause first evaluated.
//
// Returns the pass-id string or an error if no row owns the ref.
func (s *Session) lookupPassIDForRef(ref string) (string, error) {
	if s.engine == nil || s.engine.Store() == nil {
		return "", errors.New("engine unavailable")
	}
	var passID string
	err := s.engine.Store().DB().QueryRowContext(
		gocontext.Background(),
		`SELECT pass_id FROM evaluation_runs WHERE depth_type_attestation_ref = ? LIMIT 1`,
		ref,
	).Scan(&passID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(passID) == "" {
		return "", errors.New("evaluation_runs.pass_id is empty for that ref")
	}
	return passID, nil
}

// handlePassByIDCommand renders the engine row for one pass-id.
// Wired by M-7 (gate-2 auditor) so the operator can look up
// historical (closed/aborted) passes that the in-memory registry
// doesn't carry.
func (s *Session) handlePassByIDCommand(id string) SlashCommandResult {
	if s.engine == nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "✗ engine not initialized; /passes unavailable",
		}
	}
	if id == "" {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "usage: /passes <pass-id>",
		}
	}
	rec, ok, err := s.engine.Store().GetPass(gocontext.Background(), id)
	if err != nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: fmt.Sprintf("✗ GetPass failed: %v", err),
		}
	}
	if !ok {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: fmt.Sprintf("not-found: no pass with id %q", id),
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "pass %s\n", rec.PassID)
	fmt.Fprintf(&b, "  role:         %s\n", rec.Role)
	fmt.Fprintf(&b, "  context:      %s\n", rec.Context)
	fmt.Fprintf(&b, "  arrow_id:     %s\n", rec.ArrowID)
	fmt.Fprintf(&b, "  grid_version: %d\n", rec.GridVersion)
	fmt.Fprintf(&b, "  state:        %s\n", rec.State)
	fmt.Fprintf(&b, "  opened_at:    %s\n", rec.OpenedAt)
	if rec.ClosedAt != "" {
		fmt.Fprintf(&b, "  closed_at:    %s\n", rec.ClosedAt)
	}
	if rec.CloseReason != "" {
		fmt.Fprintf(&b, "  close_reason: %s\n", rec.CloseReason)
	}
	if rec.RecoveredAt != "" {
		fmt.Fprintf(&b, "  recovered_at: %s\n", rec.RecoveredAt)
	}
	return SlashCommandResult{
		Handled: true, ContinueLoop: true,
		Output: strings.TrimRight(b.String(), "\n"),
	}
}

// handlePassesCommand lists currently-open passes from the
// PassRegistry.
func (s *Session) handlePassesCommand() SlashCommandResult {
	if s.engine == nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "✗ engine not initialized; /passes unavailable",
		}
	}
	passes := s.engine.Passes().All()
	if len(passes) == 0 {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "no open passes",
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "open passes (%d):\n", len(passes))
	for _, p := range passes {
		fmt.Fprintf(&b, "  %s  role=%s context=%s arrow=%s state=%s\n",
			p.ID(), p.Role(), p.Context(), p.ArrowID(), p.State())
	}
	return SlashCommandResult{
		Handled: true, ContinueLoop: true,
		Output: strings.TrimRight(b.String(), "\n"),
	}
}

// parseOperatorVerdict accepts the canonical names + a couple of
// operator-friendly aliases.
func parseOperatorVerdict(s string) (runner.AttestationVerdict, error) {
	switch strings.ToLower(s) {
	case "pass", "p", "ok":
		return runner.AttestationPass, nil
	case "fail", "f", "no":
		return runner.AttestationFail, nil
	case "insufficient-basis", "insufficient", "ib":
		return runner.AttestationInsufficientBasis, nil
	default:
		return "", fmt.Errorf("verdict %q not in {pass, fail, insufficient-basis}", s)
	}
}

// parseAttestationRef decodes an ID produced by
// runner.ComputeAttestationID. Two shapes:
//
//	att-<arrowID>-<clauseID>-v<gridVersion>   (depth-type, with clause)
//	att-<arrowID>-v<gridVersion>              (on-the-spot, no clause)
type parsedAttestationRef struct {
	kind        runner.AttestationKind
	arrowID     string
	clauseID    string
	gridVersion uint64
}

func parseAttestationRef(ref string) (parsedAttestationRef, error) {
	if !strings.HasPrefix(ref, "att-") {
		return parsedAttestationRef{}, fmt.Errorf("missing 'att-' prefix")
	}
	body := strings.TrimPrefix(ref, "att-")
	parts := strings.Split(body, "-v")
	if len(parts) != 2 {
		return parsedAttestationRef{}, fmt.Errorf("expected '<ids>-v<version>' shape")
	}
	idPart, verPart := parts[0], parts[1]
	var ver uint64
	if _, err := fmt.Sscanf(verPart, "%d", &ver); err != nil {
		return parsedAttestationRef{}, fmt.Errorf("invalid version %q", verPart)
	}
	// idPart is either "<arrow>" (on-the-spot) or "<arrow>-<clause>"
	// (depth-type). Arrow / clause IDs themselves can contain
	// dashes; we conservatively split on the LAST dash so the
	// arrow ID may include dashes too. If no dash, it's on-the-spot.
	idx := strings.LastIndex(idPart, "-")
	if idx < 0 {
		return parsedAttestationRef{
			kind:        runner.AttestationKindOnTheSpot,
			arrowID:     idPart,
			gridVersion: ver,
		}, nil
	}
	return parsedAttestationRef{
		kind:        runner.AttestationKindDepthType,
		arrowID:     idPart[:idx],
		clauseID:    idPart[idx+1:],
		gridVersion: ver,
	}, nil
}
