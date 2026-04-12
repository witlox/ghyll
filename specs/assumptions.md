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
