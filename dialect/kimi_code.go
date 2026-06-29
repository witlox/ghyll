package dialect

import (
	"encoding/json"
	"fmt"

	"github.com/witlox/ghyll/memory"
	"github.com/witlox/ghyll/types"
)

// KimiCode dialect — covers the Moonshot Cloud API models accessed via
// api.moonshot.ai/v1 (e.g. kimi-for-coding, moonshot-v1-8k/32k/128k).
//
// Wire-contract difference from the kimi family: the Moonshot Cloud API
// returns standard OpenAI-compatible tool-call IDs (UUIDs / opaque
// strings) rather than the `functions.<name>:<index>` shape that
// self-hosted K2 (vLLM/SGLang) emits. KimiCodeParseToolCalls therefore
// delegates directly to parseOpenAIToolCalls without any ID-shape
// enforcement.
//
// Note: there is also an Anthropic Messages API-compatible endpoint
// (api.kimi.com/coding/v1/messages) used by some third-party
// integrations. That protocol is not OpenAI-compatible and is NOT
// covered by this dialect; it would require a separate Anthropic-wire
// implementation.

// KimiCodeSystemPrompt returns the system prompt for the Kimi Code
// family (kimi-for-coding, moonshot-v1-*). Uses the same workdir
// sanitization contract as the kimi family (validation-pass-8 D6).
func KimiCodeSystemPrompt(workdir string) string {
	return fmt.Sprintf(`You are a coding assistant working in %s. You have access to tools for reading files, writing files, executing bash commands, and searching code. Use tools to accomplish tasks. Be concise and direct.

You also have memory_search — a tool that queries ghyll's persistent project memory of prior sessions (decisions made, bugs fixed, ongoing work). Call it when the operator's question references past activity, asks "what did we decide about X?", or when context from a previous session would change your answer. Args: {"query": "free text or hex hash prefix", "limit": 5}. Returns: list of {hash, time, author, summary} from earlier checkpoints. Don't call it on every turn — only when prior context is genuinely relevant.`, cleanWorkdir(workdir))
}

// KimiCodeBuildMessages formats messages for the Moonshot Cloud API
// OpenAI-compatible endpoint. Delegates to buildOpenAIMessages
// (validation-pass-8 D8) for the shared assistant-only
// ReasoningContent round-trip and content:null rule.
func KimiCodeBuildMessages(msgs []types.Message, systemPrompt string) []map[string]any {
	return buildOpenAIMessages(msgs, systemPrompt)
}

// KimiCodeParseToolCalls parses tool calls from the Moonshot Cloud API
// response. Unlike KimiParseToolCalls, this does NOT enforce the
// `functions.<name>:<index>` ID shape — the Cloud API returns standard
// OpenAI-compatible IDs (UUIDs or opaque strings) that must be treated
// as opaque by the client.
func KimiCodeParseToolCalls(raw json.RawMessage) ([]types.ToolCall, error) {
	return parseOpenAIToolCalls(raw)
}

// KimiCodePlanModePrompt returns additional system instructions for plan
// mode on Kimi Code. Invariant 36: advisory only — all tools remain
// available.
func KimiCodePlanModePrompt() string {
	return `You are in PLAN MODE. Think before acting:
1. Analyze the problem and constraints
2. Consider approaches and trade-offs
3. Outline your plan before executing
4. Explain your reasoning for non-obvious choices
All tools remain available. Plan first, then act.`
}

// KimiCodeCompactionPrompt returns the compaction instruction for
// Kimi Code.
func KimiCodeCompactionPrompt() string {
	return `Summarize the following conversation turns into a concise summary. Preserve:
- The original task/goal
- Key decisions made
- Files modified and why
- Current state of the work
- Any unresolved issues

Format as a structured summary that another model instance can use to continue the work.`
}

// KimiCodeTokenCount estimates token count for Kimi Code messages.
// Uses the same 3 chars/token BPE estimate as the kimi family.
func KimiCodeTokenCount(msgs []types.Message) int {
	return runeAwareTokenCount(msgs, 3)
}

// KimiCodeHandoffSummary formats a checkpoint for Kimi Code to
// continue from. Validation-pass-8 D7: zero-checkpoint guard.
func KimiCodeHandoffSummary(cp memory.Checkpoint, recentTurns []types.Message) []types.Message {
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
