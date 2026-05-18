# Session Loop

State machine for cmd/ghyll's main loop. This is the composition root —
the only place that sees all packages and orchestrates cross-cutting flows.

## Lifecycle

```
INIT → READY → TURN → (TURN | COMPACT | HANDOFF | BACKFILL | SUB-AGENT) → ... → SHUTDOWN
```

## States

### INIT

```
1.  Load config (config/)
2.  Load or generate device key (memory/)
3.  Open sqlite store (memory/)
4.  Initialize embedder if available (memory/)
5.  Setup git worktree if needed (memory/sync)
6.  Acquire repo lockfile (<repo>/.ghyll.lock) — exit if held
7.  Start background sync goroutine (memory/sync)
8.  Pull remote checkpoints + public keys (memory/sync)
9.  Resolve active model from config + --model flag
10. Build initial routing state (PlanMode = false)
11. Load workflow (workflow/):
    a. Scan <repo>/.ghyll/ — if absent, try fallback folders (.claude/, etc.)
    b. Load global ~/.ghyll/ instructions, roles, commands
    c. Merge: global first, project appended (invariant 47)
    d. Check instruction budget (config), truncate with warning if exceeded (invariant 48)
12. Build system prompt: dialect base + workflow instructions (no role initially)
13. If --resume flag: load last session checkpoint, prepare backfill (invariant 42, 43)
14. Initialize context manager (context/)
    If --resume: inject checkpoint summary as system-level backfill
15. Initialize stream client (stream/)
16. → READY
```

If any step fails fatally (config missing, locked repo), exit with error.
Non-fatal failures (embedder unavailable, sync fails, no workflow folder): warn and continue.

### READY

Waiting for user input. Display prompt:
```
ghyll [m25] ~/repos/myproject ▸
```

On user input → TURN.
On `/deep` → set DeepOverride in routing state, → TURN (implicit).
On `/plan` → set PlanMode in routing state, rebuild system prompt with plan mode overlay.
On `/fast` → clear DeepOverride AND PlanMode, rebuild system prompt.
On `/status` → display model, turn count, tool depth, plan mode state, active role.
On `/<name>` → look up in workflow commands, inject as user message → TURN.
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

7. EXECUTE TOOLS (tool/ or cmd/ghyll for special tools)
   For each tool call:
     If call.Name == "agent" → SUB-AGENT (return result to context)
     If call.Name == "enter_plan_mode" → set PlanMode, rebuild system prompt, result = "plan mode activated"
     If call.Name == "exit_plan_mode" → clear PlanMode, rebuild system prompt, result = "plan mode deactivated"
     Else: result = tool.Execute(call)  // bash, read_file, write_file, edit_file, grep, glob, git, web_fetch, web_search
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

### SUB-AGENT

Entered from TURN (model calls agent tool).

```
1. Parse task description from tool call arguments
2. Resolve sub-agent model from config (default: fast tier)
3. Build sub-agent system prompt:
   dialect.SystemPrompt(workdir) + workflow.Instructions
   NO role overlay (invariant 38)
   NO plan mode overlay (sub-agents are focused, fast-tier tasks — plan mode adds unnecessary reasoning overhead)
4. Create sub-agent context manager (isolated, own instance)
   Inject system prompt + task description as user message
5. Create sub-agent stream client (target model endpoint)
6. Sub-agent turn loop:
   a. Send to model (stream/)
   b. Parse tool calls (dialect/)
   c. Execute tools — same as parent EXCEPT:
      - "agent" tool NOT available (invariant 41: depth 1 only)
      - "enter_plan_mode" / "exit_plan_mode" NOT available
   d. Add results to sub-agent context
   e. Track cumulative tokens (invariant 41a: token budget)
   f. If tokens > budget OR turns > max_turns → terminate with partial result
   g. If model returns stop → collect final answer
   h. Else → repeat from (a)
7. Return sub-agent final answer as tool result to parent context
8. → return to TURN step 7 (continue parent tool execution)
```

If sub-agent model is unreachable: return error as tool result.
Sub-agent has no checkpoint creation, no drift detection, no sync.

Design choice: sub-agent execution is SYNCHRONOUS — the parent session
blocks while the sub-agent runs, same as a bash tool call that runs `make test`.
The user sees sub-agent progress via terminal output but cannot type until
the sub-agent completes. A wall-clock timeout (config.SubAgent.TimeoutSeconds,
default 300s) bounds total sub-agent execution time. On timeout, the sub-agent
is terminated and returns a partial result.

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
    TokenCount     func([]types.Message) int               // from dialect
    CompactionCall func(CompactionRequest) (string, error)  // wires stream
    CreateCheckpoint func(CheckpointRequest) error          // from memory
    Embed          func([]types.Message) ([]float32, error) // from memory
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
// PlanMode is session state in cmd/ghyll, NOT passed to router (invariant 36).

// workflow/ is called at INIT — returns workflow.Workflow struct.
// cmd/ghyll reads Workflow.Instructions, Workflow.Roles, Workflow.Commands
// and wires them into the system prompt and REPL command dispatcher.
// No callbacks needed — workflow/ is a pure loader.
```

This keeps the dependency graph acyclic while allowing cross-cutting flows.

## System prompt composition

The system prompt is built by cmd/ghyll from multiple sources:

```
[dialect base prompt]        ← dialect.SystemPrompt(workdir)
[workflow instructions]      ← workflow.Instructions (global + project, project last)
[role overlay]               ← workflow.Roles[activeRole] (if any)
[plan mode overlay]          ← dialect.PlanModePrompt() (if PlanMode active)
```

Total bounded by instruction budget (invariant 48). The dialect base prompt
is NOT counted against the instruction budget — only workflow content is.

Budget enforcement is two-phase (done by cmd/ghyll, not workflow/):
1. If project instructions alone exceed budget → truncate project from end, drop global entirely
2. If combined exceeds but project alone fits → drop global (partially or fully) until combined fits
3. If combined fits → use both, no truncation
This preserves project instructions (authoritative) over global instructions.

On role switch: replace role overlay portion, keep everything else.
On plan mode toggle: append/remove plan mode overlay.
On handoff: rebuild with target dialect's base + same workflow + same role + plan mode flag.
