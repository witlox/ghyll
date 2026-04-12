package dialect

import (
	"encoding/json"
	"fmt"

	"github.com/witlox/ghyll/memory"
	"github.com/witlox/ghyll/types"
)

// GLM5SystemPrompt returns the system prompt for GLM-5.
func GLM5SystemPrompt(workdir string) string {
	return fmt.Sprintf(`You are an expert coding assistant working in %s. You handle complex reasoning tasks, multi-step debugging, and architectural decisions. You have access to tools for reading files, writing files, executing bash commands, and searching code. Think step by step for complex problems.`, workdir)
}

// GLM5BuildMessages formats messages for the GLM-5 OpenAI-compatible API.
func GLM5BuildMessages(msgs []types.Message, systemPrompt string) []map[string]any {
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

// GLM5ParseToolCalls parses tool calls from GLM-5 response format.
// GLM-5 uses the standard OpenAI tool_calls format via SGLang.
func GLM5ParseToolCalls(raw json.RawMessage) ([]types.ToolCall, error) {
	return parseOpenAIToolCalls(raw)
}

// GLM5CompactionPrompt returns the compaction instruction for GLM-5.
// Accounts for DSA attention — emphasizes preserving structural decisions.
func GLM5CompactionPrompt() string {
	return `Summarize the following conversation into a structured summary optimized for long-context continuation. Preserve:
- The original task/goal with full context
- All architectural and design decisions with rationale
- Files modified, created, or deleted with purpose
- Current state of implementation
- Unresolved issues and open questions
- Key constraints and invariants discovered

Structure the summary with clear sections. This will be used to continue complex reasoning tasks.`
}

// GLM5TokenCount estimates token count for GLM-5 messages.
// GLM-5 uses a different tokenizer — slightly more tokens per char for code.
func GLM5TokenCount(msgs []types.Message) int {
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

// GLM5HandoffSummary formats a checkpoint for GLM-5 to continue from.
func GLM5HandoffSummary(cp memory.Checkpoint, recentTurns []types.Message) []types.Message {
	summary := fmt.Sprintf("Continuing from checkpoint (turn %d, previously on %s):\n\n%s\n\nThis task requires deep reasoning. Review the context carefully before proceeding.",
		cp.Turn, cp.ActiveModel, cp.Summary)

	result := []types.Message{
		{Role: "system", Content: summary},
	}
	result = append(result, recentTurns...)
	return result
}
