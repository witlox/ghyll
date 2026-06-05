package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/witlox/ghyll/internal/safefile"
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

	// APIKey is an optional Bearer token forwarded to the endpoint
	// as `Authorization: Bearer <APIKey>`. Empty == no Authorization
	// header injected (preserves zero-config behaviour for endpoints
	// that don't require auth). Internal/dev endpoints typically
	// leave this empty; CSCS-style gateways set it. Tokens are
	// opaque: ghyll does not validate `sk-...` prefixes or any
	// other shape. Operators may override via env at runtime
	// without editing the TOML — see ResolveAPIKey.
	APIKey string `toml:"api_key,omitempty"`

	// StampLabel overrides what gets written to the Ghyll-Model
	// commit trailer (validation-pass-10 H6). Defaults to the bare
	// model name when empty — endpoint URLs are NOT included so
	// internal infrastructure DNS doesn't leak into git log on a
	// public mirror. Operators with multiple endpoints for the same
	// model can set this explicitly (e.g., "qwen-coder@gpu1"). The
	// label MUST NOT embed an api_key (no validator regex enforces
	// that yet — defence-in-depth lives in the redactor).
	StampLabel string `toml:"stamp_label"`
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

	// Validation-pass-10 H15: per-tool git timeouts. Default 5s for
	// status checks, 30s for commits — independent contexts so a
	// slow CheckPending doesn't starve the subsequent commit.
	GitCheckTimeoutSeconds  int `toml:"git_check_timeout_seconds"`
	GitCommitTimeoutSeconds int `toml:"git_commit_timeout_seconds"`
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

// MaxConfigFileBytes caps the config TOML at 1 MiB. Tier 3 / SR
// H-5: unbounded os.ReadFile + toml.Decode could OOM on a forged
// 100 MiB config.toml.
const MaxConfigFileBytes = 1 * 1024 * 1024

