# Validation pass 3 — adversarial review of runner step-3 work

Cold-context adversarial pass on the runner package after the
step-3 slices landed (arrow status derivation, transition refusal,
plus prior subprocess evaluator + runner core + no-todo-marker).
Three parallel adversary agents, 45 findings total consolidated
below.

**Severity distribution:** 3 Critical, 13 High, 20 Medium, 9 Low.

**Per user direction:** fix all findings, no deferrals.

---

## Critical

### F1 — Subprocess env inheritance leaks parent secrets to bindings
`runner/subprocess.go:170`. `cmd.Env = append(os.Environ(), ...)`
hands `ANTHROPIC_API_KEY`, `GHYLL_VAULT_TOKEN`, `SSH_AUTH_SOCK`,
ed25519 key paths, model endpoints — everything — to bindings via
`sh -c`. The "sandbox-external" comment is not license to leak by
default.

**Fix:** allowlist by default (PATH, HOME, LANG, LC_*, TMPDIR, USER,
SHELL, TERM). Add `WithInheritEnv(keys ...string)` for explicit
passthrough. Redact `*_KEY`, `*_TOKEN`, `*_SECRET`, `GHYLL_*`
from anything that does leak.

### F2 — Stdout cap never kills the subprocess
`runner/subprocess.go:244-256, 397-418`. `captureBuf.Write` returns
`len(p), nil` on overflow so `io.Copy` doesn't error; the binding
streams to a discarded sink for the full Timeout. The "kill once
the cap trips" comment is wrong — the kill block only runs after
Wait returns.

**Fix:** trigger an async killProcessGroup from `captureBuf.Write`
when overflow flips (guarded by sync.Once).

### F3 — `notodo` empty marker matches every line
`runner/notodo.go:162-209`. `markers: ["TODO", ""]` produces a
matcher that hits at position 0 of every line; `regexp.MustCompile("TODO|")`
or `strings.Index(lower, "")` both return 0. Every line becomes a
hit, every no-todo-marker clause fails universally.

**Fix:** reject any zero-length marker element in `coerceStringList`
or at the top of `EvaluateNoTodoMarker`.

---

## High

### F4 — `provisional` admits non-pass clauses (spec compliance)
`runner/arrow.go:177-186`. gates.md §7.2 demands "all evaluated
clauses are pass" as a precondition for provisional. Code returns
provisional whenever any clause has `AwaitingAttestation`, even
with another clause still pending.

**Fix:** before step 3, require non-attested clauses to all be
StatusPass. Otherwise fall through to in-progress.

### F5 — `insufficient-basis` is entirely unmodeled (spec compliance)
`runner/arrow.go:115-118 + runner.go:34-40`. gates.md §7.2 + §10
make insufficient-basis a peer of awaiting-attestation for the
provisional path. ClauseDeriveInput has no representation.

**Fix:** add `InsufficientBasis bool` to ClauseDeriveInput;
treat identically to AwaitingAttestation for status derivation.

### F6 — Threshold inclusivity ambiguous
`runner/arrow.go:149-150`. Code uses `>=` (at-or-above); docstring
says "above"; gates.md §7.3 ambiguous between "above" and "at or
above the declared severity threshold". Test at SeverityRank ==
threshold isn't pinned.

**Fix:** pin inclusive (`>=`) per the natural reading of "above
the threshold" + "harness default is medium" (so medium findings
block medium-threshold projects). Update docstring; add explicit
boundary test.

### F7 — `AwaitingAttestation` orthogonal to Status
`runner/arrow.go:178-186`. Doc admits the flag is "modeled here as
a flag rather than a new ClauseStatus value" but never restricts
which `Status` values it can coexist with. `{Status: StatusPending,
AwaitingAttestation: true}` shouldn't be possible per gates.md
§7.1 (awaiting-attestation transitions from running).

**Fix:** require `AwaitingAttestation ⇒ Status == StatusRunning` at
the top of DeriveArrowStatus (error or silent ignore — choose).

### F8 — Out-of-range ClauseStatus silently counted as pass
`runner/arrow.go:142-194`. `ClauseStatus(99)` matches no equality
switch and falls through every step. Empty-clauses defends; garbage
inputs do not. ClauseStatus.String() loudly says "invalid-clause-
status(99)" but DeriveArrowStatus is silent.

**Fix:** unrecognized status → unevaluated. Mirrors loud-corruption
stance of String().

### F9 — `runtime.Goexit` bypasses safeInvoke
`runner/runner.go:431-439`. `recover()` returns nil when goroutine
exits via Goexit; named returns stay at zero; Evaluate's goroutine
vanishes. No EvaluationRun, no audit trail.

