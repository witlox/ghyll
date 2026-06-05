package config

import (
	"os"
	"strings"
	"testing"
)

func TestScenario_Config_ResolveAPIKey_TOMLOnly(t *testing.T) {
	cfg := &Config{
		Models: map[string]ModelConfig{
			"cscs-glm5": {APIKey: "toml-key"},
		},
	}
	t.Setenv("GHYLL_API_KEY", "")
	t.Setenv("GHYLL_API_KEY_CSCS_GLM5", "")
	got, src := ResolveAPIKeyWithSource(cfg, "cscs-glm5")
	if got != "toml-key" {
		t.Fatalf("ResolveAPIKey = %q, want %q", got, "toml-key")
	}
	if src != APIKeyFromTOML {
		t.Fatalf("source = %v, want APIKeyFromTOML", src)
	}
}

func TestScenario_Config_ResolveAPIKey_GlobalEnvOverridesTOML(t *testing.T) {
	cfg := &Config{
		Models: map[string]ModelConfig{
			"cscs-glm5": {APIKey: "toml-key"},
		},
	}
	t.Setenv("GHYLL_API_KEY", "env-global")
	t.Setenv("GHYLL_API_KEY_CSCS_GLM5", "")
	got, src := ResolveAPIKeyWithSource(cfg, "cscs-glm5")
	if got != "env-global" {
		t.Fatalf("ResolveAPIKey = %q, want %q", got, "env-global")
	}
	if src != APIKeyFromEnv {
		t.Fatalf("source = %v, want APIKeyFromEnv", src)
	}
}

func TestScenario_Config_ResolveAPIKey_ScopedEnvOverridesGlobal(t *testing.T) {
	cfg := &Config{
		Models: map[string]ModelConfig{
			"cscs-glm5": {APIKey: "toml-key"},
		},
	}
	t.Setenv("GHYLL_API_KEY", "env-global")
	t.Setenv("GHYLL_API_KEY_CSCS_GLM5", "env-scoped")
	got, src := ResolveAPIKeyWithSource(cfg, "cscs-glm5")
	if got != "env-scoped" {
		t.Fatalf("ResolveAPIKey = %q, want %q", got, "env-scoped")
	}
	if src != APIKeyFromEnv {
		t.Fatalf("source = %v, want APIKeyFromEnv", src)
	}
}

