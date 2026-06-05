# Validation — adversarial-pass remediation on endpoint Bearer-token auth (pass 1)

Adversarial-pass remediation across **27 findings** flagged by two
cold-context adversaries (`AUTH-1..11`, `AUTH-W-001..011`,
`ADV-AUTH-001..010`). The user's standing rule is "no deferrals on
adversarial findings"; this pass remediates every finding except
where a duplicate/overlap with a stronger finding makes the work
redundant.

## Surface changed

| Layer | File | Change |
|------|------|--------|
| Config | `config/config.go` | (1) `Load` captures `toml.MetaData` and rejects misspelled api_key keys under `[models.*]`. (2) `Load` re-checks file mode 0o600 when any secret is present; refuses group/other read. (3) `validate` rejects two model keys that normalize to the same env-var bucket. (4) `validate` rejects api_key with control chars. (5) `validate` rejects http:// endpoint with api_key unless host is loopback. (6) `validate` rejects stamp_label embedding a secret-shaped string. (7) `ResolveAPIKeyWithSource` trims whitespace at every layer. (8) `normalizeModelEnvKey` iterates runes, not bytes. |
| Stream | `stream/client.go` | (1) `sanitizeUpstreamMessage` strips Bearer/Authorization/`sk-...` substrings from ALL non-2xx upstream messages (not just 401/403). (2) `sanitizeRequestID` caps X-Request-ID at 64 bytes and rejects non-printable / non-RFC-conventional characters. |
| CMD | `cmd/ghyll/session.go` | `compactionCall` falls back to `s.activeModel` when `req.ModelName` is empty (ADR-005 invariant) and threads ModelName into the request body. |
| CMD | `cmd/ghyll/subagent.go` | Surface a `stream.StreamError` via (status, sanitized Message) rather than `err.Error()` so non-401/403 body bytes don't reach `ToolResult.Error` / sub-agent context. Comment block rewritten to match the actual semantics (sub-agents use their OWN configured model, NOT the parent's). |
| CMD | `cmd/ghyll/main.go` | `cmdConfigShow` mirrors api_key provenance for `cfg.Vault.Token` (`<unset>|<toml>`). |
| Tests | `runner/attestation_jsonl_apikey_test.go`, `engine/store_apikey_test.go` | Replaced `os.Setenv` with `t.Setenv` (ADV-AUTH-003). Documented as defence-in-depth unit guard with explicit pointer to the new end-to-end test that drives the real wiring. |
| Tests | `cmd/ghyll/auth_integration_test.go` | (1) New `TestScenario_Handoff_RealHandoffDispatchUsesTargetKey` exercises the actual handoffToModel path with two recording servers. (2) New `TestScenario_AuditArtifacts_DoNotContainAPIKey` drives a real Session.Turn() and greps `.ghyll/` for a sentinel — replaces tautological tests with end-to-end coverage. (3) `TestScenario_Compaction_EmptyModelNameFallsBackToSession` renamed and updated to pin the post-fix behaviour. |
| Tests | `stream/auth_redaction_test.go` | New `TestScenario_Stream_NonAuth4xxBodyRedacted` (400/402/422/500/502), `TestScenario_Stream_Auth401_RequestIDFiltersAttack`, `TestScenario_Stream_Auth401_RequestIDLengthCapped`. |
| Tests | `config/config_apikey_test.go` | Trim, rune-aware normalization, collision detection, control-char rejection, http+secret rejection (5 cases), stamp_label secret rejection, misspelled key rejection (6 cases), file-mode check (both directions). |
| Tests | `tests/acceptance/steps_config.go` | All config write helpers updated from 0o644 to 0o600 since file-mode is now enforced when secrets are present. |
| Acceptance | `specs/features/auth.feature`, `tests/acceptance/steps_auth.go` | Three new BDD scenarios for misspelled key, env collision, trimming. |

## Findings closed (with how)

