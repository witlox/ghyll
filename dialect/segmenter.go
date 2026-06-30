package dialect

import (
	"github.com/witlox/ghyll/config"
	"github.com/witlox/ghyll/types"
)

// SegmentKind categorizes a content-channel event emitted by a
// Segmenter. See ADR-018 for the rationale.
type SegmentKind int

const (
	// SegmentContent is user-visible assistant text (sentinels
	// already stripped by the segmenter).
	SegmentContent SegmentKind = iota
	// SegmentReasoning is chain-of-thought text. The stream client
	// routes this into Response.ReasoningContent unless the
	// envelope already supplied reasoning_content for this turn.
	SegmentReasoning
	// SegmentToolCall is a complete structured tool dispatch
	// extracted from the content channel. The stream client routes
	// this into Response.ToolCalls unless the envelope already
	// supplied tool_calls for this turn.
	SegmentToolCall
)

// Segment is a single typed event from a Segmenter. Exactly one of
// Text / ToolCall is populated per Kind: Text for Content and
// Reasoning, ToolCall for SegmentToolCall.
type Segment struct {
	Kind     SegmentKind
	Text     string
	ToolCall *types.ToolCall
}

// Segmenter parses one assistant turn's streaming content channel
// into typed events. Feed is called per SSE delta with whatever
// raw `delta.content` bytes arrived; Flush is called at
// end-of-stream to surface any buffered tail. Implementations MUST
// be safe to call Feed many times on small fragments — a sentinel
// may split across deltas.
type Segmenter interface {
	Feed(chunk string) []Segment
	Flush() []Segment
}

// NewSegmenter returns the Segmenter for the named dialect family.
// The input is normalized via config.CanonicalDialectFamily so the
// caller can pass either a model-config raw string (e.g. "Kimi") or
// a canonical family identifier ("kimi"). Unknown or envelope-clean
// families return a passthrough segmenter that emits every chunk
// as SegmentContent — the safe default for dialects whose gateway
// already populates the OpenAI envelope correctly (deepseek,
// minimax, qwen today).
func NewSegmenter(family string) Segmenter {
	canonical, _ := config.CanonicalDialectFamily(family)
	switch canonical {
	case "kimi", "kimi-code":
		return newKimiSegmenter()
	case "glm":
		return newGLMSegmenter()
	default:
		return &passthroughSegmenter{}
	}
}

// passthroughSegmenter forwards every chunk as user-visible content
// with no parsing. Used for dialects whose envelope path is
// already correct.
type passthroughSegmenter struct{}

func (p *passthroughSegmenter) Feed(chunk string) []Segment {
	if chunk == "" {
		return nil
	}
	return []Segment{{Kind: SegmentContent, Text: chunk}}
}

func (p *passthroughSegmenter) Flush() []Segment { return nil }
