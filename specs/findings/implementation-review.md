# Adversarial Review: Implementation (2026-04-14)

Scope: Implementation-mode review of all new code for 7 features.
Reviewed: tool/edit.go, tool/glob.go, tool/web.go, workflow/loader.go, cmd/ghyll/session.go, cmd/ghyll/repl.go, cmd/ghyll/subagent.go, cmd/ghyll/main.go, config/config.go, dialect/glm5.go, dialect/minimax_m25.go, memory/checkpoint.go, memory/crypto.go, memory/store.go, memory/sync.go.

---

## Finding: Web fetch response body not size-limited before read — potential OOM
Severity: High
Category: Robustness > Resource exhaustion
Location: tool/web.go:91
Spec reference: Invariant 45, FM-17
Description: `io.ReadAll(resp.Body)` reads the entire response body into memory before truncation. A malicious or large server response (e.g., 1GB file served as text/html) would allocate unbounded memory, potentially crashing the process. The truncation at line 103 only happens AFTER the full body is in memory.
Evidence: Server returns `Content-Type: text/html` with a 500MB body. `io.ReadAll` allocates 500MB. Truncation to maxTokens happens after.
Suggested resolution: Use `io.LimitReader(resp.Body, maxBytes)` where maxBytes = maxTokens * 4 + some margin. Read at most that amount. This bounds memory allocation before truncation.

---

## Finding: Sub-agent tool result error not surfaced correctly to parent context
Severity: High
Category: Correctness > Specification compliance
Location: cmd/ghyll/session.go:300-304
Spec reference: Invariant 38, sub-agents.feature
Description: In the parent session's `sendAndProcess`, when a tool result has an error, the error string is stored in `toolResult.Error` but the context message is populated with `toolResult.Output` only (line 300: `Content: toolResult.Output`). For most tools, on error `Output` is empty and `Error` has the message. This means tool errors get injected into context as empty strings. The model never sees the error.
Evidence: Model calls `read_file` on nonexistent path. ToolResult has `Error: "file not found"`, `Output: ""`. Context message gets `Content: ""`. Model thinks the file is empty.
Suggested resolution: In the tool result context injection, use `toolResult.Output` if non-empty, else `toolResult.Error`. (Note: subagent.go does this correctly at line 165-168 — the parent session.go does not.)

---

## Finding: Edit tool CAS has TOCTOU between hash check and rename
Severity: Medium
Category: Correctness > Concurrency
Location: tool/edit.go:112-126
Spec reference: Invariant 33
Description: The CAS check re-reads the file and compares SHA256 (line 112-118). If the hash matches, it renames the temp file (line 124). Between the hash check and the rename, another process could modify the file. The rename would then overwrite that change. This is a narrower TOCTOU window than the original (read→write) but still exists. On most Unix filesystems, `os.Rename` is atomic at the filesystem level, but the check-then-rename is not atomic as a unit.
Evidence: File hash matches at line 118. Another process writes between line 118 and 124. Rename at 124 clobbers the concurrent write.
Suggested resolution: This is an inherent limitation of CAS without file locking. The window is very narrow (microseconds). Accept as documented limitation. The spec's CAS approach with SHA256 is a best-effort guard, not a guarantee. For true atomicity, `flock()` would be needed but the spec explicitly chose hash-based CAS. Add a comment noting the narrow window.

---

## Finding: Instruction budget (invariant 48) not enforced in implementation
Severity: Medium
Category: Correctness > Specification compliance
Location: cmd/ghyll/session.go:525-551 (composedSystemPrompt)
Spec reference: Invariant 48 — instruction budget enforced
Description: The `composedSystemPrompt` method concatenates dialect base + global instructions + project instructions + role + plan mode without any token budget check. The architecture spec says cmd/ghyll should enforce the instruction budget using dialect.TokenCount with two-phase truncation. This enforcement is missing from the implementation.
Evidence: Project with 10,000-token instructions.md. All content goes into the system prompt. No truncation, no warning.
Suggested resolution: After composing the workflow portion (global + project + role), count tokens via `s.tokenCount`. If over `s.cfg.Workflow.InstructionBudgetTokens`, apply two-phase truncation per session-loop.md spec. Display warning.

---

## Finding: Web search query not URL-encoded — special characters break URL
Severity: Medium
Category: Correctness > Input validation
Location: tool/web.go:150
Spec reference: Invariant 44
Description: The search URL is built via `strings.ReplaceAll(query, " ", "+")`. This only handles spaces. Characters like `&`, `=`, `#`, `?`, `%` in the query would corrupt the URL structure. For example, searching for "C++ templates" would produce `C++templates` which is ambiguous.
Evidence: Query "error: file not found" becomes `error:+file+not+found` — the `:` is not encoded.
Suggested resolution: Use `url.QueryEscape(query)` from `net/url` instead of manual space replacement.

