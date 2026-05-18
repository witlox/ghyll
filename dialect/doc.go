// Package dialect provides per-family adapters between ghyll and
// self-hosted OpenAI-compatible model endpoints. ADR-001 forbids
// interfaces; each family is a set of seven concrete functions:
//
//	<Family>SystemPrompt(workdir) string
//	<Family>BuildMessages(msgs, systemPrompt) []map[string]any
//	<Family>ParseToolCalls(raw) ([]ToolCall, error)
//	<Family>PlanModePrompt() string
//	<Family>CompactionPrompt() string
//	<Family>TokenCount(msgs) int
//	<Family>HandoffSummary(cp, recentTurns) []Message
//
// Canonical families: glm, minimax, deepseek, qwen. The router
// (cmd/ghyll/session.go normalizeDialect) collapses variant strings
// (e.g. "deepseek-v3", "qwen2.5-coder") to family names via prefix
// matching so new variants don't need a code change.
//
// Quantized variants (Q4/Q8/AWQ/GPTQ) use the SAME dialect as the
// full-precision model unless the wire format differs. They are
// distinguished at the operator config layer via [models.<name>]
// endpoint mapping, not in this package. See
// docs/usage/configuration.md.
//
// Sanitizer policy (validation-pass-8 D6): SystemPrompt funcs pass
// `workdir` through cleanWorkdir before interpolation so a workdir
// containing newlines / ANSI escapes / control chars cannot inject
// instructions into the system prompt.
//
// Handoff policy (validation-pass-8 D7/D14): HandoffSummary funcs
// check for a zero-value Checkpoint (Turn==0 && Summary=="") and
// skip the "Continuing from checkpoint..." framing. The session-
// loop layer is responsible for not calling HandoffSummary with a
// fake checkpoint; the dialect guard is defense in depth.
package dialect
