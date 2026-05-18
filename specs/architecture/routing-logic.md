# Routing Logic

Decision table, not prose. Evaluated by dialect/router.go.

## Inputs

| Input | Source | Type |
|-------|--------|------|
| context_depth | context/manager (token count) | int |
| tool_depth | context/manager (sequential tool calls) | int |
| model_locked | cmd/ghyll (--model flag) | bool |
| deep_override | cmd/ghyll (/deep command) | bool |
| active_model | routing state | string |
| backfill_triggered | context/drift | bool |
| context_depth_threshold | config | int (default 32000) |
| tool_depth_threshold | config | int (default 5) |
| context_compacted_below | context/manager (post-compaction depth) | int |

## Decision table

Rows evaluated top to bottom. First match wins. The router only *decides* —
cmd/ghyll orchestrates actions (compaction, handoff). See session-loop.md.

| # | Condition | Decision | Target | NeedCompaction | Note |
|---|-----------|----------|--------|----------------|------|
| 1 | model_locked | none | (locked) | false | Invariant 11: absolute |
| 2 | deep_override AND active == "m25" | escalate | glm5 | false | Invariant 11a: temporary |
| 3 | backfill_triggered AND active == "m25" | escalate | glm5 | false | Drift scenario |
| 4 | context_depth > threshold AND active == "m25" | escalate | glm5 | **true** | Invariant 24b |
| 5 | tool_depth > tool_threshold AND active == "m25" | escalate | glm5 | false | Tool complexity |
| 6 | context_compacted_below < threshold AND active == "glm5" AND NOT deep_override | de_escalate | m25 | false | Auto revert |
| 7 | (none of the above) | none | (current) | false | Steady state |

The router returns `RoutingDecision{Action, TargetModel, NeedCompaction}`.
When NeedCompaction is true, cmd/ghyll runs compaction on the current model
before executing the handoff. The router never calls compaction itself.

## State transitions

```
Session start → M2.5 (default)

M2.5 → GLM-5:
  - context_depth > threshold (after compaction on M2.5)
  - tool_depth > threshold
  - /deep command
  - backfill triggered

GLM-5 → M2.5:
  - compaction reduces context below threshold AND no /deep override
  - (no explicit /fast — auto-routing handles it)

Any → locked:
  - --model flag at startup
  - Once locked, no transitions until session ends
```

## Handoff protocol

On every model switch (invariant 10):

1. Create checkpoint on current model (captures pre-switch state)
2. If escalating due to context depth: compact first on current model (invariant 24b)
3. Call target dialect's HandoffSummary(checkpoint, recentTurns)
4. Replace context window with handoff summary
5. Update routing state (active_model, deep_override if applicable)
6. Next stream request goes to new endpoint

## Tier fallback (not routing)

Tier fallback is handled by stream/, not dialect/router. It is orthogonal to routing:

- Only active when auto-routing is on (invariant 20a)
- Triggers after 3 retries on the active endpoint
- Uses dialect/handoff to reformat context for alternate tier
- Does NOT update routing state permanently — routing still evaluates normally on next turn
