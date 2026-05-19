# Validation pass 2 — adversarial review of session-2 work

Cold-context adversarial pass across the new code shipped in session 2
(bootstrap auto-propose, profile, refusal, bindings, init driver,
session registry, orphan extraction, modify directional rules, runner
package). Three parallel adversary agents, 60 findings total
consolidated below.

**Severity distribution:** 2 Critical, 16 High, 22 Medium, 20 Low.

**Per user direction:** fix all findings, no deferrals.

---

## Critical

### F1 — `isPathGlobNarrowing` doc/behavior mismatch

`bootstrap/modify.go:239-247`. Docstring promises subset semantics;
implementation refuses ALL glob-vs-glob comparisons even when one is
a strict subset of the other. Symmetric refusal is safe-by-default,
but the doc claims subset detection.

**Fix:** rename to make the heuristic-not-subset semantics explicit;
or strengthen the doc to say "glob-vs-glob always refuses, operator
must extend." Add unit test asserting both directions of glob-vs-glob
return false.

### F2 — `isRegexWidening` bypassable

`bootstrap/modify.go:326-342`. Naive `|` split + set-superset check
is fooled by:
- Dead alternations (`^Z(?!a)Z`) that satisfy set-superset while
  matching nothing.
- Anchors and groups that change semantics (`^(TODO|FIXME)` vs
  alternation precedence with raw `|`).
- True widenings (`TODO` → `TOD.`) that are refused (false negative).

The system advertises raise-only enforcement but for regex args it
does string-shape comparison, not set comparison.

**Fix:** retire the regex-monotonicity check. Refuse `modify` on
regex-typed args outright. Operator who wants to change a regex must
use `extend` (adds a new clause) or `skip` (with residue).

---

## High

### F3 — `VerdictExtend` / `ErrExtensionMissing` ghost in Apply docstring
`bootstrap/propose.go:65-71, 226-238`. Both removed from the enum in
the auto-propose refactor but still named in the docstring. Confuses
callers; default-case fallthrough would silently produce "unknown
verdict kind" for `Verdict{Kind: 3}`.

### F4 — `AllVerdictsReceived` admits duplicate-ID gaps
`bootstrap/propose.go:426-431`. `len(verdicts) == len(Proposed)`
holds with one un-verdicted clause if BuildProposal produces
duplicate IDs from a malformed role file (no row-id uniqueness
check in ParseRoleFile).

### F5 — `ExtractContextSymbols` symlink TOCTOU
`bootstrap/orphan.go:75-89`. `src/<contextID>` resolved via
`filepath.Join` without rejecting `..`, symlinks, absolute path
components. A `src/contextA -> /etc` symlink causes the walker
to enumerate /etc.

### F6 — `loadSpecText` follows symlinks; size check on lstat
`bootstrap/orphan.go:274-335`. Inner symlinks (`specs/note.md ->
/etc/shadow`) pass the size check (lstat reports symlink size, not
target) and `os.ReadFile` follows them.

### F7 — Spec corpus unbounded in aggregate
`bootstrap/orphan.go:268, 325-334`. `maxSpecFileSize` is per-file
(1 MB); total concatenation has no cap. 10k spec files × 1MB = 10GB
in one strings.Builder.

### F8 — `ProfileRepo` symlink-walks `src/` into out-of-tree dirs
`bootstrap/profile.go:223-249`. `scanBrownfieldContexts` calls
`entry.IsDir()` on `ReadDir` output; a symlink to a directory passes
the IsDir check (depending on FS), proposing bounded contexts named
after dirs outside the repo.

