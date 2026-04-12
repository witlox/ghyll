# ghyll

**Purpose-built coding agent CLI for self-hosted open-weight models**

ghyll provides Claude Code-style agentic coding against self-hosted models running on your own GPU infrastructure. It is hyper-optimized for a small set of open-weight models, with model-specific dialect modules, context-depth routing, and drift-aware memory.

## Key Features

- **Model-specific dialects** --- no provider abstraction layer, each model gets hand-tuned prompt templates and tool parsing
- **Context-depth routing** --- automatically escalates from fast tier (MiniMax M2.5) to deep tier (GLM-5) based on task complexity
- **Drift-aware memory** --- vector embeddings detect when conversation drifts from the task, automatically backfills relevant context
- **Tamper-evident checkpoints** --- Merkle DAG with ed25519 signatures, synced via git orphan branch
- **Team memory** --- checkpoints from all developers accessible via vector similarity search
- **Always-yolo execution** --- Tarn handles sandboxing at the kernel level, ghyll executes tools directly

## Architecture Overview

```
Developer machine (inside Tarn sandbox)
├── ghyll CLI
│   ├── dialect/     model-specific code (M2.5, GLM-5)
│   ├── context/     compaction + drift + backfill
│   ├── memory/      sqlite + hash chain + git sync
│   ├── stream/      SSE client + terminal rendering
│   ├── tool/        direct OS operations
│   └── config/      TOML configuration
│
├── ~/.ghyll/
│   ├── config.toml  endpoints + thresholds
│   ├── memory.db    checkpoint store
│   ├── keys/        ed25519 signing keys
│   └── models/      ONNX embedding model
│
└── SGLang endpoints (Cray EX cluster)
    ├── MiniMax M2.5 (fast tier)
    └── GLM-5 (deep tier)
```

## Quick Start

```bash
# Build
make build-bin

# Configure
mkdir -p ~/.ghyll
cp config/example.toml ~/.ghyll/config.toml
# Edit endpoints to point at your SGLang instances

# Run
ghyll run .
```

## What This Is Not

- Not a general-purpose LLM client (use LiteLLM, OpenCode, etc.)
- Not model-agnostic --- adding a model means writing a new dialect file and recompiling
- Not a sandbox --- that's [Tarn's](https://github.com/witlox/tarn) job
- Not a chat interface --- this is a tool-calling coding agent
