# ADR-006: One Session Per Repository

Date: April 2026
Status: Accepted

## Context

Multiple concurrent ghyll sessions in the same repository would cause:
- Concurrent writes to the git memory branch worktree
- Concurrent appends to the same chain file
- Double sync goroutines competing on git push
- Potential sqlite WAL corruption from multiple processes

This was identified during adversary review of the architecture.

## Decision

Enforce one ghyll session per repository using a lockfile at `<repo>/.ghyll.lock`. The lockfile contains the PID of the holding process. Stale locks (dead PID) are automatically reclaimed.

The lockfile uses `O_CREATE|O_EXCL` for atomic creation to prevent TOCTOU race conditions.

## Consequences

- Cannot run two ghyll sessions in the same repo simultaneously
- A crashed session's lock is automatically recovered (dead PID detection)
- The lockfile is gitignored
- Different repos can run concurrent sessions (different lockfiles)
- This is the simplest solution; per-session worktrees or channel-based serialization were considered but add complexity for a scenario (concurrent sessions in same repo) that's unlikely in practice
