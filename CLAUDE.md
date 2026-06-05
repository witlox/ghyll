# Ghyll

A coding agent that optimizes for **correctness over speed and breadth**,
and pays for it in **friction**. Self-hosted, open-weight, sandbox-only.

ghyll is correct for a narrow class of work — novel architecture,
correctness-critical systems, long-horizon projects where a defect
reaching deployment is expensive. ghyll is wrong for CRUD, migrations,
glue code, and rapid prototyping. Stating the second half is the
position.

## Project State

ghyll is one codebase. The correctness mechanism is the gate-and-arrow
state machine; drift-aware memory, streaming, and sandbox-only
execution are continuity infrastructure. Major surfaces:

- **Engine** — sqlite-backed store + Journal observer fanout + Replay.
  Persists arrows, findings, clauses, classifications, amendments,
  evaluation runs. Source of truth across sessions.
- **Runner** — typed clause evaluation, depth routing (§7.1), arrow
  status derivation, adversarial phase, on-the-spot arrow creation.
- **Dialect** — context-depth-aware routing across MiniMax M2.5,
  GLM-5, DeepSeek, Qwen.
- **Memory** — checkpoint store, ed25519 sign + Merkle DAG, git
  orphan-branch sync to optional vault.
- **Diamond roles** — analyst → architect → implementer → integrator,
  contracts in `specs/architecture/roles/`.

The `.claude/roles/*.md` files are Claude Code's working role files
for editing this repo; they are NOT the runtime ghyll roles (those
live in `specs/architecture/roles/`).

## Build

```bash
make                  # lint + test + build
make build-bin        # versioned binaries to bin/
make test             # unit + acceptance tests
make test-race        # with race detector
make coverage-check   # enforce 78% threshold
make bench            # engine + runner benchmarks (perf/baselines.md)
make test-live        # opt-in live-endpoint tests (build tag `live`)
make docs-serve       # preview mdbook locally
make embedder         # download ONNX embedding model
```

Requires: Go 1.25+, ONNX Runtime (optional, for drift detection).

## Project Structure

