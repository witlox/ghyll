# ghyll — Direction

A description of what ghyll should be, written as a delta against the
current documentation (witlox.github.io/ghyll, fetched 2026-05-16). It
states what changes, what stays, and — explicitly — what is still
unproven.

This document is design intent. It is not validated. See §7.

---

## 1. The core change: a delivery tool becomes a correctness tool

**Current positioning.** The docs present ghyll as "Claude Code-style
agentic coding against self-hosted models." Every differentiator listed
is a *delivery* differentiator: model-specific dialects, self-hosted
endpoints, team memory, tamper-evident checkpoints. ghyll is currently
"an agent like the others, but on your own GPUs."

**Intended positioning.** ghyll's differentiator should be *behavioral*,
not infrastructural: ghyll is a coding agent that optimizes for
**correctness over speed and breadth**, and pays for it in **friction**.

This is a deliberate, bounded bet:

- ghyll is **correct for a narrow class** of work — novel architecture,
  correctness-critical systems, long-horizon projects where a defect
  reaching deployment is expensive.
- ghyll is **wrong for many things** — CRUD, migrations, glue code,
  rapid prototyping — where throughput is the real win and ghyll's gate
  ceremony is pure overhead.

Stating the second half is not a hedge. A tool that claims to be good
for everything is making a delivery claim. A tool that names what it is
bad for is making a correctness claim. The honesty is the position.

The wider market in 2026 is moving toward *more* autonomy — longer
unsupervised loops, parallel-agent throughput. ghyll moves the other
way: *constrained* autonomy, mandatory gates, operator-in-the-loop. This
is intentional and it is a bet, not a consensus.

---

## 2. Why — the failure ghyll exists to prevent

The motivating evidence is concrete. On a real project (a distributed
storage system), work an agent reported as complete proved substantially
incomplete on cloud deployment — after 10-15 deployment passes, each
asked to verify completeness first, each breaking on completeness.

An artifact-level audit of one bounded context (40 requirements) found:

- ~17 requirements **specified, attempted, but shallow** — tests that
  execute the code path but assert almost nothing (the recurring
  signature: a BDD step asserting only `last_error.is_none()`).
- 1 requirement **specified but never attempted**.
- 1 finding covering 23 operations **implemented, claimed "THOROUGH" by
  the verification role, with zero tests**.
- "Never specified" was unmeasured — the audit walked spec→code only.

The root cause was **not** missing definitions. The project's role
definitions were good — its auditor role already defined a depth ladder
and a falsifiability gate. The root cause was that the workflow was
**prose, not enforcement**: nothing structurally required a gate to pass
before the next role proceeded, and the cross-context integration role
was strong but simply never ran.

ghyll's job is to be the enforcement layer that prose workflows cannot
be. Not better definitions — *enforced* ones.

---

## 3. What changes

### 3.1 Context-depth routing — change the trigger

**Current:** routing escalates fast→deep "based on task complexity."

**Problem:** task complexity is assessed by the model. A model has no
reliable sense of effort magnitude; it under-escalates exactly on tasks
that read as simple but are not. This automates a junior's estimate.

**Change:** route on **gate definition**, not self-assessed complexity.
Each gate clause carries a declared depth type (`depth-robust` /
`depth-sensitive`). A pass runs at the lowest tier meeting the maximum
depth requirement of the clauses on the arrow it traverses. Routing
input becomes a static property of the work, not a guess about it.

### 3.2 Drift-aware memory — keep, but it is not the main mechanism

**Current:** cosine-similarity drift detection backfills context when
the conversation drifts from the task.

**Problem:** the dominant observed failure is not semantic drift. Shallow
work stays perfectly on-topic — a shallow test and a deep test embed
almost identically. Cosine drift detection cannot see the failure that
matters.

**Change:** keep drift-aware memory for what it is good at (recovering
lost context), but it is **not** ghyll's correctness mechanism and the
docs should stop implying it is. The correctness mechanism is the gate
system (§3.4).

### 3.3 Roles become fixed and first-class

**Current:** ghyll has no role concept; it is a single agentic loop.

**Change:** ghyll embeds a **fixed set of roles**. A role is not
freely redefinable — a role you can argue with is a gate you can argue
with. Roles may be **extended** with language-specific or behavioral
additions, but their contracts (entry precondition, exit gate) are
fixed.

