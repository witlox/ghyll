# Ghyll

A purpose-built coding agent CLI, hyper-optimized for a small set of self-hosted open-weight models. Runs inside a Tarn sandbox. Uses git-native memory with Merkle DAG integrity.

## What This Is

- A Go CLI (`ghyll`) that provides Claude Code-style agentic coding against self-hosted models
- Model-specific dialect modules — no provider abstraction layer, no generic adapter pattern
- Context-depth routing: automatically escalates from fast model to deep model based on task complexity
- Drift-aware memory with vector embeddings, stored on a git orphan branch with hash-chain integrity
- Always-yolo tool execution — Tarn handles sandboxing at the kernel level
- A companion service (`ghyll-vault`) for team memory search (optional, shares packages with CLI)

## What This Is Not

- Not a general-purpose LLM client (use LiteLLM, OpenCode, etc.)
- Not model-agnostic — adding a model means writing a new dialect file and recompiling
- Not a sandbox — that's Tarn's job
- Not a chat interface — this is a tool-calling coding agent

## Supported Models

- **MiniMax M2.5** (230B/10B active, 1M context) — fast tier, routine coding
- **GLM-5** (744B/40B active, 200K context) — deep tier, complex reasoning
- **Kimi K2** (1T/32B active, 256K context) — future dialect, not yet implemented

## Build

```bash
make              # build both ghyll and ghyll-vault
make test         # run all tests
make clean        # remove build artifacts
make embedder     # download ONNX embedding model to ~/.ghyll/models/
```

Requires: Go 1.22+, ONNX Runtime (auto-downloaded on first memory operation).

## Project Structure

```
cmd/
  ghyll/            CLI entry point
  ghyll-vault/      team memory server
dialect/            model-specific code (no interfaces)
  router.go         context-depth routing
  glm5.go           GLM-5 prompt templates, tool parsing, token counting
  minimax_m25.go    MiniMax M2.5 prompt templates, tool parsing, token counting
  handoff.go        checkpoint-based model switching
context/            unified context manager
  manager.go        owns compaction + memory backfill decisions
  compactor.go      model-aware context compaction
  drift.go          cosine similarity drift detection
  injection.go      prompt injection signal detection
memory/             checkpoint store + vector search
  store.go          sqlite + hash chain
  embedder.go       ONNX embedding (lazy download)
  checkpoint.go     snapshot creation + summary generation
  sync.go           git orphan branch sync
  vault_client.go   HTTP client for ghyll-vault
vault/              team memory server
  server.go         HTTP API
  search.go         vector similarity search
tool/               native OS operations, no wrappers
  bash.go           direct exec.Command
  file.go           direct os.ReadFile/WriteFile
  git.go            direct git commands
  grep.go           direct ripgrep/grep
stream/             LLM client + terminal rendering
  client.go         OpenAI-compatible streaming HTTP
  render.go         terminal markdown rendering
config/             configuration
  config.go         TOML loader
  models.go         endpoint + dialect mapping
specs/              behavioral specifications
  features/         Gherkin .feature files
  architecture/     technical architecture specs
docs/               documentation
  analysis/         sanitized analyst conversation
  decisions/        ADRs
  architecture/     design documents
```

## Conventions

- Go: standard gofmt, no globals, context.Context threaded through all I/O
- No provider interfaces — each dialect is concrete functions, called directly
- Tools are direct syscalls — no permission layer, no wrappers (Tarn handles sandboxing)
- Commits: conventional commits (feat:, fix:, docs:, test:)
- Tests: every spec item traces to at least one BDD scenario
- Memory checkpoints are append-only — never modify, only create
- The git orphan branch `ghyll/memory` is never merged into code branches
- ONNX model is downloaded at runtime, not bundled in the binary

## Key Design Decisions

- See `docs/decisions/001-architecture.md` for full rationale
- No abstraction layer: dialect modules are concrete, not interface-based
- Context-depth routing replaces external router service
- Checkpoint-based handoff for model switching (lossy but token-efficient)
- Git orphan branch for memory sync (no vault required for basic team use)
- Merkle DAG with ed25519 signatures for tamper-evident memory
- ONNX embedding model as lazy download, not bundled

## Running

```bash
ghyll run .                           # start in current repo, auto-detect model
ghyll run . --model glm5              # force GLM-5
ghyll run . --model m25               # force MiniMax M2.5
ghyll memory search "race condition"  # search checkpoints
ghyll memory sync                     # sync with git remote
ghyll memory log                      # show checkpoint chain
ghyll config show                     # display current configuration
```

## Terminal Display

```
ghyll [m25] ~/repos/myproject ▸         # fast tier active
ghyll [glm5] ~/repos/myproject ▸        # deep tier (escalated)
ghyll [glm5→m25] ~/repos/myproject ▸    # de-escalated
⚠ checkpoint 3: injection signal in turn 7 (blocked by tarn)
ℹ backfill from @alice checkpoint 5: "auth module session refresh has race condition"
⟳ switched to glm5, loaded from checkpoint 4
```
