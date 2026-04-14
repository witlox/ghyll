# Adversarial Review: Architecture Updates (2026-04-14)

Scope: Architecture-mode review of updated package-graph, data-structures, session-loop, enforcement-map, error-taxonomy, checkpoint-format, routing-logic for 7 new features.

---

## Finding: workflow/ token counting creates circular dependency
Severity: High
Category: Correctness > Implicit coupling
Location: specs/architecture/enforcement-map.md (inv 48), specs/architecture/data-structures.md (workflow/)
Spec reference: Invariant 48 — instruction budget enforced via dialect.TokenCount
Description: The enforcement map says workflow/loader.go enforces invariant 48 by calling `dialect.TokenCount` on combined instructions. But workflow/ depends only on config/ — it does NOT depend on dialect/. Calling dialect.TokenCount from workflow/ would introduce a workflow/ → dialect/ dependency, creating a new edge in the package graph that isn't declared. If you add it, workflow/ now depends on dialect/ which depends on config/ — no cycle, but it contradicts the stated "workflow/ depends on config/ only."
Evidence: enforcement-map.md says "Count tokens via dialect.TokenCount on combined text" but package-graph.md says workflow/ → config/ only.
Suggested resolution: Move budget enforcement to cmd/ghyll. workflow/loader.go returns the raw merged content without truncation. cmd/ghyll calls dialect.TokenCount on the merged content and truncates if needed. workflow/ stays pure (disk I/O + merge logic, no model awareness). Update enforcement map accordingly.

---

## Finding: Sub-agent blocks parent session — no concurrency specified
Severity: High
Category: Correctness > Specification compliance
Location: specs/architecture/session-loop.md (SUB-AGENT state)
Spec reference: Cross-context Flow 9, sub-agents.feature
Description: The session loop shows SUB-AGENT as a synchronous state entered from TURN step 7. While the sub-agent runs its turn-loop (potentially 20 turns with tool calls), the parent session is blocked — the user cannot type, no other tool calls execute, no checkpoint/drift checks run. This isn't called out as a design choice. For a sub-agent running `make test` (which could take minutes via bash), the entire session freezes.
Evidence: Session loop step 7: "If call.Name == 'agent' → SUB-AGENT (return result to context)". SUB-AGENT runs a full turn loop before returning to step 7. No mention of async or user-visible progress.
Suggested resolution: Two options: (A) Accept as design choice — sub-agents are synchronous tool calls like bash. Document it. Add a sub-agent timeout (total wall-clock, not just per-tool). (B) Run sub-agent in a goroutine, show progress indicators, allow user to cancel. Option A is simpler and consistent with the tool model. Either way, add a wall-clock timeout for the entire sub-agent execution.

---

## Finding: PlanMode in RouterInputs is dead — nothing reads it
Severity: Medium
Category: Correctness > Specification compliance
Location: specs/architecture/data-structures.md (RouterInputs), specs/architecture/routing-logic.md
Spec reference: Invariant 36 — plan mode is advisory
Description: PlanMode was added to RouterInputs with the comment "invariant 36: advisory only, does not affect routing decisions." The routing decision table has no row that checks PlanMode. No code path reads this field. It's dead data flowing through the system. If plan mode truly doesn't affect routing (invariant 36 says so), it shouldn't be in RouterInputs at all — it creates false expectation that routing cares about it.
Evidence: routing-logic.md decision table has 7 rows. None reference plan_mode. RouterInputs carries it anyway.
Suggested resolution: Remove PlanMode from RouterInputs. It's session state managed by cmd/ghyll, not routing state. The router doesn't need to know about it.

---

## Finding: Workflow token budget truncation order unclear
Severity: Medium
Category: Correctness > Specification compliance
Location: specs/architecture/enforcement-map.md (inv 48)
Spec reference: Invariant 48 — instruction budget enforced
Description: The enforcement map says "truncate from front (global first) if over budget." But the merged content is "global first, project appended" (invariant 47). Truncating from the front removes global instructions before project instructions. This is probably intentional (project is authoritative), but it means a 3000-token project instruction with a 2000-token budget would truncate ALL global instructions AND 1000 tokens of project instructions. The user might expect project instructions to always fit.
Evidence: If global = 500 tokens, project = 2500 tokens, budget = 2000: truncate from front removes all global (500) then 500 from project start. Project instructions are partially truncated from the beginning, losing early directives.
Suggested resolution: Clarify: if project instructions alone exceed the budget, truncate project from the end (keep the beginning which is more likely to contain critical directives). If combined exceeds but project alone fits, drop global entirely. Document this two-phase truncation.

---

