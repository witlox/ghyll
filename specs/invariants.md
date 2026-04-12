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
11. **Explicit override wins.** If the user specifies a model via `--model` or `/deep` command, the dialect router does not override it.

## Sync

12. **Orphan branch isolation.** The `ghyll/memory` branch shares no git history with any code branch. It is created as an orphan.
13. **Sync is non-blocking.** Git push/pull for memory sync never blocks the main interaction loop. Failures are logged, not fatal.
14. **Sync is idempotent.** Pushing the same checkpoint twice has no effect. Pulling already-present checkpoints has no effect.

## Tools

15. **No permission logic.** Tool execution never checks permissions, prompts the user, or filters operations. Tarn handles all sandboxing externally.
16. **Timeout enforced.** Every tool execution has a timeout (configurable, default 30s for bash, 5s for file ops). Timeout produces an error, not a hang.

## Embedding

17. **Model availability graceful.** If the ONNX embedding model is not downloaded, memory features (drift detection, backfill, search) are disabled with a warning — not a crash.