**Fix:** document Goexit caveat (already done for goroutine panics +
stack overflow); consider parent-spawns-child pattern with a done
channel so the parent always produces an EvaluationRun.

### F10 — `Registry.Replace` returns unwrapped error
`runner/runner.go:206-221`. Docstring says "Returns
ErrConceptAlreadyRegistered" for not-registered case; code returns
a plain `fmt.Errorf`. Caller doing `errors.Is(err,
ErrConceptAlreadyRegistered)` for the documented case gets false.
Sentinel name semantically inverted.

**Fix:** add `ErrConceptNotRegistered`; Replace wraps it.
RegisterBuiltins fallback path should log unexpected Replace
failures, not silently discard.

### F11 — `Result{Unevaluated:true, Reason:""}` accepted
`runner/runner.go:96-107 + 402-426`. Docstring says Reason must be
non-empty when Unevaluated is true; nothing enforces. A buggy
evaluator returning `&Result{Unevaluated: true}` produces an
unevaluated status with no operator-readable justification.

**Fix:** `deriveEndStatus` flags `Unevaluated && Reason==""` as
ErrEvaluatorReturnNil-equivalent; flag `Unevaluated && Pass` as
invariant violation.

### F12 — case-insensitive marker matcher mis-slices on ToLower length change
`runner/notodo.go:185-208`. `bestStart/bestEnd` index into the
lowered string but slice the original — U+0130 "İ" (2 bytes) → "i"
(1 byte) yields wrong byte boundary, producing mojibake in the
hit.marker field.

**Fix:** track separate offset maps OR walk runes in original via
strings.EqualFold-based scanning.

### F13 — `matchesScope` not equivalent to bootstrap's recursive `**` matcher
`runner/notodo.go:215-253 vs bootstrap/modify.go:285-317`. Runner
only splits on first `**`; bootstrap recurses on suffix. Drift on
patterns like `src/**/foo/**/bar`. Operator-declared scope behaves
differently between init and runtime.

**Fix:** extract one canonical matcher into `internal/pathglob` (or
similar); both packages call it.

### F14 — PID-recycle race in killProcessGroup
`runner/subprocess.go:376-384`. After SIGTERM, polling
`syscall.Kill(-pgid, 0)` can succeed against a recycled PID/PGID,
then `SIGKILL` murders the wrong group.

**Fix:** refuse to signal if `cmd.ProcessState != nil` (process
already reaped). Better: route through `cmd.Wait()`'s completion
rather than signal-0 polling.

### F15 — exec.CommandContext races our killProcessGroup
`runner/subprocess.go:169, 221-242`. CommandContext installs its
own ctx-cancel handler that SIGKILLs the leader PID (not PGID);
our killer signals PGID with SIGTERM/grace/SIGKILL. Race; common
case: exec wins, leader dies, children survive as orphans.

**Fix:** `exec.Command` (not CommandContext) + rely on explicit
killProcessGroup. Or Go 1.20+ `Cmd.Cancel`.

### F16 — Stdin deadlock when binding partial-reads stdin
`runner/subprocess.go:192`. `bytes.NewReader` → exec copier
goroutine. Binding that `head -c 64`s a >64-byte payload blocks
the copier on EPIPE, blocks Wait → surfaces as timeout, not the
real "binding exited, copier blocked" cause.

**Fix:** own the stdin pump; close write-end and ignore EPIPE the
moment subprocess exits.

---

## Medium

### F17 — Negative SeverityRank and unbounded large rank unvalidated
`runner/arrow.go:106, 149-150`. `-1` is silently ignored; `MaxInt`
blocks. Off-by-one comment ("0=info, 5=critical" vs spec's 5-value
enum 0..4).

**Fix:** named constants SeverityInfo..SeverityCritical; validate
range; document.

### F18 — `TransitionRefusalKind` and `ArrowStatus` serialize inconsistently
`runner/transition.go:76-82`. Kind is string-typed alias → `%s`
bare; UpstreamStatus is int + String() → `%q` quoted. Future
refactor of Kind to int silently changes wire format.

**Fix:** pick one wire format; document; test it.

### F19 — `CheckTransition` accepts empty arrow IDs
`runner/transition.go:110-135`. Empty IDs flow into attestation
records as structural corruption.

**Fix:** reject empty IDs at entry; reject `BlockingClauses < 0`,
`InvalidatingGridVersion < 0`.

### F20 — `InvalidatingGridVersion == 0` accepted for invalidated kind
`runner/transition.go:64-69, 116-124`. Grid versions start at v1;
"v0" in error string is structurally meaningless.

