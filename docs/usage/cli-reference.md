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

### `ghyll init --op-id <id> [project-dir]`

Bootstrap the project: discover contexts + roles, assemble the grid, persist `.ghyll/grid.v1.yaml`. Refuses to overwrite an existing grid. See [Operator Guide --- `ghyll init`](../operator-guide.md#ghyll-init---op-id-id-project-dir) for the full pipeline detail.

```bash
ghyll init --op-id alice@example.com .
```

### `ghyll init attest --op-id <id> [--dir <path>]`

Tier 3 / gate-2 producer for init attestations: emits one on-the-spot record per arrow in the grid (`AttestedByRole=init`), persisted through the standard Record path. Idempotent on re-run. See [Operator Guide --- `ghyll init attest`](../operator-guide.md#ghyll-init-attest---op-id-id---dir-path).

```bash
ghyll init attest --op-id alice@example.com --dir .
```

### `ghyll config show`

Display the loaded configuration.

### `ghyll memory log`

Show the local checkpoint chain (hash, parent, turn, summary).

```
a1b2c3d4e5f6  2026-04-12 10:30  [m25] @alice  turn 5  fixed auth race condition
d4e5f6a1b2c3  2026-04-12 10:45  [glm5] @alice  turn 10  compaction summary
```

### `ghyll memory search <query>`

Vector-search past checkpoints by semantic similarity.

```bash
ghyll memory search "auth race condition"
```

### `ghyll memory sync`

Manually sync the checkpoint chain to/from the vault (push local checkpoints to the orphan branch, fetch remote ones). See [Operator Guide --- `ghyll memory`](../operator-guide.md#ghyll-memory-subcommand).

```bash
ghyll memory sync
```

### `ghyll engine status [--dir <path>]`

Render a project-level summary of engine state: arrow / finding / requirement / amendment / evaluation-run / attestation counts. Reports `ghyll-engine-status: missing` when v2 was never initialized. See [Operator Guide --- `ghyll engine status`](../operator-guide.md#ghyll-engine-status---dir-path).

```bash
ghyll engine status --dir .
```

### `ghyll engine replay [--dir <path>]`

Re-fan-out every persisted entity through the Journal observers, useful to rebuild derived state after a code change to a fan-out consumer. See [Operator Guide --- offline CLI commands](../operator-guide.md#offline-cli-commands) for the full engine subcommand set.

```bash
ghyll engine replay --dir .
```

### `ghyll engine recover [--dry-run] [--dir <path>]`

Preview crash recovery: open passes whose runner is gone, attestation-pending passes preserved across restart, evaluation runs reconciled from the JSONL audit log. `--commit` is refused; apply recovery by starting a session via `ghyll run`. See [Operator Guide --- `ghyll engine recover`](../operator-guide.md#ghyll-engine-recover---dry-run---dir-path).

```bash
ghyll engine recover --dry-run --dir .
```

### `ghyll engine verify-attestations [--dir <path>]`

Walk `.ghyll/attestations.jsonl` and report records that violate the schema (missing fields, wrong kind/clause_id pairing, unknown verdict, §12.2 self-cert violation). Non-zero exit on failure. See [Operator Guide --- `ghyll engine verify-attestations`](../operator-guide.md#ghyll-engine-verify-attestations---dir-path).

```bash
ghyll engine verify-attestations --dir .
```

### `ghyll arrow show <arrow-id> [--dir <path>]`

Render one arrow's live state: definition (source/target role, clauses, requirements), open findings, and all recorded attestations. See [Operator Guide --- `ghyll arrow show`](../operator-guide.md#ghyll-arrow-show-arrow-id---dir-path).

```bash
ghyll arrow show A-checkout --dir .
```

### `ghyll version`

Print the version string.

## In-Session Commands

| Command | Effect |
|---------|--------|
| `/deep` | Temporarily switch to the deep-tier model. Refused when `--model` was passed. |
| `/fast` | Restore auto-routing and clear plan mode. |
| `/plan` | Enter plan mode (deeper reasoning, higher tier preference). All tools remain available. |
| `/status` | Show active model, lock state, deep/plan flags, turn count, tool depth. |
| `/exit` | End the session cleanly. Cancels any in-flight modal read; creates a final checkpoint. |
| `/quit` | REPL alias for `/exit`. |
| `/op-id <id>` | Declare the operator identity for this session. Required before `/attest`. |
| `/op-id` | Show the active op-id (or "(none)"). |
| `/op-id none` (or `clear`) | Clear the active op-id. |
| `/attest <ref> <verdict> [reason]` | Record an attestation verdict on a depth-type or on-the-spot attestation. |
| `/attestations [<arrow-id>]` | List recorded attestations, optionally filtered by arrow. |
| `/passes` | List currently-open passes from the PassRegistry. `/passes <pass-id>` shows one pass's full state. |
| `/list-arrows` | Render the grid snapshot (sorted arrow IDs + source→target / stratum / context / clause count). |
| `/run-arrow <arrow-id> [--context <ctx>]` | Dispatch one arrow synchronously; surface pass-open/close events inline. |
| `/drain-amendments` | FIFO-drain the pending amendment queue under the active op-id; refuses without `/op-id`. Each commit emits `OpEventAmendmentDrained` per ADR-v4-005. (diamond v4 / Gap 2) |
| `/adversary {enable\|disable\|status}` | Toggle the §11 adversarial-cycle hook bundle the dispatcher consults on depth-sensitive arrows. `enable` refuses with `no-dialect-configured` when no active model resolves to a configured endpoint. (diamond v4 / Gap 1) |
| `/invalidate-arrow <arrow-id> [--reason <text>]` | Invalidate one arrow; persists an audit row to `.ghyll/engine.db arrow_invalidations` (ADR-v4-008). Refuses without `/op-id`. |
| `/<name>` | User-defined slash command loaded from `.ghyll/commands/<name>.md`. The file contents are injected as user input for the next turn. |

See [Operator Guide --- Slash commands](../operator-guide.md#slash-commands) for worked examples (op-id, attest, run-arrow, verdict modal).

### Slash Commands

User-defined commands are loaded from `.ghyll/commands/` in your repository (or `~/.ghyll/commands/` globally). Each `.md` file becomes a command. When typed, the file content is injected as a user message.

```
.ghyll/
  commands/
    review.md      # /review --- inject review prompt
    verify.md      # /verify --- inject verification checklist
```

Built-in commands (table above) take precedence over user-defined commands with the same name.

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
| `~/.ghyll/commands/` | Global slash commands |
| `<repo>/.ghyll/` | Project workflow (instructions + commands; `roles/` here is ignored — see ADR-008) |
| `<repo>/.ghyll.lock` | Session lockfile |

The memory-sync git worktree is NOT in the project tree --- it is created in a system temp dir via `os.MkdirTemp` (`memory/sync.go:67`) so the working copy stays clean.
