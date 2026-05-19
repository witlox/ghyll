# Validation pass 5 — adversarial review of phase 5 work

Cold-context adversarial pass on the phase-5 adversarial-phase
mechanism (depth ladder, classifications store, every-requirement-
meets-min-depth evaluator, Adversary coordinator, remediation loop,
verification auto-insert). Three parallel adversary agents,
43 findings total consolidated below.

**Severity distribution:** 0 Critical, 16 High, 18 Medium, 9 Low.

**Per user direction:** fix all findings, no deferrals.

---

## High

### F1 — `Adversary.ensureDefaults` mutates the receiver on every Attack — concurrent-arrows race
`runner/adversarial.go:153-172`. The struct claims "thread-safe across
arrows" but lazy-init writes to six receiver fields under no lock. Two
concurrent Attacks both observe nil hooks and race-write to them.

**Fix:** populate defaults eagerly in `NewAdversary`; remove the lazy
mutation path. Direct-construction callers can call `ApplyDefaults()`
explicitly.

### F2 — Hook-install race between operator and first Attack call
`runner/adversarial.go:153-172`. The test pattern "construct, then set
`a.OpenSweep = customHook`" races against any concurrent first Attack
that's already inside ensureDefaults.

**Fix:** snapshot hooks into Attack-local locals at function entry;
document "install hooks before first Attack" in the type doc.

### F3 — Gates.md §11 fresh-instance-per-round invariant uncheckable
`runner/adversarial.go:217-404`. The runner accepts re-use silently. A
single missed re-instantiation in production violates the §11
clean-context guarantee.

**Fix:** add `used atomic.Bool` on Adversary; second Attack returns
`ErrAdversaryAlreadyUsed`. RemediationLoop constructs a fresh Adversary
per round (and the loop documentation requires that).

### F4 — `StatusUnevaluated` clauses silently treated as non-falsified
`runner/adversarial.go:287-310`. A clause that ended Unevaluated (no
model, missing dep) yields no finding and no harness error. Per
gates.md §11 "below required depth → findings status `unevaluated`."

**Fix:** when `run.EndStatus == StatusUnevaluated`, raise a
clause-falsification finding with `Status=FindingStatusUnevaluated`
(or document the new mapping explicitly).

### F5 — Hook panic crashes the goroutine; no recover() at coordinator
`runner/adversarial.go:318, :353`. Validation-pass-2 F16 forbids
evaluator panics; hooks have the same trust shape but no recovery.

**Fix:** wrap OpenSweep and Classify in `safeInvokeHook` that
recovers and converts to a HarnessError entry. Mirrors `safeInvoke`
in runner.go.

### F6 — Convergence test conflates new + stale findings (false-converge)
`runner/remediation.go:132-136`. `countOpenFindings` counts every
open record on the arrow regardless of round, AND silently swallows
findings that failed to raise (HarnessError). Combined with a
producer transitioning a prior-round finding, the loop may report
converged with a fresh open finding hidden in HarnessErrors.

**Fix:** treat raise-failure HarnessErrors as hard escalation;
optionally pin store-version across attack→snapshot to detect
out-of-band mutation.

### F7 — Mid-Attack ctx-cancel runs one extra loop iteration
`runner/remediation.go:106-110`. ctx.Err is checked only at top of
loop, not after Attack returns or after FixAttempt.

**Fix:** add `if err := ctx.Err(); err != nil` after Attack and
after FixAttempt return.

### F8 — AttackBuilder.Round overwrite silently clobbers caller value
`runner/remediation.go:111-112`. The runner sets
`attack.Round = round` after calling the AttackBuilder. A caller that
pre-sets Round (resume from checkpoint, seeded PRNG) loses it.

**Fix:** either remove the override (trust the builder) or document
loudly that the loop authoritatively renumbers; pick the former.

### F9 — Convergence vs verification: unevaluated findings mismatch
`runner/remediation.go:161-177`. The loop converges when zero open/
running, but per gates.md §11.3 unevaluated findings still BLOCK the
no-open-finding verification. Result: "converged" → verification fail.

**Fix:** introduce `RemediationConvergedWithUnevaluated` outcome; or
treat unevaluated as non-converged in countOpenFindings.

### F10 — Mindepth TOCTOU between RequirementsForArrow + ClassificationsForArrow
`runner/mindepth.go:37, :53`. Two separate RLocks; concurrent
Declare+Record between them yields stale or partial verdict.

**Fix:** add `ClassificationsStore.SnapshotArrow(arrowID) (reqs, cls,
version)` taking one RLock; rewrite the evaluator to use it.

### F11 — Operator-supplied Evidence flows unsanitized into Result.Details
`runner/mindepth.go:72` + `runner/adversarial.go:389`. CRLF, ANSI
escapes, control chars all pass through. Mirrors validation-pass-4
F43 for amendments — same fix didn't carry over.

**Fix:** reuse the `sanitizeOneLine` helper (extract from amendment.go
into a shared helper). Apply to Evidence + Description on serialize.

### F12 — MinDepth = DepthRankNone (0) silently inert
`runner/depthladder.go:156, mindepth.go:67`. `c.Observed < 0` is
impossible → any Requirement with MinDepth=NONE always passes.
Zero-value MinDepth (operator typo / forgot to set) trivially passes.

