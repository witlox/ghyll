# Data Structures

Go type definitions. No method bodies.

## types/ (leaf package — no dependencies)

```go
// Message is a single entry in the context window.
type Message struct {
    Role       string     `json:"role"`    // "system", "user", "assistant", "tool"
    Content    string     `json:"content"`
    ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
    ToolCallID string     `json:"tool_call_id,omitempty"`
    Name       string     `json:"name,omitempty"`
}

// ToolCall is a structured tool invocation parsed from model output.
type ToolCall struct {
    ID       string       `json:"id"`
    Type     string       `json:"type"` // always "function"
    Function ToolFunction `json:"function"`
}

type ToolFunction struct {
    Name      string `json:"name"`
    Arguments string `json:"arguments"` // JSON string
}

// ToolResult is returned from any tool execution.
type ToolResult struct {
    Output   string
    Error    string
    TimedOut bool
    Duration time.Duration
}
```

## config/

```go
// Config is the root configuration loaded from ~/.ghyll/config.toml.
type Config struct {
    Models  map[string]ModelConfig `toml:"models"`
    Routing RoutingConfig          `toml:"routing"`
    Memory  MemoryConfig           `toml:"memory"`
    Tools   ToolsConfig            `toml:"tools"`
    Vault   *VaultConfig           `toml:"vault,omitempty"`
}

type ModelConfig struct {
    Endpoint   string `toml:"endpoint"`
    Dialect    string `toml:"dialect"`
    MaxContext int    `toml:"max_context"`
}

type RoutingConfig struct {
    DefaultModel          string `toml:"default_model"`
    ContextDepthThreshold int    `toml:"context_depth_threshold"`
    ToolDepthThreshold    int    `toml:"tool_depth_threshold"`
    EnableAutoRouting     bool   `toml:"enable_auto_routing"`
}

type MemoryConfig struct {
    Branch                  string         `toml:"branch"`
    AutoSync                bool           `toml:"auto_sync"`
    SyncIntervalSeconds     int            `toml:"sync_interval_seconds"`
    CheckpointIntervalTurns int            `toml:"checkpoint_interval_turns"`
    DriftCheckIntervalTurns int            `toml:"drift_check_interval_turns"`
    DriftThreshold          float64        `toml:"drift_threshold"`
    Embedder                EmbedderConfig `toml:"embedder"`
}

type EmbedderConfig struct {
    ModelURL   string `toml:"model_url"`
    ModelPath  string `toml:"model_path"`
    Dimensions int    `toml:"dimensions"`
}

type ToolsConfig struct {
    BashTimeoutSeconds int  `toml:"bash_timeout_seconds"`
    FileTimeoutSeconds int  `toml:"file_timeout_seconds"`
    PreferRipgrep      bool `toml:"prefer_ripgrep"`
}

type VaultConfig struct {
    URL   string `toml:"url"`
    Token string `toml:"token,omitempty"`
}
```

## memory/

```go
// Checkpoint is the immutable unit of session memory.
// Invariant 3: never modified after creation.
// Invariant 4: Hash = hex(sha256(canonical JSON of all fields except Hash and Signature)).
type Checkpoint struct {
    Version      int       `json:"v"`
    Hash         string    `json:"hash"`
    ParentHash   string    `json:"parent"`
    DeviceID     string    `json:"device"`
    AuthorID     string    `json:"author"`
    Timestamp    int64     `json:"ts"`
    RepoRemote   string    `json:"repo"`
    Branch       string    `json:"branch"`
    SessionID    string    `json:"session"`
    Turn         int       `json:"turn"`
    ActiveModel  string    `json:"model"`
    Summary      string    `json:"summary"`
    Embedding    []float32 `json:"emb"`
    FilesTouched []string  `json:"files"`
    ToolsUsed    []string  `json:"tools"`
    InjectionSig []string  `json:"injections,omitempty"`
    Signature    string    `json:"sig"`
}

// VerificationResult reports the outcome of hash chain + signature verification.
type VerificationResult struct {
    Valid    bool
    DeviceID string
    FailedAt string // checkpoint hash where verification failed, empty if valid
    Reason   string // "broken_chain", "bad_signature", "unknown_key"
}

// SearchResult is a checkpoint with similarity score, returned from search.
type SearchResult struct {
    Checkpoint Checkpoint
    Similarity float64
}

// DeviceKey holds a loaded ed25519 key pair.
type DeviceKey struct {
    DeviceID   string
    PrivateKey ed25519.PrivateKey
    PublicKey  ed25519.PublicKey
}

// CheckpointRequest is the input for creating a new checkpoint.
type CheckpointRequest struct {
    SessionID    string
    Turn         int
    ActiveModel  string
    Summary      string
    Messages     []types.Message // for embedding
    FilesTouched []string
    ToolsUsed    []string
    InjectionSig []string
    Reason       string // "interval", "compaction", "handoff", "shutdown"
}
```

## context/