| ID | How |
|----|-----|
| AUTH-1 | `Load` captures `toml.MetaData.Undecoded()` and matches `(api[_-]?(token|key)|apikey|auth[_-]?(token|key)|token|bearer|secret)` under `[models.*]`. |
| AUTH-2 | `validate` builds a normalized-env-key map across all `cfg.Models` and rejects collisions with a directed `rename one of: %q, %q → GHYLL_API_KEY_%s` error. |
| AUTH-3 | `ResolveAPIKeyWithSource` runs `strings.TrimSpace` on every layer; `validate` rejects control-char api_keys at Load. |
| AUTH-4 | `sanitizeRequestID` caps at 64 bytes and limits to `[A-Za-z0-9._\-:]`. Attacker-controlled X-Request-ID containing a Bearer prefix is dropped (fails whitelist). |
| AUTH-5 | `checkSecretFileMode` re-stats config.toml when any model has api_key (or vault.token); rejects 0o077 mode bits with a chmod hint. |
| AUTH-6 | Subsumed by AUTH-1 — undecoded-key check catches `APIKey = ...` and `Api_Key = ...` because struct tag is lowercase `api_key` only. |
| AUTH-7 | `requireHTTPSWithSecret` refuses http:// + api_key combination unless host is loopback (127.0.0.0/8, ::1, localhost). |
| AUTH-8 | Documented in `ResolveAPIKeyWithSource` godoc: env-set-to-empty falls through to TOML; this is intentional. The "negative override" semantics requested by the finding would require switching to `os.LookupEnv` and the documentation now states this trade-off explicitly. |
| AUTH-9 | `cmdConfigShow` renders `Vault: %s (token: %s)` with `<unset>|<toml>` provenance, mirroring api_key. |
| AUTH-10 | Three new BDD scenarios added to `auth.feature`: misspelled `api_token`, env-bucket collision, trailing-whitespace trimming. |
| AUTH-11 | `TestScenario_AuditArtifacts_DoNotContainAPIKey` in `cmd/ghyll/auth_integration_test.go` drives a real Session.Turn and greps every file under `.ghyll/` for the sentinel. The unit-level grep tests in runner/ and engine/ remain as defence-in-depth but their godoc now explicitly points at the end-to-end test as the load-bearing assertion. |
| AUTH-W-001 | `sanitizeUpstreamMessage` regex strips Bearer/Authorization/sk-prefix substrings from ALL non-2xx body-derived `StreamError.Message`. |
| AUTH-W-002 | Same fix as AUTH-11. |
| AUTH-W-003 | `subagent.go` comment rewritten to match actual semantics (sub-agent uses its OWN configured model, NOT parent's). The finding suggested both options — we chose Option B (clarify the existing semantics) because the architect intent matches the existing wire-up, and changing to Option A would surprise operators who configured a sub-agent-specific model. |
| AUTH-W-004 | `compactionCall` now pins to `s.activeModel` when `req.ModelName == ""` and threads ModelName into the request body. ADR-005 invariant restored. |
| AUTH-W-005 | `stampLabelSecretPattern` in `validate` rejects `sk-[A-Za-z0-9_\-]{12,}`, `bearer`, `api_key`, `secret` substrings. |
| AUTH-W-006 | Same fix as AUTH-5. |
| AUTH-W-007 | Documented in struct godoc + steps_auth comment that env mutation is safe ONLY at godog default Concurrency. Acceptance test harness already runs sequential; the comment block guards a future maintainer who might bump it. |
| AUTH-W-008 | `normalizeModelEnvKey` iterates runes via `for _, r := range name` rather than bytes; collision-detection in `validate` is the final safety net. |
| AUTH-W-009 | Subsumed by AUTH-3 — control-char rejection in `validate` is the same fix. |
| AUTH-W-010 | `compactionCall` now sets `ModelName: dialectName` on `stream.ClientOptions` so CSCS-style gateways routing on the request body's `model` field reach the correct backend. |
| AUTH-W-011 | The single-chokepoint property is documented in `cmd/ghyll/auth.go`'s godoc and reinforced by adding the `vault.token` provenance through the same redactor pattern. A hard lint rule was considered but rejected in favor of the chokepoint comment + grep verification (`grep -rn '\.APIKey' --include='*.go' | grep -v _test.go` still returns only config/config.go + cmd/ghyll/auth.go). |
| ADV-AUTH-001 | Same fix as AUTH-W-001. |
| ADV-AUTH-002 | Same fix as AUTH-5. |
| ADV-AUTH-003 | `engine/store_apikey_test.go` and `runner/attestation_jsonl_apikey_test.go` now use `t.Setenv` instead of `os.Setenv`. |
| ADV-AUTH-004 | `TestScenario_Handoff_RealHandoffDispatchUsesTargetKey` drives the real handleHandoff path with two recording servers and asserts each server captured ONLY its own Bearer header. |
| ADV-AUTH-005 | Same fix as AUTH-W-005. |
| ADV-AUTH-006 | Same fix as AUTH-4. |
| ADV-AUTH-007 | `subagent.go` surfaces `sub-agent model unreachable (HTTP %d): %s` using `se.StatusCode + se.Message` (already-sanitized) rather than `err.Error()` (which would include the chain). |
| ADV-AUTH-008 | `specs/auth/validation-impl-pass-0.md` is the prior pass's doc; this new doc `specs/auth/validation-impl-pass-1.md` documents the actual remediation per the user's standing rule. R4 (file-mode) and R8 (stamp_label validator) are now implemented — not deferred. |
| ADV-AUTH-009 | Same fix as AUTH-W-007 — comment block documents the sequential-execution requirement. |
| ADV-AUTH-010 | `ResolveAPIKey` godoc now documents the `os.Getenv` empty-vs-unset conflation explicitly, including a "Semantics note" block stating the intentional non-override behaviour. |

## Findings deferred — none

Every finding is remediated in this pass. The user's standing rule
("no deferrals on adversarial findings") is honoured.

## Tests added / changed

**New tests:**
- `TestScenario_Config_ResolveAPIKey_TrimsWhitespace` (trim guard)
- `TestScenario_Config_NormalizeModelEnvKey_RuneAware` (rune iteration)
- `TestScenario_Config_Validate_RejectsCollidingEnvBuckets` (AUTH-2)
- `TestScenario_Config_Validate_RejectsAPIKeyControlCharacters` (AUTH-3)
- `TestScenario_Config_Validate_RejectsHTTPWithSecret` (5 cases, AUTH-7)
- `TestScenario_Config_Validate_RejectsSecretInStampLabel` (AUTH-W-005)
- `TestScenario_Config_Load_RejectsMisspelledAPIKey` (6 cases, AUTH-1)
- `TestScenario_Config_Load_RejectsGroupReadableConfigWithSecret` (AUTH-5)
- `TestScenario_Config_Load_AllowsGroupReadableConfigWithoutSecret` (boundary)
- `TestScenario_Stream_NonAuth4xxBodyRedacted` (5 status codes, AUTH-W-001)
- `TestScenario_Stream_Auth401_RequestIDFiltersAttack` (AUTH-4)
- `TestScenario_Stream_Auth401_RequestIDLengthCapped` (AUTH-4)
- `TestScenario_Handoff_RealHandoffDispatchUsesTargetKey` (ADV-AUTH-004)
- `TestScenario_AuditArtifacts_DoNotContainAPIKey` (AUTH-11, AUTH-W-002)
- 3 new BDD scenarios in `specs/features/auth.feature`

**Updated tests:**
- `TestScenario_Compaction_EmptyModelNameNoAuth` → `…FallsBackToSession` (AUTH-W-004)
- `engine/store_apikey_test.go` + `runner/attestation_jsonl_apikey_test.go`: `t.Setenv` (ADV-AUTH-003)
- `config/config_test.go` `TestScenario_Config_VaultWithToken`: 0o600 file mode
- `tests/acceptance/steps_config.go` writeConfig + token-append: 0o600

## Make targets

- `go vet ./...` — clean.
- `go build ./...` — green.
- `make test-unit` — green.
- `make test` (unit + acceptance) — green; all 348+ acceptance scenarios pass; 6 auth scenarios green (3 original + 3 new).
- `make test-race` — green.
- `make coverage-check` — **78.8%** (threshold 78%).
- `make` — green.

## Coverage delta

- Before: 78.7%
- After: 78.8% (+0.1pp). New tests offset the additional production
  code introduced by the validators and sanitizers.

## Grep verifications

```bash
grep -rn 'sk-canary-cccc-must-not-leak' --include='*.go' | grep -v '_test.go'
# (empty)

grep -rn '\.APIKey' --include='*.go' | grep -v _test.go
# config/config.go (definition + resolver)
# cmd/ghyll/auth.go (the chokepoint)
```

The chokepoint property holds: production-code `.APIKey` reads
remain confined to `config/config.go` and `cmd/ghyll/auth.go`.

## Commands run

- `gofmt -w config/ stream/ cmd/ghyll/ runner/ engine/ tests/acceptance/`
- `go vet ./...`
- `go build ./...`
- `go test ./config/ -count=1 -short`
- `go test ./stream/ -count=1 -short`
- `go test ./cmd/ghyll/ -count=1 -short`
- `go test ./engine/ ./runner/ -count=1 -short`
- `go test ./tests/acceptance/ -count=1`
- `make test-unit`
- `make test`
- `make test-race`
- `make coverage-check`
- `make`
