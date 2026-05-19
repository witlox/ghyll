# Validation pass 4 — adversarial review of runner step-4 work

Cold-context adversarial pass on the integrator-gate evaluators
(arrow-artifact-present, trace-link-present, cardinality-check,
no-open-finding, kill-server-fails-integration) plus the findings
model and the amendment flow. Three parallel adversary agents,
44 findings total consolidated below.

**Severity distribution:** 4 Critical, 16 High, 20 Medium, 4 Low.

**Per user direction:** fix all findings, no deferrals.

---

## Critical

### F1 — `byID` pointer aliases into a growable slice (silent state loss)
`runner/findings.go:108-113`. `Raise` does `s.byArrow[id] = append(...)`
then `s.byID[id] = &s.byArrow[id][idx]`. On the next append past
capacity Go reallocates the backing array; every prior `byID`
pointer now writes into a dead array. `Transition` silently fails
to update the live state seen by `ForArrow`/`Get`. The source
comment flags the hazard then ignores it.

**Fix:** key `byID` by `(arrowID, idx)` and dereference on every
access via `byArrow[arrowID][idx]`. Drop the pointer entirely.

### F2 — `ForArrow` returns records the engine concurrently mutates
`runner/findings.go:167-176` + `runner/nofinding.go:78-89`.
`Transition` writes through `byID`'s pointer (which aliases into
`byArrow`), so even with RLock/Lock, a slice realloc desynchronizes
the two maps and protection breaks. Combined with F1.

**Fix:** dissolves once F1 is fixed (no aliasing pointers).

### F3 — Path traversal via `..` and platform-specific absolute paths
`runner/arrowartifact.go:60-64`. `resolved = c.ProjectDir + "/" + artifactPath`
does no `filepath.Clean`, no containment check. `artifact-path: "../../etc/passwd"`
escapes the project tree; clause attests the integration report at
`/etc/passwd`. Windows `C:\foo`, `\\unc`, leading `\` all bypass
the POSIX-only absolute-path detection.

**Fix:** introduce `containment.go` with `ResolveProjectPath` —
`filepath.Abs(filepath.Join(projectDir, p))`, then refuse if the
result is not inside the absolute project dir. Use it everywhere
an operator-supplied path is taken.

### F4 — Parent-directory symlinks bypass the artifact symlink defense
`runner/arrowartifact.go:66-91`. `os.Lstat(resolved)` only checks
the final component. If any parent dir on the way is a symlink to
outside the project, the artifact looks valid and the schema-check
subprocess runs against an out-of-tree file.

**Fix:** walk the resolved path component-by-component from
`projectDir` outward, calling `os.Lstat` on each, and refuse any
intermediate symlink. Reuse the helper in F3.

---

## High

### F5 — TOCTOU between `ForArrow` and downstream consumer
`runner/nofinding.go:78-101`. Evaluator snapshots findings then
emits `pass`. Between snapshot and gate-consumer there is no
versioning, so a concurrent Raise on another clause changes the
arrow's blocking-status. Per gates.md §11.3, sibling clauses must
agree on finding state; today they don't.

**Fix:** add `FindingsStore.Version()` returning a counter
incremented under Lock. Snapshot returns `(records, version)`;
gate consumer cross-checks before commit.

### F6 — Finding-transition spec/code/comment divergence + no actor identity
`runner/findings.go:117-162`. Doc comment lists fewer transitions
than the code permits; `accepted-risk → open` happens with no
caller identity, defeating attestation. gates.md §7.3 names this
as "operator amendment" — runtime has no operator boundary.

**Fix:** align comment with the implemented set. Introduce
`TransitionWithReason(id, to, role, reason)`. Reject `accepted-risk → open`
unless the role is "operator" or "integrator".

### F7 — `EvaluateNoOpenFinding` drops Description from operator-facing details
`runner/nofinding.go:80-89`. Blocking findings report only
`{id, type, severity, status}`. Description, RaisedAt, RaisedByRole
are dropped — the operator can't triage without a second store
lookup that may not be available at the receipt surface.

**Fix:** include `description`, `raised-at`, `raised-by-role`.

### F8 — `isBlockingFinding` silently passes corrupt status values
`runner/nofinding.go:110-118`. Out-of-range `FindingStatus`
(including the future-enum-value case) falls through to false.
Per validation-pass-3 F8's handling of `ClauseStatus`, corruption
should default to blocking, not pass-through.

**Fix:** add default branch returning true; surface an explicit
reason in the result details.

### F9 — `Severity` is an unconstrained int — `Severity: 99` always blocks; `-1` never blocks
`runner/findings.go:41` + `runner/nofinding.go:113`. `Raise` does
not validate severity; threshold compare is naïve `>=`. A typo of
`MaxInt` blocks every arrow; `-1` bypasses the gate.

**Fix:** validate `0 ≤ Severity ≤ 4` in `Raise` (`ErrFindingInvalidSeverity`).
Clamp out-of-range to the nearest valid rank in `isBlockingFinding`
as a defense-in-depth.

### F10 — `parseSeverityRank` whitespace+case fragile vs `ParseFindingStatus`
`runner/nofinding.go:122-136`. `ParseFindingStatus` normalizes
trim+lowercase; `parseSeverityRank` does neither. `severity-threshold: "Medium"`
returns a confusing "Medium not in {info, low, medium, …}" error.

**Fix:** apply `strings.TrimSpace(strings.ToLower(s))` for parity.

### F11 — `runSchemaCheck` has no stdout/stderr cap (RAM DoS)
`runner/arrowartifact.go:159-178`. Bare `bytes.Buffer` for both
streams; a 30-second schema-check that streams unboundedly
(`yes`, accidental verbose mode) will OOM-kill the runner before
the timeout fires.

**Fix:** route through `BindingEvaluator` (preferred — reuses cap
+ kill plumbing) OR use `newCaptureBuf(maxStderrCapture, doKill)`
locally. Same risk class as validation-pass-3 F2.

### F12 — `schema-check` is a raw shell template; trust boundary unmarked
`runner/arrowartifact.go:152`. Operator-supplied `schema-check`
is interpolated into `sh -c` verbatim. Operators may believe
their command is templated; in reality it's full shell with a
quoted path appended (or swallowed by a trailing `#`).

