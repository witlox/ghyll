package dialect

import (
	"encoding/json"
	"fmt"

	"github.com/witlox/ghyll/memory"
	"github.com/witlox/ghyll/types"
)

// M25SystemPrompt returns the system prompt for MiniMax M2.5.
func M25SystemPrompt(workdir string) string {
	return fmt.Sprintf(`You are a coding assistant working in %s. You have access to tools for reading files, writing files, executing bash commands, and searching code. Use tools to accomplish tasks. Be concise and direct.`, workdir)
}

// M25BuildMessages formats messages for the MiniMax M2.5 OpenAI-compatible API.
func M25BuildMessages(msgs []types.Message, systemPrompt string) []map[string]any {
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

// M25ParseToolCalls parses tool calls from MiniMax M2.5 response format.
// M2.5 uses the standard OpenAI tool_calls format.
func M25ParseToolCalls(raw json.RawMessage) ([]types.ToolCall, error) {
	return parseOpenAIToolCalls(raw)
}

// M25CompactionPrompt returns the compaction instruction for M2.5.
func M25CompactionPrompt() string {
	return `Summarize the following conversation turns into a concise summary. Preserve:
- The original task/goal
- Key decisions made
- Files modified and why
- Current state of the work
- Any unresolved issues

Format as a structured summary that another model instance can use to continue the work.`
}

// M25TokenCount estimates token count for M2.5 messages.
// Uses a simple approximation: ~4 chars per token for English/code.
func M25TokenCount(msgs []types.Message) int {
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

// M25HandoffSummary formats a checkpoint for M2.5 to continue from.
func M25HandoffSummary(cp memory.Checkpoint, recentTurns []types.Message) []types.Message {
	summary := fmt.Sprintf("Continuing from checkpoint (turn %d, previously on %s):\n\n%s",
		cp.Turn, cp.ActiveModel, cp.Summary)

	result := []types.Message{
		{Role: "system", Content: summary},
	}
	result = append(result, recentTurns...)
	return result
}
