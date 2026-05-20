# ghyll

**Correctness-first coding agent CLI for self-hosted open-weight models**

ghyll provides Claude Code-style agentic coding against self-hosted models running on your own GPU infrastructure, with a gate-and-arrow correctness mechanism layered on top. It is hyper-optimized for a small set of open-weight models with model-specific dialect modules, context-depth routing, and a typed clause-evaluation runtime.

## Key Features

- **Gate-and-arrow correctness** --- role transitions are first-class typed arrows; clauses have evaluation type and depth type; routing follows the gate, not self-assessed complexity
- **Operator-verdict modal** --- attestations require an operator pass/fail/insufficient-basis decision; the pass cannot proceed silently past an attested clause
- **Model-specific dialects** --- no provider abstraction layer, each model gets hand-tuned prompt templates and tool parsing
- **Context-depth routing** --- escalates from fast tier (MiniMax M2.5) to deep tier (GLM-5) based on the depth requirements of the clauses on the arrow
- **Drift-aware memory** --- vector embeddings detect when conversation drifts from the task, automatically backfills relevant context
- **Tamper-evident checkpoints** --- Merkle DAG with ed25519 signatures, synced via git orphan branch
- **Team memory** --- checkpoints from all developers accessible via vector similarity search (vault server)
- **Sandbox-only execution** --- ghyll executes tool calls directly with no permission layer; the surrounding sandbox is the security boundary

## Architecture Overview

```
Developer machine (inside sandbox)
├── ghyll CLI
│   ├── dialect/     model-specific code (M2.5, GLM-5, DeepSeek, Qwen)
│   ├── runner/      gate-and-arrow runtime, clause evaluation, modal driver
│   ├── engine/      sqlite store + journal observer fanout + replay + recovery
│   ├── bootstrap/   project init, grid assembly, modify rules
│   ├── context/     compaction + drift + backfill
│   ├── memory/      sqlite + ed25519 + git orphan-branch sync
│   ├── stream/      SSE client + terminal rendering
│   ├── tool/        direct OS operations
│   └── config/      TOML configuration
│
├── .ghyll/                     (project-local)
│   ├── engine.db               persistent runner state
│   ├── attestations/<pass>/    per-pass tree audit log
│   └── attestations.jsonl      flat aggregate audit log
│
├── ~/.ghyll/                   (user-global)
│   ├── config.toml             endpoints + thresholds
│   ├── memory.db               checkpoint store
│   ├── keys/                   ed25519 signing keys
│   └── models/                 ONNX embedding model
│
└── SGLang endpoints
    ├── MiniMax M2.5 (fast tier)
    ├── GLM-5 (deep tier)
    ├── DeepSeek
    └── Qwen
```

## Quick Start

```bash
# Build
make build-bin

# Configure
mkdir -p ~/.ghyll
cp config/example.toml ~/.ghyll/config.toml
# Edit endpoints to point at your SGLang instances

# Run (inside a sandbox — see the operator guide for setup)
ghyll run .
```

See the [operator guide](operator-guide.md) for sandbox setup
(Docker / Podman / bubblewrap / sandbox-exec / Firejail / Kubernetes),
vault deployment, and the Tier 2 verdict modal flow. The
[architecture flows](architecture-flows.md) document covers the
verdict modal and orchestration sequences in detail.

## What This Is Not

- Not a general-purpose LLM client (use LiteLLM, OpenCode, etc.)
- Not model-agnostic --- adding a model means writing a new dialect file and recompiling
- Not a sandbox --- ghyll runs in YOLO mode by design; the sandbox is whatever wraps the process (Docker, bubblewrap, sandbox-exec, …)
- Not a chat interface --- this is a tool-calling coding agent with an enforced gate workflow