**Fix:** document loudly in the function docstring AND in the
concept yaml that `schema-check` is full-shell. Surface a wire
warning when the command contains `#`, `;`, `&&`, `||`, `>`, `<`,
`|` outside quotes. Long-term: allow list form
(`schema-check: ["jq", "-e", "."]`) and exec without `sh -c`.

### F13 — `link-rule` only captures group 1; alternation with split captures silently fails
`runner/tracelink.go:283`. A natural rule like
`(?:fixes|closes) #(\d+)|relates-to ([A-Z]+-\d+)` puts the target
in group 1 or group 2 depending on the branch. Today only group 1
is read; the second branch yields empty captures and silent false
negatives.

**Fix:** iterate `m[1:]` and take the first non-empty capture per
match. Document that exactly one of the capture groups must be
non-empty per match.

### F14 — `buildLinkIndex` collapses basename/basename-without-ext into one namespace
`runner/tracelink.go:240-252`. `auth.go`, `auth.py`, `auth.md`
under `to` all collide on the key `auth`. Capture `auth` matches
any of the three; a spec→test trace can pass against the markdown
doc instead of the test.

**Fix:** store `map[string][]string` (key → list of files);
record the actual `to` file each capture resolved to in the
per-from result. Operators see which target satisfied each link.

### F15 — `extractRegexCaptures` is unbounded per file (allocation DoS)
`runner/tracelink.go:277-287`. The 4 MB file-size cap bounds
input, but a pathological regex like `(.)` produces millions of
captures, each appended to the heap-growing slice. Total heap can
exceed 100 MB per file.

**Fix:** cap captures per file at a constant (e.g., 10 000) and
surface "matches truncated" in per-file details. Reject
zero-width regexes (see F16).

### F16 — Zero-width / empty-match regex not rejected
`runner/tracelink.go:73-79` + `runner/cardinality.go:57-60`.
Operator-supplied regex compiled from input may match the empty
string (`(.*)`, `(?:)`, `()`). For tracelink, every position
yields an empty capture; for cardinality with expected=0, every
line "matches".

**Fix:** at compile time, reject any pattern where
`re.MatchString("")` is true.

