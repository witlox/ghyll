# ADR-018: Dialect content-channel grammar — per-dialect segmenters

**Status:** Accepted (2026-06-30)

## Context

ghyll trusts the OpenAI-compatible side-channel envelope fields
(`delta.tool_calls`, `delta.reasoning_content`, `delta.reasoning`)
as the spec-correct surfaces for structured output. The stream
client at `stream/client.go:416-552` reads only those fields;
`delta.content` is treated as opaque user-visible text.

In practice the open-weight model families ghyll targets — Kimi
K2.x, GLM-5, DeepSeek, MiniMax M2.x, Qwen — all emit their
structured output (reasoning traces, tool calls) as
**dialect-native sentinels inside the content stream**. The
OpenAI envelope is a server-side normalization layer that vLLM,
SGLang, and llama.cpp produce only when started with the right
parser flags (e.g. vLLM's `--enable-auto-tool-choice
--tool-call-parser kimi_k2 --reasoning-parser glm`). When those
flags are missing, the gateway forwards raw token output and the
sentinels reach ghyll's content channel verbatim.

Two real-world cases observed on the CSCS Envoy AI Gateway
(June 2026):

- **Kimi K2.7-Code** under `stream=true` emits

  ```
  <|tool_calls_section_begin|>
  <|tool_call_begin|> functions.memory_search:0
  <|tool_call_argument_begin|> {"query": "arrow", "limit": 5}
  <|tool_call_end|>
  <|tool_calls_section_end|>
  ```

  in the content channel; `delta.tool_calls` is empty. The same
  prompt under `stream=false` returns a clean
  `tool_calls` array, confirming the gateway *can* normalize but
  does not in streaming mode.

- **GLM-5** under `stream=true` emits `<think>...</think>` blocks
  in the content channel rather than populating
  `delta.reasoning_content`. The closing `</think>` reached the
  operator's terminal as plain text and the chain-of-thought
  was rendered as user-visible output.

The systemic gap: the dialect package owns the model family's
behavioral contract but currently only parses the OpenAI envelope.
The content-channel grammar — which is part of the same contract —
is unhandled. This is not a defensive workaround; it is a missing
half of the dialect abstraction.

## Decision

Each dialect owns a **content-channel segmenter** that parses the
model family's native text-channel grammar into typed events.
The stream client invokes the segmenter on every content delta
and merges its output with the envelope path before producing the
final `stream.Response`.

```go
// dialect/segmenter.go (new file)

// SegmentKind is the typed event category emitted by a Segmenter.
type SegmentKind int

const (
    SegmentContent   SegmentKind = iota // user-visible text
    SegmentReasoning                    // chain-of-thought
    SegmentToolCall                     // structured tool dispatch
)

type Segment struct {
    Kind     SegmentKind
    Text     string         // populated for Content/Reasoning
    ToolCall *types.ToolCall // populated for ToolCall
}

// Segmenter processes streaming content chunks for one assistant
// turn. Feed is called per-delta; Flush is called at end-of-stream
// to surface any trailing complete sentinel or to flush a buffered
// partial that turned out to be plain content.
type Segmenter interface {
    Feed(chunk string) []Segment
    Flush() []Segment
}

// NewSegmenter returns the dialect-appropriate segmenter for
// `family`. Unknown families return a passthrough segmenter that
// emits every chunk as SegmentContent — safe default for backends
// whose envelope path is already correct (deepseek, minimax, qwen).
func NewSegmenter(family string) Segmenter
```

Stream-side merge rules at end-of-stream (`stream/client.go`):

1. If the envelope yielded a non-empty `tool_calls` array, the
   segmenter's `SegmentToolCall` events for this turn are
   **discarded**. Rationale: a correctly-configured gateway is
   authoritative; never duplicate.
2. Otherwise, segmenter `SegmentToolCall` events populate
   `Response.ToolCalls`.
3. If the envelope yielded a non-empty `reasoning_content`, the
   segmenter's `SegmentReasoning` events are **discarded** for the
   same reason.
4. Otherwise, segmenter `SegmentReasoning` events are
   concatenated into `Response.ReasoningContent` (subject to the
   existing `maxStreamContentBytes` cap).
5. `SegmentContent` events are always appended to
   `Response.Content`. The segmenter is responsible for stripping
   the sentinels themselves; the content emitted is what the
   operator sees.

## Scope

**In scope for this ADR:**

- `kimi` family — parse `<|tool_call_begin|>…<|tool_call_end|>`
  enclosed in `<|tool_calls_section_begin|>…<|tool_calls_section_end|>`.
- `kimi_code` family — same grammar as `kimi` (shared segmenter).
- `glm5` family — parse `<think>…</think>` reasoning blocks.

**Out of scope:**

- `deepseek`, `minimax`, `qwen` — passthrough segmenter.
  Promotion to per-dialect grammar happens when their content-
  channel leak is observed in the wild.
- **Per-chunk progressive renderer UX** (the "▶ tool_name args…"
  in-flight indicator). The segmenter's Feed return value carries
  enough information for this to be added later, but the renderer
  hook is a follow-up ADR. End-of-stream synthesis is sufficient
  for correctness today: tool calls dispatch correctly, reasoning
  routes to ReasoningContent, sentinels do not reach the operator.

## Rationale

The dialect package's contract is "the model family's full
behavioral surface." Tool-call ID shape (ADR-v4-001), reasoning
content round-trip (ADR-v4-009), depth-aware routing (§7.1) are
already inside that contract. The model's native text-channel
grammar belongs in the same place because:

1. **No single gateway is authoritative.** vLLM, SGLang,
   llama.cpp, Envoy AI Gateway, and downstream proxies each
   normalize differently. A model deployed across CSCS, a
   self-hosted box, and a third-party API may behave consistently
   in non-streaming mode but diverge in streaming mode.
2. **The dialect is the only layer that knows the grammar.**
   The stream client cannot guess; it can only ask the dialect
   what to do with content bytes.
3. **The chunk-boundary problem is dialect-specific.** Where a
   sentinel splits across SSE deltas (e.g. `<|tool_` in one
   chunk, `call_begin|>` in the next), only the dialect knows
   the longest possible prefix to buffer.
4. **Correctness over breadth (ghyll's core stance).** Trusting
   the envelope alone produces correct output *most* of the time
   and silently-incorrect output the rest, which is the failure
   mode ghyll is designed to refuse.

The chosen merge rule (envelope wins when populated) preserves
backwards compatibility: a correctly-configured gateway sees no
behavioral change because the segmenter's events are discarded
for the fields the envelope already filled.

## Consequences

**Positive:**

- Kimi and GLM-5 work correctly on any vLLM deployment regardless
  of `--tool-call-parser` / `--reasoning-parser` flags.
- The dialect contract becomes complete: a new dialect file
  declares system prompt, message build, envelope parse, segmenter,
  token count, handoff. Future additions follow a clear template.
- The ReasoningContent exclusion from canonical hash
  (ADR-v4-009) is preserved unchanged; segmenter-produced
  reasoning routes through the same `Message.ReasoningContent`
  field.
- Defense in depth: if CSCS later enables the vLLM parser flags,
  ghyll behavior is identical (envelope wins).

**Negative:**

- New dialect surface to maintain. Mitigated by the passthrough
  default — adding a model family that uses the OpenAI envelope
  correctly requires zero segmenter code.
- Per-chunk regex/state-machine work in the streaming hot path.
  Mitigated by the chunk sizes (vLLM emits tokens in groups of
  a few bytes; the segmenter's state is a small fixed-size
  ring buffer for the longest possible sentinel prefix).
- The merge rule introduces a third "source of truth" branch
  point (envelope vs. segmenter vs. plain content). Documented
  here and enforced by tests; the precedence is unambiguous
  (envelope > segmenter > plain).

## Alternatives considered

- **Push CSCS / vLLM operators to enable parser flags.** Rejected
  as the *sole* fix: ghyll cannot depend on every gateway
  operator getting this right. Filed as a parallel action but not
  the architectural answer.
- **Defensive post-stream regex in the session loop.** Rejected:
  the session loop is dialect-agnostic; smuggling per-family
  regexes into it leaks the dialect contract upward and breaks
  the ADR-001 "no provider interfaces" stance (the dialect file
  is the concrete contract).
- **Stream-side single normalizer that handles all known
  sentinels.** Rejected: dialects' sentinels can collide
  (Kimi's `<|tool_call_begin|>` lexically overlaps no other
  family today, but a future dialect using `<|`-prefixed
  sentinels could). The per-dialect segmenter is the
  forward-compatible shape.

## Migration

Purely additive. No on-disk format changes. No checkpoint
rewrites. Existing dialects that don't implement a custom
segmenter get the passthrough default, preserving today's
behavior byte-for-byte.

A correctly-configured Kimi/GLM-5 gateway (vLLM with the parser
flags) sees no change: the envelope still wins, the segmenter's
content output is just the (now-unsentineled) text.

## Implementation

- `dialect/segmenter.go` — Segment / SegmentKind / Segmenter
  interface; `NewSegmenter(family string) Segmenter`;
  passthroughSegmenter implementation.
- `dialect/kimi_segmenter.go` — KimiSegmenter handling the
  `<|tool_calls_section_begin|>…<|tool_calls_section_end|>`
  grammar with chunk-boundary buffering.
- `dialect/glm_segmenter.go` — GLMSegmenter handling
  `<think>…</think>` blocks with chunk-boundary buffering.
- `stream/client.go` — invoke `dialect.NewSegmenter(family)` per
  stream, Feed every content delta, Flush at end-of-stream,
  merge per the rules above.
- `dialect/kimi_segmenter_test.go`, `dialect/glm_segmenter_test.go`
  — unit tests for clean content, single tool call, multiple
  tool calls in one section, sentinel split across chunks,
  malformed inner JSON, missing closing sentinel.
- `stream/client_test.go` — SSE-level end-to-end test for both
  families.

## References

- ADR-001 (architecture, no provider interfaces) — the dialect
  file IS the contract.
- ADR-v4-001 (registry key shape) — segmenter selection keys off
  the same family identifier.
- ADR-v4-009 (reasoning excluded from hash) — preserved unchanged.
- `stream/client.go:416-552` — current envelope-only path.
- `dialect/kimi.go`, `dialect/kimi_code.go`, `dialect/glm.go` —
  pre-segmenter dialect surfaces.
- Operator log, 2026-06-30: Kimi K2.7-Code sentinel leak +
  GLM-5 `</think>` leak under CSCS Envoy AI Gateway.
