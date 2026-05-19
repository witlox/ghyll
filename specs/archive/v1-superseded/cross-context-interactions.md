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
    ├──→ config/ ──┘
    │              │
    └──→ workflow/  ──→ config/  (new: loads .ghyll/, roles, commands)
                   │
             all ──→ types/  (leaf)

cmd/ghyll-vault ──→ vault/ ──→ memory/
                               └──→ config/
```

Note: workflow/ is a new package responsible for loading and merging
project instructions, roles, and slash commands. It depends on config/
for instruction budget settings. cmd/ghyll orchestrates workflow loading
at session start and role switching during the session.

The tool/ package gains: edit_file, glob, web_fetch, web_search.
Sub-agent execution lives in cmd/ghyll (session) — it creates a mini
session with its own context/manager and stream/client instances.

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
→ memory/checkpoint (create compaction + handoff checkpoint; includes plan_mode flag in metadata)
→ dialect/handoff (format compacted context for target model)
→ cmd/ghyll (if plan mode active: apply target dialect's planning instructions to new system prompt)
→ context/manager (replace context with handoff context)
→ stream/client (next request goes to new endpoint)
```
Note: plan mode flag travels through the handoff checkpoint metadata.
The target dialect's planning instructions replace the source dialect's.

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

### Flow 9: Sub-agent execution
```
dialect/ (model returns tool call: agent(task="..."))
→ cmd/ghyll (session recognizes agent tool)
→ cmd/ghyll (create sub-agent):
  → config/ (read sub-agent defaults: model=m25, max_turns=20)
  → cmd/ghyll (load workflow: project instructions, no role unless specified)
  → dialect/ (build system prompt for sub-agent's model + instructions)
  → context/manager (new, isolated — system prompt + task only)
  → stream/client (connect to sub-agent's model endpoint)
→ cmd/ghyll (sub-agent turn-loop):
  → stream/client (send to model)
  → dialect/ (parse tool calls)
  → tool/ (execute — same tools minus agent)
  → context/manager (update sub-agent context)
  → [repeat until model returns stop or max_turns reached]
→ cmd/ghyll (collect final answer from sub-agent)
→ context/manager (parent: inject result as tool result)
→ cmd/ghyll (parent session continues)
```
Note: sub-agent has its own context/manager instance. Parent's context is untouched
during sub-agent execution. Sub-agent cannot call the agent tool (depth 1 only).

### Flow 10: Session resume
```
cmd/ghyll (--resume flag detected)
→ memory/store (query: most recent checkpoint where reason="shutdown" and repo=current)
→ memory/store (return checkpoint summary, metadata, active_model)
→ dialect/ (format checkpoint summary as backfill message for target model)
→ context/manager (inject summary as system-level backfill)
→ cmd/ghyll (session starts normally with backfilled context)
```

### Flow 11: Web fetch with Tarn retry
```
dialect/ (model returns tool call: web_fetch(url="..."))
→ tool/web (attempt HTTP GET)
→ tool/web (connection error — Tarn may be blocking)
→ tool/web (retry 1: wait 1s, attempt again)
→ tool/web (retry 2: wait 2s, attempt again)
→ tool/web (retry 3: wait 4s, attempt again)
→ tool/web (all retries failed → return error: "domain not reachable")
  OR
→ tool/web (retry N succeeds → parse HTML to markdown → return content)
```
Note: 4xx errors are not retried. Only connection errors and 5xx trigger retry.

### Flow 12: Workflow loading at session start
```
cmd/ghyll (session initialization)
→ cmd/ghyll (check <repo>/.ghyll/ exists)
  If yes: load instructions.md, roles/, commands/
  If no: check <repo>/.claude/ (fallback)
  If no: no workflow — bare dialect prompt
→ cmd/ghyll (check ~/.ghyll/ for global instructions, roles, commands)
→ cmd/ghyll (merge: global first, project overlays — project wins on conflict)
→ config/ (check instruction budget)
→ cmd/ghyll (truncate if over budget, warn user)
→ dialect/ (prepend instructions to system prompt)
→ context/manager (mark instructions as system-level, exempt from compaction)
```

### Flow 13: Role switch
```
dialect/ (model determines role switch needed based on workflow router)
→ cmd/ghyll (session updates active role)
→ cmd/ghyll (load role file from workflow)
→ dialect/ (replace role overlay portion of system prompt)
→ context/manager (system prompt updated — no compaction, no checkpoint)
→ cmd/ghyll (display "Switching to [role]")
```

### Flow 14: Slash command execution
```
cmd/ghyll (user types "/name" in REPL)
→ cmd/ghyll (check: is it a built-in command? /exit, /deep, /fast, /status, /plan)
  If built-in: execute built-in handler
  If not: check workflow commands/ for "name.md"
→ cmd/ghyll (load command file content)
→ context/manager (inject content as user message)
→ [normal turn flow from Flow 1]
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
| Plan mode flag | cmd/ghyll (session) | dialect/, context/ |
| Sub-agent execution | cmd/ghyll (session) | stream/, dialect/, context/, tool/ |
| Workflow files (.ghyll/) | cmd/ghyll (loader) | dialect/, config/ |
| Active role overlay | cmd/ghyll (session) | dialect/ |
| Slash command defs | cmd/ghyll (loader) | — |
| Instruction budget | config/ | cmd/ghyll, dialect/ |
