# Harness Gate Schema

This document defines how ghyll gates role transitions. It is the
harness-wide schema; individual role files declare *which* gates and
clauses they carry, but never redefine the semantics here.

It exists because the Kiseki experience showed a specific failure: the
role definitions were good — the auditor role already defined a
STUB/SHALLOW/MOCK/NETWORK depth ladder and a falsifiability gate — but
none of it was *enforced*. Definitions without enforcement drift. This
schema is the enforcement layer.

---

## 1. Roles, transitions, arrows

ghyll runs **one role at a time**. The default workflow (the diamond) is
a declared sequence of roles:

```
analyst → architect → adversary → implementer → auditor → integrator
```

A **role transition** is the move from one role to the next. Every
transition IS an **arrow**. An arrow is a first-class artifact: it
asserts that the upstream role's output *composes to satisfy* the
downstream role's input requirement. The node artifacts (specs, code,
tests) are produced by roles; the arrow artifact is produced by the
*transition* and is what the harness gates.

Transitions are legal **only along declared arrows**. A switch to a role
with no declared arrow from the current role is refused — see §6.

The diamond is the default declared arrow set. Other workflows are other
declared subgraphs of the same role set.

---

## 2. The arrow grid

Completeness is not one global property. It is **stratified** and
**per-context**. An arrow's completeness predicate is therefore scoped:
`complete-at-arrow-A-for-context-C`. The full set of arrows across all
strata and contexts is the **arrow grid**.

The grid is **declared, versioned, and open** — never frozen:

- Declared: produced by the definition phase (§7).
- Versioned: every change to the grid increments its version.
- Open: the grid carries an explicit **residue** — strata and
  cross-context arrows known to exist but not yet declared. Residue is a
  first-class, visible quantity. The harness never reports a project as
  "complete" — it reports "complete against grid vN, residue R,
  unevaluated C" (§5).

A frozen grid would let the harness certify completeness against a
definition known to be partial. That is false completeness promoted from
a model behavior to a harness guarantee. The residue field is what
prevents it.

---

## 3. Gate structure

Each role carries a **contract**: an entry precondition and an exit gate.

- **Entry precondition** — conditions that must hold before the role is
  entered. The harness checks these; the role does not.
- **Exit gate** — clauses that must pass before the role's output
  becomes eligible to satisfy the next role's entry precondition.

A gate is a set of **clauses**. Every clause has two declared types: an
**evaluation type** (§4) and a **depth type** (§5).

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

### Rules for `machine` clauses

- The **clause type catalogue** is predefined and harness-global:
  `compiles`, `clippy-clean`, `every-step-bound`, `no-orphan-symbol`,
  `no-todo-marker`, `mutation-score`, `kill-server-fails-integration`,
  `trace-link-present`, `acyclic-dependency-graph`. This catalogue is
  fixed; ghyll ships it.
- A small **universal base set** of machine clauses applies to every
  arrow regardless of requirement (`compiles`, `clippy-clean`,
  `no-todo-marker`, `every-step-bound`). Inherited automatically.
- All **other machine clause instances are derived per-arrow** from the
  requirement during the definition phase. Which mutations, over which
  code, asserting which behaviour — that is born with the requirement
  and cannot be predefined. Predefining the per-arrow instance set
  recreates the auditor's "THOROUGH" failure: a fixed checklist that is
  green while the system is broken.

### Rules for `attested` clauses

- The producing role MUST NOT self-certify an `attested` clause.
- The producing role MUST emit a **hint** for each `attested` clause
  (§8).
- The operator returns one of three verdicts per `attested` clause:
  `pass`, `fail`, `insufficient-basis` (§9).

---

## 5. Depth type and `unevaluated`

Some clauses can be honestly produced or attested by any model. Some
require a model of sufficient depth. The **depth type** of a clause
declares the minimum model depth required to *honestly* produce or
evaluate it.

| Depth type | Meaning |
|------------|---------|
| `depth-robust` | Any model tier can produce/evaluate this honestly; a weak model cannot fake it (`compiles`, `every-step-bound`) |
| `depth-sensitive` | Requires a model at or above a declared depth; a weaker model produces confident-but-wrong output (`hint correctness`, `failure-mode adequacy`, RFC-conformance judgement) |

The depth type of a clause is **itself a judgement**, assigned at clause
authoring time (definition phase), and is **an `attested` item on the
definition-phase gate**. It is not machine-derivable and must not default
to `depth-robust` for convenience.

### `unevaluated`

If a `depth-sensitive` clause is produced or evaluated by a model below
its required depth, the clause status is **`unevaluated`** — NOT
`provisional`, NOT `fail`.

