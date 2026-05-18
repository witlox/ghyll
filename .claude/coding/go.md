# Ghyll — Go Coding Standards

Extends `.claude/guidelines/go.md` with project-specific conventions.
Loaded for: implementer, architect, adversary (implementation review).

## Project shape

- CLI tool (`cmd/ghyll`) + team memory server (`cmd/ghyll-vault`)
- Sandbox-external: SRT / bubblewrap handles security at kernel level
- Memory: append-only, hash-chained, ed25519 signed, git orphan branch sync
- No provider interfaces — each dialect family is concrete functions

## Workspace

| Package | Role |
|---|---|
| `types/` | Shared leaf types (Message, ToolCall, ToolResult) — imports nothing from project (ADR-002) |
| `config/` | TOML loader + validation |
| `tool/` | Direct OS calls (bash, file, git, grep, edit, glob, web) — no wrapper layers |
| `dialect/` | Model-specific code + routing decision table |
| `stream/` | SSE streaming client + terminal renderer |
| `memory/` | Checkpoint store, crypto, sync, embedder |
| `context/` | Compaction + backfill orchestration |
| `workflow/` | Project instructions, roles, slash commands loader |
| `vault/` | Team memory HTTP server |

## Package conventions

### `dialect/`

Each model file exports standalone functions:
`BuildMessages`, `ParseToolCalls`, `SystemPrompt`, `CompactionPrompt`, `TokenCount`.

Router in `router.go` calls dialect functions directly based on config and
tier (ADR-007). Handoff via `GLM5HandoffSummary` / `M25HandoffSummary` creates
a checkpoint summary and reformats for the target dialect.

### `context/`

Single owner of compaction + memory + drift decisions.
Compactor calls dialect-specific compaction prompts (ADR-005 — separate API call).
Drift detector uses `memory/embedder` for cosine similarity.

### `memory/`

- Store: SQLite with append-only checkpoint table
- Hash chain: `Hash = sha256(canonical_content)`, `ParentHash = previous.Hash`
- Signatures: ed25519 sign of `Hash` using developer key from `~/.ghyll/keys/`
- Sync: git operations via `tool/git.go`, targeting orphan branch `ghyll/memory`
- Embedder: ONNX Runtime Go bindings, model lazy-downloaded to `~/.ghyll/models/`
- Embedding bytes excluded from hash (ADR-003)

### `tool/`

No abstraction layer. `bash.go` is `exec.Command("bash", "-c", cmd)` with
timeout and output capture. No permission checks — sandbox handles security.
Tool recursion bounded by depth limit (ADR-004).

### `stream/`

OpenAI-compatible `/v1/chat/completions` with streaming.
SSE parsing, tool call detection, response assembly, markdown terminal rendering.

## Crypto

- Hashing: SHA-256 canonical hash (sorted keys, deterministic encoding)
- Signing: ed25519 from `~/.ghyll/keys/`
- Chain verification: walk Hash → ParentHash, signature-verify each link

## Concurrency

- One session per repo, enforced by lockfile (ADR-006)
- Background sync via `memory/syncloop.go` goroutine
- `context.Context` threaded through every I/O call

## Error handling

- Typed errors from project error taxonomy (`specs/architecture/error-taxonomy.md`)
- Wrap with `fmt.Errorf("dialect.parse: %w", err)` — package-prefixed context
- Recoverable vs fatal classified at boundary

## Domain language

- Type names match `specs/ubiquitous-language.md` exactly
- Full names in public APIs (`Checkpoint`, not `Cp`)
- New term? Check spec; if absent, escalate to analyst

## Anti-patterns

- Adding an interface for a single concrete implementation
- Introducing a wrapper layer over `os` / `exec` / `net/http`
- Bundling the ONNX model (it is lazy-downloaded)
- Modifying the orphan branch from code paths outside `memory/sync.go`
- Globals or `init()` side effects in any package
