# Checkpoint Format

Version 2. Forward-compatible: unknown fields are preserved but ignored.

## Version history

- **v1**: Initial format — core fields.
- **v2**: Added `plan_mode` (bool) and `resumed_from` (ResumeRef, optional). Old clients ignore these fields (forward-compatible). New clients reading v1 treat missing fields as zero values (plan_mode=false, resumed_from=nil).

## Canonical serialization

For hashing (invariant 4):
1. Take all fields except `hash` and `sig`
2. Serialize as JSON with keys sorted alphabetically
3. No whitespace, UTF-8 encoding
4. Hash = hex(sha256(serialized bytes))

```go
func CanonicalHash(c *Checkpoint) string {
    // Create copy without Hash and Signature
    // json.Marshal with sorted keys
    // sha256.Sum256(bytes)
    // return hex.EncodeToString(hash[:])
}
```

## Signing (invariant 2)

```
Signature = hex(ed25519.Sign(privateKey, []byte(Hash)))
```

The signature covers the hash, not the raw content. This means:
- Verification needs only the hash and public key
- The hash binds the signature to the content deterministically

## Verification

```go
func Verify(c *Checkpoint, pubkey ed25519.PublicKey) VerificationResult {
    // 1. Recompute hash from content
    // 2. Compare with c.Hash (detects content tampering)
    // 3. ed25519.Verify(pubkey, []byte(c.Hash), decodeHex(c.Signature))
}
```

## Chain verification (per-device)

Each device maintains its own chain. Chains are independent (invariant 1).

```go
func VerifyChain(checkpoints []Checkpoint) VerificationResult {
    // checkpoints must be ordered by position in chain
    // For each checkpoint after the first:
    //   checkpoint[i].ParentHash == checkpoint[i-1].Hash
    // First checkpoint: ParentHash == "0"*64 (zero hash)
}
```

Remote chains are verified at import time (sync). Partial imports work:
if local store has [c0, c1] and remote adds [c2, c3], verify that
c2.ParentHash == c1.Hash and c3.ParentHash == c2.Hash.

## Storage

### SQLite (local, ~/.ghyll/memory.db)

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
    plan_mode  INTEGER DEFAULT 0,   -- v2: 1 if plan mode was active
    resumed_from TEXT,              -- v2: JSON {"session":"...","hash":"..."}, nullable
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

Invariant 3: no UPDATE or DELETE statements on this table.

### Git memory branch (ghyll/memory)

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

Chain file (jsonl): one line per checkpoint, ordered. Used to verify
chain integrity without loading every individual checkpoint file.

```jsonl
{"hash":"abc...","parent":"000...","ts":1712345678}
{"hash":"def...","parent":"abc...","ts":1712345700}
```
