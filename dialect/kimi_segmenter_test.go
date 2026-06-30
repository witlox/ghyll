package dialect

import (
	"strings"
	"testing"

	"github.com/witlox/ghyll/types"
)

// TestScenario_KimiSegmenter_PlainContent verifies the passthrough
// case: content with no Kimi sentinels surfaces unchanged.
func TestScenario_KimiSegmenter_PlainContent(t *testing.T) {
	s := newKimiSegmenter()
	segs := s.Feed("Hello world, no sentinels here.")
	segs = append(segs, s.Flush()...)
	if len(segs) != 1 {
		t.Fatalf("segments = %d, want 1; got %+v", len(segs), segs)
	}
	if segs[0].Kind != SegmentContent {
		t.Fatalf("kind = %v, want SegmentContent", segs[0].Kind)
	}
	if segs[0].Text != "Hello world, no sentinels here." {
		t.Fatalf("text = %q", segs[0].Text)
	}
}

// TestScenario_KimiSegmenter_SingleToolCall verifies a single
// well-formed tool call extracts cleanly, with no leading/trailing
// content.
func TestScenario_KimiSegmenter_SingleToolCall(t *testing.T) {
	raw := "<|tool_calls_section_begin|><|tool_call_begin|> functions.memory_search:0 <|tool_call_argument_begin|> {\"query\": \"arrow\", \"limit\": 5} <|tool_call_end|><|tool_calls_section_end|>"
	s := newKimiSegmenter()
	segs := s.Feed(raw)
	segs = append(segs, s.Flush()...)
	tc := findToolCall(segs)
	if tc == nil {
		t.Fatalf("expected a tool call segment; got %+v", segs)
	}
	if tc.ID != "functions.memory_search:0" {
		t.Fatalf("id = %q", tc.ID)
	}
	if tc.Function.Name != "memory_search" {
		t.Fatalf("name = %q", tc.Function.Name)
	}
	if tc.Function.Arguments != `{"query": "arrow", "limit": 5}` {
		t.Fatalf("args = %q", tc.Function.Arguments)
	}
	// No content segments — the section was the entire stream.
	for _, seg := range segs {
		if seg.Kind == SegmentContent && seg.Text != "" {
			t.Fatalf("unexpected content segment: %q", seg.Text)
		}
	}
}

// TestScenario_KimiSegmenter_MultipleToolCalls verifies the
// observed batched-call grammar (three calls in one section).
func TestScenario_KimiSegmenter_MultipleToolCalls(t *testing.T) {
	raw := "<|tool_calls_section_begin|>" +
		"<|tool_call_begin|> functions.memory_search:0 <|tool_call_argument_begin|> {\"query\":\"a\"} <|tool_call_end|>" +
		"<|tool_call_begin|> functions.memory_search:1 <|tool_call_argument_begin|> {\"query\":\"b\"} <|tool_call_end|>" +
		"<|tool_call_begin|> functions.memory_search:2 <|tool_call_argument_begin|> {\"query\":\"c\"} <|tool_call_end|>" +
		"<|tool_calls_section_end|>"
	s := newKimiSegmenter()
	segs := s.Feed(raw)
	segs = append(segs, s.Flush()...)
	calls := allToolCalls(segs)
	if len(calls) != 3 {
		t.Fatalf("tool calls = %d, want 3", len(calls))
	}
	for i, tc := range calls {
		wantID := []string{
			"functions.memory_search:0",
			"functions.memory_search:1",
			"functions.memory_search:2",
		}[i]
		if tc.ID != wantID {
			t.Errorf("call[%d].ID = %q, want %q", i, tc.ID, wantID)
		}
	}
}

// TestScenario_KimiSegmenter_LeadingAndTrailingContent verifies
// that prose surrounding the section is preserved as content.
func TestScenario_KimiSegmenter_LeadingAndTrailingContent(t *testing.T) {
	raw := "Looking up memory.\n<|tool_calls_section_begin|><|tool_call_begin|> functions.x:0 <|tool_call_argument_begin|> {} <|tool_call_end|><|tool_calls_section_end|>\nDone."
	s := newKimiSegmenter()
	segs := s.Feed(raw)
	segs = append(segs, s.Flush()...)
	content := concatContent(segs)
	if content != "Looking up memory.\n\nDone." {
		t.Fatalf("content = %q", content)
	}
	if len(allToolCalls(segs)) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(allToolCalls(segs)))
	}
}

// TestScenario_KimiSegmenter_SplitAcrossChunks verifies the
// chunk-boundary buffering: a sentinel split byte-by-byte across
// Feed calls must still extract correctly.
func TestScenario_KimiSegmenter_SplitAcrossChunks(t *testing.T) {
	raw := "Prologue.<|tool_calls_section_begin|><|tool_call_begin|> functions.x:0 <|tool_call_argument_begin|> {\"k\":\"v\"} <|tool_call_end|><|tool_calls_section_end|>Epilogue."
	s := newKimiSegmenter()
	var segs []Segment
	for i := 0; i < len(raw); i++ {
		segs = append(segs, s.Feed(raw[i:i+1])...)
	}
	segs = append(segs, s.Flush()...)
	calls := allToolCalls(segs)
	if len(calls) != 1 {
		t.Fatalf("tool calls = %d, want 1; segs=%+v", len(calls), segs)
	}
	if calls[0].Function.Arguments != `{"k":"v"}` {
		t.Fatalf("args = %q", calls[0].Function.Arguments)
	}
	if got := concatContent(segs); got != "Prologue.Epilogue." {
		t.Fatalf("content = %q", got)
	}
}

// TestScenario_KimiSegmenter_NeverClosed verifies a half-formed
// section (no closing sentinel) at end-of-stream is dropped
// silently — we can't dispatch half a tool call.
func TestScenario_KimiSegmenter_NeverClosed(t *testing.T) {
	raw := "ok<|tool_calls_section_begin|><|tool_call_begin|> functions.x:0 <|tool_call_argument_begin|> {"
	s := newKimiSegmenter()
	segs := s.Feed(raw)
	segs = append(segs, s.Flush()...)
	if calls := allToolCalls(segs); len(calls) != 0 {
		t.Fatalf("expected no tool calls from unclosed section; got %d", len(calls))
	}
	if got := concatContent(segs); got != "ok" {
		t.Fatalf("content = %q", got)
	}
}

// TestScenario_KimiSegmenter_FalsePositivePrefix verifies that
// content containing `<` that does NOT begin a sentinel is emitted
// promptly (no perpetual hold-back).
func TestScenario_KimiSegmenter_FalsePositivePrefix(t *testing.T) {
	s := newKimiSegmenter()
	// `<x>` is not a Kimi sentinel prefix; should pass through.
	out := s.Feed("here is <x> tag")
	out = append(out, s.Flush()...)
	if got := concatContent(out); got != "here is <x> tag" {
		t.Fatalf("content = %q", got)
	}
}

func findToolCall(segs []Segment) *types.ToolCall {
	for _, seg := range segs {
		if seg.Kind == SegmentToolCall && seg.ToolCall != nil {
			return seg.ToolCall
		}
	}
	return nil
}

func allToolCalls(segs []Segment) []types.ToolCall {
	var out []types.ToolCall
	for _, seg := range segs {
		if seg.Kind == SegmentToolCall && seg.ToolCall != nil {
			out = append(out, *seg.ToolCall)
		}
	}
	return out
}

func concatContent(segs []Segment) string {
	var b strings.Builder
	for _, seg := range segs {
		if seg.Kind == SegmentContent {
			b.WriteString(seg.Text)
		}
	}
	return b.String()
}
