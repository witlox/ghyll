# Adversarial Findings
Last sweep: 2026-04-14
Status: COMPLETE (spec + architecture + implementation, all resolved)

## Summary
| Severity | Count | Resolved | Open |
|----------|-------|----------|------|
| Critical | 0     | 0        | 0    |
| High     | 7     | 7        | 0    |
| Medium   | 12    | 12       | 0    |
| Low      | 10    | 10       | 0    |

## Open findings
(none)

## Resolved findings — implementation review
| # | Title | Severity | Resolution | Resolved in |
|---|-------|----------|------------|-------------|
| 22 | Web fetch OOM — unbounded body read | High | io.LimitReader caps bytes before ReadAll | tool/web.go |
| 23 | Parent tool error not surfaced to context | High | Use Error if Output empty, matching subagent pattern | cmd/ghyll/session.go |
| 24 | Edit CAS narrow TOCTOU | Medium | Documented as inherent limitation of hash-based CAS | tool/edit.go |
| 25 | Instruction budget not enforced | Medium | Two-phase truncation in composedSystemPrompt | cmd/ghyll/session.go |
| 26 | Web search query not URL-encoded | Medium | url.QueryEscape replaces manual space replacement | tool/web.go |
| 27 | Glob multiple ** unsupported | Medium | Documented limitation in matchGlob comment | tool/glob.go |
| 28 | Sub-agent no compaction on context overflow | Medium | Detect context-length error, return partial result | cmd/ghyll/subagent.go |
| 29 | HOME not parameterized in session | Low | Added GlobalDir to SessionConfig, fallback to HOME | cmd/ghyll/session.go |
| 30 | ResumeRef clearing comment misleading | Low | Clarified comment | cmd/ghyll/session.go |
| 31 | Glob goroutine leak on timeout | Low | Context propagated to globImpl, WalkDir checks cancellation | tool/glob.go |

## Resolved findings — architecture review (9)
| # | Title | Severity | Resolution | Resolved in |
|---|-------|----------|------------|-------------|
| 13-21 | (see architecture-review.md) | Various | All resolved | Various |

## Resolved findings — spec review (12)
| # | Title | Severity | Resolution | Resolved in |
|---|-------|----------|------------|-------------|
| 1-12 | (see new-features-review.md) | Various | All resolved | Various |
