# Package Graph

## Dependency direction

All dependencies point downward. No cycles.

```
                      cmd/ghyll
                  /  |  |  |  \  \
                 /   |  |  |   \  \
          dialect/ context/ stream/ tool/ workflow/
             |    \  |   \    |    /       |
             |     \ |    memory/--+        |
             |      \|      |              |
             +-------+------+--------------+---→ config/
             |       |      |
             +-------+------+---→ types/

                    cmd/ghyll-vault
                        |
                      vault/
                      /    \
                 memory/  config/
                    |
                  types/
```

## Package list

| Package | Import path | Purpose | Depends on |
|---------|------------|---------|------------|
| types | ghyll/types | Shared types: Message, ToolCall, ToolResult | (none — leaf) |
| config | ghyll/config | TOML loader, model/endpoint mapping, instruction budget | (none — leaf) |
| tool | ghyll/tool | Direct OS operations: bash, file, git, grep, edit, glob, web | types |
| memory | ghyll/memory | Checkpoint store, embedder, hash chain, sync, vault client | types, config, tool |
| dialect | ghyll/dialect | Model-specific functions, router, handoff, plan mode prompts | types, config |
| stream | ghyll/stream | SSE client, response assembly, terminal renderer | types, config |
| context | ghyll/context | Context manager, compactor, drift detector, injection detector | types, memory, config |
| workflow | ghyll/workflow | Load .ghyll/ instructions, roles, commands; merge global+project | config |
| vault | ghyll/vault | Team memory HTTP server, search | memory, config |
| cmd/ghyll | ghyll/cmd/ghyll | CLI entry, session loop, sub-agent orchestration, wiring | dialect, context, stream, tool, memory, workflow, config, types |
| cmd/ghyll-vault | ghyll/cmd/ghyll-vault | Vault server entry | vault, config |

## types/ package contents

Shared types that multiple packages need to import/return:

- `Message` — context window entry (role, content, tool calls)
- `ToolCall` — structured tool invocation parsed from model output
- `ToolResult` — output from tool execution

These were previously in context/ and tool/, which created hidden import
dependencies. Extracting them to a leaf package eliminates cycles.

## Key constraints

- **types/ is a leaf.** No dependencies. Any package can import it.
- **config/ is a leaf.** No dependencies beyond stdlib.
- **workflow/ depends on config/ only.** It reads files from disk and returns structured data. It does NOT depend on dialect/, context/, or memory/. cmd/ghyll wires workflow output into dialect system prompts.
- **dialect/ depends on types/ and config/.** It does NOT depend on context/, memory/, or stream/. Router and handoff functions receive state as arguments. Plan mode prompts are dialect functions called by cmd/ghyll when plan mode flag is set.
- **context/ depends on types/, memory/, and config/.** It does NOT depend on dialect/ or stream/. Cross-cutting flows use callbacks provided by cmd/ghyll (see session-loop.md).
- **stream/ depends on types/ and config/.** It does NOT depend on dialect/ or context/. It sends messages and returns responses.
- **tool/ depends on types/ only.** New tools (edit_file, glob, web_fetch, web_search) follow the same pattern as existing tools — direct OS calls, context.Context for timeout, return types.ToolResult.
- **cmd/ghyll is the composition root.** The only package that sees all others. It wires callbacks between packages that cannot import each other. Sub-agent orchestration lives here — it creates a mini session with its own context/manager and stream/client.

## Why types/ is separate

The architect originally placed Message in context/ and ToolCall alongside it.
Adversary review found that dialect/ functions take `[]Message` and return
`[]ToolCall`, and stream/ returns `ToolCall` in responses. This would require
dialect/ → context/ and stream/ → context/ imports, contradicting the stated
graph and creating hidden coupling.

Extracting to types/ keeps the graph honest and makes cross-package type
sharing explicit.

## One session per repo

A repo lockfile (`<repo>/.ghyll.lock`) enforces single-session access.
This prevents concurrent git worktree operations, chain file corruption,
and double sync goroutines. See session-loop.md for details.
