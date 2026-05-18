package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

var (
	ErrConfigNotFound   = errors.New("config: file not found")
	ErrConfigMalformed  = errors.New("config: invalid TOML syntax")
	ErrConfigValidation = errors.New("config: validation failed")
)

// ConfigError wraps parse errors with context.
type ConfigError struct {
	Path    string
	Message string
	Err     error
}

func (e *ConfigError) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("%s: %s", e.Path, e.Message)
	}
	return e.Message
}

func (e *ConfigError) Unwrap() error {
	return e.Err
}

// Config is the root configuration loaded from ~/.ghyll/config.toml.
type Config struct {
	Models   map[string]ModelConfig `toml:"models"`
	Routing  RoutingConfig          `toml:"routing"`
	Memory   MemoryConfig           `toml:"memory"`
	Tools    ToolsConfig            `toml:"tools"`
	SubAgent SubAgentConfig         `toml:"sub_agent"`
	Workflow WorkflowConfig         `toml:"workflow"`
	Vault    *VaultConfig           `toml:"vault,omitempty"`
}

type ModelConfig struct {
	Endpoint   string `toml:"endpoint"`
	Dialect    string `toml:"dialect"`
	MaxContext int    `toml:"max_context"`
}

type RoutingConfig struct {
	DefaultModel          string `toml:"default_model"`
	DeepModel             string `toml:"deep_model"`
	ContextDepthThreshold int    `toml:"context_depth_threshold"`
	ToolDepthThreshold    int    `toml:"tool_depth_threshold"`
	EnableAutoRouting     bool   `toml:"enable_auto_routing"`

	// GateFloorEscalateAtRank is the depth rank (1..3, matching
	// runner.DepthRank: SHALLOW/MOCKED/REALISTIC) at which a v2
	// gate's MinTier floor forces escalation to DeepModel.
	//
	// Default 2 (MOCKED): gates requiring MOCKED or REALISTIC depth
	// run on the deep tier; SHALLOW or NONE can run on the default.
	//
	// Valid range 1..3. To DISABLE the gate-floor mechanism set
	// `gate_floor_disabled = true` (see below). Field omitted in
	// TOML defaults to 2 via applyDefaults.
	GateFloorEscalateAtRank int `toml:"gate_floor_escalate_at_rank"`

	// GateFloorDisabled turns the gate-floor mechanism off entirely
	// when true. Validation-pass-8 R4: an explicit boolean is
	// unambiguous about operator intent, where a sentinel-zero on
	// the rank field is not (TOML decodes both "field omitted" and
	// "explicit zero" identically as `int = 0`).
	GateFloorDisabled bool `toml:"gate_floor_disabled"`
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
	BashTimeoutSeconds   int    `toml:"bash_timeout_seconds"`
	FileTimeoutSeconds   int    `toml:"file_timeout_seconds"`
	WebTimeoutSeconds    int    `toml:"web_timeout_seconds"`
	WebMaxResponseTokens int    `toml:"web_max_response_tokens"`
	WebSearchBackend     string `toml:"web_search_backend"`
	PreferRipgrep        bool   `toml:"prefer_ripgrep"`
}

type SubAgentConfig struct {
	DefaultModel   string `toml:"default_model"`
	MaxTurns       int    `toml:"max_turns"`
	TokenBudget    int    `toml:"token_budget"`
	TimeoutSeconds int    `toml:"timeout_seconds"`
}

type WorkflowConfig struct {
	InstructionBudgetTokens int      `toml:"instruction_budget_tokens"`
	FallbackFolders         []string `toml:"fallback_folders"`
}

type VaultConfig struct {
	URL   string `toml:"url"`
	Token string `toml:"token,omitempty"`
}

