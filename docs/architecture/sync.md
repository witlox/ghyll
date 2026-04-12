# Git Sync Protocol

Ghyll synchronizes memory checkpoints between team members using a git orphan branch. There is no custom network protocol -- all synchronization flows through the existing git remote.

## Branch Initialization

On the first session in a repository, if the `ghyll/memory` branch does not exist, ghyll creates it:

```
git checkout --orphan ghyll/memory
git rm -rf .
mkdir -p devices/ repos/<repo-hash>/checkpoints repos/<repo-hash>/chains
git add devices/<device-id>.pub
git commit -m "init: device <device-id>"
git push origin ghyll/memory
git checkout -
```

The orphan branch shares no history with any code branch. It exists purely for memory storage.

## Worktree Setup

To avoid switching branches in the working repository, ghyll uses a git worktree:

```
git worktree add --detach .ghyll-memory ghyll/memory
```

The worktree lives at `<repo>/.ghyll-memory/` (gitignored). All memory file operations happen in this worktree, so the developer's working branch is never disturbed.

## Writing Checkpoints

When a checkpoint is created during a session:

1. Write `<hash>.json` to `.ghyll-memory/repos/<repo-hash>/checkpoints/`.
2. Append a chain entry to `.ghyll-memory/repos/<repo-hash>/chains/<device-id>.jsonl`.
3. A background goroutine commits and pushes:
   ```
   cd .ghyll-memory
   git add .
   git commit -m "checkpoint <hash> by <device-id>"
   git push origin ghyll/memory
   ```

Checkpoint writes are non-blocking. The session continues immediately after steps 1 and 2; the git commit and push happen in the background.

If the push fails due to a conflict:
```
git pull --ff-only origin ghyll/memory
git push origin ghyll/memory
```
This retries up to 3 times. After 3 failures, the checkpoint is queued for the next sync interval.

Because checkpoint filenames are content hashes, writing the same checkpoint twice produces identical content, making the operation idempotent.

## Pulling Remote Checkpoints

At session start and periodically during the session, ghyll pulls remote checkpoints:

1. Fetch the latest state: `git -C .ghyll-memory fetch origin ghyll/memory`
2. Fast-forward merge: `git -C .ghyll-memory merge --ff-only origin/ghyll/memory`
3. Scan for new checkpoint files not already in the local SQLite store.
4. For each new remote device chain:
   - Load `chains/<device-id>.jsonl`.
   - Find the first checkpoint not in the local store.
   - Import from that point forward.
   - Verify chain integrity (each `parent_hash` matches the previous checkpoint's hash).
   - Verify signatures against `devices/<device-id>.pub`.
   - Insert into SQLite with `verified=1` (or `verified=0` if signature verification fails).
5. Import any new device public keys from `devices/`.

## Background Sync Loop

A goroutine runs throughout the session, ticking at a configurable interval (default 60 seconds). On each tick it pulls remote changes, then pushes any pending local checkpoints. When the session ends, it makes a final blocking push attempt before exiting.

## Conflict Model

The append-only design means merge conflicts do not occur in practice:

- Each device writes its own checkpoint files (unique content hashes).
- Each device appends to its own chain file.
- No two devices write the same file.
- `git pull --ff-only` always succeeds after a `git fetch`.

The only conflict scenario is concurrent pushes from two devices, which is handled by the pull-then-retry mechanism.

## Offline Operation

When the git remote is unreachable:

- Checkpoints accumulate in the local SQLite store and worktree.
- The chain file grows locally.
- On the next successful sync, all pending checkpoints push in a single commit.
- No data is lost. SQLite is the source of truth; git is the replication layer.

## Shallow Fetch for Large Repositories

For repositories with extensive checkpoint history, ghyll supports shallow fetching:

```
git fetch --depth=1 origin ghyll/memory
```

This fetches only the latest tree without full history, which is sufficient for importing the current checkpoint set. Full history can be retrieved when needed:

```
ghyll memory fetch --full
```
