package dialect

import (
	"strings"

	"github.com/witlox/ghyll/types"
)

// kimiSegmenter parses the Kimi K2.x native tool-call grammar from
// the content channel when vLLM/SGLang is not started with
// `--tool-call-parser kimi_k2`. See ADR-018 for context.
//
// Grammar (as observed on CSCS Envoy AI Gateway, June 2026):
//
//	<|tool_calls_section_begin|>
//	  ( <|tool_call_begin|>  functions.<name>:<idx>
//	    <|tool_call_argument_begin|>  {json args}
//	    <|tool_call_end|> )+
//	<|tool_calls_section_end|>
//
// All sentinels begin with `<|`; the longest is 28 bytes
// (`<|tool_calls_section_begin|>` / `<|tool_call_argument_begin|>`).
//
// The segmenter buffers across Feed calls because any sentinel may
// arrive split across SSE deltas. Outside a section, content is
// emitted greedily up to (but not including) a suffix that could
// still complete into a sentinel.
type kimiSegmenter struct {
	buf       strings.Builder
	inSection bool

	// Per-call accumulators (valid only while inSection == true).
	curID   strings.Builder
	curArgs strings.Builder
	curPart kimiCallPart
}

type kimiCallPart int

const (
	kimiAwaitCallBegin kimiCallPart = iota // expecting <|tool_call_begin|>
	kimiCollectingID                       // collecting id text
	kimiCollectingArgs                     // collecting args json
)

const (
	kimiSectionBegin = "<|tool_calls_section_begin|>"
	kimiSectionEnd   = "<|tool_calls_section_end|>"
	kimiCallBegin    = "<|tool_call_begin|>"
	kimiArgBegin     = "<|tool_call_argument_begin|>"
	kimiCallEnd      = "<|tool_call_end|>"

	// Longest sentinel is 28 bytes. When outside a section and we
	// see a trailing `<` in the buffer, hold back the suffix
	// starting from that `<` if it could still grow into any
	// sentinel.
	kimiMaxSentinel = 28
)

var kimiSentinels = []string{
	kimiSectionBegin, kimiSectionEnd,
	kimiCallBegin, kimiArgBegin, kimiCallEnd,
}

func newKimiSegmenter() Segmenter {
	return &kimiSegmenter{curPart: kimiAwaitCallBegin}
}

func (k *kimiSegmenter) Feed(chunk string) []Segment {
	if chunk == "" {
		return nil
	}
	k.buf.WriteString(chunk)
	return k.drain(false)
}

func (k *kimiSegmenter) Flush() []Segment {
	return k.drain(true)
}

// drain consumes as much of the internal buffer as is unambiguously
// classifiable. When `final` is true, any unclassifiable tail is
// emitted as content (we know no more deltas are coming) or simply
// dropped if we are mid-section (a half-formed call cannot be
// dispatched).
func (k *kimiSegmenter) drain(final bool) []Segment {
	var out []Segment
	for {
		buf := k.buf.String()
		if buf == "" {
			return out
		}

		if !k.inSection {
			idx := strings.Index(buf, kimiSectionBegin)
			if idx >= 0 {
				if idx > 0 {
					out = append(out, Segment{Kind: SegmentContent, Text: buf[:idx]})
				}
				k.replace(buf[idx+len(kimiSectionBegin):])
				k.inSection = true
				k.curPart = kimiAwaitCallBegin
				continue
			}
			// No section begin yet. Emit what's safe; hold back any
			// suffix that could grow into a sentinel.
			safe, hold := splitOnPossibleSentinelPrefix(buf, final)
			if safe != "" {
				out = append(out, Segment{Kind: SegmentContent, Text: safe})
			}
			k.replace(hold)
			return out
		}

		// In section. Step the inner state machine.
		consumed, emitted, done := k.stepSection(buf, final)
		if emitted != nil {
			out = append(out, *emitted)
		}
		if consumed == 0 && !done {
			// Need more input. Buffer remains as-is.
			return out
		}
		if done {
			return out
		}
	}
}

