# ADR-008: V2 Fixed Roles Deprecate Runtime Workflow Roles

Date: May 2026
Status: Accepted

## Context

v1 ghyll loads workflow roles from disk at session start via
`workflow.Load(globalDir, sc.Workdir, sc.Cfg.Workflow.FallbackFolders)`.
Operators define their roles as freeform markdown in `.claude/roles/*.md`
or `.ghyll/roles/*.md`; the session caches them in `Session.wf.Roles`
and `Session.SwitchRole(name)` lets the runtime switch between them.

v2's correctness mechanism (`specs/direction/direction.md` §3.3) is the
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

**Note on tense**: this ADR is accepted now; the code changes it
describes execute during Phase D-1 of v2-final consolidation (see
`specs/v2-final-plan.md`). Wording is in the future tense for changes
that are not yet committed.

### 1. Drop runtime free-form role loading

At Phase D-1 commit:

- `workflow.Load()` will no longer read `.claude/roles/*.md` or
  `.ghyll/roles/*.md`.
- `Session.wf.Roles` will be removed from the struct.
- `Session.activeRole` will be removed.
- `Session.SwitchRole()` will be removed.

These surfaces still exist today in `cmd/ghyll/session.go` lines 55,
960-961, 1015-1024 and `workflow/workflow.go` — they remain functional
until Phase D-1 ships.

### 2. Embed the four v2 roles as Go data

The four diamond roles will ship as compiled-in role specs (entry
precondition, exit gate, prompt content). The operator cannot redefine
them at runtime. Per `specs/direction/roles/*.md`, each role is
reconciled to `gates.md` and the embedded form will mirror those specs.

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

Per `specs/direction/direction.md` §3.5, every arrow carrying a
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
- specs/direction/direction.md §3.3 — fixed roles
- specs/direction/roles/*.md — per-role contracts
- specs/v2-final-plan.md — Phase D-1 of v2-final consolidation
