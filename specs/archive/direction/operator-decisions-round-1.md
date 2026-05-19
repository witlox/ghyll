# Operator decisions — round 1

Produced by an analyst-role interrogation of the operator on
2026-05-18, following `validation-pass-1.md`. The analyst played
itself meta: the design documents (`gates.md`, `roles/analyst.md`)
were treated as the artifacts under analysis; each unresolved
contradiction or undefined term in `validation-pass-1.md` became a
candidate question; only operator-intent items were surfaced. Three
rounds of three questions each were posed per `roles/analyst.md`
behavioral rule 2 ("Max 3 questions at a time").

This is **not** a redesign of `gates.md`. It is the operator's input
to design pass 2. Pass 2 must reconcile `gates.md` and
`roles/analyst.md` with the decisions below.

---

## Decisions

### D1 — Catalogue is concept-named and language-agnostic

**Source:** validation finding 4/10.

`gates.md` §4's catalogue currently mixes concept (`compiles`,
`mutation-score`, `no-orphan-symbol`) with instrument
(`clippy-clean` — Rust-specific). For a multi-language ghyll, the
catalogue must be **closed at the concept layer**. Per-language
**instrument bindings** are configured separately and are not
catalogue entries.

- `clippy-clean` becomes `lint-clean`; the binding `lint-clean.rust =
  clippy`, `lint-clean.go = staticcheck + go vet`, etc. lives in
  per-project or per-language config.
- `compiles` stays a concept; the binding decides whether
  `compiles.rust = cargo check` or `compiles.go = go build`.
- The catalogue is **closed** at the concept layer. New concepts
  enter via a deliberate harness change, not per-role declaration.

### D2 — Add new concepts to cover analyst clauses that don't map

**Source:** validation finding 4, follow-up.

Analyst's machine clauses E2 (mode-determinable), G3
(unique-definition), G7 (no-open-divergence), G8
(arrow-artifact-present) have no clean match in the current 9
concepts. Resolution: **add new concepts to the catalogue**, rather
than reclassify these as `attested`. The catalogue grows from 9 to
~13.

Rationale (operator's): the catalogue is the harness's primitive
vocabulary and should be expressive enough that role machine clauses
*are* catalogue instances. Reclassifying as `attested` would shift
the analyst arrow's load further onto operator attention, which is
already finite and is best preserved for genuinely
depth-sensitive judgement.

### D3 — Stratum vocabulary is uniform across roles, 6 layers

**Source:** validation finding 1.

A stratum is a uniform layer concept that each role interprets in
its own medium. The vocabulary:

| Layer | Meaning | Analyst interpretation | Architect interpretation (illustrative) |
|---|---|---|---|
| 1 | Structure | Domain model, terms, shapes | Interfaces, types, module boundaries |
| 2 | Invariants | Consistency / cardinality / ordering predicates | Type-system invariants, compile-time guarantees |
| 3 | Behavior | Commands, events, queries | Behavior expressed as functions / methods |
| 4 | Composition | Cross-context interactions | Integration points, API boundaries |
| 5 | Failure | Failure modes, degradation | Error types, fault handling |
| 6 | Assumptions/risk | Falsifiable assumptions log | Architectural bets, accepted risks |

Each role's layered work specializes the same six layers. Cross-role
strata comparisons become possible (e.g., analyst L4 cross-context
spec maps to architect L4 integration boundary).

The arrow grid is **`(stratum × bounded-context)`**, where
stratum ∈ {1, …, 6}.

### D4 — "Context" is DDD bounded context

**Source:** validation finding 1.

The "context" dimension of the arrow grid is the DDD bounded context
of the work — same meaning as `roles/analyst.md` E1. No change to
analyst.md required.

### D5 — Arrow weight = sum of per-clause operator-action costs

**Source:** validation finding 6.

`gates.md` §9 "weight" is **per-clause attestation cost the operator
spends**, measured in **operator-action units**. Arrow weight is the
sum of its clauses' costs.

- Each clause-concept ships with a **harness default cost** (e.g.,
  `mutation-score` defaults to 3 actions; `compiles` defaults to 0;
  `no-todo-marker` defaults to 0; G14 coverage-residue-honest
  defaults to higher — exact values TBD by architect).
- An arrow at definition time may **raise** (never lower) a clause's
  cost for its specific traversal. The analyst→architect arrow
  raises costs above defaults; that is *how* it ends up the heaviest
  per `direction.md` §3.6.
- "Operator action" needs a defined unit set. Working draft:
  `confirm` (1), `record-locations-inspected` (3), `write-residue-note` (5).
  Exact mapping is architect work.

### D6 — v0 grid is harness-provided

**Source:** validation finding 11.

The schema's self-reference (first definition phase has no upstream
arrow, but depth-typing is `depth-sensitive` and must be attested)
is broken by **shipping a hardcoded v0 grid with the harness**. The
v0 grid contains:

- The diamond workflow as a declared arrow set.
- The universal-base machine clauses (`compiles`, `lint-clean`,
  `no-todo-marker`, `every-step-bound`) on every arrow.
- The 6-layer stratum vocabulary from D3.

A project's first definition phase runs as a **normal arrow against
the v0 grid** — there is no special bootstrap arrow. The schema
becomes its own bootstrap; v0 is the floor.

### D7 — Residue R = sum of operator-action cost across undeclared cells

**Source:** validation finding 14.

`R` in "complete against grid vN, residue R, unevaluated C" is a
**scalar in operator-action units**: the sum of attestation cost
across **undeclared (stratum, context) cells** in the arrow grid.

This makes R honest about *how much attention the operator has
deferred* by not declaring an arrow, not just "how many holes are
there." Units are consistent with D5 weight.

---

## Mapping back to validation findings

| Finding | Status after round 1 | Notes |
|---|---|---|
| 1 (stratum/context undefined) | **resolved** | D3, D4 |
| 2 (entry-precondition failure status) | open | Status state machine pass |
| 3 (hint/precondition flow) | open | Architect — implementation flavor |
| 4 (catalogue closed contradicted by analyst clauses) | **resolved** | D1, D2 |
| 5 (status vocabulary diverges) | open | Status state machine pass |
| 6 (arrow weight) | **resolved** | D5 |
| 7 (severity enum) | open | Likely architect |
| 8 (unevaluated lifecycle) | open | Status state machine pass |
| 9 (G6 orphan check mechanism) | open | Architect |
| 10 (predeclared analyst clauses vs §4) | **resolved via 4** | D1 + D2 |
| 11 (bootstrap self-reference) | **resolved** | D6 |
| 12 (5 other roles unreconciled) | known | Role reconciliation step |
| 13 (`provisional` semantics) | open | Status state machine pass |
| 14 (residue R arithmetic) | **resolved** | D7 |
| 15 (unable-to-hint downstream effect) | open | Status state machine pass |

**Resolved: 6 of 15** (and the three load-bearing ones from
validation-pass-1.md verdict).

---

## What is still open

Two coherent design passes remain before the enforcement spine is
buildable:

**Pass 2 — Status state machine.** Findings 2, 5, 8, 13, 15.
Canonize the full status set (`pass`, `fail`, `insufficient-basis`,
`unevaluated`, `provisional`, `awaiting-attestation`, `blocked`,
`resolved`, `invalidated`, possibly `unable-to-hint`) in `gates.md`
with explicit transitions. Specify what happens when an entry
precondition fails. Specify how `unevaluated` clears on
re-traversal. Specify `unable-to-hint`'s downstream status.

**Pass 3 — Architect reconciliation.** Findings 3, 7, 9, plus per-
concept attestation cost defaults from D5. This is architect-level
work (per `roles/analyst.md` rule 4: stay at domain/behavioral
level, escalate architecture to the architect). Includes: G6 orphan
check mechanism; severity enum; operator-action unit definitions
and per-concept default costs; entry-precondition arrow semantics.

The 5 unreconciled role files (finding 12) come after passes 2 and
3, not in parallel — they need the resolved schema to reconcile to.

---

## Analyst note on the meta-application

Using the analyst role to interrogate the design itself is a stretch
of the role's intended use. Two limits noted, for honesty:

1. The role file (`roles/analyst.md`) was the very artifact being
   interrogated about, so the analyst was applying its own protocol
   to its own definition. Self-application can mask gaps the role
   itself has.
2. There is no architect downstream to consume this output. The
   "arrow artifact" the analyst would normally emit — the coverage
   claim mapping features to domain model — has no analogue here.
   This document is the closest substitute: a list of operator
   decisions, with residue (the "still open" section) made explicit.