// Load reads and parses a TOML config file, applies defaults, and validates.
func Load(path string) (*Config, error) {
	// Tier 3 / SR H-5: cap at 1 MiB + refuse symlinks before
	// toml.Decode allocates.
	data, err := safefile.ReadCappedFile(path, MaxConfigFileBytes)
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
	md, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return nil, &ConfigError{
			Path:    path,
			Message: err.Error(),
			Err:     ErrConfigMalformed,
		}
	}

	// AUTH-1 / AUTH-6: BurntSushi/toml silently drops keys that do
	// not match a struct tag. An operator writing `api_token = ...`
	// (or `Api_Key = ...`) under `[models.*]` thinking it is the
	// auth key gets a silent miss + an unauthenticated request +
	// the redacted `authentication failed` error with NO signal
	// that the cause was a typo. Surface a directed error for
	// known auth-key misspellings under [models.*].
	if err := checkUndecodedAuthKeys(path, md); err != nil {
		return nil, err
	}

	applyDefaults(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	// AUTH-5 / AUTH-W-006 / ADV-AUTH-002: file-mode re-check at
	// Load when any api_key (or vault token) is present in TOML.
	// The bootstrap-seed sets 0o600, but a co-tenant chmod after
	// seed (or a tarball extraction that strips mode) silently
	// re-exposes the secret. Refuse with a directed message so the
	// operator can fix it. Mitigation runs ONLY when there is a
	// secret in TOML — empty configs continue to work even on a
	// chmod-644'd file.
	if err := checkSecretFileMode(path, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// suspectAuthKeyPattern matches TOML keys an operator likely meant
// as `api_key`: api_token, api-key, apikey, token, auth_token,
// auth-key, bearer, secret. Case-insensitive at use sites.
var suspectAuthKeyPattern = regexp.MustCompile(`(?i)^(api[_-]?(token|key)|apikey|auth[_-]?(token|key)|token|bearer|secret)$`)

// checkUndecodedAuthKeys walks toml.MetaData.Undecoded() and flags
// any key under `[models.*]` whose name matches the suspect auth
// pattern. Returns a directed ConfigError with a "did you mean"
// hint. Other undecoded keys (typos in unrelated sections) are
// allowed through — the function fails-narrow not fails-closed to
// avoid breaking benign migrations that leave forward-compat fields
// commented in.
func checkUndecodedAuthKeys(path string, md toml.MetaData) error {
	for _, key := range md.Undecoded() {
		parts := key
		if len(parts) < 3 {
			continue
		}
		if parts[0] != "models" {
			continue
		}
		// parts[1] = model name, parts[2] = field name
		field := parts[len(parts)-1]
		if suspectAuthKeyPattern.MatchString(field) {
			return &ConfigError{
				Path: path,
				Message: fmt.Sprintf("unknown key %q under [models.%s] — did you mean 'api_key'?",
					field, parts[1]),
				Err: ErrConfigValidation,
			}
		}
	}
	return nil
}

// checkSecretFileMode stats path and refuses the load when any
// model carries a non-empty APIKey (or vault.token is set) AND the
// file mode permits group or other read. Mitigates a malicious or
// accidental chmod 644 after the bootstrap-seed sets 0o600.
//
// AUTH-5 / AUTH-W-006 / ADV-AUTH-002 — defence-in-depth that closes
// the architect plan's R4 gap.
func checkSecretFileMode(path string, cfg *Config) error {
	hasSecret := false
	for _, m := range cfg.Models {
		if m.APIKey != "" {
			hasSecret = true
			break
		}
	}
	if !hasSecret && cfg.Vault != nil && cfg.Vault.Token != "" {
		hasSecret = true
	}
	if !hasSecret {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		// Already-readable file (we just read it) — Stat failures
		// here are unusual; surface but do not block.
		return nil
	}
	perm := info.Mode().Perm()
	if perm&0o077 != 0 {
		return &ConfigError{
			Path: path,
			Message: fmt.Sprintf(
				"refusing to load: file mode %#o permits group/other read and the config contains a secret; chmod 0600 %s",
				perm, path),
			Err: ErrConfigValidation,
		}
	}
	return nil
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
	if cfg.Tools.GitCheckTimeoutSeconds == 0 {
		cfg.Tools.GitCheckTimeoutSeconds = 5
	}
	if cfg.Tools.GitCommitTimeoutSeconds == 0 {
		cfg.Tools.GitCommitTimeoutSeconds = 30
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

// stampLabelSecretPattern flags stamp_label values that look like
// they contain a Bearer token / api_key / generic secret. AUTH-W-005
// / ADV-AUTH-005: defence-in-depth — the stamp_label is the ONE
// documented path where api_key bytes could escape into a public
// git commit trailer. Forbid the obvious misconfig at Load time.
var stampLabelSecretPattern = regexp.MustCompile(`(?i)(sk-[A-Za-z0-9_\-]{12,}|bearer\s|api[_-]?key|secret)`)

// apiKeyControlCharPattern flags control bytes in an api_key that
// would corrupt the Authorization header. Go's http transport
// silently rewrites embedded CRLF to spaces, producing a malformed
// token with no operator-visible signal. AUTH-3 / AUTH-W-009.
var apiKeyControlCharRE = regexp.MustCompile(`[\x00-\x1f\x7f]`)

func validate(cfg *Config) error {
	// Default model must have an endpoint
	if _, ok := cfg.Models[cfg.Routing.DefaultModel]; !ok {
		return &ConfigError{
			Message: fmt.Sprintf("default model '%s' has no endpoint configured", cfg.Routing.DefaultModel),
			Err:     ErrConfigValidation,
		}
	}

	// AUTH-2: detect model TOML keys that normalize to the same
	// env-var bucket. Two distinct configured models that map to
	// the same GHYLL_API_KEY_<NORM> would silently leak one
	// tenant's env key onto the other endpoint. Reject at Load so
	// the operator can rename one before the keys hit the wire.
	envBuckets := make(map[string]string)
	for name := range cfg.Models {
		norm := normalizeModelEnvKey(name)
		if norm == "" {
			continue
		}
		if prev, ok := envBuckets[norm]; ok && prev != name {
			return &ConfigError{
				Message: fmt.Sprintf(
					"model keys %q and %q both normalize to env var GHYLL_API_KEY_%s; rename one to avoid silent cross-tenant key leakage",
					prev, name, norm),
				Err: ErrConfigValidation,
			}
		}
		envBuckets[norm] = name
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
		// Tier 3 / SR H-5: endpoint URL scheme MUST be http or
		// https. Forbid file://, data://, ext:, etc. — these
		// can route through Go transport plugins or trigger
		// unexpected handlers.
		if err := safefile.ValidateURLScheme(m.Endpoint, "http", "https"); err != nil {
			return &ConfigError{
				Message: fmt.Sprintf("model '%s' endpoint %q: %v", name, m.Endpoint, err),
				Err:     ErrConfigValidation,
			}
		}
		if !knownDialects[m.Dialect] {
			return &ConfigError{
				Message: fmt.Sprintf("model '%s' has unknown dialect '%s' (known families: minimax, glm, deepseek, qwen)", name, m.Dialect),
				Err:     ErrConfigValidation,
			}
		}

		// AUTH-3 / AUTH-W-009: reject control bytes in api_key — these
		// produce a malformed Authorization header (Go silently rewrites
		// embedded CRLF) with no operator signal. The redactor would then
		// surface "authentication failed" leaving the operator chasing
		// a phantom upstream failure.
		if m.APIKey != "" && apiKeyControlCharRE.MatchString(m.APIKey) {
			return &ConfigError{
				Message: fmt.Sprintf(
					"model '%s' api_key contains a control character (newline, tab, or CRLF) — check for a clipboard or shell-pipe artifact",
					name),
				Err: ErrConfigValidation,
			}
		}

		// AUTH-7: refuse a non-loopback http:// endpoint when an
		// api_key is configured (token transmitted in plaintext).
		// Loopback is exempt because it is the documented sane case
		// for an internal-only endpoint behind a TLS-terminating
		// sidecar.
		if m.APIKey != "" {
			if err := requireHTTPSWithSecret(name, m.Endpoint); err != nil {
				return err
			}
		}

		// AUTH-W-005 / ADV-AUTH-005: defence-in-depth — stamp_label
		// MUST NOT embed an api_key or other secret-shaped string,
		// because the label is committed to git trailers and is
		// reachable from any public mirror.
		if m.StampLabel != "" && stampLabelSecretPattern.MatchString(m.StampLabel) {
			return &ConfigError{
				Message: fmt.Sprintf(
					"model '%s' stamp_label %q appears to embed a secret (sk-prefix, bearer, api_key, or secret token); secrets must not appear in commit trailers",
					name, m.StampLabel),
				Err: ErrConfigValidation,
			}
		}
	}
	// Tier 3 / SR H-5: vault URL also scheme-checked when set.
	if cfg.Vault != nil && cfg.Vault.URL != "" {
		if err := safefile.ValidateURLScheme(cfg.Vault.URL, "http", "https"); err != nil {
			return &ConfigError{
				Message: fmt.Sprintf("vault.url %q: %v", cfg.Vault.URL, err),
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

// APIKeySource describes where a resolved api_key came from. Used
// by the cmd/ghyll redactor so operator-visible output can show
// provenance without leaking the value.
type APIKeySource int

const (
	// APIKeyUnset means no api_key is configured for the model
	// (neither TOML nor env). Empty resolution; no Authorization
	// header will be emitted.
	APIKeyUnset APIKeySource = iota
	// APIKeyFromTOML means the value came from cfg.Models[name].APIKey.
	APIKeyFromTOML
	// APIKeyFromEnv means the value came from one of the env
	// variables (GHYLL_API_KEY_<MODEL> or GHYLL_API_KEY).
	APIKeyFromEnv
)

// ResolveAPIKey returns the effective api_key for modelName, applied
// in this precedence (highest first):
//
//  1. env `GHYLL_API_KEY_<NORM>` where <NORM> is the model TOML key
//     normalized via normalizeModelEnvKey.
//  2. env `GHYLL_API_KEY` (global fallback).
//  3. cfg.Models[modelName].APIKey (TOML).
//
// An empty result means no Authorization header should be emitted.
// The function never panics on a nil cfg or an unknown modelName —
// it returns "" so callers can treat the result uniformly.
//
// Asymmetry note: cfg.Models is keyed by the EXACT TOML key
// (case-sensitive). The env var is upper-cased. So [models.CSCS-GLM5]
// becomes env GHYLL_API_KEY_CSCS_GLM5 — the operator types the env
// uppercase even though the TOML key is mixed-case.
func ResolveAPIKey(cfg *Config, modelName string) string {
	value, _ := ResolveAPIKeyWithSource(cfg, modelName)
	return value
}

// ResolveAPIKeyWithSource is like ResolveAPIKey but also reports
// which layer produced the value. The redactor uses this to render
// `<unset>` / `<env>` / `<toml>` provenance tokens.
//
// All returned values are passed through strings.TrimSpace to defend
// against AUTH-3 / AUTH-W-009: a trailing newline (from a clipboard
// paste or `$(cat key.txt)`) produces a malformed Authorization
// header whose only surface signal is a redacted `authentication
// failed`. Trim at the chokepoint so every caller benefits.
//
// Semantics note (AUTH-8 / ADV-AUTH-010): os.Getenv conflates "unset"
// and "set to empty string". Both are treated as "no value at this
// layer"; the resolver falls through to the next precedence band.
// An operator cannot use `GHYLL_API_KEY_X=""` to negative-override a
// TOML-configured key — the TOML value still wins. This is the
// documented behaviour.
func ResolveAPIKeyWithSource(cfg *Config, modelName string) (string, APIKeySource) {
	if modelName != "" {
		norm := normalizeModelEnvKey(modelName)
		if norm != "" {
			if v := strings.TrimSpace(os.Getenv("GHYLL_API_KEY_" + norm)); v != "" {
				return v, APIKeyFromEnv
			}
		}
	}
	if v := strings.TrimSpace(os.Getenv("GHYLL_API_KEY")); v != "" {
		return v, APIKeyFromEnv
	}
	if cfg != nil {
		if mc, ok := cfg.Models[modelName]; ok {
			if trimmed := strings.TrimSpace(mc.APIKey); trimmed != "" {
				return trimmed, APIKeyFromTOML
			}
		}
	}
	return "", APIKeyUnset
}

// requireHTTPSWithSecret enforces AUTH-7: when an api_key is set on
// a model, the endpoint scheme MUST be https unless the host is a
// loopback address. Loopback (127.0.0.1, ::1, localhost) is exempt
// because that is the documented sane case for internal-only
// endpoints behind a TLS-terminating sidecar.
func requireHTTPSWithSecret(modelName, endpoint string) error {
	// Reuse safefile-style parsing.
	low := strings.ToLower(endpoint)
	if strings.HasPrefix(low, "https://") {
		return nil
	}
	if !strings.HasPrefix(low, "http://") {
		// Other schemes are caught by safefile.ValidateURLScheme; we
		// only need to police http here.
		return nil
	}
	// Strip scheme and walk to host portion.
	rest := endpoint[len("http://"):]
	// Strip userinfo if present (rare).
	if at := strings.Index(rest, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	host := rest
	// Cut off path / query.
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	// Strip port; handle bracketed IPv6.
	if strings.HasPrefix(host, "[") {
		if end := strings.Index(host, "]"); end >= 0 {
			host = host[1:end]
		}
	} else if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	host = strings.ToLower(host)
	switch host {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return nil
	}
	// 127.0.0.0/8 alias check
	if strings.HasPrefix(host, "127.") {
		return nil
	}
	return &ConfigError{
		Message: fmt.Sprintf(
			"model '%s' uses http:// endpoint %q with api_key configured; refuse plaintext bearer transmission (use https:// or restrict to loopback)",
			modelName, endpoint),
		Err: ErrConfigValidation,
	}
}

// normalizeModelEnvKey upper-cases the TOML model key and replaces
// any rune outside [A-Z0-9_] with '_'. Used to compose the model-
// scoped env variable name (GHYLL_API_KEY_<NORM>). Empty input
// returns empty output (the caller skips the scoped lookup).
//
// AUTH-W-008: the loop iterates RUNES, not bytes. A model name
// containing a multi-byte UTF-8 rune (e.g. `é` = 0xC3 0xA9) emits
// ONE underscore per rune, not per byte; this keeps `qwen-é` and
// `qwen-ê` distinct env-var buckets so two non-ASCII model names
// don't silently collide. The collision-detection at validate()
// gives the final safety net.
func normalizeModelEnvKey(name string) string {
	if name == "" {
		return ""
	}
	out := make([]byte, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			out = append(out, byte(r)-('a'-'A'))
		case r >= 'A' && r <= 'Z':
			out = append(out, byte(r))
		case r >= '0' && r <= '9':
			out = append(out, byte(r))
		case r == '_':
			out = append(out, byte(r))
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}
