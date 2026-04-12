# CLI Reference

## ghyll

### `ghyll run [dir] [--model <model>]`

Start an interactive coding session.

- `dir` --- working directory (default: `.`)
- `--model` --- lock to a specific model for the session (disables auto-routing and tier fallback)

```bash
ghyll run .                    # auto-detect model
ghyll run . --model glm5       # force GLM-5
ghyll run ~/repos/myproject    # specify directory
```

### `ghyll config show`

Display the loaded configuration.

### `ghyll memory log`

Show all checkpoints ordered by timestamp.

```
a1b2c3d4e5f6  2026-04-12 10:30  [m25] @alice  turn 5  fixed auth race condition
d4e5f6a1b2c3  2026-04-12 10:45  [glm5] @alice  turn 10  compaction summary
```

### `ghyll memory search <query>`

Search checkpoint summaries for matching terms.

```bash
ghyll memory search "auth race condition"
```

### `ghyll version`

Print the version string.

## In-Session Commands

| Command | Effect |
|---------|--------|
| `/deep` | Switch to GLM-5 (temporary, auto-routing can revert) |
| `/fast` | Clear /deep override, restore auto-routing |
| `/status` | Show model, turn count, tool depth, lock state |
| `/exit` | End session gracefully |

## ghyll-vault

### Running

```bash
ghyll-vault
```

Starts the team memory search server on `:9090`. Requires `[vault]` section in config.

### `ghyll-vault version`

Print the version string.

## Environment

ghyll reads configuration from `~/.ghyll/config.toml`. The following paths are used:

| Path | Purpose |
|------|---------|
| `~/.ghyll/config.toml` | Configuration |
| `~/.ghyll/memory.db` | Checkpoint store (SQLite) |
| `~/.ghyll/keys/` | Ed25519 signing keys |
| `~/.ghyll/models/` | ONNX embedding model |
| `<repo>/.ghyll.lock` | Session lockfile |
| `<repo>/.ghyll-memory/` | Git worktree for sync |
