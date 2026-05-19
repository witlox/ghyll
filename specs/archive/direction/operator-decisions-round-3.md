# Operator decisions — round 3 (phase 4)

Closes the third design pass identified in
`phase-3-architect-findings.md`. With these 10 decisions, the
schema's contracts are typed and an enforcement-spine implementation
can proceed.

Three sub-rounds: A (foundations), B (phase-3 proposal confirmations
+ mid-phase interleaving), C (remaining structural).

Plus one framing decision (D14) that shapes how `gates.md` is read.

---

## Decisions

### D11 — Artifact IDs: hybrid (path-default, manual where stability matters)

**Source:** architect findings #2, #3.

Every artifact entry has an addressable identity. The default is
**path-based** (`invariants.md#section-3-cardinality-1`). Clauses
that other arrows declare dependence on, or that need to survive
content rewordings (e.g., re-orderings, edits, file splits), get a
**manually-assigned `id:` field** (e.g., `INV-001`, `FEAT-USER-001`).

The operator decides at clause-authoring time whether a clause needs
a manual ID. Default is path-based; explicit `id:` is the marked
case.

**Mechanism:**

- `unique-definition` catalogue concept enforces ID uniqueness
  where manual IDs are present.
- Dependency declarations (D16) may target any granularity:
  `file` / `section` / `clause-id`.

### D12 — Arrow identity is structural; passes are iterations

**Source:** architect finding #10.

In the state-space-iteration frame (D14): arrows are *operators*,
passes are *iterations* of the operator on the project state.

Arrow identity = `(role-pair, stratum, bounded-context, grid-version)`.
Immutable. Re-traversals after `invalidated` are *new passes on the
same arrow*; the arrow's identity does not change unless the grid
version changes.

Pass identity = unique pass-ID (UUID or timestamp-based) with
attributes: `pass-id`, `arrow-id`, `started-at`, `completed-at`,
`pass-status`.

**Pass status set:** `running`, `completed`, `aborted`.
- `running` — pass is in progress.
- `completed` — pass concluded; arrow status is whatever the
  clause/finding state derives.
- `aborted` — pass was terminated mid-iteration by an external event
  (per D17 — mid-phase invalidation). Findings discovered before
  abort are retained; arrow status becomes `invalidated`.

The arrow's *current* status is the latest pass's derived status.
Pass history lives in the checkpoint log.

### D13 — Per-concept schema files

**Source:** architect finding #1.

Each of the 16 catalogue concepts has a typed schema declaring its
arguments, types, and evaluator contract. Schemas are introspectable
and validatable; the catalogue is no longer prose.

**Storage:** `gates/concepts/<concept-name>.yaml` (one file per
concept), shipped with the harness.

**Shape (illustrative):**

```yaml
# gates/concepts/mutation-score.yaml
concept: mutation-score
description: Mutation testing reports a score above a declared threshold
arguments:
  scope:
    type: path-glob
    required: true
    description: Files or directories to mutate
  threshold:
    type: number
    required: true
    range: [0.0, 1.0]
    description: Minimum acceptable mutation score
evaluator:
  contract: machine
  produces: { pass: boolean, score: number, killed: int, survived: int }
default-cost: 3
```

Bindings reference the schema name; concept invocations from role
files reference the schema's argument names.

### D14 — `gates.md` framing: state-space-iteration

**Source:** operator framing question.

`gates.md` is rewritten with the explicit conceptual lens: it
describes a state-transition system over an extensible grid. Cells
are points; arrows are transition operators; passes are iterations;
invalidation resets cells to earlier states; grid amendment extends
the space. The fixed point is "every cell in grid vN has arrow status
`complete`."

This is not a re-design; it names what the schema already is. But
naming it sharpens several decisions: pass identity (D12), concept
schemas (D13), and the discipline that operators must be well-defined
on the state.

The "vector space" metaphor is loose (no linearity, scaling, or
addition over cells). The accurate name is **Kripke structure over
an extensible state space** or simply **state-transition system with
non-monotonic updates and state-space growth**.

