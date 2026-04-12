# Troubleshooting

## Common Issues

### "another ghyll session is active (pid N)"

Only one ghyll session can run per repository at a time. Check if another terminal is running ghyll in the same directory. If the previous session crashed, the stale lockfile will be automatically reclaimed.

### "no config found at ~/.ghyll/config.toml"

Create the configuration file. See [Configuration](configuration.md) for the full reference.

### "default model 'm25' has no endpoint configured"

The routing default model must have a corresponding `[models.m25]` section in config.toml.

### "all model endpoints unreachable"

Both SGLang endpoints are down. Check network connectivity to your inference cluster. ghyll retries 3 times with exponential backoff before giving up.

### "embedding model not available, drift detection disabled"

This is a warning, not an error. Run `make embedder` to download the ONNX model for drift detection. ghyll works fine without it.

### "stream interrupted after N tokens"

The connection to the model endpoint dropped mid-response. The partial response is preserved. You can retry by sending the same request.

### Checkpoint verification warnings

- `unverified checkpoint from @device (unknown key)` --- the device's public key is not on the memory branch. Wait for the next sync, or manually fetch.
- `checkpoint chain broken at X from @device` --- a checkpoint was tampered with or a sync was incomplete. The checkpoint is excluded from backfill.

## Performance

### Token counting is approximate

ghyll uses character-based estimation (~4 chars/token for M2.5, ~3 chars/token for GLM-5). This is intentionally approximate to avoid tokenizer dependencies. The reactive compaction fallback handles cases where the estimate is too low.

### Sync is slow on first run

The initial orphan branch creation clones the repo. Subsequent syncs are fast (append-only).

### Tool depth limit

ghyll limits tool call chains to 50 sequential calls. If a model keeps requesting tool calls, the session stops with an error. This prevents runaway loops from buggy models.
