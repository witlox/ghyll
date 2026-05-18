# Vault API

HTTP API served by ghyll-vault. Optional team memory search service.

## Authentication (invariants 25, 26)

- **Remote vault**: Bearer token in `Authorization` header. Token from config.toml.
  Missing or invalid token → 401 Unauthorized.
- **Localhost** (127.0.0.1, ::1): No auth required. Token check skipped.

## Endpoints

### POST /v1/search

Search for checkpoints by embedding similarity.

**Request:**
```json
{
  "embedding": [0.1, 0.2, ...],  // float32 array, dimensions must match
  "repo": "sha256-of-remote-url",
  "top_k": 5
}
```

**Response (200):**
```json
{
  "results": [
    {
      "checkpoint": { /* full checkpoint object */ },
      "similarity": 0.87
    }
  ]
}
```

Results sorted by similarity descending. Filtered by repo hash.

**Errors:**
- 400: missing/malformed fields
- 401: missing or invalid token (remote only)
- 500: internal error

### POST /v1/checkpoints

Push a checkpoint to the vault.

**Request:**
```json
{
  "checkpoint": { /* full checkpoint object with hash and sig */ }
}
```

**Response (201):** Stored successfully.

**Validation:**
1. Verify checkpoint signature against known public keys
2. Verify hash matches content (recompute canonical hash)
3. If valid → store
4. If invalid → 403 Forbidden with reason

**Errors:**
- 400: malformed checkpoint
- 401: missing or invalid token (remote only)
- 403: signature verification failed
- 409: checkpoint with this hash already exists (idempotent, not an error in practice)
- 500: internal error

### GET /v1/health

Health check.

**Response (200):**
```json
{
  "status": "ok",
  "checkpoints": 12345,
  "devices": 7
}
```

## Storage

Vault uses the same sqlite schema as the CLI (see checkpoint-format.md).
Embedding similarity search uses brute-force cosine similarity on the
embedding column. At expected scale (<100K checkpoints), this is
sufficient. Index if needed later.

## Client behavior (memory/vault_client.go)

```go
// VaultClient handles HTTP communication with ghyll-vault.
// Timeout: 5s per request (FM-12).
// Push failures are logged, not fatal (invariant 13 spirit).
// Search failures fall back to local memory.

type VaultClient struct {
    URL     string
    Token   string // empty for localhost
    Timeout time.Duration
}
```

Localhost detection: parse URL, check if host resolves to 127.0.0.1 or ::1.
If localhost and no token configured, skip Authorization header.
If remote and no token configured, log warning and disable vault features.
