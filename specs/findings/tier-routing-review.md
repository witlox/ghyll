# Adversarial Review: Tier-Based Routing Refactor (ADR-007)

Date: 2026-04-14
Scope: ADR-007 implementation — dialect families, tier-based routing, config changes
Reviewer: adversary

## Finding ADV-1: Old `dialect = "glm5"` config silently gets wrong dialect

Severity: High
Category: Correctness > Semantic drift
Location: `cmd/ghyll/session.go:resolveDialect()`, `cmd/ghyll/subagent.go:RunSubAgent()`
Description: The switch on `Dialect` field matched `"glm5"` before the refactor. After renaming to `case "glm"`, a user with `dialect = "glm5"` in their config falls through to `default:` and silently gets minimax functions applied to their GLM endpoint. Wrong system prompt, wrong token counting ratio (4 chars/token vs 3), wrong compaction strategy.
Evidence: `switch d { case "glm": ... default: // minimax }` — any unrecognized string gets minimax.
Resolution: **FIXED.** Added `normalizeDialect()` function that maps legacy strings (`"glm5"`, `"glm51"`, `"minimax_m25"`, `"minimax_m27"`) to family names. Used in both `resolveDialect()` and `RunSubAgent()`.

## Finding ADV-2: No dialect string validation

Severity: Medium
Category: Correctness > Missing negatives
Location: `config/config.go:validate()`
Description: The `Dialect` field was never validated. Typos like `dialect = "minimx"` are silently accepted and routed to the default (minimax) branch. Combined with ADV-1, this creates a class of silent misconfiguration bugs with no error and no warning.
Resolution: **FIXED.** Added dialect validation in `validate()` — rejects unknown dialect strings with a clear error message listing known values. Accepts both family names and legacy versioned names.

## Finding ADV-3: `ModelName` in API request sends dialect family string

Severity: Medium
Category: Correctness > Implicit coupling
Location: `cmd/ghyll/session.go:124,450`, `cmd/ghyll/subagent.go:78`
Description: The `"model"` field in the OpenAI API request body is set to `modelCfg.Dialect` (now `"minimax"` or `"glm"`). For SGLang single-model endpoints this is ignored. For vLLM multi-model deployments, this field selects the model — sending a family name instead of a model identifier breaks routing.
Resolution: **ACCEPTED.** Pre-existing issue (previously sent `"minimax_m25"` which was also not a real model path). Proper fix is adding a `ModelName` field to `ModelConfig` — tracked for future work, not blocking this refactor.

## Finding ADV-4: `deep_model == default_model` silently disables routing

Severity: Low
Category: Correctness > Missing negatives
Location: `config/config.go:validate()`, `dialect/router.go:37`
Description: Setting `deep_model = "m25"` (same as default) passes validation. The `canEscalate` guard in the router is false, silently disabling all escalation with no warning to the user.
Resolution: **ACCEPTED.** Edge case — user would have to deliberately set both to the same value. Single-tier operation (no deep_model set) is the intended way to disable escalation.

## Finding ADV-5: Row 6 de-escalation dead code when `DeepModel == ""`

Severity: Low
Category: Correctness > Edge cases
Location: `dialect/router.go:60-65`
Description: When `DeepModel` is empty (single-tier), Row 6 checks `ActiveModel == ""` which never fires. The `canEscalate` guard applied to rows 2-5 should also cover row 6 for symmetry.
Resolution: **FIXED.** Added `canEscalate` guard to Row 6.

## Summary

| Severity | Count | Fixed | Accepted |
|----------|-------|-------|----------|
| High     | 1     | 1     | 0        |
| Medium   | 2     | 1     | 1        |
| Low      | 2     | 1     | 1        |

All high-severity findings resolved. No blockers for merge.