```go
// RoutingState tracks the current model selection and override state.
type RoutingState struct {
    ActiveModel  string // current model key ("m25", "glm5")
    ModelLocked  bool   // true if --model flag set (invariant 11)
    DeepOverride bool   // true if /deep active (invariant 11a)
    AutoRouting  bool   // true if routing decisions are automatic
    ToolDepth    int    // sequential tool calls without user input
}

// DriftResult reports the outcome of a drift measurement.
type DriftResult struct {
    Similarity     float64
    Threshold      float64
    Drifted        bool
    ComparedTo     string // checkpoint hash measured against (invariant 28)
    BackfillNeeded bool
}

// InjectionSignal reports a detected prompt injection pattern.
type InjectionSignal struct {
    Turn    int
    Pattern string // "instruction_override", "base64_payload", "sensitive_path", "system_prompt_modify"
    Snippet string // relevant text excerpt
}

// CompactionRequest is passed from context/manager to the compaction callback.
// Invariant 24a: contains only turns to summarize, not the full context.
type CompactionRequest struct {
    TurnsToSummarize []types.Message
    CompactionPrompt string // from dialect, provided via callback
    ModelEndpoint    string
}

// ManagerDeps are callbacks provided by cmd/ghyll to wire packages
// that cannot import each other. See session-loop.md.
type ManagerDeps struct {
    TokenCount       func([]types.Message) int
    CompactionCall   func(CompactionRequest) (string, error)
    CreateCheckpoint func(memory.CheckpointRequest) error
    Embed            func([]types.Message) ([]float32, error)
}
```

## stream/

```go
// StreamResponse is the assembled result of a streaming API call.
type StreamResponse struct {
    Content      string
    ToolCalls    []types.ToolCall
    Usage        Usage
    FinishReason string // "stop", "tool_calls", "length"
    Partial      bool   // true if stream was interrupted (invariant 20)
    RawToolCalls json.RawMessage // unparsed, passed to dialect for model-specific parsing
}

type Usage struct {
    PromptTokens     int
    CompletionTokens int
    TotalTokens      int
}

// StreamError classifies the failure mode for retry/fallback logic.
type StreamError struct {
    StatusCode     int
    Retryable      bool
    RetryAfter     int  // seconds, from Retry-After header (429)
    ContextTooLong bool // true if context_length_exceeded (triggers reactive compaction)
    Message        string
    Err            error
}
```

## dialect/

Dialect has no shared types — each model file exports standalone functions.
The function signatures are the contract. See routing-logic.md for the router.

```go
// Per-dialect function signatures (not an interface — concrete per model file):
//
// glm5.go:
//   func GLM5SystemPrompt(workdir string) string
//   func GLM5BuildMessages(msgs []types.Message, systemPrompt string) []map[string]any
//   func GLM5ParseToolCalls(raw json.RawMessage) ([]types.ToolCall, error)
//   func GLM5CompactionPrompt() string
//   func GLM5TokenCount(msgs []types.Message) int
//   func GLM5HandoffSummary(cp memory.Checkpoint, recentTurns []types.Message) []types.Message
//
// minimax_m25.go:
//   func M25SystemPrompt(workdir string) string
//   func M25BuildMessages(msgs []types.Message, systemPrompt string) []map[string]any
//   func M25ParseToolCalls(raw json.RawMessage) ([]types.ToolCall, error)
//   func M25CompactionPrompt() string
//   func M25TokenCount(msgs []types.Message) int
//   func M25HandoffSummary(cp memory.Checkpoint, recentTurns []types.Message) []types.Message
//
// router.go:
//   func Evaluate(inputs RouterInputs) RoutingDecision
//
// RouterInputs and RoutingDecision are defined in dialect/:

type RouterInputs struct {
    ContextDepth      int
    ToolDepth         int
    ModelLocked       bool
    DeepOverride      bool
    ActiveModel       string
    BackfillTriggered bool
    Config            config.RoutingConfig
}

type RoutingDecision struct {
    Action         string // "none", "escalate", "de_escalate"
    TargetModel    string
    NeedCompaction bool   // true if compact-before-handoff required (invariant 24b)
}
```

Note: dialect/ imports types/ and config/ only. HandoffSummary takes a
`memory.Checkpoint` which means dialect/ also imports memory/. This is
acceptable — dialect/ → memory/ is a read-only dependency on the Checkpoint
type, and memory/ does not import dialect/.

## tool/

```go
// tool/ uses types.ToolResult as its return type.
// No additional types needed — tool/ is thin wrappers around OS calls.
```

## vault/

```go
// SearchRequest is the vault search API request body.
type SearchRequest struct {
    Embedding []float32 `json:"embedding"`
    RepoHash  string    `json:"repo"`
    TopK      int       `json:"top_k"`
}

// SearchResponse is the vault search API response body.
type SearchResponse struct {
    Results []memory.SearchResult `json:"results"`
}

// PushRequest is the vault checkpoint push API request body.
type PushRequest struct {
    Checkpoint memory.Checkpoint `json:"checkpoint"`
}
```
