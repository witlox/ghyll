# Workflow System

The workflow system loads project-specific **instructions** and
**slash commands** to guide model behavior during a session. It
does NOT load runtime roles — per
[ADR-008](../decisions/008-v2-fixed-roles-deprecate-runtime-workflow-roles.md),
the four diamond roles (analyst / architect / implementer /
integrator) are contracts embedded into the binary at build time
via `//go:embed specs/architecture/roles/*.md`, not files the
operator drops into a directory.

If you put a `roles/` subdirectory under `.ghyll/` or `.claude/`,
the loader **ignores it intentionally** (see `workflow/loader.go`).
The files there are reserved for an editing assistant's working
roles — Claude Code reads `.claude/roles/*.md` when editing this
repo — and have no effect on ghyll's runtime.

## File Structure

```
~/.ghyll/                     # Global (user-level)
  instructions.md             # Behavioral instructions for all projects
  commands/                   # Slash commands

<repo>/.ghyll/                # Project-level (overrides global)
  instructions.md             # Project-specific instructions
  commands/
    review.md                 # /review command
    verify.md                 # /verify command
```

## Loading Order

1. Load global instructions from `~/.ghyll/instructions.md`.
2. Load global commands from `~/.ghyll/commands/`.
3. Check for `<repo>/.ghyll/` --- if found, load project
   instructions and commands (overriding global on name
   conflict).
4. If `.ghyll/` absent, try fallback folders (default: `.claude/`).
   Maps `CLAUDE.md` to instructions; `commands/` loaded
   identically.
5. If no workflow folder found, the session starts with the bare
   dialect prompt.

## System Prompt Composition

The system prompt is built from a small fixed set of layers:

```
[Dialect base prompt]           # MiniMax M2.5 / GLM-5 / DeepSeek / Qwen base
[Global instructions]           # ~/.ghyll/instructions.md
[Project instructions]          # .ghyll/instructions.md (authoritative)
[Plan mode overlay]             # Dialect-specific planning instructions
```

There is no role-overlay layer. The runtime knows which role
owns the current arrow's source and target (because the arrow
declares them), but no per-role system-prompt overlay is
applied — the role contracts in
[`specs/architecture/roles/`](https://github.com/witlox/ghyll/tree/main/specs/architecture/roles)
are consumed by `bootstrap.ParseRoleFileEmbedded` during
`ghyll init`, not injected into the model's prompt.

Total workflow content is bounded by the instruction budget
(default 2,000 tokens). If exceeded, global instructions are
dropped first; if project alone exceeds, it's truncated from
the end.

## Slash Commands

Each `.md` file in `commands/` becomes a `/<name>` command. When
typed, the file content is injected as a user message. Built-in
commands (`/deep`, `/fast`, `/plan`, `/status`, `/exit`,
`/list-arrows`, `/run-arrow`, `/op-id`, `/attest`, `/attestations`,
`/passes`, `/quit`) take precedence over any same-named
user-defined command.

## Fallback to .claude/

When `.ghyll/` is absent, ghyll checks fallback folders
(configurable, default: `.claude/`). The mapping:

| .claude/ path     | Treated as                                          |
|-------------------|-----------------------------------------------------|
| `CLAUDE.md`       | `instructions.md` (if no `instructions.md` exists)  |
| `instructions.md` | `instructions.md` (takes precedence over `CLAUDE.md`) |
| `commands/`       | `commands/` (identical)                             |
| `roles/`          | **Intentionally ignored** (per ADR-008)             |

## See also

- [Why ghyll — Roles are fixed](../why.md#1-roles-are-fixed-the-diamond) — the design rationale.
- [ADR-008](../decisions/008-v2-fixed-roles-deprecate-runtime-workflow-roles.md) — the decision to deprecate runtime workflow roles.
- [Glossary — Role](../glossary.md#role) — what the four diamond roles are and how they relate to arrows.
