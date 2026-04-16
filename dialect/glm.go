package dialect

import (
	"encoding/json"
	"fmt"

	"github.com/witlox/ghyll/memory"
	"github.com/witlox/ghyll/types"
)

// GLMSystemPrompt returns the system prompt for the GLM family (GLM-5, GLM-5.1, etc.).
func GLMSystemPrompt(workdir string) string {
	return fmt.Sprintf(`You are an expert coding assistant working in %s. You handle complex reasoning tasks, multi-step debugging, and architectural decisions. You have access to tools for reading files, writing files, executing bash commands, and searching code. Think step by step for complex problems.`, workdir)
}

// GLMBuildMessages formats messages for GLM family OpenAI-compatible API.
func GLMBuildMessages(msgs []types.Message, systemPrompt string) []map[string]any {
	result := make([]map[string]any, 0, len(msgs)+1)

	result = append(result, map[string]any{
		"role":    "system",
		"content": systemPrompt,
	})

	for _, msg := range msgs {
		m := map[string]any{
			"role":    msg.Role,
			"content": msg.Content,
		}
		if len(msg.ToolCalls) > 0 {
			m["tool_calls"] = msg.ToolCalls
		}
		if msg.ToolCallID != "" {
			m["tool_call_id"] = msg.ToolCallID
		}
		if msg.Name != "" {
			m["name"] = msg.Name
		}
		result = append(result, m)
	}

	return result
}

// GLMParseToolCalls parses tool calls from GLM family response format.
// GLM uses the standard OpenAI tool_calls format via SGLang.
func GLMParseToolCalls(raw json.RawMessage) ([]types.ToolCall, error) {
	return parseOpenAIToolCalls(raw)
}

// GLMPlanModePrompt returns additional system instructions for plan mode on GLM.
// Invariant 36: advisory only — all tools remain available.
func GLMPlanModePrompt() string {
	return `You are in PLAN MODE. Before taking any action, think deeply and systematically:
1. Analyze the full context and constraints before proposing changes
2. Consider multiple approaches and their trade-offs
3. Identify risks and edge cases
4. Outline your plan step by step before executing
5. For architectural decisions, explain your reasoning thoroughly
All tools remain available. Use them when ready, but think first.`
}

// GLMCompactionPrompt returns the compaction instruction for GLM.
// Accounts for DSA attention — emphasizes preserving structural decisions.
func GLMCompactionPrompt() string {
	return `Summarize the following conversation into a structured summary optimized for long-context continuation. Preserve:
- The original task/goal with full context
- All architectural and design decisions with rationale
- Files modified, created, or deleted with purpose
- Current state of implementation
- Unresolved issues and open questions
- Key constraints and invariants discovered

Structure the summary with clear sections. This will be used to continue complex reasoning tasks.`
}

// GLMTokenCount estimates token count for GLM family messages.
// GLM uses a different tokenizer — slightly more tokens per char for code.
func GLMTokenCount(msgs []types.Message) int {
	total := 0
	for _, msg := range msgs {
		total += len(msg.Content) / 3 // GLM tokenizer is slightly less efficient
		for _, tc := range msg.ToolCalls {
			total += len(tc.Function.Name)/3 + len(tc.Function.Arguments)/3 + 10
		}
		total += 4
	}
	return total
}

// GLMHandoffSummary formats a checkpoint for GLM to continue from.
func GLMHandoffSummary(cp memory.Checkpoint, recentTurns []types.Message) []types.Message {
	summary := fmt.Sprintf("Continuing from checkpoint (turn %d, previously on %s):\n\n%s\n\nThis task requires deep reasoning. Review the context carefully before proceeding.",
		cp.Turn, cp.ActiveModel, cp.Summary)

	result := []types.Message{
		{Role: "system", Content: summary},
	}
	result = append(result, recentTurns...)
	return result
}
