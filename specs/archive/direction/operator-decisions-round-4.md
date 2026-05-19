# Operator decisions — round 4

Closes the second validation pass (`validation-pass-2.md`). Three
blocker decisions plus six smaller items folded in. With these, the
schema's multi-cell dynamics, terminal-state routing, and small
remaining holes are typed.

---

## Decisions

### D21 — Synthetic role-ids `init` and `adversary` for special arrows

**Source:** findings 3, 4, 5, 9.

The harness carries **synthetic identities** for attestation paths
that are distinct from role files (`roles/*.md`). These are
identities, not contracts:

- `init` — the producer for the initialization arrow's `attested`
  clauses (§2.3). Emits hints per `gates.md` §9 the same way any
  producer would. The init role-id is bound to a fresh harness
  instance at project start; the operator attests against init's
  hints during the init flow.
- `adversary` — the producer identity for the per-arrow adversarial
  phase (§11). Bound to a *fresh model instance with clean context*
  per `gates.md` §11, separate from the upstream producer. Across
  remediation rounds, each re-attack is a new `adversary` spawn with
  the same clean-context rule.

This does **not** contradict §1's "no standalone adversary role" — §1
forbids `adversary.md` as a *role file* (a behavioral contract with
its own exit gate). `adversary` as a *synthetic role-id* is the
identity the harness uses for attestation paths and finding
provenance.

Attestation paths from §10.2 expand: `init→analyst`,
`analyst→adversary→architect`, `analyst→architect`,
`implementer→adversary→integrator`, etc. The `adversary` step
appears in arrows that ran an adversarial phase.

### D22 — Global amendment serialization with project-wide write-lock

**Source:** findings 1, 2, 6, 12, 14.

Grid amendments are serialized through a **project-wide write-lock**.
Concurrent amendments queue (FIFO). When an amendment commits as
v(N+1):

1. Every in-flight pass is checked against its declared dependencies
   (§2.1).
2. Affected passes are `aborted` mid-phase per §7.2; unaffected
   passes continue against vN until they record completion.
3. Preserved findings on aborted passes carry a `grid-version: vN`
   tag so they are distinguishable from findings on the new vN+1
   arrow.
4. After the v(N+1) write completes, the lock releases; the next
   queued amendment may proceed.

**Concurrency surface defined:**

- The lock lives in the harness's coordination layer (implementation
  detail of architect-pass finding #11's `single-active-role-instance`
  coordinator).
- Two integrator passes proposing amendments concurrently both
  enqueue; ordering is FIFO; the second amendment lands against the
  state produced by the first.

### D23 — Terminal routing: `insufficient-basis` and `unevaluated` findings

**Source:** findings 13, 15.

Both terminal states route to escalation rather than collapsing to
`fail` or `critical`.

**`insufficient-basis`** on an attested clause:

- The arrow stays `provisional` per §7.2.
- The producer may re-emit the hint at a deeper depth tier in the
  next remediation round (a re-emit is a remediation step).
- After **N=3 rounds** of `insufficient-basis` on the same clause,
  the clause escalates: the operator either attests `accepted-risk`
  with a `write-residue-note` recording why the basis remains
  insufficient, or the operator escalates the upstream artifact back
  for deeper rework (route the finding to the producer role with
  `requires-deeper-artifact` as the rationale).
- N=3 is a default; init may raise per `gates.md` §2.1.

**`unevaluated` finding severity** (severity assignment by the
adversarial phase returned `unevaluated`):

- The finding's status is `unevaluated` until severity can be
  assigned at sufficient depth.
- `no-open-finding` treats `unevaluated`-severity findings as
  blocking: an arrow with any `unevaluated`-severity finding does
  not close (this aligns with §7.2's `unevaluated` arrow status
  precedence above `provisional` and below `blocked`).
- Re-route at deeper tier to clear; if still unevaluated, the
  operator attests severity directly (recording the basis).

### D24 — Operator identity (`op-id`) is a declared string

**Source:** finding 10.

Each operator declares an `op-id` (string, conventionally email or
handle) at session start. Every attestation record (§10.2) includes
the `op-id` of the operator who returned the verdict. Multi-operator
projects have multiple `op-id`s active concurrently; each attestation
records who, and the verifier can compute per-operator coverage.

The schema does not enforce one-`op-id`-per-arrow; ad-hoc handoff
between operators within a pass is allowed. The
attestations-`<pass-id>.jsonl` file shows the full attestation chain.

### D25 — Bootstrap severity threshold for init = harness-default `medium`

**Source:** finding 11.

The initialization arrow needs a severity threshold *before* the
project's threshold is declared. The harness ships a hardcoded
`init-arrow-severity-threshold = medium`. This is the floor; init may
declare a project-wide threshold that applies to all other arrows but
cannot change its own arrow's threshold (that would be a
self-referential override).

### D26 — Depth-ladder gating via `every-requirement-meets-min-depth`

**Source:** finding 7.

The depth ladder (§11.1) classifies per-requirement depth but had no
clause concept that *gated on* the classification. Add a new
catalogue concept:

```yaml
# gates/concepts/every-requirement-meets-min-depth.yaml
concept: every-requirement-meets-min-depth
description: |
  Every requirement on the arrow was classified at or above its
  declared minimum depth by the adversarial phase's depth-classification
  sub-activity (§11.1). Findings are raised for any requirement below
  its minimum.
arguments:
  arrow:
    type: arrow-id
    required: true
evaluator:
  contract: machine
  produces: { pass: boolean, below-min: [requirement-id, ...] }
default-cost: 0
```

