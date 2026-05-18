# Validation pass 6 — adversarial review of phase 6 work

Cold-context adversarial pass on phase-6 (in-memory Grid +
on-the-spot arrow creation per gates.md §12). One adversary agent,
15 findings.

**Severity distribution:** 0 Critical, 5 High, 6 Medium, 4 Low.

**Per user direction:** fix all findings, no deferrals.

---

## High

### F1 — Lookup returns shared slice/map headers (caller-poisoning)
`runner/grid.go:195` (pre-fix). Mutating returned Clauses/Args
poisons stored state.

**Fix:** `deepCopyArrow` deep-copies on Append AND Lookup; Clause.Args
map is also copied.

### F2 — ArrowDefinition.Validate accepts blank Stratum/Context
`runner/grid.go:60-87` (pre-fix). Whitespace-only fields fork state
into separate map keys.

**Fix:** trim+nonempty checks on Stratum and Context.

### F3 — Append and AppendOnTheSpot duplicate the mutation body
`runner/grid.go:146-191` (pre-fix). Drift hazard.

**Fix:** `appendInternal(def, kind, bumpInterruption)` shared body.

### F4 — No cap on Clauses/Requirements per arrow
`runner/grid.go:60-87` (pre-fix). LLM-backed definer can OOM the
runner.

**Fix:** `maxArrowClauses = 256` + `maxArrowRequirements = 256`
checks in Validate.

### F5 — Self-cert check covers SourceRole only, not TargetRole
`runner/onthespot.go:175-179` (pre-fix). Conservative reading of
§12.2: target role is also a stakeholder.

**Fix:** check BOTH source and target. Documented as a conservative
reading; spec text is ambiguous and may need analyst clarification.

---

## Medium

### F6 — DetectUndeclared swallows malformed Transition silently
`runner/onthespot.go:82-99` (pre-fix). Returns (zero, false) on
validation error, same as "valid, declared."

**Fix:** signature changed to `(Suspension, bool, error)`; malformed
Transition surfaces explicit error.

### F7 — Suspension fields not trimmed; identity compares ambiguous
`runner/onthespot.go:92-99` (pre-fix).

**Fix:** `Transition.normalize()` trims IDs and sanitizes
UpstreamArtifactRef before placing into Suspension.

### F8 — UpstreamArtifactRef + Attestation.Reason unbounded
`runner/onthespot.go:44-48, 122-125` (pre-fix).

**Fix:** `maxUpstreamRefLen` and `maxAttestationReasonLen` enforced
in Transition.Validate / ResolveOnTheSpot. Reason sanitized via
sanitizeOneLine when used.

### F9 — ResolveOnTheSpot doesn't check ctx.Err() upfront
`runner/onthespot.go:181-184` (pre-fix). Already-cancelled ctx
wastes definer work.

**Fix:** ctx.Err() checked at function entry AND after definer
returns.

### F10 — Grid observers fire under write Lock; slow observer stalls Lookups
`runner/grid.go:130-134, 157-162`. Documented; no enforcement.

**Fix:** GridObserver docstring tightened — "MUST be fast and
non-blocking" carries through.

### F11 — Definer-produced invalid def surfaces as generic Validate error
`runner/onthespot.go:202`.

**Fix:** `ErrDefinerProducedInvalid` sentinel wraps the Validate
error.

---

## Low

### F12 — `ErrArrowUndeclared` exported but unused
`runner/grid.go:139` (pre-fix). Dead code.

**Fix:** removed.

### F13 — `TestResolveOnTheSpot_DefinerPanicRecovered` vacuous assertion
Test matched against a never-matching local sentinel.

**Fix:** real sentinel `ErrDefinerPanicked` exported; test asserts
via `errors.Is`.

### F14 — `ErrArrowAlreadyDeclared` no documented recovery path
`runner/grid.go`. Caller doesn't know whether to retry or escalate.

**Fix:** Append doc comment explains: caller SHOULD Lookup the
now-declared arrow and proceed.

### F15 — No race-detector test for Grid observer + concurrent mutations
`runner/grid_test.go`. Coverage gap.

**Fix:** `TestGrid_ConcurrentAppendLookupObserve` added.

---

## Remediation summary

All 15 findings addressed. Grid is now insulated against
caller-side poisoning, single-shot on the spec invariants
(stratum/context required, self-cert wide), and bounded against
LLM-driven RAM-DoS. ctx-cancel honored. Tests cover the formerly
vacuous panic-recovery assertion AND a fresh concurrent-mutation
race test.
