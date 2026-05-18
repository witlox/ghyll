# Harness Gate Schema

This document defines how ghyll gates role transitions. It is the
harness-wide schema; individual role files declare *which* gates and
clauses they carry, but never redefine the semantics here.

Definitions without enforcement drift; this schema is the enforcement
layer.

Reconciled to operator decisions in
`operator-decisions-round-1.md`, `operator-decisions-round-2.md`, and
the corrections that followed `phase-3-architect-findings.md`.

---

## 1. Roles, transitions, arrows

ghyll runs **one role at a time**. The default workflow (the diamond)
is a declared sequence of four roles:

```
analyst → architect → implementer → integrator
```

There is **no standalone adversary role** and **no standalone auditor
role**. Adversarial scrutiny and depth classification are *phases of
every arrow* (§11), not roles. A role in a sequence can be skipped; a
phase of a transition cannot.

A **role transition** is the move from one role to the next. Every
transition IS an **arrow**. An arrow is a first-class artifact: it
asserts that the upstream role's output *composes to satisfy* the
downstream role's input requirement. The node artifacts (specs, code,
tests) are produced by roles; the arrow artifact is produced by the
*transition* and is what the harness gates.

Transitions are legal **only along declared arrows**. A switch to a role
with no declared arrow from the current role is refused — see §12.

The diamond is the default declared arrow set. Other workflows are other
declared subgraphs of the same role set.

---

## 2. Project initialization

**Project initialization is step one when ghyll is invoked on a new
project. It is not optional.**

Initialization refines the harness's v0 baseline into this project's
vN. The v0 grid ships with the harness (§3.4); initialization is what
turns v0 into a project-specific grid that the diamond can run
against. A project that has not been initialized cannot enter the
diamond.

### 2.1 Outputs of initialization

| Output | What it declares |
|---|---|
| Arrow grid | The declared arrow set for this project — which arrows exist across (stratum, context) cells (§3) |
| Per-arrow clause arguments | Concept arguments for machine clauses (e.g., `mutation-score` threshold, scope) |
| Depth ladder labels | Project's labels for the depth tiers used by the adversarial phase (§11.1); the harness ships generic defaults that the project may override |
| Language bindings | Per-language instrument bindings for catalogue concepts (e.g., `lint-clean.go = staticcheck && go vet`); see §5 |
| Severity thresholds | The threshold above which an open finding blocks an arrow (§7.3) |
| Per-arrow dependency declarations | Which spec artifacts each arrow depends on, for invalidation propagation (§7.2) |
| Explicit residue | Which `(stratum, context)` cells the operator has chosen NOT to declare, and why |

### 2.2 Authorship

Initialization is **operator-owned, agent-assisted**. The agent helps
draft; the operator owns the grid. ghyll's role is to *enforce that the
residue is declared and attested* — not to generate the grid
unsupervised. (Producing the grid is a large, novel-architecture
activity, which is the activity class agents are weakest at; leaving it
agent-driven would need its own gate and start an infinite regress.)

### 2.3 Initialization runs as an arrow

Initialization is not a special case in the schema. It runs as a
normal arrow against the v0 grid (§3.4) — the schema is its own
bootstrap; v0 is the floor. The initialization arrow's exit gate's
central `attested` clauses:

- The residue is explicitly listed and the operator attests it is
  acceptable to proceed with.
- Every declared arrow's depth-type assignments are attested.
- Per-arrow dependency declarations are recorded.
- Per-project depth-ladder labels (if overridden from defaults) are
  recorded.
- Per-project language bindings are recorded.
- Per-project severity thresholds are recorded.

### 2.4 Refusal

Initialization should detect a low-risk project profile — few
contexts, no cross-context seams, no novel architecture — and refuse
to proceed: "ghyll's friction will be pure cost here; use a fast
agent." Refusal is the most differentiated behavior of an opinionated
tool. A tool that cannot decline the wrong job just makes every job
slow.

---

## 3. The arrow grid

Completeness is not one global property. It is **stratified** and
**per-context**. An arrow's completeness predicate is therefore scoped:
`complete-at-arrow-A-for-stratum-S-for-context-C`. The full set of
arrows across all strata and contexts is the **arrow grid**, produced
by project initialization (§2).

### 3.1 Stratum vocabulary

A stratum is a **uniform layer concept** that every role interprets in
its own medium. Six layers:

| # | Layer | Analyst interpretation | Architect (illustrative) | Implementer (illustrative) |
|---|---|---|---|---|
| 1 | Structure | Domain model, terms, shapes | Interfaces, types, module boundaries | Type / data declarations |
| 2 | Invariants | Consistency / cardinality / ordering predicates | Type-system invariants | Assertions, runtime invariants |
| 3 | Behavior | Commands, events, queries | Behavior in functions/methods | Function bodies |
| 4 | Composition | Cross-context interactions | Integration points, API boundaries | Cross-module wiring |
| 5 | Failure | Failure modes, degradation | Error types, fault handling | Error paths in code |
| 6 | Assumptions/risk | Falsifiable assumptions log | Architectural bets | Accepted runtime risks |

Each role declares how its work projects onto these six layers. Strata
are uniform at the harness level so that cross-role comparisons are
meaningful (analyst's L4 cross-context spec maps to architect's L4
integration boundary).

### 3.2 "Context"

A **context** is a Domain-Driven Design bounded context. The arrow grid
is therefore indexed as `(stratum, bounded-context)`.

### 3.3 Declared, versioned, open

The grid is:

- **Declared**: produced by project initialization (§2).
- **Versioned**: every change to the grid increments its version.
- **Open**: the grid carries an explicit **residue** — `(stratum,
  context)` cells known to need arrows but not yet declared. Residue
  is a first-class, visible quantity. The harness never reports a
  project as "complete" — it reports **complete against grid vN,
  residue R, unevaluated C** (§7.4).

Residue `R` is computed as the **sum of operator-action cost across
undeclared cells** in the grid. Honest about *how much attention has
been deferred*, not just how many holes there are.

Undeclared cells have no clauses and therefore no clause costs.
Cost-per-undeclared-cell is **imputed** at the conservative upper
bound: the maximum per-clause cost in the harness catalogue (the value
of `write-residue-note`, §10.1). This over-estimates rather than
under-estimates — residue should never look smaller than it might be.

A frozen grid would let the harness certify completeness against a
definition known to be partial. That is false completeness promoted
from a model behavior to a harness guarantee. The residue field
prevents it.

### 3.4 v0 grid (the bootstrap)

The harness ships a **v0 grid** that exists before any project's
initialization runs. The v0 grid contains:

- The diamond workflow (§1) as the declared arrow set.
- The universal-base machine clauses (§5.2) on every arrow.
- The six-layer stratum vocabulary (§3.1).
- The initialization arrow's exit gate, including its `attested`
  clauses (§2.3) — these ride on the initialization arrow specifically
  and ship with the harness so the first project has a non-empty gate
  to satisfy.
- The default depth ladder (§11.1).

The initialization arrow runs as a **normal arrow against the v0
grid** — there is no special bootstrap. v0 is the floor.

---

## 4. Gate structure

Each role carries a **contract**: a single exit gate.

An exit gate is a set of **clauses** that must pass before the role's
output becomes eligible to satisfy the next role's input.

There is **no separate "entry precondition" concept**. Conditions that
must hold before a role is entered are expressed as exit clauses of the
*upstream* arrow as viewed from the downstream side. Same machinery,
no new state.

Every clause has two declared types: an **evaluation type** (§5) and a
**depth type** (§6).

---

## 5. Evaluation type

How a clause is decided.

| Type | Decided by | Trust property |
|------|-----------|----------------|
| `machine` | The harness, build, or test tooling — a deterministic check returning pass/fail | Trustworthy in *evaluation*; only as good as the artifact it runs on |
| `attested` | An explicit operator verdict | Trustworthy only to the limit of operator attention at attestation time |

**`machine` is not "objective" and `attested` is not "subjective".** A
`machine` clause runs on an artifact a model produced; a shallow
artifact can pass a shallow machine check. The split is about *who can
decide the clause*, not about reliability.

### 5.1 Machine clauses: concept-named, language-bound

The machine clause catalogue is **closed at the concept layer** and
**language-agnostic**. Concepts name *what property* is asserted;
per-language **instrument bindings** decide *what tool runs the check*.

For example, the concept `lint-clean` has bindings like
`lint-clean.rust = clippy`, `lint-clean.go = staticcheck && go vet`,
`lint-clean.typescript = eslint`. Bindings live in per-project config
(declared at initialization, §2.1), not in the catalogue.