**Auto-inserted** by the harness on every arrow that runs an
adversarial phase (parallel to `no-open-finding`, §11). This makes
the depth-ladder a first-class gating signal, not just a finding
input.

### D27 — Residue R imputation: full role exit-gate cost per cell

**Source:** finding 17.

Each undeclared cell contributes `imputed-cost = total operator-action
cost of the role's full exit gate as auto-proposed for that cell`.
The harness can compute this exactly: it knows what the role file
declares; init's auto-propose flow (D20) tells the harness what cost
that cell would have carried if declared.

So `R = Σ (imputed-cost over all undeclared cells)`. Replaces the
earlier conservative-but-vague "max per-clause cost" rule (§3.3); now
R is honest and computable.

### D28 — `single-active-role-instance` scope: `(role, context)`

**Source:** finding 12.

The catalogue concept `single-active-role-instance` is scoped to
`(role, bounded-context)`, not `(role, stratum, context)`. A role's
work spans all strata for a single context (analyst writes
`domain-model.md` whole, not per-stratum). Concurrent traversals of
different strata for the same `(role, context)` would race on shared
artifacts; the schema forbids it.

Concurrent traversals of *different contexts* are allowed and not
blocked by this concept.

### D29 — `aborted` pass-status applies to any mid-phase termination

**Source:** finding 14.

`aborted` (§7.1a) applies to any pass that did not reach `completed`
status — invalidation, operator interrupt, crash, manual stop. Each
aborted pass records a `reason` field on its pass record:

- `reason: invalidated` — upstream amendment per D22.
- `reason: operator-interrupt` — operator stopped the pass.
- `reason: crash` — harness or model failure.
- `reason: manual-stop` — operator explicitly closed the session
  mid-pass.

Arrow status after a non-invalidation abort: the arrow's status is
whatever the latest *completed* pass left it at (could be `complete`,
`blocked`, `provisional`, or `unevaluated`). The abort itself does
not change arrow status unless it was an invalidation abort.

---

## Doc fixes (no decision needed)

- **#8**: `roles/analyst.md` strata reference §2.1 → §3.1.
- **#16**: `roles/integrator.md` should also reference `gates.md` §7.2
  for the invalidation mechanism (the §3.7 reference in
  `direction.md` is for rationale, not contract).

---

## Mapping back to validation-pass-2 findings

| Finding | Status after round 4 |
|---|---|
| 1 (multi-context coordination) | **resolved** (D22) |
| 2 (grid-version race) | **resolved** (D22) |
| 3 (init attestation self-reference) | **resolved** (D21) |
| 4 (init-arrow hint producer) | **resolved** (D21) |
| 5 (adversarial-phase role identity) | **resolved** (D21) |
| 6 (preserved findings temporal coupling) | **resolved** (D22 — `grid-version` tag) |
| 7 (depth-ladder gating) | **resolved** (D26) |
| 8 (doc-drift §2.1→§3.1) | **fixed** in same commit |
| 9 (re-attack producer identity) | **resolved** (D21 — fresh `adversary` per round) |
| 10 (`op-id` provenance) | **resolved** (D24) |
| 11 (init bootstrap severity threshold) | **resolved** (D25) |
| 12 (`single-active-role-instance` scope) | **resolved** (D28) |
| 13 (`insufficient-basis` routing) | **resolved** (D23) |
| 14 (`aborted` non-invalidation reasons) | **resolved** (D29) |
| 15 (`unevaluated` finding gating) | **resolved** (D23) |
| 16 (`integrator.md` cross-ref drift) | **fixed** in same commit |
| 17 (residue R per-cell conversion) | **resolved** (D27) |

**Resolved: 17 of 17.** The schema's multi-cell dynamics and
terminal states are now typed.

---

## Required changes to existing documents

Applied in the same commit:

**`gates.md`:**

- §0 — extend cell state to include `grid-version: vN` tag for
  preserved findings (D22).
- §1 — clarify that synthetic role-ids `init` and `adversary` are
  not role files but identities (D21).
- §2 — clarify init runs against the v0 grid; init's own severity
  threshold is harness-default (D25).
- §3.3 — replace the residue R imputation rule with D27.
- §5.1 — add `every-requirement-meets-min-depth` to the catalogue
  table (D26).
- §5.1 — clarify `single-active-role-instance` scope is
  `(role, context)` not `(role, stratum, context)` (D28).
- §7.1a — `aborted` carries a `reason` field; expand the unit set
  (D29).
- §7.2 — replace the mid-phase invalidation rule with the D22 fuller
  version (project-wide lock, FIFO, dependency check, grid-version
  tag).
- §7.3 — `unevaluated`-severity findings block; threshold default
  `medium` (D23).
- §10 — `insufficient-basis` after N rounds routes to operator
  escalation (D23).
- §10.2 — `op-id` is a declared string per session (D24).
- §11 — verification auto-inserts both `no-open-finding` *and*
  `every-requirement-meets-min-depth` (D26).
- §11 — re-attack across remediation rounds spawns a fresh
  `adversary` (D21).

**`roles/analyst.md`:** strata reference §2.1 → §3.1.

**`roles/integrator.md`:** add `gates.md` §7.2 reference alongside
the `direction.md` §3.7 reference.

**`build-notes.md`:** mark validation-pass-2 closed; remaining work
is purely implementation.
