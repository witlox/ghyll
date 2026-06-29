package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	if cfg.Tools.MaxCallDepth != 200 {
		t.Errorf("max_call_depth = %d, want 200", cfg.Tools.MaxCallDepth)
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
	// AUTH-5 / AUTH-W-006 / ADV-AUTH-002: vault.token requires
	// 0o600 mode (consistent with model api_keys).
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
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

// Validation-pass-8 R11: applyDefaults sets GateFloorEscalateAtRank=2
// when the field is omitted (TOML decodes as zero).
func TestScenario_Config_GateFloorEscalateAtRankDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[models.m25]
endpoint = "http://localhost:8001/v1"
dialect = "minimax"
max_context = 1000000

[routing]
default_model = "m25"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Routing.GateFloorEscalateAtRank != 2 {
		t.Errorf("default GateFloorEscalateAtRank = %d; want 2", cfg.Routing.GateFloorEscalateAtRank)
	}
}

// Validation-pass-8 R2: out-of-range gate floor escalation rank
// rejected at validation time.
func TestScenario_Config_GateFloorEscalateAtRankOutOfRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[models.m25]
endpoint = "http://localhost:8001/v1"
dialect = "minimax"
max_context = 1000000

[routing]
default_model = "m25"
gate_floor_escalate_at_rank = 4
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("rank=4 should fail validation")
	}
}

// Validation-pass-8 D1: new dialect families accepted by validator.
func TestScenario_Config_AcceptsNewDialectFamilies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[models.deepseek]
endpoint = "http://localhost:8001/v1"
dialect = "deepseek"
max_context = 65000

[models.qwen-coder-q4]
endpoint = "http://localhost:8002/v1"
dialect = "qwen"
max_context = 32000

[routing]
default_model = "deepseek"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Errorf("deepseek+qwen config should validate; got %v", err)
	}
}

// TestConfig_AcceptsKimiFamily — the Kimi family is accepted by
// the validator (both short and provider-qualified forms).
func TestConfig_AcceptsKimiFamily(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[models.kimi-k26]
endpoint = "https://ai-gateway.svc.cscs.ch/v1"
dialect = "kimi"
max_context = 200000

[models.kimi-mq]
endpoint = "https://moonshot.example/v1"
dialect = "moonshotai/kimi-k2.6"
max_context = 200000

[routing]
default_model = "kimi-k26"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Errorf("Kimi config should validate; got %v", err)
	}
}

// TestConfig_AcceptsMixedCaseKimiDialect — KIMI-CFG-2 / K-ADV-3 fix.
// Operators pasting the literal mixed-case model id docs show (e.g.
// `moonshotai/Kimi-K2.6`) used to fail validation because the
// knownDialects map was case-sensitive. CanonicalDialectFamily now
// lowercases before lookup, so the documented literal Loads OK.
func TestConfig_AcceptsMixedCaseKimiDialect(t *testing.T) {
	cases := []string{
		"moonshotai/Kimi-K2.6", // canonical literal id
		"MOONSHOTAI/kimi-k2.5", // shout-case operator typo
		"Kimi",                 // capitalised short form
		"KIMI-K2.6",            // all-caps short form
	}
	for _, d := range cases {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.toml")
		content := fmt.Sprintf(`
[models.kimi-mq]
endpoint = "https://moonshot.example/v1"
dialect = %q
max_context = 200000

[routing]
default_model = "kimi-mq"
`, d)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err != nil {
			t.Errorf("dialect %q should load (case-insensitive Kimi alias); got %v", d, err)
		}
	}
}

// TestConfig_AcceptsBareKimiK2ShortForms — KIMI-CFG-1 / K-ADV-4 fix.
// The aliases `kimi-k2.5` and `kimi-k2.6` are listed in session.go's
// docstring as accepted, but used to fail config.Load because the
// knownDialects map only contained the provider-qualified forms.
func TestConfig_AcceptsBareKimiK2ShortForms(t *testing.T) {
	cases := []string{"kimi-k2.5", "kimi-k2.6"}
	for _, d := range cases {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.toml")
		content := fmt.Sprintf(`
[models.kimi-mq]
endpoint = "https://moonshot.example/v1"
dialect = %q
max_context = 200000

[routing]
default_model = "kimi-mq"
`, d)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err != nil {
			t.Errorf("dialect %q should load (bare Kimi short form); got %v", d, err)
		}
	}
}

// TestConfig_KnownDialectFamiliesList_StableOrdering — KIMI-CFG-6 /
// CONFIG-1 fix. config and session emit the same error-message
// "known families" list because both layers consume the SAME slice.
// This test pins the canonical order so a future re-order doesn't
// drift the two error UX surfaces apart.
func TestConfig_KnownDialectFamiliesList_StableOrdering(t *testing.T) {
	got := KnownDialectFamiliesList()
	want := "minimax, glm, deepseek, qwen, kimi, kimi-code"
	if got != want {
		t.Errorf("KnownDialectFamiliesList() = %q, want %q (drift between config and session error UX)", got, want)
	}
}

// TestConfig_Validate_RejectsUnknownKimiVariant — loud refusal on
// an unsupported Kimi alias. The K2-Thinking model is deferred; an
// operator who pasted dialect = "kimi-tgi-mode" or "kimi-thinking"
// hoping for that surface MUST see a validation error naming the
// known families.
func TestConfig_Validate_RejectsUnknownKimiVariant(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[models.kimi-thinking]
endpoint = "http://localhost:8001/v1"
dialect = "kimi-tgi-mode"
max_context = 200000

[routing]
default_model = "kimi-thinking"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for unknown kimi variant")
	}
	if !IsValidation(err) {
		t.Errorf("expected validation error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "kimi") {
		t.Errorf("error must name the kimi family in known list: %v", err)
	}
}

// TestConfig_AcceptsKimiCodeFamily — the kimi-code family (Moonshot
// Cloud API with standard OpenAI-compatible tool-call IDs) is
// accepted by config.Load. This covers kimi-for-coding,
// moonshot-v1-*, and the kimi-k2.7-code coding model.
func TestConfig_AcceptsKimiCodeFamily(t *testing.T) {
	aliases := []string{
		"kimi-code",
		"kimi-for-coding",
		"moonshot-v1-8k",
		"moonshot-v1-32k",
		"moonshot-v1-128k",
		"kimi-k2.7-code",
		"kimi-k2-7-code",
		"kimi-k2.7-code-highspeed",
	}
	for _, d := range aliases {
		d := d
		t.Run(d, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			content := fmt.Sprintf(`
[models.kimi-cloud]
endpoint = "https://api.moonshot.cn/v1"
dialect = %q
max_context = 262144

[routing]
default_model = "kimi-cloud"
`, d)
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err != nil {
				t.Errorf("dialect %q should load; got %v", d, err)
			}
		})
	}
}
