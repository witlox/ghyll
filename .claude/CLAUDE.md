# Workflow Router

Correctness over velocity. Every shortcut becomes debt.

Role definitions in `.claude/roles/`. Read the relevant role file when
activating a mode. These are behavioral constraints.

## Role → files to load

| Role | Load these files |
|------|-----------------|
| analyst | `roles/analyst.md` |
| architect | `roles/architect.md`, `coding/go.md` |
| adversary | `roles/adversary.md`, `coding/go.md` |
| implementer | `roles/implementer.md`, `guidelines/engineering.md`, `guidelines/go.md`, `coding/go.md` |
| auditor | `roles/auditor.md` |
| integrator | `roles/integrator.md`, `guidelines/ci.md` |

Standards: `.claude/guidelines/`. Project conventions: `.claude/coding/`.

## Pre-commit

Three test tiers, cascading. Each higher tier includes the lower:

| Tier | What | Make target | When |
|---|---|---|---|
| **1 (fast — default)** | unit tests, excludes acceptance suite | `make test-unit` | between every edit; pre-commit |
| **2 (slow)** | tier 1 + acceptance (godog) + race detector + coverage threshold | `make test && make test-race && make coverage-check` | pre-PR |
| **3 (full)** | tier 2 + live-endpoint integration (when implemented) | TBD | pre-merge / nightly |

`make` (no target) = lint + test + build. Run before every commit.
Lefthook enforces fmt, lint, test, vet on commit.

## Automatic commands

| Command | When |
|---|---|
| `/status` | First message of every new session |
| `/verify` | Before every commit |
| `/spec-check` | After completing a build phase or spec change |

## Mode detection

### Step 1: Project state

1. `specs/fidelity/INDEX.md` with checkpoint? → Baselined
2. `specs/fidelity/SWEEP.md` IN PROGRESS? → Resume sweep
3. Source code exists and tested? → Brownfield with baseline
4. Near-empty? → Pure greenfield

Current project state lives in the root `CLAUDE.md`.

### Step 2: User intent → role

| Intent | Mode | Role |
|--------|------|------|
| status | ASSESS | Read indexes |
| sweep / baseline | SWEEP | auditor |
| adversary sweep / security review | ADV-SWEEP | adversary |
| audit [X] | AUDIT | auditor |
| implement / add | FEATURE | implementer |
| fix / bug / error | BUGFIX | implementer |
| design / spec | DESIGN | analyst or architect |
| review / find flaws | REVIEW | adversary |
| integrate | INTEGRATE | integrator |
| continue / next | RESUME | Read sweep state |
| Unclear | ASK | |

### Step 3: Before acting, one line

```
Mode: [MODE]. Project: [state]. Role: [role]. Reason: [why].
```

## Role switching

On switch: `Switching to [role]. Previous: [role].`
Read `.claude/roles/[role].md` plus its declared standards files.
Apply their constraints.

## Protocols

**Feature**: analyst → spec | architect → interfaces | adversary → gate 1 |
implementer → BDD + code | auditor → gate 2 | adversary → findings |
integrator (if cross-feature). Done = scenarios pass + fidelity HIGH +
adversary signed off.

**Bugfix**: diagnose → failing test first → fix → audit depth → update index.

**Design**: new domain → analyst | arch change → architect | ADR → write it.
Adversary reviews before implementation.

**Sweep**: fidelity (auditor) and adversary in parallel. LOW areas get
higher adversary priority.

## Escalation paths

- Implementer → Architect (interface) or Analyst (spec)
- Adversary → Architect (structural) or Analyst (gap)
- Auditor → Implementer (shallow tests) or Architect (contract divergence)
- Integrator → Architect (cross-cutting)

All escalations go to `specs/escalations/`.
