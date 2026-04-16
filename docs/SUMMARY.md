# Summary

[Introduction](README.md)

---

# User Guide

- [Getting Started](usage/getting-started.md)
- [Configuration](usage/configuration.md)
- [CLI Reference](usage/cli-reference.md)
- [Memory & Sync](usage/memory.md)
- [Troubleshooting](usage/troubleshooting.md)

---

# Architecture

- [System Design](architecture/design.md)
- [Package Graph](architecture/package-graph.md)
- [Routing Logic](architecture/routing.md)
- [Checkpoint Format](architecture/checkpoints.md)
- [Sync Protocol](architecture/sync.md)
- [Vault API](architecture/vault-api.md)
- [Error Handling](architecture/errors.md)
- [Session Loop](architecture/session-loop.md)

---

# Internals

- [Dialect Modules](internals/dialects.md)
- [Context Management](internals/context.md)
- [Drift Detection](internals/drift.md)
- [Injection Detection](internals/injection.md)
- [Tool Execution](internals/tools.md)
- [Workflow System](internals/workflow.md)
- [Sub-Agents](internals/sub-agents.md)

---

# Decisions

- [ADR-001: Architecture](decisions/001-architecture.md)
- [ADR-002: Shared Types Leaf Package](decisions/002-types-leaf-package.md)
- [ADR-003: Embedding Excluded from Hash](decisions/003-embedding-excluded-from-hash.md)
- [ADR-004: Tool Call Depth Limit](decisions/004-tool-depth-limit.md)
- [ADR-005: Compaction as Separate API Call](decisions/005-compaction-separate-api-call.md)
- [ADR-006: One Session Per Repository](decisions/006-one-session-per-repo.md)
- [ADR-007: Tier-Based Routing](decisions/007-tier-based-routing.md)