// Load reads and parses a TOML config file, applies defaults, and validates.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &ConfigError{
				Path:    path,
				Message: fmt.Sprintf("no config found at %s", path),
				Err:     ErrConfigNotFound,
			}
		}
		return nil, &ConfigError{Path: path, Message: err.Error(), Err: err}
	}

	var cfg Config
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return nil, &ConfigError{
			Path:    path,
			Message: err.Error(),
			Err:     ErrConfigMalformed,
		}
	}

	applyDefaults(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Routing.DefaultModel == "" {
		cfg.Routing.DefaultModel = "m25"
	}
	if cfg.Routing.ContextDepthThreshold == 0 {
		cfg.Routing.ContextDepthThreshold = 32000
	}
	if cfg.Routing.ToolDepthThreshold == 0 {
		cfg.Routing.ToolDepthThreshold = 5
	}
	if cfg.Routing.GateFloorEscalateAtRank == 0 {
		// Default = MOCKED (rank 2). Operators set
		// gate_floor_disabled=true to turn the mechanism off; the
		// rank field expresses the threshold not the on/off state.
		cfg.Routing.GateFloorEscalateAtRank = 2
	}
	if cfg.Memory.Branch == "" {
		cfg.Memory.Branch = "ghyll/memory"
	}
	if cfg.Memory.SyncIntervalSeconds == 0 {
		cfg.Memory.SyncIntervalSeconds = 60
	}
	if cfg.Memory.CheckpointIntervalTurns == 0 {
		cfg.Memory.CheckpointIntervalTurns = 5
	}
	if cfg.Memory.DriftCheckIntervalTurns == 0 {
		cfg.Memory.DriftCheckIntervalTurns = 5
	}
	if cfg.Memory.DriftThreshold == 0 {
		cfg.Memory.DriftThreshold = 0.7
	}
	if cfg.Memory.Embedder.Dimensions == 0 {
		cfg.Memory.Embedder.Dimensions = 384
	}
	if cfg.Tools.BashTimeoutSeconds == 0 {
		cfg.Tools.BashTimeoutSeconds = 30
	}
	if cfg.Tools.FileTimeoutSeconds == 0 {
		cfg.Tools.FileTimeoutSeconds = 5
	}
	if cfg.Tools.WebTimeoutSeconds == 0 {
		cfg.Tools.WebTimeoutSeconds = 30
	}
	if cfg.Tools.WebMaxResponseTokens == 0 {
		cfg.Tools.WebMaxResponseTokens = 10000
	}
	if cfg.SubAgent.MaxTurns == 0 {
		cfg.SubAgent.MaxTurns = 20
	}
	if cfg.SubAgent.TokenBudget == 0 {
		cfg.SubAgent.TokenBudget = 50000
	}
	if cfg.SubAgent.TimeoutSeconds == 0 {
		cfg.SubAgent.TimeoutSeconds = 300
	}
	if cfg.Workflow.InstructionBudgetTokens == 0 {
		cfg.Workflow.InstructionBudgetTokens = 2000
	}
	if len(cfg.Workflow.FallbackFolders) == 0 {
		cfg.Workflow.FallbackFolders = []string{".claude"}
	}
}

func validate(cfg *Config) error {
	// Default model must have an endpoint
	if _, ok := cfg.Models[cfg.Routing.DefaultModel]; !ok {
		return &ConfigError{
			Message: fmt.Sprintf("default model '%s' has no endpoint configured", cfg.Routing.DefaultModel),
			Err:     ErrConfigValidation,
		}
	}

	// Deep model must have an endpoint (if configured)
	if cfg.Routing.DeepModel != "" {
		if _, ok := cfg.Models[cfg.Routing.DeepModel]; !ok {
			return &ConfigError{
				Message: fmt.Sprintf("deep model '%s' has no endpoint configured", cfg.Routing.DeepModel),
				Err:     ErrConfigValidation,
			}
		}
	}

	// Every model must have an endpoint and valid dialect.
	// Per validation-pass-8 D1: include the new deepseek + qwen
	// families (and documented variants). Quant-suffixed names like
	// `qwen-coder-q4` are operator-config (model name + endpoint),
	// NOT dialect identifiers — they should set `dialect = "qwen"`
	// per docs/usage/configuration.md.
	knownDialects := map[string]bool{
		"minimax": true, "minimax_m25": true, "minimax_m27": true,
		"glm": true, "glm5": true, "glm51": true,
		"deepseek": true, "deepseek-v3": true, "deepseek-coder": true, "deepseek-coder-v3": true,
		"qwen": true, "qwen-coder": true, "qwen2.5-coder": true, "qwen3-coder": true,
		"": true, // empty defaults to minimax
	}
	for name, m := range cfg.Models {
		if m.Endpoint == "" {
			return &ConfigError{
				Message: fmt.Sprintf("model '%s' has no endpoint", name),
				Err:     ErrConfigValidation,
			}
		}
		if !knownDialects[m.Dialect] {
			return &ConfigError{
				Message: fmt.Sprintf("model '%s' has unknown dialect '%s' (known families: minimax, glm, deepseek, qwen)", name, m.Dialect),
				Err:     ErrConfigValidation,
			}
		}
	}

	// Validation-pass-8 R2: gate-floor escalation rank must be 1..3.
	// Out-of-range values silently disable the mechanism in the
	// router without R2's fix, which is exactly the operator-
	// misconfig pattern we want to catch.
	if cfg.Routing.GateFloorEscalateAtRank < 1 || cfg.Routing.GateFloorEscalateAtRank > 3 {
		return &ConfigError{
			Message: fmt.Sprintf("gate_floor_escalate_at_rank = %d out of 1..3 (1=SHALLOW, 2=MOCKED, 3=REALISTIC; set gate_floor_disabled=true to disable)",
				cfg.Routing.GateFloorEscalateAtRank),
			Err: ErrConfigValidation,
		}
	}

	return nil
}

// IsNotFound reports whether the error is a config-not-found error.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrConfigNotFound)
}

// IsMalformed reports whether the error is a TOML parse error.
func IsMalformed(err error) bool {
	return errors.Is(err, ErrConfigMalformed)
}

// IsValidation reports whether the error is a config validation error.
func IsValidation(err error) bool {
	return errors.Is(err, ErrConfigValidation)
}