### F17 — `runShellWithCap` has no stdout/stderr cap (RAM DoS)
`runner/killserver.go:209-211`. Bare `bytes.Buffer` for both
streams; a `yes` test-command consumes GB/sec before the 5-minute
timeout fires.

**Fix:** mirror `BindingEvaluator` — use `newCaptureBuf` with
kill-on-overflow for both streams. Same fix as F11.

### F18 — Unkill failure silently contaminates subsequent dep probes
`runner/killserver.go:142-145`. `unkillRun, _ := …` records only
`UnkillExit`. If unkill fails, the dep stays dead; next dep is
tested against a corrupted baseline; the dep that "happens to
fail after kill" passes G3 for the wrong reason.

**Fix:** after each unkill, re-run baseline. If it doesn't pass,
mark the whole evaluation Unevaluated with reason
"unkill-failed-cannot-continue".

### F19 — `runShellWithCap` conflates timeout / signal / unknown failure
`runner/killserver.go:222-228, 235-239`. exitCode=-1 means
"timed out" OR "killed by signal" OR "operational error".
G3 cannot distinguish "test failed after kill (good)" from
"test hung after kill (Unevaluated)".

**Fix:** add `timedOut bool` + `signal int` to `shellRunResult`;
treat "test after kill timed out" as Unevaluated for that dep.

### F20 — Baseline pass with zero tests executed is treated as success
`runner/killserver.go:94-111`. `go test ./...` in a directory
with no test files exits 0 — same wire signal as a real pass.
The G3 invariant rests on baseline being a real signal; today
there is no countermeasure.

**Fix:** require optional `min-tests-detected` (regex against
baseline stdout, e.g., `--- PASS:` count > 0). If absent, surface
a warning in details when baseline stdout is empty/short.

---

## Medium

### F21 — `severity-threshold` accepts only string (typed catalogue mismatch)
`runner/nofinding.go:55-64`. Threshold must be a string; if the
typed-severity arg lands as int from a YAML decoder, evaluator
errors.

**Fix:** accept int/int64/float64 (validate 0..4) OR string
(route through `parseSeverityRank`).

### F22 — `Raise` accepts whitespace-only or non-printable `Type`
`runner/findings.go:99-100`. `Type = "local-bug\n"` succeeds at
Raise; integrator's cardinality-check later rejects it. Operator
sees the error far from the source.

**Fix:** require `Type` to match `^[a-z][a-z0-9-]*$`. Reject at
Raise time.

### F23 — `Raise` accepts any `Status` including out-of-range
`runner/findings.go:92-115`. `Status: FindingStatus(99)` succeeds;
Transition then errors forever; isBlockingFinding silently
non-blocks (F8 compounds).

**Fix:** validate `Status` is one of the known enum values in
`Raise`; allow zero (defaults to Open) and validate non-zero.

### F24 — `FindingsStore` has no eviction or Forget API
`runner/findings.go:65-69`. Both maps grow monotonically across
long sessions. Resolved/accepted-risk findings remain forever.

**Fix:** add `Forget(id)` and `ForgetArrow(arrowID)`. Document
that the engine must call them at checkpoint boundaries.

### F25 — Transition cycle `open → running → open` is unbounded
`runner/findings.go:138-162`. No churn detection, no transition
counter, no rate limit.

**Fix:** add per-record `TransitionCount` field. Optionally
reject above a configurable cap (e.g., 100) with
`ErrFindingTransitionChurn` — operator clears via amendment.

### F26 — `FindingsFromContext` silent nested-shadow
`runner/nofinding.go:30-45`. Nested `WithFindingsStore` overrides
the outer store with no diagnostic. A test helper that wraps with
`NewFindingsStore()` to "reset" silently disconnects the gate
from the real store.

**Fix:** in `WithFindingsStore`, if `ctx.Value(findingsHookKey{}) != nil`,
panic with "findings-store-already-attached". Document the
"innermost wins" semantic explicitly if intentional.

