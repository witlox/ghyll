# Error Types

Ghyll uses typed errors organized by package. Each package defines its own error types, and cross-package errors are wrapped with context at the boundary. All errors implement the standard `error` interface and support `errors.Is` / `errors.As` for matching.

## Design Principles

- Each package defines its own error types and sentinel values.
- Cross-package errors are wrapped with context at the package boundary.
- Sentinel errors are used for conditions that callers need to match on.
- Structured error types carry additional context (line numbers, exit codes, retry information).

## config/

Configuration errors are fatal at startup.

```go
var (
    ErrConfigNotFound   = errors.New("config: file not found")
    ErrConfigMalformed  = errors.New("config: invalid TOML syntax")
    ErrConfigValidation = errors.New("config: validation failed")
)

type ConfigError struct {
    Path    string
    Line    int
    Message string
    Err     error
}
```

`ConfigError` wraps parse errors with file path and line number context for precise error reporting.

## memory/

Memory errors cover the checkpoint store, hash chain integrity, and synchronization.

```go
var (
    ErrHashMismatch     = errors.New("memory: recomputed hash does not match")
    ErrSignatureInvalid = errors.New("memory: ed25519 signature verification failed")
    ErrChainBroken      = errors.New("memory: parent hash does not match previous checkpoint")
    ErrUnknownKey       = errors.New("memory: no public key found for device")
    ErrKeyPermissions   = errors.New("memory: private key has insecure file permissions")
    ErrStoreReadOnly    = errors.New("memory: checkpoint store is append-only")
    ErrEmbedderUnavail  = errors.New("memory: embedding model not available")
)

type SyncError struct {
    Op      string // "fetch", "push", "pull"
    Attempt int
    Err     error
}
```

`SyncError` wraps git operation failures with the operation type and retry attempt number.

## context/

Context errors relate to token limits, compaction, and drift detection.

```go
var (
    ErrContextTooLong    = errors.New("context: exceeds model token limit")
    ErrCompactionFailed  = errors.New("context: compaction did not reduce context sufficiently")
    ErrReactiveRetryFail = errors.New("context: reactive compaction retry failed")
)

type DriftError struct {
    Reason string // "embedder_unavailable", "no_checkpoints"
    Err    error
}

type InjectionWarning struct {
    Turn     int
    Patterns []string
}
```

`InjectionWarning` is not an error -- it is a signal surfaced to the user when prompt injection patterns are detected in tool output.

## stream/

Stream errors cover network communication with model endpoints and include retry/fallback classification.

```go
var (
    ErrStreamInterrupted = errors.New("stream: connection dropped mid-response")
    ErrAllTiersDown      = errors.New("stream: all model endpoints unreachable")
    ErrModelLocked       = errors.New("stream: locked model endpoint unreachable")
    ErrRateLimited       = errors.New("stream: rate limited")
)

type StreamError struct {
    StatusCode     int
    Retryable      bool
    RetryAfter     int    // seconds
    ContextTooLong bool   // triggers reactive compaction
    Message        string
    Err            error
}
```

`StreamError` is the most structured error type. Its fields drive the session loop's retry and fallback logic:

- **Retryable** -- the stream client retries internally (up to 3 times with backoff).
- **ContextTooLong** -- triggers reactive compaction in the session loop.
- **RetryAfter** -- honors rate limit headers from the inference server.

## tool/

Tool errors wrap execution failures with command context.

```go
var (
    ErrToolTimeout = errors.New("tool: execution timed out")
)

type ToolError struct {
    Tool     string // "bash", "file", "git", "grep"
    Command  string
    ExitCode int
    Stderr   string
    Err      error
}
```

## dialect/

Dialect errors are minimal, covering parse failures and unknown model identifiers.

```go
var (
    ErrParseToolCall = errors.New("dialect: failed to parse tool call from response")
    ErrUnknownModel  = errors.New("dialect: unknown model identifier")
)
```

## vault/

Vault errors cover communication with the optional team memory server.

```go
var (
    ErrVaultUnauthorized = errors.New("vault: unauthorized (invalid or missing token)")
    ErrVaultUnavailable  = errors.New("vault: server unreachable")
    ErrVaultRejected     = errors.New("vault: checkpoint rejected (signature invalid)")
)
```

## Error Flow Across Package Boundaries

Errors propagate upward through the dependency graph, with each boundary adding context:

```
tool/ errors     --> wrapped by context/manager --> surfaced by cmd/ghyll
memory/ errors   --> wrapped by context/manager --> surfaced by cmd/ghyll
stream/ errors   --> handled by cmd/ghyll (retry/fallback logic)
dialect/ errors  --> handled by cmd/ghyll (parse failures)
config/ errors   --> handled by cmd/ghyll (startup, fatal)
vault/ errors    --> handled by memory/vault_client --> logged, non-fatal
```

Vault errors are notably non-fatal. The vault is optional infrastructure, and its unavailability never prevents the CLI from functioning.
