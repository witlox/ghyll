package dialect

import (
	"strings"
	"testing"
)

func TestScenario_GLMSegmenter_PlainContent(t *testing.T) {
	s := newGLMSegmenter()
	segs := s.Feed("Just a normal answer.")
	segs = append(segs, s.Flush()...)
	if got := concatContent(segs); got != "Just a normal answer." {
		t.Fatalf("content = %q", got)
	}
	if got := concatReasoning(segs); got != "" {
		t.Fatalf("reasoning unexpectedly = %q", got)
	}
}

// TestScenario_GLMSegmenter_CanonicalThinkBlock — the spec shape.
func TestScenario_GLMSegmenter_CanonicalThinkBlock(t *testing.T) {
	s := newGLMSegmenter()
	segs := s.Feed("<think>let me consider</think>final answer")
	segs = append(segs, s.Flush()...)
	if got := concatContent(segs); got != "final answer" {
		t.Fatalf("content = %q", got)
	}
	if got := concatReasoning(segs); got != "let me consider" {
		t.Fatalf("reasoning = %q", got)
	}
}

// TestScenario_GLMSegmenter_NoOpenerImplicitThink — the observed
// CSCS Envoy AI Gateway leak shape: chat template injects an
// opening <think> implicitly, so the stream begins inside a think
// block. The first </think> seen retroactively classifies the
// prefix as reasoning.
func TestScenario_GLMSegmenter_NoOpenerImplicitThink(t *testing.T) {
	s := newGLMSegmenter()
	segs := s.Feed("I'll recall prior context.</think>查询 arrow grid 的工作。")
	segs = append(segs, s.Flush()...)
	if got := concatReasoning(segs); got != "I'll recall prior context." {
		t.Fatalf("reasoning = %q", got)
	}
	if got := concatContent(segs); got != "查询 arrow grid 的工作。" {
		t.Fatalf("content = %q", got)
	}
}

// TestScenario_GLMSegmenter_SplitAcrossChunks — byte-by-byte Feed
// must still correctly classify.
func TestScenario_GLMSegmenter_SplitAcrossChunks(t *testing.T) {
	raw := "prelude<think>thinking out loud</think>postlude"
	s := newGLMSegmenter()
	var segs []Segment
	for i := 0; i < len(raw); i++ {
		segs = append(segs, s.Feed(raw[i:i+1])...)
	}
	segs = append(segs, s.Flush()...)
	if got := concatContent(segs); got != "preludepostlude" {
		t.Fatalf("content = %q", got)
	}
	if got := concatReasoning(segs); got != "thinking out loud" {
		t.Fatalf("reasoning = %q", got)
	}
}

// TestScenario_GLMSegmenter_NeverClosedThink — an unclosed think
// block at end-of-stream surfaces what we have as reasoning.
func TestScenario_GLMSegmenter_NeverClosedThink(t *testing.T) {
	s := newGLMSegmenter()
	segs := s.Feed("before<think>thinking but never closed")
	segs = append(segs, s.Flush()...)
	if got := concatContent(segs); got != "before" {
		t.Fatalf("content = %q", got)
	}
	if got := concatReasoning(segs); got != "thinking but never closed" {
		t.Fatalf("reasoning = %q", got)
	}
}

// TestScenario_GLMSegmenter_AlternatingBlocks — multiple think
// blocks interspersed with content.
func TestScenario_GLMSegmenter_AlternatingBlocks(t *testing.T) {
	s := newGLMSegmenter()
	segs := s.Feed("A<think>1</think>B<think>2</think>C")
	segs = append(segs, s.Flush()...)
	if got := concatContent(segs); got != "ABC" {
		t.Fatalf("content = %q", got)
	}
	if got := concatReasoning(segs); got != "12" {
		t.Fatalf("reasoning = %q", got)
	}
}

func concatReasoning(segs []Segment) string {
	var b strings.Builder
	for _, seg := range segs {
		if seg.Kind == SegmentReasoning {
			b.WriteString(seg.Text)
		}
	}
	return b.String()
}