**Fix:** reject MinDepth==NONE in `Requirement.Validate` — operator
must demand at least SHALLOW.

### F13 — Empty-requirements arrow trivially passes verification
`runner/mindepth.go:38-51`. If init forgot to declare requirements,
the verification step still passes. The auto-insert promise breaks.

**Fix:** return Unevaluated with reason; require an explicit
`allow-empty: true` arg to opt into trivial-pass.

### F14 — `ClassificationsStore` has no Forget / ForgetArrow / Observer
`runner/depthstore.go`. Unbounded growth across long sessions.
FindingsStore got these in validation-pass-4 F24; depth didn't.

**Fix:** add Forget(arrowID, reqID), ForgetArrow(arrowID), Observe(fn)
mirroring FindingsStore.

### F15 — Silent classification overwrite erases adversary audit trail
`runner/depthstore.go:75-93`. Re-classification overwrites with no
audit signal. A buggy or adversarial re-run can lower depth silently.

**Fix:** emit an Observer event on overwrite; keep an overwrite
counter on the store; expose `OverwriteCount` per (arrow, req).

### F16 — Re-classified above-min requirement leaves stale below-min finding open
`runner/adversarial.go:362-401`. Round 0 raises depth-below-min; round
1 classifies above-min but doesn't auto-resolve the stale finding.
Arrow can never close until manual transition.

**Fix:** on classify-above-min in round N, search for prior
depth-below-min findings on (arrowID, reqID) and Transition to
FindingStatusResolved with role="adversary", reason="re-classified
above-min in round N".

---

## Medium

### F17 — Single batch `raisedAt` timestamp for all findings in round
`runner/adversarial.go:248`. RFC3339 timestamps coalesce. Use Nano
precision, stamped per-finding.

### F18 — Open-sweep hook output not pre-validated; bad findings silently dropped
`runner/adversarial.go:322-347`. Severity=99 / bad Type fail
FindingsStore.Raise; the loop logs HarnessError but the finding is
gone. Prompt-injected adversary can suppress real defects by
producing un-raisable findings.

**Fix:** pre-validate at the hook boundary; on validation failure
raise a synthetic finding of type `harness-error`.

### F19 — `AttackReport.AnyOpen` is round-local but reads like cross-round
`runner/adversarial.go:199-215`. Naive caller doing
`if !report.AnyOpen() { proceed }` skips verification while old
findings still open in the store.

**Fix:** rename to `RaisedThisRound` or document the round-local
semantic in the method doc.

### F20 — Version not exposed in classifications snapshots; downstream TOCTOU
`runner/depthstore.go`. No `ForArrowVersioned`-style accessor.

**Fix:** return `(reqs, cls, version)` from the new SnapshotArrow.
Embed in `Result.Details["store-version"]`.

### F21 — Description and Evidence are unbounded strings
`runner/depthladder.go:142-184`. LLM hook can return MB+ strings.

**Fix:** cap at 8KB; truncate in Validate with a marker.

### F22 — Empty Requirement.Description silently allowed
`runner/depthladder.go:156`. Operator triage gets only the ID.

**Fix:** require non-empty Description in Validate.

### F23 — Unicode case-folding edge in DepthLadder label parser
`runner/depthladder.go:87, :121`. Turkish dotless-I etc. survive
dup detection.

**Fix:** restrict labels to ASCII via `^[a-zA-Z][a-zA-Z0-9-]*$`.

### F24 — Cross-arrow duplicate requirement IDs allowed; arrowID not trimmed
`runner/depthstore.go:49`. `"A1"` vs `"A1 "` are distinct map keys.

**Fix:** trim arrowID at store entry points; document the per-arrow
uniqueness policy in the type doc.

### F25 — Empty arrow-id passes evaluator; writers reject it
`runner/mindepth.go:24` vs `runner/depthstore.go:51`. Asymmetric.

**Fix:** trim+nonempty check at evaluator entry.

### F26 — RemediationConfig has no error budget on FixAttempt
`runner/remediation.go:145-152`. madeProgress=true + err loops to
rounds-max even if every round errors.

**Fix:** add `MaxConsecutiveFixErrors int` field; new outcome
`RemediationEscalatedHookError` when exceeded.

### F27 — Convergence test ignores severity-threshold (verification uses one)
`runner/remediation.go:166-177`. Low-severity findings can keep the
loop spinning when verification's threshold is high.

**Fix:** thread `SeverityThreshold int` through RemediationConfig;
default = SeverityInfo (current behavior).

### F28 — FixAttempt callback contract on mutation undocumented
`runner/remediation.go:144`. Snapshot is deep-copied but a naive
caller may mutate `open[i].Status` expecting it to transition.

**Fix:** docstring warning; regression test that mutates the
snapshot and asserts no effect.

### F29 — VerificationAutoInsert emits clauses with empty ClauseID
`runner/remediation.go:225-237`. Runner.Evaluate then errors with
"clauseID must be non-empty."

**Fix:** synthesize deterministic ClauseID `auto/<arrow>/<concept>`.

