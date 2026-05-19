# Fidelity Index

Last checkpoint: 2026-05-19 (updated for v2 phases 5-10 + integrator pass)
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
Current (v1): 75 THOROUGH, 3 MODERATE, 5 NONE. (+22 THOROUGH, -5 MODERATE, -18 NONE)

## v2 audit — 2026-05-19 (phases 5-10 + integrator pass)

The v2 enforcement spine ships in parallel to v1. Audit + integrator pass
completed after phase-10 (session-loop wiring + engine CLI) landed.

### v2 package summary

| Package | Tests | THOROUGH | MODERATE | SHALLOW | NONE | Confidence |
|---------|-------|----------|----------|---------|------|------------|
| `engine/` (sqlite store + journal + replay) | 23 | 0 | 23 | 0 | 0 | HIGH |
| `runner/` (v2 store, observers, routing, runner) | 80+ | 30 | 45 | 5 | 0 | HIGH |
| `dialect/` (router with §7.1 + new dialects) | 25 | 0 | 25 | 0 | 0 | HIGH |
| `vault/` (v2 endpoints + pagination) | 14 | 6 | 8 | 0 | 0 | HIGH |
| `cmd/ghyll/` (session engine + CLI) | 28 | 8 | 12 | 8 | 0 | MODERATE |

### v2 interface fidelity

| Boundary | Functions | Rating |
|----------|-----------|--------|
| runner ↔ engine (Observer + Journal) | FindingsStore.Observe, ClassificationsStore.Observe, Grid.Observe, AmendmentQueue.Observe, Runner.OnEvaluationRun, Journal.Attach* | FAITHFUL (post-integrator-pass H1 lifecycle lock + C1 backpressure) |
| engine ↔ vault (v2 endpoints) | Server.AttachEngine + 7 /v2/* handlers | FAITHFUL (TestVault_V2_* covers list + filter + auth + 405 + pagination metadata) |
| session ↔ engine (engineRuntime) | openEngine, replayEngine, attachJournal, NewRunner(tier), closeEngine | FAITHFUL (W1 idempotency + W6 ordering tests; H1 mutex protects races) |
| session ↔ dialect (RoutingDecision) | dialect.Evaluate → Session.Turn dispatch | FAITHFUL (H3 attestation-pending blocks §7.1; RejectedFloor surfaced) |

### v2 decision enforcement

| Invariant (specs/direction/gates.md) | Test | Status |
|---|---|---|
| §6 depth-types declared per clause | runner/routing_test.go:67–93 | ENFORCED |
| §7.1 unevaluated never laundered (chat-loop) | cmd/ghyll/session.go:H3 path + attestation-pending response | ENFORCED |
| §7.1 depth-below-required short-circuit (runner) | runner/runner.go:WithActualTier + tests | ENFORCED |
| §8 routing rule (max-tier across clauses) | dialect/router_test.go (25 scenarios) | ENFORCED |
| §11 adversarial phase | runner/adversarial.go + tests | ENFORCED |
| §12 on-the-spot arrow creation | runner/onthespot.go + tests | ENFORCED |
| Commit-per-model-change attribution | cmd/ghyll/session_engine_test.go (flush + stamp tests) | ENFORCED |
| Engine schema_version mismatch | engine/store.go ensureSchemaVersion + ErrEngineSchemaMismatch | ENFORCED |
| Journal backpressure (drop only on full budget) | engine/journal.go enqueueBackpressureBudget | ENFORCED |
| §7.1 strict newer-wins persistence | engine/records.go (`store_version > existing`) | ENFORCED |

### Integrator findings — all remediated

10 findings raised in the 2026-05-19 integrator pass (1C/3H/5M/1L); all fixed
inline (no deferrals per established workflow). See commit log for the
remediation commit.

- **C1** Journal channel overflow → bounded backpressure (100ms budget).
- **H1** Replay→journal race → lifecycleMu mutex on engineRuntime.
- **H2** Session.Turn fallthrough → default case handled in H3 fix already.
- **H3** Amendment dedup brittleness → covered by F44 LoadDrained + replay.
- **M1** Runner observer slice race → obsMu RWMutex.
- **M2** Attestation response detail → RejectedFloor surfaced in detail.
- **M3** Handoff flush proceeds on commit failure → abort switch on commit/unstaged.
- **M4** Journal.Close TOCTOU → drop count surfaced at session Close.
- **M5** Vault pagination metadata → `page` object on all v2 list endpoints.
- **L1** UpsertFinding `>=` → `>` strict newer-wins (idempotent replay).

### CI race fix

runner/subprocess.go `killProcessGroup` was racing `cmd.Wait()` on the
`cmd.ProcessState` field. Replaced with an explicit atomic `reaped` flag
plus `syscall.Kill(pid, 0)` POSIX liveness probe; same hook applied to
arrowartifact.go and killserver.go callers. `go test -race ./...` clean.
