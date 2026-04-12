# Package Graph

Ghyll is organized into a set of Go packages with a strict, acyclic dependency graph. All dependencies point downward from the entry points toward leaf packages, with no cycles permitted.

## Dependency Diagram

```
                    cmd/ghyll
                   /  |  |  \
                  /   |  |   \
           dialect/ context/ stream/  tool/
              |    \  |   \    |     /
              |     \ |    memory/--+
              |      \|      |
              +-------+------+----> config/
              |       |      |
              +-------+------+----> types/

                    cmd/ghyll-vault
                        |
                      vault/
                      /    \
                 memory/  config/
                    |
                  types/
```

The two binaries (`cmd/ghyll` and `cmd/ghyll-vault`) sit at the top. Each depends on the packages it needs to wire together, but lower-level packages never import upward.

## Package Reference

| Package | Import path | Purpose | Depends on |
|---------|------------|---------|------------|
| types | ghyll/types | Shared types: Message, ToolCall, ToolResult | (none -- leaf) |
| config | ghyll/config | TOML loader, model/endpoint mapping | (none -- leaf) |
| tool | ghyll/tool | Direct OS operations: bash, file, git, grep | types |
| memory | ghyll/memory | Checkpoint store, embedder, hash chain, sync, vault client | types, config, tool |
| dialect | ghyll/dialect | Model-specific functions, router, handoff | types, config |
| stream | ghyll/stream | SSE client, response assembly, terminal renderer | types, config |
| context | ghyll/context | Context manager, compactor, drift detector, injection detector | types, memory, config |
| vault | ghyll/vault | Team memory HTTP server, search | memory, config |
| cmd/ghyll | ghyll/cmd/ghyll | CLI entry, session loop, wiring | dialect, context, stream, tool, memory, config, types |
| cmd/ghyll-vault | ghyll/cmd/ghyll-vault | Vault server entry | vault, config |

## The types/ Package

The `types/` package is a leaf with no dependencies of its own. It contains the shared types that multiple packages need to pass around:

- **Message** -- a context window entry (role, content, tool calls).
- **ToolCall** -- a structured tool invocation parsed from model output.
- **ToolResult** -- output from tool execution.

These types were originally defined in `context/` and `tool/`, but that created hidden import dependencies. For example, `dialect/` functions accept `[]Message` and return `[]ToolCall`, and `stream/` returns `ToolCall` in responses. If these types lived in `context/`, both `dialect/` and `stream/` would need to import `context/`, contradicting the intended dependency direction.

Extracting them into a dedicated leaf package keeps the graph honest and makes cross-package type sharing explicit.

## Key Constraints

- **types/ is a leaf.** No dependencies. Any package can import it.
- **dialect/ depends on types/ and config/ only.** It does not depend on context/, memory/, or stream/. Router and handoff functions receive state as arguments rather than importing those packages directly.
- **context/ depends on types/, memory/, and config/.** It does not depend on dialect/ or stream/. Cross-cutting flows use callbacks provided by cmd/ghyll (see [Session Loop](session-loop.md)).
- **stream/ depends on types/ and config/.** It does not depend on dialect/ or context/. It sends messages and returns responses.
- **cmd/ghyll is the composition root.** It is the only package that sees all others. It wires callbacks between packages that cannot import each other.

## One Session Per Repository

A repo lockfile (`<repo>/.ghyll.lock`) enforces single-session access. This prevents concurrent git worktree operations on the memory branch, chain file corruption, and double sync goroutines. See [Session Loop](session-loop.md) for details on how the lockfile is managed.
