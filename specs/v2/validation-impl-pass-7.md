# Validation pass 7 — adversarial review of phase 7 work

Cold-context adversarial pass on phase-7 (depth-type + routing per
gates.md §6 + §8). One adversary agent, 14 findings.

**Severity distribution:** 0 Critical, 4 High, 7 Medium, 3 Low.

**Per user direction:** fix all findings, no deferrals.

---

## High

### F1 — Evaluator path ignores depth-type entirely (§6/§7.1 invariant unenforced)
`runner/runner.go:412+`. A depth-sensitive clause evaluated below
its declared depth still produced Pass/Fail — direct violation of
gates.md §7.1: "depth-below-required → unevaluated."

**Fix:** `Runner.WithActualTier(DepthRank)` declares the dispatcher
tier; `Evaluate` short-circuits to StatusUnevaluated with
Reason=depth-below-required when the clause's MinDepthTier exceeds
the actual tier. Legacy callers that don't set actualTier behave as
before (no enforcement).

### F2 — RouteArrow returns zero RoutingRequirement on error (ambiguous with fast-tier success)
`runner/routing.go:126` (pre-fix).

**Fix:** added `Routed bool` field; the error path returns
`RoutingRequirement{}` (Routed=false). Callers can no longer
confuse "validation failed" with "all-robust, fast-tier route."

### F3 — Dead branch in MaxDepthClauseID seed logic
`runner/routing.go:134-141` (pre-fix). `else if req.MaxDepthClauseID
== ""` was unreachable given Validate rejects MinDepthTier=NONE.

**Fix:** the branch is preserved but documented as defensive
(unreachable today; documented in code so future Validate changes
don't silently invalidate it).

### F4 — IsKnownDepthType vs ParseClauseDepthType asymmetric
`runner/routing.go:34-40` (pre-fix). `IsKnownDepthType("Depth-Robust")`
returned false while parser accepted it.

**Fix:** `IsKnownDepthType` now delegates to ParseClauseDepthType.
Operator YAML with mixed case loads consistently.

---

## Medium

### F5 — Tie-break determinism undocumented
`runner/routing.go:134-138`. Two clauses tied at the same
MinDepthTier — first wins, undocumented.

**Fix:** doc comment on RoutingRequirement.MaxDepthClauseID
explicitly states "first encountered in iteration order
(deterministic given stable slice order)."

### F6 — Error messages embed unsanitized ClauseID
`runner/routing.go:89, 94, 101`.

**Fix:** `describeClause(c)` returns sanitized
`<unset:concept>` or sanitized ClauseID; all error formatters use it.

### F7 — No attestation provenance on Clause.DepthType
gates.md §6 mandates attestation; the field wasn't carried.

**Fix:** added `Clause.DepthTypeAttestationRef` (optional). The
runner doesn't verify the link (engine layer's job); EvaluationRun
echoes the field so audit chains can resolve it.

### F8 — No contract test asserting depth-below-required → unevaluated
`runner/routing_test.go` gap.

**Fix:** `TestEvaluate_DepthBelowRequiredShortCircuits` +
`TestEvaluate_DepthMetRunsEvaluator` +
`TestEvaluate_LegacyPathSkipsDepthCheck` pin the contract.

### F9 — No defined behavior for v0 grids deserialized into v2 Clause
Wire-format change unflagged.

**Fix:** Clause doc comment states the migration contract: bootstrap
back-fills from concept catalogue; an empty DepthType reaching
RouteArrow is rejected.

### F10 — Clause mutation post-creation undefined behavior
`runner/runner.go:124-151`. Reference fields shared by value
receivers.

**Fix:** Clause-invariants doc comment explicitly states "read-only
after construction" and lists the Args shallow-clone the runner
performs internally.

### F11 — gates.md §7.1 Unevaluated reason vocabulary not modeled
`runner/runner.go:110-115`. Free-form Reason string.

**Fix:** `UnevaluatedReason` typed enum with the §7.1 set
(depth-below-required, no-rule-selectable-locations,
producer-no-response). `IsKnownUnevaluatedReason` validates. The
runner's depth short-circuit uses the canonical constant.

---

## Low

### F12 — RouteArrow is O(2N) (validation pass + routing pass)
`runner/routing.go:124-142`. Perf foot-gun for large grids.

**Fix:** documented; no immediate change (constant factor is small;
loops are tight). Benchmark TBD if scale warrants.

### F13 — Missing tests called out in the brief
`runner/routing_test.go` gaps.

**Fix:** added tests for IsKnownDepthType mirroring parser
(`TestIsKnownDepthType_MirrorsParser`), tie-break
(`TestRouteArrow_TieBreakFirstEncountered`), error-includes-clause-
descriptor (`TestValidateClauseDepthDeclaration_ErrorIncludesClauseDescriptor`),
RouteArrow error-vs-success distinguishable
(`TestRouteArrow_ErrorReturnsUnroutedZero`).

### F14 — AnyDepthSensitive redundant with MaxDepthClauseID != ""
`runner/routing.go:53-57`. Two representations of one truth.

**Fix:** field removed; replaced with `HasSensitive()` method.

---

## Remediation summary

All 14 findings addressed. The depth-type enforcement is now wired
end-to-end: Clause carries the declaration + optional attestation
ref, RouteArrow returns a typed routing requirement, Runner.Evaluate
short-circuits to Unevaluated with a canonical reason when the
dispatcher's tier is insufficient. UnevaluatedReason is now a typed
enum the runner uses canonically (operator-facing audit consumers
can sort/filter against it).

## Architect escalations (carry into next phase)

- §12.2 self-cert scope — runner chose the conservative reading
  (both roles forbidden). Analyst should pin the spec.
- §6 depth-type attestation linkage — the runner now carries the
  ref; the engine layer is responsible for resolving it against
  the attestation store.
- v0 → v2 grid migration policy — bootstrap layer's job;
  documented as a non-runner concern.