// stepSection advances the inner state machine by one sentinel
// boundary or until input is exhausted. Returns the number of bytes
// consumed from k.buf, an optional emitted Segment, and a `done`
// flag meaning "no further work possible without more input."
func (k *kimiSegmenter) stepSection(buf string, final bool) (int, *Segment, bool) {
	switch k.curPart {
	case kimiAwaitCallBegin:
		// Either the section ends or a new call begins.
		endIdx := strings.Index(buf, kimiSectionEnd)
		beginIdx := strings.Index(buf, kimiCallBegin)
		if endIdx >= 0 && (beginIdx < 0 || endIdx < beginIdx) {
			k.replace(buf[endIdx+len(kimiSectionEnd):])
			k.inSection = false
			return 1, nil, false
		}
		if beginIdx >= 0 {
			k.replace(buf[beginIdx+len(kimiCallBegin):])
			k.curID.Reset()
			k.curArgs.Reset()
			k.curPart = kimiCollectingID
			return 1, nil, false
		}
		if final {
			// Section never closed and no more deltas. Drop the
			// partial section silently — half a tool-call cannot
			// be dispatched safely. The operator-facing diagnostic
			// path is end-of-stream parse failure surfaced by the
			// session loop.
			k.buf.Reset()
			k.inSection = false
			return 0, nil, true
		}
		return 0, nil, true

	case kimiCollectingID:
		// ID is everything up to <|tool_call_argument_begin|>.
		if idx := strings.Index(buf, kimiArgBegin); idx >= 0 {
			k.curID.WriteString(buf[:idx])
			k.replace(buf[idx+len(kimiArgBegin):])
			k.curPart = kimiCollectingArgs
			return 1, nil, false
		}
		// Buffer prefix that can't be part of `<|tool_` is safe to
		// accumulate into the ID buffer.
		safe, hold := splitOnPossibleSentinelPrefix(buf, final)
		if safe != "" {
			k.curID.WriteString(safe)
		}
		k.replace(hold)
		if final {
			k.buf.Reset()
			k.inSection = false
			return 0, nil, true
		}
		return 0, nil, true

	case kimiCollectingArgs:
		// Args are everything up to <|tool_call_end|>.
		if idx := strings.Index(buf, kimiCallEnd); idx >= 0 {
			k.curArgs.WriteString(buf[:idx])
			k.replace(buf[idx+len(kimiCallEnd):])
			seg := k.finishCall()
			k.curPart = kimiAwaitCallBegin
			return 1, seg, false
		}
		safe, hold := splitOnPossibleSentinelPrefix(buf, final)
		if safe != "" {
			k.curArgs.WriteString(safe)
		}
		k.replace(hold)
		if final {
			k.buf.Reset()
			k.inSection = false
			return 0, nil, true
		}
		return 0, nil, true
	}
	return 0, nil, true
}

func (k *kimiSegmenter) finishCall() *Segment {
	id := strings.TrimSpace(k.curID.String())
	args := strings.TrimSpace(k.curArgs.String())
	k.curID.Reset()
	k.curArgs.Reset()
	if id == "" {
		return nil
	}
	// Extract the function name from `functions.<name>:<idx>`. If
	// the id does not match the expected shape we still emit the
	// call with the raw id — the merge layer or the runner will
	// surface the diagnostic (consistent with KimiParseToolCalls'
	// loud-failure stance).
	name := id
	if rest, ok := strings.CutPrefix(id, "functions."); ok {
		if i := strings.IndexByte(rest, ':'); i >= 0 {
			name = rest[:i]
		} else {
			name = rest
		}
	}
	return &Segment{
		Kind: SegmentToolCall,
		ToolCall: &types.ToolCall{
			ID:   id,
			Type: "function",
			Function: types.ToolFunction{
				Name:      name,
				Arguments: args,
			},
		},
	}
}

func (k *kimiSegmenter) replace(s string) {
	k.buf.Reset()
	k.buf.WriteString(s)
}

// splitOnPossibleSentinelPrefix returns (safe, hold): `safe` is the
// prefix of `buf` that cannot still grow into a Kimi sentinel, and
// `hold` is the suffix to retain in the buffer until more input
// arrives. When `final` is true, the whole buffer is safe.
func splitOnPossibleSentinelPrefix(buf string, final bool) (string, string) {
	if final {
		return buf, ""
	}
	if len(buf) == 0 {
		return "", ""
	}
	// Walk back from the end up to kimiMaxSentinel-1 bytes looking
	// for the first `<` that could begin a sentinel.
	start := len(buf) - (kimiMaxSentinel - 1)
	if start < 0 {
		start = 0
	}
	for i := len(buf) - 1; i >= start; i-- {
		if buf[i] != '<' {
			continue
		}
		suffix := buf[i:]
		if isKimiSentinelPrefix(suffix) {
			return buf[:i], suffix
		}
	}
	return buf, ""
}

// isKimiSentinelPrefix reports whether `s` is a prefix of any known
// Kimi sentinel. A complete sentinel match also counts as a prefix
// (callers handle full matches via strings.Index before calling).
func isKimiSentinelPrefix(s string) bool {
	for _, sentinel := range kimiSentinels {
		if strings.HasPrefix(sentinel, s) {
			return true
		}
	}
	return false
}
