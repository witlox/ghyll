# Workflow System

The workflow system loads project-specific instructions, roles, and slash commands to guide model behavior during a session.

## File Structure

```
~/.ghyll/                     # Global (user-level)
  instructions.md             # Behavioral instructions for all projects
  roles/                      # Role definitions
  commands/                   # Slash commands

<repo>/.ghyll/                # Project-level (overrides global)
  instructions.md             # Project-specific instructions
  roles/
    analyst.md                # Role: produce specs only
    implementer.md            # Role: write code within boundaries
  commands/
    review.md                 # /review command
    verify.md                 # /verify command
```

## Loading Order

1. Load global instructions from `~/.ghyll/instructions.md`
2. Load global roles and commands from `~/.ghyll/roles/` and `~/.ghyll/commands/`
3. Check for `<repo>/.ghyll/` --- if found, load project instructions, roles, commands (overriding global on name conflict)
4. If `.ghyll/` absent, try fallback folders (default: `.claude/`). Maps `CLAUDE.md` to instructions; `roles/` and `commands/` loaded identically.
5. If no workflow folder found, session starts with bare dialect prompt.

## System Prompt Composition

The system prompt is built from multiple layers:

```
[Dialect base prompt]           # M2.5 or GLM-5 base
[Global instructions]           # ~/.ghyll/instructions.md
[Project instructions]          # .ghyll/instructions.md (authoritative)
[Active role overlay]           # .ghyll/roles/<role>.md
[Plan mode overlay]             # Dialect-specific planning instructions
```

Total workflow content is bounded by the instruction budget (default 2,000 tokens). If exceeded, global instructions are dropped first; if project alone exceeds, it's truncated from the end.

## Roles

Roles are behavioral constraint sets. When activated, the role file content is appended to the system prompt. Only one role is active at a time --- switching roles replaces the overlay.

The model can switch roles automatically by reading the workflow router in project instructions, or the user can define role activation in slash commands.

Role switches do not create checkpoints or trigger compaction.

## Slash Commands

Each `.md` file in `commands/` becomes a `/<name>` command. When typed, the file content is injected as a user message. Built-in commands (`/deep`, `/fast`, `/plan`, `/status`, `/exit`) take precedence.

## Fallback to .claude/

When `.ghyll/` is absent, ghyll checks fallback folders (configurable, default: `.claude/`). The mapping:

| .claude/ path | Treated as |
|---------------|------------|
| `CLAUDE.md` | `instructions.md` (if no `instructions.md` exists) |
| `instructions.md` | `instructions.md` (takes precedence over `CLAUDE.md`) |
| `roles/` | `roles/` (identical) |
| `commands/` | `commands/` (identical) |
