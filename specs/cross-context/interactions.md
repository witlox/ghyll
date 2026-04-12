# Cross-Context Interactions

## Package Dependency Graph

```
cmd/ghyll ──→ dialect/ ──→ config/
    │              │           │
    ├──→ context/ ─┼──→ memory/ ──→ tool/
    │              │       │
    ├──→ stream/ ──┤       ├──→ config/
    │              │       │
    ├──→ tool/ ────┤       └──→ types/
    │              │
    └──→ config/ ──┘
                   │
             all ──→ types/  (leaf)

cmd/ghyll-vault ──→ vault/ ──→ memory/
                               └──→ config/
```

See `specs/architecture/package-graph.md` for the authoritative graph.

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
→ context/manager (trigger compaction on current model first)
→ dialect/ (get CompactionPrompt for current model)
→ stream/client (separate API call: turns-to-summarize + compaction prompt)
→ context/manager (replace old turns with summary)
→ memory/checkpoint (create compaction + handoff checkpoint)
→ dialect/handoff (format compacted context for target model)
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

### Flow 6: Stream failure with tier fallback (auto-routing only)
```
stream/client (send request to active model endpoint)
→ stream/client (receive 5xx or connection error)
→ stream/client (retry with exponential backoff: 1s, 2s, 4s)
→ stream/client (3 failures → check if auto-routing is active)
  If --model flag set: surface error, session stays open
  If auto-routing:
→ dialect/router (select alternate tier)
→ dialect/handoff (reformat context for alternate dialect)
→ stream/client (send to alternate endpoint)
→ cmd/ghyll (display fallback warning)
```

### Flow 7: Proactive compaction
```
context/manager (before each turn, check token count)
→ context/manager (>90% of max → trigger compaction)
→ context/manager (select turns to summarize: all except last 3)
→ dialect/ (get CompactionPrompt for active model)
→ stream/client (separate API call: compaction prompt + turns-to-summarize only)
→ context/manager (replace old turns with returned summary)
→ memory/checkpoint (create checkpoint of pre-compaction state)
→ context/drift (measure drift after compaction)
```
Note: context/manager orchestrates. dialect/ provides the prompt,
stream/client makes the call. dialect/ and stream/ do not call each other.

### Flow 8: Reactive compaction (fallback)
```
stream/client (model rejects with context_length_exceeded)
→ context/manager (trigger compaction)
→ [same as Flow 7 from dialect/ onward]
→ stream/client (retry original request once with compacted context)
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
