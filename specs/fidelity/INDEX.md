# Fidelity Index

Last checkpoint: 2026-04-12
Status: CHECKPOINT

## Summary

| Package | Scenarios | THOROUGH | MODERATE | SHALLOW | NONE | Confidence |
|---------|-----------|----------|----------|---------|------|------------|
| config/ | 9 | 7 | 2 | 0 | 0 | HIGH |
| tools/ | 7 | 6 | 0 | 0 | 1 | HIGH |
| stream/ | 11 | 6 | 2 | 0 | 3 | MODERATE |
| routing/ | 9 | 9 | 0 | 0 | 0 | HIGH |
| memory/ | 7 | 5 | 1 | 0 | 1 | HIGH |
| drift/ | 7 | 3 | 0 | 0 | 4 | LOW |
| sync/ | 8 | 4 | 1 | 0 | 3 | MODERATE |
| keys/ | 8 | 4 | 0 | 0 | 4 | MODERATE |
| compaction/ | 9 | 4 | 0 | 0 | 5 | LOW |
| vault/ | 9 | 5 | 2 | 0 | 2 | MODERATE |
| **Total** | **84** | **53** | **8** | **0** | **23** | |

## Detailed Assessment by Feature

### config.feature — HIGH confidence

| # | Scenario | Unit test | Acceptance | Depth |
|---|----------|-----------|------------|-------|
| 1 | Load valid config | TestScenario_Config_LoadValid | godog: pass | THOROUGH |
| 2 | Default values applied | TestScenario_Config_DefaultValues | godog: pass | THOROUGH |
| 3 | Config file missing | TestScenario_Config_FileMissing | godog: pass | THOROUGH |
| 4 | Malformed TOML | TestScenario_Config_MalformedTOML | godog: pass | THOROUGH |
| 5 | Missing required model endpoint | TestScenario_Config_MissingRequiredEndpoint | godog: pass | THOROUGH |
| 6 | Model override via flag | — | godog: pass | MODERATE (sets state, doesn't test routing) |
| 7 | Vault config optional | TestScenario_Config_VaultOptional | godog: pass | THOROUGH |
| 8 | Vault config with token | TestScenario_Config_VaultWithToken | godog: pass | THOROUGH |
| 9 | Vault on localhost skips auth | — | godog: pass | MODERATE (config level, not client) |

### tools.feature — HIGH confidence

| # | Scenario | Unit test | Depth |
|---|----------|-----------|-------|
| 1 | Bash command execution | TestScenario_Tools_BashExecution | THOROUGH (real exec) |
| 2 | Bash command timeout | TestScenario_Tools_BashTimeout | THOROUGH (real timeout) |
| 3 | File read | TestScenario_Tools_FileRead | THOROUGH |
| 4 | File write | TestScenario_Tools_FileWrite | THOROUGH |
| 5 | Git operation | TestScenario_Tools_GitOperation | THOROUGH (real git repo) |
| 6 | Grep with ripgrep | TestScenario_Tools_GrepRipgrep | THOROUGH |
| 7 | Grep fallback to standard grep | — | NONE (no test for fallback path) |

### stream.feature — MODERATE confidence

| # | Scenario | Unit test | Depth |
|---|----------|-----------|-------|
| 1 | Successful streaming response | TestScenario_Stream_SuccessfulResponse | THOROUGH (httptest) |
| 2 | Response with tool calls | TestScenario_Stream_ToolCalls | THOROUGH |
| 3 | Multiple tool calls | TestScenario_Stream_MultipleToolCalls | THOROUGH |
| 4 | Partial response on stream cut | TestScenario_Stream_PartialResponse | THOROUGH |
| 5 | Retry with exponential backoff | TestScenario_Stream_RetryBackoff | THOROUGH |
| 6 | Rate limit handling | TestScenario_Stream_RateLimit | THOROUGH |
| 7 | Tier fallback (auto-routing) | — | NONE (orchestration in cmd/ghyll, not tested) |
| 8 | Tier fallback reverse | — | NONE (same) |
| 9 | No fallback with model lock | — | NONE (same) |
| 10 | Both tiers unreachable | TestScenario_Stream_ContextTooLong | MODERATE (tests error, not both-tiers) |
| 11 | Malformed SSE frame | TestScenario_Stream_MalformedSSE | MODERATE (tests skip, not logging) |

Tier fallback scenarios require integration testing across stream + dialect + session — covered conceptually in session tests but not the specific fallback paths.

### routing.feature — HIGH confidence

| # | Scenario | Unit test | Depth |
|---|----------|-----------|-------|
| 1 | Fresh session starts on fast tier | TestScenario_Routing_FreshSession | THOROUGH |
| 2 | Context depth escalates | TestScenario_Routing_ContextDepthEscalates | THOROUGH (incl NeedCompaction) |
| 3 | Tool depth escalates | TestScenario_Routing_ToolDepthEscalates | THOROUGH |
| 4 | /deep temporary override | TestScenario_Routing_DeepOverride | THOROUGH |
| 5 | /deep reverts when conditions clear | TestScenario_Routing_DeepReverts | THOROUGH |
| 6 | /deep ignored when locked | TestScenario_Routing_DeepIgnoredWhenLocked | THOROUGH |
| 7 | Explicit model flag | TestScenario_Routing_ExplicitModelFlag | THOROUGH |
| 8 | De-escalation after compaction | TestScenario_Routing_DeEscalation | THOROUGH |
| 9 | Drift backfill escalation | TestScenario_Routing_DriftEscalates | THOROUGH |

### memory.feature — HIGH confidence

| # | Scenario | Unit test | Depth |
|---|----------|-----------|-------|
| 1 | Checkpoint creation | TestScenario_Memory_StoreAppend | THOROUGH (real sqlite) |
| 2 | Hash chain integrity | TestScenario_Memory_ChainVerification | THOROUGH |
| 3 | Tampered checkpoint detected | TestScenario_Memory_VerifyFailsTampered + HashChangesOnModification | THOROUGH |
| 4 | Signature verification | TestScenario_Memory_SignAndVerify + VerifyFailsWrongKey | THOROUGH |
| 5 | First checkpoint in session | TestScenario_Memory_StoreAppend (zero parent) | MODERATE |
| 6 | Checkpoint at model switch | TestScenario_Session_WithStore | THOROUGH (end-to-end) |
| 7 | Injection signal detection | TestScenario_Injection_DetectsOverride + Base64 + NoFalsePositive | NONE (not tested at checkpoint creation time, only as standalone) |

### drift.feature — LOW confidence

| # | Scenario | Unit test | Depth |
|---|----------|-----------|-------|
| 1 | No drift detected | TestScenario_Drift_NoDrift | THOROUGH |
| 2 | Drift detected | TestScenario_Drift_Detected | THOROUGH |
| 3 | Drift against checkpoint 0 | TestScenario_Drift_EmptyEmbeddings | THOROUGH |
| 4 | Backfill from team memory | — | NONE (requires embedder + vault + store integration) |
| 5 | Backfill respects token budget | — | NONE (not tested) |
| 6 | Embedding model not available | TestScenario_Embedder_Unavailable | NONE (tests embedder, not drift flow) |
| 7 | Drift check frequency | — | NONE (not tested — turn counting logic) |

### sync.feature — MODERATE confidence

| # | Scenario | Unit test | Depth |
|---|----------|-----------|-------|
| 1 | Initialize memory branch | TestScenario_Sync_InitMemoryBranch | THOROUGH (real git) |
| 2 | Checkpoint triggers sync | TestScenario_Sync_WriteCheckpoint | THOROUGH |
| 3 | Pull on session start | TestScenario_Sync_ReadCheckpoints | THOROUGH (cross-device) |
| 4 | Partial chain import | — | NONE |
| 5 | Concurrent push conflict | — | NONE (hard to test without concurrency setup) |
| 6 | Orphan branch isolation | TestScenario_Sync_OrphanIsolation | MODERATE (checks log, not merge) |
| 7 | Offline operation | TestScenario_Sync_OfflineOperation | THOROUGH |
| 8 | Large repo clone optimization | — | NONE (shallow fetch not tested) |

### keys.feature — MODERATE confidence

| # | Scenario | Unit test | Depth |
|---|----------|-----------|-------|
| 1 | First-run key generation | TestScenario_Keys_FirstRunGeneration | THOROUGH |
| 2 | Public key pushed to memory branch | — | NONE (requires syncer integration) |
| 3 | Remote public keys fetched | — | NONE (same) |
| 4 | Checkpoint signed with device key | TestScenario_Memory_SignAndVerify | THOROUGH |
| 5 | Key wrong permissions | TestScenario_Keys_WrongPermissions | THOROUGH |
| 6 | Verification known key | TestScenario_Memory_SignAndVerify | THOROUGH |
| 7 | Verification unknown key | TestScenario_Memory_VerifyFailsWrongKey | THOROUGH (different key, not "unknown") |
| 8 | Device ID derivation | — | NONE (hostname logic untested) |

### compaction.feature — LOW confidence

| # | Scenario | Unit test | Depth |
|---|----------|-----------|-------|
| 1 | Proactive compaction | TestScenario_Compaction_ProactiveBeforeTurn | THOROUGH |
| 2 | Reactive compaction | — | NONE (tested in session via ContextTooLong but not directly) |
| 3 | Compaction preserves recent | TestScenario_Compaction_PreservesRecentTurns | THOROUGH |
| 4 | Compaction call is separate | TestScenario_Compaction_ProactiveBeforeTurn | THOROUGH (callback verifies) |
| 5 | Compaction uses dialect prompt | — | NONE (not verified which prompt is used) |
| 6 | Compaction before routing escalation | — | NONE (requires routing + compaction integration) |
| 7 | Compaction triggers drift check | — | NONE |
| 8 | Compaction creates checkpoint | TestScenario_Compaction_CreatesCheckpoint | THOROUGH |
| 9 | Repeated compaction | — | NONE |

### vault.feature — MODERATE confidence

| # | Scenario | Unit test (server) | Unit test (client) | Depth |
|---|----------|-------------------|-------------------|-------|
| 1 | Search via vault | TestScenario_Vault_PushAndSearch | TestScenario_VaultClient_Search | THOROUGH |
| 2 | Bearer token | TestScenario_Vault_AuthRequired | TestScenario_VaultClient_BearerToken | THOROUGH |
| 3 | Localhost no token | — | TestScenario_VaultClient_LocalhostNoAuth | THOROUGH |
| 4 | Vault unreachable | — | TestScenario_VaultClient_Unreachable | THOROUGH |
| 5 | Unverified checkpoint | — | — | NONE (sig verification on client side not tested) |
| 6 | Broken hash chain | — | — | NONE (chain verification on import not tested) |
| 7 | Push checkpoint | TestScenario_Vault_PushAndSearch | TestScenario_VaultClient_Push | THOROUGH |
| 8 | Search API | TestScenario_Vault_PushAndSearch | — | MODERATE |
| 9 | Push rejects invalid | TestScenario_Vault_PushRejectsInvalidSig | — | MODERATE |

## Decision Enforcement

| ADR | Decision | Status |
|-----|----------|--------|
| 001-1 | Go over TypeScript/Rust | ENFORCED (project is Go) |
| 001-2 | Concrete dialects over abstraction | ENFORCED (no interface, concrete functions, test verifies) |
| 001-3 | Context-depth routing over external | ENFORCED (dialect/router.go, 11 tests) |
| 001-4 | Checkpoint-based handoff | ENFORCED (session.go handleHandoff, test with store) |
| 001-5 | Git orphan branch sync | ENFORCED (memory/sync.go, 5 integration tests) |
| 001-6 | Merkle DAG with ed25519 | ENFORCED (memory/crypto.go, 7 tests) |
| 001-7 | Always-yolo (no permissions) | ENFORCED (tool/*.go has no permission checks) |
| 001-8 | ONNX download over bundled | DOCUMENTED (embedder stub, no download test) |

## Priority Actions

1. **drift.feature: 4/7 NONE** — drift detection unit tests exist but the full flow (drift → backfill → escalation) is untested. This is the highest-risk gap: if drift triggers backfill but the integration path is broken, the feature silently fails.

2. **compaction.feature: 5/9 NONE** — compaction unit tests cover the manager but not integration paths (reactive compaction, compaction before handoff, dialect prompt selection, repeated compaction).

3. **stream.feature: 3/11 NONE** — tier fallback scenarios are orchestration (cmd/ghyll), need integration tests.

4. **keys.feature: 4/8 NONE** — public key distribution via syncer is untested.

5. **sync.feature: 3/8 NONE** — concurrent push, partial chain import, shallow fetch untested.
