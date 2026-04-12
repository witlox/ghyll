# Memory & Sync

ghyll maintains a tamper-evident checkpoint chain for conversation memory, synced via a git orphan branch.

## Checkpoints

Every N turns (configurable), ghyll creates a checkpoint containing:

- Structured summary of recent work
- Vector embedding for similarity search
- Files touched and tools used
- Hash chain link to previous checkpoint
- Ed25519 signature from the device key

Checkpoints are append-only and never modified after creation.

## Hash Chain

Each checkpoint's hash covers all fields except the hash and signature themselves. The parent hash links to the previous checkpoint, forming a Merkle DAG. This makes tampering detectable --- modifying any checkpoint breaks all subsequent hashes.

## Signing

Checkpoints are signed with the device's ed25519 private key (`~/.ghyll/keys/<device-id>.key`). Keys are generated automatically on first run.

Public keys are distributed via the memory branch at `devices/<device-id>.pub`.

## Git Sync

Checkpoints sync via a git orphan branch (`ghyll/memory`) in the project repo:

- **Push**: after each checkpoint, committed and pushed in the background
- **Pull**: on session start, fetches remote checkpoints from other devices
- **Conflict-free**: append-only design means fast-forward merges always work
- **Offline**: checkpoints accumulate locally and push when connectivity returns

The orphan branch shares no history with code branches.

## Team Memory

When multiple developers work on the same repo, their checkpoints are visible to each other. Drift detection can backfill from team checkpoints when relevant.

For faster search across repos, use the optional vault server (`ghyll-vault`).

## Drift Detection

Every N turns, ghyll measures cosine similarity between the current context embedding and the most recent checkpoint. If similarity drops below the threshold (default 0.7), backfill is triggered --- injecting relevant checkpoint summaries into the context.

Requires the ONNX embedding model (`make embedder`). Without it, drift detection is disabled gracefully.

## Commands

```bash
ghyll memory log                          # show checkpoint chain
ghyll memory search "race condition"      # search summaries
```
