# Dialect Modules

Each supported model has a dedicated dialect file with concrete functions. There are no shared interfaces --- each dialect is hand-tuned for its model's training, tool-calling format, and attention characteristics.

## Function Signatures

Every dialect exports the same set of functions:

| Function | Purpose |
|----------|---------|
| `SystemPrompt(workdir)` | Generate the system prompt for the model |
| `BuildMessages(msgs, sysPrompt)` | Format messages for the OpenAI-compatible API |
| `ParseToolCalls(raw)` | Parse tool calls from model response |
| `CompactionPrompt()` | Return the instruction for context compaction |
| `TokenCount(msgs)` | Estimate token count for a message list |
| `HandoffSummary(cp, recent)` | Format context for handoff to this model |

## MiniMax M2.5 (`dialect/minimax_m25.go`)

The fast tier. Handles 80% of routine coding tasks.

- **Token estimation**: ~4 characters per token
- **Max context**: 1,000,000 tokens
- **Compaction prompt**: General-purpose summary instruction
- **System prompt**: Concise, action-oriented

## GLM-5 (`dialect/glm5.go`)

The deep tier. Handles complex reasoning and multi-step debugging.

- **Token estimation**: ~3 characters per token (slightly less efficient tokenizer)
- **Max context**: 200,000 tokens
- **Compaction prompt**: DSA-aware --- emphasizes preserving structural decisions and rationale
- **System prompt**: Encourages step-by-step reasoning

## Adding a New Dialect

To add support for a new model (e.g., Kimi K2):

1. Create `dialect/kimi_k2.go` with all six functions
2. Add the dialect name to `resolveDialect()` in `cmd/ghyll/session.go`
3. Add model config in `config.toml`
4. Recompile

No interface changes needed. Each dialect is independent.

## Why Not Interfaces?

The abstraction tax is real. A generic provider interface forces all models through the same code path, losing model-specific optimizations: custom system prompts tuned to training, model-specific tool-calling format parsing, compaction prompts that account for attention characteristics. See [ADR-001](../decisions/001-architecture.md) for the full rationale.
