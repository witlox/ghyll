package dialect

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/witlox/ghyll/memory"
	"github.com/witlox/ghyll/types"
)

// kimiToolCallIDRE validates the Kimi-specific tool-call id contract:
// `functions.<name>:<index>` where <name> is the function (tool)
// name and <index> is a zero-based integer. Non-conformant ids
// (e.g. a vanilla UUID) surface ErrParseToolCall via
// KimiParseToolCalls so the runner can pattern-match and emit an
// operator-facing diagnostic naming the offending shape rather than
// silently dispatching against the unparseable id.
var kimiToolCallIDRE = regexp.MustCompile(`^functions\.[a-zA-Z_][a-zA-Z0-9_-]*:\d+$`)

// KimiSystemPrompt returns the system prompt for the Kimi family
// (Kimi 2.5 / Kimi 2.6 / moonshotai/Kimi-K2.5 / moonshotai/Kimi-K2.6).
// Validation-pass-8 D6: workdir sanitized via cleanWorkdir.
func KimiSystemPrompt(workdir string) string {
	return fmt.Sprintf(`You are a coding assistant working in %s. You have access to tools for reading files, writing files, executing bash commands, and searching code. Use tools to accomplish tasks. Be concise and direct.`, cleanWorkdir(workdir))
}

// KimiBuildMessages formats messages for the Kimi family
// OpenAI-compatible API. Validation-pass-8 D8: shared
// buildOpenAIMessages — the assistant-only ReasoningContent
// round-trip lives in buildOpenAIMessages (helpers.go) so every
// dialect that benefits from it inherits the wire-key emission.
func KimiBuildMessages(msgs []types.Message, systemPrompt string) []map[string]any {
	return buildOpenAIMessages(msgs, systemPrompt)
}

// KimiParseToolCalls parses tool calls from the Kimi family response
// format. The Kimi backend ships the OpenAI tool_calls envelope BUT
// enforces a strict `functions.<name>:<index>` id shape. A
// non-conformant id (random UUID, empty string, etc.) is the
// documented sentinel of a misconfigured or wrong-version backend
// and surfaces ErrParseToolCall so the session loop can name the
// offending shape in an operator-facing diagnostic rather than
// silently dispatching against an unparseable id.
func KimiParseToolCalls(raw json.RawMessage) ([]types.ToolCall, error) {
	calls, err := parseOpenAIToolCalls(raw)
	if err != nil {
		return nil, err
	}
	for _, c := range calls {
		if !kimiToolCallIDRE.MatchString(c.ID) {
			return nil, fmt.Errorf("%w: kimi tool_call id %q does not match required shape functions.<name>:<index>",
				ErrParseToolCall, c.ID)
		}
	}
	return calls, nil
}

// KimiPlanModePrompt returns additional system instructions for plan
// mode on Kimi. Invariant 36: advisory only — all tools remain
// available.
func KimiPlanModePrompt() string {
	return `You are in PLAN MODE. Think before acting:
1. Analyze the problem and constraints
2. Consider approaches and trade-offs
3. Outline your plan before executing
4. Explain your reasoning for non-obvious choices
All tools remain available. Plan first, then act.`
}

// KimiCompactionPrompt returns the compaction instruction for Kimi.
func KimiCompactionPrompt() string {
	return `Summarize the following conversation turns into a concise summary. Preserve:
- The original task/goal
- Key decisions made
- Files modified and why
- Current state of the work
- Any unresolved issues

Format as a structured summary that another model instance can use to continue the work.`
}

// KimiTokenCount estimates token count for Kimi messages. Kimi's
// BPE is tighter than MiniMax's — 3 chars/token for ASCII (vs 4 for
// MiniMax). Under-counts vs over-counts are the synthesis rationale:
// the drift detector tolerates under-count, so a slightly tighter
// estimate is correctness-preserving.
func KimiTokenCount(msgs []types.Message) int {
	return runeAwareTokenCount(msgs, 3)
}

// KimiHandoffSummary formats a checkpoint for Kimi to continue from.
// Validation-pass-8 D7: zero-checkpoint guard.
func KimiHandoffSummary(cp memory.Checkpoint, recentTurns []types.Message) []types.Message {
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