---

## Finding: Glob pattern with multiple ** segments unsupported
Severity: Medium
Category: Correctness > Edge cases
Location: tool/glob.go:179
Spec reference: Invariant 35
Description: `matchGlob` uses `strings.SplitN(pattern, "**", 2)` — it only handles one `**` in the pattern. A pattern like `src/**/test/**/*.go` would split at the first `**` and treat the rest as a suffix, incorrectly. The second `**` would be treated as literal text.
Evidence: `glob("src/**/test/**/*.go", dir)` — splits to prefix "src/" and suffix "test/**/*.go". The suffix "test/**/*.go" is passed to `filepath.Match` which doesn't support `**`, so it fails.
Suggested resolution: Either document that only one `**` is supported (acceptable limitation), or implement recursive splitting. Single `**` handles 95% of real-world patterns. Document the limitation.

---

## Finding: Sub-agent context may exceed model limit without compaction
Severity: Medium
Category: Robustness > Resource exhaustion
Location: cmd/ghyll/subagent.go:82-88
Spec reference: Invariant 41a
Description: The sub-agent context manager is created with `CompactThreshold: 0.9` but `CompactionCall` is nil (no compaction for sub-agents). If the sub-agent's context grows beyond the model's limit (e.g., reading many large files), the API call will fail with context_length_exceeded. The sub-agent has no reactive compaction path — the error returns as "model unreachable."
Evidence: Sub-agent reads 50 files, each 1000 lines. Context grows to 200K tokens. M2.5 has 1M context so this specific case is fine, but with enough tool results it's possible. The token budget (50K) should catch this first in most cases.
Suggested resolution: The token budget (invariant 41a) catches this in practice — 50K tokens is well below M2.5's 1M limit. However, if someone configures a high token budget (e.g., 500K) with a small model context, this could hit. Add a guard: if the sub-agent's stream call returns context_too_long, return a partial result instead of an opaque error. Low priority given the token budget guard.

---

## Finding: Workflow loader reads from HOME env var at session init — not testable
Severity: Low
Category: Correctness > Specification compliance
Location: cmd/ghyll/session.go:138
Spec reference: Invariant 47
Description: `filepath.Join(os.Getenv("HOME"), ".ghyll")` is hardcoded at session init. The unit tests in workflow/ correctly parameterize the global dir, but the session integration path always reads from the real HOME directory. This means session tests may pick up the developer's actual `~/.ghyll/instructions.md`, causing non-deterministic test behavior.
Evidence: Developer has `~/.ghyll/instructions.md` with custom content. Session tests pass locally but fail in CI where HOME is different.
Suggested resolution: Pass the global dir as a SessionConfig field instead of reading HOME at init. The workflow tests already do this correctly — session.go should follow the same pattern.

---

## Finding: ResumeRef cleared after first checkpoint but assigned before checkpoint runs
Severity: Low
Category: Correctness > Edge cases
Location: cmd/ghyll/session.go:512-514
Spec reference: Invariant 42
Description: `s.resumeRef` is set in NewSession (line 178) and cleared in `createCheckpoint` after first use (line 514). But the clear happens in `createCheckpoint` which may be called for a handoff or compaction before the first interval checkpoint. In that case, the handoff checkpoint gets the resumeRef (correct — it IS the first checkpoint) and subsequent checkpoints don't (also correct). The logic is actually fine, but the comment "Only include resumeRef in the first checkpoint" is misleading because the actual clearing is a side effect of createCheckpoint, not explicitly gated to the first call.
Evidence: Session resumes, handoff triggers immediately, handoff checkpoint gets resumeRef, interval checkpoint doesn't. Correct behavior.
Suggested resolution: The behavior is correct. Rename the comment to clarify: "resumeRef is cleared after first checkpoint creation (regardless of reason)."

---

## Finding: Glob WalkDir goroutine leak on timeout
Severity: Low
Category: Robustness > Resource exhaustion
Location: tool/glob.go:30-51
Spec reference: Invariant 16
Description: On timeout, the `select` returns the timeout result, but the goroutine running `globImpl` continues walking the filesystem. For very large directory trees, this goroutine could run for a long time after the caller has moved on. The goroutine cannot be cancelled because `globImpl` doesn't take a context.
Evidence: Glob on a huge NFS mount. Timeout fires after 5s. Goroutine continues walking for minutes.
Suggested resolution: Pass context into `globImpl` and check `ctx.Err()` in the WalkDir callback. Same pattern applies to `editFileImpl` and `webFetchImpl` (which already take context). Low priority — the goroutine is read-only and eventually terminates.
