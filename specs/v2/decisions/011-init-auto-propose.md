# ADR-011: Initialization is auto-propose + operator-confirm

**Status:** Accepted (2026-05-18; D20, D41)

**Context:**

The v0 grid ships with the harness but does not contain
project-specific clauses — those depend on the project's bounded
contexts, languages, and concrete arguments. Project initialization
turns v0 into v1 (the project-specific grid). The question was who
drives this:

- **Operator-authored:** operator writes the grid by hand
  (high-quality but slow; bus-factor risk).
- **Agent-authored:** the harness drafts the entire grid (fast but
  high-error; needs its own gate; risks regress).
- **Auto-propose + operator-confirm:** the harness drafts; the
  operator confirms / modifies / extends / skips each proposal.

`gates.md` §2.2 said "operator-owned, agent-assisted" but didn't
specify the mechanism. Validation-pass-2 finding #7 + #12 surfaced
that init's own behavior was underspecified (especially the
adversarial-phase application to init).

**Decision:**

Init runs in **two sub-phases**, both within the single init arrow:

**Sub-phase A — Project profile + context discovery.**

Init interrogates the operator (or scans the repo in brownfield
mode) to determine: bounded contexts, languages used, refusal-or-
proceed. The producer is the operator; the artifact is the
proposed context list. `single-active-role-instance(init, *)`
skips the context check (init is project-scoped, not
context-scoped).

**Sub-phase B — Per-`(role-pair, context)` auto-propose.**

Once contexts are declared, init iterates all
`(role-pair, context)` arrows and proposes the role file's full
exit-gate clause set per arrow:

1. Auto-propose: harness drafts each clause from
   `roles/<role>.md`'s exit-gate table.
2. Operator returns one of four verdicts per clause:
   - **`confirm`** — accept as-is.
   - **`modify`** — raise costs (never lower), tighten thresholds,
     refine arguments. Schema enforces raise-only.
   - **`extend`** — add per-context clauses beyond the role-file
     default.
   - **`skip`** — drop the clause for this `(role-pair, context)`
     arrow. **Requires a residue entry** recording why.
3. Grid is recorded **only after every proposed clause has received
   a verdict**.

Init's own arrow has an adversarial phase that attacks the
proposed grid:

- A fresh `adversary` instance reads the proposed grid + the
  operator's context-discovery rationale.
- Sub-activities apply: clause-falsification (missing clauses
  operator silently dropped), open sweep (residue not declared),
  depth classification (dependency granularity wrong).
- Findings raise; remediation runs; verification auto-inserts
  `no-open-finding` + `every-requirement-meets-min-depth`.

**Consequences:**

- **Rules in:** operator-owned grid that's still fast to produce
  (harness does the typing); structural enforcement of "no
  clause silently dropped" (skip requires residue); init follows
  its own schema (no special case).
- **Rules out:** "agent decides the grid"; "operator writes from
  scratch"; weakening clauses below role-file defaults (only
  skip-with-residue, never weaken).
- **Operator burden:** every clause proposed at init must receive
  a verdict. For 4 roles × N bounded contexts × ~10 clauses per
  role, that's ~40N verdicts at init. For N=3 contexts, ~120
  verdicts. Auto-propose makes this manageable but not trivial.
- **Refusal path** (init's own §2.4): if the project profile is
  low-risk, init proposes refusal rather than running through
  auto-propose. Operator may accept (ghyll exits) or override
  (residue note required).
- **Brownfield divergences**: residue candidates from sub-phase A
  become divergence-candidates that the analyst arrow materializes
  into `divergences.md` entries on its first traversal.
- **Init is mandatory before any other arrow** (`gates.md` §2 +
  init.md INV-1). No code path bypasses init.

See `gates.md` §2; `specs/direction/components/init.md`;
`specs/v2/features/init.feature`;
`specs/direction/operator-decisions-round-3.md` D20;
`specs/direction/operator-decisions-round-4.md` D41.
