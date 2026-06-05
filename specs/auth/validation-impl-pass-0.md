# Validation — endpoint Bearer-token auth seam (pass 1)

Single-seam implementation per the architect plan: wire `api_key`
from `[models.<name>]` TOML + `GHYLL_API_KEY[_<MODEL>]` env into
every `stream.NewClient` call site, redact the value at every
operator-visible egress, never echo upstream 401/403 bodies.

## Surface changed

| Layer | File | Change |
|------|------|--------|
| Config | `config/config.go` | New `ModelConfig.APIKey` field, `ResolveAPIKey`, `ResolveAPIKeyWithSource`, `normalizeModelEnvKey`, `APIKeySource` enum. |
| Stream | `stream/client.go` | `classifyHTTPError` short-circuits 401/403 with the fixed string `"authentication failed"` (preserves `X-Request-ID` if upstream set it) BEFORE the body is unmarshalled. |
| CMD | `cmd/ghyll/auth.go` | New `buildAuthHeader` + `redactKeySource` helpers (single chokepoint). |
| CMD | `cmd/ghyll/session.go` | Three `stream.NewClient` call sites attach `ExtraHeaders: buildAuthHeader(...)`. Compaction path threads the active model name through `CompactionRequest.ModelName`. |
| CMD | `cmd/ghyll/subagent.go` | Sub-agent stream client inherits parent model's resolved key. |
| CMD | `cmd/ghyll/main.go` | `cmdConfigShow` per-model line gains `api_key: <unset>/<env>/<toml>` via `redactKeySource`. |
| Context | `context/manager.go` | `CompactionRequest` gains `ModelName string`; `Manager.compact` populates it from its existing `activeModel` arg. |
| Config seed | `config/example.toml` | Commented `api_key = ""` line under each `[models.*]` block. |
| Userhome seed | `config/userhome/instructions.md` | New "Endpoint authentication" subsection pointing at operator-guide.md. |
| Docs | `docs/operator-guide.md`, `docs/usage/configuration.md`, `CLAUDE.md` | Precedence table + redaction guarantees + secrets-handling convention. |

## TDD units (15 tests)

- `TestScenario_Config_ResolveAPIKey_TOMLOnly` — TOML-only path.
- `TestScenario_Config_ResolveAPIKey_GlobalEnvOverridesTOML` — env beats TOML.
- `TestScenario_Config_ResolveAPIKey_ScopedEnvOverridesGlobal` — model-scoped env wins.
- `TestScenario_Config_NormalizeModelEnvKey_NonAlphanumReplaced` — table-driven coverage of the env-key normalization rules.
- `TestScenario_Config_ResolveAPIKey_EmptyReturnsEmpty` — nil cfg + unknown model + zero-value APIKey all return `""`, no panics.
- `TestScenario_Config_ResolveAPIKey_MixedCaseTOMLKey` — cfg.Models key is mixed-case; env still normalizes upper-case.
- `TestScenario_Auth_BuildAuthHeader_EmptyKeyReturnsNilHeader` — nil header (not empty map) when no key resolves.
- `TestScenario_Auth_BuildAuthHeader_NonEmptyKeySetsBearer` — `Bearer <key>` format.
- `TestScenario_Auth_BuildAuthHeader_EnvOverridesTOML` — env precedence preserved at the chokepoint.
- `TestScenario_Auth_RedactKeySource_ReturnsProvenanceTokens` — three fixed literals, length-leak guard.
- `TestScenario_Stream_Auth401BodyNotSurfaced` — 401 body containing `Bearer sk-leak-zzz` does NOT appear in `StreamError.Error()`.
- `TestScenario_Stream_Auth403BodyNotSurfaced` — same for 403.
- `TestScenario_Stream_Auth401PreservesRequestID` — operator-set `X-Request-ID` is preserved as diagnostic; token still suppressed.
- `TestScenario_Stream_RequestIncludesAuthorizationHeader` — Bearer reaches the wire, Content-Type and Accept unmodified.
- `TestScenario_Stream_RequestIncludesAuthorizationHeader_Session` — Session.Turn carries the Bearer header through the dispatcher.
- `TestScenario_Compaction_PreservesAuthHeader` — `s.compactionCall` resolves the key against `req.ModelName`.
- `TestScenario_Compaction_EmptyModelNameNoAuth` — legacy callers passing zero-value `ModelName` get no Authorization header.
- `TestScenario_SubAgent_PreservesAuthHeader` — sub-agent stream client carries the parent's resolved key.
- `TestScenario_Handoff_ResolvesPerTargetModel` — two models with distinct api_keys produce distinct Bearer headers.
- `TestScenario_ConfigShow_RedactsAPIKey` — `redactKeySource` returns provenance, never the value.
- `TestScenario_JSONL_DoesNotContainAPIKey` — sentinel api_key in env never reaches `.ghyll/attestations.jsonl`.
- `TestScenario_Engine_DoesNotContainAPIKey` — sentinel api_key in env never reaches `.ghyll/engine.db`.

