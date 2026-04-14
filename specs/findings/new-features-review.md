# Adversarial Review: New Feature Specs (2026-04-14)

Scope: Architecture-mode review of 7 new features (edit, glob, plan mode, sub-agents, resume, web, workflow).
Reviewed: domain-model.md, invariants.md, assumptions.md, failure-modes.md, cross-context/interactions.md, all 7 .feature files, ubiquitous-language.md.

---

## Finding: Sub-agent token bomb — no token budget on sub-agent context
Severity: High
Category: Robustness > Resource exhaustion
Location: specs/features/sub-agents.feature, specs/invariants.md (inv 40)
Spec reference: Invariant 40 caps turn count (20) but not token consumption
Description: A sub-agent has its own context manager but no specified token budget. The parent dispatches a task, the sub-agent runs 20 turns accumulating context, and each turn may include large tool results (file reads, grep output). With M2.5's 1M token context, a sub-agent could consume significant inference cost before hitting the turn limit. Invariant 40 bounds turns but not tokens. There is no specified maximum total token cost for a sub-agent invocation.
Evidence: Sub-agent reads 50 large files across 15 turns. Each file is 1000 lines. That's ~50K tokens of tool results alone, plus 15 model inference calls on M2.5. The parent only gets a final summary — most of those tokens are "wasted" from the parent's perspective.
Suggested resolution: Add a sub-agent token budget (configurable, default e.g. 50K tokens). Sub-agent's context manager enforces this like the parent's 95% rule. When budget is exhausted, sub-agent compacts or terminates with partial result. Add as invariant.

---

## Finding: Edit tool TOCTOU — file can change between read and write
Severity: High
Category: Correctness > Concurrency
Location: specs/features/edit.feature, specs/invariants.md (inv 33)
Spec reference: Invariant 33 says "edit is atomic" but the spec doesn't address concurrent modification
Description: The edit tool reads a file, finds old_string, computes the replacement, and writes the result. Between the read and write, another process (or a concurrent sub-agent tool call on the same file) could modify the file. The write would then clobber the concurrent change. Invariant 33 says "atomic" but the spec doesn't define what atomic means when another writer is involved.
Evidence: Parent model calls edit_file on main.go. Sub-agent also reads main.go and calls edit_file on a different line. Both read the same version. Both write — second write overwrites first.
Suggested resolution: Use file locking (flock) or compare-and-swap (read, check mtime/hash before write, fail if changed). Add scenario to edit.feature: "Edit fails if file was modified since tool read."

---

## Finding: Workflow conflict detection is undefined — "conflict" has no spec
Severity: High
Category: Correctness > Specification compliance
Location: specs/invariants.md (inv 47), specs/features/workflow.feature (scenario: "Project instructions override global on conflict")
Spec reference: Invariant 47 says "project-level directives take precedence on conflict"
Description: The spec never defines what constitutes a "conflict" between global and project instructions. The Gherkin scenario uses a clean example (verbose vs minimal logging) but real instructions are free-form markdown. How does ghyll detect that "Use verbose logging" and "Use minimal logging" are in conflict? It can't — these are natural language directives, not structured keys. The override semantics are unimplementable as specified.
Evidence: Global says "Always add docstrings." Project says "Be concise, skip documentation." Are these in conflict? No algorithm can determine this from markdown text.
Suggested resolution: Two options: (A) Drop conflict detection entirely — just concatenate global + project, project appended last so it has "last word" in the system prompt. The model resolves contradictions by favoring later instructions. (B) Use structured TOML sections for directives that need override semantics, and freeform markdown for the rest. Option A is simpler and honest about what's achievable.

---

