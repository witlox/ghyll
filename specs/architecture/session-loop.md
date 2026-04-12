# Session Loop

State machine for cmd/ghyll's main loop. This is the composition root —
the only place that sees all packages and orchestrates cross-cutting flows.

## Lifecycle

```
INIT → READY → TURN → (TURN | COMPACT | HANDOFF | BACKFILL) → ... → SHUTDOWN
```

## States

### INIT

```
1. Load config (config/)
2. Load or generate device key (memory/)
3. Open sqlite store (memory/)
4. Initialize embedder if available (memory/)
5. Setup git worktree if needed (memory/sync)
6. Acquire repo lockfile (<repo>/.ghyll.lock) — exit if held
7. Start background sync goroutine (memory/sync)
8. Pull remote checkpoints + public keys (memory/sync)
9. Resolve active model from config + --model flag
10. Build initial routing state
11. Build system prompt via active dialect
12. Initialize context manager (context/)
13. Initialize stream client (stream/)
14. → READY
```

If any step fails fatally (config missing, locked repo), exit with error.
Non-fatal failures (embedder unavailable, sync fails): warn and continue.

### READY

Waiting for user input. Display prompt:
```
ghyll [m25] ~/repos/myproject ▸
```

On user input → TURN.
On `/deep` → set DeepOverride in routing state, → TURN (implicit).
On Ctrl-C or `/exit` → SHUTDOWN.

### TURN

The main execution cycle. Steps in order:

```
1. TOKEN COUNT
   tokens = dialect.TokenCount(context.Messages())
   Pass to routing and context manager.

2. PRE-TURN CHECK (context/manager)
   If tokens > 90% of active model max → COMPACT (return here after)

3. ROUTING DECISION (dialect/router)
   decision = router.Evaluate(routingState, tokens, driftState)
   If decision.ModelLocked → skip
   If decision.NeedCompaction → COMPACT, then HANDOFF
   If decision.Escalate → HANDOFF
   If decision.DeEscalate → HANDOFF

4. SEND (stream/client)
   messages = dialect.BuildMessages(context.Messages(), systemPrompt)
   response = stream.Send(endpoint, messages)

   On StreamError:
     If ContextTooLong → COMPACT (reactive), retry once
     If Retryable → stream retries internally (3x backoff)
     If AllTiersDown or ModelLocked → surface error, → READY
     If fallback eligible (auto-routing) → reformat via dialect.HandoffSummary,
       send to alternate endpoint

   On partial response → surface to user, → READY (user can retry)

5. PARSE TOOL CALLS (dialect/)
   toolCalls = dialect.ParseToolCalls(response.Raw)

6. UPDATE CONTEXT (context/manager)
   Add assistant message + tool calls to context

7. EXECUTE TOOLS (tool/)
   For each tool call:
     result = tool.Execute(call)
     Add tool result to context (via context/manager)
     Increment toolDepth in routing state

   If more tool calls in response → back to step 4 (model continues)

8. CHECKPOINT CHECK (context/manager + memory/)
   If turn % checkpoint_interval == 0:
     Create checkpoint (memory/)
     Run injection signal detection (context/)
     Push to vault if configured (memory/vault_client)
     Queue for git sync (memory/sync)

9. DRIFT CHECK (context/ + memory/)
   If turn % drift_check_interval == 0:
     embedding = memory.Embed(context.Messages())
     latestCheckpoint = memory.LatestCheckpoint(sessionID)
     drift = context.MeasureDrift(embedding, latestCheckpoint)
     If drift.BackfillNeeded → BACKFILL

10. RESET TOOL DEPTH
    If this was a user-initiated turn (not tool continuation):
      routingState.ToolDepth = 0

11. → READY
```

### COMPACT

Entered from TURN (proactive or reactive).

```
1. Select turns to summarize (all except last N)
2. Get compaction prompt from active dialect
3. Build CompactionRequest (context/)
4. Send compaction request to active model (stream/) — separate API call
5. Replace old turns with summary (context/manager)
6. Create compaction checkpoint (memory/)
7. Measure drift post-compaction (context/)
8. Recalculate token count
9. → return to caller (TURN step 2 or HANDOFF)
```

If compaction itself fails (model error on compaction call): surface error.
If reactive and retry also fails: ErrReactiveRetryFail → surface, → READY.

### HANDOFF

Entered from TURN (routing decision).

```
1. If triggered by context depth: COMPACT first (invariant 24b)
2. Create handoff checkpoint on current model (invariant 10)
3. Format context for target model: dialect.HandoffSummary(checkpoint, recentTurns)
4. Replace context window (context/manager)
5. Update routing state (active model, clear/set deep override)
6. Update stream client endpoint
7. Display switch indicator: "⟳ switched to glm5, loaded from checkpoint N"
8. → return to TURN (continue with new model)
```

### BACKFILL

Entered from TURN (drift detected).

```
1. Search local checkpoints by embedding similarity (memory/)
2. If local results insufficient and vault configured:
   Search vault (memory/vault_client)
3. Verify signatures on all candidates (memory/)
4. Discard unverified checkpoints, warn user
5. Select top-k verified checkpoints within token budget
6. Inject summaries into context (context/manager) — additive (invariant 8)
7. Display: "ℹ backfill from checkpoints X, Y"
8. If backfill added significant context:
   Re-evaluate routing (may escalate to deep tier)
9. → return to TURN
```

### SHUTDOWN

```
1. Create final checkpoint if session had activity
2. Final sync push (blocking, with timeout)
3. Release repo lockfile
4. Close sqlite store
5. Close ONNX session
6. Exit
```

## Repo lockfile

One ghyll session per repo. Enforced by `<repo>/.ghyll.lock`:

```go
// Lock acquired in INIT, released in SHUTDOWN.
// Contains PID + timestamp for stale lock detection.
// If lock exists and PID is alive: exit with error.
// If lock exists and PID is dead: warn and acquire.
```

The lockfile is gitignored. It prevents:
- Concurrent git worktree operations on the memory branch
- Concurrent chain file writes
- Double sync goroutines

## Callback wiring

cmd/ghyll passes capabilities to packages that can't import each other:

```go
// context/manager receives:
type ManagerDeps struct {
    TokenCount     func([]types.Message) int          // from dialect
    CompactionCall func(CompactionRequest) (string, error) // wires stream
    CreateCheckpoint func(CheckpointRequest) error     // from memory
    Embed          func([]types.Message) ([]float32, error) // from memory
}

// dialect/router receives:
type RouterInputs struct {
    ContextDepth int
    ToolDepth    int
    ModelLocked  bool
    DeepOverride bool
    ActiveModel  string
    BackfillTriggered bool
    Config       config.RoutingConfig
}
```

This keeps the dependency graph acyclic while allowing cross-cutting flows.
