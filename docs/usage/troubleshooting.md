# Troubleshooting

## Common Issues

### "another ghyll session is active (pid N)"

Only one ghyll session can run per repository at a time. Check if another terminal is running ghyll in the same directory. If the previous session crashed, the stale lockfile will be automatically reclaimed.

### "wrote default config at ~/.ghyll/config.toml; edit the model endpoints and re-run"

First run wrote a default config and exited --- edit `~/.ghyll/config.toml` (drop in real model endpoints) and re-run. The auto-bootstrap is implemented in `cmd/ghyll/config_bootstrap.go` (the C-2 surface): when no config exists, the embedded default template is written with `0o600` and ghyll exits cleanly so the operator can fill in endpoints. See [Configuration](configuration.md) for the full reference.

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

The first sync creates the orphan branch (an `--orphan` checkout on the existing clone, not a re-clone of the repo) and pushes; subsequent syncs are incremental.

### Tool depth limit

After `tool_depth_threshold` (default 5) the router escalates to the deep tier; a hard ceiling of 50 sequential calls aborts the chain (ADR-004). The escalation threshold is NOT the abort --- it's the routing signal that shifts the next call to the deeper model. If a model keeps requesting tool calls past the ceiling, the session stops with an error. This prevents runaway loops from buggy models.
