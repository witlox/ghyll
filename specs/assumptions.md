# Assumptions

Things we believe but haven't proven. Each is falsifiable.

## Infrastructure

1. **SGLang exposes stable OpenAI-compatible API.** We assume `/v1/chat/completions` with streaming and tool calling works identically across SGLang versions for GLM-5 and MiniMax M2.5. *Falsifiable: test against actual SGLang endpoints.*

2. **Model tool-calling formats are stable.** GLM-5 uses glm47 tool parser, MiniMax M2.5 uses minimax_m2 parser. We assume these formats don't change between model updates. *Falsifiable: monitor SGLang/vLLM release notes.*

3. **Git is available on all developer machines.** We assume `git` is in PATH and authenticated to the project remote. *Falsifiable: check on first run.*

4. **Tarn (or equivalent sandbox) is running.** We assume ghyll runs inside a sandboxed environment. We do not verify this. *Falsifiable: ghyll could detect Tarn's presence, but we explicitly choose not to — defense in depth, not dependency.*

## Models

5. **MiniMax M2.5 handles 80% of coding tasks adequately.** Based on SWE-bench (80.2%) and the observation that most coding assistant requests are routine. *Falsifiable: measure escalation rate in production — if >40% escalate to GLM-5, this assumption is wrong.*

6. **Context depth is a useful routing signal.** We assume that longer contexts correlate with harder tasks. *Falsifiable: track routing decisions vs user satisfaction.*

7. **GLM-5's DSA attention affects which tokens matter at long range.** We assume compaction should be DSA-aware. *Falsifiable: compare DSA-aware vs naive compaction on retrieval quality.*

8. **60MB ONNX embedding model produces useful similarity scores.** BGE-micro or GTE-micro at this size may not capture code semantics well enough for drift detection. *Falsifiable: measure drift detection accuracy against human judgment.*

## Memory

9. **Checkpoint summaries are sufficient for handoff.** We assume losing full message history during model switch is acceptable if the summary is good. *Falsifiable: compare handoff quality with full replay vs checkpoint summary.*

10. **Git orphan branch sync is fast enough.** We assume git push/pull of small JSON files completes in <5s on typical networks. *Falsifiable: measure on real infrastructure.*

11. **Developers will trust team memory.** We assume seeing attributed, hash-verified checkpoints from teammates is useful and trusted. *Falsifiable: observe actual usage — do developers use team memory search?*

12. **Append-only scales for years.** At ~5KB per checkpoint, ~50 checkpoints per developer per day, 1000 developers: ~250MB/day, ~90GB/year. Git handles this but clone time grows. *Falsifiable: measure clone time after 6 months.*

## Developer Behavior

13. **Developers won't need to switch models more than 2-3 times per session.** Frequent switching would degrade quality due to lossy handoff. *Falsifiable: track switch frequency.*

14. **Drift detection threshold of 0.7 cosine similarity is reasonable.** Too sensitive wastes tokens on backfill; too loose misses drift. *Falsifiable: tune empirically.*

## Sub-Agents

15. **Sub-agent on fast tier is cost-effective.** We assume dispatching M2.5 for focused tasks (file exploration, grep, summarization) costs fewer tokens overall than the parent doing it inline with its expensive deep-tier context. *Falsifiable: compare total token usage with and without sub-agents on representative tasks.*

15a. **Sub-agent work loss on crash is acceptable.** Sub-agents are not checkpointed during their turn-loop. If a sub-agent crashes mid-execution, all its intermediate work is lost. We accept this because sub-agents are cheap (fast tier), focused (single task), and short-lived (max 20 turns). The parent can re-dispatch. *Falsifiable: if sub-agents routinely crash after many turns of expensive work, checkpointing per sub-agent turn becomes worthwhile.*

16. **Sub-agents complete within 20 turns.** Most focused tasks (explore files, run tests, search code) should resolve in well under 20 turn-loops. *Falsifiable: measure actual turn counts in production. If >30% hit the limit, the default is too low.*

17. **SGLang handles concurrent inference requests.** When the parent (GLM-5) dispatches a sub-agent (M2.5), both endpoints may receive requests near-simultaneously. We assume SGLang's request queue handles this. *Falsifiable: load test concurrent requests to both endpoints.*

## Web Tools

18. **Tarn denial is indistinguishable from network timeout.** We assume ghyll cannot differentiate "Tarn blocked this domain" from "network unreachable." The retry-with-backoff strategy handles both cases identically. *Falsifiable: if Tarn provides a distinguishable signal (specific error code, header), we can give better error messages.*

19. **DuckDuckGo or equivalent is self-hostable.** We assume a search backend exists that can be deployed on-premise. If no self-hosted option is viable, web search degrades to web fetch only (user provides URLs). *Falsifiable: evaluate SearXNG or similar.*

## Workflow

20. **Models can follow role instructions reliably.** We assume GLM-5 and M2.5 can parse and follow behavioral constraints from role files (e.g., "do not write code, produce specs only"). *Falsifiable: test role compliance across a representative set of tasks. If models routinely violate role constraints, the workflow system has limited value.*

21. **Instruction budget of ~2000 tokens is sufficient.** Most project instructions + role overlay should fit in ~2000 tokens. This leaves 98-99% of context for the actual conversation. *Falsifiable: measure actual instruction sizes across projects. If most exceed 2000, the default is too low.*

22. **Automatic role switching works with open-weight models.** We assume the model can read a workflow router in project instructions and determine the correct role automatically. This requires instruction-following capability that may vary across models. *Falsifiable: test automatic role detection on both GLM-5 and M2.5 with a representative set of intents.*

## Session Resume

23. **Checkpoint summary is sufficient for session continuity.** We assume a developer can resume meaningful work from a structured summary of the previous session (task, decisions, files touched) without replaying the full conversation. *Falsifiable: user satisfaction survey after resume. If >40% feel they lack context, summaries need enrichment.*

## Edit Tool

24. **Models produce correct old_string values.** Surgical edits require the model to reproduce the exact text to replace, including whitespace. We assume models can do this reliably from their context. *Falsifiable: measure edit tool error rate (old_string not found) vs write_file usage. High error rate → models prefer full rewrites.*