## Finding: Sub-agent inherits role but spec doesn't say which role
Severity: Medium
Category: Correctness > Specification compliance
Location: specs/features/sub-agents.feature (scenario: "Sub-agent inherits project instructions"), specs/invariants.md (inv 38)
Spec reference: Invariant 38 says "system prompt (with project instructions + role if applicable)"
Description: The spec says sub-agents inherit project instructions. Invariant 38 says "role if applicable" but doesn't specify: does the sub-agent inherit the parent's active role? Get no role? Get a role based on the task? The Gherkin scenario only tests instruction inheritance, not role inheritance. A sub-agent inheriting the "analyst" role constraint ("do not write code") when dispatched to "fix this bug" would be broken.
Evidence: Parent is in analyst role. Dispatches sub-agent with task "fix the failing test." Sub-agent inherits analyst role → refuses to write code → returns useless result.
Suggested resolution: Sub-agents should NOT inherit the parent's active role by default. They get project instructions + bare dialect prompt. Add a scenario to sub-agents.feature: "Sub-agent does not inherit parent's active role." If needed, parent can include role hints in the task description.

---

## Finding: Web fetch response size unbounded — potential context bomb
Severity: Medium
Category: Robustness > Resource exhaustion
Location: specs/features/web.feature, specs/invariants.md (inv 45)
Spec reference: Invariant 45 says "plain text" but no size limit
Description: A web page converted to markdown can be enormous (Wikipedia articles, API reference pages, generated docs). The entire content becomes a tool result in the context window. No maximum response size is specified. A single web_fetch could inject 100K+ tokens into the parent's context, triggering compaction or exceeding budget.
Evidence: Model fetches a long API reference page. Page converts to 50K tokens of markdown. Context immediately jumps to 90%+ capacity, triggering proactive compaction that summarizes away the previous conversation to make room for one web page.
Suggested resolution: Add a configurable max_response_tokens for web_fetch (default e.g. 10K tokens). Truncate with "[truncated — showing first N tokens]" marker. Add scenario to web.feature.

---

## Finding: Plan mode + routing interaction unspecified
Severity: Medium
Category: Correctness > Implicit coupling
Location: specs/features/plan-mode.feature (scenario: "Plan mode persists across model switch"), specs/cross-context/interactions.md
Spec reference: Invariant 37 says plan mode survives compaction; scenario says it persists across model switch
Description: Plan mode appends dialect-specific planning instructions to the system prompt. When routing switches from M2.5 to GLM-5, the planning instructions need to change (each dialect has its own). The scenario says "GLM-5 system prompt contains GLM-5's planning instructions" but the interaction flow for this isn't mapped. Is it the dialect's responsibility to check the plan mode flag? The session's? Where does the flag live during handoff?
Evidence: Plan mode active on M2.5. Context depth triggers escalation. Handoff creates checkpoint, switches to GLM-5. Who rebuilds the system prompt with GLM-5's planning instructions + the active role?
Suggested resolution: Add to cross-context Flow 2 (context-depth escalation): "If plan mode is active, handoff summary includes plan mode flag. New dialect's system prompt is built with planning instructions." Plan mode flag should be in the checkpoint metadata so it survives handoff.

---

## Finding: Session resume doesn't record which session was resumed
Severity: Medium
Category: Correctness > Missing negatives
Location: specs/features/resume.feature, specs/domain-model.md (Session Resume)
Spec reference: Resume spec says "new session ID" but doesn't link to predecessor
Description: When resuming, a new session starts with a new ID. The checkpoint from the new session has a fresh parent hash chain. There's no metadata linking the new session to the one it resumed from. This breaks traceability — you can't reconstruct "session B was a continuation of session A" from checkpoints alone.
Evidence: Developer resumes 3 times across a morning. Checkpoints show 3 independent sessions. No way to know they were a logical continuation. Team memory search returns fragments without continuity.
Suggested resolution: Add a `resumed_from` field to the first checkpoint of a resumed session, containing the session_id and checkpoint hash of the source. Add scenario: "Resume records predecessor session in first checkpoint."

---

