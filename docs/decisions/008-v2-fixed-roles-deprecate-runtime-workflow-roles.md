# ADR-008: V2 Fixed Roles Deprecate Runtime Workflow Roles

Date: May 2026
Status: Accepted

## Context

v1 ghyll loads workflow roles from disk at session start via
`workflow.Load(globalDir, sc.Workdir, sc.Cfg.Workflow.FallbackFolders)`.
Operators define their roles as freeform markdown in `.claude/roles/*.md`
or `.ghyll/roles/*.md`; the session caches them in `Session.wf.Roles`
and `Session.SwitchRole(name)` lets the runtime switch between them.

v2's correctness mechanism (`specs/architecture/v2-design.md` §3.3) is the
gate system, and the gate system requires roles to have fixed
contracts:

> A role is not freely redefinable — a role you can argue with is a gate
> you can argue with. Roles may be **extended** with language-specific
> or behavioral additions, but their contracts (entry precondition,
> exit gate) are fixed.

The v2 role set is the "diamond":

```
analyst → architect → implementer → integrator
```

with adversarial scrutiny and depth classification reframed as
**phases of every arrow** rather than standalone roles.

The two surfaces are now incompatible:

1. v1 runtime role loading lets an operator redefine the analyst role
   to skip the architect's entry preconditions — the gate the
   architect arrow depends on becomes negotiable.
2. v1 has no notion of arrow phases. The auditor/adversary roles from
   `.claude/roles/` have no v2 counterpart.

## Decision

### 1. Drop runtime free-form role loading

 removed:

- The `Roles` field from `workflow.Workflow`; `workflow.Load()` no
  longer reads `.claude/roles/*.md` or `.ghyll/roles/*.md`.
- `Session.wf.Roles`, `Session.activeRole`, and `Session.SwitchRole()`
  from `cmd/ghyll/session.go`.
- The role-overlay branch in `composedSystemPrompt`.

### 2. Four fixed roles, defined in specs as source-of-truth

The four diamond role contracts live at
`specs/architecture/roles/{analyst,architect,implementer,integrator}.md`
and are the canonical definition. The runtime does not load these from
disk for system-prompt construction — v2's correctness mechanism is the
gate-and-arrow state machine, not a swappable system-prompt overlay.

Bootstrap's auto-propose pass (`bootstrap.ParseRoleFile`) does read
these markdowns to extract role clauses for grid construction, but that
is a build-time/init concern: the parsed output flows into the typed
grid (`.ghyll/grid.vN.yaml`), not into a runtime overlay. Operators
cannot redefine the role contracts; the spec files are the contracts.

### 3. Workflow.Load() retains the other v1 responsibilities

`workflow.Load()` continues to:

- Load **project instructions** (the body of `.claude/CLAUDE.md` and
  `.ghyll/CLAUDE.md`). These compose into the system prompt per the
  invariants in `specs/architecture/data-structures.md` lines 359-365:
  - 46: instructions survive compaction (system-level)
  - 47: global prepended, project appended (project has last word)
  - 48: total tokens bounded by instruction budget
- Load **slash commands** (user-defined extensions like `/ultrareview`
  in `.claude/commands/*.md`). These remain operator-controllable.

What goes away: the `Roles` map on the returned `*Workflow`.

### 4. Adversarial and auditor functions reframe as arrow phases

Per `specs/architecture/v2-design.md` §3.5, every arrow carrying a
depth-sensitive clause runs three phases:

1. Adversarial — separate instance attacks the upstream artifact.
2. Remediation — bounded loop fixing or accepting-risk findings.
3. Verification — gate clauses evaluated.

These are not roles. There is no standalone adversary or auditor in
v2.

## Consequences

### Breaking changes

- `Session.SwitchRole()` removed.
- `cfg.Workflow.FallbackFolders` no longer used for role discovery
  (still used for CLAUDE.md discovery).
- The `Workflow.Roles` field is removed.
- Operators who relied on custom roles in `.claude/roles/*.md` must
  either:
  - Express their domain rules as arrow gates (the gate becomes the
    enforcement, not the role description).
  - Use slash commands for ad-hoc workflows that don't need gate
    enforcement.
  - Stay on v1 (no longer supported).

### Test impact

- `workflow.feature`: ~10 of 28 scenarios drop (role loading, role
  switching, role inheritance). Remaining scenarios (instruction
  budget, CLAUDE.md composition, slash commands) retain coverage.
- `sub-agents.feature`: per ADR-009 (forthcoming), sub-agents narrow
  to a tool call rather than an inherited session topology.
- `plan-mode.feature`: unaffected — plan mode is advisory and orthogonal
  to the role system.

### Migration path for operators

A v1→v2 migration note in the docs/usage/ section explains:

- If your `.claude/roles/X.md` encoded domain knowledge, that becomes
  arrow clauses in your project's grid.
- If your `.claude/roles/X.md` encoded prompts, that becomes a slash
  command.
- If your `.claude/roles/X.md` encoded skip-checks, the only way is the
  gate clause approach — there is no equivalent for arguing past a
  gate.

### Compatibility

`.claude/CLAUDE.md` is unchanged in shape (project instructions still
load). The v2 fixed roles are NOT loaded from `.claude/roles/*.md` —
those files become Claude Code's own role definitions (used by the
build agents, not by ghyll's runtime).

## Alternatives considered

1. **Keep both layers.** Rejected: the gate system loses its
   enforcement guarantee the moment a role can argue with it.
2. **Keep role loading as extensions only.** Considered: operators
   add roles BEYOND the four fixed ones, but cannot redefine them.
   Rejected: the diamond is closed by design; opening it at the
   role layer means the next role's gate is no longer compositional.
3. **Defer to a future release.** Rejected: every release that ships
   with both surfaces compounds the v1↔v2 split this consolidation
   exists to close.

## Related decisions

- ADR-007 — tier-based routing (orthogonal; still applies)
- specs/architecture/v2-design.md §3.3 — fixed roles
- specs/architecture/roles/*.md — per-role contracts (source of truth)
- specs/v2-final-plan.md — the consolidation plan that executed this decision