- `provisional` = passed, awaiting confirmation.
- `unevaluated` = the instrument that produced this was not sharp enough
  for the claim to carry meaning.

`unevaluated` is a first-class status. An arrow with any `unevaluated`
clause cannot close. A `depth-sensitive` clause whose required depth
exceeds *every available model tier* routes to operator attestation — it
is never laundered through the largest available model and recorded as
`provisional`.

Project-level status always carries three numbers:
**complete against grid vN, residue R, unevaluated C.**
`C > 0` is the condition that most resembles "green but will break on
deployment". It must never be hidden inside an aggregate pass.

---

## 6. Routing

ghyll routes each pass to a model tier. Routing is driven by **the gate
definition**, never by a model's self-assessment of task complexity
(self-assessed complexity is the magnitude-blind component rating its own
magnitude — it under-escalates exactly when it matters).

**Routing rule:** a pass traversing an arrow runs at the lowest model
tier whose depth meets the **maximum depth requirement across all clauses
on that arrow**.

- Machine-only / `depth-robust`-only arrow → fast tier. Not a risk:
  `depth-robust` clauses cannot be faked by a weak model.
- Arrow with any `depth-sensitive` clause → a tier meeting that clause's
  declared depth, or those clauses return `unevaluated` (§5).

ghyll defines depth *types*; a separate, swappable config maps types to
concrete model tiers. The schema does not name models, so it does not rot
when the model lineup changes.

---

## 7. The definition phase

Project setup includes a **definition phase** that runs before the
diamond. Its output is the **arrow grid** (§2): strata × contexts → the
declared arrow set, each arrow's clauses typed by evaluation type (§4)
and depth type (§5), plus the explicit **residue**.

The definition phase is **operator-owned, agent-assisted**. The agent
helps draft; the operator owns the grid. ghyll's role is to *enforce that
the residue is declared and attested* — not to generate the grid
unsupervised. (Defining the grid is a large, novel-architecture activity,
which is the activity class agents are weakest at; leaving it
agent-driven would need its own gate and start an infinite regress.)

The definition-phase gate's central `attested` clauses:

- The residue is explicitly listed and the operator attests it is
  acceptable to proceed with.
- Every declared arrow's depth-type assignments are attested (§5).

---

## 8. Hints

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
- If the role cannot select locations by a stated rule, it reports the
  clause `unable-to-hint` — itself a signal that the artifact may be too
  thin to attest.

The hint is itself a model output and therefore depth-sensitive: a
shallow model produces a shallow hint. Hint-correctness is a
`depth-sensitive` clause and routes accordingly (§5, §6).

---

## 9. Operator attestation

For each `attested` clause the operator returns:

- **`pass`** — operator inspected the hinted locations and attests.
- **`fail`** — operator rejects; the role does not exit; findings route
  back into the role.
- **`insufficient-basis`** — operator cannot attest (artifact ambiguous,
  context too large to judge). This is **not** a failure. It routes per
  the escalation paths. It exists so an uncertain operator is never
  forced to choose between a false `pass` and a punitive `fail`.

An arrow with any `attested` clause not yet `pass` propagates status
`provisional`. A `provisional` arrow does not satisfy the next role's
entry precondition.

### Attestation weight

Arrows carry a **weight**. A heavier arrow demands a heavier operator
action — e.g. the operator records which hinted locations were actually
inspected, not a single confirmation. Weight is set per arrow at
definition time. Friction is allocated deliberately: highest where
rubber-stamping is most dangerous.

---

## 10. Undeclared transitions — on-the-spot arrow creation

When ghyll hits a transition with no declared arrow, it does **not**
fail and does **not** silently proceed. It **suspends** and triggers
on-the-spot arrow definition:

1. The arrow is defined now — clauses, evaluation types, depth types.
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

## 11. What this schema does NOT solve

Stated explicitly so the harness is not mistaken for a guarantee it does
not provide:

- **Artifact depth.** The schema makes "was the check run, and did it
  pass" structural. It cannot make a thin spec deep or a shallow test
  meaningful. `depth-sensitive` + `attested` clauses + `mutation-score`
  machine clauses are the *defenses*; they are not proofs.
- **Operator fatigue.** `attested` clauses depend on operator attention.
  Weight (§9) allocates attention; it does not create it.
- **Definition-phase quality.** The grid is only as good as the
  definition phase. The residue and on-the-spot interruption count are
  the signals that surface a weak definition phase — they do not prevent
  one.

The schema converts "did the work happen and was it checked" from hope
into fact. It does not convert "is the work good". That remains a human
responsibility, made visible rather than eliminated.