### D15 — Severity enum: `info / low / medium / high / critical`

**Source:** architect finding #4 + phase-3 proposal.

Findings carry a required `severity` field on the enum
`{info, low, medium, high, critical}`. The `no-open-finding` clause
defaults to `threshold = medium` — findings at or above `medium`
that remain `open` block the arrow. Per-arrow override at
initialization is allowed (raise only; cannot weaken).

**Severity assignment is `depth-sensitive`** — assigning the correct
severity to a finding is itself a judgement. The adversarial phase
must therefore route per `gates.md` §8; below required depth, severity
assignments are `unevaluated` and the findings themselves are too.

The hint emitted with each finding includes severity + basis (the
rule by which the severity was assigned).

### D16 — Dependency declaration: three granularities

**Source:** architect finding #3 + phase-3 proposal + D11.

Each arrow's definition may declare per-dependency entries:

```yaml
dependencies:
  - artifact: <path>
    granularity: file | section | clause-id
    on-change: invalidate | reevaluate-named-clauses
```

- `file` — any change to the file invalidates this arrow.
- `section` — path-based addressing; change to the named section
  invalidates.
- `clause-id` — manual-ID precision (D11); change to the
  identified clause invalidates.

Arrows that declare no dependencies fall back to the conservative
rule (any upstream change invalidates), per `gates.md` §7.2.

### D17 — Mid-phase invalidation: abort and restart, preserve findings

**Source:** architect finding #5.

When invalidation lands while a cell is in the adversarial,
remediation, or verification phase:

1. The iteration is **aborted**. The pass record is marked with
   pass-status `aborted` (D12).
2. The arrow's status becomes `invalidated` per `gates.md` §7.2.
3. **Findings discovered before abort are retained** on the arrow's
   finding log. Useful signal is not lost. The producer may treat
   them as hints when the arrow is re-traversed against the amended
   upstream.
4. Next pass starts fresh — phases re-run from adversarial. No
   "resume mid-phase."

State-space-frame rationale: invalidation is an operator that resets
the cell to an earlier point. Partial iteration state at the time of
reset is incoherent and discarded; only the artifacts that survived
as findings are preserved.

### D18 — `<concept>.<lang>` bindings: project-declared at init

**Source:** architect finding #2 + operator clarification.

The harness ships the catalogue (D13) but **does NOT ship language
bindings**. Bindings like `lint-clean.go = staticcheck && go vet`,
`no-orphan-symbol.rust = { extractor: …, index: …, mapper: … }` are
declared by each project at initialization (`gates.md` §2.1).

If a binding is absent for a language the project uses, the harness
**runs initialization** to obtain it. This applies uniformly to
greenfield and brownfield — init is the place where bindings live,
regardless of whether code already exists.

This generalizes finding #2's `no-orphan-symbol`-specific resolution
to the whole catalogue: every `<concept>.<lang>` binding is a
project-declared init output.

### D19 — Attestation records: typed JSONL, structured path

**Source:** architect finding #8 + phase-3 proposal + D12.

Each operator attestation appends one JSONL record at:

```
attestations/<grid-vN>/<context>/<stratum>/<role-pair>/<pass-id>.jsonl
```

Path encodes the arrow identity from D12. Each record's shape
depends on the action unit (`gates.md` §10.1):

```json
{ "unit": "confirm",
  "clause": "<id>",
  "verdict": "pass|fail|insufficient-basis",
  "ts": "<iso8601>",
  "op-id": "<operator-identity>" }

{ "unit": "record-locations-inspected",
  "clause": "<id>",
  "verdict": "...",
  "ts": "...",
  "op-id": "...",
  "inspected": ["<file:line-range>", ...] }

{ "unit": "write-residue-note",
  "clause": "<id>",
  "verdict": "...",
  "ts": "...",
  "op-id": "...",
  "inspected": [...],
  "residue-note": "<text>" }
```

