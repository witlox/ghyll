# Session Loop

The session loop is the state machine at the heart of `cmd/ghyll`. As the composition root, it is the only place that sees all packages and orchestrates the cross-cutting flows between them.

## Lifecycle Overview

```
INIT --> READY --> TURN --> (TURN | COMPACT | HANDOFF | BACKFILL) --> ... --> SHUTDOWN
```

A session progresses through initialization, then alternates between waiting for user input and executing turns. Turns may trigger compaction, model handoff, or memory backfill as needed. The session ends with a clean shutdown that persists final state.

## States

### INIT

Initialization runs the following steps in order:

1. Load configuration (`config/`).
2. Load or generate the device ed25519 key (`memory/`).
3. Open the SQLite checkpoint store (`memory/`).
4. Initialize the ONNX embedder if available (`memory/`).
5. Set up the git worktree if needed (`memory/sync`).
6. Acquire the repo lockfile (`<repo>/.ghyll.lock`) -- exit if already held.
7. Start the background sync goroutine (`memory/sync`).
8. Pull remote checkpoints and public keys (`memory/sync`).
9. Resolve the active model from config and the `--model` flag.
10. Build initial routing state.
11. Build the system prompt via the active dialect.
12. Initialize the context manager (`context/`).
13. Initialize the stream client (`stream/`).
14. Transition to READY.

If any step fails fatally (missing config, locked repo), the process exits with an error. Non-fatal failures (embedder unavailable, sync failure) produce a warning and the session continues with reduced capabilities.

### READY

The session waits for user input, displaying the prompt:

```
ghyll [m25] ~/repos/myproject >
```

- On user input: transition to TURN.
- On `/deep`: set DeepOverride in routing state, then transition to TURN.
- On Ctrl-C or `/exit`: transition to SHUTDOWN.

### TURN

The main execution cycle. Each turn proceeds through these steps:

**1. Token Count.** Count tokens in the current context using the active dialect's tokenizer. Pass the count to routing and context manager.

**2. Pre-turn Check.** If tokens exceed 90% of the active model's maximum context, transition to COMPACT before continuing.

**3. Routing Decision.** Evaluate the routing decision table (see [Routing](routing.md)). Depending on the result:
- Model locked: skip.
- Needs compaction: go to COMPACT, then HANDOFF.
- Escalate or de-escalate: go to HANDOFF.

**4. Send.** Build messages using the active dialect and send them to the model endpoint via the stream client. Error handling:
- `ContextTooLong`: go to COMPACT (reactive), retry once.
- `Retryable`: the stream client retries internally (3 attempts with backoff).
- `AllTiersDown` or `ModelLocked`: surface error, return to READY.
- Fallback eligible (auto-routing): reformat context via `dialect.HandoffSummary`, send to alternate endpoint.
- Partial response: surface to user, return to READY.

**5. Parse Tool Calls.** Extract tool calls from the model response using the active dialect's parser.

**6. Update Context.** Add the assistant message and tool calls to the context window.

**7. Execute Tools.** For each tool call, execute it and add the result to context. Increment the tool depth counter. If the model response contains more tool calls, loop back to step 4.

**8. Checkpoint Check.** At configured intervals (default every 5 turns):
- Create a checkpoint (`memory/`).
- Run injection signal detection (`context/`).
- Push to vault if configured (`memory/vault_client`).
- Queue for git sync (`memory/sync`).

**9. Drift Check.** At configured intervals (default every 5 turns):
- Embed the current context.
- Compare with the latest checkpoint.
- If drift exceeds the threshold, transition to BACKFILL.

**10. Reset Tool Depth.** If this was a user-initiated turn (not a tool continuation), reset the tool depth counter.

**11. Return to READY.**

### COMPACT

Entered from TURN when context is too large (proactive) or when the model rejects the context as too long (reactive).

1. Select turns to summarize (all except the last N).
2. Get the compaction prompt from the active dialect.
3. Build a compaction request (`context/`).
4. Send the compaction request to the active model -- this is a separate API call.
5. Replace old turns with the summary (`context/manager`).
6. Create a compaction checkpoint (`memory/`).
7. Measure drift post-compaction (`context/`).
8. Recalculate the token count.
9. Return to the caller (TURN step 2 or HANDOFF).

If compaction fails (model error on the compaction call), the error is surfaced. If this was a reactive compaction and the retry also fails, `ErrReactiveRetryFail` is surfaced and the session returns to READY.

### HANDOFF

Entered from TURN when the router decides to switch models.

1. If triggered by context depth: run COMPACT first.
2. Create a handoff checkpoint on the current model.
3. Format context for the target model: `dialect.HandoffSummary(checkpoint, recentTurns)`.
4. Replace the context window (`context/manager`).
5. Update routing state (active model, clear/set deep override).
6. Update the stream client endpoint.
7. Display: `switched to glm5, loaded from checkpoint N`.
8. Return to TURN to continue with the new model.

### BACKFILL

Entered from TURN when drift detection finds the conversation has moved significantly from known checkpoints.

1. Search local checkpoints by embedding similarity (`memory/`).
2. If local results are insufficient and vault is configured, search the vault (`memory/vault_client`).
3. Verify signatures on all candidate checkpoints (`memory/`).
4. Discard unverified checkpoints with a warning.
5. Select top-k verified checkpoints within the token budget.
6. Inject summaries into context (`context/manager`) -- this is additive, never replacing existing context.
7. Display: `backfill from checkpoints X, Y`.
8. If backfill added significant context, re-evaluate routing (may escalate to deep tier).
9. Return to TURN.

### SHUTDOWN

1. Create a final checkpoint if the session had activity.
2. Final sync push (blocking, with timeout).
3. Release the repo lockfile.
4. Close the SQLite store.
5. Close the ONNX session.
6. Exit.

## Repo Lockfile

One ghyll session is permitted per repository at a time. This is enforced by `<repo>/.ghyll.lock`:

- The lock is acquired in INIT and released in SHUTDOWN.
- It contains the PID and a timestamp for stale lock detection.
- If the lock exists and the PID is alive: exit with an error.
- If the lock exists and the PID is dead: warn and acquire the lock.

The lockfile is gitignored. It prevents concurrent git worktree operations on the memory branch, concurrent chain file writes, and double sync goroutines.

## Callback Wiring

Because packages like `context/` and `dialect/` cannot import each other, `cmd/ghyll` wires them together using function callbacks:

```go
// context/manager receives:
type ManagerDeps struct {
    TokenCount       func([]types.Message) int
    CompactionCall   func(CompactionRequest) (string, error)
    CreateCheckpoint func(CheckpointRequest) error
    Embed            func([]types.Message) ([]float32, error)
}

// dialect/router receives:
type RouterInputs struct {
    ContextDepth      int
    ToolDepth         int
    ModelLocked       bool
    DeepOverride      bool
    ActiveModel       string
    BackfillTriggered bool
    Config            config.RoutingConfig
}
```

This pattern keeps the dependency graph acyclic while allowing the complex cross-cutting flows that the session loop requires.
