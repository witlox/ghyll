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
| gate_floor | runner (`RoutingRequirement.MinTier` of the traversing arrow, encoded as a depth rank 0..3) | int |
| gate_floor_escalate_at_rank | config (`routing.gate_floor_escalate_at_rank`, default 2) | int |
| gate_floor_disabled | config (`routing.gate_floor_disabled`, default false) | bool |

## Decision Table

Rows are evaluated top to bottom. The first matching row wins. The v2 gate-floor rows (1-3) take precedence over per-turn signals because gates.md §7.1 forbids laundering a depth-sensitive clause through an insufficient tier.

| # | Condition | Decision | Target | Needs Compaction | Note |
|---|-----------|----------|--------|-----------------|------|
| 1 | gate_floor out of range (< 0 or > 3) | invalid | (current) | no | Programmer/config error. RejectedFloor is set. |
| 2 | model_locked AND gate_floor active | gate_locked_conflict | (locked) | no | --model lock collides with §7.1. Session must route to operator attestation. |
| 3 | model_locked | none | (locked) | no | The --model flag is absolute. No routing changes occur. |
| 4 | gate_floor active AND no deep_model configured | gate_unsatisfiable | (current) | no | §7.1 unsatisfiable. Session must NOT silently dispatch on the insufficient tier. |
| 5 | gate_floor active AND active != deep_model | escalate (gate-floor) | deep_model | no | The arrow's `MinTier` exceeds `gate_floor_escalate_at_rank`; authoritative over /deep, context-depth, tool-depth. |
| 6 | deep_override AND active == default_model | escalate | deep_model | no | User requested /deep. Temporary override. |
| 7 | backfill_triggered AND active == default_model | escalate | deep_model | no | Drift detected, additional context loaded. |
| 8 | context_depth > threshold AND active == default_model | escalate | deep_model | **yes** | Context too large for fast tier. Compact first. |
| 9 | tool_depth > tool_threshold AND active == default_model | escalate | deep_model | no | Complex multi-tool chain detected. |
| 10 | context_compacted_below < threshold AND active == deep_model AND NOT deep_override AND NOT gate_floor active | de-escalate | default_model | no | Context reduced enough to return to fast tier; blocked while a gate floor is active. |
| 11 | (none of the above) | none | (current) | no | Steady state. Continue on current model. |

"Gate floor active" means `!gate_floor_disabled && gate_floor_escalate_at_rank > 0 && gate_floor >= gate_floor_escalate_at_rank`. The runner consumes the decision in `dialect/router.go` (`Evaluate`, see the `gateFloorActive` predicate at the top of the function); `cmd/ghyll` translates the typed `Reason` field (`gate-floor`, `gate-unsatisfiable`, `gate-locked-conflict`) into the operator-visible event downstream.

The router returns a `RoutingDecision{Action, TargetModel, NeedCompaction, Reason, RejectedFloor}`. When `NeedCompaction` is true, `cmd/ghyll` runs compaction on the current model before executing the handoff. The router never calls compaction itself.

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
