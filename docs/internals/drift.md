# Drift Detection

Drift detection monitors whether the conversation has strayed from the original task and triggers backfill when needed.

## How It Works

Every N turns (configurable, default 5), ghyll:

1. Embeds the current context window using the ONNX embedding model
2. Retrieves the most recent checkpoint's embedding
3. Computes cosine similarity between the two
4. If similarity drops below the threshold (default 0.7), triggers backfill

## Measurement Target

Drift is measured against the **most recent checkpoint**, not the original task. This means:

- After checkpoint 0 (session start): measures against the initial embedding
- After checkpoint 3: measures against checkpoint 3's embedding
- After compaction: measures against the compaction checkpoint

This tracks drift from *recent work*, not just the original goal.

## Backfill

When drift is detected:

1. Search local checkpoints by embedding similarity
2. If local results are insufficient and vault is configured, search team memory
3. Verify signatures on all candidates
4. Select top-k within token budget
5. Prepend summaries to context (additive --- no messages removed)

## Graceful Degradation

If the ONNX embedding model is not downloaded, drift detection is disabled entirely. ghyll displays a warning and continues normally. All other features work without it.

## Thresholds

The drift threshold (default 0.7) controls sensitivity:

- Higher (e.g., 0.8): more sensitive, backfills more often
- Lower (e.g., 0.5): more tolerant, only backfills on major drift
- Configurable per-project in `config.toml`
