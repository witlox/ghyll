# Package Graph

Ghyll is organized into a set of Go packages with a strict, acyclic dependency graph. All dependencies point downward from the entry points toward leaf packages, with no cycles permitted. The v2 gate-and-arrow surface (`runner/`, `engine/`, `bootstrap/`, `catalogue/`, `gates/concepts/`) layers on top of the v1 dialect / memory / stream foundation; nothing in the v2 layer is imported by the v1 packages.

## Dependency Diagram

```
                                    cmd/ghyll
   +-----------+-----------+-----------+-----------+-----------+-----------+
   |           |           |           |           |           |           |
   v           v           v           v           v           v           v
 modal/    bootstrap/   engine/     runner/    dialect/    context/    stream/
   |           |           |        ^   ^         |           |           |
   |           |           |        |   |         |           |           |
   |           v           v        |   |         v           v           v
   +-----> catalogue/ ----+         |   |        memory/      |        (types)
           ^   ^                    |   |         |           |           ^
           |   |                    |   |         v           v           |
   bootstrap/  +--- gates/concepts  |   |        config/   memory/        |
       |              (embed YAML)  |   |          ^         ^            |
       v                            |   |          |         |            |
   tool/  workflow/  internal/      |   |          |         |            |
     |       |          |           |   |          |         |            |
     v       v          v           v   v          v         v            |
   types/  internal/  (leaves)   (no upward imports)         |            |
                                                              |            |
                          cmd/ghyll-vault                     |            |
                              |                               |            |
                              v                               |            |
                            vault/                            |            |
                            /  |  \                           |            |
                       engine/ |  memory/ --------------------+            |
                               |    |                                      |
                               +----+----> ui/, config/, types/ -----------+
```

The two binaries (`cmd/ghyll` and `cmd/ghyll-vault`) sit at the top. `cmd/ghyll` wires the v1 substrate (dialect / context / stream / tool / memory) together with the v2 gate-and-arrow surface (runner / engine / bootstrap / catalogue / modal). `cmd/ghyll-vault` reuses `engine/` and `memory/` to serve team memory over HTTP. Lower-level packages never import upward.

Key v2 layering rules:

- **`runner/` does not import `bootstrap/`, `engine/`, `catalogue/`, `dialect/`, or `cmd/...`.** It is the runtime kernel; callers wire it. It depends only on `internal/pathglob` and `internal/skipdirs` plus the standard library.
- **`engine/` imports `runner/`** (the persistent store consumes runner record types) but `runner/` does NOT import `engine/`. The Journal observer pattern keeps the direction one-way.
- **`bootstrap/` imports `runner/` and `catalogue/`** to populate the grid and validate clauses; nothing imports `bootstrap/` except `cmd/ghyll`.
- **`catalogue/` embeds `gates/concepts/*.yaml`** via `//go:embed`. The `gates/concepts/` tree is not a Go package; it is a data directory.
- **`internal/{pathglob,safefile,skipdirs}` are leaves** with no project imports.

## Package Reference

### Entry points

| Package | Import path | Purpose | Depends on |
|---------|------------|---------|------------|
| cmd/ghyll | ghyll/cmd/ghyll | CLI entry, session loop, repl, all v2 subcommands, callback wiring | bootstrap, catalogue, cmd/ghyll/modal, config, context, dialect, engine, memory, runner, stream, tool, types, ui, workflow |
| cmd/ghyll/modal | ghyll/cmd/ghyll/modal | Tier 2 verdict modal (TermModal, LineReader, sanitize) | runner |
| cmd/ghyll-vault | ghyll/cmd/ghyll-vault | Vault server entry | config, memory, ui, vault |

### v2 gate-and-arrow surface

| Package | Import path | Purpose | Depends on |
|---------|------------|---------|------------|
| runner | ghyll/runner | Gate-and-arrow runtime: clause evaluation, dispatcher, RunArrow, InsufficientBasisTracker, OperatorBus, AttestationStore, adversarial orchestrator, amendment queue | internal/pathglob, internal/skipdirs |
| engine | ghyll/engine | sqlite-backed persistent store + Journal observer fanout + Replay (sessionstart load) + Recovery (crash reconciliation) | runner |
| bootstrap | ghyll/bootstrap | `ghyll init`: auto-propose, modify rules, orphan-symbol extraction, role-clause parsing, session registry, grid build | catalogue, internal/pathglob, internal/safefile, internal/skipdirs, runner, project root (for embedded role specs) |
| catalogue | ghyll/catalogue | Closed concept vocabulary (per-language bindings); embeds `gates/concepts/*.yaml` | project root (for embedded YAML) |

