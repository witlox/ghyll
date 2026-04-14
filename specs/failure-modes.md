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

### FM-15: Sub-agent infinite tool loop
**Scenario:** Sub-agent enters a cycle: calls tool A → gets result → calls tool A again with same args → repeats until turn limit.
**Detection:** Turn counter enforced (max 20 default). Identical consecutive tool calls detectable.
**Mitigation:** Hard turn limit terminates sub-agent with partial result. Parent model sees the partial result and can reason about the failure.
**Residual risk:** Sub-agent burns tokens on the fast tier before hitting the limit. Acceptable given M2.5 throughput.

### FM-16: Sub-agent model unreachable
**Scenario:** Parent on GLM-5 dispatches sub-agent to M2.5, but M2.5 endpoint is down.
**Detection:** Connection error on sub-agent's stream client.
**Mitigation:** Return error to parent as tool result ("sub-agent model unreachable"). Parent can: retry, do the task itself, or inform the user. No tier fallback for sub-agents — they target a specific model.

### FM-17: Web fetch blocked by Tarn indefinitely
**Scenario:** Model needs documentation from a URL. Tarn blocks it. Retries exhaust. User doesn't have Tarn access to approve.
**Detection:** Tool returns "domain not reachable" error after 3 retries.
**Mitigation:** Model surfaces the error to the user with the domain name. User can either approve in Tarn or provide the content manually. Session continues — web fetch failure is not fatal.

### FM-18: Instruction budget exceeded silently
**Scenario:** Project instructions + role overlay exceeds the token budget. Content is truncated. Critical instruction at the end is lost.
**Detection:** Token count check during workflow loading. Warning displayed to user.
**Mitigation:** Truncation warning includes the token count and budget. User can reduce instructions or increase budget in config.toml. Truncation is from the end of the combined content — project instructions (which take precedence) are loaded last, so global instructions are truncated first.
**Residual risk:** User may not notice the warning. Critical instructions may still be lost.

### FM-19: Workflow folder conflict resolution ambiguity
**Scenario:** Global and project instructions both define a directive, but phrased differently — not a clear "conflict." Both get included, model receives contradictory guidance.
**Detection:** Not automatically detectable — requires semantic understanding.
**Mitigation:** Document that project-level is authoritative. Users should make project instructions self-contained for critical directives rather than relying on override semantics.

### FM-20: Edit tool partial file corruption
**Scenario:** Edit tool finds the match, writes the replacement, but the write is interrupted (disk full, signal).
**Detection:** File write returns error. File may be partially written.
**Mitigation:** Edit tool writes to a temporary file first, then atomically renames. If rename fails, the original file is preserved. Temp file is cleaned up on failure.

### FM-21: Slash command injection via command file
**Scenario:** A malicious command file in `.ghyll/commands/` contains prompt injection patterns designed to override model behavior.
**Detection:** Injection signal detector scans all user messages, including injected command content.
**Mitigation:** Command files are checked into the repo — visible in code review. Injection signals are logged in checkpoint metadata. Tarn provides the hard boundary.
**Residual risk:** Command files from `.claude/` fallback may not have been reviewed for this project.

### FM-13: Vault serves poisoned checkpoint
**Scenario:** Compromised vault returns a checkpoint with valid structure but malicious summary.
**Detection:** ed25519 signature verification against known public keys. Unknown key → reject. Broken hash chain → reject.
**Mitigation:** Unverified checkpoints are never used for backfill. Warning displayed.
**Residual risk:** Same as FM-01 — compromised developer key allows signed poisoned checkpoints.
