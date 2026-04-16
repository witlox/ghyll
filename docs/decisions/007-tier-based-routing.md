# ADR-007: Tier-Based Routing with Dialect Families

Date: April 2026
Status: Accepted

## Context

Ghyll's router hardcodes specific model identifiers (`"m25"` for MiniMax M2.5 and `"glm5"` for GLM-5) in its escalation/de-escalation logic. When new model versions ship (GLM 5.1, MiniMax M2.7), this creates a forced choice: either rename identifiers (breaking existing configs) or add new dialect files that duplicate the existing ones verbatim.

The root issue is conflation of three distinct concerns:

1. **Routing tier** — fast vs. deep, determined by context depth, tool depth, and user override.
2. **Model identity** — the user-chosen name for a configured endpoint (e.g., `"m27"`, `"glm51"`).
3. **Dialect family** — the set of functions for prompt formatting, tool-call parsing, token counting, and compaction, determined by the model's API contract, not its version number.

## Decision

### 1. Router operates on tiers, not model names

The routing decision table references two config fields:

| Field | Meaning | Example |
|-------|---------|---------|
| `routing.default_model` | Fast tier model | `"m27"` |
| `routing.deep_model` | Deep tier model | `"glm51"` |

Escalation targets `deep_model`. De-escalation targets `default_model`. The router never mentions a concrete model name in code.

### 2. Dialect families replace versioned dialect identifiers

Each `ModelConfig` specifies a dialect family string:

| Family | API contract | Files |
|--------|-------------|-------|
| `"minimax"` | OpenAI-compatible, Lightning Attention characteristics | `dialect/minimax.go` |
| `"glm"` | OpenAI-compatible via SGLang, DSA attention characteristics | `dialect/glm.go` |

The dialect family determines system prompt tuning, compaction strategy, token counting ratio, and handoff summary format. Version-specific quirks (if any) can be handled internally via config fields on `ModelConfig` — no new file required for a point release.

### 3. Model names are user-chosen, not framework-imposed

Config entries are keyed by arbitrary names:

```toml
[models.m27]
endpoint = "https://inference.internal:8001/v1"
dialect = "minimax"
max_context = 1000000

[models.glm51]
endpoint = "https://inference.internal:8002/v1"
dialect = "glm"
max_context = 200000

[routing]
default_model = "m27"
deep_model = "glm51"
```

Users running older hardware can keep `default_model = "m25"` with `dialect = "minimax"` — the dialect functions are the same.

## Consequences

- Adding a new model version (e.g., M2.9) requires only a config change if the API contract is unchanged.
- Adding a genuinely new model family (e.g., DeepSeek V4) requires one new dialect file + recompilation.
- Existing configs must update `dialect = "minimax_m25"` → `dialect = "minimax"` and `dialect = "glm5"` → `dialect = "glm"`. This is a one-time migration.
- The router no longer needs modification for model upgrades — only for changes to the routing algorithm itself.
- A/B testing old vs. new versions of the same family is config-only: define both models, point `default_model` at the one you want to test.
