# Validation — adversarial-pass remediation on Kimi dialect impl (pass 1)

Adversarial-pass remediation across **20 findings** flagged by two
cold-context adversaries (`K-ADV-1..8`, `KIMI-CFG-1..6`,
`WIRE-1..3`, `CHKPT-1`, `CONFIG-1`, `ADR-1`). User's standing rule
is "no deferrals on adversarial findings"; every finding is
remediated in this pass. The deep insight across the 20 findings:
the previous Kimi pass shipped a contract surface (the
`functions.<name>:<index>` id rule, the `reasoning_content` round-
trip, the case-insensitive Kimi alias acceptance) that was
honoured ONLY by the unit / BDD tests — production code paths
silently bypassed every assertion. This pass wires the production
paths so the contract is enforced where it has to be: in the
session loop and on the wire.

## Surface changed

| Layer | File | Change |
|------|------|--------|
| Stream | `stream/client.go` | (a) `sseEvent.Delta.ReasoningContent` field + `Response.ReasoningContent` accumulator (WIRE-1 / K-ADV-2). (b) Late-chunk `existing.Type` merge updates non-empty Type values (K-ADV-7 / WIRE-3). (c) Comment block corrected to describe vLLM 0.6 first-chunk-omits-type behaviour accurately (WIRE-3). |
| CMD | `cmd/ghyll/session.go` | (a) Re-marshal `resp.ToolCalls` and run through `s.parseToolCalls` before dispatch — wires `dialect.KimiParseToolCalls` into the live streaming path so the id contract is enforced at runtime (K-ADV-1 / KIMI-CFG-3 / WIRE-2). (b) Append `ReasoningContent: resp.ReasoningContent` on the assistant Message so the next outbound turn round-trips it. (c) `normalizeDialect` now delegates to `config.CanonicalDialectFamily` — both layers consume the same source of truth (KIMI-CFG-1 / KIMI-CFG-6 / CONFIG-1). (d) `wireModelName` helper picks operator-set `Model` field over `Dialect` for the wire `model` field so the docs-promised "appears verbatim" contract is honest (KIMI-CFG-4). |
| CMD | `cmd/ghyll/subagent.go` | `ModelName` is now `wireModelName(modelCfg)` so sub-agents also honour the operator-set wire `model` literal (KIMI-CFG-4 symmetry). |
| Config | `config/dialect_families.go` (new) | Single source of truth: `KnownDialectFamilies`, `dialectAliases`, `permissivePrefixes`, `CanonicalDialectFamily` (case-folded lookup), `KnownDialectFamiliesList`. Resolves K-ADV-3, K-ADV-4, KIMI-CFG-1, KIMI-CFG-2, KIMI-CFG-6, CONFIG-1 by construction — the two whitelists cannot drift because there is only one whitelist. |
| Config | `config/config.go` | (a) Inline `knownDialects` map replaced by `CanonicalDialectFamily`; error message rendered via `KnownDialectFamiliesList` so config + session emit identical error UX (KIMI-CFG-6 / CONFIG-1). (b) `ModelConfig.Model` field added with `toml:"model,omitempty"` for the literal wire model id (KIMI-CFG-4). |
| Dialect | `dialect/helpers.go` | `buildOpenAIMessages` emits `content: null` for assistant turns that carry tool_calls but no content — symmetric across all 5 dialects, matches the OpenAI Chat Completions spec, no longer rejected by strict Kimi backends (K-ADV-8). |
| Memory | `memory/crypto_reasoning_test.go` | Tautological structural-copy test replaced by a teeth-bearing assertion that exercises `canonicalJSON` directly with two divergent reasoning payloads, asserting bytes are identical when reasoning is excluded AND inversely demonstrating that INCLUDING it would diverge (CHKPT-1 / K-ADV-5). |
| Docs | `docs/decisions/v4/009-reasoning-content-excluded-from-checkpoint-hash.md` | Added "Migration" section: no chain rewrite required, existing checkpoints verify byte-for-byte identical, prospective lock for future Checkpoint refactors (ADR-1). |
| Docs | `docs/operator-guide.md`, `docs/usage/configuration.md`, `config/example.toml`, `config/userhome/instructions.md` | Honest "wire model field" copy: the literal id sent on the OpenAI request is the optional `model` field, not the `dialect` field. Worked example now includes `model = "moonshotai/Kimi-K2.6"` (KIMI-CFG-4). |
| Tests | `dialect/kimi_test.go` | `TestKimi_BuildMessages_AssistantToolCallsEmptyContentEmitsNull` (K-ADV-8). |
| Tests | `stream/client_test.go` | `TestStream_SSEToolCallDelta_LateTypeMergeUpdatesEmptyType` (K-ADV-7 / WIRE-3). `TestStream_SSEReasoningContent_AccumulatesIntoResponse` (K-ADV-2 / WIRE-1). |
| Tests | `cmd/ghyll/session_test.go` | `TestKimi_NormalizeDialect_AndConfigLoadAgree` (KIMI-CFG-1/6 reciprocity). `TestScenario_Session_KimiTurn_RejectsNonConformantToolCallID` (K-ADV-1 / KIMI-CFG-3 / WIRE-2 — end-to-end through SSE). `TestScenario_Session_KimiTurn_PreservesReasoningContent` (K-ADV-2 / WIRE-1 end-to-end). `TestScenario_Session_KimiTurn_SendsLiteralWireModel` (KIMI-CFG-4 end-to-end). |
| Tests | `config/config_test.go` | `TestConfig_AcceptsMixedCaseKimiDialect` (K-ADV-3 / KIMI-CFG-2). `TestConfig_AcceptsBareKimiK2ShortForms` (K-ADV-4 / KIMI-CFG-1). `TestConfig_KnownDialectFamiliesList_StableOrdering` (KIMI-CFG-6 / CONFIG-1). |
| BDD | `specs/features/kimi.feature`, `tests/acceptance/steps_kimi.go` | New scenario: SSE parser reads inbound `reasoning_content` (K-ADV-2 / WIRE-1 — and the existing round-trip step now reads from `resp.ReasoningContent` instead of the step argument, closing K-ADV-6's "test cheats" criticism). New scenario: config.Load on the docs-example Kimi block + assertion on wire model id (KIMI-CFG-5). |

## Findings closed (with how)

| ID | How |
|----|-----|
| K-ADV-1 | `cmd/ghyll/session.go` re-marshals `resp.ToolCalls` and runs them through `s.parseToolCalls` before dispatch; on `ErrParseToolCall` (or any parse error) the session emits an operator-facing diagnostic via `RenderWarning` + `s.output` and refuses tool execution. `TestScenario_Session_KimiTurn_RejectsNonConformantToolCallID` drives the full path through a mock SSE server. |
| K-ADV-2 | `sseEvent.Delta.ReasoningContent` added + accumulated into `Response.ReasoningContent` in `parseSSEStream`; session loop propagates onto the appended assistant Message. `TestStream_SSEReasoningContent_AccumulatesIntoResponse` + `TestScenario_Session_KimiTurn_PreservesReasoningContent` lock end-to-end. |
| K-ADV-3 | `config.CanonicalDialectFamily` lowercases at the lookup boundary; `TestConfig_AcceptsMixedCaseKimiDialect` covers the docs-canonical literal `moonshotai/Kimi-K2.6` and `MOONSHOTAI/...` and `Kimi`. |
| K-ADV-4 | `dialectAliases` includes `kimi-k2.5` and `kimi-k2.6` short forms; `TestConfig_AcceptsBareKimiK2ShortForms` locks this against future regression. |
| K-ADV-5 | `crypto_reasoning_test.go` rewritten to exercise `canonicalJSON` directly with two divergent reasoning payloads. Both the equality assertion (rule honoured) AND the divergence assertion (rule violation WOULD diverge) are present — the rule has teeth. |
| K-ADV-6 | (a) The BDD round-trip step at `steps_kimi.go:167` now reads `lastAssistMsg.ReasoningContent` from `resp.ReasoningContent` (the wire-parsed Response) instead of the step argument — the "test cheats" criticism is closed. (b) The negative scenario now has a parallel session-level test `TestScenario_Session_KimiTurn_RejectsNonConformantToolCallID` that drives a real session through the SSE server and asserts the diagnostic surfaces via `s.output` AND names the required shape `functions.<name>:<index>`. |
| K-ADV-7 | `stream/client.go`'s tool-call merge path now updates `existing.Type` when the later chunk carries a non-empty type. `TestStream_SSEToolCallDelta_LateTypeMergeUpdatesEmptyType` exercises the late-chunk arrival. |
| K-ADV-8 | `buildOpenAIMessages` emits `content: nil` for assistant turns that carry tool_calls but no content. `TestKimi_BuildMessages_AssistantToolCallsEmptyContentEmitsNull` locks this AND verifies the boundary cases (non-empty content stays a string; empty content without tool_calls stays an empty string). |
| KIMI-CFG-1 | Subsumed by the dialect_families.go centralization — adding `kimi-k2.5` / `kimi-k2.6` is a single map-entry. `TestKimi_NormalizeDialect_AndConfigLoadAgree` pins the reciprocity property: every form one layer accepts the other layer accepts. |
| KIMI-CFG-2 | Same fix as K-ADV-3 — case-folded lookup in `CanonicalDialectFamily`. |
| KIMI-CFG-3 | Same fix as K-ADV-1 — `s.parseToolCalls` is now invoked in the session loop on every turn. |
| KIMI-CFG-4 | `ModelConfig.Model` field added; `wireModelName` helper picks `Model` over `Dialect`; the three `stream.NewClient` sites (session init, handoff, sub-agent dispatch) now call `wireModelName(modelCfg)`. Docs (operator-guide, configuration, example.toml, instructions.md) now show the canonical mixed-case literal under `model = "..."`. `TestScenario_Session_KimiTurn_SendsLiteralWireModel` asserts the wire body carries the literal verbatim. |
| KIMI-CFG-5 | New BDD scenario `Kimi config pasted from operator-guide.md docs loads successfully` drives `config.Load` on a TOML with the canonical mixed-case literal and asserts the loaded wire model id equals `moonshotai/Kimi-K2.6`. End-to-end through the real loader. |
| KIMI-CFG-6 | Both layers now consume `config.KnownDialectFamiliesList()` for the error-message "known families" list — there is only one list. `TestConfig_KnownDialectFamiliesList_StableOrdering` pins the canonical order so a future re-order doesn't drift the two UX surfaces. |
| WIRE-1 | Same fix as K-ADV-2. |
| WIRE-2 | Same fix as K-ADV-1 / KIMI-CFG-3. |
| WIRE-3 | Same fix as K-ADV-7. Plus the comment block at `stream/client.go:426-431` is rewritten to describe the actual behaviour (first-chunk-omits, later-chunks-honoured). |
| CHKPT-1 | Same fix as K-ADV-5. |
| CONFIG-1 | Same fix as KIMI-CFG-1 — single source of truth removes the drift hazard structurally. |
| ADR-1 | New `## Migration` section in ADR-v4-009 explicitly states: no chain rewrite required, existing checkpoints verify byte-for-byte identical, and the prospective-lock test lives in `memory/crypto_reasoning_test.go`. |

## Findings deferred — none

Every finding is remediated in this pass. The user's standing rule
("no deferrals on adversarial findings") is honoured.

## Tests added / changed

**New tests:**
- `TestStream_SSEToolCallDelta_LateTypeMergeUpdatesEmptyType`
- `TestStream_SSEReasoningContent_AccumulatesIntoResponse`
- `TestKimi_BuildMessages_AssistantToolCallsEmptyContentEmitsNull`
- `TestKimi_NormalizeDialect_AndConfigLoadAgree`
- `TestScenario_Session_KimiTurn_RejectsNonConformantToolCallID`
- `TestScenario_Session_KimiTurn_PreservesReasoningContent`
- `TestScenario_Session_KimiTurn_SendsLiteralWireModel`
- `TestConfig_AcceptsMixedCaseKimiDialect`
- `TestConfig_AcceptsBareKimiK2ShortForms`
- `TestConfig_KnownDialectFamiliesList_StableOrdering`

**Rewritten tests:**
- `TestMemory_CanonicalHash_StableAcrossReasoningSerialization`
  (tautological → teeth-bearing canonicalJSON divergence/equality
  exercise)
- BDD step `the model emits an assistant turn with reasoning_content ...`
  now reads from `resp.ReasoningContent` instead of the step argument

**New BDD scenarios:**
- `SSE parser reads inbound reasoning_content and surfaces it on the
  parsed Response`
- `A Kimi config pasted from operator-guide.md docs loads
  successfully`

## Verification

- `go vet ./...` — clean
- `gofmt -w` — clean (no files needed rewrite after the run)
- `make test-unit` — all packages pass
- `make test` — full test (acceptance included) passes, 119s
- `make test-race` — race detector clean
- `make coverage-check` — total 78.8% (above 78% threshold)

## Coverage delta

Before this pass: 78.7% (per the previous pass-0 baseline noted in
the implementer summary's "78.8%" claim — slight drift from the
mid-edit state).

After this pass: 78.8% — net neutral or slightly positive. The
remediation added meaningful production code (the parseToolCalls
wiring in session.go, the wireModelName helper, the SSE
ReasoningContent accumulator) BUT it also added matching coverage
via the new session-level and stream-level tests. The two cancel
out roughly equally; the threshold-floor of 78% is preserved.

## Structural simplification (orthogonal)

The dialect-family whitelist is now centralized in
`config/dialect_families.go`. Adding a new dialect family is a
single edit:
1. Append to `KnownDialectFamilies`.
2. Either add explicit aliases to `dialectAliases` OR add the
   family-name prefix to `permissivePrefixes`.

The prior surface required edits in two places (`config.go`'s
`knownDialects` map and `session.go`'s `normalizeDialect` switch
plus `isKimiDialect` helper). The integrator-review of THIS PR
already surfaced that the two whitelists had drifted on
`kimi-k2.5` / `kimi-k2.6` — a perfect demonstration of why the
single-source-of-truth pattern was the right structural fix rather
than just patching the two layers individually.
