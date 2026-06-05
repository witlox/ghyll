package main

import (
	"testing"

	"github.com/witlox/ghyll/config"
)

func TestScenario_Auth_BuildAuthHeader_EmptyKeyReturnsNilHeader(t *testing.T) {
	t.Setenv("GHYLL_API_KEY", "")
	t.Setenv("GHYLL_API_KEY_CSCS_GLM5", "")
	cfg := &config.Config{
		Models: map[string]config.ModelConfig{"cscs-glm5": {}},
	}
	got := buildAuthHeader(cfg, "cscs-glm5")
	if got != nil {
		t.Fatalf("empty key must yield nil header, got %v", got)
	}
}

func TestScenario_Auth_BuildAuthHeader_NonEmptyKeySetsBearer(t *testing.T) {
	t.Setenv("GHYLL_API_KEY", "")
	t.Setenv("GHYLL_API_KEY_CSCS_GLM5", "")
	cfg := &config.Config{
		Models: map[string]config.ModelConfig{
			"cscs-glm5": {APIKey: "sk-xyz"},
		},
	}
	got := buildAuthHeader(cfg, "cscs-glm5")
	if got == nil {
		t.Fatalf("expected non-nil header")
	}
	if v := got.Get("Authorization"); v != "Bearer sk-xyz" {
		t.Fatalf("Authorization = %q, want %q", v, "Bearer sk-xyz")
	}
}

func TestScenario_Auth_BuildAuthHeader_EnvOverridesTOML(t *testing.T) {
	t.Setenv("GHYLL_API_KEY", "env-global")
	t.Setenv("GHYLL_API_KEY_CSCS_GLM5", "env-scoped")
	cfg := &config.Config{
		Models: map[string]config.ModelConfig{
			"cscs-glm5": {APIKey: "toml-loser"},
		},
	}
	got := buildAuthHeader(cfg, "cscs-glm5")
	if got == nil {
		t.Fatalf("expected non-nil")
	}
	if v := got.Get("Authorization"); v != "Bearer env-scoped" {
		t.Fatalf("Authorization = %q, want Bearer env-scoped", v)
	}
}

func TestScenario_Auth_RedactKeySource_ReturnsProvenanceTokens(t *testing.T) {
	cfg := &config.Config{
		Models: map[string]config.ModelConfig{
			"a": {APIKey: "toml-key"},
			"b": {}, // no key
		},
	}

	t.Setenv("GHYLL_API_KEY", "")
	t.Setenv("GHYLL_API_KEY_A", "")
	t.Setenv("GHYLL_API_KEY_B", "")

	if got := redactKeySource(cfg, "a"); got != "<toml>" {
		t.Errorf("a: got %q, want <toml>", got)
	}
	if got := redactKeySource(cfg, "b"); got != "<unset>" {
		t.Errorf("b: got %q, want <unset>", got)
	}

	t.Setenv("GHYLL_API_KEY_B", "env-only-b")
	if got := redactKeySource(cfg, "b"); got != "<env>" {
		t.Errorf("b with env: got %q, want <env>", got)
	}

	// Ensure literal never contains the value (length-leak guard).
	t.Setenv("GHYLL_API_KEY_A", "a-very-long-secret-token-xyz")
	got := redactKeySource(cfg, "a")
	if got != "<env>" {
		t.Fatalf("got %q, want <env>", got)
	}
	if len(got) != len("<env>") {
		t.Fatalf("redactor must return fixed-length literal, got len %d", len(got))
	}
}
