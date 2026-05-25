# Architecture Overview

Ghyll is a coding agent for self-hosted open-weight models, with
a typed-clause + arrow-based correctness mechanism layered on top.
It is **sandbox-only** by design — the runtime executes tool
calls directly with no permission layer; the sandbox boundary is
the security boundary. See [Why ghyll](../why.md#5-sandbox-only-execution)
for the rationale.

This page is the entry to the architecture chapter. Each
subsystem has its own dedicated page; this page sketches the
shape and points you at the right deep-dive.

## System diagram

```
+----------------------------------------------------------+
| Developer machine (inside an operator-chosen sandbox)    |
|                                                          |
|  ghyll (single Go binary)                                |
|  +----------------------------------------------------+  |
|  | cmd/ghyll          CLI, session loop, slash cmds   |  |
|  | cmd/ghyll/modal    Tier 2 verdict modal (TermModal)|  |
|  | runner/            gate-and-arrow runtime          |  |
|  |                    - clause evaluation             |  |
|  |                    - dispatcher / RunArrow         |  |
|  |                    - InsufficientBasisTracker      |  |
|  |                    - OperatorBus, AttestationStore |  |
|  | engine/            sqlite store + journal replay   |  |
|  |                    + crash recovery (ADR-015)      |  |
|  | bootstrap/         project init, grid build,       |  |
|  |                    propose + modify rules          |  |
|  | catalogue/         closed concept vocabulary       |  |
|  |                    (embedded gates/concepts/*.yaml)|  |
|  | gates/concepts/    18 clause concept schemas       |  |
|  | dialect/           model-specific code             |  |
|  |   router.go        gate-driven routing             |  |
|  |   minimax_m25.go   MiniMax M2.5 dialect            |  |
|  |   glm5.go          GLM-5 dialect                   |  |
|  |   deepseek.go      DeepSeek dialect                |  |
|  |   qwen.go          Qwen dialect                    |  |
|  |   parse.go         shared OpenAI tool-call parser  |  |
|  |   helpers.go       sanitize / strip helpers        |  |
|  | context/           context manager                 |  |
|  |   manager.go       compaction + backfill           |  |
|  |   drift.go         cosine drift detection          |  |
|  |   injection.go     prompt-injection signal         |  |
|  | memory/            checkpoint store                |  |
|  |   store.go         sqlite + hash chain             |  |
|  |   crypto.go        ed25519 sign / verify           |  |
|  |   embedder.go      ONNX runtime (optional)         |  |
|  |   sync.go          git orphan branch sync          |  |
|  |   vault_client.go  HTTP client for ghyll-vault     |  |
|  | stream/            SSE client + terminal renderer  |  |
|  | tool/              direct OS operations            |  |
|  | workflow/          project-instructions loader     |  |
|  | ui/                user-facing terminal output     |  |
|  | config/            TOML loader + validation        |  |
|  | internal/          pathglob, safefile, skipdirs    |  |
|  | vault/             team-memory HTTP server         |  |
|  +----------------------------------------------------+  |
|                                                          |
|  .ghyll/                       (project-local)           |
|    grid.v1.yaml                bootstrap-built grid      |
|    engine.db                   sqlite runner state       |
|    attestations/<pass>/        per-pass tree audit log   |
|    attestations.jsonl          flat aggregate audit log  |
|                                                          |
|  ~/.ghyll/                     (user-global)             |
|    config.toml                 endpoints + thresholds    |
|    memory.db                   sqlite checkpoint store   |
|    keys/                       ed25519 device keys       |
|    models/                     ONNX embedding model      |
+----------------------------+-----------------------------+
                             | HTTPS
                +------------+------------+
                |                         |
       SGLang endpoints           ghyll-vault (optional)
       (MiniMax M2.5, GLM-5,      team-memory HTTP search
        DeepSeek, Qwen)            against vault.db
```

## Architecture pages

The big subsystems each have their own page:

- **[Architecture Flows](../architecture-flows.md)** — sequence
  diagrams for the major flows: init, dispatch, Tier 2 verdict
  modal, amendment commit, crash recovery.
- **[Package Graph](package-graph.md)** — Go package layout,
  dependency direction, and the role of the `types/` leaf
  package.
- **[Session Loop](session-loop.md)** — the state machine at
  the heart of `cmd/ghyll`: init, turn execution, compaction,
  handoff, backfill, shutdown.
- **[Context-Depth Routing](routing.md)** — the decision table
  that picks a tier from the depth requirements of an arrow's
  clauses. ADR-007 documents the rationale.
- **[Checkpoint Format](checkpoints.md)** — canonical
  serialization, ed25519 signing, hash-chain verification,
  storage layout.
- **[Sync Protocol](sync.md)** — git-based memory sync via the
  `ghyll/memory` orphan branch.
- **[Vault API](vault-api.md)** — HTTP API exposed by
  `ghyll-vault` for team memory search.
- **[Error Types](errors.md)** — typed errors per package,
  sentinel patterns, error-flow across boundaries.

## See also

- [Why ghyll](../why.md) — design rationale for the five core
  decisions.
- [Architecture decisions](../decisions/) — 17 main ADRs and
  13 v2-pivot ADRs.
- [`specs/architecture/`](https://github.com/witlox/ghyll/tree/main/specs/architecture) —
  canonical design reference (current code, not aspirational).

## Configuration

The full configuration reference lives on the
[Configuration](../usage/configuration.md) page. A minimal
working config:

```toml
[models.m25]
endpoint = "https://inference.internal:8001/v1"
dialect = "minimax"
max_context = 1000000

[models.glm5]
endpoint = "https://inference.internal:8002/v1"
dialect = "glm"
max_context = 200000

[routing]
default_model = "m25"
deep_model = "glm5"
context_depth_threshold = 32000
enable_auto_routing = true
```
