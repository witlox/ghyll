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
// Validation-pass-8 D6: workdir is sanitized via cleanWorkdir.
func QwenSystemPrompt(workdir string) string {
	return fmt.Sprintf(`You are an expert coding assistant working in %s. You produce careful, idiomatic code with attention to the surrounding style. You have access to tools for reading files, writing files, executing bash commands, and searching code. Prefer to read the existing code before writing new code.`, cleanWorkdir(workdir))
}

// QwenBuildMessages formats messages for Qwen OpenAI-compatible API.
// Validation-pass-8 D8: delegates to buildOpenAIMessages shared helper.
func QwenBuildMessages(msgs []types.Message, systemPrompt string) []map[string]any {
	return buildOpenAIMessages(msgs, systemPrompt)
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
// Validation-pass-8 D2: rune-based; Qwen BPE ~3.2 chars/token →
// runesPerToken=3 conservative.
func QwenTokenCount(msgs []types.Message) int {
	return runeAwareTokenCount(msgs, 3)
}

// QwenHandoffSummary formats a checkpoint for Qwen to continue from.
// Validation-pass-8 D7: zero-checkpoint guard.
func QwenHandoffSummary(cp memory.Checkpoint, recentTurns []types.Message) []types.Message {
	if isZeroCheckpoint(cp.Turn, cp.Summary) {
		return recentTurns
	}
	// Tier 3 / SR C-2: sanitize operator-controlled fields.
	summary := fmt.Sprintf("Continuing from checkpoint (turn %d, previously on %s):\n\n%s\n\nVerify the inherited code-style conventions before producing new code.",
		cp.Turn, sanitizeHandoffField(cp.ActiveModel), sanitizeHandoffField(cp.Summary))
	result := []types.Message{{Role: "system", Content: summary}}
	result = append(result, recentTurns...)
	return result
}
