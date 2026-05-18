# Validation pass 8 — adversarial review of phase 8 work

Cold-context adversarial pass on phase 8 (v1↔v2 routing bridge,
DeepSeek + Qwen dialects, quantization docs, commit-attribution
stamp). Three parallel adversaries, 45 findings total.

**Severity distribution:** 1 Critical, 12 High, 21 Medium, 11 Low.

**Per user direction:** fix all findings, no deferrals.

Adversary numbering preserved: `Rn` for router-bridge, `Dn` for
dialects, `Sn` for commit-stamp.

---

## Critical

### R1 — §7.1 silent laundering: gate-floor no-ops when DeepModel unset
`dialect/router.go:57-64`. When `cfg.DeepModel == ""` or
`DeepModel == DefaultModel`, `canEscalate` is false → gate-floor row
skipped → falls through to `Action: "none"`. A v2 gate demanding
REALISTIC runs on the fast tier with no signal — exactly what
gates.md §7.1 forbids ("never laundered through the largest
available model").

**Fix:** new `Action: "gate_unsatisfiable"` returned when
`gateFloorActive && !canEscalate`. Session loop refuses to dispatch
and surfaces §7.1 attestation path.

---

## High

### R2 — Bridge silently disabled when `GateFloorEscalateAtRank > 3`
`dialect/router.go:61`. Out-of-range threshold → `GateFloor >= threshold`
never true → mechanism off. **Fix:** `validate()` rejects `< 0` or
`> 3`.

### R3 — Untyped `GateFloor int` accepts -1 or 99; no parity with DepthRank
`dialect/router.go:7-27`. **Fix:** clamp to valid range in `Evaluate`;
out-of-range returns `Action: "invalid"` with a Reason.

### R4 — `applyDefaults` can't distinguish unset from explicit zero
`config/config.go:157-163`. Operator-explicit `=0` overwritten by
default `=2`. **Fix:** use `*int` pointer so nil = unset.

### R5 — Bridge precondition `ActiveModel == DefaultModel` lets third model survive
`dialect/router.go:62`. **Fix:** when `gateFloorActive && ActiveModel != DeepModel`,
force escalate to DeepModel regardless of current model.

### R7 — Locked-model trumps gate-floor without §7.1 attestation
`dialect/router.go:51-54`. **Fix:** when `ModelLocked && gateFloorActive`,
return `Action: "gate_locked_conflict"`.

### D1 — Config validator rejects "deepseek" and "qwen" (front door closed)
`config/config.go:231-248`. `knownDialects` whitelist doesn't include
the new families. **Fix:** add deepseek+qwen families and all
variants to the whitelist; update the error message.

### D2 — TokenCount byte/rune mismatch for CJK/emoji
`dialect/{deepseek,qwen,glm,minimax}.go` TokenCount. `len()` returns
bytes; the ratio assumes chars. CJK content underestimates → false
negative on compaction → overflow.

**Fix:** `utf8.RuneCountInString` + recalibrated ratio. Document the
approximation; not safe for non-Latin content without rounding down.

### D3 — `resolveDialect` falls through to MiniMax on unknown dialect
`cmd/ghyll/session.go:219-227` + `subagent.go:64-68`. Typos / future
variants silently map to MiniMax. **Fix:** return error on unknown.

### S1 — sanitizeTrailerValue weaker than the project standard; lets U+2028/U+2029 through
`tool/commit.go:140-152`. Unicode line separators slip past the
control-char strip. **Fix:** also strip U+0085, U+2028, U+2029; or
escape via `\uXXXX` per `runner/sanitize.go`.

### S2 — Sanitizer silently mutates Ghyll-* values; audit trail lies
`tool/commit.go:129-130, 140-152`. **Fix:** REJECT (don't strip) on
any control char for the Ghyll-* values. `Ghyll-Version` and
`Ghyll-Model` come from the project's own pipeline; loud failure is
the right policy.

### S3 — trailerKeyOK rejects `_` (blocks Co_Authored_By and similar)
`tool/commit.go:69-84`. **Fix:** add `_` (and `.`) to the allowed set.

### S4 — validateExtraTrailer misses `\v`, `\f`, NEL, U+2028/U+2029
`tool/commit.go:96-98`. Asymmetry with sanitizeTrailerValue. **Fix:**
reject any `unicode.IsControl(r)` for the entire trailer line.

---

## Medium

### R6 — /deep override silently no-ops while gate-floor active; no Reason field
`dialect/router.go:60-69`. **Fix:** add `RoutingDecision.Reason`
typed enum (gate-floor, deep-override, backfill, context-depth,
tool-depth, steady-state).

### R8 — Threshold > 0 clause forecloses NONE-rank semantic
`dialect/router.go:61`. Confusing dual-purpose `=0`. **Fix:** named
`gate_floor_disabled bool` companion field (handled by the
pointer-default in R4 — explicit zero == disabled).

### R9 — No hysteresis on gate-floor flapping
`dialect/router.go:48-99`. Alternating arrows thrash the model.
**Fix:** doc-only for now; flag for session-loop layer (phase 10) to
implement hysteresis. Track in outstanding work.

### R10 — No test pins the §7.1 single-tier-laundering bug
`dialect/router_test.go`. **Fix:** add test that asserts R1 behavior.

### R11 — Tests don't go through `applyDefaults`
`dialect/router_test.go`. **Fix:** add an integration test asserting
the default value after `config.Load`.

### D4 — `normalizeDialect` not future-proof (no prefix-based fallback)
`cmd/ghyll/session.go:239-242`. **Fix:** switch to
`strings.HasPrefix`-based detection so new variants normalize
automatically.

### D5 — `resolveDialect` doesn't validate empty Dialect
`cmd/ghyll/session.go`. **Fix:** require non-empty Dialect at
`NewSession`; or emit a loud warning if defaulting.

### D6 — System prompt injects unsanitized workdir
`dialect/*.go` SystemPrompt funcs. Newlines / ANSI in path inject
into prompt. **Fix:** sanitize workdir at the dialect boundary
(strip control chars + ANSI; reject if pathological).

### D7 — HandoffSummary with zero-value Checkpoint produces misleading "continuing from..."
`dialect/{deepseek,qwen}.go:94-96`. **Fix:** add a guard at
`handleHandoff` and have HandoffSummary skip the framing when
checkpoint is zero-value.

### D8 — `BuildMessages` byte-identical across four dialects
`dialect/*.go`. **Fix:** extract `buildOpenAIMessages` shared helper
in `parse.go`; each dialect's wrapper calls it.

### D9 — Token-count constants `+10`/`+4` uncalibrated for new dialects
`dialect/{deepseek,qwen}.go:81-91`. **Fix:** document the source;
add ratio-sanity tests.

### D10 — No tests exercise deepseek/qwen dispatch paths
`cmd/ghyll/session_test.go:498-517`. **Fix:** extend the normalize
table; add `TestScenario_Session_ResolveDialectDeepSeek/Qwen`.

### D11 — Dialect tests don't assert ratio correctness or multibyte safety
`dialect/dialect_test.go`. **Fix:** add CJK input case + ratio-bound
assertions for each TokenCount.

### S5 — CommitOptions slices not defensively copied (TOCTOU)
`tool/commit.go:105-136, 158-178`. **Fix:** snapshot ExtraTrailers
and Paths at function entry.

### S6 — Whitespace-only trailing on body breaks trailer-block delimiter
`tool/commit.go:106-108, 124`. **Fix:**
`strings.TrimRightFunc(opts.Message, unicode.IsSpace)`.

### S7 — No test that git actually parses the trailers
`tool/commit_test.go:171-193`. **Fix:** add
`git log --format='%(trailers:key=Ghyll-Model,valueonly)'` assertion.

### S8 — HasPendingChanges conflates staged/unstaged/untracked
`tool/commit.go:210-230`. **Fix:** split into `HasStagedChanges`
(`git diff --cached --quiet`) and `HasUnstagedChanges` (or use
`-uno` to ignore untracked).

### S9 — `writeAndStage` test helper uses `sh -c "echo ..."` (injection-prone)
`tool/commit_test.go:158-169`. **Fix:** `os.WriteFile`.

### S10 — HasPendingChanges error swallowed at call site
`tool/commit.go:218-230, commit_test.go:205`. **Fix:** return typed
`PendingStatus` enum (Unknown, Clean, Staged, Unstaged); forces
callers to handle the Unknown case.

### S11 — BuildCommitMessage has no upper bound on Message / trailers
`tool/commit.go:105-136`. POSIX ARG_MAX risk. **Fix:** cap fields;
truncate with marker per `runner/sanitize.go` pattern.

---

## Low

### R12 — `RoutingDecision` carries no reason
`dialect/router.go:30-35`. Already covered by R6.

### R13 — `validate()` doesn't reject `< 0` (collapses into R2 fix).

### R14 — No test pins gate-floor precedence over /deep / backfill / tool-depth
`dialect/router_test.go`. **Fix:** add precedence test using the new
Reason field (R6).

### R15 — `singleTierConfig` test docstring contradicts §7.1 after R1 fix
`dialect/router_test.go:192-198`. **Fix:** update comment.

### D12 — `dialect/doc.go` is empty
`dialect/doc.go`. **Fix:** populate with the seven-function contract,
family-name canonical list, and normalization rule.

### D13 — Subagent dispatch doesn't log dialect resolution
`cmd/ghyll/subagent.go:51-68`. **Fix:** share `resolveDialect`
between session and subagent.

### D14 — DeepSeek/Qwen HandoffSummary may double-instruct
`dialect/{deepseek,qwen}.go:94-96`. **Fix:** doc-only; document the
intended policy in `dialect/doc.go`.

### D15 — `parseOpenAIToolCalls` discards underlying JSON error
`dialect/parse.go:14-19`. **Fix:** wrap via `%w`.

### S12 — `git commit -m <multi-KB>` instead of `-F -`
`tool/commit.go:168, 187`. **Fix:** switch to `-F -` with stdin.

### S13 — Empty-value trailers allowed but semantics undocumented
`tool/commit.go:88-100`. **Fix:** require ": " (colon-space) after
key in validator.

### S14 — GitCommit has no caller-layer serialization documented
`tool/commit.go:158-208`. **Fix:** doc-only; "caller must serialize."

### S15 — Missing test coverage on SignOff, AllowEmpty, Paths
`tool/commit_test.go`. **Fix:** add three integration tests.

---

## Highest-risk areas

1. **§7.1 spec compliance violations across the bridge (R1, R5, R7)** — three independent ways for a depth-sensitive gate to land on the wrong tier without operator signal. R1 is the central one.
2. **Front-door config blocking (D1)** — phase 8 slice B is unreachable from documented config until the validator accepts the new family names.
3. **TokenCount byte/rune mismatch (D2)** — silent compaction-threshold failure for CJK/emoji content; pre-existing in glm/minimax too.
4. **Trailer-sanitizer scope (S1, S2, S4)** — sanitizer strips a too-narrow set, mutates rather than rejects, validator missing classes.

## Remediation plan

No deferrals. Order chosen so structural fixes don't churn later changes:

1. Add `RoutingDecision.Reason` typed enum (R6, R12) — touched by many later fixes.
2. Router bridge: §7.1 attestation outcomes (R1, R5, R7), validation/clamping (R2, R3, R4), test additions (R10, R11, R14, R15).
3. Config: add deepseek+qwen to whitelist (D1), pointer-based default for gate-floor (R4).
4. Dialects: workdir sanitization (D6), TokenCount rune-based (D2, D9), shared buildOpenAIMessages (D8), parse error-wrap (D15), doc.go (D12, D14), tests (D10, D11).
5. Dispatch: resolveDialect returns error on unknown (D3, D5, D13), prefix-based normalize (D4), HandoffSummary zero-checkpoint guard (D7).
6. Commit stamp: strict sanitization (S1, S2, S4), trailer key set expansion (S3), TOCTOU/validate fields (S5, S6, S11), test additions (S7, S15), HasPendingChanges split + typed (S8, S10), helper safety (S9), stdin invocation (S12), trailer format normalize (S13, S14).
