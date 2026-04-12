# Context-Depth Routing

Ghyll automatically selects which model to use based on the current session state. The router, implemented in `dialect/router.go`, evaluates a decision table on every turn and returns a routing decision. It only decides -- the actual compaction and handoff are orchestrated by `cmd/ghyll`.

## Inputs

The router evaluates the following inputs each turn:

| Input | Source | Type |
|-------|--------|------|
| context_depth | context/manager (token count) | int |
| tool_depth | context/manager (sequential tool calls) | int |
| model_locked | cmd/ghyll (--model flag) | bool |
| deep_override | cmd/ghyll (/deep command) | bool |
| active_model | routing state | string |
| backfill_triggered | context/drift | bool |
| context_depth_threshold | config (default 32000) | int |
| tool_depth_threshold | config (default 5) | int |
| context_compacted_below | context/manager (post-compaction depth) | int |

## Decision Table

Rows are evaluated top to bottom. The first matching row wins.

| # | Condition | Decision | Target | Needs Compaction | Note |
|---|-----------|----------|--------|-----------------|------|
| 1 | model_locked | none | (locked) | no | The --model flag is absolute. No routing changes occur. |
| 2 | deep_override AND active == "m25" | escalate | glm5 | no | User requested /deep. Temporary override. |
| 3 | backfill_triggered AND active == "m25" | escalate | glm5 | no | Drift detected, additional context loaded. |
| 4 | context_depth > threshold AND active == "m25" | escalate | glm5 | **yes** | Context too large for fast tier. Compact first. |
| 5 | tool_depth > tool_threshold AND active == "m25" | escalate | glm5 | no | Complex multi-tool chain detected. |
| 6 | context_compacted_below < threshold AND active == "glm5" AND NOT deep_override | de-escalate | m25 | no | Context reduced enough to return to fast tier. |
| 7 | (none of the above) | none | (current) | no | Steady state. Continue on current model. |

The router returns a `RoutingDecision{Action, TargetModel, NeedCompaction}`. When `NeedCompaction` is true, `cmd/ghyll` runs compaction on the current model before executing the handoff. The router never calls compaction itself.

## State Transitions

```
Session start --> M2.5 (default)

M2.5 --> GLM-5:
  - context_depth > threshold (after compaction on M2.5)
  - tool_depth > threshold
  - /deep command
  - backfill triggered

GLM-5 --> M2.5:
  - compaction reduces context below threshold AND no /deep override
  - (automatic -- no explicit /fast command needed)

Any --> locked:
  - --model flag at startup
  - Once locked, no transitions until session ends
```

## Handoff Protocol

Every model switch follows this sequence:

1. Create a checkpoint on the current model (captures pre-switch state).
2. If escalating due to context depth: compact first on the current model.
3. Call the target dialect's `HandoffSummary(checkpoint, recentTurns)` to format context for the new model.
4. Replace the context window with the handoff summary.
5. Update routing state (active model, deep override flag).
6. The next stream request goes to the new model's endpoint.

## Tier Fallback

Tier fallback is distinct from routing. It is handled by `stream/`, not `dialect/router`, and is orthogonal to routing decisions:

- Only active when auto-routing is enabled (no --model lock).
- Triggers after 3 retries on the active endpoint fail.
- Uses `dialect/handoff` to reformat context for the alternate tier.
- Does not update routing state permanently -- routing still evaluates normally on the next turn.