### F30 — VerificationAutoInsert silently accepts empty arrowID
`runner/remediation.go:207`. Empty arrowID passes through.

**Fix:** trim+nonempty check; return existing unchanged.

### F31 — Auto-insert concept dedup is case-sensitive
`runner/remediation.go:218-220`. "No-Open-Finding" misses dedup.

**Fix:** lowercase comparison via `strings.EqualFold`.

### F32 — HarnessErrors flat strings; lose round + sub-activity provenance
`runner/remediation.go:126`. Cross-round triage is hard.

**Fix:** prefix attack-side errors with round number when copying
into the report.

### F33 — Test coverage gap: concurrent Attack on different arrows
`runner/adversarial_test.go`. No t.Parallel, no go-routine fan-out.
F1 and F2 invisible without it.

**Fix:** add `TestAdversary_ConcurrentAttacks` with `-race` runs.

### F34 — Test coverage gap: AcceptedRisk vs Resolved convergence
`runner/remediation_test.go`. countOpenFindings excludes both, but no
test pins the policy.

**Fix:** add `TestRemediation_AcceptedRiskCountsAsConverged`.

---

## Low

### F35 — Label() out-of-range returns empty string
`runner/depthladder.go:111`. Findings can have empty labels in
description. Mirror FindingStatus.String's `invalid-…` pattern.

**Fix:** return `fmt.Sprintf("invalid-depth-rank(%d)", r)`.

### F36 — `DefaultDepthLabels` is a mutable public global
`runner/depthladder.go:54`. Cross-test contamination if a test mutates
it.

**Fix:** rename to `defaultDepthLabels` (unexported); expose
`DefaultDepthLabels()` returning a copy.

### F37 — defaultAdversaryFindingIDGen tight-loop uniqueness untested
`runner/adversarial.go:424-436`. Tight loop with mocked Now could
collide.

**Fix:** add test asserting 10k unique IDs in tight loop.

### F38 — PassID re-use across all clauses in one attack — undocumented
`runner/adversarial.go:278`. Document the intent.

**Fix:** comment at the call site.

### F39 — Test gaps: hook panic, bad-shape FindingRecord, Unevaluated clause, Round numbering
`runner/adversarial_test.go`. Multiple coverage holes.

**Fix:** add table-driven coverage for each.

### F40 — ensureDefaults runs before required-field validation
`runner/adversarial.go:227-242`. Mutation persists on validation
failure.

**Fix:** moot once F1's eager-init fix lands.

### F41 — VerificationAutoInsert pre-allocates 2 extra slots even when both skipped
`runner/remediation.go:208`. Minor heap noise.

**Fix:** pre-count needed inserts; allocate exact.

### F42 — Test asserts position-of-insert instead of presence
`runner/remediation_test.go:195-204`. Refactor-fragile.

**Fix:** build a concept-set map and assert presence.

### F43 — Test gap: attack.Round override behavior
`runner/remediation_test.go`. F8 contract pin missing.

**Fix:** add `TestRemediation_AttackRoundOverride`.

---

## Highest-risk areas

1. **Convergence semantics (F6, F9, F16)** — the loop trusts the
   FindingsStore but doesn't reconcile with stale findings, raise
   failures, or unevaluated state. Three distinct ways to silently
   skip a real defect.
2. **Concurrency contract (F1, F2, F3)** — race detector will fire
   under realistic harness use; the documented "thread-safe across
   arrows" claim is false.
3. **Verification soundness (F10, F11, F12, F13)** — the auto-inserted
   gate has TOCTOU, accepts unsanitized strings, and silently passes
   on empty / zero-min inputs.
4. **Hook trust boundary (F5, F18)** — operator-supplied LLM hooks
   can panic the runner or smuggle bad data past Raise.

## Remediation plan

No deferrals (user direction). Order chosen so structural fixes don't
churn later changes:

1. Add `runner/sanitize.go` (shared helper extracted from amendment).
2. Adversary single-shot + eager defaults (F1, F2, F3, F40).
3. Hook safety: panic recovery + pre-validation (F5, F18).
4. Unevaluated clause handling (F4).
5. Per-finding timestamps + raisedAt nano (F17).
6. Stale below-min auto-resolve (F16).
7. ClassificationsStore: SnapshotArrow + Version + Forget +
   Observer (F10, F14, F15, F20).
8. Mindepth: snapshot use + sanitize evidence + empty-arrow-id +
   empty-requirements-Unevaluated (F11, F13, F25).
9. Requirement validation (F12, F21, F22, F23, F24).
10. Remediation loop: ctx checks, error budget, severity threshold,
    contract docs (F7, F9, F26, F27, F28, F32).
11. Loop helpers: AttackBuilder.Round drop, AttackReport rename,
    HarnessErrors prefix (F8, F19).
12. VerificationAutoInsert: clauseID synth, arrowID validate,
    case-insensitive dedup, exact allocation (F29, F30, F31, F41).
13. Low-tier polish: Label fallback string, default-labels
    encapsulation (F35, F36).
14. Test coverage additions (F33, F34, F37, F39, F42, F43); doc-only
    fixes (F38).
