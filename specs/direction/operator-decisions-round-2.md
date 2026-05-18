# Operator decisions — round 2 (status state machine)

Closes the second design pass identified in
`operator-decisions-round-1.md`. Combined with round 1, this is
enough to draft a revised `gates.md` §5 / §9 and a small adjustment
to `roles/analyst.md`.

---

## Decisions

### D8 — `unable-to-hint` collapses into `unevaluated`; a finding is raised

**Source:** validation findings 5, 15.

Two terminal statuses for "instrument too thin to decide" are
redundant. The clause status is **`unevaluated`** with an optional
`reason` field (`depth-below-required` / `no-rule-selectable-locations`).

The producing role *also* raises a **finding** of type
`unable-to-hint` against itself. This preserves the
producer-acknowledged-thinness signal without doubling the status
set. Findings are processed in the adversarial/remediation phase per
`direction.md` §3.5.

### D9 — Entry preconditions are exit clauses on the inbound arrow

**Source:** validation finding 2.

Reframe: an "entry precondition" on role R is just an exit clause of
the *upstream* arrow as viewed from R's side. The harness check runs
on the upstream arrow's exit gate, not on a separate inbound check.

Consequences:

- `gates.md` §3 no longer needs a separate "entry precondition"
  concept. The role contract has one section: exit gate clauses.
- `roles/analyst.md` E1–E3 move to the (definition-phase → analyst)
  arrow's exit gate. For the very first project, the v0 grid (D6)
  ships those clauses as part of the harness's bootstrap arrow.
- No `refused` status is needed. Entry-precondition failure becomes
  a normal clause `fail` on the upstream arrow, which derives the
  arrow status as `blocked` per the precedence rules below.

### D10 — Invalidation: operator-declared dependencies with conservative fallback

**Source:** validation finding 14, `direction.md` §3.7.

Each arrow's definition optionally lists the spec artifacts its
clauses depend on. When the analyst produces a grid amendment, the
harness marks:

- Arrows that **declared dependence** on a changed spec artifact:
  `invalidated`.
- Arrows that **declared no dependencies**: `invalidated`
  (conservative fallback).
- Arrows that **declared dependencies and none changed**: status
  unchanged.

Encourages dependency declaration without making it mandatory. The
count of conservatively-invalidated arrows on each grid amendment is
a quality signal — if it stays high, declarations are missing.

---

## Canonical state machines

These follow from rounds 1 and 2. They are what `gates.md` §5 / §9
should be rewritten to say.

### Clause lifecycle

```
                          ┌─ machine evaluator → pass | fail
(initial: pending) ───────┤
                          └─ attested → awaiting-attestation
                                           └─ operator → pass | fail | insufficient-basis

At any evaluation, if the model depth used was below the clause's
declared depth requirement, the clause status is `unevaluated`
(reason: depth-below-required) instead of pass/fail/etc.

At hint emission, if the role cannot select locations by a stated
rule, the clause status is `unevaluated`
(reason: no-rule-selectable-locations), AND the role raises a
finding of type `unable-to-hint` against itself.

Re-traversal at deeper tier:
  unevaluated  → re-evaluate fresh; status replaced by new verdict.
  fail         → re-evaluate after producer fix; new verdict overwrites.

Status is per-pass; pass history lives in the checkpoint log, not
in the status field.
```

**Clause status set:**
`pending`, `pass`, `fail`, `awaiting-attestation`,
`insufficient-basis`, `unevaluated`.

### Arrow lifecycle (derived)

An arrow's status is the **most severe** status of its clauses,
plus invalidation events. Precedence, most severe first:

| Arrow status | When |
|---|---|
| `invalidated` | An upstream spec the arrow declared dependence on (or, for non-declaring arrows, any upstream spec) changed since last traversal. Supersedes everything below. |
| `blocked` | Any clause is `fail`. |
| `unevaluated` | No clause is `fail`, but at least one clause is `unevaluated`. |
| `provisional` | All evaluated clauses are `pass`, but at least one attested clause is `awaiting-attestation` or `insufficient-basis`. |
| `complete` | All clauses are `pass`. |

Precedence consequences:

- An arrow with both `unevaluated` clauses and `awaiting-attestation`
  clauses is `unevaluated`, not `provisional` — "we couldn't decide
  it" trumps "we decided but want confirmation."
- An arrow with both `fail` clauses and `unevaluated` clauses is
  `blocked` — fail signals are actionable; unevaluated signals can be
  re-traversed at depth, but the fails must be addressed first.
- Only `complete` arrows satisfy the next role's entry condition (now
  reframed as upstream arrow exit per D9). `provisional` does not.

