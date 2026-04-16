# Fidelity Index

Last checkpoint: 2026-04-16 (updated for ADR-007 tier-based routing)
Status: CHECKPOINT

## Summary

| Package | Scenarios | THOROUGH | MODERATE | SHALLOW | NONE | Confidence |
|---------|-----------|----------|----------|---------|------|------------|
| config/ | 9 | 9 | 0 | 0 | 0 | HIGH |
| tools/ | 7 | 7 | 0 | 0 | 0 | HIGH |
| stream/ | 11 | 8 | 1 | 0 | 2 | HIGH |
| routing/ | 9 | 9 | 0 | 0 | 0 | HIGH |
| memory/ | 7 | 7 | 0 | 0 | 0 | HIGH |
| drift/ | 7 | 5 | 1 | 0 | 1 | MODERATE |
| sync/ | 8 | 7 | 0 | 0 | 1 | HIGH |
| keys/ | 8 | 7 | 0 | 0 | 1 | HIGH |
| compaction/ | 9 | 8 | 0 | 0 | 1 | HIGH |
| vault/ | 9 | 8 | 1 | 0 | 0 | HIGH |
| **Total** | **84** | **75** | **3** | **0** | **5** | |

## ADR-007 (tier-based routing) audit — 2026-04-16

Routing and config scenarios were adapted, not augmented, by the refactor
(same 9 + 9 scenarios, new dialect family strings). New code paths introduced
by the adversary-fix commits have dedicated unit tests:

| Code path | Purpose | Tests |
|-----------|---------|-------|
| `cmd/ghyll/session.go:normalizeDialect` | Legacy dialect string mapping (ADV-1) | `TestScenario_Session_NormalizeDialect`, `TestScenario_Session_ResolveDialectLegacyGLM5` |
| `config/config.go:validate` dialect allow-list | Reject unknown dialects (ADV-2) | `TestScenario_Config_UnknownDialect`, `TestScenario_Config_LegacyDialectsAccepted` |
| `config/config.go:validate` deep_model endpoint | Reject dangling deep_model reference | `TestScenario_Config_DeepModelNoEndpoint` |
| `dialect/router.go` canEscalate on rows 2-6 | Single-tier and deep==default guards (ADV-5) | `TestScenario_Routing_SingleTierNoEscalate`, `TestScenario_Routing_SingleTierNoDeEscalate`, `TestScenario_Routing_DeepEqualsDefaultNoEscalate` |

Confidence for routing/ and config/ remains HIGH after the refactor.

## Remaining gaps (5 NONE, 3 MODERATE)

| Scenario | Reason | Risk |
|----------|--------|------|
| Tier fallback auto-routing (stream) | Orchestration in REPL/session layer, not stream | Low — retries tested, fallback is routing logic |
| Tier fallback reverse (stream) | Same as above | Low |
| Backfill from team memory (drift) | Requires live embedder + vault + store | Medium — individual components tested |
| Concurrent push conflict (sync) | Requires concurrent git processes | Low — append-only design prevents conflicts |
| Large repo clone optimization (sync) | Shallow fetch not testable without large repo | Low |
| Drift check frequency (drift) | Turn counting tested, interval logic in session | Low |
| Device ID derivation (keys) | Hostname-based, tested as stable across loads | Low |
| Compaction before routing escalation | Router + compaction integration in session | Low — both tested independently |

## Decision Enforcement

| ADR | Decision | Status |
|-----|----------|--------|
| 001-1 | Go over TypeScript/Rust | ENFORCED |
| 001-2 | Concrete dialects | ENFORCED |
| 001-3 | Context-depth routing | ENFORCED (11 tests) |
| 001-4 | Checkpoint-based handoff | ENFORCED (session test with store) |
| 001-5 | Git orphan branch sync | ENFORCED (7 integration tests) |
| 001-6 | Merkle DAG + ed25519 | ENFORCED (7 crypto tests) |
| 001-7 | Always-yolo | ENFORCED |
| 001-8 | ONNX lazy download | DOCUMENTED |
| 007   | Tier-based routing + dialect families | ENFORCED (router reads DefaultModel/DeepModel; dialect family allow-list; legacy-string normalization) |

## Test counts

141 unit/integration tests across 10 packages, plus 8 unit tests added in the
2026-04-16 ADR-007 audit (2 session, 3 config, 3 router).
84 godog acceptance scenarios wired (9 config with real assertions).

Previous checkpoint: 53 THOROUGH, 8 MODERATE, 23 NONE.
Current: 75 THOROUGH, 3 MODERATE, 5 NONE. (+22 THOROUGH, -5 MODERATE, -18 NONE)
