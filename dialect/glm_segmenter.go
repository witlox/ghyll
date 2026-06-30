package dialect

import "strings"

// glmSegmenter parses GLM-5's `<think>…</think>` reasoning grammar
// from the content channel when vLLM/SGLang is not started with
// `--reasoning-parser glm`. See ADR-018.
//
// Observed shapes (CSCS Envoy AI Gateway, June 2026):
//
//  1. Canonical:   <think>chain of thought</think>visible answer
//  2. No-opener:   visible-looking text</think>actual visible answer
//     (the chat template injected an implicit opening <think>;
//     the closer leaked into the assistant turn's content stream).
//
// The segmenter therefore tracks the first delimiter rather than
// assuming an initial mode: a leading </think> retroactively
// classifies the preceding bytes as reasoning, while a leading
// <think> classifies the preceding bytes as content.
type glmSegmenter struct {
	buf       strings.Builder
	inThink   bool
	sawOpener bool // true once we've decided the initial mode
}

const (
	glmThinkOpen  = "<think>"
	glmThinkClose = "</think>"

	// Longest sentinel is 8 bytes (`</think>`). Hold back any
	// trailing `<` whose suffix could still grow into either.
	glmMaxSentinel = 8
)

var glmSentinels = []string{glmThinkOpen, glmThinkClose}

func newGLMSegmenter() Segmenter {
	return &glmSegmenter{}
}

func (g *glmSegmenter) Feed(chunk string) []Segment {
	if chunk == "" {
		return nil
	}
	g.buf.WriteString(chunk)
	return g.drain(false)
}

func (g *glmSegmenter) Flush() []Segment {
	return g.drain(true)
}

func (g *glmSegmenter) drain(final bool) []Segment {
	var out []Segment
	for {
		buf := g.buf.String()
		if buf == "" {
			return out
		}

		// Until we've seen any delimiter, scan for whichever comes
		// first to decide the initial classification of the
		// already-buffered prefix.
		if !g.sawOpener {
			openIdx := strings.Index(buf, glmThinkOpen)
			closeIdx := strings.Index(buf, glmThinkClose)
			// Strict ordering: a `</think>` earlier than any
			// `<think>` means the model started inside an implicit
			// think block.
			switch {
			case closeIdx >= 0 && (openIdx < 0 || closeIdx < openIdx):
				if closeIdx > 0 {
					out = append(out, Segment{Kind: SegmentReasoning, Text: buf[:closeIdx]})
				}
				g.replace(buf[closeIdx+len(glmThinkClose):])
				g.inThink = false
				g.sawOpener = true
				continue
			case openIdx >= 0:
				if openIdx > 0 {
					out = append(out, Segment{Kind: SegmentContent, Text: buf[:openIdx]})
				}
				g.replace(buf[openIdx+len(glmThinkOpen):])
				g.inThink = true
				g.sawOpener = true
				continue
			default:
				// No delimiter yet. Whatever is buffered is
				// content (the conservative choice — if a later
				// </think> appears we revisit; if a <think>
				// appears we don't need to since content stays
				// content). Hold back any possible-sentinel
				// suffix.
				safe, hold := splitOnPossibleSentinelPrefixGLM(buf, final)
				if safe != "" {
					out = append(out, Segment{Kind: SegmentContent, Text: safe})
				}
				g.replace(hold)
				if final && hold == "" {
					return out
				}
				return out
			}
		}

		// Initial classification is fixed. Toggle on each
		// delimiter.
		var (
			target   string
			kind     SegmentKind
			nextMode bool
		)
		if g.inThink {
			target, kind, nextMode = glmThinkClose, SegmentReasoning, false
		} else {
			target, kind, nextMode = glmThinkOpen, SegmentContent, true
		}
		if idx := strings.Index(buf, target); idx >= 0 {
			if idx > 0 {
				out = append(out, Segment{Kind: kind, Text: buf[:idx]})
			}
			g.replace(buf[idx+len(target):])
			g.inThink = nextMode
			continue
		}
		safe, hold := splitOnPossibleSentinelPrefixGLM(buf, final)
		if safe != "" {
			out = append(out, Segment{Kind: kind, Text: safe})
		}
		g.replace(hold)
		return out
	}
}

func (g *glmSegmenter) replace(s string) {
	g.buf.Reset()
	g.buf.WriteString(s)
}

func splitOnPossibleSentinelPrefixGLM(buf string, final bool) (string, string) {
	if final {
		return buf, ""
	}
	if len(buf) == 0 {
		return "", ""
	}
	start := len(buf) - (glmMaxSentinel - 1)
	if start < 0 {
		start = 0
	}
	for i := len(buf) - 1; i >= start; i-- {
		if buf[i] != '<' {
			continue
		}
		suffix := buf[i:]
		if isGLMSentinelPrefix(suffix) {
			return buf[:i], suffix
		}
	}
	return buf, ""
}

func isGLMSentinelPrefix(s string) bool {
	for _, sentinel := range glmSentinels {
		if strings.HasPrefix(sentinel, s) {
			return true
		}
	}
	return false
}