The verifier checks for **presence of the required fields** for the
declared unit. Distinguishing genuine inspection from fast-clicked
confirms is procedural-only and the schema says so explicitly.

### D20 — Initialization auto-proposes, operator confirms

**Source:** architect finding #7 + operator clarification.

Initialization is **mandatory before any other arrow runs**
(`gates.md` §2 already states this). Within init, the harness
**auto-proposes** default clauses for each declared bounded-context,
drawing from the role files (`roles/*.md`):

- For each `(role-pair, context)` arrow, the harness proposes the
  role's full exit-gate clause set as a default.
- The operator confirms, modifies, or extends each proposal.
- Operator may raise costs and add per-context clauses; may not
  weaken or drop the role's declared clauses without recording an
  explicit residue entry.

The grid is recorded only after every proposed clause has received
an operator verdict. This makes init the place where the per-project
clause set is materialized from the harness's templates.

---

## Mapping back to architect findings

| Finding | Status after round 3 |
|---|---|
| 1 (catalogue argument signatures) | **resolved** (D13) |
| 2 (`no-orphan-symbol` binding mechanism) | **resolved** (D18) |
| 3 (dependency declaration syntax) | **resolved** (D11, D16) |
| 4 (severity enum + threshold storage) | **resolved** (D15) |
| 5 (mid-phase invalidation race) | **resolved** (D17) |
| 6 (`no-open-finding` auto-insertion) | resolved in prior commit (gates.md §11.3) |
| 7 (v0 grid contents) | **resolved** (D20 — auto-propose from role files) |
| 8 (operator-action unit semantics) | **resolved** (D19) |
| 9 (concurrent on-the-spot traversal locking) | open — implementation detail; see below |
| 10 (pass/arrow identity) | **resolved** (D12) |
| 11 (`single-active-role-instance` coordinator) | open — implementation detail |
| 12 (`mode-determinable-from-repo` argument type) | resolved via D13 (per-concept schema declares it) |
| 13 (G4 residue-target argument disagreement) | resolved in prior commit (analyst.md G4 updated) |
| 14 (residue R imputation) | resolved in prior commit (gates.md §3.3) |
| 15 (clause→arrow status lift) | resolved in prior commit (gates.md §7.2) |

**Resolved by round 3 + prior commits: 13 of 15.** Remaining two
(#9, #11) are concurrency/coordination implementation details, not
schema-level decisions. They go to the harness implementation, not
to another decision round.

---

## What remains for implementation, not for another decision round

- **Concurrency primitives** (architect findings #9, #11). When two
  passes overlap on adjacent arrows, when on-the-spot creation
  amends the grid mid-traversal — these need a coordinator
  (process-level lock, lease, or content-addressed consistency).
  This is implementation, not schema.
- **Per-concept default costs** (architect's phase-3 table). The
  unit set is fixed (D19); the per-concept default values are
  architect-tuning work to be done as the catalogue is implemented.
- **Bindings, language-specific** (D18). Each project's init declares
  these; the harness does not ship them. Test projects will exercise
  the init flow.

---

## Required changes to existing documents

A separate task, mostly additions to `gates.md`:

**`gates.md`:**

- Add intro framing (state-space-iteration lens, D14).
- §2.1 — add language bindings as init outputs (already present;
  refine to note no-defaults-shipped, D18).
- §2 — add "auto-propose, operator confirms" mechanism (D20).
- §5.1 — point at per-concept schema files (`gates/concepts/`)
  per D13.
- §7.1 — add `aborted` pass status; add pass identity tuple per D12.
- §7.2 — already updated for invalidation in prior commits;
  add the "abort and restart, preserve findings" rule from D17.
- §7.3 — severity enum and threshold per D15.
- §8 — note per-concept schema-declared depth requirements.
- §10 — attestation record format per D19.
- §11 — note adversarial-phase severity assignment is depth-sensitive
  per D15.

**`build-notes.md`:** mark phase 4 closed; note remaining
implementation-only items (concurrency, per-concept default costs,
project-specific bindings).

These are applied in the same commit as this decisions doc.