```
cmd/
  ghyll/              CLI entry point + session loop + engine wiring
  ghyll-vault/        team memory server entry point
config/               TOML loader + validation
types/                shared types (Message, ToolCall, ToolResult) — leaf package
tool/                 direct OS operations (bash, file, git, grep, edit, glob, web)
workflow/             project instructions + slash commands loader
ui/                   user-facing terminal output (CLI). slog handles diagnostics.
stream/               SSE streaming client + terminal renderer
dialect/              model-specific code + routing decision table
  router.go           context-depth routing
  glm.go              GLM-5 dialect
  minimax.go          MiniMax M2.5 dialect
  deepseek.go         DeepSeek dialect
  qwen.go             Qwen dialect
  parse.go            shared OpenAI tool call parser
  helpers.go          shared sanitize/strip helpers
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
runner/               gate-and-arrow runtime: clause evaluation, arrow status,
                      adversarial phase, on-the-spot arrow creation, amendments
engine/               sqlite-backed persistent store + Journal observer fanout +
                      Replay (loads persisted entities at session start) +
                      Recovery (crash reconciliation between engine, JSONL audit,
                      and runner stores at session start)
bootstrap/            project initialization: auto-propose, modify rules,
                      orphan-symbol extraction, role-clause parsing, session registry
catalogue/            machine-clause concept catalogue (per-language bindings)
gates/                gate concept schemas (one YAML per concept)
internal/             pathglob + skipdirs utilities (leaf packages)
vault/                team memory HTTP server
tests/acceptance/     godog BDD acceptance tests
specs/                behavioral specifications + architecture + fidelity
docs/                 mdbook documentation site + ADRs
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
- CI: build -> validate -> test pipeline, 78% coverage threshold
- Releases: weekly cron or `workflow_dispatch` in `release.yml`; version is `v<year>.<sum-of-ADRs>.<commit-count>`; the ADR-sum is a manual constant bumped in the workflow when a new ADR lands
- Secrets (`api_key`, `vault.token`) are TOML-loaded and env-overridable (`GHYLL_API_KEY_<MODEL>` > `GHYLL_API_KEY` > TOML); ghyll never logs them — `ghyll config show` prints `<unset>`/`<env>`/`<toml>` provenance only

## Key Design Decisions

- [ADR-001: Architecture](docs/decisions/001-architecture.md) — Go, no interfaces, orphan branch, Merkle DAG
- [ADR-002: Types leaf package](docs/decisions/002-types-leaf-package.md) — import cycle prevention
- [ADR-003: Embedding excluded from hash](docs/decisions/003-embedding-excluded-from-hash.md) — float portability
- [ADR-004: Tool depth limit](docs/decisions/004-tool-depth-limit.md) — unbounded recursion guard
- [ADR-005: Compaction separate API call](docs/decisions/005-compaction-separate-api-call.md) — context overflow prevention
- [ADR-006: One session per repo](docs/decisions/006-one-session-per-repo.md) — lockfile concurrency
- [ADR-007: Tier-based routing](docs/decisions/007-tier-based-routing.md) — decouple routing from model names

- [ADR-008: Fixed v2 roles, no runtime overlay](docs/decisions/008-v2-fixed-roles-deprecate-runtime-workflow-roles.md) — diamond roles are contracts in specs, not swappable system prompts

- [ADR-015: Pass persistence + JSONL source of truth](docs/decisions/015-pass-persistence-and-jsonl-source-of-truth.md) — Tier 1: `passes` table + crash recovery + JSONL becomes the authoritative attestation log (amends ADR-010)

- [ADR-016: Tier 2 operator modal + tree primary](docs/decisions/016-tier-2-operator-modal-and-tree-primary.md) — operator verdict modal; per-pass attestation tree becomes the primary write target

- [ADR-017: ProjectStatus aggregator](docs/decisions/017-project-status-aggregator.md) — read-only aggregator over runner stores, no cache

Architectural reference (current code, not aspirational):

- [v2 design](specs/architecture/v2-design.md) — gate-and-arrow rationale
- [Gates schema](specs/architecture/gates.md) — evaluation types, depth types, arrows, routing, attestation
- [Role contracts](specs/architecture/roles/) — analyst, architect, implementer, integrator
- [Component designs](specs/architecture/components/) — per-component breakdowns
- [v2 ADRs](docs/decisions/v2/) — 13 structural decisions distilled from the operator-decisions rounds
- [v4 ADRs](docs/decisions/v4/) — 8 decisions from the diamond v4 pass (registry key shape, adversarial-phase enablement, re-register ordering, concept classification, typed event payload, two-table evaluator dispatch, binding-registration seam, engine schema migrations)

## Running

```bash
ghyll run .                           # start session, auto-detect model
ghyll run . --model glm5              # force GLM-5
ghyll run . --resume                  # resume from last session's checkpoint
ghyll init attest --op-id alice       # emit on-the-spot init attestations
ghyll memory search "race condition"  # search checkpoints
ghyll memory sync                     # manual sync
ghyll memory log                      # show checkpoint chain
ghyll engine status                   # show persistent engine state
ghyll engine replay                   # replay persisted entities
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
| `/op-id <id>` | Declare operator identity for this session (required before `/attest`, `/drain-amendments`, `/invalidate-arrow`) |
| `/attest <ref> <verdict> [reason]` | Record an attestation verdict on a depth-type or on-the-spot attestation |
| `/list-arrows` | Render the grid snapshot (sorted arrow IDs + source→target / stratum / context) |
| `/run-arrow <arrow-id> [--context <ctx>]` | Dispatch one arrow synchronously; surface pass + adversarial-cycle events inline |
| `/drain-amendments` | FIFO-drain the pending amendment queue under the active op-id (refuses without `/op-id`) |
| `/adversary {enable\|disable\|status}` | Toggle the §11 adversarial-cycle hook bundle (refuses `enable` when no dialect is configured) |
| `/invalidate-arrow <arrow-id> [--reason <text>]` | Invalidate an arrow; persists an audit row to `.ghyll/engine.db arrow_invalidations` |
| `/<name>` | Run user-defined slash command from .ghyll/commands/ |
