# Harness Gate Schema

This document defines how ghyll gates role transitions. It is the
harness-wide schema; individual role files declare *which* gates and
clauses they carry, but never redefine the semantics here.

It exists because the Kiseki experience showed a specific failure: the
role definitions were good — the auditor role already defined a
STUB/SHALLOW/MOCK/NETWORK depth ladder and a falsifiability gate — but
none of it was *enforced*. Definitions without enforcement drift. This
schema is the enforcement layer.

Reconciled to operator decisions in
`operator-decisions-round-1.md` and `operator-decisions-round-2.md`.

---

## 1. Roles, transitions, arrows

ghyll runs **one role at a time**. The default workflow (the diamond) is
a declared sequence of five roles:

```
analyst → architect → implementer → auditor → integrator
```

There is **no standalone adversary role**. Adversarial scrutiny is a
*phase of every arrow* (§11), not a role in the sequence. A role in a
sequence can be skipped; a phase of a transition cannot.

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

## 2. The arrow grid

Completeness is not one global property. It is **stratified** and
**per-context**. An arrow's completeness predicate is therefore scoped:
`complete-at-arrow-A-for-stratum-S-for-context-C`. The full set of
arrows across all strata and contexts is the **arrow grid**.

### 2.1 Stratum vocabulary

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

### 2.2 "Context"

A **context** is a Domain-Driven Design bounded context. The arrow grid
is therefore indexed as `(stratum, bounded-context)`. An analyst spec
names exactly one bounded context per the analyst role file's exit gate.

### 2.3 Declared, versioned, open

The grid is:

- **Declared**: produced by the definition phase (§8).
- **Versioned**: every change to the grid increments its version.
- **Open**: the grid carries an explicit **residue** — `(stratum,
  context)` cells known to need arrows but not yet declared. Residue
  is a first-class, visible quantity. The harness never reports a
  project as "complete" — it reports **complete against grid vN,
  residue R, unevaluated C** (§6.4).

Residue `R` is computed as the **sum of operator-action cost across
undeclared cells** in the grid. Honest about *how much attention has
been deferred*, not just how many holes there are.

A frozen grid would let the harness certify completeness against a
definition known to be partial. That is false completeness promoted from
a model behavior to a harness guarantee. The residue field prevents it.

### 2.4 v0 grid (the bootstrap)

The harness ships a **v0 grid** that exists before any project's first
definition phase runs. The v0 grid contains:

- The diamond workflow (§1) as the declared arrow set.
- The universal-base machine clauses (§4.2) on every arrow.
- The six-layer stratum vocabulary (§2.1).

A project's first definition phase runs as a **normal arrow against the
v0 grid** — there is no special bootstrap arrow. The schema is its own
bootstrap; v0 is the floor.

---

## 3. Gate structure

Each role carries a **contract**: a single exit gate.

An exit gate is a set of **clauses** that must pass before the role's
output becomes eligible to satisfy the next role's input.

There is **no separate "entry precondition" concept**. Conditions that
must hold before a role is entered are expressed as exit clauses of the
*upstream* arrow as viewed from the downstream side. Same machinery, no
new state. (This is why the analyst role's E1–E3 checks live on the
v0 (definition-phase → analyst) arrow, not on a separate inbound check.)

Every clause has two declared types: an **evaluation type** (§4) and a
**depth type** (§5).

---

## 4. Evaluation type

How a clause is decided.

| Type | Decided by | Trust property |
|------|-----------|----------------|
| `machine` | The harness, build, or test tooling — a deterministic check returning pass/fail | Trustworthy in *evaluation*; only as good as the artifact it runs on |
| `attested` | An explicit operator verdict | Trustworthy only to the limit of operator attention at attestation time |

**`machine` is not "objective" and `attested` is not "subjective".** A
`machine` clause runs on an artifact a model produced; a shallow artifact
can pass a shallow machine check (this is exactly how the Kiseki audit's
17 shallow-test rows passed CI). The split is about *who can decide the
clause*, not about reliability.

### 4.1 Machine clauses: concept-named, language-bound

