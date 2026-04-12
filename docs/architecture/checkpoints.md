# Checkpoint Format

Checkpoints are the fundamental unit of ghyll's memory system. Each checkpoint captures a snapshot of session state at a point in time, forming an append-only, tamper-evident chain secured by cryptographic hashing and ed25519 signatures.

This document describes checkpoint format version 1. The format is forward-compatible: unknown fields are preserved but ignored.

## Checkpoint Structure

```go
type Checkpoint struct {
    Version      int       `json:"v"`
    Hash         string    `json:"hash"`          // hex(sha256(canonical content))
    ParentHash   string    `json:"parent"`        // previous checkpoint hash, or "0"*64
    DeviceID     string    `json:"device"`
    AuthorID     string    `json:"author"`
    Timestamp    int64     `json:"ts"`            // unix nanos
    RepoRemote   string    `json:"repo"`          // git remote URL
    Branch       string    `json:"branch"`        // git branch at time of checkpoint
    SessionID    string    `json:"session"`       // unique per ghyll invocation
    Turn         int       `json:"turn"`
    ActiveModel  string    `json:"model"`         // "m25" or "glm5"
    Summary      string    `json:"summary"`       // structured natural language
    Embedding    []float32 `json:"emb"`           // vector from ONNX model
    FilesTouched []string  `json:"files"`
    ToolsUsed    []string  `json:"tools"`
    InjectionSig []string  `json:"injections,omitempty"`
    Signature    string    `json:"sig"`           // hex(ed25519.Sign(privkey, hash))
}
```

## Canonical Serialization

To compute the hash of a checkpoint:

1. Take all fields except `hash` and `sig`.
2. Serialize as JSON with keys sorted alphabetically.
3. No whitespace, UTF-8 encoding.
4. Hash = `hex(sha256(serialized bytes))`.

This deterministic serialization ensures that any two implementations produce the same hash for the same checkpoint content.

## Signing

The signature covers the hash string, not the raw content:

```
Signature = hex(ed25519.Sign(privateKey, []byte(Hash)))
```

This means verification needs only the hash and public key. The hash binds the signature to the content deterministically.

## Verification

Verification is a two-step process:

1. **Hash verification** -- recompute the canonical hash from the checkpoint content and compare it with the stored hash. A mismatch indicates content tampering.
2. **Signature verification** -- verify the ed25519 signature against the device's public key.

## Chain Verification

Each device maintains its own chain of checkpoints. Chains are independent across devices.

To verify a chain:

- Checkpoints must be ordered by position in the chain.
- For each checkpoint after the first: `checkpoint[i].ParentHash` must equal `checkpoint[i-1].Hash`.
- The first checkpoint in a chain has `ParentHash` set to `"0"*64` (64 zero characters).

Remote chains are verified at import time during sync. Partial imports work correctly: if the local store has checkpoints `[c0, c1]` and a remote device adds `[c2, c3]`, verification confirms that `c2.ParentHash == c1.Hash` and `c3.ParentHash == c2.Hash`.

## Local Storage (SQLite)

Checkpoints are stored locally in `~/.ghyll/memory.db`:

```sql
CREATE TABLE checkpoints (
    hash       TEXT PRIMARY KEY,
    parent     TEXT NOT NULL,
    device     TEXT NOT NULL,
    author     TEXT NOT NULL,
    ts         INTEGER NOT NULL,
    repo       TEXT NOT NULL,
    branch     TEXT NOT NULL,
    session    TEXT NOT NULL,
    turn       INTEGER NOT NULL,
    model      TEXT NOT NULL,
    summary    TEXT NOT NULL,
    embedding  BLOB NOT NULL,       -- float32 array, binary
    files      TEXT NOT NULL,        -- JSON array
    tools      TEXT NOT NULL,        -- JSON array
    injections TEXT,                 -- JSON array, nullable
    sig        TEXT NOT NULL,
    verified   INTEGER DEFAULT 1,   -- 0 = unverified remote
    imported   INTEGER DEFAULT 0    -- unix timestamp of import
);

CREATE INDEX idx_checkpoints_session ON checkpoints(session);
CREATE INDEX idx_checkpoints_device ON checkpoints(device);
CREATE INDEX idx_checkpoints_repo ON checkpoints(repo);
```

The checkpoint table is append-only: no UPDATE or DELETE statements are ever issued against it.

## Git Memory Branch

Checkpoints are also stored on a git orphan branch (`ghyll/memory`) for team synchronization:

```
ghyll/memory (orphan branch)
  devices/
    <device-id>.pub              ed25519 public key
  repos/
    <sha256(git-remote-url)>/
      checkpoints/
        <checkpoint-hash>.json   individual checkpoint files
      chains/
        <device-id>.jsonl        ordered hash chain per device
```

The chain file (JSONL format) contains one line per checkpoint, ordered chronologically. It is used to verify chain integrity without loading every individual checkpoint file:

```jsonl
{"hash":"abc...","parent":"000...","ts":1712345678}
{"hash":"def...","parent":"abc...","ts":1712345700}
```

See [Sync Protocol](sync.md) for details on how checkpoints are synchronized across devices.
