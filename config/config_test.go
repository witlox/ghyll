package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestScenario_Config_LoadValid maps to:
// Scenario: Load valid config
func TestScenario_Config_LoadValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	content := `
[models.m25]
endpoint = "https://inference.internal:8001/v1"
dialect = "minimax"
max_context = 1000000

[models.glm5]
endpoint = "https://inference.internal:8002/v1"
dialect = "glm"
max_context = 200000

[routing]
default_model = "m25"
context_depth_threshold = 32000
tool_depth_threshold = 5
enable_auto_routing = true

[memory]
branch = "ghyll/memory"
auto_sync = true
sync_interval_seconds = 60
checkpoint_interval_turns = 5
drift_check_interval_turns = 5
drift_threshold = 0.7

[memory.embedder]
model_url = "https://huggingface.co/model.onnx"
model_path = "~/.ghyll/models/gte-micro.onnx"
dimensions = 384

[tools]
bash_timeout_seconds = 30
file_timeout_seconds = 5
prefer_ripgrep = true
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Model endpoints resolved
	if cfg.Models["m25"].Endpoint != "https://inference.internal:8001/v1" {
		t.Errorf("m25 endpoint = %q", cfg.Models["m25"].Endpoint)
	}
	if cfg.Models["glm5"].Endpoint != "https://inference.internal:8002/v1" {
		t.Errorf("glm5 endpoint = %q", cfg.Models["glm5"].Endpoint)
	}
	if cfg.Models["m25"].MaxContext != 1000000 {
		t.Errorf("m25 max_context = %d", cfg.Models["m25"].MaxContext)
	}
	if cfg.Routing.DefaultModel != "m25" {
		t.Errorf("default_model = %q", cfg.Routing.DefaultModel)
	}
	if cfg.Memory.DriftThreshold != 0.7 {
		t.Errorf("drift_threshold = %f", cfg.Memory.DriftThreshold)
	}
	if cfg.Tools.BashTimeoutSeconds != 30 {
		t.Errorf("bash_timeout = %d", cfg.Tools.BashTimeoutSeconds)
	}
}

// TestScenario_Config_DefaultValues maps to:
// Scenario: Default values applied
func TestScenario_Config_DefaultValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	// Minimal config — only model endpoints
	content := `
[models.m25]
endpoint = "https://inference.internal:8001/v1"
dialect = "minimax"
max_context = 1000000
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if cfg.Routing.DefaultModel != "m25" {
		t.Errorf("default_model = %q, want %q", cfg.Routing.DefaultModel, "m25")
	}
	if cfg.Routing.ContextDepthThreshold != 32000 {
		t.Errorf("context_depth_threshold = %d, want 32000", cfg.Routing.ContextDepthThreshold)
	}
	if cfg.Routing.ToolDepthThreshold != 5 {
		t.Errorf("tool_depth_threshold = %d, want 5", cfg.Routing.ToolDepthThreshold)
	}
	if cfg.Memory.CheckpointIntervalTurns != 5 {
		t.Errorf("checkpoint_interval_turns = %d, want 5", cfg.Memory.CheckpointIntervalTurns)
	}
	if cfg.Memory.DriftThreshold != 0.7 {
		t.Errorf("drift_threshold = %f, want 0.7", cfg.Memory.DriftThreshold)
	}
	if cfg.Tools.BashTimeoutSeconds != 30 {
		t.Errorf("bash_timeout = %d, want 30", cfg.Tools.BashTimeoutSeconds)
	}
}

// TestScenario_Config_FileMissing maps to:
// Scenario: Config file missing
func TestScenario_Config_FileMissing(t *testing.T) {
	_, err := Load("/nonexistent/path/config.toml")
	if err == nil {
		t.Fatal("expected error for missing config")
	}
	if !IsNotFound(err) {
		t.Errorf("expected not-found error, got: %v", err)
	}
}

// TestScenario_Config_MalformedTOML maps to:
// Scenario: Malformed TOML
func TestScenario_Config_MalformedTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	content := `
[models.m25
endpoint = "broken
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for malformed TOML")
	}
	if !IsMalformed(err) {
		t.Errorf("expected malformed error, got: %v", err)
	}
}

// TestScenario_Config_MissingRequiredEndpoint maps to:
// Scenario: Missing required model endpoint
func TestScenario_Config_MissingRequiredEndpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	content := `
[routing]
default_model = "m25"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing model endpoint")
	}
	if !IsValidation(err) {
		t.Errorf("expected validation error, got: %v", err)
	}
}

// TestScenario_Config_VaultOptional maps to:
// Scenario: Vault config optional
func TestScenario_Config_VaultOptional(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	content := `
[models.m25]
endpoint = "https://inference.internal:8001/v1"
dialect = "minimax"
max_context = 1000000
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if cfg.Vault != nil {
		t.Error("expected Vault to be nil when not configured")
	}
}

// TestScenario_Config_VaultWithToken maps to:
// Scenario: Vault config with token
func TestScenario_Config_VaultWithToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	content := `
[models.m25]
endpoint = "https://inference.internal:8001/v1"
dialect = "minimax"
max_context = 1000000

[vault]
url = "https://vault.internal:9090"
token = "team-secret"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if cfg.Vault == nil {
		t.Fatal("expected Vault to be configured")
	}
	if cfg.Vault.URL != "https://vault.internal:9090" {
		t.Errorf("vault url = %q", cfg.Vault.URL)
	}
	if cfg.Vault.Token != "team-secret" {
		t.Errorf("vault token = %q", cfg.Vault.Token)
	}
}

// TestScenario_Config_UnknownDialect verifies that an unrecognized dialect
// string is rejected with a validation error (ADV-2 fix). Before the fix, a
// typo like "minimx" was silently accepted and fell through to the default
// minimax branch in resolveDialect.
func TestScenario_Config_UnknownDialect(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	content := `
[models.m25]
endpoint = "https://inference.internal:8001/v1"
dialect = "minimx"
max_context = 1000000
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for unknown dialect")
	}
	if !IsValidation(err) {
		t.Errorf("expected validation error, got: %v", err)
	}
}

// TestScenario_Config_LegacyDialectsAccepted verifies that pre-ADR-007 config
// strings ("glm5", "minimax_m25") still load successfully so users aren't
// forced to migrate config files to upgrade. The session layer normalises
// these to family names via normalizeDialect.
func TestScenario_Config_LegacyDialectsAccepted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	content := `
[models.m25]
endpoint = "https://inference.internal:8001/v1"
dialect = "minimax_m25"
max_context = 1000000

[models.glm5]
endpoint = "https://inference.internal:8002/v1"
dialect = "glm5"
max_context = 200000

[routing]
default_model = "m25"
deep_model = "glm5"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("legacy dialect strings should load, got: %v", err)
	}
	if cfg.Models["m25"].Dialect != "minimax_m25" {
		t.Errorf("m25 dialect = %q, want %q (strings are not rewritten at load)", cfg.Models["m25"].Dialect, "minimax_m25")
	}
}

// TestScenario_Config_DeepModelNoEndpoint verifies that a deep_model value
// with no matching [models.<name>] entry is rejected. Without this check,
// escalation to a non-existent model would fail at runtime with a less
// obvious error.
func TestScenario_Config_DeepModelNoEndpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	content := `
[models.m25]
endpoint = "https://inference.internal:8001/v1"
dialect = "minimax"
max_context = 1000000

[routing]
default_model = "m25"
deep_model = "glm5"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for deep_model with no endpoint")
	}
	if !IsValidation(err) {
		t.Errorf("expected validation error, got: %v", err)
	}
}
