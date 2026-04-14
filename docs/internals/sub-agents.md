# Sub-Agents

Sub-agents are focused model inference calls dispatched by the parent session via the `agent` tool. They run on the fast tier with isolated context and return their findings as a tool result.

## How It Works

1. The parent model calls `agent(task: "...")`.
2. ghyll creates a fresh context with the dialect's system prompt + workflow instructions (no role, no plan mode).
3. A mini turn-loop runs: send to model, parse tool calls, execute tools, repeat.
4. When the model returns without tool calls (or limits are hit), the final answer is returned to the parent.

## Isolation

Sub-agents are intentionally isolated from the parent:

- **No parent history**: the sub-agent sees only the system prompt and task description.
- **No role overlay**: even if the parent is in "analyst" mode, the sub-agent operates with bare instructions.
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
