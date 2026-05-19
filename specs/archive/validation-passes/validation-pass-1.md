# Validation pass 1 — cold read of gates.md + roles/analyst.md

Per `direction.md` §7, the design must pass a cold read before any
enforcement spine is built. This is pass 1.

**Inputs.** Only `gates.md` and `roles/analyst.md`. The agent did not
see `direction.md`, `build-notes.md`, or any project context.

**Verdict.** **Not yet buildable.** The design's intent is coherent and
the diagnoses it offers (Kiseki-style shallow-test rot, fixed-checklist
failure, magnitude-blind self-rating) are sharp, but the enforcement
spine has three load-bearing holes (see findings 4, 5, 10 below).
Another design pass is required before code.

---

## Findings (hardest first)

1. **[undefined] "stratum" / "context" never defined.** gates.md §2
   builds the entire arrow grid on `strata × contexts`, and §7
   reiterates it, but neither term is defined; analyst.md treats
   "bounded context" (DDD) as the meaning of "context" (E1), which may
   or may not be what §2 intends. A builder cannot implement the grid
   index without this.

2. **[gap] No status for an entry precondition that fails.** gates.md
   §3 says the harness checks entry preconditions but defines no
   status, hint, or routing for failure; §9's `provisional` / `fail` /
   `insufficient-basis` apply only to exit-gate `attested` clauses.
   Analyst.md E1–E3 are `machine` but the schema never says what
   happens when E1 returns false.

3. **[contradiction] Hints are required only for `attested` clauses,
   yet analyst entry preconditions are all `machine` and unhinted — but
   gates.md §10 demands attestation of arrow *definitions* including
   on-the-spot ones; the entry-precondition arrow is never reconciled.**
   gates.md §8 vs. §10 vs. analyst.md Entry precondition table. The
   upstream-arrow-to-entry-precondition flow is simply not specified.

4. **[underspecified] "the harness checks E1" — how.** analyst.md E1
   ("names exactly one bounded context") is tagged `machine`, but
   gates.md §4 fixes the machine-clause catalogue to nine names;
   `names-one-bounded-context` is not in it, and §4 forbids unlisted
   machine clause *types*. E1–E3, G3, G5, G6, G7, G8 are all outside
   the fixed catalogue — direct contradiction.

5. **[contradiction] Status vocabulary diverges.** analyst.md Session
   management lists `awaiting-attestation` and `blocked` as reportable
   per-clause / arrow statuses; gates.md §5/§9 defines only
   `unevaluated`, `provisional`, `pass`, `fail`, `insufficient-basis`
   (and `complete` implicitly). `awaiting-attestation`, `blocked`,
   `resolved`, `invalidated` appear in analyst.md and/or `direction.md`
   but never in gates.md.

6. **[undefined] Arrow "weight".** gates.md §9 says weight is "set per
   arrow at definition time" and drives operator friction, but gives no
   scale, units, or mapping from weight to required operator action;
   analyst.md never assigns a weight to analyst→architect.
   Unimplementable as written.

7. **[undefined] "Severity" on G13.** analyst.md G13 requires failure
   modes to "carry severity"; neither file defines a severity set or
   check. As a `machine`-adjacent quality this would need an enum; as
   `attested` it would need a hint rule.

8. **[gap] `unevaluated` lifecycle.** gates.md §5 introduces
   `unevaluated` and says an arrow with any `unevaluated` clause cannot
   close, but no transition is defined: does re-running at a deeper
   tier clear it? Is it persisted across passes? Analyst.md Session
   management lists it as a per-clause report value with no transition
   rule.

9. **[underspecified] G6 orphan check.** analyst.md G6 is `machine`
   `depth-robust` and requires tracing "every exported behaviour" to a
   spec clause — but no mechanism, language scope, or symbol-extraction
   rule is given; gates.md's catalogue lists `no-orphan-symbol` but the
   binding to spec clauses is undefined.

10. **[contradiction] "Per-arrow machine clauses are derived from the
    requirement" vs. analyst.md's fully predeclared G1–G8.** gates.md
    §4 says non-base machine clauses are *born with the requirement*
    during the definition phase; analyst.md hard-codes eight machine
    clauses in the role file, which would freeze them across all
    requirements — precisely the "fixed checklist" failure §4 warns
    against.

11. **[gap] Definition-phase gate is self-referential.** gates.md §7
    says depth-type assignments are themselves `attested` on the
    definition-phase gate, and §10 escalates tier for on-the-spot
    definition because depth-typing is `depth-sensitive` — but the
    *first* definition phase has no prior arrow, so nothing routes it.
    Bootstrap is unspecified.

12. **[underspecified] Diamond has six roles, schema reconciles one.**
    gates.md §1 declares analyst→architect→adversary→implementer→
    auditor→integrator, but only analyst.md exists; cross-arrow
    invariants (residue propagation, coverage-claim consumption by
    architect, who consumes G14) cannot be validated.

13. **[contradiction] `provisional` semantics.** gates.md §5 defines
    `provisional` = "passed, awaiting confirmation"; §9 says any arrow
    with an unconfirmed `attested` clause is `provisional`. Analyst.md
    Session management treats `provisional` as an *arrow* status
    alongside `complete` / `blocked`, but `blocked` is never defined in
    gates.md and `complete` is reserved in §2 for project-level
    reporting against grid vN.

14. **[undefined] "Residue R" arithmetic.** gates.md §2/§5 reports
    "residue R" as a number, but residue is defined qualitatively
    (undeclared strata/contexts; uncovered domain elements in
    analyst.md's coverage claim); how the harness produces a scalar R
    is unspecified.

15. **[gap] Hint `unable-to-hint` has no downstream effect.** gates.md
    §8 and analyst.md both let a role report `unable-to-hint`, but
    neither file says what status the clause takes — `unevaluated`?
    `insufficient-basis`? `fail`? — or whether it blocks the arrow.

---

## Pre-build prerequisites (derived from the findings)

Before the enforcement spine can be implemented, the design must:

1. Reconcile the machine-clause catalogue with declared clauses (open
   the catalogue, OR rewrite analyst.md's machine clauses as instances
   of catalogue types — finding 4, 10).
2. Pin the full status state machine across both files. Every status
   that appears anywhere must be defined in gates.md with its
   transitions (finding 5, 8, 13, 15).
3. Define "stratum", "context", "weight", "severity", and "residue R"
   quantitatively or remove them (finding 1, 6, 7, 14).
4. Specify entry-precondition failure semantics and the
   bootstrap path for the first definition phase (finding 2, 3, 11).
5. Make machine clauses outside the catalogue an explicit, attested
   extension or reduce analyst.md G3/G5–G8 to catalogue instances
   (finding 4, 9).