**Fix:** reject `invalidatingGridVersion < 1` when status ==
Invalidated.

### F21 — Invalidated refusal drops BlockingClauses
`runner/transition.go:116-124`. Per gates.md §7.2, findings raised
before invalidation are retained tagged with grid-version. Setting
BlockingClauses=0 for invalidated misleads operators.

**Fix:** populate BlockingClauses for invalidated refusal too.

### F22 — BlockingClauses count conflates clauses + findings
`runner/arrow.go:154-156`. Return is `failCount + openBlockingFindings`,
but findings aren't clauses. Field name `BlockingClauses` is wrong.

**Fix:** split into `(status, blockingClauses, blockingFindings)` or
rename to `BlockingItems`.

### F23 — `DeriveArrowStatus` doesn't document thread-safety
`runner/arrow.go:129-200`. No copy, no mutex; caller-shared slices
race under future caching.

**Fix:** docstring states "callers must not mutate inputs
concurrently with this call".

### F24 — `ArrowStatusInProgress` is the zero value AND empty-clauses sentinel
`runner/arrow.go:29-33, 130-137`. ArrowStatus{} silently equals
in-progress. Also: gates.md §7.2 line 600 lists only `{complete,
provisional, unevaluated, blocked, invalidated}` — in-progress is
NOT in the spec's 5-status set.

**Fix:** introduce ArrowStatusUnset at index 0 (sticky-invalid).
Either get analyst sign-off to add `in-progress` to the spec OR
collapse in-progress into a runner-internal-only state never
surfaced in attestation.

### F25 — FindingStatusUnevaluated severity unchecked (correct, undocumented)
`runner/arrow.go:168-172`. Unevaluated finding propagates regardless
of SeverityRank; severity itself is unassigned per gates.md §7.3.

**Fix:** add comment documenting that SeverityRank is meaningless
when Status == FindingStatusUnevaluated.

### F26 — EvaluationRun exported fields invite post-return mutation
`runner/runner.go:263-274`. Doc says callers MUST NOT mutate; not
enforced. Result is a shared pointer; Result.Details is a shared
map.

**Fix:** deep-copy Result.Details inside Evaluate before constructing
the EvaluationRun.

### F27 — Clause.Args passed by reference; mutation leaks across invocations
`runner/runner.go:116-122 + notodo.go:40-111`. Built-in evaluators
only read today, but the contract allows mutation. Re-evaluation
of the same clause reuses the same map pointer.

**Fix:** shallow-clone Clause.Args at the Evaluate boundary before
invoking; document Args as evaluator-owned within the call.

### F28 — `defaultIDGen` not collision-free across processes
`runner/runner.go:447-454`. Counter resets on process restart;
concurrent processes (runner + vault replay) can collide at the
same wall-clock + counter.

**Fix:** per-process random suffix from crypto/rand at package init.

### F29 — `pending → unevaluated` transition is dead code
`runner/runner.go:66-76`. Map allows it; Evaluate never drives it
(always transitions to running first).

**Fix:** delete the edge OR wire a depth-below-required short-circuit.

### F30 — Skip-dir check fires on basename, not full path
`runner/notodo.go:80-89`. Legitimate `src/cli/build/` skipped
because basename "build" is in skip set. Inverse: `scope=vendor/**`
can't be scanned (basename "vendor" is in skip).

**Fix:** scope-overrides-skip-set OR check immediate-root-depth
only.

### F31 — `O_NOFOLLOW` not portable; non-Linux symlink errors propagate
`runner/notodo.go:289-298, 365-382`. `isSymlinkOpenError` only
treats ELOOP; FreeBSD historically returned EMLINK. Also
syscall.O_NOFOLLOW is Linux/BSD-specific; the "Lstat-guarded
fallback" comment doesn't exist in code.

**Fix:** accept EMLINK, EPERM too; OR Lstat-then-continue on
ModeSymlink (portable).

### F32 — bufio scanner cap (1 MB) trips on realistic minified files
`runner/notodo.go:309-353`. File cap is 4 MB, line cap is 1 MB.
A 1.2 MB minified bundle.js has no newlines → ErrTooLong → "line
too long" pseudo-hit → clause Pass=false. "Couldn't parse" should
not be a clause failure.

**Fix:** raise per-line buffer to file cap OR emit Unevaluated with
Reason="line too long" so operator triages rather than blocked.

### F33 — Forensic blobs durable; secrets in subprocess stderr land in attestation
`runner/subprocess.go:349-354, 304-306, 330-335`. Malformed JSON →
first 16KB stdout + 32KB stderr persisted into EvaluationRun.
If a binding echoes `$API_KEY` in stderr, the secret is committed
to the orphan branch on `ghyll memory sync`.

