package dialect

import (
	"encoding/json"
	"fmt"

	"github.com/witlox/ghyll/memory"
	"github.com/witlox/ghyll/types"
)

// MinimaxSystemPrompt returns the system prompt for the MiniMax family (M2.5, M2.7, etc.).
func MinimaxSystemPrompt(workdir string) string {
	return fmt.Sprintf(`You are a coding assistant working in %s. You have access to tools for reading files, writing files, executing bash commands, and searching code. Use tools to accomplish tasks. Be concise and direct.`, workdir)
}

// MinimaxBuildMessages formats messages for MiniMax family OpenAI-compatible API.
func MinimaxBuildMessages(msgs []types.Message, systemPrompt string) []map[string]any {
	result := make([]map[string]any, 0, len(msgs)+1)

	// System message first
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

// MinimaxParseToolCalls parses tool calls from MiniMax family response format.
// MiniMax uses the standard OpenAI tool_calls format.
func MinimaxParseToolCalls(raw json.RawMessage) ([]types.ToolCall, error) {
	return parseOpenAIToolCalls(raw)
}

// MinimaxPlanModePrompt returns additional system instructions for plan mode on MiniMax.
// Invariant 36: advisory only — all tools remain available.
func MinimaxPlanModePrompt() string {
	return `You are in PLAN MODE. Think before acting:
1. Analyze the problem and constraints
2. Consider approaches and trade-offs
3. Outline your plan before executing
4. Explain your reasoning for non-obvious choices
All tools remain available. Plan first, then act.`
}

// MinimaxCompactionPrompt returns the compaction instruction for MiniMax.
func MinimaxCompactionPrompt() string {
	return `Summarize the following conversation turns into a concise summary. Preserve:
- The original task/goal
- Key decisions made
- Files modified and why
- Current state of the work
- Any unresolved issues

Format as a structured summary that another model instance can use to continue the work.`
}

// MinimaxTokenCount estimates token count for MiniMax family messages.
// Uses a simple approximation: ~4 chars per token for English/code.
func MinimaxTokenCount(msgs []types.Message) int {
	total := 0
	for _, msg := range msgs {
		// Content tokens
		total += len(msg.Content) / 4
		// Tool call tokens (rough estimate)
		for _, tc := range msg.ToolCalls {
			total += len(tc.Function.Name)/4 + len(tc.Function.Arguments)/4 + 10
		}
		// Per-message overhead
		total += 4
	}
	return total
}

// MinimaxHandoffSummary formats a checkpoint for MiniMax to continue from.
func MinimaxHandoffSummary(cp memory.Checkpoint, recentTurns []types.Message) []types.Message {
	summary := fmt.Sprintf("Continuing from checkpoint (turn %d, previously on %s):\n\n%s",
		cp.Turn, cp.ActiveModel, cp.Summary)

	result := []types.Message{
		{Role: "system", Content: summary},
	}
	result = append(result, recentTurns...)
	return result
}