The machine clause catalogue is **closed at the concept layer** and
**language-agnostic**. Concepts name *what property* is asserted;
per-language **instrument bindings** decide *what tool runs the check*.

For example, the concept `lint-clean` has bindings like
`lint-clean.rust = clippy`, `lint-clean.go = staticcheck && go vet`,
`lint-clean.typescript = eslint`. Bindings live in per-language config,
not in the catalogue.

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

### 4.2 Universal base set

A subset of catalogue concepts applies to every arrow regardless of
requirement:

- `compiles`
- `lint-clean`
- `no-todo-marker`
- `every-step-bound`

These four are inherited automatically by every arrow in the v0 grid
(§2.4). Roles may not opt out.

### 4.3 Per-arrow instances

All **other machine clause instances are derived per-arrow** from the
requirement during the definition phase. Which mutations, over which
code, asserting which behaviour — that is born with the requirement
and cannot be predefined. Predefining the per-arrow instance set
recreates the auditor's "THOROUGH" failure: a fixed checklist that is
green while the system is broken.

Per-arrow instances reference catalogue concepts by name and supply
the arguments the concept needs (target scope, threshold value,
expected cardinality, etc.).

### 4.4 Rules for `attested` clauses

- The producing role MUST NOT self-certify an `attested` clause.
- The producing role MUST emit a **hint** for each `attested` clause
  (§9).
- The operator returns one of three verdicts per `attested` clause:
  `pass`, `fail`, `insufficient-basis` (§10).

---

## 5. Depth type

Some clauses can be honestly produced or evaluated by any model. Some
require a model of sufficient depth. The **depth type** of a clause
declares the minimum model depth required.

| Depth type | Meaning |
|------------|---------|
| `depth-robust` | Any model tier can produce/evaluate this honestly; a weak model cannot fake it (`compiles`, `every-step-bound`) |
| `depth-sensitive` | Requires a model at or above a declared depth; a weaker model produces confident-but-wrong output (`hint correctness`, `failure-mode adequacy`, RFC-conformance judgement) |

The depth type of a clause is **itself a judgement**, assigned at clause
authoring time (definition phase), and is **an `attested` item on the
definition-phase gate**. It is not machine-derivable and must not default
to `depth-robust` for convenience.

If a `depth-sensitive` clause is produced or evaluated by a model below
its required depth, the clause status is `unevaluated` (§6.1).

---

## 6. Status state machines

Three distinct state machines: **clause**, **arrow**, **finding**.
Project-level status is a composite derived from arrows, not its own
state machine.

### 6.1 Clause lifecycle

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
  clause could not select hint locations by a stated rule. In this case
  the role *also* raises a finding of type `unable-to-hint` against
  itself (see §6.3).

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
`pending`, `pass`, `fail`, `awaiting-attestation`, `insufficient-basis`,
`unevaluated`.

### 6.2 Arrow lifecycle

An arrow's status is the **most severe** status of its clauses, plus
invalidation events. Precedence, most severe first:

| Arrow status | When |
|---|---|
| `invalidated` | An upstream spec the arrow declared dependence on (or, for arrows that declared no dependencies, any upstream spec) changed since last traversal. Supersedes everything below. |
| `blocked` | Any clause is `fail`. |
| `unevaluated` | No clause is `fail`, but at least one clause is `unevaluated`. |
| `provisional` | All evaluated clauses are `pass`, but at least one attested clause is `awaiting-attestation` or `insufficient-basis`. |
| `complete` | All clauses are `pass`. |

Precedence consequences:

- An arrow with both `unevaluated` clauses and `awaiting-attestation`
  clauses is `unevaluated`, not `provisional` — "we couldn't decide it"
  trumps "we decided but want confirmation."
- An arrow with both `fail` and `unevaluated` clauses is `blocked` —
  fails are immediately actionable; unevaluated needs depth re-routing.
- Only `complete` arrows satisfy the next role's input (the next
  arrow's predecessor). `provisional` does not.

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

### 6.3 Finding lifecycle

