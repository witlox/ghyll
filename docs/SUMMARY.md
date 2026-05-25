# Summary

[Introduction](README.md)

---

# Why ghyll

- [The design rationale](why.md)

---

# User Guide

- [Getting Started](usage/getting-started.md)
- [Configuration](usage/configuration.md)
- [CLI Reference](usage/cli-reference.md)
- [Operator Guide](operator-guide.md)
- [Memory & Sync](usage/memory.md)
- [Troubleshooting](usage/troubleshooting.md)

---

# Architecture

- [System Design](architecture/design.md)
- [Architecture Flows](architecture-flows.md)
- [Package Graph](architecture/package-graph.md)
- [Routing Logic](architecture/routing.md)
- [Session Loop](architecture/session-loop.md)
- [Checkpoint Format](architecture/checkpoints.md)
- [Sync Protocol](architecture/sync.md)
- [Vault API](architecture/vault-api.md)
- [Error Handling](architecture/errors.md)

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

# Architecture Decisions

- [ADR-001: Architecture](decisions/001-architecture.md)
- [ADR-002: Shared Types Leaf Package](decisions/002-types-leaf-package.md)
- [ADR-003: Embedding Excluded from Hash](decisions/003-embedding-excluded-from-hash.md)
- [ADR-004: Tool Call Depth Limit](decisions/004-tool-depth-limit.md)
- [ADR-005: Compaction as Separate API Call](decisions/005-compaction-separate-api-call.md)
- [ADR-006: One Session Per Repository](decisions/006-one-session-per-repo.md)
- [ADR-007: Tier-Based Routing](decisions/007-tier-based-routing.md)
- [ADR-008: V2 Fixed Roles, No Runtime Overlay](decisions/008-v2-fixed-roles-deprecate-runtime-workflow-roles.md)
- [ADR-009: Self-Cert Scope Includes Target Role](decisions/009-self-cert-scope-includes-target-role.md)
- [ADR-010: Attestation Store — Runner/Engine Split](decisions/010-attestation-store-runner-engine-split.md)
- [ADR-011: Per-Role-Context Lock](decisions/011-per-role-context-lock.md)
- [ADR-012: Operator Event Bus](decisions/012-operator-event-bus.md)
- [ADR-013: Pass Entity & Registry](decisions/013-pass-entity-and-registry.md)
- [ADR-014: Adversarial Orchestrator](decisions/014-adversarial-orchestrator.md)
- [ADR-015: Pass Persistence + JSONL Source of Truth](decisions/015-pass-persistence-and-jsonl-source-of-truth.md)
- [ADR-016: Tier-2 Operator Modal + Tree Primary](decisions/016-tier-2-operator-modal-and-tree-primary.md)
- [ADR-017: ProjectStatus Aggregator](decisions/017-project-status-aggregator.md)

---

# V2 Architecture Decisions

- [V2-ADR-001: V2 Pivot](decisions/v2/001-v2-pivot.md)
- [V2-ADR-002: State-Space Frame](decisions/v2/002-state-space-frame.md)
- [V2-ADR-003: Four-Role Diamond](decisions/v2/003-four-role-diamond.md)
- [V2-ADR-004: Synthetic Role IDs](decisions/v2/004-synthetic-role-ids.md)
- [V2-ADR-005: Concept Catalogue](decisions/v2/005-concept-catalogue.md)
- [V2-ADR-006: Per-Concept Schemas](decisions/v2/006-per-concept-schemas.md)
- [V2-ADR-007: Hybrid Artifact IDs](decisions/v2/007-hybrid-artifact-ids.md)
- [V2-ADR-008: Arrow/Pass Identity](decisions/v2/008-arrow-pass-identity.md)
- [V2-ADR-009: Three Locks](decisions/v2/009-three-locks.md)
- [V2-ADR-010: Versioned Grid Files](decisions/v2/010-versioned-grid-files.md)
- [V2-ADR-011: Init Auto-Propose](decisions/v2/011-init-auto-propose.md)
- [V2-ADR-012: Amendment Serialization](decisions/v2/012-amendment-serialization.md)
- [V2-ADR-013: Add tests-pass Concept](decisions/v2/013-add-tests-pass-concept.md)