(`gates/concepts/` ships YAML schemas only; it is not a Go package.)

### v1 substrate

| Package | Import path | Purpose | Depends on |
|---------|------------|---------|------------|
| types | ghyll/types | Shared types: Message, ToolCall, ToolResult | (none -- leaf) |
| config | ghyll/config | TOML loader, model/endpoint mapping, validation | internal/safefile |
| tool | ghyll/tool | Direct OS operations: bash, file, git, grep, edit, glob, web | internal/safefile, types |
| memory | ghyll/memory | Checkpoint store, embedder, hash chain, sync, vault client | internal/safefile |
| dialect | ghyll/dialect | Model-specific functions, router (incl. v2 gate-floor), handoff | config, memory, types |
| stream | ghyll/stream | SSE client, response assembly, terminal renderer | types |
| context | ghyll/context | Context manager, compactor, drift detector, injection detector | types |
| vault | ghyll/vault | Team memory HTTP server, search | engine, memory |

### Support packages

| Package | Import path | Purpose | Depends on |
|---------|------------|---------|------------|
| ui | ghyll/ui | User-facing terminal output (CLI); slog handles diagnostics | (none -- stdlib only) |
| workflow | ghyll/workflow | Project instructions + slash commands loader (reads `.ghyll/`, `.claude/`) | internal/safefile |
| internal/pathglob | ghyll/internal/pathglob | Path-glob matcher used by bootstrap modify rules and the runner orphan scan | (none -- leaf) |
| internal/safefile | ghyll/internal/safefile | Safe file open / read helpers (path-traversal-resistant) | (none -- leaf) |
| internal/skipdirs | ghyll/internal/skipdirs | Default directory skip list for the orphan scanner | (none -- leaf) |

## The types/ Package

The `types/` package is a leaf with no dependencies of its own. It contains the shared types that multiple packages need to pass around:

- **Message** -- a context window entry (role, content, tool calls).
- **ToolCall** -- a structured tool invocation parsed from model output.
- **ToolResult** -- output from tool execution.

These types were originally defined in `context/` and `tool/`, but that created hidden import dependencies. For example, `dialect/` functions accept `[]Message` and return `[]ToolCall`, and `stream/` returns `ToolCall` in responses. If these types lived in `context/`, both `dialect/` and `stream/` would need to import `context/`, contradicting the intended dependency direction.

Extracting them into a dedicated leaf package keeps the graph honest and makes cross-package type sharing explicit.

## Key Constraints

- **types/ is a leaf.** No dependencies. Any package can import it.
- **internal/{pathglob,safefile,skipdirs} are leaves.** No project dependencies. Any package can import them.
- **dialect/ depends on types/, config/, and memory/ only.** It does not depend on context/, runner/, or stream/. Router and handoff functions receive state as arguments rather than importing those packages directly. The router consumes a v2 gate-floor rank as an `int` (not a `runner.DepthRank`) precisely so the package stays near-leaf.
- **context/ depends on types/ only.** It does not depend on dialect/, memory/, or stream/. Cross-cutting flows use callbacks provided by cmd/ghyll (see [Session Loop](session-loop.md)).
- **stream/ depends on types/ only.** It does not depend on dialect/ or context/. It sends messages and returns responses.
- **runner/ does not import any other project package.** It is the v2 runtime kernel; bootstrap/, engine/, and cmd/ghyll wire it from above.
- **engine/ imports runner/ but not the other way around.** The Journal observer pattern keeps persistence one-directional: runner publishes typed events, engine consumes them.
- **bootstrap/ depends on runner/ and catalogue/.** It calls into the runner's grid + clause types and uses the catalogue to validate concept references.
- **catalogue/ depends on nothing project-level** (it embeds `gates/concepts/*.yaml` via `//go:embed`).
- **cmd/ghyll is the composition root.** It is the only package that sees all others. It wires callbacks between packages that cannot import each other (dialect <-> context, runner <-> dialect, modal <-> runner, etc.).

## One Session Per Repository

A repo lockfile (`<repo>/.ghyll.lock`) enforces single-session access. This prevents concurrent git worktree operations on the memory branch, chain file corruption, and double sync goroutines. See [Session Loop](session-loop.md) for details on how the lockfile is managed.