## BDD scenarios (specs/features/auth.feature)

1. **TOML api_key reaches the wire on every chat completion** — recording HTTP server captures inbound `Authorization`; asserts `Bearer sk-test-fixture-9f2a` plus unmodified `Content-Type` / `Accept`.
2. **env-scoped key beats env-global which beats TOML** — three layers configured simultaneously; assert the scoped env value reaches the wire.
3. **the api_key never appears in operator-visible output** — upstream returns 401 with the token echoed in body; assert `StreamError.Message == "authentication failed"`, the sentinel does not appear in the error chain, and the redactor returns `<toml>` provenance.

## Make targets

- `make test-unit` — green.
- `make test` (unit + acceptance) — green; 348 acceptance scenarios pass; 3 new auth scenarios green.
- `make test-race` — green (race-clean).
- `make coverage-check` — 78.7% (threshold 78%).
- `make` (lint + test + build) — green.

## Risks remediated in this pass

- **R1 (compaction request struct expansion)** — verified the literal is constructed in only one place (`context/manager.go:148`); no test fixtures rely on positional literals. Zero-value `ModelName` yields no Authorization header (legacy callers untouched).
- **R2 (TOML lower-cased vs env upper-cased asymmetry)** — documented in `ResolveAPIKey` godoc; `TestScenario_Config_ResolveAPIKey_MixedCaseTOMLKey` asserts both layers see the operator's intent.
- **R3 (401 body redaction may swallow diagnostics)** — `X-Request-ID` is preserved as suffix in `StreamError.Message` when upstream sets it.
- **R5 (single chokepoint discipline)** — `.APIKey` direct reads confined to `config/config.go` + `cmd/ghyll/auth.go`. Grep verified.
- **R7 (env-var test pollution)** — `t.Setenv` for unit tests; godog Before/After hooks reset env vars in `steps_auth.go`.

## Risks deferred (NOT remediated this pass)

- **R4 (file mode 0o600 re-check on Load)** — defence-in-depth; opt for the redactor + grep-test belt before adding the suspenders. File for follow-up.
- **R8 (stamp_label regex validator forbidding `sk-...`)** — codify only if a stamp_label leak surfaces in adversarial review.
- **R9 (live integration test against a real gateway)** — Tier 3 opt-in; manual verification step belongs in operator runbook, not CI.
- **R10 (adversarial cold-context pass)** — per CLAUDE.md the orchestrator runs the adversarial pass next.

## Grep verifications

```bash
grep -rn "sk-canary-cccc-must-not-leak" --include="*.go" | grep -v "_test.go\|specs/"
# (empty)

grep -rn "\.APIKey" --include="*.go" | grep -v _test.go
# - config/config.go (definition + resolver)
# - cmd/ghyll/auth.go (the chokepoint)
# - tests/acceptance/steps_auth.go (test wiring via enum, no value read)
```

## Outstanding work

Adversarial pass (cold-context) per project discipline. Plan and
docs do not need further changes for this seam.
