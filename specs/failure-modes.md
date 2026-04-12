# Failure Modes

## Critical

### FM-01: Memory poisoning via compromised checkpoint
**Scenario:** A compromised ghyll instance pushes a checkpoint with a falsified summary designed to steer future sessions toward insecure actions.
**Detection:** Hash chain verification + ed25519 signature check. If the attacker doesn't have the developer's private key, the signature won't verify.
**Mitigation:** Reject unverified checkpoints during backfill. Display warning for broken chains. Never auto-apply unverified team memory.
**Residual risk:** If a developer's private key is compromised, signed poisoned checkpoints would pass verification.

### FM-02: Model endpoint returns malicious tool calls
**Scenario:** A compromised or misconfigured SGLang endpoint returns tool calls designed to exfiltrate data or modify critical files.
**Detection:** Injection signal detector catches some patterns at checkpoint time.
**Mitigation:** Tarn's Endpoint Security layer blocks access outside sandbox boundary regardless of what ghyll executes.
**Residual risk:** Actions within the workspace (e.g., inserting backdoors into code) are not blocked by Tarn.

## High

### FM-03: Drift detection fails silently when ONNX unavailable
**Scenario:** Embedding model not downloaded, drift detection disabled, conversation drifts badly without correction.
**Mitigation:** Display persistent warning in terminal when drift detection is disabled. Prompt to download on first session.

### FM-04: Context compaction loses critical information
**Scenario:** Compaction summarizes away a key decision or constraint, model makes contradictory choices in subsequent turns.
**Mitigation:** Compaction preserves last N turns unchanged. Checkpoint summaries retain structured decisions. Backfill can recover from checkpoints if drift is detected.

### FM-05: Git sync race corrupts memory branch
**Scenario:** Two developers push simultaneously, one push rejected, retry loop fails.
**Mitigation:** Append-only design means fast-forward merges always work. Retry with exponential backoff. After 3 failures, queue for next sync interval.

## Medium

### FM-06: Model handoff produces incoherent context
**Scenario:** Checkpoint summary doesn't capture enough context for the new model to continue effectively.
**Mitigation:** Handoff summary includes: original task, key decisions, current file state, last 3 turns verbatim. Developer sees switch indicator and can provide additional context.

### FM-07: Embedding model drift from code semantics
**Scenario:** The small ONNX model (GTE-micro) doesn't capture code-specific semantics well enough, causing false drift positives or missed drift.
**Mitigation:** Make embedding model configurable and updatable via `ghyll update-embedder`. Allow threshold tuning per-project in config.toml.

### FM-08: Orphan branch accumulates unbounded
**Scenario:** After a year, ghyll/memory has 50K+ checkpoint files, slowing git operations.
**Mitigation:** Shallow fetch by default. Periodic archival: compress old checkpoints into summary checkpoints, prune originals. `ghyll memory archive` command.

## Low

### FM-09: TOML config malformed
**Scenario:** Developer edits config.toml manually, introduces syntax error.
**Mitigation:** Validate on load, fall back to defaults with warning.

### FM-10: SGLang endpoint unreachable
**Scenario:** Network issue between developer machine and inference cluster.
**Mitigation:** Connection timeout (5s), retry with exponential backoff (1s, 2s, 4s), tier fallback to alternate model. If both tiers unreachable, session stays open for manual retry.

### FM-11: Stream interrupted mid-response
**Scenario:** Connection drops while model is streaming a response.
**Detection:** Stream client detects EOF or connection reset before receiving stop token.
**Mitigation:** Partial content preserved and surfaced to user. User can retry with same context. No checkpoint created for incomplete turn.

### FM-12: Vault unreachable during backfill
**Scenario:** Drift triggers backfill, local checkpoints insufficient, vault is down.
**Mitigation:** Vault request times out after 5s. Fall back to local git-synced checkpoints only. Log warning. Session continues with reduced team memory.
**Residual risk:** If local checkpoints are also insufficient, drift correction is incomplete.

### FM-14: Private key lost or machine replaced
**Scenario:** Developer gets a new machine, old key pair is gone. Old checkpoints are signed with the old key.
**Mitigation:** Generate new key pair on new machine. Old checkpoints remain verifiable via old public key still on memory branch. New checkpoints use new key. No key rotation protocol needed — old and new keys coexist as different device IDs.

### FM-13: Vault serves poisoned checkpoint
**Scenario:** Compromised vault returns a checkpoint with valid structure but malicious summary.
**Detection:** ed25519 signature verification against known public keys. Unknown key → reject. Broken hash chain → reject.
**Mitigation:** Unverified checkpoints are never used for backfill. Warning displayed.
**Residual risk:** Same as FM-01 — compromised developer key allows signed poisoned checkpoints.
