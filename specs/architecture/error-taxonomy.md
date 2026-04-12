# Error Taxonomy

Typed errors per package. No generic error wrapping across boundaries.

## Design principles

- Each package defines its own error types
- Cross-package errors are wrapped with context at the boundary
- Sentinel errors for conditions callers need to match on
- All errors implement `error` and support `errors.Is` / `errors.As`

## config/

```go
var (
    ErrConfigNotFound   = errors.New("config: file not found")
    ErrConfigMalformed  = errors.New("config: invalid TOML syntax")
    ErrConfigValidation = errors.New("config: validation failed")
)

// ConfigError wraps parse errors with line number context.
type ConfigError struct {
    Path    string
    Line    int
    Message string
    Err     error
}
```

## memory/

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

// SyncError wraps git operation failures.
type SyncError struct {
    Op      string // "fetch", "push", "pull"
    Attempt int
    Err     error
}
```

## context/

```go
var (
    ErrContextTooLong    = errors.New("context: exceeds model token limit")
    ErrCompactionFailed  = errors.New("context: compaction did not reduce context sufficiently")
    ErrReactiveRetryFail = errors.New("context: reactive compaction retry failed")
)

// DriftError wraps embedding comparison failures.
type DriftError struct {
    Reason string // "embedder_unavailable", "no_checkpoints"
    Err    error
}

// InjectionWarning is not an error — it's a signal surfaced to the user.
type InjectionWarning struct {
    Turn     int
    Patterns []string
}
```

## stream/

```go
var (
    ErrStreamInterrupted = errors.New("stream: connection dropped mid-response")
    ErrAllTiersDown      = errors.New("stream: all model endpoints unreachable")
    ErrModelLocked       = errors.New("stream: locked model endpoint unreachable")
    ErrRateLimited       = errors.New("stream: rate limited")
)

// StreamError includes retry/fallback classification.
type StreamError struct {
    StatusCode     int
    Retryable      bool
    RetryAfter     int  // seconds
    ContextTooLong bool // triggers reactive compaction
    Message        string
    Err            error
}
```

## tool/

```go
var (
    ErrToolTimeout = errors.New("tool: execution timed out")
)

// ToolError wraps execution failures with command context.
type ToolError struct {
    Tool     string // "bash", "file", "git", "grep"
    Command  string
    ExitCode int
    Stderr   string
    Err      error
}
```

## dialect/

```go
var (
    ErrParseToolCall = errors.New("dialect: failed to parse tool call from response")
    ErrUnknownModel  = errors.New("dialect: unknown model identifier")
)
```

## vault/

```go
var (
    ErrVaultUnauthorized = errors.New("vault: unauthorized (invalid or missing token)")
    ErrVaultUnavailable  = errors.New("vault: server unreachable")
    ErrVaultRejected     = errors.New("vault: checkpoint rejected (signature invalid)")
)
```

## Error flow across boundaries

```
tool/ errors     → wrapped by context/manager → surfaced by cmd/ghyll
memory/ errors   → wrapped by context/manager → surfaced by cmd/ghyll
stream/ errors   → handled by cmd/ghyll (retry/fallback logic)
dialect/ errors  → handled by cmd/ghyll (parse failures)
config/ errors   → handled by cmd/ghyll (startup, fatal)
vault/ errors    → handled by memory/vault_client → logged, non-fatal
```