**Arrow status set:**
`complete`, `provisional`, `unevaluated`, `blocked`, `invalidated`.

### Finding lifecycle

Findings are raised by the adversarial phase or by a role against
itself (D8). They live on the **arrow artifact**, not on a clause.

```
(open) ──┬─ producer fixes → adversary re-attacks ──┬─ resolved
         │                                          └─ open  (still defective)
         │
         └─ producer proposes accepted-risk ──┬─ operator attests → accepted-risk
                                              └─ operator rejects → open
```

Verification phase machine check: any finding above a severity
threshold still `open` → arrow status `blocked` (a `fail` clause is
synthesized to express this; severity threshold is phase 3
architect work, see round 1's "what's still open").

**Finding status set:** `open`, `resolved`, `accepted-risk`.

### Project-level status (composite, not a state machine)

`(complete-against-grid-vN, residue-R, unevaluated-C)`:

- **vN** — the current arrow grid version.
- **R** (D7) — sum of operator-action cost across undeclared
  `(stratum, context)` cells.
- **C** — count of arrows whose status is `unevaluated`.

C > 0 is the "green but will break on deployment" failure mode and
must never be hidden inside an aggregate pass. R > 0 is the "we
deferred deciding" deferred-attention surface.

---

## Mapping back to validation findings

| Finding | Status after rounds 1+2 |
|---|---|
| 1 (stratum/context undefined) | resolved (D3, D4) |
| 2 (entry-precondition failure status) | **resolved** (D9 — reframed as exit clauses) |
| 3 (hint/precondition flow) | **resolved** (D9 — single gate, hints apply normally) |
| 4 (catalogue contradiction) | resolved (D1, D2) |
| 5 (status vocabulary diverges) | **resolved** (full state machines above) |
| 6 (arrow weight) | resolved (D5) |
| 7 (severity enum) | open — phase 3 architect |
| 8 (`unevaluated` lifecycle) | **resolved** (per-pass; replaced on re-traversal) |
| 9 (G6 orphan check mechanism) | open — phase 3 architect |
| 10 (predeclared analyst clauses vs §4) | resolved (D1, D2) |
| 11 (bootstrap self-reference) | resolved (D6) |
| 12 (5 other roles unreconciled) | known — post-phase-3 |
| 13 (`provisional` semantics) | **resolved** (state machines above) |
| 14 (residue R arithmetic) | resolved (D7) |
| 15 (`unable-to-hint` downstream) | **resolved** (D8) |

**Resolved: 12 of 15.** Remaining three (7, 9, 12) are phase 3 work
and the post-phase-3 role-reconciliation pass.

---

## Required changes to existing documents

A separate task, not done here.

**`specs/direction/gates.md`:**

- §3 — remove "entry precondition" as a separate concept (D9). Each
  role has one contract section: exit-gate clauses.
- §5 — rewrite to define the full clause status set, the per-pass
  semantics, the `unevaluated.reason` field (D8), and the
  re-traversal rule.
- §9 — rewrite to define the full arrow status set, the precedence
  rules, the invalidation propagation policy (D10), and the finding
  lifecycle.
- §10 — adjust on-the-spot creation to use the new state machine
  vocabulary.

**`specs/direction/roles/analyst.md`:**

- Move E1–E3 from "Entry precondition" to the exit gate of the
  (definition-phase → analyst) arrow (D9). For greenfield projects,
  these clauses ship in the v0 grid (D6).
- Update Session management's status vocabulary to match (D8: drop
  `awaiting-attestation` / `blocked` from per-clause reporting where
  it conflicts with the canonical set; keep them for arrow-level
  reporting).

**`specs/direction/build-notes.md`:**

- Note phase 2 is closed and what is still in phase 3.

---

## What is still open for phase 3 (architect)

1. **Per-concept default attestation costs** (D5 — the values for
   each catalogue concept, e.g., `compiles = 0`, `mutation-score = 3`).
2. **Operator-action unit set** (D5 — `confirm` / `record-locations`
   / `write-residue-note` definitions and their action-cost values).
3. **Severity enum for findings** (validation finding 7 — the
   threshold that triggers `blocked` per the finding-lifecycle rule).
4. **G6 orphan check mechanism** (validation finding 9 — how
   `no-orphan-symbol` is bound across languages; analyst's
   exported-behavior-to-spec-clause trace mechanism).
5. **Dependency declaration syntax for invalidation** (D10 — what
   form does the per-arrow dependency list take? Per-clause or
   per-arrow? Spec artifact IDs or file paths?).

Phase 3 is architect-level. The analyst role escalates these rather
than asking the operator directly (per `roles/analyst.md` rule 4).