## Finding: Edit tool mtime CAS has sub-second resolution risk
Severity: Medium
Category: Correctness > Edge cases
Location: specs/architecture/enforcement-map.md (inv 33), specs/architecture/data-structures.md (EditArgs)
Spec reference: Invariant 33 — CAS via mtime
Description: The CAS mechanism uses file modification time. On some filesystems (HFS+ on older macOS, ext3), mtime resolution is 1 second. If two edits happen within the same second, the CAS check passes but the second edit overwrites the first. This is particularly relevant for sub-agents — a fast sub-agent could make multiple edits to the same file within a second.
Evidence: Sub-agent reads file at t=1.100, parent reads at t=1.200. Sub-agent writes at t=1.300 (mtime = 1). Parent writes at t=1.800 (mtime check: 1 == 1, passes). Parent overwrites sub-agent's edit.
Suggested resolution: Use content hash (SHA256 of file bytes) instead of mtime for the CAS check. More expensive (read file twice) but immune to filesystem timestamp resolution. Alternative: use mtime + file size as a heuristic, accept the edge case as documented.

---

## Finding: Sub-agent inherits PlanMode but spec says plan mode tools excluded
Severity: Medium
Category: Correctness > Implicit coupling
Location: specs/architecture/session-loop.md (SUB-AGENT step 3 vs step 6c)
Spec reference: Invariant 36, 38
Description: SUB-AGENT step 3 says "If parent PlanMode active: include dialect.PlanModePrompt()". But step 6c says "enter_plan_mode / exit_plan_mode NOT available" for sub-agents. This is contradictory in spirit: the sub-agent inherits the parent's plan mode state but cannot toggle it. If plan mode means "think deeper," should a focused sub-agent dispatched for file exploration really be in plan mode? It wastes tokens on reasoning instructions for a mechanical task.
Evidence: Parent enters plan mode for architectural analysis. Dispatches sub-agent with task "grep for all TODOs." Sub-agent receives plan mode prompt "think deeply about each step" for a simple grep task.
Suggested resolution: Sub-agents should NOT inherit plan mode. They are focused, fast-tier tasks. Remove the plan mode inheritance from SUB-AGENT step 3. If the parent wants the sub-agent to reason deeply, it can say so in the task description.

---

## Finding: Workflow WorkflowConfig defined in two places
Severity: Low
Category: Correctness > Semantic drift
Location: specs/architecture/data-structures.md
Spec reference: None
Description: WorkflowConfig is defined twice in data-structures.md — once under config/ (as part of ToolsConfig block, lines ~96-99) and once under workflow/ (lines ~370-373). The definitions are slightly different: the config/ version has `FallbackFolders []string` while the workflow/ version only has `InstructionBudgetTokens`. This is confusing — which is authoritative?
Evidence: config/ section defines `WorkflowConfig{InstructionBudgetTokens, FallbackFolders}`. workflow/ section defines `WorkflowConfig{InstructionBudgetTokens}` with a note "Lives in config/ but documented here for context."
Suggested resolution: Remove the duplicate definition from workflow/ section. WorkflowConfig lives in config/ only. The workflow/ section should reference it, not redefine it.

---

## Finding: No tool definition schema for new tools exposed to the model
Severity: Low
Category: Correctness > Specification compliance
Location: specs/architecture/data-structures.md (tool/), specs/architecture/session-loop.md
Spec reference: Features: edit.feature, glob.feature, web.feature, plan-mode.feature, sub-agents.feature
Description: The model needs to know what tools are available (names, parameters, descriptions) — this is sent in the API request as a tools array. The architecture defines the Go types for tool arguments (EditArgs, GlobArgs, etc.) but doesn't define the JSON schema exposed to the model. For example, the model needs to know that edit_file takes `path`, `old_string`, `new_string` as parameters. This schema definition is missing from the architecture.
Evidence: Existing tools (bash, read_file, write_file, grep, git) presumably have tool schemas defined somewhere, but the architecture spec doesn't document the tool definition format. New tools need the same treatment.
Suggested resolution: Add a tool-definitions.md to specs/architecture/ listing the JSON tool schema for each tool (name, description, parameters). This is the contract between ghyll and the model — it belongs in architecture, not implementation.

---

## Finding: Web search backend configuration not validated
Severity: Low
Category: Robustness > Error handling
Location: specs/architecture/data-structures.md (ToolsConfig)
Spec reference: Assumption 19 — self-hostable search backend
Description: ToolsConfig has `WebSearchBackend string` defaulting to "duckduckgo". But there's no validation of this value and no error if it's set to something unsupported. The error taxonomy has no "unknown search backend" error.
Evidence: User sets `web_search_backend = "google"` in config.toml. ghyll starts successfully. Model calls web_search. Tool fails with an opaque error because there's no Google backend implementation.
Suggested resolution: Validate WebSearchBackend at config load time. Add ErrConfigValidation check for supported backends. Currently only "duckduckgo" is planned — fail fast if something else is configured.
