# Fidelity Audit: 7 New Features (2026-04-14)

## Audit Summary

| Feature | Spec Scenarios | Unit Tests | Acceptance Steps | Assertion Depth | Confidence |
|---------|---------------|------------|-----------------|-----------------|------------|
| edit_file | 12 | 11 | 0 (PENDING) | THOROUGH | MODERATE |
| glob | 13 | 13 | 0 (PENDING) | THOROUGH | MODERATE |
| web fetch/search | 14 | 14 | 0 (PENDING) | THOROUGH | MODERATE |
| plan mode | 14 | 0 | 0 (PENDING) | NONE | LOW |
| workflow | 28 | 15 | 0 (PENDING) | MODERATE | MODERATE |
| session resume | 10 | 0 | 0 (PENDING) | NONE | LOW |
| sub-agents | 18 | 0 | 0 (PENDING) | NONE | LOW |

## Critical Finding: 109 godog scenarios are PENDING

`tests/acceptance/acceptance_test.go` loads `specs/features/*.feature` with `Strict: false`.
All 109 new scenarios are loaded by godog but have NO step definitions — every step is
undefined/pending and passes silently. This inflates the acceptance test pass count.

**No acceptance step registration functions exist for:**
- edit.feature (12 scenarios)
- glob.feature (13 scenarios)  
- plan-mode.feature (14 scenarios)
- sub-agents.feature (18 scenarios)
- resume.feature (10 scenarios)
- web.feature (14 scenarios)
- workflow.feature (28 scenarios)

**Impact:** The acceptance test suite reports 100% pass but 109 of those are phantom passes.

## Per-Feature Analysis

### edit_file (tool/edit.go)

**Unit tests (tool/edit_test.go): 11 tests — THOROUGH**

| Scenario | Test | Depth | Notes |
|----------|------|-------|-------|
| Successful single replacement | TestScenario_Edit_SuccessfulReplacement | THOROUGH | Verifies replacement + untouched content |
| Old string not found | TestScenario_Edit_OldStringNotFound | THOROUGH | Checks error message + file unchanged |
| Ambiguous match | TestScenario_Edit_AmbiguousMatch | THOROUGH | Checks error message + file unchanged |
| File does not exist | TestScenario_Edit_FileNotFound | MODERATE | Checks error, doesn't verify specific message |
| Preserves permissions | TestScenario_Edit_PreservesPermissions | THOROUGH | Verifies exact 0644 permission |
| Multiline replacement | TestScenario_Edit_Multiline | THOROUGH | Verifies multiline content present |
| Empty new_string (delete) | TestScenario_Edit_EmptyNewString | THOROUGH | Verifies deleted + remaining content |
| Concurrent modification | TestScenario_Edit_ConcurrentModification | MODERATE | Tests old_string absent, not true CAS race |
| No-op same content | TestScenario_Edit_NoOpSameContent | THOROUGH | Verifies file unchanged |
| Timeout | TestScenario_Edit_Timeout | SHALLOW | Context cancel, not true FS slowness |
| CAS SHA256 | TestScenario_Edit_CAS_SHA256 | MODERATE | Sequential edits work, but doesn't prove SHA256 vs mtime |
| Edit cleans up temp file on failure | — | NONE | No test |

**Missing:** temp file cleanup test (spec scenario exists, no unit test).

### glob (tool/glob.go)

**Unit tests (tool/glob_test.go): 13 tests — THOROUGH**

| Scenario | Test | Depth |
|----------|------|-------|
| All Go files recursively | TestScenario_Glob_AllGoFiles | THOROUGH |
| Test files only | TestScenario_Glob_TestFilesOnly | THOROUGH |
| Subdirectory | TestScenario_Glob_Subdirectory | THOROUGH |
| No matches | TestScenario_Glob_NoMatches | THOROUGH |
| Directory wildcard | TestScenario_Glob_DirectoryWildcard | THOROUGH |
| Invalid path | TestScenario_Glob_InvalidPath | THOROUGH |
| Sorted by mtime | TestScenario_Glob_SortedByMtime | THOROUGH |
| Broken symlink | TestScenario_Glob_BrokenSymlink | THOROUGH |
| External symlink | TestScenario_Glob_ExternalSymlink | THOROUGH |
| Hidden files | TestScenario_Glob_HiddenFiles | THOROUGH |
| Empty pattern | TestScenario_Glob_EmptyPattern | THOROUGH |
| Valid workspace symlink | TestScenario_Glob_ValidWorkspaceSymlink | THOROUGH |
| Timeout | — | NONE |

**Missing:** Timeout scenario has no dedicated test (Glob uses context-based timeout added in adversary fix, but no test verifies it).

### web fetch/search (tool/web.go)

**Unit tests (tool/web_test.go): 14 tests — THOROUGH**