## Finding: .claude/ fallback loads CLAUDE.md but spec doesn't map role/command structure
Severity: Medium
Category: Correctness > Specification compliance
Location: specs/features/workflow.feature (scenario: "Fallback to .claude/ when .ghyll/ absent")
Spec reference: Invariant 51 says "attempts .claude/ (and similar known folders)"
Description: The `.claude/` folder has a different structure than `.ghyll/`. Claude Code uses `CLAUDE.md` (not `instructions.md`), `roles/` exists but commands are in `commands/` with different semantics (they're prompt templates with tool references). The fallback scenario only tests loading `CLAUDE.md` as instructions, but doesn't specify whether `.claude/roles/` and `.claude/commands/` are also loaded, or whether only the top-level CLAUDE.md is treated as instructions.
Evidence: Repo has `.claude/CLAUDE.md` (workflow router), `.claude/roles/analyst.md`, `.claude/commands/verify.md`. Ghyll falls back to `.claude/`. Does it load roles? Commands? Or just CLAUDE.md as a flat instruction blob?
Suggested resolution: Define the mapping explicitly. Proposal: `.claude/CLAUDE.md` → loaded as instructions. `.claude/roles/` → loaded as roles (same format). `.claude/commands/` → loaded as commands (same format). Add scenarios for each.

---

## Finding: Glob tool missing scenario for symlink handling
Severity: Low
Category: Correctness > Edge cases
Location: specs/features/glob.feature
Spec reference: Invariant 35 says "every path exists at time of call"
Description: The glob spec doesn't address symlinks. Should glob follow symlinks? If a symlink points outside the workspace (e.g., to `/etc/`), should it be included in results? Given that Tarn handles sandboxing, this may not be a security issue, but it affects correctness (broken symlinks would violate invariant 35 — the target doesn't exist).
Evidence: Workspace contains `src/config -> /etc/app/config` (symlink). Glob returns it. Model calls read_file on it. Tarn blocks (outside sandbox) or allows (if whitelisted). Either way, glob reported a path that might not be usable.
Suggested resolution: Add scenario: "Glob does not follow symlinks outside workspace" or "Glob skips broken symlinks." Decide on policy and document.

---

## Finding: Edit tool — no scenario for empty new_string (deletion)
Severity: Low
Category: Correctness > Edge cases
Location: specs/features/edit.feature
Spec reference: Invariant 34 (ambiguity check)
Description: The edit tool spec doesn't address the case where new_string is empty — effectively deleting the matched text. This is a valid and useful operation (removing a code block) but could be surprising. Should an empty new_string be allowed?
Evidence: Model calls edit_file with old_string="// TODO: remove this\n" and new_string="". Is this a valid edit or an error?
Suggested resolution: Add scenario: "Edit with empty new_string deletes the matched text." This is a feature, not a bug — document and test it.

---

## Finding: Sub-agent checkpointing — sub-agent turns not checkpointed
Severity: Low
Category: Correctness > Observability gaps
Location: specs/features/sub-agents.feature, specs/cross-context/interactions.md (Flow 9)
Spec reference: None — sub-agent checkpoint behavior unspecified
Description: The parent session creates checkpoints at intervals. Sub-agent execution is a single tool call from the parent's perspective. The sub-agent's internal turn-loop (potentially 20 turns with tool calls) is not checkpointed. If the sub-agent crashes or is killed mid-execution, all its work is lost with no recovery point.
Evidence: Sub-agent runs 15 turns of file exploration, crashes on turn 16. No checkpoint exists for turns 1-15. Parent receives an error. Work is completely lost.
Suggested resolution: Accept this as a design trade-off (sub-agents are cheap, fast-tier, and focused — loss is low-impact) and document it. Or: create a single checkpoint when the sub-agent completes (not per-turn), capturing its final result. Add to assumptions.md.

---

## Finding: Slash command content not subject to instruction budget
Severity: Low
Category: Robustness > Resource exhaustion
Location: specs/invariants.md (inv 48, 49)
Spec reference: Invariant 48 (instruction budget), Invariant 49 (slash commands are user messages)
Description: Invariant 48 limits instructions + role overlay to a token budget. But invariant 49 says slash commands inject as user messages, not system prompt content. This means slash command content bypasses the instruction budget entirely. A large command file could inject thousands of tokens as a user message without any budget check.
Evidence: User creates `/full-review` command file with 5000 tokens of detailed review criteria. Types `/full-review`. 5000 tokens injected as a user message, no budget check.
Suggested resolution: This is probably fine — user messages are already part of the context window and subject to compaction. But add a note that slash commands are subject to normal context management, not instruction budget.
