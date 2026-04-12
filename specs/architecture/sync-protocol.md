# Sync Protocol

Git-based memory sync via orphan branch. No custom network protocol.

## Branch initialization (invariant 12)

On first session, if `ghyll/memory` does not exist:

```
git checkout --orphan ghyll/memory
git rm -rf .
mkdir -p devices/ repos/<repo-hash>/checkpoints repos/<repo-hash>/chains
# write device public key
git add devices/<device-id>.pub
git commit -m "init: device <device-id>"
git push origin ghyll/memory
git checkout -   # return to previous branch
```

The orphan branch shares no history with any code branch.

## Worktree setup

ghyll uses a git worktree to avoid switching branches:

```
git worktree add --detach .ghyll-memory ghyll/memory
```

Location: `<repo>/.ghyll-memory/` (gitignored). All memory file operations
happen in this worktree.

## Checkpoint write (invariant 13: non-blocking)

On checkpoint creation:

```
1. Write <hash>.json to .ghyll-memory/repos/<repo-hash>/checkpoints/
2. Append chain entry to .ghyll-memory/repos/<repo-hash>/chains/<device-id>.jsonl
3. Background goroutine:
   cd .ghyll-memory
   git add .
   git commit -m "checkpoint <hash> by <device-id>"
   git push origin ghyll/memory
```

If push fails (conflict):
```
git pull --ff-only origin ghyll/memory
git push origin ghyll/memory
# Retry up to 3 times, then queue for next sync interval
```

Invariant 14 (idempotent): checkpoint filenames are content hashes.
Writing the same file twice produces the same content.

## Sync pull (session start + periodic)

```
1. git -C .ghyll-memory fetch origin ghyll/memory
2. git -C .ghyll-memory merge --ff-only origin/ghyll/memory
3. Scan for new checkpoint files not in local sqlite
4. For each new remote device chain:
   a. Load chains/<device-id>.jsonl
   b. Find first checkpoint not in local store
   c. Import from that point forward
   d. Verify chain integrity (each parent_hash matches previous hash)
   e. Verify signatures against devices/<device-id>.pub
   f. Insert into sqlite with verified=1 (or verified=0 if sig fails)
5. Import new device public keys from devices/
```

## Background sync loop

```go
// Runs in a goroutine, started at session init
func syncLoop(ctx context.Context, interval time.Duration) {
    // tick every interval (default 60s)
    // on tick: pull, then push any pending local checkpoints
    // on context cancel: final push attempt, then exit
}
```

## Conflict model

Append-only design means no merge conflicts:
- Each device writes its own checkpoint files (unique hashes)
- Each device appends to its own chain file
- No two devices write the same file
- `git pull --ff-only` always succeeds after a `git fetch`

The only conflict scenario is concurrent push, handled by pull-then-retry.

## Offline operation

When remote is unreachable:
- Checkpoints accumulate in local sqlite and worktree
- Chain file grows locally
- On next successful sync, all pending checkpoints push in one commit
- No data loss — sqlite is the source of truth, git is replication

## Shallow fetch for large repos (FM-08)

```
git fetch --depth=1 origin ghyll/memory
```

This fetches only the latest tree, not full history. Sufficient for
importing the current checkpoint set. Full history available via:

```
ghyll memory fetch --full
```