All 14 spec scenarios have corresponding tests with real HTTP servers (httptest).
Retry behavior verified with atomic counters. Truncation verified. Binary rejection verified.

| Gap | Detail |
|-----|--------|
| io.LimitReader | Not specifically tested (the truncation test covers the behavior indirectly) |
| url.QueryEscape | Not tested with special characters (tests use simple queries) |

### plan mode (dialect/ + cmd/ghyll)

**Unit tests: 0 dedicated — NONE**

Plan mode is implemented (PlanModePrompt functions exist, session.go wires it) but has zero
dedicated tests. The dialect_test.go doesn't test PlanModePrompt. No session-level test
verifies that /plan changes the system prompt, that plan mode survives compaction, or that
/fast clears it. All 14 scenarios are pending in acceptance.

**Invariant coverage:**
| Invariant | Enforced in code | Tested |
|-----------|-----------------|--------|
| 36 (advisory) | Yes — no tool gate | No |
| 37 (survives compaction) | Yes — session flag | No |

### workflow (workflow/loader.go)

**Unit tests (workflow/loader_test.go): 15 tests — MODERATE**

Core loading, merging, fallback, and override behaviors are tested. Tests use real
filesystem fixtures (t.TempDir). 

| Gap | Detail |
|-----|--------|
| Budget enforcement | NOT tested — composedSystemPrompt truncation logic has no unit test |
| Role switch | NOT tested — SwitchRole() has no unit test |
| Slash command injection | NOT tested — REPL command injection path has no unit test |
| Instruction budget 3 scenarios | Spec has 3 budget scenarios, 0 tests |
| Role not found scenario | Spec has it, no test |

### session resume (cmd/ghyll)

**Unit tests: 0 dedicated — NONE**

Resume is implemented (main.go --resume flag, session.go backfill logic, store.LatestByRepo)
but has zero tests. The LatestByRepo store method has no test. The backfill injection logic
has no test. The resumeRef checkpoint linking has no test.

### sub-agents (cmd/ghyll/subagent.go)

**Unit tests: 0 dedicated — NONE**

The sub-agent is fully implemented (RunSubAgent, executeSubAgentTool, tool exclusion,
turn limit, token budget, wall-clock timeout) but has zero tests. This is the most complex
new feature with the least test coverage.

## Invariant Enforcement Summary

| # | Invariant | Enforced | Tested |
|---|-----------|----------|--------|
| 33 | Edit CAS atomic | Yes (SHA256) | Partial (no true race test) |
| 34 | Edit match unambiguous | Yes | Yes |
| 35 | Glob workspace-local | Yes (symlink checks) | Yes |
| 36 | Plan mode advisory | Yes (no gate) | No |
| 37 | Plan survives compaction | Yes (session flag) | No |
| 38 | Sub-agent isolated | Yes (fresh context) | No |
| 39 | Sub-agent shares lockfile | Yes (no AcquireLock) | No |
| 40 | Sub-agent turn limit | Yes (counter) | No |
| 41 | Sub-agent no nesting | Yes (tool exclusion) | No |
| 41a | Sub-agent token budget | Yes (accumulator) | No |
| 42 | Resume loads checkpoint | Yes (summary inject) | No |
| 43 | Resume requires checkpoint | Yes (nil check) | No |
| 44 | Web retry on transient | Yes (3x loop) | Yes |
| 45 | Web size-bounded | Yes (LimitReader + truncate) | Yes |
| 46 | Instructions survive compaction | Yes (system prompt) | No |
| 47 | Project authoritative | Yes (concat order) | Yes |
| 48 | Instruction budget | Yes (composedSystemPrompt) | No |
| 49 | Slash commands user msg | Yes (REPL inject) | No |
| 50 | Role switch non-destructive | Yes (prompt replace) | No |
| 51 | Fallback folders | Yes (loader) | Yes |

## Priority Actions

1. **HIGH: Wire 109 godog acceptance steps** — The biggest gap. Either set `Strict: true` and write step definitions, or convert key scenarios to unit tests. Current phantom passes are misleading.

2. **HIGH: Add plan mode tests** — 0 tests for a feature that modifies the system prompt on every turn. At minimum: test PlanModePrompt returns non-empty, test composedSystemPrompt includes plan mode text, test /plan and /fast toggle.

3. **HIGH: Add sub-agent tests** — 0 tests for the most complex new feature. At minimum: test tool exclusion (agent/plan mode not in tool set), test turn limit termination, test token budget termination.

4. **MEDIUM: Add resume tests** — Test LatestByRepo query, test backfill injection, test resumeRef in first checkpoint.

5. **MEDIUM: Add workflow budget/role tests** — Test two-phase truncation, test SwitchRole, test role-not-found error.

6. **LOW: Fill minor unit test gaps** — Edit temp cleanup, glob timeout, web URL encoding with special chars.
