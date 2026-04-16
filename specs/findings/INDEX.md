# Adversarial Findings
Last sweep: 2026-04-14
Status: COMPLETE (spec + architecture + implementation + tier-routing, all resolved)

## Summary
| Severity | Count | Resolved | Open |
|----------|-------|----------|------|
| Critical | 0     | 0        | 0    |
| High     | 8     | 8        | 0    |
| Medium   | 14    | 13       | 1    |
| Low      | 12    | 11       | 1    |

## Open findings
| # | Title | Severity | Category | Location | Status |
|---|-------|----------|----------|----------|--------|
| ADV-3 | ModelName in API request sends dialect family | Medium | Correctness | cmd/ghyll/session.go | Accepted — pre-existing, tracked |
| ADV-4 | deep_model == default_model silently disables routing | Low | Correctness | config/config.go | Accepted — edge case |

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

## Resolved findings — tier-routing review (ADR-007)
| # | Title | Severity | Resolution | Resolved in |
|---|-------|----------|------------|-------------|
| ADV-1 | Old dialect="glm5" silently gets minimax | High | normalizeDialect() maps legacy strings | cmd/ghyll/session.go |
| ADV-2 | No dialect string validation | Medium | validate() rejects unknown dialects | config/config.go |
| ADV-5 | Row 6 de-escalation dead code when DeepModel="" | Low | Added canEscalate guard to Row 6 | dialect/router.go |

## Resolved findings — architecture review (9)
| # | Title | Severity | Resolution | Resolved in |
|---|-------|----------|------------|-------------|
| 13-21 | (see architecture-review.md) | Various | All resolved | Various |

## Resolved findings — spec review (12)
| # | Title | Severity | Resolution | Resolved in |
|---|-------|----------|------------|-------------|
| 1-12 | (see new-features-review.md) | Various | All resolved | Various |
