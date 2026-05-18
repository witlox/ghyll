package dialect

import (
	"encoding/json"
	"fmt"

	"github.com/witlox/ghyll/memory"
	"github.com/witlox/ghyll/types"
)

// GLMSystemPrompt returns the system prompt for the GLM family (GLM-5, GLM-5.1, etc.).
// Validation-pass-8 D6: workdir sanitized via cleanWorkdir.
func GLMSystemPrompt(workdir string) string {
	return fmt.Sprintf(`You are an expert coding assistant working in %s. You handle complex reasoning tasks, multi-step debugging, and architectural decisions. You have access to tools for reading files, writing files, executing bash commands, and searching code. Think step by step for complex problems.`, cleanWorkdir(workdir))
}

// GLMBuildMessages formats messages for GLM family OpenAI-compatible API.
// Validation-pass-8 D8: shared buildOpenAIMessages.
func GLMBuildMessages(msgs []types.Message, systemPrompt string) []map[string]any {
	return buildOpenAIMessages(msgs, systemPrompt)
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

// GLMTokenCount estimates token count for GLM messages.
// Validation-pass-8 D2: rune-based; GLM tokenizer ~3 chars/token.
func GLMTokenCount(msgs []types.Message) int {
	return runeAwareTokenCount(msgs, 3)
}

// GLMHandoffSummary formats a checkpoint for GLM to continue from.
// Validation-pass-8 D7: zero-checkpoint guard.
func GLMHandoffSummary(cp memory.Checkpoint, recentTurns []types.Message) []types.Message {
	if isZeroCheckpoint(cp.Turn, cp.Summary) {
		return recentTurns
	}
	summary := fmt.Sprintf("Continuing from checkpoint (turn %d, previously on %s):\n\n%s\n\nThis task requires deep reasoning. Review the context carefully before proceeding.",
		cp.Turn, cp.ActiveModel, cp.Summary)
	result := []types.Message{{Role: "system", Content: summary}}
	result = append(result, recentTurns...)
	return result
}
