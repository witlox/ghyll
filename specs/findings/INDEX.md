# Adversarial Findings
Last sweep: 2026-04-14
Status: COMPLETE (spec review + architecture review, all resolved)

## Summary
| Severity | Count | Resolved | Open |
|----------|-------|----------|------|
| Critical | 0     | 0        | 0    |
| High     | 5     | 5        | 0    |
| Medium   | 8     | 8        | 0    |
| Low      | 7     | 7        | 0    |

## Open findings
(none)

## Resolved findings — spec review
| # | Title | Severity | Resolution | Resolved in |
|---|-------|----------|------------|-------------|
| 1 | Sub-agent token bomb — no token budget | High | Added invariant 41a, sub-agent token budget (50K default) | invariants.md, sub-agents.feature |
| 2 | Edit tool TOCTOU — concurrent modification | High | CAS via content SHA256 hash, not mtime | invariants.md, edit.feature |
| 3 | Workflow conflict detection undefined | High | Concatenate global+project, project last, model resolves | invariants.md, workflow.feature |
| 4 | Sub-agent role inheritance unspecified | Medium | Sub-agents are role-free, inherit instructions only | invariants.md, sub-agents.feature |
| 5 | Web fetch response size unbounded | Medium | 10K token default max, truncation with marker | invariants.md, web.feature |
| 6 | Plan mode + routing interaction gap | Medium | Plan mode flag in checkpoint metadata, rebuilt on handoff | cross-context/interactions.md |
| 7 | Session resume no predecessor link | Medium | Added resumed_from field | domain-model.md, resume.feature |
| 8 | .claude/ fallback structure unmapped | Medium | Explicit mapping: CLAUDE.md→instructions, roles/, commands/ | invariants.md, workflow.feature |
| 9 | Glob symlink handling unspecified | Low | Exclude broken/external symlinks | invariants.md, glob.feature |
| 10 | Edit empty new_string scenario missing | Low | Deletion scenario added | edit.feature |
| 11 | Sub-agent turns not checkpointed | Low | Accepted trade-off (assumption 15a) | assumptions.md |
| 12 | Slash command bypasses instruction budget | Low | Commands are user messages, subject to compaction | invariants.md |

## Resolved findings — architecture review
| # | Title | Severity | Resolution | Resolved in |
|---|-------|----------|------------|-------------|
| 13 | workflow/ token counting circular dependency | High | Budget enforcement moved to cmd/ghyll; workflow/ returns raw content | enforcement-map.md, data-structures.md, session-loop.md |
| 14 | Sub-agent blocks parent session | High | Documented as design choice (synchronous like bash); wall-clock timeout added (300s default) | session-loop.md, data-structures.md |
| 15 | PlanMode in RouterInputs is dead data | Medium | Removed from RouterInputs; plan mode is session state in cmd/ghyll | data-structures.md, routing-logic.md, session-loop.md |
| 16 | Workflow token budget truncation order unclear | Medium | Two-phase: project fits → drop global; project exceeds → truncate project from end | session-loop.md |
| 17 | Edit tool mtime CAS sub-second resolution risk | Medium | Changed to content SHA256 hash instead of mtime | invariants.md, enforcement-map.md |
| 18 | Sub-agent inherits PlanMode contradicts exclusion | Medium | Removed plan mode from sub-agent system prompt | session-loop.md |
| 19 | WorkflowConfig defined in two places | Low | Removed duplicate; single definition in config/ | data-structures.md |
| 20 | No tool definition schema for model | Low | Created tool-definitions.md with JSON schemas for all 12 tools | architecture/tool-definitions.md |
| 21 | Web search backend not validated | Low | Added ErrConfigUnknownBackend sentinel error | error-taxonomy.md |