**Fix:** redact common secret patterns (regex on Bearer/sk-/etc.)
before persisting; OR hash-store the raw to a side table and put
the hash in Details.

### F34 — Zero Grace skips SIGTERM, straight to SIGKILL
`runner/subprocess.go:120-122, 376-384`. Constructor default-fills,
but `&BindingEvaluator{Grace: 0}` direct-construction bypasses.

**Fix:** clamp grace minimum (e.g., 100ms) in killProcessGroup
itself, not just at construction.

### F35 — Untyped JSON schema on stdout
`runner/subprocess.go:288-302`. `details` is any-typed; a binding
can balloon it to 15MB of nested data inside the 16MB cap.

**Fix:** enforce shallow schema after unmarshal; reject otherwise
as ReasonMalformedOutput.

### F36 — Open FDs leak to subprocess
`runner/subprocess.go:169-179`. SQLite handles, lockfiles, ONNX
model FDs can be inherited by exec'd binding. Memory-DB and
session-lockfile leakage is the bigger problem (lockfile held by
zombie binding survives runner crash).

**Fix:** document that cgo FD-openers must use O_CLOEXEC; add
startup sweep over `/proc/self/fd`.

---

## Low

### F37 — `errors.Is(err, ErrTransitionRefused)` only matches the sentinel
`runner/transition.go:88-90`. Comparing against `errors.New("transition-refused")`
fails (different pointer); undocumented.

**Fix:** doc line on ErrTransitionRefused.

### F38 — Whitespace-only Command produces misleading ReasonMalformedOutput
`runner/subprocess.go:159-161`. `"   "` → `sh -c` → exit 0 → empty
stdout → ReasonMalformedOutput. Real cause (operator config typo)
hidden.

**Fix:** TrimSpace at the empty-check.

### F39 — `captureBuf` returns len(p) on overflow even with zero absorbed
`runner/subprocess.go:397-418`. Phantom-throughput accounting.

**Fix:** after F2's fix this becomes moot; until then, return
`(n, io.ErrShortWrite)`.

### F40 — Two cancellation causes collapse to ReasonTimeout
`runner/subprocess.go:227-241`. Caller-cancel and deadline-expiry
both surface as timeout; downstream metrics overcount timeouts.

**Fix:** add ReasonCancelled; branch on context.Canceled vs
context.DeadlineExceeded.

### F41 — Setsid escape bypasses killProcessGroup (document)
`runner/subprocess.go:179, 360-385`. A binding that calls setsid
moves children to a new session/PGID; killProcessGroup misses
them. Sandbox territory but comment claims a guarantee that isn't
held.

**Fix:** fix the comment ("group kill reaches children that stay in
the group; setsid escape requires sandbox"); document that ghyll
requires sandbox for session containment.

### F42 — OOM misattribution (signal 9 = "OOM")
`runner/subprocess.go:259-275`. Any SIGKILL (operator pkill,
sandbox enforcement, OOM, our own killProcessGroup) labeled
"evaluator-killed: oom".

**Fix:** rename to ReasonKilledBySignal; report signal number;
document genuine OOM detection requires cgroup/proc inspection.

### F43 — `matchesScope` non-anchored semantics
`runner/notodo.go:215-253`. `**/foo` against `x/foo/y` does NOT
match. Bootstrap recursive matcher may differ. Spec-write needed.

**Fix:** mooted by F13's canonical matcher extraction.

### F44 — WalkDir aborts on any per-entry error
`runner/notodo.go:73-103`. One permission-denied subdir kills the
entire clause.

**Fix:** filter recoverable errors (fs.ErrPermission, fs.ErrNotExist)
to skip; abort only on unknown.

### F45 — TOCTOU between cmd.Start and killProcessGroup
`runner/subprocess.go:205-208, 361-363`. `cmd.Process == nil` check
protects pre-Start; post-Wait race against recycled PID exists.

**Fix:** mooted by F14's ProcessState check.

---

## Remediation strategy

Same pattern as pass-2: all fixed, no deferrals. Some findings
collapse (F39/F45 mooted by F14 + F2; F43 mooted by F13). Some
need light spec coordination (F24 — `in-progress` as a runner-
internal state never written to attestation records).

Commit batching:

1. **Critical + spec-compliance** (C1-C3, F4-F8): subprocess
   security, marker matcher, arrow status correctness.
2. **High** (F9-F16): runner.go invariants, glob matcher unification,
   subprocess kill-path fixes.
3. **Medium + low** (F17-F45): bounds, documentation, cosmetic.

Tests added per finding where the bug is testable. Lint clean.
