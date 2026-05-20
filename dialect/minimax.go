package dialect

import (
	"encoding/json"
	"fmt"

	"github.com/witlox/ghyll/memory"
	"github.com/witlox/ghyll/types"
)

// MinimaxSystemPrompt returns the system prompt for the MiniMax family (M2.5, M2.7, etc.).
// Validation-pass-8 D6: workdir sanitized via cleanWorkdir.
func MinimaxSystemPrompt(workdir string) string {
	return fmt.Sprintf(`You are a coding assistant working in %s. You have access to tools for reading files, writing files, executing bash commands, and searching code. Use tools to accomplish tasks. Be concise and direct.`, cleanWorkdir(workdir))
}

// MinimaxBuildMessages formats messages for MiniMax family OpenAI-compatible API.
// Validation-pass-8 D8: shared buildOpenAIMessages.
func MinimaxBuildMessages(msgs []types.Message, systemPrompt string) []map[string]any {
	return buildOpenAIMessages(msgs, systemPrompt)
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

// MinimaxTokenCount estimates token count for MiniMax messages.
// Validation-pass-8 D2: rune-based; MiniMax tokenizer ~4 chars/token.
func MinimaxTokenCount(msgs []types.Message) int {
	return runeAwareTokenCount(msgs, 4)
}

// MinimaxHandoffSummary formats a checkpoint for MiniMax to continue from.
// Validation-pass-8 D7: zero-checkpoint guard.
func MinimaxHandoffSummary(cp memory.Checkpoint, recentTurns []types.Message) []types.Message {
	if isZeroCheckpoint(cp.Turn, cp.Summary) {
		return recentTurns
	}
	// Tier 3 / SR C-2: sanitize operator-controlled fields before
	// they land in the system prompt.
	summary := fmt.Sprintf("Continuing from checkpoint (turn %d, previously on %s):\n\n%s",
		cp.Turn, sanitizeHandoffField(cp.ActiveModel), sanitizeHandoffField(cp.Summary))
	result := []types.Message{{Role: "system", Content: summary}}
	result = append(result, recentTurns...)
	return result
}
