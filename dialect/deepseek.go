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
// Validation-pass-8 D6: workdir is sanitized via cleanWorkdir to
// strip embedded newlines / ANSI escapes / control chars.
func DeepSeekSystemPrompt(workdir string) string {
	return fmt.Sprintf(`You are an expert coding assistant working in %s. You handle code reasoning, multi-file debugging, and architectural tasks. You have access to tools for reading files, writing files, executing bash commands, and searching code. Be precise and verify before committing to a direction.`, cleanWorkdir(workdir))
}

// DeepSeekBuildMessages formats messages for DeepSeek OpenAI-compatible API.
// Validation-pass-8 D8: delegates to buildOpenAIMessages shared helper.
func DeepSeekBuildMessages(msgs []types.Message, systemPrompt string) []map[string]any {
	return buildOpenAIMessages(msgs, systemPrompt)
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
// Validation-pass-8 D2: rune-based counting so CJK / emoji content
// isn't silently undercounted. ASCII tokenizes at ~3.5 chars/token
// (runesPerToken=4 conservative); non-ASCII at 1 token/rune.
func DeepSeekTokenCount(msgs []types.Message) int {
	return runeAwareTokenCount(msgs, 4)
}

// DeepSeekHandoffSummary formats a checkpoint for DeepSeek to continue from.
// Validation-pass-8 D7: if the checkpoint is the zero value, skip
// the "Continuing from checkpoint..." framing — recent turns alone
// are honest.
func DeepSeekHandoffSummary(cp memory.Checkpoint, recentTurns []types.Message) []types.Message {
	if isZeroCheckpoint(cp.Turn, cp.Summary) {
		return recentTurns
	}
	// Tier 3 / SR C-2: sanitize operator-controlled fields.
	summary := fmt.Sprintf("Continuing from checkpoint (turn %d, previously on %s):\n\n%s\n\nReview the context before proceeding; verify any inherited assumption against the actual code.",
		cp.Turn, sanitizeHandoffField(cp.ActiveModel), sanitizeHandoffField(cp.Summary))
	result := []types.Message{{Role: "system", Content: summary}}
	result = append(result, recentTurns...)
	return result
}
