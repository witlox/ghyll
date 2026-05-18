# specs/v2/features/

The **unified end-state BDD set** for ghyll. Each `.feature` file
describes behavior that ghyll exhibits (or will exhibit) when v2 is
complete. Each carries a comment-line marker after the `Feature:`
declaration:

- `# Implementation: v1` — currently implemented in shipping v1 code.
  v2 inherits the behavior; the same Go code (or a refactored
  successor) satisfies these scenarios.
- `# Implementation: v2 (not yet built)` — describes v2-only
  behavior. Currently aspirational; scenarios will become passing as
  v2 components land per `specs/direction/build-notes.md` build
  order.

When a v2 component is implemented and reaches parity, its
implementation marker flips from `v2 (not yet built)` to `v1+v2`
(or simply `v2` if it replaces v1 wholesale).

## The 16 features

### Inherited from v1 (10)

These describe behavior implemented by v1 today; v2 reuses the same
infrastructure or behavioral contracts.

| Feature | Notes |
|---|---|
| [memory.feature](memory.feature) | Merkle DAG checkpoint chain. v2's state-machine + amendment write to this same log per `cross-context.md`. |
| [keys.feature](keys.feature) | ed25519 device keys. Used for checkpoint signing in both v1 and v2. |
| [sync.feature](sync.feature) | Git orphan branch sync (`ghyll/memory` branch). Carried forward to v2. |
| [vault.feature](vault.feature) | Team memory vault HTTP service. Kept in v2. |
| [config.feature](config.feature) | TOML config loader. v2 extends with init-time bindings, depth ladder, severity threshold, N knobs (ADR-005, ADR-011). |
| [stream.feature](stream.feature) | SSE streaming to terminal. Kept in v2. |
| [tools.feature](tools.feature) | Direct OS tool calls. Kept in v2. |
| [edit.feature](edit.feature) | Edit tool. Kept in v2. |
| [glob.feature](glob.feature) | Glob tool. Kept in v2. |
| [web.feature](web.feature) | Web fetch / search tools. Kept in v2. |

### New in v2 — not yet built (6)

These describe v2-only behavior. Scenarios become passing as v2
components land.

| Feature | Build-notes step |
|---|---|
| [init.feature](init.feature) | Step 2 — project initialization, auto-propose + operator-confirm |
| [runner.feature](runner.feature) | Step 3 — machine-clause runner, enforcement spine |
| [state-machine.feature](state-machine.feature) | Step 3 (alongside runner) — clause/arrow/finding/pass lifecycles |
| [adversarial.feature](adversarial.feature) | Step 5 — per-arrow adversarial phase |
| [amendment.feature](amendment.feature) | Step 4-ish — grid amendment + global lock |
| [attestation.feature](attestation.feature) | Step 4-ish — operator attestation flow |

## Retired v1 features (not present here)

The following 7 v1 features describe v1-only mechanisms that
**will not exist in v2's end state**. They remain at `specs/features/`
(running against v1 code today) but are not part of the unified
end-state BDD set; they retire when v1 code retires:

- `compaction.feature` — v1's context-management mechanism. v2 uses
  init + grid + state-machine instead.
- `drift.feature` — v1's drift detection. v2's correctness mechanism
  is the gate system, not drift.
- `routing.feature` — v1's context-depth routing. v2 routes per
  `gates.md` §7 (depth-sensitivity-driven).
- `plan-mode.feature` — v1's plan-mode toggle on dialects. Replaced
  by v2's depth-sensitive routing + adversarial phase.
- `sub-agents.feature` — v1's sub-agent tool. v2 has internal
  fresh-adversary spawn but no operator-facing sub-agent tool.
- `resume.feature` — v1's session resume from checkpoint. v2's
  "resume" is pass re-traversal — different mechanism.
- `workflow.feature` — v1's `.ghyll/` workflow loader. Replaced by
  v2's hardcoded diamond + gate enforcement.

## Status

- The 10 v1-inherited features are **currently passing** (the v1
  acceptance suite at `tests/acceptance/` runs them against v1 code
  at `specs/features/`).
- The 6 v2-only features are **currently aspirational** (no step
  definitions yet; await v2 component implementations).
- The unified set will become a single passing acceptance suite
  when v2 reaches parity and v1 retires.

## Validation history

- **Initial extraction (3e60a55)** — 6 v2-only features (~103
  scenarios) extracted from component-design markdown.
- **Adversarial pass (10a776e)** — cold-context attack returned 18
  findings, verdict "not ready to wire." ~37 scenarios added
  across the 6 v2-only files to address weakness; ~10 existing
  strengthened. Findings recorded in
  [`validation-adversarial-pass.md`](validation-adversarial-pass.md).
- **v1 inheritance (this commit)** — 10 v1 features copied here,
  each annotated `# Implementation: v1`. v1-only features (7) stay
  at `specs/features/` and retire with v1.

Total now: 16 features. Scenarios across v2-only files: ~140
(includes ~18 from Scenario Outlines that expand at runtime).
v1-inherited scenarios: as-is from v1 (~40-50 across the 10 files).
Combined ~180–190 scenario declarations in the unified end-state
BDD set.

## Path layout (future)

When v2 reaches feature parity and v1 retires:

- `specs/features/` (v1 home) goes away. The 7 retiring features
  delete with v1.
- `specs/v2/features/` either stays where it is (no path churn) or
  promotes to `specs/features/` (cleanest long-term).
- This decision can be deferred until v1 retires — no consequence
  to deferring.
