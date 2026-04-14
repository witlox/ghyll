package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gocontext "context"
	"github.com/witlox/ghyll/config"
	ghyllcontext "github.com/witlox/ghyll/context"
	"github.com/witlox/ghyll/dialect"
	"github.com/witlox/ghyll/memory"
	"github.com/witlox/ghyll/stream"
	"github.com/witlox/ghyll/tool"
	"github.com/witlox/ghyll/types"
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

	// Workflow
	wf         *workflow.Workflow
	activeRole string // currently active role name, empty if none

	// Resume
	resumeRef *memory.ResumeRef // set if this session was resumed

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
	Workdir     string
	SessionID   string
	Renderer    *stream.Renderer
	Output      func(string)
}

// NewSession creates and initializes a session.
func NewSession(sc SessionConfig) (*Session, error) {
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
	}

	if s.renderer == nil {
		s.renderer = stream.NewRenderer(os.Stdout)
	}
	if s.output == nil {
		s.output = func(msg string) { fmt.Println(msg) }
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

	// Resolve dialect functions
	s.resolveDialect()

	// Create stream client
	modelCfg := sc.Cfg.Models[s.activeModel]
	s.streamClient = stream.NewClient(modelCfg.Endpoint, &stream.ClientOptions{
		MaxRetries:    3,
		BaseBackoffMs: 1000,
		ModelName:     modelCfg.Dialect,
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
	globalDir := filepath.Join(os.Getenv("HOME"), ".ghyll")
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

	return s, nil
}

func (s *Session) resolveDialect() {
	switch s.cfg.Models[s.activeModel].Dialect {
	case "glm5":
		s.systemPrompt = dialect.GLM5SystemPrompt
		s.planModePrompt = dialect.GLM5PlanModePrompt
		s.buildMessages = dialect.GLM5BuildMessages
		s.parseToolCalls = dialect.GLM5ParseToolCalls
		s.compactionPrompt = dialect.GLM5CompactionPrompt
		s.tokenCount = dialect.GLM5TokenCount
		s.handoffSummary = dialect.GLM5HandoffSummary
	default: // minimax_m25
		s.systemPrompt = dialect.M25SystemPrompt
		s.planModePrompt = dialect.M25PlanModePrompt
		s.buildMessages = dialect.M25BuildMessages
		s.parseToolCalls = dialect.M25ParseToolCalls
		s.compactionPrompt = dialect.M25CompactionPrompt
		s.tokenCount = dialect.M25TokenCount
		s.handoffSummary = dialect.M25HandoffSummary
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

	if decision.Action == "escalate" || decision.Action == "de_escalate" {
		if err := s.handleHandoff(decision); err != nil {
			s.output(fmt.Sprintf("⚠ handoff failed: %v", err))
		}
	}

	// Send to model
	return s.sendAndProcess()
}

func (s *Session) sendAndProcess() (string, error) {
	// Finding 1: guard against unbounded tool call recursion
	if s.toolDepth > maxToolDepth {
		return "", fmt.Errorf("tool call depth exceeded (%d), stopping", maxToolDepth)
	}

	sysPrompt := s.composedSystemPrompt()
	messages := s.buildMessages(s.ctxManager.Messages(), sysPrompt)
	resp, err := s.streamClient.SendStream(messages, func(delta string) {
		s.renderer.RenderDelta(delta)
	})

	if err != nil {
		var sErr *stream.StreamError
		if stream.AsStreamError(err, &sErr) && sErr.ContextTooLong {
			endpoint := s.cfg.Models[s.activeModel].Endpoint
			if cErr := s.ctxManager.ReactiveCompact(s.activeModel, endpoint, s.compactionPrompt()); cErr != nil {
				return "", fmt.Errorf("reactive compaction failed: %w", cErr)
			}
			messages = s.buildMessages(s.ctxManager.Messages(), sysPrompt)
			resp, err = s.streamClient.SendStream(messages, func(delta string) {
				s.renderer.RenderDelta(delta)
			})
			if err != nil {
				return "", err
			}
		} else {
			return "", err
		}
	}

	// Add assistant response to context
	s.ctxManager.AddMessage(types.Message{
		Role:      "assistant",
		Content:   resp.Content,
		ToolCalls: resp.ToolCalls,
	})

	// Finish the streaming line
	if resp.Content != "" {
		s.renderer.RenderComplete()
	}

	if resp.Partial {
		s.renderer.RenderWarning("stream interrupted")
		return resp.Content, nil
	}

	// Execute tool calls
	if len(resp.ToolCalls) > 0 {
		for _, tc := range resp.ToolCalls {
			s.renderer.RenderToolCall(tc.Function.Name, tc.Function.Arguments)
			toolResult := s.executeTool(tc)
			s.renderer.RenderToolResult(toolResult.Output, toolResult.Error, toolResult.TimedOut)
			s.ctxManager.AddMessage(types.Message{
				Role:       "tool",
				Content:    toolResult.Output,
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
	case "bash":
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

	// Create handoff checkpoint on current model (invariant 10)
	_ = s.createCheckpoint(ghyllcontext.CheckpointRequest{
		SessionID:   s.sessionID,
		Turn:        s.ctxManager.Turn(),
		ActiveModel: s.activeModel,
		Summary:     fmt.Sprintf("handoff: %s → %s", prevModel, decision.TargetModel),
		Messages:    s.ctxManager.Messages(),
		Reason:      "handoff",
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

	// Switch dialect
	s.activeModel = decision.TargetModel
	s.resolveDialect()

	// Format handoff context using target dialect's HandoffSummary
	handoffMsgs := s.handoffSummary(cp, recentTurns)

	// Update stream client endpoint
	modelCfg := s.cfg.Models[s.activeModel]
	s.streamClient = stream.NewClient(modelCfg.Endpoint, &stream.ClientOptions{
		MaxRetries:    3,
		BaseBackoffMs: 1000,
		ModelName:     modelCfg.Dialect,
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

	client := stream.NewClient(req.ModelEndpoint, &stream.ClientOptions{
		MaxRetries:    1,
		BaseBackoffMs: 500,
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
	// Only include resumeRef in the first checkpoint
	if s.resumeRef != nil {
		s.resumeRef = nil // clear after first use
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

	// Append workflow instructions (invariant 47: global first, project appended)
	if s.wf != nil {
		if s.wf.GlobalInstructions != "" {
			prompt += "\n\n" + s.wf.GlobalInstructions
		}
		if s.wf.ProjectInstructions != "" {
			prompt += "\n\n" + s.wf.ProjectInstructions
		}
	}

	// Append active role overlay (invariant 50: replace, not accumulate)
	if s.activeRole != "" && s.wf != nil {
		if content, ok := s.wf.Roles[s.activeRole]; ok {
			prompt += "\n\n" + content
		}
	}

	// Append plan mode overlay (invariant 36: advisory only)
	if s.planMode && s.planModePrompt != nil {
		prompt += "\n\n" + s.planModePrompt()
	}

	return prompt
}

// SwitchRole changes the active role overlay.
// Invariant 50: non-destructive — no compaction, no checkpoint.
func (s *Session) SwitchRole(name string) error {
	if s.wf == nil {
		return fmt.Errorf("role not found: %s (no workflow loaded)", name)
	}
	if _, ok := s.wf.Roles[name]; !ok {
		return fmt.Errorf("role not found: %s", name)
	}
	s.activeRole = name
	return nil
}

// PlanMode returns whether plan mode is active.
func (s *Session) PlanMode() bool {
	return s.planMode
}

// SetPlanMode sets plan mode on or off and returns the new state.
func (s *Session) SetPlanMode(active bool) {
	s.planMode = active
}

// ActiveModel returns the current model name.
func (s *Session) ActiveModel() string {
	return s.activeModel
}

// Prompt returns the terminal prompt string.
func (s *Session) Prompt() string {
	return fmt.Sprintf("ghyll [%s] %s ▸ ", s.activeModel, s.workdir)
}