Findings are raised by the **adversarial phase** of an arrow (§11) or
by a role against itself (e.g., `unable-to-hint`). They live on the
arrow artifact, not on a clause.

```
(open) ──┬─ producer fixes → adversary re-attacks ──┬─ resolved
         │                                          └─ open (still defective)
         │
         └─ producer proposes accepted-risk ──┬─ operator attests → accepted-risk
                                              └─ operator rejects → open
```

The producer may NOT accept its own risk — only the operator may attest
`accepted-risk`.

**Verification phase machine check:** any finding above a declared
severity threshold still `open` synthesizes a `fail` on a corresponding
machine clause (`no-open-finding`), which propagates to arrow status
`blocked` by §6.2 precedence.

**Finding status set:** `open`, `resolved`, `accepted-risk`.

### 6.4 Project-level status (derived)

`(complete-against-grid-vN, residue-R, unevaluated-C)`:

- **vN** — the current arrow grid version.
- **R** — sum of operator-action cost across undeclared `(stratum,
  context)` cells (§2.3).
- **C** — count of arrows whose status is `unevaluated`.

C > 0 is the "green but will break on deployment" failure mode and
must never be hidden inside an aggregate pass. R > 0 is the
deferred-attention surface.

---

## 7. Routing

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
  clause's declared depth, or those clauses return `unevaluated` (§6.1).

ghyll defines depth *types*; a separate, swappable config maps types to
concrete model tiers. The schema does not name models, so it does not
rot when the model lineup changes.

---

## 8. The definition phase

Project setup includes a **definition phase** that runs before the
diamond. Its output is the **arrow grid** (§2): strata × contexts → the
declared arrow set, each arrow's clauses typed by evaluation type (§4)
and depth type (§5), plus the explicit **residue**, plus per-arrow
**dependency declarations** for invalidation propagation (§6.2).

The definition phase is **operator-owned, agent-assisted**. The agent
helps draft; the operator owns the grid. ghyll's role is to *enforce
that the residue is declared and attested* — not to generate the grid
unsupervised. (Defining the grid is a large, novel-architecture
activity, which is the activity class agents are weakest at; leaving it
agent-driven would need its own gate and start an infinite regress.)

The definition phase runs as a normal arrow against the v0 grid
(§2.4) — there is no special bootstrap arrow.

The definition-phase exit gate's central `attested` clauses:

- The residue is explicitly listed and the operator attests it is
  acceptable to proceed with.
- Every declared arrow's depth-type assignments are attested.
- Per-arrow dependency declarations for invalidation are recorded.

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
  determined**. The `locations` must be selected by a stated rule, not
  by the model's impression of where risk lies.
- A hint MUST NOT contain a verdict or recommendation. The strings
  `approve`, `looks good`, `sufficient`, `LGTM` (and equivalents) are
  forbidden in a hint. The producing role points; the operator judges.
- A hint MUST disclose its **residue** — what the selection rule did not
  classify. A hint that highlights without disclosing what it skipped
  silently steers the operator away from unclassified content, which can
  hide the very gap being looked for.
- If the role cannot select locations by a stated rule, the clause is
  recorded `unevaluated` with reason `no-rule-selectable-locations`,
  and the role raises a finding of type `unable-to-hint` against
  itself (§6.1, §6.3).

The hint is itself a model output and therefore depth-sensitive: a
shallow model produces a shallow hint. Hint-correctness is a
`depth-sensitive` clause and routes accordingly (§5, §7).

---

## 10. Operator attestation and weight

For each `attested` clause the operator returns:

- **`pass`** — operator inspected the hinted locations and attests.
- **`fail`** — operator rejects; the role does not exit; findings route
  back into the role.
- **`insufficient-basis`** — operator cannot attest (artifact ambiguous,
  context too large to judge). This is **not** a failure. It routes per
  the escalation paths. It exists so an uncertain operator is never
  forced to choose between a false `pass` and a punitive `fail`.

An arrow with any `attested` clause in `awaiting-attestation` or
`insufficient-basis` propagates status `provisional` per §6.2. A
`provisional` arrow does not satisfy the next role's input.