The catalogue concepts ghyll ships:

| Concept | What it asserts |
|---|---|
| `compiles` | The artifact (code / Gherkin / schema) compiles or parses without error |
| `lint-clean` | The artifact passes the language's lint toolchain with no warnings above threshold |
| `no-todo-marker` | No `TODO` / `TBD` / `???` / `FIXME` strings in the artifact |
| `every-step-bound` | Every step / case / branch is structurally bound (no dangling clauses) |
| `no-orphan-symbol` | Every exported symbol traces to a declared spec clause; orphans are listed as residue |
| `mutation-score` | Mutation testing reports a score above a declared threshold |
| `kill-server-fails-integration` | The integration test suite fails when a critical dependency is unavailable |
| `trace-link-present` | A declared link from one artifact to another (e.g., from a spec ID to its implementation) exists |
| `acyclic-dependency-graph` | The dependency graph of the named scope is acyclic |
| `unique-definition` | A named field's values are unique across the artifact (e.g., no duplicate term definitions) |
| `predicate-form` | Each entry in a named collection is written as an assertable predicate, not prose |
| `arrow-artifact-present` | The arrow's declared output artifact exists at its declared location |
| `no-open-finding` | All findings on the arrow are `resolved` or `accepted-risk` (none `open`) |
| `cardinality-check` | A named query returns exactly the declared cardinality (e.g., "1 bounded context") |
| `mode-determinable-from-repo` | A named mode discriminator (e.g., greenfield / brownfield) is determinable from repo state |
| `single-active-role-instance` | No other active role traversal exists for the same (role, stratum, context) tuple |

This catalogue is the harness's primitive vocabulary. New concepts
enter via deliberate harness changes, not per-role declaration.

