# Sub-Agents

Sub-agents are how the model delegates a self-contained sub-task ("go read these three files and summarize") without consuming the parent session's context. You don't trigger them; the model calls the `agent` tool on its own. The output lands in your transcript as a tool-result, the same shape as any other tool call --- you'll see the sub-agent's streaming output rendered inline, followed by the final answer when it returns. Sub-agents are useful when the parent's context is already large or when the task is narrow enough that the parent doesn't need the intermediate steps.

Sub-agents are focused model inference calls dispatched by the parent session via the `agent` tool. They run on the fast tier with isolated context and return their findings as a tool result.

## How It Works

1. The parent model calls `agent(task: "...")`.
2. ghyll creates a fresh context with the dialect's system prompt + workflow instructions (no plan mode).
3. A mini turn-loop runs: send to model, parse tool calls, execute tools, repeat.
4. When the model returns without tool calls (or limits are hit), the final answer is returned to the parent.

## Isolation

Sub-agents are intentionally isolated from the parent:

- **No parent turn history**: the sub-agent sees only the system prompt and the task description --- not the parent session's prior messages or tool results.
- **Workflow instructions ARE inherited**: the parent's `GlobalInstructions` and `ProjectInstructions` are appended to the sub-agent's system prompt (`cmd/ghyll/subagent.go` lines 78-86). The sub-agent runs against the same project conventions as the parent.
- **No "role" state to inherit**: per [ADR-008](../decisions/008-v2-fixed-roles-deprecate-runtime-workflow-roles.md) ghyll has no runtime role-overlay layer in the system prompt — the four diamond roles are contracts attached to arrows, not modes the session is "in". A sub-agent is dispatched by the model from whatever arrow's pass is currently open in the parent; it carries no role tag of its own.
- **No plan mode**: sub-agents are fast, focused tasks --- reasoning overhead is unnecessary.
- **No checkpoints**: sub-agent turns are not checkpointed or synced.
- **No drift detection**: no embedding, no backfill.

## Limits

| Limit | Default | Config key |
|-------|---------|------------|
| Max turns | 20 | `sub_agent.max_turns` |
| Token budget | 50,000 | `sub_agent.token_budget` |
| Wall-clock timeout | 300s | `sub_agent.timeout_seconds` |

When any limit is hit, the sub-agent terminates and returns a partial result describing what was accomplished.

## Available Tools

Sub-agents have access to 9 of the 12 tools:

| Available | Excluded |
|-----------|----------|
| bash, read_file, write_file, edit_file, git, grep, glob, web_fetch, web_search | agent, enter_plan_mode, exit_plan_mode |

The `agent` tool is excluded to prevent recursive sub-agent spawning (depth 1 only).

## Design Choice: Synchronous Execution

Sub-agents run synchronously --- the parent session blocks during execution. This is the same model as the `bash` tool (a long-running `make test` also blocks). The wall-clock timeout prevents indefinite blocking.