### F9 — `ProjectProfile` mutation methods unsynchronized
`bootstrap/profile.go:50-66 + risk.go:152-204 + init.go:67,97`. No
mutex, no documented "not safe for concurrent use" contract (Session
has the latter; ProjectProfile doesn't). BuildInitGrid reads
`profile.BoundedContexts` and `profile.RefusalAccepted()` while
DeclareContext/AcceptRefusal could run concurrently.

### F10 — Refusal state machine has silent flip path
`bootstrap/risk.go:173-204`. Accept then Override silently flips
`Accepted` true→false. `ErrRefusalAlreadyResolved` exists but is only
checked in ProposeRefusal.

### F11 — ProfileRepo: unbounded walk + no `context.Context`
`bootstrap/profile.go:142-215`. Walks the whole tree, no
cancellation, no file-count cap. Violates `.claude/coding/go.md`
"context.Context threaded through every I/O call".

### F12 — Op-id PII leaked verbatim in error messages
`bootstrap/registry.go:52`. `Declare`'s error message contains the
active op-id. If op-id is an email or incident handle, a less-trusted
caller learns the holder. No op-id confidentiality policy currently
declared.

### F13 — Multi-`**` glob mishandled by `pathGlobMatchRecursive`
`bootstrap/modify.go:263-311`. Only the first `**` is split out;
suffix containing another `**` is passed to `path.Match` which
doesn't understand it. `path-glob` validity errors are silently
swallowed (no `ErrBadPattern` propagation).

### F14 — Registry hot-swap loses attestation pin
`runner/runner.go:142-154 + 232-264`. `Lookup` captures the function
value; subsequent `Register` doesn't affect in-flight calls.
EvaluationRun records no evaluator identity, so attestation cannot
pin which binding actually ran.

### F15 — EvaluationRun cannot distinguish "evaluator broken" from genuine fail
`runner/runner.go:244-265, 271-295`. `Result=nil, err=nil` produces
`EndStatus=fail` with `Result=nil`; a real fail also has
`EndStatus=fail`. No `Error string` field on EvaluationRun. Operational
errors are lost on persistence.

### F16 — `safeInvoke` cannot catch all panics
`runner/runner.go:300-308`. Goroutines spawned by evaluators that
panic kill the process. Stack overflows often unrecoverable. Doc
overstates the guarantee.

### F17 — `EvaluateNoTodoMarker` file-open TOCTOU symlink-swap
`runner/notodo.go:253-268`. `Lstat` check then `os.Open` follows
symlinks. Race window allows file content from outside the project
(e.g., `~/.ghyll/keys/`) to be scanned and leaked into hit
attestation records.

### F18 — `bufio.Scanner` long-line aborts whole evaluation
`runner/notodo.go:270-289`. 1MB scanner buffer; minified file with a
single >1MB line returns `bufio.ErrTooLong` which propagates up and
aborts the entire `no-todo-marker` evaluation.

---

## Medium

### F19 — `ClassifyOrphans` is O(symbols × specBytes)
`bootstrap/orphan.go:244-262`. Per-symbol substring re-scan.

### F20 — `ClassifyOrphans` substring match has false-negatives on short names
`bootstrap/orphan.go:255`. `Run`, `Args`, `ID`, `New`, `T` appear in
prose; classification reports them as non-orphan even when no spec
actually references them. Reason string lies.

### F21 — `parseExitGateTable` mishandles escaped/quoted pipes
`bootstrap/roleclause.go:147-155`. `strings.Split(inner, "|")` doesn't
respect backtick-spans or `\|` escapes; a legitimate `` `(arg-a|arg-b)` ``
cell becomes 6 cells.

### F22 — `conceptCallRE` strict on attested-cell exact match
`bootstrap/roleclause.go:202, 214-223`. `(Judgement)` (capitalized)
fails the regex; trailing whitespace inside parens not stripped.

### F23 — `extractGoSymbols` has no per-file size cap
`bootstrap/orphan.go:160-167`. A 2GB Go file OOMs the parser. No
panic recovery either — one bad file aborts the brownfield scan.

### F24 — Walk error in ExtractContextSymbols aborts entire context
`bootstrap/orphan.go:88-132`. One unreadable/unparseable file drops
all already-discovered symbols.

### F25 — `filepathBase` hand-rolled; mishandles `:`
`bootstrap/roleclause.go:95-100`. orphan.go already imports
`path/filepath`; reimplementing here for "one-call need" is wrong.

### F26 — `BuildProposal` accepts zero-clause role file
`bootstrap/propose.go:166-203`. Empty Proposed slice means
AllVerdictsReceived returns true vacuously; arrow recorded with no
exit conditions.

### F27 — `ArrowProposal` methods unsynchronized
`bootstrap/propose.go:120-431`. Same pattern as F9; no mutex, no
documented "not safe for concurrent use".

### F28 — `ResidueEntry` loses `RoleArgsHint`
`bootstrap/propose.go:296-305`. Adversarial phase that attacks
residue can't tell which arg-hint distinguished two clauses sharing
a concept.

### F29 — `Extend` and Confirm record clauses with missing required args
`bootstrap/propose.go:340-378`. No post-recording validation that
Args satisfy the concept's required-without-default args. Closed-
catalogue principle is broken at the gate writer.

### F30 — `DeclareContext` doesn't NFC-normalize (op-id does)
`bootstrap/profile.go:264-280`. `café-svc` (composed) vs
`café-svc` (decomposed) classified differently; ASCII-only also
surprises operators typing composed accents.

### F31 — `RiskAssessment` accepts negative counts
`bootstrap/risk.go:52-93, 98-125`. `int` not `uint`;
`BoundedContextCount: -100` satisfies `<= 1` and rationale prints
nonsense.

### F32 — `NewGrid` defaults flow into init grid unreviewed
`bootstrap/init.go:96 + grid.go:101-111`. SeverityThreshold,
DepthLadder, RemediationRoundsMax baked into the grid without
operator review.

### F33 — `scanBrownfieldContexts` skips `isValidContextID`
`bootstrap/profile.go:223-249`. Directory names become bounded
contexts with no format validation; YAML injection blocked by the
serializer but other surfaces (terminal display, future JSON) unsafe.

### F34 — `SessionRegistry.Declare` holds mutex during StartSession
`bootstrap/registry.go:48-60`. If StartSession grows to do slow I/O,
the whole registry stalls.

### F35 — `SessionRegistry.history` unbounded
`bootstrap/registry.go:32, 97`. Long-running process accumulates
sessions; History() snapshots all of it on every call.

### F36 — `bounded-context-id` format duplicated across files
`bootstrap/profile.go:283-308 + bindings, init`. No single canonical
validator; spec drift waiting.

### F37 — `BuildInitGrid` doesn't require refusal resolution
`bootstrap/init.go:60-94`. Profile with RefusalProposed but neither
Accepted nor Overridden silently produces a grid.

### F38 — `BoundedContexts` unbounded; O(N²) DeclareContext
`bootstrap/profile.go:260-281`. Linear dup scan; no cap.

### F39 — Skip-dir set drift bootstrap vs runner
`runner/notodo.go:235-243 + bootstrap/profile.go:97-116`. Runner
skips fewer dirs than bootstrap; specs/, docs/ scanned by
no-todo-marker but invisible to bootstrap's mode detection.

### F40 — `notodo` no per-line context check
`runner/notodo.go:70-100, 273-285`. Long files uncancellable.

### F41 — Marker matcher misattributes for custom case-insensitive markers
`runner/notodo.go:154-186`. Case-insensitive matcher returns
`markers[i]` (configured form), not the file's actual marker. With
overlapping markers, attribution wrong.

### F42 — Non-regular files hang the scanner
`runner/notodo.go:261-289`. Lstat reports size 0 for fifos/devices;
size check passes; `os.Open` blocks forever on read.

### F43 — `defaultIDGen` not unique under concurrency
`runner/runner.go:314-320`. Two `Evaluate` calls in the same
nanosecond produce identical EvaluationRun.IDs.

### F44 — `NewForTest` exported in production package
`catalogue/catalogue.go:194-209`. Doc-only constraint; any code path
can call it and bypass the closed-vocabulary guarantee.

### F45 — `coerceStringList` silently accepts string for list
`runner/notodo.go:126-146`. Operator typo `markers: TODO` (missing
brackets) → `["TODO"]` silently.

---

## Low

### F46 — `loadSpecText` error leaks absolute path
`bootstrap/orphan.go:316-317`. Use relative path or sentinel.

### F47 — Default args shared by reference across proposed clauses
`bootstrap/propose.go:209-217, 437-446`. Slice/map defaults aliased;
mutation leaks.

### F48 — Separator line not validated
`bootstrap/roleclause.go:119-122`. Missing separator silently drops
the first data row.

### F49 — Zero-width chars in concept names not stripped
`bootstrap/roleclause.go:202`. U+200B etc. defeats the regex with
no diagnostic.

### F50 — `DeclareContext` trimmed-dup confusing
`bootstrap/profile.go:264-280`. Three different-looking inputs
normalize to one ID with no warning.

### F51 — `BindingKey.String/FromStrings` dot-delimiter ambiguous
`bootstrap/bindings.go:36-38, 191-209`. Hierarchical concept name
containing `.` round-trips incorrectly.

### F52 — `CheckRequiredBindings` returns normalized keys without note
`bootstrap/bindings.go:143-171`. Result list differs from caller's
input; caller can't detect their own bug.

### F53 — `DeclareBinding` silent overwrite
`bootstrap/bindings.go:89-111`. No audit; second `DeclareBinding`
silently replaces the first with no history.

### F54 — `ProposeRefusal` not idempotent despite doc claim
`bootstrap/risk.go:152-165`. Second call returns
`(RecommendationProceed, ErrRefusalAlreadyResolved)` — misleading
recommendation on error path.

### F55 — `Active()` race window post-return
`bootstrap/registry.go:65-72`. Returned `*Session` may be End'd
concurrently. Doc endorses this; needs explicit caller guidance or
snapshot return.

### F56 — `ClauseStatus.String()` returns "unknown" for out-of-range
`runner/runner.go:43-58`. Silent corruption looks like documented
state.

### F57 — `splitTrim` swallows double-pipe regex typos
`bootstrap/modify.go:346-357`. `"^TODO||^XXX"` normalized to
`["^TODO", "^XXX"]` masking a real regex that matches everything.
(Mostly mooted by F2's refusal-of-regex-modify, but the helper
itself shouldn't normalize this away.)

### F58 — `EvaluationRun` fields publicly mutable post-return
`runner/runner.go:166-183`. Caller can mutate `Result.Details` after
Evaluate. Breaks chain-of-custody if attestation hashes the record.

### F59 — `Clause` doesn't carry ClauseID/PassID to evaluators
`runner/runner.go:232-264`. Future subprocess evaluators can't
correlate their logs with EvaluationRun records.

### F60 — `NewForTest` doesn't dedup; last-write-wins
`catalogue/catalogue.go:203-209`. `Load` errors on duplicate names;
test helper silently overwrites.

---

## Remediation strategy

Each finding gets either a code fix or, where the issue is genuinely
architectural (F2 — regex-modify reformulation, F12 — op-id PII), a
typed design decision committed as ADR or escalation note alongside
the code change. No fix is deferred to "later"; everything in this
doc gets resolved before next slice.

Commit batching:

1. **Critical + load-bearing high** (F1-F2 architectural, F3-F18):
   refusal flow, propose loop, init driver, runner core fixes.
2. **Medium correctness + concurrency** (F19-F36): bounded
   structures, context plumbing, dedup, sync.
3. **Medium robustness + low** (F37-F60): tighter validation,
   immutability, audit hooks, doc fixes.

Tests added per finding where the bug is testable. Lint clean
throughout.
