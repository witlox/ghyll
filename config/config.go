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
}

func validate(cfg *Config) error {
	// Default model must have an endpoint
	if _, ok := cfg.Models[cfg.Routing.DefaultModel]; !ok {
		return &ConfigError{
			Message: fmt.Sprintf("default model '%s' has no endpoint configured", cfg.Routing.DefaultModel),
			Err:     ErrConfigValidation,
		}
	}

	// Every model must have an endpoint
	for name, m := range cfg.Models {
		if m.Endpoint == "" {
			return &ConfigError{
				Message: fmt.Sprintf("model '%s' has no endpoint", name),
				Err:     ErrConfigValidation,
			}
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