### F27 — tracelink empty `to` glob reported as Fail, not Unevaluated
`runner/tracelink.go:106-115, 142-160`. Empty `from` is correctly
Unevaluated. Empty `to` produces Fail (every from "unmet count
0 < min 1") — indistinguishable from "operator misspelled the
glob".

**Fix:** if `len(toFiles) == 0`, return Unevaluated with reason
"no files match `to` glob; cannot evaluate trace targets".

### F28 — cardinality `project-state` target returns error, not Unevaluated
`runner/cardinality.go:53-54`. v1 doesn't support project-state;
returning `errors.New` causes runner-level error and Status=Fail.
Per the Result contract, this should be Unevaluated.

**Fix:** return `&Result{Unevaluated: true, Reason: "query-target `project-state` not yet supported in v1"}`.

### F29 — cardinality double-scans the file when regex has no capture group; semantics drift
`runner/cardinality.go:160-181`. With-capture-group counts matches;
no-capture-group counts lines. Two semantics from one wire shape,
and the no-capture-group path scans the file twice.

**Fix:** pick one semantic. Recommend "match count" (composes
with regex), implement directly without the first throwaway pass.
Document.

### F30 — No per-line `ctx.Err()` in tracelink / cardinality scanners
`runner/tracelink.go:280-287` + `runner/cardinality.go:202-209`.
notodo (validation-pass-3 F40) checks ctx every 1024 lines;
these don't. Cancellation invisible mid-file.

**Fix:** thread ctx through `extractRegexCaptures` /
`countLinesMatching`; check `ctx.Err()` every 1024 lines.

### F31 — `filteredParentEnv` near-duplicates `BindingEvaluator.buildEnv` (drift target)
`runner/arrowartifact.go:190-219` vs
`runner/subprocess.go:209-243`. No way for schema-check to opt
into `VIRTUAL_ENV` / `PYTHONPATH` even when the binding evaluator
allows it via `WithInheritEnv`.

**Fix:** route schema-check through `BindingEvaluator` (preferred
— closes F11 too) OR export `buildDefaultEnv()` from subprocess.go
and call it from both. Delete `filteredParentEnv`.

### F32 — `runShellWithCap` operational errors silently discarded
`runner/killserver.go:127, 136, 143`. Three call sites use
`_, _ :=`. If `cmd.Start()` fails (no sh on PATH), kill registers
as success and the dep wrongly flags as mocked.

**Fix:** propagate the error. Any non-nil err from
`runShellWithCap` returns Unevaluated with reason
"harness-spawn-failed".

### F33 — Empty-string dep in `critical-deps` passes through silently
`runner/killserver.go:70-92`. `criticalDeps = ["db", ""]` results
in `kill-cmd.` lookup; empty dep appears in `missing-kill-cmds`
or as `dep: ""` in details.

**Fix:** filter empty/whitespace-only entries after coercion.
Return Unevaluated if the post-filter list is empty.

### F34 — kill-cmd / unkill-cmd raw shell; dep names not validated
`runner/killserver.go:127, 136, 143, 203`. Dep names with shell
metacharacters can become foot-guns if a future operator
templates them into the command body. No upfront validation.

**Fix:** validate `^[a-zA-Z0-9_.-]+$` for dep names; reject
otherwise with structured error. Validate `c.ProjectDir` exists
upfront and surface Unevaluated with reason "project-dir-missing".

### F35 — No quiescence delay between kill-cmd return and test rerun
`runner/killserver.go:127-136`. `docker stop` returns when daemon
ack'd; TCP listener drains for ~100 ms. Test may pass against a
warm-but-going-down dep, falsely flagging suite as mocked.

**Fix:** accept optional `kill-settle-ms.<dep>` arg (default 250).
`time.Sleep` between kill-cmd return and test-command.

### F36 — `AmendmentRequest.Validate` silently accepts unknown Reason
`runner/amendment.go:78-98`. Operator typo
`"missing-cross-conetxt-spec"` (typo) passes Validate (non-empty,
not matched in switch). Downstream handler dispatches on the wire
string and silently no-ops.

**Fix:** add default case in the switch returning
"amendment-reason-unknown".

### F37 — `AmendmentQueue.Drain` returns shared per-request slice fields
`runner/amendment.go:140-156`. `Pending` deep-copies slice header
but per-request `Contexts`/`FindingIDs` slices are shared.
Consumer mutation poisons the queue.

**Fix:** deep-copy slice fields in both `Pending` and `Drain`.
Add a test that mutates the snapshot and asserts queue unaffected.

### F38 — `AmendmentQueue` has no max-length / no backpressure
`runner/amendment.go:104-136`. A broken drain consumer plus an
emitting integrator → unbounded slice growth.

**Fix:** add `MaxLen int` field (default 1024). Return
`ErrAmendmentQueueFull` on overflow.

### F39 — `PendingAmendments` doesn't validate contexts upfront
`runner/amendment.go:180-216`. With `len(contexts) < 2`, the
function builds requests anyway; Validate fires lazily at Enqueue.

**Fix:** at function entry, if `len(contexts) < 2`, return
`(nil, errAmendmentContextsTooFew)`. Change signature.

### F40 — `defaultAmendmentIDGen` collides at nanosecond resolution
`runner/amendment.go:221-223`. Tight loop calls may produce
identical IDs on coarse-clock platforms; CreatedAt strings can
match within the same call.

**Fix:** add an `atomic.Int64` counter; ID format
`amend-<nano>-<seq>-<processIDPrefix>`. Capture `time.Now()` once
at the top of `PendingAmendments`.

---

## Low

### F41 — `ParseFindingStatus` doesn't accept underscore form
`runner/findings.go:207-221`. Operator typo `accepted_risk`
returns generic "unknown finding-status".

**Fix:** normalize `_` → `-` after TrimSpace+ToLower.

### F42 — `coerceInt64` NaN/Inf error message is misleading
`runner/arrowartifact.go:224-241`. YAML `.nan` or `.inf` reports
as "non-integer float" — the actual issue is non-finite.

**Fix:** explicit `math.IsNaN(x) || math.IsInf(x, 0)` check
returning "expected finite integer".

### F43 — `FormatAmendmentSummary` doesn't sanitize newlines / control chars
`runner/amendment.go:228-250`. Operator description with embedded
`\n  source-arrow: forged\n` forges fields in line-based parsers.

**Fix:** strip or escape `\n`, `\r`, `\t`, control chars in
Description, Contexts, FindingIDs before formatting. Use a
delimiter not allowed in contexts (or JSON-encode per element).

### F44 — Drained amendment IDs not remembered (re-enqueue duplicate logical work)
`runner/amendment.go:140-147`. After Drain, byID is cleared; a
second PendingAmendments call for the same still-open finding
emits a duplicate amendment because byID has no memory.

**Fix:** maintain a `seenIDs` set that survives Drain. Add
`Reset()` for explicit session-end cleanup. Document the lifetime
semantics in `AmendmentQueue`'s docstring.

---

## Highest-risk areas

1. **`FindingsStore` byID aliasing (F1, F2)** — data-loss bug
   under realistic Raise patterns. Single root cause for two
   Criticals. Fix first.
2. **Path containment for operator-supplied paths (F3, F4)** —
   sound-defeating. Introduce `runner/containment.go` and route
   every operator-path through it.
3. **Subprocess output caps (F11, F17)** — same RAM-DoS class
   that validation-pass-3 F2 invented `captureBuf` for, now
   missing in two newer subprocess sites. Consolidate.
4. **Mock-detection invariants in kill-server (F18, F19, F20)**
   — every one is a silent-failure mode for the gate whose entire
   purpose is detecting silent failures.

## Remediation plan

No deferrals (user direction). Sequenced for minimal churn:

1. Add `runner/containment.go` (F3, F4 — used by later fixes).
2. Fix `FindingsStore` byID/byArrow data model (F1, F2).
3. Add validation to `Raise` and parsers (F8, F9, F10, F22, F23, F41).
4. Fix `EvaluateNoOpenFinding` details and threshold parsing
   (F5, F7, F21, F26).
5. Wire schema-check + kill-server through BindingEvaluator-style
   cap+kill (F11, F17, F19, F31, F32).
6. Fix kill-server invariants (F18, F20, F33, F34, F35).
7. Fix tracelink/cardinality semantics (F13, F14, F15, F16, F27,
   F28, F29, F30).
8. Fix amendment validation + dedup + bound (F36, F37, F38, F39,
   F40, F43, F44).
9. Fix transition / store cleanup (F6, F24, F25).
10. Fix the misc Low (F12 doc, F42).
