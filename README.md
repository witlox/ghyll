# Ghyll

[![CI](https://github.com/witlox/ghyll/actions/workflows/ci.yml/badge.svg)](https://github.com/witlox/ghyll/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/witlox/ghyll/branch/main/graph/badge.svg)](https://codecov.io/gh/witlox/ghyll)
[![Go Report Card](https://goreportcard.com/badge/github.com/witlox/ghyll)](https://goreportcard.com/report/github.com/witlox/ghyll)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

> A coding agent for self-hosted open-weight models with a
> typed-clause, arrow-based correctness mechanism. Sandbox-only.
> Right for novel architecture and correctness-critical systems;
> intentionally wrong for CRUD, glue code, and rapid prototyping.

## The position

Most coding agents optimize for speed and breadth — they finish
faster, they touch more files, they accept more vague prompts.
ghyll optimizes for **correctness** and pays for it in **friction**.

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

## Why this way

ghyll's correctness mechanism is **behavioral, not infrastructural**.
Drift detection, sandboxes, retries, and rollback help — but they
catch a class of failure that's downstream of the real problem.
The real problem is models confidently delivering work they were
never positioned to evaluate. Five design decisions follow from
treating that as the central risk:

### 1. Roles are fixed (the diamond)

ghyll runs four roles end-to-end: **analyst → architect →
implementer → integrator**. The set is fixed at build time. You
cannot add a `reviewer` or a `tester` role at runtime; you cannot
swap the diamond for a different shape.

Why: role-shape drift is one of the most reliable ways for an
agent to lose accountability. If the integrator can also act as
the implementer, "I checked it" and "I wrote it" become the same
sentence — and the cross-check disappears. Fixed roles enforce a
contract that survives every session.

See [ADR-008](docs/decisions/008-v2-fixed-roles-deprecate-runtime-workflow-roles.md)
and the role files at
[`specs/architecture/roles/`](specs/architecture/roles/).

### 2. Transitions are arrows

An arrow named `analyst→architect/default` is the project's
declaration that the **analyst** hands work off to the **architect**
in the `default` context. Before the runtime will dispatch a pass
on that arrow, every clause attached to it (e.g. `tests-pass`,
`lint-clean`, `no-todo-marker`) must reach a verdict — `pass`,
`fail`, or `insufficient-basis`. If a clause is still
`unevaluated`, the arrow stays open and the operator is asked
what to do.

An arrow from analyst to architect exists; an arrow from analyst
to integrator does not. Undeclared transitions don't silently
proceed — they **suspend**, and the operator is asked to declare
a new arrow or refuse the work.

Why: undeclared paths are how agents quietly skip steps. An LLM
that "just goes ahead and implements" is bypassing analysis; a
typed arrow makes that bypass a visible event the operator has
to acknowledge.

See the [glossary](docs/glossary.md) for grounded definitions of
arrow, pass, clause, and verdict;
[`specs/architecture/v2-design.md`](specs/architecture/v2-design.md)
and [`specs/architecture/gates.md`](specs/architecture/gates.md)
for the design reference.

### 3. Clauses are typed on two axes

Each clause carries two type tags:

- **Evaluation type** — `machine` (a deterministic check: tests
  pass, lint clean, schema validates) or `attested` (an operator
  reads, judges, records a verdict).
- **Depth type** — `depth-robust` (a small model can produce or
  evaluate this honestly) or `depth-sensitive` (the work requires
  the deep tier to avoid plausible-looking errors).

Why: collapsing these axes is where shallow agents go wrong.
"Does this code pass tests?" is a machine / depth-robust check.
"Does this design respect the cross-context invariant from the
analyst's spec?" is attested / depth-sensitive. Both are
"verification" in casual speech, but they require completely
different machinery. The type system makes the difference legible.

For example, the `lint-clean` clause is `machine` / `depth-robust`
— ghyll just runs the linter. The `acyclic-dependency-graph`
clause is `attested` / `depth-sensitive` — the runtime escalates
to GLM-5 to draft the analysis, then opens a verdict modal
asking the operator to confirm `pass`, `fail`, or
`insufficient-basis`.

A `depth-sensitive` clause produced by an under-depth model has
status `unevaluated` — not `pass`, not `fail`. `unevaluated` is
a first-class status precisely because it is the status that most
resembles "green but will break on deployment," and it must never
be hidden in a summary.

### 4. Routing follows the gate, not the model

A pass runs at the **lowest tier** that meets the maximum depth
requirement of the clauses on the arrow. Self-assessed task
complexity is not a routing input. The model doesn't decide
which model to use — the gate does.

Why: every routing system that asks the model "is this hard?"
gets the wrong answer reliably. Easy tasks get over-escalated;
hard tasks get plausibly handled by the small model. Gate-driven
routing eliminates the question — the depth-tag on the clause is
the answer.

See [ADR-007](docs/decisions/007-tier-based-routing.md).

### 5. Sandbox-only execution

ghyll runs in YOLO mode: tool calls from the model execute
directly, with no confirmation, no permission check, no filtering.
The **sandbox boundary** is the only security boundary.

Why: in-process permission gates are theatre. They protect against
careless model output but not against compromised or adversarial
output, and they add friction that operators learn to bypass. A
real sandbox (Docker, bubblewrap, sandbox-exec, Firejail,
Kubernetes pod, …) is honest: it catches every escape, including
the ones a permission layer would have missed. ghyll trusts
nothing it can't sandbox.

See [`cmd/ghyll/sandbox.go`](cmd/ghyll/sandbox.go) for the
detection table, and the [operator guide](docs/operator-guide.md)
for setup recipes.

---

## When ghyll is the right tool

- Designing a system you've never built before — architecture
  decisions you'll live with for years.
- Correctness-critical code: distributed-system invariants,
  cryptographic protocols, financial state machines.
- Long-horizon refactors where regressions are paid in incident
  hours, not bug-fix tickets.
- Spec-heavy work where the analyst → architect handoff is the
  job, not an afterthought.

## When ghyll is wrong

- CRUD endpoints, glue code, scaffolding.
- Database migrations from a known-good template.
- Rapid prototyping where you'll throw the result away.
- Anything where the cheapest path is "let the model output run
  and fix whatever's wrong." Use a fast agent.

ghyll's [`init`](docs/operator-guide.md) phase can refuse work
that doesn't fit. That refusal is a feature, not a bug.

---

## Status

Latest release: **[`v2026.30.247`](https://github.com/witlox/ghyll/releases/tag/v2026.30.247)**.

- Tier 0-4 of the prod-readiness roadmap shipped.
- All Critical, High, Medium, and Low adversarial findings closed.
- 345 BDD scenarios passing under `Strict: true`. Race-clean.
  Coverage 78.9%.
- 12 spec scenarios remain `@deferred`, all tracked as GitHub
  issues for the next feature increments (artifact dep-check,
  residue calculator, invalidated-status, vault `--config` flag).

See the [CHANGELOG](CHANGELOG.md) for release history, the
[operator guide](docs/operator-guide.md) for usage, and
[architecture flows](docs/architecture-flows.md) for the major
sequences.

---

## Quick start

The full walkthrough is in the
[operator guide](docs/operator-guide.md); the five steps are:

```bash
# 1. Install (release binary or from source)
make build-bin

# 2. First run auto-writes ~/.ghyll/config.toml and exits.
ghyll run

# 3. Edit endpoints to point at your SGLang instances.
$EDITOR ~/.ghyll/config.toml

# 4. Initialize the gate-and-arrow grid for a project.
cd ~/repos/myproject
ghyll init --op-id you@example.com

# 5. Start a session. Run /list-arrows to see what was generated;
#    /run-arrow <id> dispatches the first pass. The §11 adversarial
#    cycle is auto-enabled when a dialect endpoint resolves; toggle
#    it with /adversary {enable|disable|status}. Use
#    /drain-amendments to apply pending grid amendments (FIFO under
#    your /op-id) and /invalidate-arrow to retire a stale arrow.
ghyll run .
```

Run ghyll inside a sandbox; see the
[Sandbox required](#sandbox-required) section below.

---

## Sandbox required

> :warning: **ghyll executes tool calls from LLM output directly**
> — no confirmation, no permission checks, no filtering. This is
> by design.
>
> **You MUST run ghyll inside a sandbox.** Without one, a
> compromised model endpoint can execute arbitrary code with your
> user privileges.

ghyll's detection table (`cmd/ghyll/sandbox.go`):

| Platform | Sandbox | Notes |
|----------|---------|-------|
| macOS | [sandbox-exec](https://www.unix.com/man-page/osx/1/sandbox-exec/) | Native macOS sandboxing. |
| Linux | [bubblewrap](https://github.com/containers/bubblewrap) | Unprivileged user-namespace sandboxing. |
| Linux | [Firejail](https://firejail.wordpress.com/) | SUID-based sandboxing. |
| Any | Docker / Podman | Container isolation. |
| Any | Kubernetes pod | Pod-level isolation in a managed cluster. |

The operator guide's
[Sandbox setup](docs/operator-guide.md#sandbox-setup) section has
copy-pastable recipes for each.

---

## Continuity infrastructure

The gate-and-arrow runtime is the correctness mechanism. Dialects,
memory, and drift detection support continuity across sessions —
they don't catch shallow work, but they make sure shallow work
doesn't get repeated.

| Model | Active params | Context | Tier |
|-------|---------------|---------|------|
| MiniMax M2.5 | 10B / 230B | 1M tokens | Fast |
| GLM-5 | 40B / 744B | 200K tokens | Deep |
| DeepSeek | — | 128K tokens | Auxiliary |
| Qwen | — | 128K tokens | Auxiliary |

- Model-specific **dialects** — hand-tuned prompts, tool-call
  parsing, compaction per model. No provider-abstraction layer.
- Real-time **streaming** — tokens appear as they arrive; tool
  calls render inline.
- Drift-aware **memory** — cosine similarity drift detection with
  checkpoint backfill (continuity, not correctness).
- Tamper-evident **checkpoints** — Merkle DAG with ed25519
  signatures, synced via a `ghyll/memory` git orphan branch.
- Team **memory** — searchable checkpoints from all developers
  via the optional `ghyll-vault` server.

---

## Documentation

The mdbook lives at
**[witlox.github.io/ghyll](https://witlox.github.io/ghyll)**. The
canonical entry points are:

- [Operator guide](docs/operator-guide.md) — install, configure,
  run, attest, escalate.
- [Architecture flows](docs/architecture-flows.md) — sequence
  diagrams for the major flows (init, dispatch, verdict modal,
  amendment, recovery).
- [Architecture decisions](docs/decisions/) — 17 main ADRs +
  13 v2-pivot ADRs + 9 v4 ADRs for the design rationale.
- [Specs](specs/architecture/) — the design reference (current
  code, not aspirational).

---

## Development

```bash
make setup           # install tools + git hooks
make                 # lint + test + build
make test-race       # tests under the race detector
make coverage-check  # enforce 78% coverage
make bench           # engine + runner benchmarks (perf/baselines.md)
make test-live       # opt-in live-endpoint tests (build tag `live`)
make docs-serve      # preview mdbook docs locally
```

Releases are tagged automatically by
[`.github/workflows/release.yml`](.github/workflows/release.yml)
on a weekly schedule (or via `workflow_dispatch`). Versioning is
`v<year>.<sum-of-ADRs>.<commit-count>`; the ADR-sum counter is
bumped manually in the workflow when a new ADR lands.

## License

MIT.
