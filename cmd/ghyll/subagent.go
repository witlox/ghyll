package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	gocontext "context"
	"github.com/witlox/ghyll/config"
	ghyllcontext "github.com/witlox/ghyll/context"
	"github.com/witlox/ghyll/dialect"
	"github.com/witlox/ghyll/stream"
	"github.com/witlox/ghyll/tool"
	"github.com/witlox/ghyll/types"
)

// excludedSubAgentTools are tools not available to sub-agents.
// Invariant 41: sub-agents cannot spawn sub-agents (depth 1 only).
var excludedSubAgentTools = map[string]bool{
	"agent":           true,
	"enter_plan_mode": true,
	"exit_plan_mode":  true,
}

// RunSubAgent executes a focused sub-agent as a tool call within the parent session.
// Invariant 38: isolated context, role-free, no parent history.
// Invariant 39: shares session lockfile (no additional lock).
// Invariant 40: terminates at max turns.
// Invariant 41a: terminates at token budget.
func RunSubAgent(parentSession *Session, task string) types.ToolResult {
	cfg := parentSession.cfg
	agentCfg := cfg.SubAgent

	// Resolve sub-agent model (default: fast tier)
	modelName := agentCfg.DefaultModel
	if modelName == "" {
		modelName = cfg.Routing.DefaultModel
	}
	modelCfg, ok := cfg.Models[modelName]
	if !ok {
		return types.ToolResult{Error: fmt.Sprintf("sub-agent model %q not configured", modelName)}
	}

	// Resolve dialect functions for the sub-agent model
	var (
		systemPromptFn func(string) string
		buildMsgFn     func([]types.Message, string) []map[string]any
		tokenCountFn   func([]types.Message) int
	)
	switch normalizeDialect(modelCfg.Dialect) {
	case "glm":
		systemPromptFn = dialect.GLMSystemPrompt
		buildMsgFn = dialect.GLMBuildMessages
		tokenCountFn = dialect.GLMTokenCount
	default:
		systemPromptFn = dialect.MinimaxSystemPrompt
		buildMsgFn = dialect.MinimaxBuildMessages
		tokenCountFn = dialect.MinimaxTokenCount
	}
	// parseToolCalls is handled by the stream client internally via dialect parsing

	// Build sub-agent system prompt: dialect base + workflow instructions, NO role, NO plan mode
	sysPrompt := systemPromptFn(parentSession.workdir)
	if parentSession.wf != nil {
		if parentSession.wf.GlobalInstructions != "" {
			sysPrompt += "\n\n" + parentSession.wf.GlobalInstructions
		}
		if parentSession.wf.ProjectInstructions != "" {
			sysPrompt += "\n\n" + parentSession.wf.ProjectInstructions
		}
	}

	// Create sub-agent stream client
	client := stream.NewClient(modelCfg.Endpoint, &stream.ClientOptions{
		MaxRetries:    3,
		BaseBackoffMs: 1000,
		ModelName:     modelCfg.Dialect,
	})

	// Create sub-agent context manager (isolated)
	ctxMgr := ghyllcontext.NewManager(ghyllcontext.ManagerConfig{
		MaxContext:       modelCfg.MaxContext,
		PreserveTurns:    3,
		CompactThreshold: 0.9,
	}, ghyllcontext.ManagerDeps{
		TokenCount: tokenCountFn,
		// No compaction, no checkpoint, no embedding for sub-agents
	})

	// Initialize context with system prompt and task
	ctxMgr.AddMessage(types.Message{Role: "system", Content: sysPrompt})
	ctxMgr.AddMessage(types.Message{Role: "user", Content: task})

	// Wall-clock timeout
	timeout := time.Duration(agentCfg.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 300 * time.Second
	}
	deadline := time.Now().Add(timeout)

	// Sub-agent turn loop
	var totalTokens int
	var lastContent string

	for turn := 0; turn < agentCfg.MaxTurns; turn++ {
		// Check wall-clock timeout
		if time.Now().After(deadline) {
			return types.ToolResult{
				Output: fmt.Sprintf("[sub-agent wall-clock timeout after %d turns]\n\n%s", turn, lastContent),
			}
		}

		// Check token budget (invariant 41a)
		if totalTokens >= agentCfg.TokenBudget {
			return types.ToolResult{
				Output: fmt.Sprintf("[sub-agent token budget exhausted after %d turns, %d tokens]\n\n%s", turn, totalTokens, lastContent),
			}
		}

		// Send to model
		messages := buildMsgFn(ctxMgr.Messages(), sysPrompt)
		resp, err := client.SendStream(messages, func(delta string) {
			parentSession.renderer.RenderDelta(delta) // Show sub-agent output
		})
		if err != nil {
			// If context too long, return partial result instead of opaque error
			errMsg := err.Error()
			if strings.Contains(errMsg, "context") || strings.Contains(errMsg, "length") {
				return types.ToolResult{
					Output: fmt.Sprintf("[sub-agent context overflow after %d turns]\n\n%s", turn, lastContent),
				}
			}
			return types.ToolResult{Error: fmt.Sprintf("sub-agent model unreachable: %v", err)}
		}

		totalTokens += resp.Usage.TotalTokens

		// Add assistant response
		ctxMgr.AddMessage(types.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		if resp.Content != "" {
			parentSession.renderer.RenderComplete()
			lastContent = resp.Content
		}

		// If no tool calls, sub-agent is done
		if len(resp.ToolCalls) == 0 {
			return types.ToolResult{Output: resp.Content}
		}

		// Execute tool calls (excluding agent/plan tools)
		for _, tc := range resp.ToolCalls {
			if excludedSubAgentTools[tc.Function.Name] {
				ctxMgr.AddMessage(types.Message{
					Role:       "tool",
					Content:    fmt.Sprintf("tool %q is not available to sub-agents", tc.Function.Name),
					ToolCallID: tc.ID,
					Name:       tc.Function.Name,
				})
				continue
			}

			parentSession.renderer.RenderToolCall(tc.Function.Name, tc.Function.Arguments)
			toolResult := executeSubAgentTool(parentSession.cfg, parentSession.workdir, tc)
			parentSession.renderer.RenderToolResult(toolResult.Output, toolResult.Error, toolResult.TimedOut)

			content := toolResult.Output
			if toolResult.Error != "" {
				content = toolResult.Error
			}
			ctxMgr.AddMessage(types.Message{
				Role:       "tool",
				Content:    content,
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
			})
		}
	}

	// Turn limit reached (invariant 40)
	return types.ToolResult{
		Output: fmt.Sprintf("[sub-agent reached turn limit (%d turns)]\n\n%s", agentCfg.MaxTurns, lastContent),
	}
}

// executeSubAgentTool runs a tool for the sub-agent. Same tools as parent minus excluded.
func executeSubAgentTool(cfg *config.Config, workdir string, tc types.ToolCall) types.ToolResult {
	ctx := gocontext.Background()
	bashTimeout := time.Duration(cfg.Tools.BashTimeoutSeconds) * time.Second
	fileTimeout := time.Duration(cfg.Tools.FileTimeoutSeconds) * time.Second
	webTimeout := time.Duration(cfg.Tools.WebTimeoutSeconds) * time.Second
	if webTimeout == 0 {
		webTimeout = 30 * time.Second
	}
	webMaxTokens := cfg.Tools.WebMaxResponseTokens
	if webMaxTokens == 0 {
		webMaxTokens = 10000
	}

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
	}
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		return types.ToolResult{Error: fmt.Sprintf("failed to parse tool arguments: %v", err)}
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
		return tool.Git(ctx, workdir, gitArgs, bashTimeout)
	case "grep":
		return tool.Grep(ctx, args.Pattern, args.Path, bashTimeout)
	case "glob":
		path := args.Path
		if path == "" {
			path = workdir
		}
		return tool.Glob(ctx, args.Pattern, path, bashTimeout)
	case "web_fetch":
		return tool.WebFetch(ctx, args.URL, webMaxTokens, webTimeout)
	case "web_search":
		backend := cfg.Tools.WebSearchBackend
		if backend == "" {
			backend = "https://html.duckduckgo.com"
		}
		return tool.WebSearch(ctx, args.Query, backend, 10, webTimeout)
	default:
		return types.ToolResult{Error: fmt.Sprintf("unknown tool: %s", tc.Function.Name)}
	}
}
