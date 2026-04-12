# Domain Model

## Core Concepts

### Session
A single interactive coding conversation between a developer and ghyll. Has a lifecycle: start → turns → (optional: model switch, compaction, backfill) → end. Produces checkpoints.

### Turn
One request-response cycle within a session. A turn may involve multiple tool calls. Each turn has an input (user prompt + tool results) and an output (model response + tool calls).

### Dialect
Model-specific code that handles prompt formatting, tool call parsing, token counting, system prompts, and compaction prompts. Each supported model has exactly one dialect. Dialects are concrete — no shared interface.

### Context Window
The current set of messages being sent to the model. Managed by the context manager. Subject to compaction when approaching the model's limit. Subject to backfill when drift is detected.

### Checkpoint
A snapshot of session state at a point in time. Contains: structured summary, vector embedding, metadata (files touched, tools used, turn number), hash chain link, and ed25519 signature. Append-only — never modified after creation.

### Hash Chain (Merkle DAG)
Checkpoints are linked by hash: each checkpoint contains the hash of its parent. Multiple branches are allowed (one per device). Tamper-evident — modifying any checkpoint breaks all subsequent hashes. Signed — each checkpoint carries an ed25519 signature from its author.

### Drift
Semantic divergence between the current conversation context and the original task. Measured by cosine similarity between the current context embedding and the session's checkpoint embeddings. When drift exceeds a threshold, backfill is triggered.

### Backfill
Injection of relevant checkpoint summaries into the current context to correct drift. Retrieves the top-k most semantically similar checkpoints and prepends them as context. Model-aware — the backfill format matches the active dialect.

### Compaction
Reduction of context size when approaching the model's token limit. Summarizes older turns while preserving recent turns and key decisions. Model-aware — uses the dialect's compaction prompt for the active model.

### Routing
Selection of which model handles the current turn. Based on context depth (token count), tool call depth, and explicit user override. Handled by the dialect router, not an external service.

### Handoff
Transfer of session context from one model to another during a model switch. Uses checkpoint-based approach: create a checkpoint, start the new model with checkpoint summary + recent turns in the target dialect's format. Intentionally lossy.

### Memory Sync
Replication of checkpoints between ghyll instances via git. Uses an orphan branch (`ghyll/memory`) in the project's git repo. Append-only — no merge conflicts. Background sync on configurable interval.

### Team Memory
Checkpoints from all developers working on the same repo, accessible via vector similarity search. Attributed — each checkpoint shows who wrote it and when. Tamper-evident via hash chain verification.

### Injection Signal
Detection of prompt injection patterns in conversation turns. Checked at checkpoint creation time. Patterns include: instruction override attempts, base64 payloads, requests for files outside workspace, attempts to modify system prompts. Reported to developer, not blocked (Tarn handles enforcement).

## Package Mapping

| Concept | Primary Package | Secondary |
|---------|----------------|-----------|
| Session | cmd/ghyll | context/ |
| Turn | stream/ | dialect/ |
| Dialect | dialect/ | — |
| Context Window | context/ | dialect/ |
| Checkpoint | memory/ | context/ |
| Hash Chain | memory/ | — |
| Drift | context/ | memory/ |
| Backfill | context/ | memory/ |
| Compaction | context/ | dialect/ |
| Routing | dialect/ | context/ |
| Handoff | dialect/ | memory/ |
| Memory Sync | memory/ | tool/git |
| Team Memory | vault/ | memory/ |
| Injection Signal | context/ | — |
