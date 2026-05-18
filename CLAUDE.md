# Ghyll

A coding agent that optimizes for **correctness over speed and breadth**,
and pays for it in **friction**. Self-hosted, open-weight, sandbox-only.

ghyll is correct for a narrow class of work — novel architecture,
correctness-critical systems, long-horizon projects where a defect
reaching deployment is expensive. ghyll is wrong for CRUD, migrations,
glue code, and rapid prototyping. Stating the second half is the
position.

## Project State

**Phase**: v1 implemented (this repo). v2 design complete (5 operator-
decision rounds D1–D44; 4 cold-read validation passes; 82 findings
resolved). v2 implementation about to begin. v1 specs and acceptance
suite removed — v2 specs/features being written.

- **v1** — dialects, drift-aware memory, streaming, sandbox-only
  execution. Continuity infrastructure for v2; will be retired as v2
  implementation reaches feature parity.
- **v2** — gate-and-arrow enforcement, fixed role set, integrator
  feedback cycle, typed clause/finding/pass state machines, init
  with auto-propose, per-arrow adversarial phase. Design in
  [`specs/direction/`](specs/direction/); implementation specs at
  `specs/` (root) under active development.

Use the diamond workflow (`.claude/CLAUDE.md`) for v1 bugfixes and any
non-v2 work. The `.claude/roles/*.md` files are Claude Code's roles for
building ghyll; they are NOT the runtime roles ghyll will embed (those
live in `specs/direction/roles/`).

## Build

```bash
make                  # lint + test + build
make build-bin        # versioned binaries to bin/
make test             # unit + acceptance tests
make test-race        # with race detector
make coverage-check   # enforce 50% threshold
make docs-serve       # preview mdbook locally
make embedder         # download ONNX embedding model
```

Requires: Go 1.25+, ONNX Runtime (optional, for drift detection).

## Project Structure

```
cmd/
  ghyll/              CLI entry point + session loop
  ghyll-vault/        team memory server entry point
config/               TOML loader + validation
types/                shared types (Message, ToolCall, ToolResult) — leaf package
tool/                 direct OS operations (bash, file, git, grep, edit, glob, web)
workflow/             project instructions, roles, slash commands loader
stream/               SSE streaming client + terminal renderer
dialect/              model-specific code + routing decision table
  router.go           context-depth routing
  glm5.go             GLM-5 dialect
  minimax_m25.go      MiniMax M2.5 dialect
  parse.go            shared OpenAI tool call parser
memory/               checkpoint store + crypto + sync + embedder
  store.go            sqlite + hash chain
  crypto.go           canonical hash, ed25519 sign/verify, chain verification
  keys.go             device key management
  embedder.go         ONNX embedding inference
  sync.go             git orphan branch sync
  syncloop.go         background sync goroutine
  vault_client.go     HTTP client for ghyll-vault
context/              unified context manager
  manager.go          compaction + backfill orchestration
  drift.go            cosine similarity drift detection
  injection.go        prompt injection signal detection
vault/                team memory HTTP server
specs/direction/      v2 design (gate schema, role contracts, component specs, decisions history)
specs/                v2 implementation specs (domain-model, invariants, features) — being written
docs/                 mdbook documentation site
scripts/              scenario verification tooling
```

## Conventions

- Go: standard gofmt, no globals, context.Context threaded through all I/O
- No provider interfaces — each dialect family is concrete functions (minimax.go, glm.go)
- Tools are direct OS calls — no permission layer (sandbox handles security)
- Commits: conventional commits (feat:, fix:, docs:, test:, ci:)
- Tests: TDD (red-green-refactor), TestScenario_* naming, godog for acceptance
- Memory checkpoints: append-only, hash-chained, ed25519 signed
- The git orphan branch `ghyll/memory` is never merged into code branches
- CI: build -> validate -> test pipeline, 50% coverage threshold

## Key Design Decisions

- [ADR-001: Architecture](docs/decisions/001-architecture.md) — Go, no interfaces, orphan branch, Merkle DAG
- [ADR-002: Types leaf package](docs/decisions/002-types-leaf-package.md) — import cycle prevention
- [ADR-003: Embedding excluded from hash](docs/decisions/003-embedding-excluded-from-hash.md) — float portability
- [ADR-004: Tool depth limit](docs/decisions/004-tool-depth-limit.md) — unbounded recursion guard
- [ADR-005: Compaction separate API call](docs/decisions/005-compaction-separate-api-call.md) — context overflow prevention
- [ADR-006: One session per repo](docs/decisions/006-one-session-per-repo.md) — lockfile concurrency
- [ADR-007: Tier-based routing](docs/decisions/007-tier-based-routing.md) — decouple routing from model names

v2 design intent (not yet ADRs, not yet code):

- [Direction](specs/direction/direction.md) — pivot rationale, what changes, §7 hypothesis caveat
- [Gates schema](specs/direction/gates.md) — evaluation types, depth types, arrows, routing, attestation
- [Analyst role contract](specs/direction/roles/analyst.md) — the one role reconciled to the schema
- [Build notes](specs/direction/build-notes.md) — what is designed vs. deliberately not yet built

## Running

```bash
ghyll run .                           # start session, auto-detect model
ghyll run . --model glm5              # force GLM-5
ghyll run . --resume                  # resume from last session's checkpoint
ghyll memory search "race condition"  # search checkpoints
ghyll memory sync                     # manual sync
ghyll memory log                      # show checkpoint chain
ghyll config show                     # display configuration
ghyll version                         # print version
```

## In-Session Commands

| Command | Effect |
|---------|--------|
| `/deep` | Temporarily switch to GLM-5 |
| `/fast` | Restore auto-routing, clear plan mode |
| `/plan` | Enter plan mode (deeper reasoning) |
| `/status` | Show model, turn count, tool depth, plan mode |
| `/exit` | End session (creates final checkpoint) |
| `/<name>` | Run user-defined slash command from .ghyll/commands/ |