The role set (the "diamond"):

```
analyst → architect → implementer → integrator
```

Note: there is **no standalone adversary role and no standalone
auditor role**. Adversarial scrutiny and depth classification are
*phases of every arrow* (§3.5), not roles — a role in a sequence can
be skipped; a phase of a transition cannot. The depth-classification
work that an auditor role might do is part of the per-arrow
adversarial phase, applied uniformly to every depth-sensitive arrow.

### 3.4 The workflow becomes an embedded, enforced default

**Current:** no workflow; the agent does what the prompt says.

**Change:** the diamond workflow is **embedded as the default** and is
**enforced**, not advised. It is **overridable** — the operator stays
sovereign over which workflow runs — but the default is opinionated and
the override is explicit.

Enforcement specifics:

- Every **role transition is an arrow** — a first-class artifact
  asserting the upstream output composes to satisfy the downstream
  input. Nodes (specs, code) are produced by roles; arrows are produced
  by transitions and are what the harness gates.
- Transitions are legal **only along declared arrows**. An undeclared
  transition is refused, not silently allowed.
- Gate clauses are typed: `machine` (harness-decided) vs `attested`
  (operator-decided). `machine` is not "objective" — a shallow artifact
  passes a shallow machine check; the type is about *who decides*.
- A clause produced below its required model depth is **`unevaluated`**
  — a distinct status from `provisional` (passed, awaiting confirmation)
  and `fail`. `unevaluated` means the instrument was not sharp enough
  for the claim to mean anything. It is the status that most resembles
  "green but will break on deployment" and must never be hidden.
- Project status is never "complete." It is **"complete against grid
  vN, residue R, unevaluated C"** — three numbers, always.

### 3.5 Every arrow has a three-phase structure

A role beginning an outbound transition does not simply hand off. The
arrow has three phases:

1. **Adversarial** — a *separate instance* from the producer (clean
   context) attacks the upstream artifact. It runs two modes:
   clause-falsification (try to make each declared depth-sensitive
   clause fail) and an open sweep (find what no clause names). Depth-
   sensitive by nature; below required depth → findings `unevaluated`.
2. **Remediation** — a bounded loop. The producer addresses each
   finding: either *fixes* it (re-attack confirms `resolved`) or
   *proposes accepted-risk* (the operator attests — the producer may not
   accept its own risk). Non-convergence in N rounds → escalate, do not
   spin.
3. **Verification** — gate clauses evaluated, including a machine check
   that no finding above a severity threshold is left undisposed.

The adversarial + remediation phases attach to arrows carrying
depth-sensitive clauses. A purely `machine`/`depth-robust` arrow runs
verification only — adversarial scrutiny of a deterministic check buys
nothing.

### 3.6 The analyst arrow is asymmetric — heaviest by design

The analyst→architect arrow is the heaviest in the system, and not by
arbitrary weighting:

- It is the only arrow attacking an artifact with no executable referent
  and no upstream artifact to check against — it hunts *absence*, which
  has no line number.
- Error cost is monotone upstream: a spec gap becomes architecture,
  code, and tests built on the gap. The analyst arrow is the cheapest
  place to catch the most expensive class of error.
- Its adversarial phase is **open-sweep-dominant** — clause-
  falsification is weak here because the clauses are themselves soft.
- It must route to the deepest available tier unconditionally.
- Its remediation loop is expected to terminate by **operator residue-
  attestation**, not clean convergence — "is this spec complete" has no
  clean terminator. Non-convergence here is the residue mechanism
  working, not failing.

A heavy analyst arrow does **not** mean the spec must be perfect before
proceeding — that is the waterfall failure in this vocabulary. It means
the analyst arrow must honestly *report its residue*.

### 3.7 The workflow is a cycle, not a chain — integrator feeds back to analyst

A pure forward chain cannot learn from integration. The integrator
detects a class of defect — incompatible assumptions at an unspecified
cross-context seam — that is invisible to every earlier role: each
context is locally fine; the defect exists only in composition.

That finding is an **analyst-level defect** (a missing cross-context
specification) and must not route to local code-patching, which would
launder a spec defect into a shallow fix.

**Change:** integrator findings are **typed**. The type "missing/wrong
cross-context specification" routes to a **grid amendment**: the analyst
is re-engaged, the missing interaction is specified, the grid increments
to vN+1, and downstream arrows that depended on the changed spec are
marked **`invalidated`** and must be re-traversed.

