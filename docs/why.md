# Why ghyll

Most coding agents optimize for **speed** and **breadth** — they
finish faster, they touch more files, they accept more vague
prompts. ghyll optimizes for **correctness** and pays for it in
**friction**.

The honest second half of that pitch is the position: ghyll is
wrong for a lot of work. Writing a CRUD endpoint, fixing a typo,
generating a migration, or wiring glue between two libraries —
these are throughput tasks, and ghyll's gate ceremony is pure
overhead. Use a faster agent.

ghyll is right when a defect reaching deployment is expensive:
novel architecture, distributed-system invariants, long-horizon
projects where the cost of "looks fine, ships fine, breaks in
production" is paid in days of incident response. There the
friction is the feature.

This chapter walks through the five design decisions that
distinguish ghyll, in the order they matter.

---

## The risk ghyll is built to manage

ghyll's correctness mechanism is **behavioral, not infrastructural**.
Drift detection, sandboxes, retries, and rollback help — but they
catch a class of failure that's downstream of the real problem.

The real problem is **models confidently delivering work they
were never positioned to evaluate**. A model trained on millions
of similar-looking codebases produces plausible code on demand.
It also produces plausible *reviews* of that code on demand — and
plausible explanations, plausible tests, plausible "I checked it"
statements. Past a certain depth of task, the model's output is
indistinguishable from work that was actually done.

Every design decision below follows from treating that as the
central risk.

---

## 1. Roles are fixed (the diamond)

### The decision

ghyll runs four roles end-to-end: **analyst → architect →
implementer → integrator**. The set is fixed at build time. You
cannot add a `reviewer` or a `tester` role at runtime; you cannot
swap the diamond for a different shape.

### The alternative

The obvious alternative is "roles as runtime config" — the
operator declares a role set in a YAML file at the start of a
project, and ghyll uses whatever was declared.

### Why this beats it

Role-shape drift is one of the most reliable ways for an agent
to lose accountability. If the integrator can also act as the
implementer, "I checked it" and "I wrote it" become the same
sentence — and the cross-check disappears. The analyst→architect
handoff exists *precisely* because the analyst doesn't get to be
the architect; they don't get to mark their own homework. Letting
the operator collapse roles is letting them remove the contract
the diamond enforces.

Fixed roles guarantee that every project enforces the same
separation. An ADR can change the diamond shape, but a single
operator under deadline cannot.

### Reference

- [ADR-008](decisions/008-v2-fixed-roles-deprecate-runtime-workflow-roles.md) —
  the decision to deprecate runtime workflow roles.
- [V2-ADR-003](decisions/v2/003-four-role-diamond.md) — why
  four roles, why this shape.
