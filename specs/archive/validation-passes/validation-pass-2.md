# Validation pass 2 — round-3 cold read

Cold-context end-to-end coherence check against the round-3-reconciled
`gates.md` + `roles/*.md` on 2026-05-18. Different lens from
`validation-pass-1.md` (analyst consistency) and
`phase-3-architect-findings.md` (architect buildability) — this is a
**use-the-schema-end-to-end** scrutiny.

**Verdict.** Not ready for the enforcement-spine implementation. The
state-space framing is sharper than round 2, but the multi-cell /
multi-context dynamics (concurrent amendments, mid-flight invalidation
in *another* cell, adversarial-phase role identity,
initialization-arrow producer) are exactly the surface an implementer
hits when wiring the harness. One more iteration warranted.

The three blocker clusters: (a) producer/role identity for
adversarial-phase and initialization-arrow, (b) global
grid-amendment serialization, (c) `insufficient-basis` and
`unevaluated`-finding routing.

---

## Findings (hardest first)

1. **[silent]** Multi-context coordination across the grid. `gates.md`
   treats each `(role-pair, stratum, context)` cell as independent,
   but when ContextA's diamond is mid-flight and ContextB's analyst
   lands a grid amendment, nothing in §7.2 or §12 says whether
   ContextA must abort, whether the amendment originates from a cell
   or a project-global queue, or how concurrent amendments serialize.

2. **[concurrency]** Grid-version race during amendment. §7.1a fixes
   arrow-id by `grid-version`, and §7.2 marks dependents `invalidated`
   when a spec changes — but two integrator passes (ContextA→analyst
   and ContextB→analyst) can both propose amendments concurrently
   with no defined ordering, lock, or merge of v→v+1 transitions.
   §12 says "written back as N+1" but does not name the writer-locking
   mechanism.

3. **[attestation-gap]** Initialization attestation is self-referential.
   §2.3 lists init's exit-gate `attested` clauses including "every
   declared arrow's depth-type assignments are attested," yet §6
   already says depth-type assignment is itself an `attested` clause
   produced at init — these are the same items, with no separate
   hint/locations defined for them and no statement of who emits the
   initialization-arrow hints.

4. **[silent]** No producer for initialization-arrow hints. §5.4
   forbids self-certification and requires a hint per `attested`
   clause, but the initialization arrow has no role file. Who emits
   hints for the init clauses in §2.3 is undefined.

5. **[contradiction]** Adversarial phase role identity is undefined.
   §11 says a "separate instance from the producer (clean context)
   attacks the upstream artifact," but §1 declares "no standalone
   adversary role" and "ghyll runs one role at a time." Which
   role-id labels the adversarial pass, what its routing tier is,
   and how it is recorded in the attestation path (§10.2 uses
   `<role-pair>`) is unspecified.

6. **[frame-violation]** Preserved findings carry hidden temporal
   coupling. §7.2's mid-phase invalidation rule preserves findings
   across an `aborted` pass and feeds them as "hints" into the next
   pass — yet §0 says cell state is `(clause statuses, findings,
   arrow status)` only. Arrow-id changes with grid-version, so the
   preserved findings are bound to a now-defunct arrow-id.

7. **[depth-confusion]** Adversarial depth classification vs. clause
   depth-type collision. §11.1's depth ladder classifies "what depth
   the upstream artifact reached" per requirement, but no clause
   concept references it. There is no defined clause that asserts
   "every requirement classified ≥ declared minimum," so depth-ladder
   findings are raised but their gating goes only through
   `no-open-finding` + severity, never directly.

8. **[doc-drift]** `roles/analyst.md` Layer-table prose says "the six
   strata defined in `gates.md` §2.1" — strata are defined in §3.1,
   not §2.1 (drifted after §0 was inserted ahead of §2).

9. **[contradiction]** Re-attack producer identity. §7.3's finding
   lifecycle says "producer fixes → adversarial phase re-attacks,"
   requiring the adversarial instance to persist or be re-spawned
   with the same context; `integrator.md` G9 asserts re-test in
   isolation by integrator itself. The attacker identity across
   remediation rounds is unspecified.

10. **[attestation-gap]** `op-id` provenance. §10.2 records `op-id`
    but nothing defines operator identity, multi-operator scenarios,
    or how the harness binds a verdict to a human (relevant when
    teams invoke ghyll).

11. **[silent]** Bootstrap severity threshold for the initialization
    arrow. §7.3 says threshold is declared at init (§2.1, default
    `medium`), but the init arrow itself needs a threshold to
    evaluate its own `no-open-finding` synthesis (§11). Bootstrap
    floor undefined.

12. **[concurrency]** `single-active-role-instance` scope. §5.1
    defines it per `(role, stratum, context)` — but a diamond
    traversal spans all strata for one context; nothing prevents two
    cells in different strata of the same context from running
    simultaneously, which the role files imply is not intended
    (analyst writes `domain-model.md` as a whole, not per-stratum).

13. **[silent]** `insufficient-basis` routing. §10 says it "routes
    per the escalation paths" but no escalation path for
    `insufficient-basis` is defined in `gates.md` or any role file.

14. **[contradiction]** `aborted` pass-status applies only to
    invalidation in §7.1a, but a pass aborted for reasons other than
    invalidation (operator interrupt, crash, manual stop) has no
    defined arrow status.

15. **[depth-confusion]** Severity assignment is `depth-sensitive`
    (§7.3); an `unevaluated` severity makes the finding `unevaluated`
    — but `no-open-finding` only checks `open` vs
    `resolved`/`accepted-risk`. An `unevaluated` finding's gating
    effect is undefined.

16. **[doc-drift]** `roles/integrator.md` references `direction.md`
    §3.7 which is outside the schema files but is load-bearing for
    the amendment-arrow mechanism — `gates.md` alludes to the
    amendment cycle without defining the integrator→analyst arrow
    semantics directly.

17. **[silent]** Residue R imputation. §3.3 imputes residue cost at
    "max per-clause cost" (`write-residue-note` = 5), but the unit is
    *per-clause*, not per-cell. Number of clauses on an undeclared
    cell is unknown. The conversion to a cell-level cost is undefined.

---

## What needs operator decisions (round 4)

The three blocker clusters and a few smaller items. Quick fixes for
the doc drift and obviously-fixable items can ride in the same commit
as the round-4 decisions.

**Round 4 candidates:**

- Producer/role identity for special arrows (findings 3, 4, 5, 9).
- Multi-cell grid-amendment coordination (findings 1, 2, 6, 12, 14).
- `insufficient-basis` + `unevaluated`-finding routing (findings 13, 15).
- Smaller items folded in: operator-identity / `op-id` provenance
  (10), bootstrap severity threshold for init (11), depth-ladder
  gating mechanism (7), residue-R per-cell conversion (17).

## What can be patched without a decision

- **#8 doc-drift**: fix `analyst.md` `§2.1 → §3.1`.
- **#16 doc-drift**: `integrator.md` should reference `gates.md` for
  the amendment mechanism, not (only) `direction.md` §3.7 — either
  promote that mechanism into `gates.md` or accept the
  forward-reference explicitly.