func TestScenario_Config_NormalizeModelEnvKey_NonAlphanumReplaced(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"cscs-glm5", "CSCS_GLM5"},
		{"qwen-coder@gpu1", "QWEN_CODER_GPU1"},
		{"", ""},
		{"M25", "M25"},
		{"m25", "M25"},
		{"a.b.c", "A_B_C"},
	}
	for _, c := range cases {
		got := normalizeModelEnvKey(c.in)
		if got != c.want {
			t.Errorf("normalizeModelEnvKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestScenario_Config_ResolveAPIKey_EmptyReturnsEmpty(t *testing.T) {
	t.Setenv("GHYLL_API_KEY", "")
	t.Setenv("GHYLL_API_KEY_CSCS_GLM5", "")

	if got := ResolveAPIKey(nil, ""); got != "" {
		t.Errorf("nil cfg empty model = %q, want empty", got)
	}
	if got := ResolveAPIKey(nil, "cscs-glm5"); got != "" {
		t.Errorf("nil cfg known model = %q, want empty", got)
	}

	cfg := &Config{Models: map[string]ModelConfig{}}
	if got := ResolveAPIKey(cfg, "cscs-glm5"); got != "" {
		t.Errorf("missing model = %q, want empty", got)
	}

	cfgZero := &Config{Models: map[string]ModelConfig{"x": {}}}
	if got := ResolveAPIKey(cfgZero, "x"); got != "" {
		t.Errorf("zero-value APIKey = %q, want empty", got)
	}
}

func TestScenario_Config_ResolveAPIKey_MixedCaseTOMLKey(t *testing.T) {
	// TOML key is mixed-case; lookup into cfg.Models must use the
	// exact key, but the env normalization upper-cases it.
	cfg := &Config{
		Models: map[string]ModelConfig{
			"CSCS-GLM5": {APIKey: "toml-mixed"},
		},
	}
	t.Setenv("GHYLL_API_KEY", "")
	t.Setenv("GHYLL_API_KEY_CSCS_GLM5", "env-scoped-upper")

	got := ResolveAPIKey(cfg, "CSCS-GLM5")
	if got != "env-scoped-upper" {
		t.Fatalf("expected env-scoped-upper (env wins), got %q", got)
	}
}

// TestScenario_Config_ResolveAPIKey_TrimsWhitespace asserts AUTH-3
// / AUTH-W-009: a trailing newline (from clipboard paste or
// `export GHYLL_API_KEY=$(cat keyfile)`) is stripped at the
// resolver so the eventual Authorization header is well-formed.
func TestScenario_Config_ResolveAPIKey_TrimsWhitespace(t *testing.T) {
	cfg := &Config{
		Models: map[string]ModelConfig{
			"m": {APIKey: "  sk-toml-x  \n"},
		},
	}
	t.Setenv("GHYLL_API_KEY", "")
	t.Setenv("GHYLL_API_KEY_M", "")
	got := ResolveAPIKey(cfg, "m")
	if got != "sk-toml-x" {
		t.Fatalf("trim failed: got %q, want %q", got, "sk-toml-x")
	}

	// Env-set case (with trailing newline as from $(cat)).
	t.Setenv("GHYLL_API_KEY_M", "sk-env-x\n")
	got = ResolveAPIKey(cfg, "m")
	if got != "sk-env-x" {
		t.Fatalf("env trim failed: got %q, want %q", got, "sk-env-x")
	}
}

// TestScenario_Config_NormalizeModelEnvKey_RuneAware asserts
// AUTH-W-008: rune iteration prevents two distinct UTF-8 names from
// collapsing to the same byte-count of underscores.
func TestScenario_Config_NormalizeModelEnvKey_RuneAware(t *testing.T) {
	a := normalizeModelEnvKey("qwen-é") // U+00E9 — one rune
	b := normalizeModelEnvKey("qwen-ê") // U+00EA — one rune
	// Both normalize to the same shape (each non-ASCII rune → one
	// underscore) — this is intentional. The collision-detection in
	// validate() (env-bucket loop) catches the ambiguous case so an
	// operator can rename. The point of this test is to assert ONE
	// underscore per rune, not two-per-byte (which would have made
	// the byte-iteration variant produce QWEN___ and silently lose
	// the distinct-name property in env lookups).
	if a != "QWEN__" {
		t.Fatalf("qwen-é normalize = %q, want QWEN__", a)
	}
	if b != "QWEN__" {
		t.Fatalf("qwen-ê normalize = %q, want QWEN__", b)
	}
	// ASCII-only inputs still produce the expected results.
	if normalizeModelEnvKey("qwen-abc") != "QWEN_ABC" {
		t.Fatal("ascii regression")
	}
}

// TestScenario_Config_Validate_RejectsCollidingEnvBuckets asserts
// AUTH-2: two TOML model keys that normalize to the same env-var
// bucket are rejected at Load so the operator can rename one.
func TestScenario_Config_Validate_RejectsCollidingEnvBuckets(t *testing.T) {
	dir := t.TempDir()
	cfgPath := dir + "/config.toml"
	const data = `
[models."cscs-glm5"]
endpoint = "https://x/v1"
dialect = "glm"

[models."cscs_glm5"]
endpoint = "https://y/v1"
dialect = "glm"

[routing]
default_model = "cscs-glm5"
`
	if err := os.WriteFile(cfgPath, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected validation error for colliding env buckets")
	}
	if !IsValidation(err) {
		t.Fatalf("not a validation error: %v", err)
	}
	if !strings.Contains(err.Error(), "normalize to env var") {
		t.Fatalf("error missing collision hint: %v", err)
	}
}

// TestScenario_Config_Validate_RejectsAPIKeyControlCharacters
// asserts AUTH-3 / AUTH-W-009: a TOML api_key with an embedded
// CRLF / newline is rejected at Load.
func TestScenario_Config_Validate_RejectsAPIKeyControlCharacters(t *testing.T) {
	dir := t.TempDir()
	cfgPath := dir + "/config.toml"
	data := "[models.m]\nendpoint = \"https://x/v1\"\ndialect = \"glm\"\napi_key = \"sk-x\\nsmuggle: header\"\n[routing]\ndefault_model = \"m\"\n"
	if err := os.WriteFile(cfgPath, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected validation error for control char in api_key")
	}
	if !IsValidation(err) {
		t.Fatalf("not a validation error: %v", err)
	}
	if !strings.Contains(err.Error(), "control character") {
		t.Fatalf("error missing control-character hint: %v", err)
	}
}

// TestScenario_Config_Validate_RejectsHTTPWithSecret asserts AUTH-7:
// http:// endpoints with api_key set are refused unless the host is
// loopback.
func TestScenario_Config_Validate_RejectsHTTPWithSecret(t *testing.T) {
	cases := []struct {
		name        string
		endpoint    string
		shouldError bool
	}{
		{"http_remote", "http://example.com/v1", true},
		{"http_loopback_v4", "http://127.0.0.1:8000/v1", false},
		{"http_loopback_localhost", "http://localhost/v1", false},
		{"http_loopback_v6", "http://[::1]:9000/v1", false},
		{"https_remote", "https://example.com/v1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := dir + "/config.toml"
			data := "[models.m]\nendpoint = \"" + tc.endpoint + "\"\ndialect = \"glm\"\napi_key = \"sk-x\"\n[routing]\ndefault_model = \"m\"\n"
			if err := os.WriteFile(cfgPath, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(cfgPath)
			if tc.shouldError && err == nil {
				t.Fatalf("expected error for %s", tc.endpoint)
			}
			if !tc.shouldError && err != nil {
				t.Fatalf("unexpected error for %s: %v", tc.endpoint, err)
			}
		})
	}
}

// TestScenario_Config_Validate_RejectsSecretInStampLabel asserts
// AUTH-W-005 / ADV-AUTH-005: stamp_label MUST NOT embed an api_key
// or bearer token; the validator catches obvious misconfig.
func TestScenario_Config_Validate_RejectsSecretInStampLabel(t *testing.T) {
	dir := t.TempDir()
	cfgPath := dir + "/config.toml"
	data := `[models.m]
endpoint = "https://x/v1"
dialect = "glm"
stamp_label = "glm5@sk-abcdef0123456789"
[routing]
default_model = "m"
`
	if err := os.WriteFile(cfgPath, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected validation error for secret-shaped stamp_label")
	}
	if !IsValidation(err) {
		t.Fatalf("not a validation error: %v", err)
	}
	if !strings.Contains(err.Error(), "stamp_label") {
		t.Fatalf("error missing stamp_label hint: %v", err)
	}
}

// TestScenario_Config_Load_RejectsMisspelledAPIKey asserts AUTH-1 /
// AUTH-6: a misspelled `api_token` (or `apikey`, `bearer`, `token`,
// `auth_token`) under [models.*] is caught at Load with a directed
// "did you mean api_key?" hint.
func TestScenario_Config_Load_RejectsMisspelledAPIKey(t *testing.T) {
	cases := []string{"api_token", "auth_token", "bearer", "token", "apikey", "secret"}
	for _, mis := range cases {
		t.Run(mis, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := dir + "/config.toml"
			data := "[models.m]\nendpoint = \"https://x/v1\"\ndialect = \"glm\"\n" + mis + " = \"sk-x\"\n[routing]\ndefault_model = \"m\"\n"
			if err := os.WriteFile(cfgPath, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(cfgPath)
			if err == nil {
				t.Fatalf("expected validation error for %q", mis)
			}
			if !IsValidation(err) {
				t.Fatalf("expected validation error class, got %v", err)
			}
			if !strings.Contains(err.Error(), "api_key") {
				t.Fatalf("error missing did-you-mean hint: %v", err)
			}
		})
	}
}

// TestScenario_Config_Load_RejectsGroupReadableConfigWithSecret
// asserts AUTH-5 / AUTH-W-006 / ADV-AUTH-002: a config.toml with
// group/other read perms AND an api_key inside is refused at Load.
func TestScenario_Config_Load_RejectsGroupReadableConfigWithSecret(t *testing.T) {
	dir := t.TempDir()
	cfgPath := dir + "/config.toml"
	const data = `[models.m]
endpoint = "https://x/v1"
dialect = "glm"
api_key = "sk-must-be-private"

[routing]
default_model = "m"
`
	if err := os.WriteFile(cfgPath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected validation error for group-readable config with secret")
	}
	if !IsValidation(err) {
		t.Fatalf("not a validation error: %v", err)
	}
	if !strings.Contains(err.Error(), "chmod") {
		t.Fatalf("error missing chmod hint: %v", err)
	}
}

// TestScenario_Config_Load_AllowsGroupReadableConfigWithoutSecret
// asserts the file-mode check only fires when a secret is actually
// in the file (zero-config users with no api_key are unaffected).
func TestScenario_Config_Load_AllowsGroupReadableConfigWithoutSecret(t *testing.T) {
	dir := t.TempDir()
	cfgPath := dir + "/config.toml"
	const data = `[models.m]
endpoint = "https://x/v1"
dialect = "glm"

[routing]
default_model = "m"
`
	if err := os.WriteFile(cfgPath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(cfgPath); err != nil {
		t.Fatalf("expected no error for chmod-644 config without secret: %v", err)
	}
}
