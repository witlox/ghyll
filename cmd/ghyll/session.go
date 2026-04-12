package main

import (
	"encoding/json"
	"fmt"
	"strings"

	gocontext "context"
	"github.com/witlox/ghyll/config"
	ghyllcontext "github.com/witlox/ghyll/context"
	"github.com/witlox/ghyll/dialect"
	"github.com/witlox/ghyll/memory"
	"github.com/witlox/ghyll/stream"
	"github.com/witlox/ghyll/tool"
	"github.com/witlox/ghyll/types"
	"time"
)

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
	toolDepth    int
	sessionID    string
	workdir      string

	// Dialect functions resolved for active model
	systemPrompt     func(string) string
	buildMessages    func([]types.Message, string) []map[string]any
	parseToolCalls   func(json.RawMessage) ([]types.ToolCall, error)
	compactionPrompt func() string
	tokenCount       func([]types.Message) int
	handoffSummary   func(memory.Checkpoint, []types.Message) []types.Message

	// Output callback for terminal display
	output func(string)
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
	Workdir     string
	SessionID   string
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
		output:      sc.Output,
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
	endpoint := sc.Cfg.Models[s.activeModel].Endpoint
	s.streamClient = stream.NewClient(endpoint, &stream.ClientOptions{
		MaxRetries:    3,
		BaseBackoffMs: 1000,
	})

	// Create context manager with callbacks
	s.ctxManager = ghyllcontext.NewManager(ghyllcontext.ManagerConfig{
		MaxContext:       sc.Cfg.Models[s.activeModel].MaxContext,
		PreserveTurns:    3,
		CompactThreshold: 0.9,
	}, ghyllcontext.ManagerDeps{
		TokenCount:       s.tokenCount,
		CompactionCall:   s.compactionCall,
		CreateCheckpoint: s.createCheckpoint,
	})

	// Build system prompt
	sysPrompt := s.systemPrompt(s.workdir)
	s.ctxManager.AddMessage(types.Message{Role: "system", Content: sysPrompt})

	return s, nil
}

func (s *Session) resolveDialect() {
	switch s.cfg.Models[s.activeModel].Dialect {
	case "glm5":
		s.systemPrompt = dialect.GLM5SystemPrompt
		s.buildMessages = dialect.GLM5BuildMessages
		s.parseToolCalls = dialect.GLM5ParseToolCalls
		s.compactionPrompt = dialect.GLM5CompactionPrompt
		s.tokenCount = dialect.GLM5TokenCount
		s.handoffSummary = dialect.GLM5HandoffSummary
	default: // minimax_m25
		s.systemPrompt = dialect.M25SystemPrompt
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
	sysPrompt := s.systemPrompt(s.workdir)
	messages := s.buildMessages(s.ctxManager.Messages(), sysPrompt)
	resp, err := s.streamClient.Send(messages)

	if err != nil {
		var sErr *stream.StreamError
		if stream.AsStreamError(err, &sErr) && sErr.ContextTooLong {
			// Reactive compaction
			endpoint := s.cfg.Models[s.activeModel].Endpoint
			if cErr := s.ctxManager.ReactiveCompact(s.activeModel, endpoint, s.compactionPrompt()); cErr != nil {
				return "", fmt.Errorf("reactive compaction failed: %w", cErr)
			}
			// Retry once
			messages = s.buildMessages(s.ctxManager.Messages(), sysPrompt)
			resp, err = s.streamClient.Send(messages)
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

	if resp.Partial {
		s.output("⚠ stream interrupted")
		return resp.Content, nil
	}

	// Execute tool calls
	if len(resp.ToolCalls) > 0 {
		for _, tc := range resp.ToolCalls {
			toolResult := s.executeTool(tc)
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
	if turn > 0 && turn%s.cfg.Memory.CheckpointIntervalTurns == 0 {
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

	var args struct {
		Command string `json:"command"`
		Path    string `json:"path"`
		Content string `json:"content"`
		Pattern string `json:"pattern"`
		Args    string `json:"args"`
	}
	_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)

	switch tc.Function.Name {
	case "bash":
		return tool.Bash(ctx, args.Command, bashTimeout)
	case "read_file":
		return tool.ReadFile(ctx, args.Path, fileTimeout)
	case "write_file":
		return tool.WriteFile(ctx, args.Path, args.Content, fileTimeout)
	case "git":
		gitArgs := strings.Fields(args.Args)
		return tool.Git(ctx, s.workdir, gitArgs, bashTimeout)
	case "grep":
		return tool.Grep(ctx, args.Pattern, args.Path, bashTimeout)
	default:
		return types.ToolResult{Error: fmt.Sprintf("unknown tool: %s", tc.Function.Name)}
	}
}

func (s *Session) handleHandoff(decision dialect.RoutingDecision) error {
	prevModel := s.activeModel
	s.activeModel = decision.TargetModel
	s.resolveDialect()

	// Update stream client endpoint
	endpoint := s.cfg.Models[s.activeModel].Endpoint
	s.streamClient = stream.NewClient(endpoint, &stream.ClientOptions{
		MaxRetries:    3,
		BaseBackoffMs: 1000,
	})

	// Update context manager config
	s.ctxManager = ghyllcontext.NewManager(ghyllcontext.ManagerConfig{
		MaxContext:       s.cfg.Models[s.activeModel].MaxContext,
		PreserveTurns:    3,
		CompactThreshold: 0.9,
	}, ghyllcontext.ManagerDeps{
		TokenCount:       s.tokenCount,
		CompactionCall:   s.compactionCall,
		CreateCheckpoint: s.createCheckpoint,
	})

	s.output(fmt.Sprintf("⟳ switched to %s from %s", s.activeModel, prevModel))
	return nil
}

func (s *Session) compactionCall(req ghyllcontext.CompactionRequest) (string, error) {
	// Build compaction messages
	msgs := []map[string]any{
		{"role": "system", "content": req.CompactionPrompt},
	}
	for _, m := range req.TurnsToSummarize {
		msgs = append(msgs, map[string]any{
			"role":    m.Role,
			"content": m.Content,
		})
	}

	// Send to model
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

	// Get latest checkpoint for parent hash
	parentHash := "0000000000000000000000000000000000000000000000000000000000000000"
	if latest, err := s.store.LatestBySession(s.sessionID); err == nil {
		parentHash = latest.Hash
	}

	cp := &memory.Checkpoint{
		Version:      1,
		ParentHash:   parentHash,
		DeviceID:     s.deviceKey.DeviceID,
		AuthorID:     s.deviceKey.DeviceID,
		Timestamp:    time.Now().UnixNano(),
		SessionID:    req.SessionID,
		Turn:         req.Turn,
		ActiveModel:  req.ActiveModel,
		Summary:      req.Summary,
		FilesTouched: req.FilesTouched,
		ToolsUsed:    req.ToolsUsed,
		InjectionSig: req.InjectionSig,
	}

	memory.SignCheckpoint(cp, s.deviceKey.PrivateKey)
	return s.store.Append(cp)
}

// ActiveModel returns the current model name.
func (s *Session) ActiveModel() string {
	return s.activeModel
}

// Prompt returns the terminal prompt string.
func (s *Session) Prompt() string {
	return fmt.Sprintf("ghyll [%s] %s ▸ ", s.activeModel, s.workdir)
}