- [`specs/architecture/roles/`](https://github.com/witlox/ghyll/tree/main/specs/architecture/roles) —
  the four role contracts, embedded into the binary at build
  time via `//go:embed`.

---

## 2. Transitions are arrows

### The decision

An arrow named `analyst→architect/default` is the project's
declaration that the **analyst** hands work off to the
**architect** in the `default` context. Before the runtime
will dispatch a [pass](glossary.md#pass) on that arrow, every
clause attached to it (e.g. `tests-pass`, `lint-clean`,
`no-todo-marker`) must reach a verdict — `pass`, `fail`, or
`insufficient-basis`. If a clause is still `unevaluated`, the
arrow stays open and the operator is asked what to do.

The pass is the runtime invocation of the arrow; the
[stratum](glossary.md#stratum) decides whether MiniMax M2.5 or
GLM-5 runs that pass. Every cross-role handoff is a first-class
artifact in this shape — source role, target role, context (which
bounded context within the project), stratum, and a list of typed
gate [clauses](glossary.md#clause) — and arrows are persisted in
`.ghyll/grid.v1.yaml` and replay across sessions.

> **Note on "context":** in ghyll's v2 docs (this page, the
> operator guide, getting-started) "context" means a **bounded
> context** in the DDD sense — a logical scope of the project,
> like `checkout` or `inventory`. This is NOT the LLM context
> window. The LLM-context-window meaning still applies in
> [`docs/internals/context.md`](internals/context.md). This is
> the single most-likely-to-confuse term in the whole doc set.

An arrow from analyst to architect exists; an arrow from analyst
to integrator does not. **Undeclared transitions don't silently
proceed — they suspend**, and the operator is asked to declare a
new arrow (on-the-spot arrow creation) or refuse the work.

### The alternative

The obvious alternative is "implicit handoff": each role does
its work and produces an artifact; the next role picks up
whatever was produced. The path between roles is whatever
happens to happen.

### Why this beats it

Undeclared paths are how agents quietly skip steps. An LLM that
"just goes ahead and implements" is bypassing analysis; an LLM
that returns a `pass` verdict on a clause it didn't actually
evaluate is bypassing the gate. A typed arrow makes that bypass
a visible event the operator has to acknowledge:

- The arrow doesn't exist → the runtime suspends and asks.
- The clause's `unevaluated` status doesn't auto-derive to
  `pass` → the operator sees it.
- The arrow's pass count, lock status, and last verdict are
  persisted → forensic reconstruction is mechanical.

### Reference

- [`specs/architecture/v2-design.md`](https://github.com/witlox/ghyll/blob/main/specs/architecture/v2-design.md) —
  gate-and-arrow rationale in full.
- [`specs/architecture/gates.md`](https://github.com/witlox/ghyll/blob/main/specs/architecture/gates.md) —
  evaluation types, depth types, arrows, routing, attestation.
- [V2-ADR-008](decisions/v2/008-arrow-pass-identity.md) —
  why arrows and passes are separate entities.
- [ADR-013](decisions/013-pass-entity-and-registry.md) —
  the Pass entity + registry.
- [V2-ADR-013](decisions/v2/013-add-tests-pass-concept.md) —
  adding the `tests-pass` concept to the closed catalogue.
- [Architecture flows](architecture-flows.md) — sequence
  diagrams for the major arrow flows.

---

## 3. Clauses are typed on two axes

### The decision

Each clause carries two type tags:

- **Evaluation type** — `machine` or `attested`.
  - `machine`: a deterministic check ghyll can run (tests pass,
    lint clean, schema validates, no-todo-marker grep returns
    nothing).
  - `attested`: an operator reads, judges, records a verdict
    (the typed verdict goes through the Tier 2 modal flow).
- **Depth type** — `depth-robust` or `depth-sensitive`.
  - `depth-robust`: a small model can produce or evaluate this
    honestly.
  - `depth-sensitive`: the work requires the deep tier to avoid
    plausible-looking errors.

A `depth-sensitive` clause produced by an under-depth model has
status `unevaluated` — not `pass`, not `fail`.

In the verdict modal, `unevaluated` is the default state of every
clause the runtime hasn't yet asked you about; in `/list-arrows`
and `ghyll arrow show`, an arrow with any `unevaluated` clauses
cannot transition closed.

### The alternative

The obvious alternative is "verification is verification" —
every check is the same kind of thing, run by whatever happens
to be in front of it.

### Why this beats it

Collapsing the axes is where shallow agents go wrong.

- "Does this code pass tests?" is a `machine` / `depth-robust`
  check. Any model can run `make test` and read the exit code.
- "Does this design respect the cross-context invariant from the
  analyst's spec?" is `attested` / `depth-sensitive`. It needs
  the deep tier *and* an operator's judgment. Asking the fast
  tier produces fluent-sounding nonsense.

The two questions read similarly in casual prose ("is this
verified?") but require completely different machinery. The type
system makes the difference legible and the dispatcher refuses
to run a `depth-sensitive` clause on the fast tier.

`unevaluated` is first-class because it is the status that most
resembles "green but will break on deployment" — and it must
never be hidden in a summary. ghyll's project-status reporting
treats `unevaluated` differently from both `pass` and `fail`.

### Reference

- [`specs/architecture/gates.md`](https://github.com/witlox/ghyll/blob/main/specs/architecture/gates.md) —
  clause type axes, the full evaluation contract.
- [V2-ADR-005](decisions/v2/005-concept-catalogue.md) — why
  the concept catalogue is closed (18 concepts, embedded).
- [V2-ADR-006](decisions/v2/006-per-concept-schemas.md) — per-
  concept schema files.

---

## 4. Routing follows the gate, not the model

### The decision

A [**pass**](glossary.md#pass) is one runtime invocation of an
arrow — open the arrow, run its clauses, collect verdicts,
close. Multiple passes can run against the same arrow over a
project's life.

A pass runs at the **lowest tier** that meets the maximum depth
requirement of the clauses on the arrow. Self-assessed task
complexity is not a routing input. The model doesn't decide
which model to use — the gate does.

If an arrow's clauses are all `depth-robust`, the fast tier runs
the pass. If even one clause is `depth-sensitive`, the dispatcher
escalates to the deep tier. No exceptions, no overrides from
inside the pass.

### The alternative

The obvious alternative is "ask the model how hard this is" —
every coding agent that ships routing does some variant of this.

### Why this beats it

Every routing system that asks the model "is this hard?" gets
the wrong answer reliably:

- Easy tasks get over-escalated (the model defaults to "looks
  complex" to be safe → cost balloons).
- Hard tasks get plausibly handled by the small model (the model
  is fluent enough to claim it understood when it didn't → the
  bug ships).

Gate-driven routing eliminates the question entirely. The
depth-tag on the clause is the answer. Adding a clause means
re-running routing; the routing decision is reproducible because
it depends only on the arrow definition, not on a fresh model
call.

### Reference

- [ADR-007](decisions/007-tier-based-routing.md) — tier-based
  routing decision.
- [`dialect/router.go`](https://github.com/witlox/ghyll/blob/main/dialect/router.go) —
  context-depth routing implementation.

---

## 5. Sandbox-only execution

### The decision

ghyll runs in YOLO mode: tool calls from the model execute
**directly**, with no confirmation, no permission check, no
filtering. The **sandbox boundary** is the only security
boundary.

The runtime detects whether it's inside a sandbox at startup
(`cmd/ghyll/sandbox.go` checks for Docker, Podman, bubblewrap,
sandbox-exec, Firejail, Kubernetes pod). Operators can opt into
hard-refusal via `GHYLL_REQUIRE_SANDBOX=1`.

### The alternative

The obvious alternative is "in-process permission gates" — every
tool call is reviewed by a layer that decides whether it's safe.
Commercial coding agents do this extensively.

### Why this beats it

In-process permission gates are theatre. They protect against
careless model output but not against compromised or adversarial
output:

- The model can call shell with arbitrary arguments → no
  permission layer covers every escape path.
- The model can write to a file the permission layer "trusts" →
  permission layers always have trust roots.
- Operators learn to bypass friction → "is this safe?" prompts
  collapse to "yes" reflex.

A real sandbox is honest. It catches every escape (it's the
operating system, not a guess about what the operator meant).
The friction is upfront (you set it up once) instead of
per-tool-call.

This was a contested decision: an early prod-readiness pass
added in-process control layers (env scrub on bash, git
subcommand allowlist, symlink refusal in edit/file ops). They
were all reverted on the explicit grounds above; see the design
pushback in the
[CHANGELOG](https://github.com/witlox/ghyll/blob/main/CHANGELOG.md)
under v2026.30.x for the list of reverts.

ghyll trusts nothing it can't sandbox. If you can't sandbox it,
don't run ghyll there.

### Reference

- [`cmd/ghyll/sandbox.go`](https://github.com/witlox/ghyll/blob/main/cmd/ghyll/sandbox.go) —
  detection table.
- [Operator guide — Sandbox setup](operator-guide.md#sandbox-setup) —
  copy-pastable recipes for each sandbox.

---

## Bonus: ghyll can refuse

The `ghyll init` definition phase scans the project, applies the
modify rules, and synthesizes a grid. If the project is empty,
greenfield, single-file, or otherwise a poor fit, ghyll surfaces
that. The operator can override (it's still your project), but
the refusal — or the "are you sure?" prompt — is a feature.

This is the position again, made operational: ghyll knows it's
not right for everything, and it tells you when it's about to be
wrong.

---

## See also

- [Operator guide](operator-guide.md) — install, configure, run.
- [Architecture flows](architecture-flows.md) — the sequence
  diagrams for the gate-and-arrow runtime.
- [Architecture decisions](decisions/) — all 17 main ADRs,
  13 v2 ADRs, and 9 v4 ADRs.
