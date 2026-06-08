# Configuration

ghyll reads its configuration from `~/.ghyll/config.toml`. A complete example is provided at [`config/example.toml`](https://github.com/witlox/ghyll/blob/main/config/example.toml):

```bash
cp config/example.toml ~/.ghyll/config.toml
# Edit endpoints to match your SGLang instances
```

## Models

Each model requires an endpoint, dialect, and max context size:

```toml
[models.m25]
endpoint = "https://inference.internal:8001/v1"
dialect = "minimax"
max_context = 1000000

[models.glm5]
endpoint = "https://inference.internal:8002/v1"
dialect = "glm"
max_context = 200000
```

Available dialect families:

- `minimax` (MiniMax M2.5, M2.7, ...)
- `glm` (GLM-5, GLM-5.1, ...)
- `deepseek` (DeepSeek-V3, DeepSeek-Coder, ...)
- `qwen` (Qwen2.5-Coder, Qwen3-Coder, ...)
- `kimi` (Kimi 2.5, Kimi 2.6 — `moonshotai/Kimi-K2.5`, `moonshotai/Kimi-K2.6`)

See [ADR-007](../decisions/007-tier-based-routing.md) and
[ADR-v4-009](../decisions/v4/009-reasoning-content-excluded-from-checkpoint-hash.md)
for the Kimi `reasoning_content` exclusion rule.

**Tested backends.** The CSCS-hosted gateway at
`https://ai-gateway.svc.cscs.ch/v1` is the tested Kimi-K2.6 endpoint.
The K2-Thinking alias is intentionally deferred (ghyll sends no
`tool_choice`); operators pasting `dialect = "kimi-thinking"` get a
loud validation error rather than a silent fall-through to the
default minimax dialect.

### Endpoint authentication (api_key)

The optional `api_key` field forwards a Bearer token on every
chat-completion request:

```toml
[models.cscs-glm5]
endpoint = "https://ai-gateway.svc.cscs.ch/v1"
dialect = "glm"
max_context = 200000
api_key = "sk-..."   # optional; empty == no Authorization header
```

Resolution precedence (highest first):

1. `GHYLL_API_KEY_<MODEL>` — model-scoped env var. `<MODEL>` is
   the TOML model key upper-cased with any non-`[A-Z0-9_]` rune
   replaced by `_`. So `[models.cscs-glm5]` becomes
   `GHYLL_API_KEY_CSCS_GLM5`.
2. `GHYLL_API_KEY` — global fallback. Use this for single-tenant
   hosts; the scoped form for multi-endpoint setups (CSCS gateway +
   a local dev endpoint).
3. `cfg.Models[name].api_key` from TOML.

An empty resolution (no TOML field, no env var) emits no
`Authorization` header at all — preserving the zero-config
behaviour for endpoints that do not require auth.

**Redaction guarantees.**

- `ghyll config show` prints `api_key: <unset>` / `<env>` / `<toml>`
  for each model — provenance only, never the value or its length.
- 401 / 403 upstream responses are surfaced as a fixed
  `authentication failed` message; the raw response body is
  discarded before any logging in case the gateway echoed the
  Bearer header.
- The attestation JSONL audit trail (`.ghyll/attestations.jsonl`)
  and the engine sqlite store (`.ghyll/engine.db`) have no
  api_key-bearing column; regression tests grep both for sentinel
  tokens.
- Git commit trailers carry the model NAME (or `stamp_label`); the
  endpoint URL and api_key are never embedded.

A handoff to a different model resolves the key fresh against the
TARGET model — distinct keys per endpoint stay separated.

### Quantized variants

A quantized model in the SAME family uses the SAME dialect. Quantization (Q4/Q5/Q8, GGUF/AWQ/GPTQ) changes the endpoint backing the model, not the wire protocol — SGLang, vLLM, and llama.cpp all expose the OpenAI tool-call format that the dialect already speaks. Configure each quantized variant as its own `[models.<name>]` block with the endpoint pointing at the quantized backend:

```toml
[models.qwen-coder-q4]
endpoint = "http://localhost:11434/v1"   # llama.cpp server hosting Q4_K_M
dialect = "qwen"
max_context = 32000

[models.qwen-coder-q8]
endpoint = "http://localhost:11435/v1"   # vLLM hosting Q8
dialect = "qwen"
max_context = 65000
```

The dialect identifier is the same; the model name and endpoint disambiguate. The effective depth (and thus the appropriate routing tier) differs across quantizations — a Q4 of a 70B model is NOT the same as the full-precision model. Operators are responsible for setting `max_context` to match the backend's reported context window and for mapping each quantized variant to the right routing tier.

If a quantization vendor ships a non-standard tool-call format (rare but documented for some early GGUF tool-format experiments), a new dialect file is needed instead. Stick with the family dialect unless the wire format actually differs.

### Kimi 2.5 / 2.6 (CSCS gateway)

The Kimi family carries an extra `reasoning_content` field on every assistant turn. ghyll round-trips the field on the wire (assistant-only) so the next turn sees the prior reasoning trace; the field is excluded from the canonical checkpoint hash per [ADR-v4-009](../decisions/v4/009-reasoning-content-excluded-from-checkpoint-hash.md).

```toml
[models.kimi-k26-cscs]
endpoint = "https://ai-gateway.svc.cscs.ch/v1"
dialect = "kimi"
model = "moonshotai/Kimi-K2.6"   # literal id on the OpenAI `model` field
max_context = 200000
# api_key = "sk-..."  # CSCS Bearer; override via GHYLL_API_KEY_KIMI_K26_CSCS
```

The Kimi backend enforces a strict tool-call id shape: `functions.<name>:<index>`. A non-conformant id (e.g. a vanilla UUID) surfaces `ErrParseToolCall` and the session loop emits an operator-facing diagnostic that names the offending shape — the dispatch is REFUSED rather than silently executed. This is the documented sentinel of a wrong-version or misconfigured Kimi backend.

**Wire `model` field.** The optional `model` field is the literal string sent on the OpenAI `chat/completions` request body's `model` field. CSCS-style gateways route on this field, so paste the canonical mixed-case id (`moonshotai/Kimi-K2.6`) verbatim. Omitting `model` falls back to the dialect string (legacy behaviour for the 4 other dialects). Validation is case-insensitive on the `dialect` key, so `dialect = "moonshotai/Kimi-K2.6"` also loads (it is mapped to the `kimi` family) — but for clarity the recommended idiom is `dialect = "kimi"` + `model = "moonshotai/Kimi-K2.6"`.

## Routing

Controls automatic model selection:

```toml
[routing]
default_model = "m25"              # Fast tier model
deep_model = "glm5"                # Deep tier model (escalation target)
context_depth_threshold = 32000     # Escalate to deep tier above this
tool_depth_threshold = 5            # Escalate after N sequential tool calls
enable_auto_routing = true          # Set false to disable routing

# v2 gate-floor bridge: when a v2 gate declares MinTier (depth-sensitive
# clause requirement) at or above this rank, the dispatcher MUST land
# on deep_model and de-escalation is blocked.
#
# Rank scale (runner.DepthRank):
#   0 = NONE       1 = SHALLOW       2 = MOCKED       3 = REALISTIC
#
# Default = 2 (MOCKED): gates demanding MOCKED or REALISTIC depth
# escalate to deep_model regardless of context-depth signals.
# Set to 0 to disable the gate-floor mechanism entirely (legacy v1
# behavior).
gate_floor_escalate_at_rank = 2
```

Override with `--model` flag: `ghyll run . --model glm5`

## Memory

Controls checkpointing and drift detection:

```toml
[memory]
branch = "ghyll/memory"             # Git orphan branch name
auto_sync = true                    # Background push/pull
sync_interval_seconds = 60          # Sync frequency
checkpoint_interval_turns = 5       # Checkpoint every N turns
drift_check_interval_turns = 5      # Check drift every N turns
drift_threshold = 0.7               # Cosine similarity threshold

[memory.embedder]
model_url    = "https://huggingface.co/Xenova/gte-small/resolve/main/onnx/model.onnx"
model_sha256 = "398a29991324e0b383afa13375d681ced3079c83e097fb1ebd9290d7498523b3"  # optional: verify after download
model_path   = "~/.ghyll/models/gte-small.onnx"
dimensions   = 384
```

When `model_sha256` is set, `ghyll memory fetch-embedder` verifies
the downloaded hash before installing — a CDN compromise or HF
account takeover that doesn't reproduce the exact bytes is rejected
and the file is removed. The default URL is SHA-256 pinned in the
binary; setting your own `model_sha256` is only required when you
override `model_url`.

## Tools

Tool execution timeouts and web settings:

```toml
[tools]
bash_timeout_seconds = 30
file_timeout_seconds = 5
web_timeout_seconds = 30          # Timeout for web_fetch/web_search
web_max_response_tokens = 10000   # Max tokens returned by web_fetch (truncated with [truncated] marker)
web_search_backend = "duckduckgo" # Search backend (currently only duckduckgo)
prefer_ripgrep = true
```

## Sub-Agents

Controls for the `agent` tool --- model-dispatched sub-agents that run focused tasks:

```toml
[sub_agent]
default_model = "m25"             # Model for sub-agents (default: routing.default_model)
max_turns = 20                    # Maximum turn-loop iterations
token_budget = 50000              # Maximum total tokens consumed
timeout_seconds = 300             # Wall-clock timeout for entire sub-agent execution
```

Sub-agents run synchronously (the parent session blocks). They have access to all tools except `agent`, `enter_plan_mode`, and `exit_plan_mode`.

## Workflow

Controls project instruction loading:

```toml
[workflow]
instruction_budget_tokens = 2000  # Max tokens for instructions in the system prompt
fallback_folders = [".claude"]    # Folders to check if .ghyll/ is absent
```

Workflow files are loaded from `<repo>/.ghyll/` (or fallback folders):

```
.ghyll/
  instructions.md    # Project-level behavioral instructions
  commands/          # Slash command definitions (review.md, verify.md, etc.)
```

Global instructions from `~/.ghyll/instructions.md` are prepended; project instructions are appended (project has the "last word"). If combined content exceeds the token budget, global instructions are dropped first.

The four diamond roles (analyst / architect / implementer / integrator) are NOT loaded from `<repo>/.ghyll/roles/`. Per [ADR-008](../decisions/008-v2-fixed-roles-deprecate-runtime-workflow-roles.md), they are contracts embedded into the binary at build time from `specs/architecture/roles/*.md`. A `roles/` subdirectory under `.ghyll/` or `.claude/` is intentionally ignored — see [Workflow System](../internals/workflow.md) for the loader's behavior.

## Vault (optional)

Team memory search server:

```toml
[vault]
url = "https://vault.internal:9090"
token = "team-shared-secret"
```

Localhost vault (`http://localhost:9090`) doesn't require a token.

## Defaults

If a field is omitted, these defaults apply:

| Field | Default |
|-------|---------|
| `routing.default_model` | `"m25"` |
| `routing.context_depth_threshold` | `32000` |
| `routing.tool_depth_threshold` | `5` |
| `routing.gate_floor_escalate_at_rank` | `2` (MOCKED) |
| `memory.branch` | `"ghyll/memory"` |
| `memory.sync_interval_seconds` | `60` |
| `memory.checkpoint_interval_turns` | `5` |
| `memory.drift_threshold` | `0.7` |
| `tools.bash_timeout_seconds` | `30` |
| `tools.file_timeout_seconds` | `5` |
| `tools.web_timeout_seconds` | `30` |
| `tools.web_max_response_tokens` | `10000` |
| `sub_agent.max_turns` | `20` |
| `sub_agent.token_budget` | `50000` |
| `sub_agent.timeout_seconds` | `300` |
| `workflow.instruction_budget_tokens` | `2000` |
| `workflow.fallback_folders` | `[".claude"]` |
