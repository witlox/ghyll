# Phase 3 — architect-lens findings

Cold-context architect pass over the reconciled `gates.md`,
`roles/analyst.md`, and `direction.md` on 2026-05-18. The analyst pass
(`validation-pass-1.md`) hunted **consistency**. This pass hunts
**buildability** — what an implementer would hit when typing the
schema's contracts into code.

**Verdict.** With the five phase-3 items filled in (architect
proposals below), the schema is **still not implementation-ready**.
A phase 4 is needed before the enforcement spine can be built.

---

## Findings (hardest first)

1. **[missing-contract]** §4.3, §10.1 — Per-arrow instances reference
   catalogue concepts by name and supply arguments, but no concept's
   argument signature is specified. `mutation-score(threshold?, scope?)`,
   `cardinality-check(query?, n?)`, `trace-link-present(from?, to?)`
   are all guessed. An implementer cannot write the catalogue
   interface without this.

2. **[phase-3] [missing-contract]** §4.1 `no-orphan-symbol`, analyst
   G4 — "Every exported symbol traces to a declared spec clause" has
   no language-binding mechanism analogous to `lint-clean.<lang>`;
   what counts as "exported," how clauses are addressed (IDs?), and
   what produces the trace index are unspecified.

3. **[phase-3] [missing-contract]** §6.2 invalidation, §8 dependency
   declarations — "Per-arrow dependency declarations" have no syntax,
   no granularity (file? spec ID? clause ID?), no diff rule for
   "changed." Without this, the conservative-fallback path is the only
   path that ever fires.

4. **[phase-3] [missing-contract]** §6.3 — `no-open-finding` triggers
   on "above a declared severity threshold" but findings have no
   severity field, no enum, no per-arrow threshold storage. Both the
   producer (raising) and the verifier (checking) need this to agree.

5. **[contradiction] [state-hole]** §6.2 vs §11 — Arrow precedence
   says `invalidated` supersedes everything, but §11 phases run
   Adversarial → Remediation → Verification with no defined behavior
   when an upstream amendment lands mid-phase. Race: does in-flight
   remediation discard findings? Re-enter adversarial?

6. **[order-of-ops]** §11 vs §6.2 — Verification synthesizes
   `no-open-finding`, but that machine clause is a per-arrow instance
   (§4.3), not in the universal base set (§4.2). An arrow whose
   definition forgot to add it has no enforcement of finding-disposal.

7. **[order-of-ops] [state-hole]** §2.4, §8 — The definition phase
   "runs as a normal arrow against the v0 grid," but the v0 grid only
   carries the four universal-base machine clauses. The §8 "central
   attested clauses" of the definition phase have no slot in v0.

8. **[phase-3] [missing-contract]** §10.1 — Operator-action unit
   semantics are only described by name and cost; no spec of what
   "record" means as an artifact (file? log? attestation receipt?),
   where it persists, or how the harness verifies the operator did
   the higher-cost action vs clicked `confirm`.

9. **[state-hole]** §12.4 — On-the-spot arrow creation writes back as
   grid v(N+1); concurrent traversals on adjacent arrows referencing
   grid vN are not described. No locking, no rebase semantics.

10. **[state-hole]** §6.1 `unevaluated` re-traversal vs §6.2
    `invalidated` — "Per-pass" status with checkpoint history is
    asserted, but pass identity is undefined. Is a re-traversal after
    `invalidated` a new pass on the same arrow, or a new arrow
    instance?

11. **[catalogue-instance]** §4.1 `single-active-role-instance` —
    Needs a process-level coordinator (lock, lease) that is not named
    anywhere; the schema implies global state the harness must own
    but does not specify where it lives.

12. **[catalogue-instance]** §4.1 `mode-determinable-from-repo` —
    Takes a "named mode discriminator" as argument but the
    discriminator's evaluator type is not declared — Go function?
    script path? regex over repo state?

13. **[contradiction]** analyst.md G4 vs gates.md §4.1 — G4 binds
    `no-orphan-symbol` with `residue-target: coverage-claim`, but the
    catalogue defines `no-orphan-symbol` with no residue-target
    argument. Argument schema disagrees with only example use site.

14. **[missing-contract]** §2.3 — Residue R is "sum of operator-action
    cost across undeclared cells," but undeclared cells have no
    clauses and therefore no costs. The sum is over a yet-to-be-defined
    imputation function.

15. **[state-hole]** §6.2 — "Most severe status of its clauses" plus
    invalidation events: `invalidated` is not a clause status (§6.1),
    only an arrow status. The lift from clause-status set to
    arrow-status set is not a total function as written.

