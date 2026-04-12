# Invariants

Things that must always be true. Violations are bugs.

## Memory Integrity

1. **Hash chain unbroken.** For any checkpoint C with ParentHash P, there must exist a checkpoint with Hash == P (or P is the zero hash for the first checkpoint in a chain).
2. **Signatures valid.** For any checkpoint C, `ed25519.Verify(author_pubkey, C.Hash, C.Signature)` must return true.
3. **Append-only.** No checkpoint is ever modified after creation. The sqlite store has no UPDATE or DELETE operations on the checkpoints table.
4. **Hash deterministic.** `sha256(canonical_serialize(checkpoint_content))` always produces the same hash for the same content.

## Context Management

5. **Single owner.** The context manager is the only component that adds or removes messages from the context window. Dialect, memory, and stream packages request changes; the manager decides.
6. **Token budget respected.** The context window token count never exceeds 95% of the active model's maximum context length. The remaining 5% is reserved for the model's response.
7. **Compaction preserves recent.** Compaction never removes the most recent N turns (N configurable, default 3). Only older turns are summarized.
8. **Backfill is additive.** Backfill inserts checkpoint summaries as system-level context; it does not remove existing messages.

## Routing

9. **One model per turn.** Each turn is handled by exactly one model. Model switching happens between turns, never mid-generation.
10. **Handoff creates checkpoint.** Every model switch creates a checkpoint before the switch occurs. The new model starts with this checkpoint's summary.
11. **`--model` flag is absolute.** If the user specifies a model via `--model` flag, the dialect router does not change models and tier fallback does not apply. If the locked model is unreachable, the request fails.
11a. **`/deep` is temporary.** `/deep` switches to GLM-5 immediately but auto-routing continues to evaluate. When routing conditions no longer warrant GLM-5 (e.g., compaction reduces context below threshold), the model reverts to M2.5 automatically. `/deep` has no effect when `--model` flag is set.

## Sync

12. **Orphan branch isolation.** The `ghyll/memory` branch shares no git history with any code branch. It is created as an orphan.
13. **Sync is non-blocking.** Git push/pull for memory sync never blocks the main interaction loop. Failures are logged, not fatal.
14. **Sync is idempotent.** Pushing the same checkpoint twice has no effect. Pulling already-present checkpoints has no effect.

## Tools

15. **No permission logic.** Tool execution never checks permissions, prompts the user, or filters operations. Tarn handles all sandboxing externally.
16. **Timeout enforced.** Every tool execution has a timeout (configurable, default 30s for bash, 5s for file ops). Timeout produces an error, not a hang.

## Embedding

17. **Model availability graceful.** If the ONNX embedding model is not downloaded, memory features (drift detection, backfill, search) are disabled with a warning — not a crash.

## Stream

18. **Retry before fallback.** The stream client retries 3 times with exponential backoff before triggering tier fallback. No fallback on first failure.
19. **Fallback reformats context.** When falling back to another tier, context is reformatted for the target dialect before sending. Never send M2.5-formatted context to GLM-5 or vice versa.
20. **Partial responses are surfaced.** If a stream is interrupted after receiving partial content, the partial content is shown to the user, not silently dropped.
20a. **Fallback requires auto-routing.** Tier fallback only applies when auto-routing is active. When `--model` flag is set, the model is locked and failure means failure — no silent switch.

## Compaction

21. **Proactive before reactive.** Proactive compaction (>90% check) runs before every turn. Reactive compaction (model rejection) is a fallback, not the normal path.
22. **Compaction creates checkpoint.** Every compaction creates a checkpoint capturing pre-compaction state. Compacted information is recoverable from checkpoints.
23. **Reactive retry is once.** After reactive compaction, the request is retried exactly once. A second rejection is surfaced as an error.
24a. **Compaction is a separate API call.** The compaction request contains only the turns to summarize and the dialect's compaction prompt. It does not send the full context window. This prevents the compaction call itself from exceeding the model's context limit.
24b. **Compaction before handoff.** When context depth triggers routing escalation, compaction runs on the current model first. The handoff to the new model uses the compacted context. Never hand off an over-limit context.

## Vault

25. **Vault is optional.** All core functionality works without a vault. Vault adds faster team search, not new capabilities.
26. **Localhost vault needs no token.** Requests to 127.0.0.1 or ::1 skip bearer token auth. Remote vault requires a configured token.
27. **Vault never serves unverified checkpoints for backfill.** Checkpoint signature must verify against a known public key before use in backfill. Unverified checkpoints are logged and skipped.

## Drift

28. **Drift measures against most recent checkpoint.** Cosine similarity is computed between the current context embedding and the most recent checkpoint's embedding. If no checkpoints exist yet, checkpoint 0 (session start) is used.

## Keys

29. **Key pair exists before first checkpoint.** If no ed25519 key pair exists at `~/.ghyll/keys/`, one is generated on first session start. The public key is pushed to `devices/<device-id>.pub` on the memory branch.
30. **Public keys sync with memory branch.** Device public keys are stored on the `ghyll/memory` orphan branch alongside checkpoints. They are fetched during sync like any other memory branch content.

## Session

31. **One session per repo.** Only one ghyll session can run per repository at a time. Enforced by a lockfile at `<repo>/.ghyll.lock`. Second session exits with error.
32. **Lockfile released on exit.** The lockfile is released in SHUTDOWN, including on signal interrupts. Stale locks (dead PID) are detected and reclaimed.
