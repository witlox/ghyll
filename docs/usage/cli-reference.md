# CLI Reference

## ghyll

### `ghyll run [dir] [--model <model>] [--resume]`

Start an interactive coding session.

- `dir` --- working directory (default: `.`)
- `--model` --- lock to a specific model for the session (disables auto-routing and tier fallback)
- `--resume` --- load the previous session's final checkpoint summary as context. Restores plan mode state and links the new session to its predecessor.

```bash
ghyll run .                    # auto-detect model
ghyll run . --model glm5       # force GLM-5
ghyll run . --resume           # continue from last session
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
| `/fast` | Clear /deep override and plan mode, restore auto-routing |
| `/plan` | Enter plan mode --- augments system prompt for deeper reasoning. All tools remain available. |
| `/status` | Show model, turn count, tool depth, plan mode, lock state |
| `/exit` | End session gracefully |
| `/<name>` | Run a user-defined slash command from `.ghyll/commands/<name>.md` |

### Slash Commands

User-defined commands are loaded from `.ghyll/commands/` in your repository (or `~/.ghyll/commands/` globally). Each `.md` file becomes a command. When typed, the file content is injected as a user message.

```
.ghyll/
  commands/
    review.md      # /review --- inject review prompt
    verify.md      # /verify --- inject verification checklist
```

Built-in commands (`/deep`, `/fast`, `/plan`, `/status`, `/exit`) take precedence over user-defined commands with the same name.

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
| `~/.ghyll/instructions.md` | Global workflow instructions |
| `~/.ghyll/roles/` | Global role definitions |
| `~/.ghyll/commands/` | Global slash commands |
| `<repo>/.ghyll/` | Project workflow (instructions, roles, commands) |
| `<repo>/.ghyll.lock` | Session lockfile |
| `<repo>/.ghyll-memory/` | Git worktree for sync |
