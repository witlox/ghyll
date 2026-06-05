# ADR-v4-009: ReasoningContent excluded from canonical checkpoint hash

**Status:** Accepted (2026-06-05)

## Context

The Kimi 2.5/2.6 dialect surfaces a model-side reasoning trace via
the OpenAI-compatible `reasoning_content` field on assistant turns.
Other dialects ignore the field; Kimi uses it for chain-of-thought
streaming. To preserve the trace across multi-turn round-trips we
add `ReasoningContent string` to `types.Message` with a
`json:"reasoning_content,omitempty"` tag.

Today `memory.Checkpoint` does NOT persist `Message` arrays — the
canonical hash already excludes them. But the invariant matters
prospectively: a future refactor could wire Messages onto Checkpoint
(or surface them in the JSONL audit trail under a hashed payload).
ADR-003 set the precedent for excluding fields whose cross-platform
serialization is unstable; ReasoningContent is the next member of
that class:

- Tokenizer-injected sentinel bytes vary between deployments and
  quantization tiers of the same model.
- Whitespace normalization is backend-dependent — vLLM, SGLang, and
  llama.cpp do not agree on how to surface model-side `\r`, `‬`
  (PDF control), and certain CJK runes inside reasoning streams.
- A future cross-device verifier that hashed reasoning content
  would fail in exactly the same way ADR-003 said the embedding
  field would fail (Section "Decision" of ADR-003).

## Decision

`types.Message.ReasoningContent` MUST follow the ADR-003 exclusion
precedent: any future canonical-hash path that ingests Message
arrays MUST omit this field from the canonical map.

Today the rule is documented (1) inline in `memory/crypto.go`
CanonicalHash next to the existing Embedding-exclusion comment and
(2) asserted by
`TestMemory_CanonicalHash_StableAcrossReasoningSerialization` in
`memory/crypto_reasoning_test.go`. The wire-side round-trip lives
in `dialect/helpers.go`'s `buildOpenAIMessages`, which emits
`reasoning_content` ONLY for assistant turns (user / tool / system
turns never carry a reasoning trace).

## Consequences

- Cross-device checkpoint hash verification stays portable across
  Kimi backends with divergent reasoning serialization.
- The `omitempty` JSON tag keeps non-Kimi dialects' wire surface
  unchanged — they continue to ignore the field entirely.
- The wire round-trip is restricted to `Role == "assistant"`; an
  upstream that smuggles `reasoning_content` onto a user turn does
  not corrupt the assistant-turn-only contract.
- The single producer for `reasoning_content` is the Kimi dialect
  family; if other dialects later surface their own reasoning
  field, they SHOULD route through `Message.ReasoningContent` and
  inherit the exclusion automatically.
- If integrity protection over reasoning becomes necessary (e.g.,
  for an attestation audit), use a fixed binary encoding alongside
  the canonical hash rather than including the JSON-serialized
  string — same escape hatch ADR-003 left open for embeddings.

## Migration

No chain rewrite is required. Checkpoints written before this commit
have no `ReasoningContent` (the field did not exist on
`types.Message`); the field is `omitempty`; and `memory.CanonicalHash`
currently consults only the scalar Checkpoint fields — Message arrays
are never folded into the canonical map today. The addition is
purely prospective: existing chains continue to verify byte-for-byte
identical to before this commit, and `VerifyChain` /
`VerifyCheckpoint` need no special case for "pre-v4-009" entries.

If a future refactor wires `Messages []types.Message` into Checkpoint
(or any other hashed payload), it MUST drop `ReasoningContent` from
the canonical map per this ADR. The teeth-bearing assertion lives in
`memory.TestMemory_CanonicalHash_StableAcrossReasoningSerialization`:
it directly exercises `canonicalJSON` and asserts the bytes are
identical regardless of which reasoning payload was constructed
alongside, and inversely demonstrates that INCLUDING the reasoning
in the map WOULD produce divergent bytes.
