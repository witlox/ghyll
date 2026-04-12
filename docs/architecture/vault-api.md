# Vault HTTP API

The ghyll-vault server provides an optional team memory search service. It exposes an HTTP API for searching checkpoints by embedding similarity and for pushing new checkpoints from CLI clients.

Git-based sync handles basic team memory sharing without a vault. The vault adds real-time vector search across all team checkpoints, which is useful for larger teams or when checkpoint volume exceeds what local search can handle efficiently.

## Authentication

- **Remote vault**: Requires a Bearer token in the `Authorization` header. The token is configured in `config.toml`. Requests with a missing or invalid token receive a `401 Unauthorized` response.
- **Localhost** (127.0.0.1 or ::1): No authentication required. The token check is skipped entirely, making local development frictionless.

## Endpoints

### POST /v1/search

Search for checkpoints by embedding similarity.

**Request:**
```json
{
  "embedding": [0.1, 0.2, ...],
  "repo": "sha256-of-remote-url",
  "top_k": 5
}
```

The `embedding` field is a float32 array whose dimensions must match the configured embedding model. The `repo` field filters results to a specific repository. `top_k` controls how many results to return.

**Response (200):**
```json
{
  "results": [
    {
      "checkpoint": { ... },
      "similarity": 0.87
    }
  ]
}
```

Results are sorted by cosine similarity in descending order.

**Errors:**
- `400` -- Missing or malformed fields.
- `401` -- Missing or invalid token (remote only).
- `500` -- Internal server error.

### POST /v1/checkpoints

Push a checkpoint to the vault.

**Request:**
```json
{
  "checkpoint": { ... }
}
```

The checkpoint object must include the `hash` and `sig` fields.

**Response (201):** Stored successfully.

**Validation:**
1. Verify the checkpoint signature against known public keys.
2. Verify the hash matches the content (recompute the canonical hash).
3. If valid, store the checkpoint.
4. If invalid, reject with `403 Forbidden` and a reason.

**Errors:**
- `400` -- Malformed checkpoint.
- `401` -- Missing or invalid token (remote only).
- `403` -- Signature verification failed.
- `409` -- Checkpoint with this hash already exists. This is idempotent and not a true error -- the checkpoint was already stored.
- `500` -- Internal server error.

### GET /v1/health

Health check endpoint.

**Response (200):**
```json
{
  "status": "ok",
  "checkpoints": 12345,
  "devices": 7
}
```

## Storage

The vault uses the same SQLite schema as the CLI (see [Checkpoint Format](checkpoints.md)). Embedding similarity search uses brute-force cosine similarity on the embedding column. At the expected scale (under 100K checkpoints), this is sufficient. Indexing can be added later if needed.

## Client Behavior

The CLI communicates with the vault through `memory/vault_client.go`:

```go
type VaultClient struct {
    URL     string
    Token   string        // empty for localhost
    Timeout time.Duration // 5s per request
}
```

Key behaviors:

- **Push failures are logged, not fatal.** Checkpoint creation never blocks on vault availability.
- **Search failures fall back to local memory.** If the vault is unreachable, the CLI searches its local SQLite store instead.
- **Localhost detection**: The client parses the configured URL and checks if the host resolves to 127.0.0.1 or ::1. If so and no token is configured, it skips the Authorization header. If the vault is remote and no token is configured, the client logs a warning and disables vault features.