### 10.1 Attestation weight

Arrow weight is the **sum of per-clause operator-action costs** of its
clauses, measured in **operator-action units**.

- Each catalogue concept (§4) ships with a **harness default cost** in
  operator-action units (e.g., `compiles` = 0; `mutation-score` = 3).
  Default values are part of the catalogue alongside the concept.
- An arrow at definition time may **raise** (never lower) a clause's
  cost for its specific traversal. The analyst→architect arrow raises
  costs above defaults; that is *how* it ends up the heaviest per
  `direction.md` §3.6.

The unit set:

| Unit | Action | Cost |
|---|---|---|
| `confirm` | Single confirm verdict | 1 |
| `record-locations-inspected` | Confirm + record which hinted locations were actually inspected | 3 |
| `write-residue-note` | Confirm + record + write a residue note for what was not inspected | 5 |

(Exact per-concept default values are architect work; the unit set is
fixed.)

Friction is allocated deliberately: highest where rubber-stamping is
most dangerous.

---

## 11. Arrow phases

A role beginning an outbound transition does not simply hand off. Every
arrow carrying any `depth-sensitive` clause has three phases:

1. **Adversarial.** A *separate instance* from the producer (clean
   context) attacks the upstream artifact. It runs two modes:
   clause-falsification (try to make each declared depth-sensitive
   clause fail) and an open sweep (find what no clause names). Each hit
   becomes a finding (§6.3). The adversarial phase is depth-sensitive
   by nature; below required depth → findings status `unevaluated`.
2. **Remediation.** A bounded loop. The producer addresses each finding:
   either *fixes* it (re-attack confirms `resolved`) or *proposes
   accepted-risk* (the operator attests — the producer may not accept
   its own risk). Non-convergence in N rounds → escalate, do not spin.
3. **Verification.** Gate clauses evaluated, including a machine check
   that no finding above the severity threshold is left `open`.

A purely `machine` / `depth-robust` arrow runs **verification only** —
adversarial scrutiny of a deterministic check buys nothing.

The adversarial phase replaces what would otherwise be a standalone
adversary role: a role in a sequence can be skipped; a phase of a
transition cannot.

---

## 12. Undeclared transitions — on-the-spot arrow creation

When ghyll hits a transition with no declared arrow, it does **not**
fail and does **not** silently proceed. It **suspends** and triggers
on-the-spot arrow definition:

1. The arrow is defined now — clauses, evaluation types, depth types,
   dependency declarations.
2. This definition is **itself gated by operator attestation** — the
   producing role may not self-certify the definition it wants to use to
   continue.
3. Because depth-type assignment is `depth-sensitive`, on-the-spot
   creation **escalates the model tier for the duration of the
   definition act**, then routes the actual traversal per the resulting
   clauses.
4. The new arrow is **written back into the grid as version N+1** — it
   is not a local exception. An on-the-spot arrow that is not persisted
   is recreated, possibly inconsistently, on every future pass — the
   stationary-failure mode.
5. The interruption is **logged and visible**. A definition phase that
   produces many on-the-spot interruptions was shallow; that count is a
   free quality signal and must not be smoothed away.

---

## 13. What this schema does NOT solve

Stated explicitly so the harness is not mistaken for a guarantee it does
not provide:

- **Artifact depth.** The schema makes "was the check run, and did it
  pass" structural. It cannot make a thin spec deep or a shallow test
  meaningful. `depth-sensitive` + `attested` clauses + `mutation-score`
  machine clauses are the *defenses*; they are not proofs.
- **Operator fatigue.** `attested` clauses depend on operator attention.
  Weight (§10.1) allocates attention; it does not create it.
- **Definition-phase quality.** The grid is only as good as the
  definition phase. The residue and on-the-spot interruption count are
  the signals that surface a weak definition phase — they do not prevent
  one.

The schema converts "did the work happen and was it checked" from hope
into fact. It does not convert "is the work good". That remains a human
responsibility, made visible rather than eliminated.
