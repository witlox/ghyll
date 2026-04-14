# Integration Report: 7 New Features (2026-04-14)

## Integration Points Examined

### 1. Handoff + Plan Mode + Workflow Instructions
**Flow:** User in plan mode → context depth triggers escalation → handoff to GLM-5 → system prompt rebuilt

**Mechanism:** `handleHandoff()` calls `resolveDialect()` (switches planModePrompt to GLM-5's) then creates a new context manager. But `composedSystemPrompt()` is NOT called during handoff — instead `handoffSummary()` provides the new context. The handoff messages don't include the composed system prompt with workflow instructions + role + plan mode.

**Finding: ISSUE — Workflow instructions lost on handoff**
Severity: High
When handoff occurs, a new context manager is created and populated with `handoffMsgs` from `s.handoffSummary(cp, recentTurns)`. The handoff summary is formatted by the target dialect and contains the checkpoint summary + recent turns. But the workflow instructions, active role, and plan mode overlay are NOT re-injected. The new context manager has no system message with workflow content.

Compare: at session INIT (line 154), `composedSystemPrompt()` is called and added as a system message. But `handleHandoff()` (line 464-467) only adds handoff messages — no `composedSystemPrompt()`.

**Impact:** After a model switch, the model loses all workflow instructions, role constraints, and plan mode prompt. A developer using the analyst role who triggers escalation to GLM-5 would find the model no longer follows analyst constraints.

**Resolution:** After populating handoff messages, inject `composedSystemPrompt()` as the first system message, or prepend it to the handoff summary. This requires adding one line after line 467.

---

### 2. Sub-agent + Workflow Instructions Budget
**Flow:** Parent dispatches sub-agent → sub-agent builds system prompt with workflow instructions

**Mechanism:** `RunSubAgent()` (subagent.go:64-72) directly concatenates `parentSession.wf.GlobalInstructions` and `parentSession.wf.ProjectInstructions` into the sub-agent system prompt. It does NOT apply the instruction budget check.

**Finding: ISSUE — Sub-agent bypasses instruction budget**
Severity: Medium
The parent session's `composedSystemPrompt()` applies instruction budget enforcement (two-phase truncation). But `RunSubAgent()` directly accesses `wf.GlobalInstructions` and `wf.ProjectInstructions` without budget checking. If instructions are large, the sub-agent's prompt could be oversized.

**Impact:** Sub-agent system prompt could exceed what the fast tier model handles well. Mitigated by M2.5's 1M context — unlikely to be a practical problem, but violates invariant 48 for sub-agent context.

**Resolution:** Extract the instruction concatenation + budget logic into a shared helper, or accept as design trade-off (sub-agents use fast tier with huge context).

---

### 3. Resume + Workflow Reload
**Flow:** Session starts with --resume → loads previous checkpoint → loads workflow from disk → plan mode restored

**Mechanism:** In `NewSession()`, workflow loading (line 138-151) happens BEFORE resume handling (line 157-187). The system prompt is built with `composedSystemPrompt()` at line 154, then resume adds a backfill message at line 173.

**Finding: OK — Correct ordering**
Workflow loads first, system prompt includes instructions, then resume backfill is added as a second system message. Plan mode is restored from checkpoint (line 177) but `composedSystemPrompt()` was already called without plan mode. However, `sendAndProcess()` calls `composedSystemPrompt()` on EVERY turn (line 249), so the plan mode overlay will be included from the first actual model call.

No issue.

---

### 4. Edit + Glob in Sub-agent Context
**Flow:** Sub-agent calls glob to find files, then edit_file on results

**Mechanism:** Both tools use `context.Background()` for their execution context. Sub-agent's `executeSubAgentTool()` creates a fresh `context.Background()` for each tool call (subagent.go:186). The edit tool's CAS mechanism uses SHA256 hash comparison.

**Finding: ISSUE — Sub-agent concurrent edits to same file as parent**
Severity: Low
If the parent model edits a file, then dispatches a sub-agent that also edits the same file, the edit CAS mechanism protects against concurrent modification. However, the parent is blocked during sub-agent execution (synchronous), so true concurrency between parent and sub-agent tools is impossible. This is safe by design.

No issue.

---

### 5. Web Fetch + Sub-agent
**Flow:** Sub-agent calls web_fetch → Tarn may block → retry with backoff → result returned to parent

**Mechanism:** `executeSubAgentTool()` uses the same retry logic as the parent (calls `tool.WebFetch` directly). The sub-agent's wall-clock timeout applies to the entire turn loop, not individual tool calls. A web_fetch retry cycle (3 attempts × exponential backoff = ~7 seconds) runs within the sub-agent's 300s wall-clock budget.

**Finding: OK — No issue**
Web retry is bounded, sub-agent timeout is generous. If Tarn blocks all attempts, the web_fetch returns an error, the sub-agent receives it and can continue with other tools.

---

### 6. Slash Commands + Plan Mode
**Flow:** User types `/review` → command content injected as user message → model responds → plan mode is still active

**Mechanism:** REPL (repl.go:97-109) handles slash commands by calling `sess.Turn(content)`. The `Turn()` method adds the content as a user message and calls `sendAndProcess()` which uses `composedSystemPrompt()` including plan mode overlay.

**Finding: OK — No issue**
Slash commands go through the normal turn flow, which includes plan mode in the system prompt.

---

### 7. Checkpoint V2 + Hash Chain + Resume
**Flow:** Session creates v2 checkpoints with plan_mode/resumed_from → syncs via git → new session resumes

**Mechanism:** `CanonicalHash()` (crypto.go) includes `plan_mode` and `resumed_from` in the hash computation. `SignCheckpoint()` signs the hash. `LatestByRepo()` queries by timestamp.

**Finding: ISSUE — V2 fields not persisted in SQLite**
Severity: High
The `Checkpoint` struct has `PlanMode bool` and `ResumedFrom *ResumeRef` fields (checkpoint.go). But the SQLite schema (store.go:41-66) does NOT have `plan_mode` or `resumed_from` columns. The `Append()` method (store.go:79-116) doesn't write these fields. The `scanCheckpoint()` method doesn't read them.

The v2 fields are included in the canonical hash (crypto.go:37-42), so the hash is correct. But the fields are lost when writing to and reading from the database. A resumed session's `LatestByRepo()` returns a checkpoint WITHOUT `PlanMode` or `ResumedFrom`, so plan mode is never restored from a checkpoint.

**Impact:** Invariant 37 (plan mode survives) is violated across sessions. Invariant 42 (resume loads checkpoint) partially works but loses plan_mode and resumed_from metadata.

**Resolution:** Add `plan_mode INTEGER DEFAULT 0` and `resumed_from TEXT` columns to the SQLite schema. Update `Append()` to write them. Update `scanCheckpoint()`/`scanCheckpointRow()` to read them.

---

### 8. Handoff + System Prompt Recomposition
**Flow:** Handoff creates new context manager → system prompt needs workflow + role + plan mode

**(Covered in finding #1 above — workflow instructions lost on handoff)**

---

### 9. Config Defaults + New Fields
**Flow:** User has old config.toml without [sub_agent] or [workflow] sections → ghyll starts

**Mechanism:** `applyDefaults()` in config.go sets defaults for new fields (sub_agent.max_turns=20, workflow.instruction_budget=2000, etc.). TOML parsing with `BurntSushi/toml` ignores unknown sections, and missing sections get zero values which are then defaulted.

**Finding: OK — No issue**
Backward compatible. Old config files work because defaults are applied for all new fields.

---

### 10. Workflow + Handoff + Role Persistence
**Flow:** Model in "analyst" role → handoff to GLM-5 → is the role still active?

**Mechanism:** `handleHandoff()` calls `resolveDialect()` which updates dialect functions but does NOT touch `s.activeRole` or `s.wf`. The role name persists. But since `handleHandoff()` doesn't call `composedSystemPrompt()` (finding #1), the role overlay is not in the new context.

**(Same root cause as finding #1)**

---

## Summary

| # | Integration Point | Status | Severity |
|---|------------------|--------|----------|
| 1 | Handoff + plan mode + workflow | ISSUE | High |
| 2 | Sub-agent + instruction budget | ISSUE | Medium |
| 3 | Resume + workflow reload | OK | — |
| 4 | Edit + glob in sub-agent | OK | — |
| 5 | Web fetch + sub-agent | OK | — |
| 6 | Slash commands + plan mode | OK | — |
| 7 | Checkpoint v2 + SQLite persistence | ISSUE | High |
| 8 | Handoff + system prompt | (Same as #1) | — |
| 9 | Config backward compat | OK | — |
| 10 | Workflow + handoff + role | (Same as #1) | — |

## Critical Findings

### INT-1: Workflow instructions, role, and plan mode lost on handoff (High)
`handleHandoff()` populates the new context with handoff messages only. It does not re-inject `composedSystemPrompt()`. After a model switch, the model has no workflow instructions, no role constraints, no plan mode overlay.

### INT-2: Checkpoint v2 fields not persisted in SQLite (High)
`PlanMode` and `ResumedFrom` are in the Go struct and canonical hash, but not in the SQLite schema. They are silently lost on database roundtrip.

### INT-3: Sub-agent bypasses instruction budget (Medium)
Sub-agent system prompt concatenation doesn't apply the two-phase truncation from composedSystemPrompt.
