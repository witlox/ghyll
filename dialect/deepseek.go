package dialect

import (
	"encoding/json"
	"fmt"

	"github.com/witlox/ghyll/memory"
	"github.com/witlox/ghyll/types"
)

// DeepSeek dialect — covers DeepSeek-V3 and DeepSeek-Coder family.
// Self-hosted via SGLang / vLLM / llama.cpp speaks the standard
// OpenAI tool-call protocol; this dialect mirrors that.

// DeepSeekSystemPrompt returns the system prompt for the DeepSeek family.
func DeepSeekSystemPrompt(workdir string) string {
	return fmt.Sprintf(`You are an expert coding assistant working in %s. You handle code reasoning, multi-file debugging, and architectural tasks. You have access to tools for reading files, writing files, executing bash commands, and searching code. Be precise and verify before committing to a direction.`, workdir)
}

// DeepSeekBuildMessages formats messages for DeepSeek OpenAI-compatible API.
func DeepSeekBuildMessages(msgs []types.Message, systemPrompt string) []map[string]any {
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

// DeepSeekParseToolCalls parses tool calls from DeepSeek response format.
// DeepSeek uses the standard OpenAI tool_calls format.
func DeepSeekParseToolCalls(raw json.RawMessage) ([]types.ToolCall, error) {
	return parseOpenAIToolCalls(raw)
}

// DeepSeekPlanModePrompt returns additional system instructions for plan mode.
func DeepSeekPlanModePrompt() string {
	return `You are in PLAN MODE. Before taking any action:
1. Restate the task in your own words to confirm understanding
2. List the assumptions you are making
3. Enumerate two or three concrete approaches with trade-offs
4. Identify edge cases and failure modes
5. Outline your chosen plan step by step before executing
All tools remain available. Verify your reasoning before acting.`
}

// DeepSeekCompactionPrompt returns the compaction instruction for DeepSeek.
func DeepSeekCompactionPrompt() string {
	return `Summarize the following conversation into a structured summary optimized for continuation. Preserve:
- The original task and its full context
- Design decisions and their justification
- Files modified, created, or deleted, with intent for each
- Current implementation state and remaining work
- Constraints and invariants surfaced during work
- Open questions and uncertainties

Structure the summary with clear sections.`
}

// DeepSeekTokenCount estimates token count for DeepSeek messages.
// DeepSeek's BPE tokenizer averages ~3.5 chars per token on code.
func DeepSeekTokenCount(msgs []types.Message) int {
	total := 0
	for _, msg := range msgs {
		total += len(msg.Content) * 2 / 7 // ~3.5 chars/token
		for _, tc := range msg.ToolCalls {
			total += len(tc.Function.Name)*2/7 + len(tc.Function.Arguments)*2/7 + 10
		}
		total += 4
	}
	return total
}

// DeepSeekHandoffSummary formats a checkpoint for DeepSeek to continue from.
func DeepSeekHandoffSummary(cp memory.Checkpoint, recentTurns []types.Message) []types.Message {
	summary := fmt.Sprintf("Continuing from checkpoint (turn %d, previously on %s):\n\n%s\n\nReview the context before proceeding; verify any inherited assumption against the actual code.",
		cp.Turn, cp.ActiveModel, cp.Summary)

	result := []types.Message{
		{Role: "system", Content: summary},
	}
	result = append(result, recentTurns...)
	return result
}
