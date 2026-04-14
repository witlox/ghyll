# Adversarial Findings
Last sweep: 2026-04-14
Status: COMPLETE (new features review)

## Summary
| Severity | Count | Resolved | Open |
|----------|-------|----------|------|
| Critical | 0     | 0        | 0    |
| High     | 3     | 3        | 0    |
| Medium   | 4     | 4        | 0    |
| Low      | 4     | 4        | 0    |

## Open findings (sorted by severity)
(none)

## Resolved findings
| # | Title | Severity | Resolution | Resolved in |
|---|-------|----------|------------|-------------|
| 1 | Sub-agent token bomb — no token budget | High | Added invariant 41a, sub-agent token budget (50K default), scenario added | invariants.md, sub-agents.feature |
| 2 | Edit tool TOCTOU — concurrent modification | High | Changed inv 33 to compare-and-swap (mtime check), added scenario | invariants.md, edit.feature |
| 3 | Workflow conflict detection undefined | High | Replaced inv 47: concatenate global+project, project last, model resolves | invariants.md, workflow.feature |
| 4 | Sub-agent role inheritance unspecified | Medium | Updated inv 38: sub-agents are role-free, inherit instructions only | invariants.md, sub-agents.feature |
| 5 | Web fetch response size unbounded | Medium | Updated inv 45: 10K token default max, truncation with marker | invariants.md, web.feature |
| 6 | Plan mode + routing interaction gap | Medium | Updated Flow 2: plan mode flag in checkpoint metadata, rebuilt on handoff | cross-context/interactions.md |
| 7 | Session resume no predecessor link | Medium | Added resumed_from field to domain model, updated scenario | domain-model.md, resume.feature |
| 8 | .claude/ fallback structure unmapped | Medium | Updated inv 51 with explicit mapping, added 3 fallback scenarios | invariants.md, workflow.feature |
| 9 | Glob symlink handling unspecified | Low | Updated inv 35, added 2 symlink scenarios | invariants.md, glob.feature |
| 10 | Edit empty new_string scenario missing | Low | Added deletion scenario | edit.feature |
| 11 | Sub-agent turns not checkpointed | Low | Documented as accepted trade-off in assumption 15a | assumptions.md |
| 12 | Slash command bypasses instruction budget | Low | Clarified in inv 49: commands are user messages, subject to compaction not budget | invariants.md |
