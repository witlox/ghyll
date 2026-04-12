# ADR-003: Embedding Excluded from Canonical Hash

Date: April 2026
Status: Accepted

## Context

The canonical hash of a checkpoint originally included all fields except `hash` and `sig`. The `Embedding` field is a `[]float32` vector. Go's `json.Marshal` for float32 values can produce different string representations across Go versions and platforms (e.g., trailing zeros, scientific notation for edge cases). If two Go versions produce different JSON for the same `[]float32`, the hash differs and cross-device verification fails.

This was identified during adversary review as a high-severity correctness issue.

## Decision

Exclude the `Embedding` field from the canonical hash computation. Embeddings serve search and drift detection --- they don't need integrity protection. The summary text (which is hashed) captures the semantic content that the embedding represents.

## Consequences

- Cross-platform hash verification is reliable
- An attacker who modifies only the embedding can change search results but not the summary or metadata
- This is acceptable: embeddings are for convenience (search ranking), not trust (content integrity)
- If embedding integrity becomes needed, use a fixed binary encoding instead of JSON serialization
