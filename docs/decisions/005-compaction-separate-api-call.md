# ADR-005: Compaction as Separate API Call

Date: April 2026
Status: Accepted

## Context

Context compaction summarizes older turns to reduce token count. The original design was ambiguous about whether the compaction request uses the full context window or a subset.

If compaction sends the full context (which is at 90%+ of the limit), the compaction request itself may exceed the model's context limit. The compaction prompt plus the turns-to-summarize must fit within the model's capacity.

This was identified during adversary review of the analyst specs.

## Decision

Compaction is a separate API call containing only:
1. The dialect's compaction prompt
2. The turns to summarize (all except the last N preserved turns)

The full context window is never sent in the compaction request. The compaction call is made to the same model endpoint but as an independent request.

## Consequences

- Compaction cannot exceed the model's context limit
- The compaction prompt and turns-to-summarize must fit in the model's context (guaranteed since they're a subset of the full context which was within limits before the recent growth)
- Compaction before handoff: runs on the current model first, then handoff uses the compacted context
- The context manager builds a `CompactionRequest` struct; `cmd/ghyll` wires the stream client to execute it
