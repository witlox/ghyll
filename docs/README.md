# Ghyll Documentation

This book is the canonical reference for installing, operating,
and extending **ghyll**. The top-level
[README](https://github.com/witlox/ghyll/blob/main/README.md)
covers what ghyll is and why it's built the way it is; this book
covers everything else.

## How this book is organized

1. **[Why ghyll](why.md)** — the design rationale in depth. Five
   core decisions (fixed roles, typed clauses, gate-driven
   routing, sandbox-only execution, refusal as a feature), with
   the alternatives considered and the cross-links to the ADRs.
   Read this if you want to understand *why* before *how*.

2. **User Guide** — the operator path: install, configure, run.
   - [Getting Started](usage/getting-started.md)
   - [Configuration](usage/configuration.md)
   - [CLI Reference](usage/cli-reference.md)
   - [Operator Guide](operator-guide.md) — the deep end-to-end
     walkthrough, including the Tier 2 verdict modal, sandbox
     setup table, and vault deployment.
   - [Memory & Sync](usage/memory.md)
   - [Troubleshooting](usage/troubleshooting.md)

3. **Architecture** — how ghyll is built.
   - [System Design](architecture/design.md)
   - [Architecture Flows](architecture-flows.md) — sequence
     diagrams for init, dispatch, verdict modal, amendment, and
     recovery.
   - Subsystem deep-dives: package graph, session loop, routing,
     checkpoint format, sync protocol, vault API, error types.

4. **Internals** — the implementation details: dialect modules,
   context management, drift detection, injection detection,
   tool execution, the workflow system, and sub-agents.

5. **Architecture Decisions** — every architectural choice ghyll
   makes is recorded as an [ADR](decisions/). The list at the end
   of the SUMMARY is in order; ADR-008 onward documents the v2
   gate-and-arrow architecture.

## Where to start

- **New here?** Read the top-level
  [README](https://github.com/witlox/ghyll/blob/main/README.md)
  for the position and the why, then come back to [Getting
  Started](usage/getting-started.md).
- **Already running ghyll and want to deploy properly?** Jump to
  the [Operator Guide](operator-guide.md).
- **Curious about a specific design decision?** Check the
  [Decisions](decisions/) index — every choice has rationale,
  alternatives considered, and the date it landed.
- **Want to read the gate-and-arrow spec?** The canonical design
  reference is [`specs/architecture/`](https://github.com/witlox/ghyll/tree/main/specs/architecture)
  in the repository.

## Project state

ghyll is at a stable, releasable state — Tier 0-4 of the
prod-readiness roadmap shipped, all adversarial findings closed.
The latest release is documented in the
[CHANGELOG](https://github.com/witlox/ghyll/blob/main/CHANGELOG.md);
remaining feature work is tracked in
[GitHub Issues](https://github.com/witlox/ghyll/issues).
