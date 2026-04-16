# ADR-001: Ghyll Architecture

Date: April 2026
Status: Accepted

## Context

Organizations with self-hosted GPU infrastructure (e.g., Cray EX with GH200 nodes) need a coding assistant CLI that is hyper-optimized for their specific open-weight models rather than a general-purpose tool supporting hundreds of providers. Existing tools (Claude Code, OpenCode, Gemini CLI) prioritize breadth over depth, paying an abstraction tax that degrades performance for any specific model.

## Decisions

### 1. Go over TypeScript/Rust

**Decision:** Single Go binary, no runtime dependencies.

**Rationale:** TypeScript requires Node.js runtime, npm ecosystem, and carries abstraction overhead. Rust ecosystem complexity ("too big of a mess") outweighs benefits for a tool this size (~10K lines). Go provides: fast compilation, single static binary, excellent stdlib for HTTP/JSON/exec, good ONNX Runtime bindings, cross-platform without ceremony.

### 2. Concrete dialects over provider abstraction

**Decision:** Each model gets a dedicated file with standalone functions. No shared interface, no adapter pattern.

**Rationale:** The abstraction tax is real. A generic provider interface forces all models through the same code path, losing model-specific optimizations: custom system prompts tuned to training, model-specific tool-calling format parsing, compaction prompts that account for attention characteristics (DSA for GLM-5, Lightning Attention for M2.5), token counting with the model's actual tokenizer. Adding a model requires writing a new file and recompiling — this is intentional, not a limitation.

### 3. Context-depth routing over external router

**Decision:** Dialect router inside the Go binary, not a separate Python/LiteLLM service.

**Rationale:** Eliminates an entire infrastructure component and its failure modes. The routing logic is ~100 lines of Go based on context depth, tool depth, and user override. Model-specific thresholds live in the dialect module where they belong. No network hop, no separate process, no Python dependency.

### 4. Checkpoint-based handoff over full replay

**Decision:** Model switches use a checkpoint summary + last N turns, not full history replay.

**Rationale:** Full replay wastes tokens re-prefilling the entire history in the new dialect's format. Checkpoint summaries are already being generated for drift detection. The lossy nature is explicit — the developer sees "⟳ switched to glm5, loaded from checkpoint 4" and can provide additional context if needed.

### 5. Git orphan branch over vault service for sync

**Decision:** Memory checkpoints sync via `ghyll/memory` orphan branch in the project's git repo.

**Rationale:** Zero additional infrastructure. Developers already authenticate to git. Append-only checkpoints never conflict. The orphan branch is invisible in normal git workflows. Clone gives you the full team memory. No vault service to deploy, no new auth system, no new ports.

### 6. Merkle DAG over plain database

**Decision:** Checkpoints are hash-linked and ed25519 signed, forming a tamper-evident append-only log.

**Rationale:** Team memory is a trust surface. A compromised ghyll instance could push poisoned checkpoints. Hash-chain verification catches tampering. Ed25519 signatures provide attribution and non-repudiation. The cost is minimal (~200 lines for hash/sign/verify) and retrofitting later would require migrating all existing checkpoints.

### 7. Always-yolo over built-in permissions

**Decision:** No permission system in ghyll. All tool calls execute immediately.

**Rationale:** SRT (Anthropic's Sandbox Runtime) provides OS-level sandboxing via Seatbelt (macOS) and bubblewrap (Linux). Building a second permission layer inside ghyll would be redundant, add complexity, and create a false sense of security. Defense in depth is provided by two separate projects, not two layers in one project.

### 8. ONNX download over bundled model

**Decision:** Embedding model (~60MB) is downloaded on first use, not bundled in the binary.

**Rationale:** Keeps the Go binary small (~15MB). Allows model updates without recompiling. Supports air-gapped environments by making the download URL configurable. Graceful degradation when model is unavailable — memory features disabled, core functionality unaffected.

## Consequences

- Adding a new model requires Go code changes and recompilation
- No hot-swapping of model support
- Git is a hard dependency for memory features
- SRT (or equivalent sandbox) is assumed but not enforced
- Team memory trust depends on developer key management
