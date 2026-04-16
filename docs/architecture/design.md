# Architecture Overview

Ghyll is a purpose-built coding agent CLI for self-hosted open-weight models. It runs inside an SRT sandbox and targets a Cray EX inference cluster with GH200 nodes serving GLM-5 and MiniMax M2.5 via SGLang.

This section documents ghyll's internal architecture. Each page covers a specific subsystem in detail.

## System Diagram

```
+--------------------------------------------------+
| Developer machine (inside SRT sandbox)            |
|                                                   |
|  ghyll (single Go binary)                         |
|  +----------------------------------------------+ |
|  | cmd/ghyll          CLI, session loop          | |
|  | dialect/           model-specific code        | |
|  |   router.go        context-depth routing      | |
|  |   glm5.go          GLM-5 dialect              | |
|  |   minimax_m25.go   MiniMax M2.5 dialect       | |
|  |   parse.go         shared tool call parser      | |
|  | context/           unified context manager    | |
|  |   manager.go       compaction + backfill      | |
|  |   drift.go         embedding similarity       | |
|  |   injection.go     signal detection           | |
|  | memory/            checkpoint store           | |
|  |   store.go         sqlite + hash chain        | |
|  |   embedder.go      ONNX runtime              | |
|  |   sync.go          git orphan branch          | |
|  | tool/              direct OS operations       | |
|  | stream/            SSE client + renderer      | |
|  | config/            TOML configuration         | |
|  +----------------------------------------------+ |
|                                                   |
|  ~/.ghyll/                                        |
|    config.toml        endpoints, thresholds       |
|    memory.db          sqlite checkpoint store     |
|    keys/              ed25519 keypair             |
|    models/            ONNX embedding model        |
+-------------------------+-------------------------+
                          | HTTPS
                +---------+---------+
                |                   |
      SGLang: M2.5        SGLang: GLM-5
      (Cray EX nodes)     (Cray EX blades)
```

## Architecture Pages

- **[Package Graph](package-graph.md)** -- How ghyll's Go packages are organized, their dependencies, and the role of the types/ leaf package.

- **[Session Loop](session-loop.md)** -- The state machine at the heart of cmd/ghyll: initialization, turn execution, compaction, handoff, backfill, and shutdown.

- **[Context-Depth Routing](routing.md)** -- The decision table that determines when to escalate from the fast tier (MiniMax M2.5) to the deep tier (GLM-5) and back.

- **[Checkpoint Format](checkpoints.md)** -- The structure of memory checkpoints, canonical serialization, cryptographic signing, chain verification, and storage layout.

- **[Sync Protocol](sync.md)** -- Git-based memory synchronization via the orphan branch, including worktree setup, conflict handling, and offline operation.

- **[Vault API](vault-api.md)** -- The HTTP API served by ghyll-vault for team memory search, including authentication, endpoints, and client behavior.

- **[Error Types](errors.md)** -- Typed errors organized by package, including sentinel errors, structured error types, and error flow across package boundaries.

## Configuration

```toml
[models.m25]
endpoint = "https://inference.internal:8001/v1"
dialect = "minimax_m25"
max_context = 1000000
description = "MiniMax M2.5 -- fast tier"

[models.glm5]
endpoint = "https://inference.internal:8002/v1"
dialect = "glm5"
max_context = 200000
description = "GLM-5 -- deep tier"

[routing]
default_model = "m25"
context_depth_threshold = 32000
tool_depth_threshold = 5
enable_auto_routing = true

[memory]
branch = "ghyll/memory"
auto_sync = true
sync_interval_seconds = 60
checkpoint_interval_turns = 5
drift_check_interval_turns = 5
drift_threshold = 0.7

[memory.embedder]
model_url = "https://huggingface.co/Xenova/gte-micro/resolve/main/model.onnx"
model_path = "~/.ghyll/models/gte-micro.onnx"
dimensions = 384

[tools]
bash_timeout_seconds = 30
file_timeout_seconds = 5
prefer_ripgrep = true
```