---

## Phase-3 concrete proposals (from architect agent)

These are proposals; they require operator confirmation before going
into `gates.md`. Saved here for the next round.

### Severity enum

```
severity ∈ {info, low, medium, high, critical}
```

Finding has required `severity` field. Per-arrow
`no-open-finding(threshold=medium)` default; override allowed at
definition time. Adversarial phase assigns severity per a stated rule
and discloses it (treated like a hint).

**Blocker:** rule for severity assignment is itself depth-sensitive
and needs an attested clause.

### `no-orphan-symbol` binding

```
no-orphan-symbol.<lang> = {
  extractor: <cmd producing exported-symbol list>,
  index:     <path to spec-clause IDs>,
  mapper:    <cmd or regex spec→symbol>
}
```

Bindings shipped per language like `lint-clean.<lang>`.

**Blocker:** requires every spec clause to carry a stable ID, which
the analyst artifact format does not currently mandate.

### Per-concept default costs

| Concept | Default cost |
|---|---|
| compiles | 0 |
| lint-clean | 0 |
| no-todo-marker | 0 |
| every-step-bound | 0 |
| no-orphan-symbol | 1 |
| mutation-score | 3 |
| kill-server-fails-integration | 3 |
| trace-link-present | 1 |
| acyclic-dependency-graph | 0 |
| unique-definition | 0 |
| predicate-form | 1 |
| arrow-artifact-present | 0 |
| no-open-finding | 1 |
| cardinality-check | 1 |
| mode-determinable-from-repo | 1 |
| single-active-role-instance | 0 |

Attested judgements default to 3 (`record-locations-inspected`);
residue-bearing analyst clauses to 5 (`write-residue-note`).

**Blocker:** none.

### Dependency declaration syntax

```yaml
dependencies:
  - artifact:    <path>
    granularity: file | section | clause-id
    on-change:   invalidate | reevaluate-named-clauses
```

Diff by content hash at clause-id granularity where IDs exist; by file
hash otherwise.

**Blocker:** requires stable clause-IDs across artifacts (same blocker
as `no-orphan-symbol` binding).

### Operator-action unit semantics

Each unit produces a typed attestation record:

```
confirm                     → {clause, verdict, ts, op-id}
record-locations-inspected  → {... , inspected: [file:line, ...]}
write-residue-note          → {... , inspected, residue-note: <text>}
```

Records appended to `attestations/<arrow>/<pass>.jsonl`. The verifier
of cost is **presence of the required fields**, not user-friction.

**Blocker (honest):** distinguishing genuine inspection from
fast-clicked confirms cannot be enforced. The cost is procedural, not
behavioral, and the schema should say so.

---

## What needs a phase 4

The non-phase-3 findings (1, 3, 5, 6, 7, 9, 10, 13, 14, 15) cluster
into four phase-4 themes:

1. **Concept argument schemas.** Every catalogue concept needs a typed
   signature: what arguments, what return shape, what evaluator
   contract. Without this finding #1 is a wall.
2. **Phase/invalidation interleaving rules.** What happens when an
   upstream amendment arrives mid-arrow. Pass/arrow identity (#10)
   is part of this.
3. **v0 grid contents beyond the universal base.** §2.4 lists three
   things v0 ships; the definition-phase exit gate's attested clauses
   are not among them. Resolve before any project's first run.
4. **Stable artifact IDs.** Both the `no-orphan-symbol` binding and
   the dependency declaration syntax depend on this. Likely needs a
   shared clause-ID convention across analyst output formats.

Phase 4 should run as another operator-decision round on these four
themes, then a re-reconciliation of `gates.md` and the role files.

---

## What is fixable now (small refinements)

A subset of findings can be patched without operator input:

- **#6** — auto-insert `no-open-finding` in `gates.md` §11
  verification phase when adversarial phase ran. Add to §11 directly.
- **#7** — `gates.md` §2.4 names what v0 ships; expand to include the
  definition-phase exit gate's central attested clauses (already
  named in §8).
- **#13** — `analyst.md` G4 uses an argument the catalogue doesn't
  declare; either remove the argument or note it's pending phase 4.
- **#14** — `gates.md` §2.3 needs an explicit imputation rule for
  undeclared-cell cost. Conservative default: max default cost across
  the catalogue (`5`, i.e. `write-residue-note`).
- **#15** — `gates.md` §6.2 should explicitly state that
  `invalidated` is set by the grid-amendment process, not lifted
  from clause states.

These are applied in the same commit as this findings doc.