Formal argument schemas per concept are phase-4 work (see
`phase-3-architect-findings.md` #1). The current role files use
concept-with-argument-name notation that is illustrative pending those
schemas.

### 5.2 Universal base set

A subset of catalogue concepts applies to every arrow regardless of
requirement:

- `compiles`
- `lint-clean`
- `no-todo-marker`
- `every-step-bound`

These four are inherited automatically by every arrow in the v0 grid
(§3.4). Roles may not opt out.

### 5.3 Per-arrow instances

All **other machine clause instances are derived per-arrow** from the
requirement during initialization (§2). Which mutations, over which
code, asserting which behaviour — that is born with the requirement
and cannot be predefined. Predefining the per-arrow instance set
recreates a fixed-checklist failure: a green checklist while the
system is broken.

Per-arrow instances reference catalogue concepts by name and supply
the arguments the concept needs (target scope, threshold value,
expected cardinality, etc.).

### 5.4 Rules for `attested` clauses

- The producing role MUST NOT self-certify an `attested` clause.
- The producing role MUST emit a **hint** for each `attested` clause
  (§9).
- The operator returns one of three verdicts per `attested` clause:
  `pass`, `fail`, `insufficient-basis` (§10).

---

## 6. Depth type

Some clauses can be honestly produced or evaluated by any model. Some
require a model of sufficient depth. The **depth type** of a clause
declares the minimum model depth required.

| Depth type | Meaning |
|------------|---------|
| `depth-robust` | Any model tier can produce/evaluate this honestly; a weak model cannot fake it (`compiles`, `every-step-bound`) |
| `depth-sensitive` | Requires a model at or above a declared depth; a weaker model produces confident-but-wrong output (`hint correctness`, `failure-mode adequacy`, RFC-conformance judgement) |

The depth type of a clause is **itself a judgement**, assigned at clause
authoring time (initialization, §2), and is **an `attested` item on
the initialization arrow's exit gate**. It is not machine-derivable
and must not default to `depth-robust` for convenience.

If a `depth-sensitive` clause is produced or evaluated by a model below
its required depth, the clause status is `unevaluated` (§7.1).

Depth type is **about the clause** (what model is required to produce
it honestly). The **depth ladder** in §11.1 is a separate concept:
it classifies the *artifact's depth*, not the clause's depth
requirement.

---

## 7. Status state machines

Three distinct state machines: **clause**, **arrow**, **finding**.
Project-level status is a composite derived from arrows, not its own
state machine.

### 7.1 Clause lifecycle

```
                          ┌─ machine evaluator → pass | fail
(initial: pending) ───────┤
                          └─ attested → awaiting-attestation
                                           └─ operator → pass | fail | insufficient-basis
```

At any evaluation, if the model depth used was below the clause's
declared depth requirement, the clause status is `unevaluated` instead
of `pass` / `fail` / etc. The `unevaluated` status carries an optional
`reason` field:

- `depth-below-required` — the model that ran the evaluation was below
  the clause's depth-sensitivity requirement.
- `no-rule-selectable-locations` — the role producing an attested
  clause could not select hint locations by a stated rule. In this
  case the role *also* raises a finding of type `unable-to-hint`
  against itself (see §7.3).

`unevaluated` is a first-class status. An arrow with any `unevaluated`
clause cannot close — `provisional` is not a substitute. A
`depth-sensitive` clause whose required depth exceeds *every available
model tier* routes to operator attestation; it is never laundered
through the largest available model and recorded as `provisional`.

**Re-traversal at deeper tier:**

- `unevaluated` → re-evaluate fresh against the new tier; status is
  replaced by the new verdict.
- `fail` → re-evaluate after the producer's fix; new verdict
  overwrites.
- `awaiting-attestation` → no change until the operator returns a
  verdict.

Clause status is **per-pass**. Pass history lives in the checkpoint
log, not in the status field.

**Clause status set:**
`pending`, `pass`, `fail`, `awaiting-attestation`,
`insufficient-basis`, `unevaluated`.

### 7.2 Arrow lifecycle

An arrow's status is the **most severe** status of its clauses, plus
**invalidation events**. `invalidated` is not a clause status (it is
not in §7.1's set); it is set on the arrow by the grid-amendment
process and overrides any clause-derived status until the arrow is
re-traversed. Precedence, most severe first:

| Arrow status | When |
|---|---|
| `invalidated` | An upstream spec the arrow declared dependence on (or, for arrows that declared no dependencies, any upstream spec) changed since last traversal. Supersedes everything below. |
| `blocked` | Any clause is `fail`. |
| `unevaluated` | No clause is `fail`, but at least one clause is `unevaluated`. |
| `provisional` | All evaluated clauses are `pass`, but at least one attested clause is `awaiting-attestation` or `insufficient-basis`. |
| `complete` | All clauses are `pass`. |

Precedence consequences:

- An arrow with both `unevaluated` clauses and `awaiting-attestation`
  clauses is `unevaluated`, not `provisional` — "we couldn't decide
  it" trumps "we decided but want confirmation."
- An arrow with both `fail` and `unevaluated` clauses is `blocked` —
  fails are immediately actionable; unevaluated needs depth re-routing.
- Only `complete` arrows satisfy the next role's input. `provisional`
  does not.

**Invalidation propagation:** when an analyst grid amendment changes a
spec, the harness marks:

- Arrows that **declared dependence** on a changed spec artifact:
  `invalidated`.
- Arrows that **declared no dependencies**: `invalidated`
  (conservative fallback — encourages explicit declaration without
  making it mandatory).
- Arrows that **declared dependencies and none of them changed**:
  status unchanged.

The count of conservatively-invalidated arrows on each grid amendment
is a quality signal; if it stays high, dependency declarations are
missing.

**Arrow status set:**
`complete`, `provisional`, `unevaluated`, `blocked`, `invalidated`.

### 7.3 Finding lifecycle

Findings are raised by the **adversarial phase** of an arrow (§11) or
by a role against itself (e.g., `unable-to-hint`). They live on the
arrow artifact, not on a clause.

```
(open) ──┬─ producer fixes → adversarial phase re-attacks ──┬─ resolved
         │                                          └─ open (still defective)
         │
         └─ producer proposes accepted-risk ──┬─ operator attests → accepted-risk
                                              └─ operator rejects → open
```

The producer may NOT accept its own risk — only the operator may
attest `accepted-risk`.

**Verification phase machine check:** any finding above a declared
severity threshold still `open` → the harness synthesizes a `fail`
on the arrow's `no-open-finding` clause, which propagates to arrow
status `blocked` by §7.2 precedence. (The severity threshold is
declared at initialization, §2.1; the per-finding severity is set by
the adversarial phase.)

**Finding status set:** `open`, `resolved`, `accepted-risk`.

### 7.4 Project-level status (derived)

`(complete-against-grid-vN, residue-R, unevaluated-C)`:

- **vN** — the current arrow grid version.
- **R** — sum of operator-action cost across undeclared `(stratum,
  context)` cells (§3.3).
- **C** — count of arrows whose status is `unevaluated`.

C > 0 is the "green but will break on deployment" failure mode and
must never be hidden inside an aggregate pass. R > 0 is the
deferred-attention surface.

---

## 8. Routing

ghyll routes each pass to a model tier. Routing is driven by **the gate
definition**, never by a model's self-assessment of task complexity
(self-assessed complexity is the magnitude-blind component rating its
own magnitude — it under-escalates exactly when it matters).

**Routing rule:** a pass traversing an arrow runs at the lowest model
tier whose depth meets the **maximum depth requirement across all
clauses on that arrow**.

- Machine-only / `depth-robust`-only arrow → fast tier. Not a risk:
  `depth-robust` clauses cannot be faked by a weak model.
- Arrow with any `depth-sensitive` clause → a tier meeting that
  clause's declared depth, or those clauses return `unevaluated`
  (§7.1).

ghyll defines depth *types*; a separate, swappable config (declared
at initialization) maps types to concrete model tiers. The schema does
not name models, so it does not rot when the model lineup changes.

---

## 9. Hints

For each `attested` clause, the producing role emits a **hint**: a
structured pointer telling the operator where to look. A hint has this
shape and no other:

```
clause:    <clause id>
locations: [ <file:line-range>, ... ]
basis:     "<the rule by which these locations were selected>"
residue:   "<what the selection rule did NOT classify, and how much>"
```

Rules, all mandatory:

- A hint states **where the load-bearing content is and how that was
  determined**. The `locations` must be selected by a stated rule,
  not by the model's impression of where risk lies.
- A hint MUST NOT contain a verdict or recommendation. The strings
  `approve`, `looks good`, `sufficient`, `LGTM` (and equivalents) are
  forbidden in a hint. The producing role points; the operator judges.
- A hint MUST disclose its **residue** — what the selection rule did
  not classify. A hint that highlights without disclosing what it
  skipped silently steers the operator away from unclassified content,
  which can hide the very gap being looked for.
- If the role cannot select locations by a stated rule, the clause is
  recorded `unevaluated` with reason `no-rule-selectable-locations`,
  and the role raises a finding of type `unable-to-hint` against
  itself (§7.1, §7.3).

The hint is itself a model output and therefore depth-sensitive: a
shallow model produces a shallow hint. Hint-correctness is a
`depth-sensitive` clause and routes accordingly (§6, §8).

---

## 10. Operator attestation and weight

For each `attested` clause the operator returns:

- **`pass`** — operator inspected the hinted locations and attests.
- **`fail`** — operator rejects; the role does not exit; findings route
  back into the role.
- **`insufficient-basis`** — operator cannot attest (artifact
  ambiguous, context too large to judge). This is **not** a failure.
  It routes per the escalation paths. It exists so an uncertain
  operator is never forced to choose between a false `pass` and a
  punitive `fail`.

An arrow with any `attested` clause in `awaiting-attestation` or
`insufficient-basis` propagates status `provisional` per §7.2. A
`provisional` arrow does not satisfy the next role's input.

### 10.1 Attestation weight

Arrow weight is the **sum of per-clause operator-action costs** of its
clauses, measured in **operator-action units**.

- Each catalogue concept (§5) ships with a **harness default cost** in
  operator-action units. Default values are part of the catalogue
  alongside the concept.
- An arrow at definition time may **raise** (never lower) a clause's
  cost for its specific traversal.

The unit set:

| Unit | Action | Cost |
|---|---|---|
| `confirm` | Single confirm verdict | 1 |
| `record-locations-inspected` | Confirm + record which hinted locations were actually inspected | 3 |
| `write-residue-note` | Confirm + record + write a residue note for what was not inspected | 5 |

(Exact per-concept default values are phase-3 architect work; the
unit set is fixed.)

Friction is allocated deliberately: highest where rubber-stamping is
most dangerous.

---

## 11. Arrow phases

A role beginning an outbound transition does not simply hand off.
Every arrow carrying any `depth-sensitive` clause has three phases:

1. **Adversarial.** A *separate instance* from the producer (clean
   context) attacks the upstream artifact. Three sub-activities:
   - **Clause-falsification.** For each declared `depth-sensitive`
     clause, try to make it fail.
   - **Open sweep.** Find defects no clause names.
   - **Depth classification.** Walk each requirement in the upstream
     artifact and classify it on the depth ladder (§11.1). For each
     requirement below its declared minimum, raise a finding.

   Each sub-activity raises findings with a severity. The adversarial
   phase is depth-sensitive by nature; below required depth →
   findings status `unevaluated`.

2. **Remediation.** A bounded loop. The producer addresses each
   finding: either *fixes* it (re-attack confirms `resolved`) or
   *proposes accepted-risk* (the operator attests — the producer may
   not accept its own risk). Non-convergence in N rounds → escalate,
   do not spin.

3. **Verification.** Gate clauses evaluated. If the adversarial phase
   ran on this arrow, the harness **auto-inserts** a `no-open-finding`
   machine clause into the verification step (regardless of whether
   the arrow's definition declared it). This guarantees that an arrow
   that hosted findings cannot close while findings above the severity
   threshold are still `open`.

A purely `machine` / `depth-robust` arrow runs **verification only** —
adversarial scrutiny of a deterministic check buys nothing, and depth
classification of a fully-machine-checkable artifact is moot.

### 11.1 Depth ladder

The depth ladder classifies *what depth the upstream artifact
actually reached* — distinct from a clause's depth-type requirement
(§6, which is about the model required to evaluate the clause).

The harness ships a **default 4-tier ladder**:

| Tier | Default label | Meaning |
|---|---|---|
| 0 | NONE | The artifact has no implementation of this requirement |
| 1 | SHALLOW | An implementation exists but does not exercise the specified behavior (trivial assertions, structure-only, untested code paths) |
| 2 | MOCKED | The implementation exercises the behavior but only against mocks / stubs / fakes — no real dependency is involved |
| 3 | REALISTIC | The implementation exercises the behavior against real (or production-equivalent) dependencies |

Each arrow declares a **minimum depth per requirement** at
initialization. The adversarial phase's depth-classification
sub-activity classifies each requirement and raises a finding for any
that classifies below its declared minimum.

**Project override.** A project may rename the labels at
initialization (§2.1). The *number of tiers* is fixed at 4; only the
labels change. A project might use a domain-specific vocabulary (e.g.,
a security project: NONE / DEFENSIVE / OFFENSIVE / RED-TEAMED). The
labels are operator-attested at initialization.

The ladder is a property of the schema (it applies to every arrow's
adversarial phase), not of any role. No role file declares a depth
ladder; the harness provides the mechanism uniformly.

---

## 12. Undeclared transitions — on-the-spot arrow creation

When ghyll hits a transition with no declared arrow, it does **not**
fail and does **not** silently proceed. It **suspends** and triggers
on-the-spot arrow definition:

1. The arrow is defined now — clauses, evaluation types, depth types,
   dependency declarations, per-requirement minimum depth.
2. This definition is **itself gated by operator attestation** — the
   producing role may not self-certify the definition it wants to use
   to continue.
3. Because depth-type assignment is `depth-sensitive`, on-the-spot
   creation **escalates the model tier for the duration of the
   definition act**, then routes the actual traversal per the
   resulting clauses.
4. The new arrow is **written back into the grid as version N+1** — it
   is not a local exception. An on-the-spot arrow that is not
   persisted is recreated, possibly inconsistently, on every future
   pass — the stationary-failure mode.
5. The interruption is **logged and visible**. An initialization that
   produces many on-the-spot interruptions was shallow; that count is
   a free quality signal and must not be smoothed away.

---

## 13. What this schema does NOT solve

Stated explicitly so the harness is not mistaken for a guarantee it
does not provide:

- **Artifact depth.** The schema makes "was the check run, and did it
  pass" structural. It cannot make a thin spec deep or a shallow test
  meaningful. `depth-sensitive` + `attested` clauses +
  `mutation-score` machine clauses + the depth ladder (§11.1) are the
  *defenses*; they are not proofs.
- **Operator fatigue.** `attested` clauses depend on operator
  attention. Weight (§10.1) allocates attention; it does not create
  it.
- **Initialization quality.** The grid is only as good as
  initialization (§2). The residue and on-the-spot interruption count
  are the signals that surface a weak initialization — they do not
  prevent one.

The schema converts "did the work happen and was it checked" from
hope into fact. It does not convert "is the work good". That remains a
human responsibility, made visible rather than eliminated.
