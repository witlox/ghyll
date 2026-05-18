package dialect

import (
	"encoding/json"
	"fmt"

	"github.com/witlox/ghyll/memory"
	"github.com/witlox/ghyll/types"
)

// Qwen dialect — covers Qwen2.5-Coder and Qwen3-Coder families. Self-
// hosted via SGLang / vLLM / llama.cpp speaks the standard OpenAI
// tool-call protocol; this dialect mirrors that.

// QwenSystemPrompt returns the system prompt for the Qwen Coder family.
func QwenSystemPrompt(workdir string) string {
	return fmt.Sprintf(`You are an expert coding assistant working in %s. You produce careful, idiomatic code with attention to the surrounding style. You have access to tools for reading files, writing files, executing bash commands, and searching code. Prefer to read the existing code before writing new code.`, workdir)
}

// QwenBuildMessages formats messages for Qwen OpenAI-compatible API.
func QwenBuildMessages(msgs []types.Message, systemPrompt string) []map[string]any {
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

// QwenParseToolCalls parses tool calls from Qwen Coder response format.
func QwenParseToolCalls(raw json.RawMessage) ([]types.ToolCall, error) {
	return parseOpenAIToolCalls(raw)
}

// QwenPlanModePrompt returns additional system instructions for plan mode on Qwen.
func QwenPlanModePrompt() string {
	return `You are in PLAN MODE. Before any action:
1. Read enough of the existing code to ground your plan in the actual surface
2. List the files you intend to read, write, or modify
3. Outline the change step by step
4. Note risks and edge cases
All tools remain available. Read first, plan, then act.`
}

// QwenCompactionPrompt returns the compaction instruction for Qwen Coder.
func QwenCompactionPrompt() string {
	return `Summarize the following conversation into a structured summary. Preserve:
- The original task
- Files modified, created, or deleted with intent
- Design decisions and their rationale
- Current state of implementation
- Open questions
- Style and convention observations specific to this codebase

Structure the summary with clear sections.`
}

// QwenTokenCount estimates token count for Qwen Coder messages.
// Qwen's BPE tokenizer averages ~3.2 chars per token on code.
func QwenTokenCount(msgs []types.Message) int {
	total := 0
	for _, msg := range msgs {
		total += len(msg.Content) * 5 / 16 // ~3.2 chars/token
		for _, tc := range msg.ToolCalls {
			total += len(tc.Function.Name)*5/16 + len(tc.Function.Arguments)*5/16 + 10
		}
		total += 4
	}
	return total
}

// QwenHandoffSummary formats a checkpoint for Qwen to continue from.
func QwenHandoffSummary(cp memory.Checkpoint, recentTurns []types.Message) []types.Message {
	summary := fmt.Sprintf("Continuing from checkpoint (turn %d, previously on %s):\n\n%s\n\nVerify the inherited code-style conventions before producing new code.",
		cp.Turn, cp.ActiveModel, cp.Summary)

	result := []types.Message{
		{Role: "system", Content: summary},
	}
	result = append(result, recentTurns...)
	return result
}