Consequences, stated plainly:

- The diamond is a **cyclic graph**, and the cycle is load-bearing.
- **Completion is revocable.** Project status is **non-monotone** — an
  arrow that was `complete` can become `invalidated`. `gates.md` gains
  an `invalidated` status.
- The integrator→analyst edge is **operator-attested** — the operator
  triages "genuine missing arrow" vs "local bug." Without that triage,
  every integration hiccup escalates and the project never converges.

### 3.8 ghyll should be able to refuse

If ghyll is correct-for-narrow and wrong-for-broad, it must be able to
**decline projects it is wrong for**. The definition phase should detect
a low-risk profile — few contexts, no cross-context seams, no novel
architecture — and say so: "ghyll's friction will be pure cost here; use
a fast agent." An opinionated tool that cannot say no to the wrong job
is not opinionated; it just makes every job slow. This is the most
differentiated behavior in the design.

---

## 4. What stays unchanged

- Self-hosted, open-weight focus; SGLang endpoints on owned hardware.
- Model-specific dialects (no provider abstraction). But see §6 — the
  tier model is a real cost.
- SRT for sandboxing; always-yolo execution.
- Memory / checkpoints / git-sync — useful as continuity infrastructure.
  Recast as continuity, not as the correctness mechanism.

---

## 5. Friction is a cost, not the goal

ghyll will produce more friction than any other agent. This is correct
**only** where the friction is load-bearing. Every gate, phase, and
attestation must answer: *does this catch a failure that would otherwise
reach deployment?*

- **Correctness friction** — catches a real, demonstrable failure class.
  Keep it. It is the product. (Proven so far: mutation-score machine
  clauses and the implementer→integrator adversarial phase — aimed at
  the 17 audited shallow-test rows.)
- **Dead friction** — feels rigorous, discriminates nothing. Pure cost.

The two feel identical to the operator: both make you wait. ghyll must
be able to tell them apart, or it will accrete ceremony until operators
route around all of it — including the parts that mattered.

"ghyll is opinionated / not for that" must not become the phrase that
excuses dead friction. When friction bites, the question is not "is this
a job ghyll is wrong for" — it is "did this friction just catch
something." If not, that is a bug report, not a defense.

---

## 6. Open decisions — not yet made

- **Tier count.** Two tiers (fast/deep) vs the documented three.
  Each model is a hand-tuned dialect — a real maintenance and drift
  surface. Drop the third unless it earns its tuning cost.
- **On-the-spot arrow ceremony.** Heavy (operator stops and thinks) vs
  light-provisional (agent proposes, operator confirms later — safe only
  if the later attestation is itself machine-enforced and unskippable).
- **Definition phase authorship.** Operator-authored vs agent-assisted.
  Agent-authored needs its own gate and risks regress.
- **Integrator→analyst triage.** Hard operator gate vs a confident
  adversarial pass proposing the grid amendment for lighter confirmation.

These are operator decisions. They should not be settled silently in
code.

---

## 7. Status — read before building

This direction was derived in a single long design conversation — the
medium whose drift motivated ghyll in the first place. That is a
specific, named risk.

What has been **tested**: the diagnosis. The audit returned real
artifact-level bucket counts from a real codebase.

What has **not** been tested: the solution. The gate system, the
three-phase arrow, the integrator cycle, `invalidated` status — all are
supported only by internal consistency. Internal consistency is also a
property of a well-constructed wrong answer.

Therefore:

1. This is design intent, version 1. Treat it as a hypothesis.
2. Before building, validate cold: a fresh context given only the schema
   (`gates.md`) and the role files, asked whether the design is
   self-consistent and a builder would hit contradictions.
3. Build the **enforcement spine first** — the machine-clause runner and
   transition refusal. An unenforced router or an unvalidated depth
   model is decoration. Then the integrator gate (the audited root
   cause). Then reconcile the remaining roles. Then routing, definition
   phase, on-the-spot arrows.
4. "ghyll is a very different coding agent" is a flag to plant **after**
   ghyll catches one real failure it was designed to catch — measured as
   a ratio of friction-that-caught-something to friction-that-only-cost.
   Until then, "correctness over speed" is a hypothesis with good
   arguments, not a proven product.
