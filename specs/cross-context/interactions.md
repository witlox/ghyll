# Cross-Context Interactions

## Package Dependency Graph

```
cmd/ghyll ──→ dialect/ ──→ (no deps)
    │              │
    ├──→ context/ ─┼──→ memory/
    │              │       │
    ├──→ stream/   │       ├──→ tool/git (for sync)
    │              │       └──→ config/
    ├──→ tool/     │
    │              │
    └──→ config/ ──┘

cmd/ghyll-vault ──→ vault/ ──→ memory/
                               └──→ config/
```

## Interaction Flows

### Flow 1: Normal turn (no routing change)
```
cmd/ghyll → stream/client (send messages to model endpoint)
         → stream/client (receive streaming response)
         → dialect/ (parse tool calls from response)
         → tool/ (execute tool calls)
         → context/manager (update context with turn results)
         → context/manager (check if checkpoint interval reached)
         → memory/checkpoint (create checkpoint if needed)
```

### Flow 2: Context-depth escalation
```
context/manager (detects context depth > threshold)
→ dialect/router (decides to escalate)
→ memory/checkpoint (create handoff checkpoint)
→ dialect/handoff (format checkpoint for target model)
→ context/manager (replace context with handoff context)
→ stream/client (next request goes to new endpoint)
```

### Flow 3: Drift detection and backfill
```
context/manager (every N turns, triggers drift check)
→ memory/embedder (embed current context window)
→ memory/store (retrieve session checkpoints)
→ context/drift (compute cosine similarity)
→ context/drift (similarity < threshold → trigger backfill)
→ memory/store (search for relevant checkpoints, local + synced)
→ memory/store (verify hash chain for remote checkpoints)
→ context/manager (inject checkpoint summaries into context)
→ dialect/router (may escalate to deep tier on backfill)
```

### Flow 4: Memory sync
```
memory/checkpoint (new checkpoint created)
→ memory/sync (write JSON to ghyll/memory worktree)
→ tool/git (git add, commit, push — background, non-blocking)

On session start:
→ memory/sync (git fetch origin ghyll/memory)
→ memory/store (import new remote checkpoints)
→ memory/store (verify hash chains)
```

### Flow 5: Team memory via vault
```
memory/vault_client (POST /checkpoint — on creation, optional)
context/drift (backfill needed, local insufficient)
→ memory/vault_client (GET /search?q=<embedding>&repo=<hash>)
→ memory/store (verify returned checkpoint signatures)
→ context/manager (inject verified checkpoint summaries)
```

## Data Ownership

| Data | Owner | Readers |
|------|-------|---------|
| Context window (messages) | context/manager | dialect/, stream/ |
| Checkpoint store (sqlite) | memory/store | context/, vault/ |
| Routing decision | dialect/router | context/manager, cmd/ghyll |
| Tool execution | tool/* | context/manager (via results) |
| Config | config/ | all packages |
| ONNX model file | memory/embedder | context/drift |
| Git orphan branch | memory/sync | memory/store |
