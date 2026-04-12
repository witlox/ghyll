# Ghyll — Technical Design

## Overview

Ghyll is a purpose-built coding agent CLI for self-hosted open-weight models. It runs inside a Tarn sandbox and targets a Cray EX inference cluster with GH200 nodes serving GLM-5 and MiniMax M2.5 via SGLang.

## Architecture

```
┌──────────────────────────────────────────────────┐
│ Developer machine (inside Tarn sandbox)           │
│                                                   │
│  ghyll (single Go binary)                         │
│  ┌──────────────────────────────────────────────┐ │
│  │ cmd/ghyll          CLI, session loop          │ │
│  │ dialect/           model-specific code        │ │
│  │   router.go        context-depth routing      │ │
│  │   glm5.go          GLM-5 dialect              │ │
│  │   minimax_m25.go   MiniMax M2.5 dialect       │ │
│  │   handoff.go       checkpoint-based switch     │ │
│  │ context/           unified context manager    │ │
│  │   manager.go       compaction + backfill      │ │
│  │   drift.go         embedding similarity       │ │
│  │   injection.go     signal detection           │ │
│  │ memory/            checkpoint store           │ │
│  │   store.go         sqlite + hash chain        │ │
│  │   embedder.go      ONNX runtime              │ │
│  │   sync.go          git orphan branch          │ │
│  │ tool/              direct OS operations       │ │
│  │ stream/            SSE client + renderer      │ │
│  │ config/            TOML configuration         │ │
│  └──────────────────────────────────────────────┘ │
│                                                   │
│  ~/.ghyll/                                        │
│    config.toml        endpoints, thresholds       │
│    memory.db          sqlite checkpoint store     │
│    keys/              ed25519 keypair             │
│    models/            ONNX embedding model        │
└───────────────────────┬───────────────────────────┘
                        │ HTTPS
              ┌─────────┴─────────┐
              │                   │
    SGLang: M2.5        SGLang: GLM-5
    (Cray EX nodes)     (Cray EX blades)
```

## Checkpoint Format

```go
type Checkpoint struct {
    Version      int       `json:"v"`
    Hash         string    `json:"hash"`          // hex(sha256(canonical content))
    ParentHash   string    `json:"parent"`        // previous checkpoint hash, or "0"*64
    DeviceID     string    `json:"device"`
    AuthorID     string    `json:"author"`
    Timestamp    int64     `json:"ts"`            // unix nanos
    RepoRemote   string    `json:"repo"`          // git remote URL
    Branch       string    `json:"branch"`        // git branch at time of checkpoint
    SessionID    string    `json:"session"`       // unique per ghyll invocation
    Turn         int       `json:"turn"`
    ActiveModel  string    `json:"model"`         // "m25" or "glm5"
    Summary      string    `json:"summary"`       // structured natural language
    Embedding    []float32 `json:"emb"`           // vector from ONNX model
    FilesTouched []string  `json:"files"`
    ToolsUsed    []string  `json:"tools"`
    InjectionSig []string  `json:"injections,omitempty"`
    Signature    string    `json:"sig"`           // hex(ed25519.Sign(privkey, hash))
}
```

Canonical serialization: JSON with sorted keys, no whitespace, UTF-8. Hash computed over all fields except `hash` and `sig`.

## Routing Decision Table

| Condition | Current Model | Action |
|-----------|--------------|--------|
| Session start | — | Select M2.5 |
| context_depth > 32K tokens | M2.5 | Escalate to GLM-5 |
| tool_depth > 5 sequential calls | M2.5 | Escalate to GLM-5 |
| drift backfill triggered | M2.5 | Escalate to GLM-5 |
| User types `/deep` | any | Switch to GLM-5 |
| User types `/fast` | any | Switch to M2.5 |
| `--model` flag set | — | Use specified, never change |
| Compaction reduces context < 16K | GLM-5 (auto-escalated) | De-escalate to M2.5 |

## Git Memory Branch Layout

```
ghyll/memory (orphan branch)
  devices/
    <device-id>.pub              ed25519 public key
  repos/
    <sha256(git-remote-url)>/
      checkpoints/
        <checkpoint-hash>.json   individual checkpoint files
      chains/
        <device-id>.jsonl        ordered hash chain per device
```

## Sync Protocol

```
Session start:
  git fetch origin ghyll/memory:ghyll/memory (background)
  import new checkpoint files into local sqlite
  verify hash chains for imported checkpoints

Checkpoint creation:
  write <hash>.json to worktree
  append to chains/<device-id>.jsonl
  git add + commit (background)
  git push origin ghyll/memory (background, retry on conflict)

Push conflict resolution:
  git pull --ff-only origin ghyll/memory
  retry push (append-only = always fast-forward)
  after 3 failures: queue for next sync interval
```

## Configuration

```toml
[models.m25]
endpoint = "https://inference.internal:8001/v1"
dialect = "minimax_m25"
max_context = 1000000
description = "MiniMax M2.5 — fast tier"

[models.glm5]
endpoint = "https://inference.internal:8002/v1"
dialect = "glm5"
max_context = 200000
description = "GLM-5 — deep tier"

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
