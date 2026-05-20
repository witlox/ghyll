package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScenario_Load_RefusesFileScheme verifies Tier 3 / SR H-5 —
// model endpoint with file:// scheme is rejected at validation.
func TestScenario_Load_RefusesFileScheme(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
[models.evil]
endpoint = "file:///etc/passwd"
dialect = "minimax"
max_context = 1000

[routing]
default_model = "evil"
context_depth_threshold = 1000
tool_depth_threshold = 5
enable_auto_routing = true
gate_floor_escalate_at_rank = 2
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Error("file:// endpoint accepted; want scheme rejection")
	}
	if !strings.Contains(err.Error(), "scheme") {
		t.Errorf("err = %v; want scheme-rejection", err)
	}
}

// TestScenario_Load_RefusesOversizedConfig verifies SR H-5 size cap.
func TestScenario_Load_RefusesOversizedConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	huge := strings.Repeat("# x\n", 300*1024) // ~1.2 MiB > 1 MiB cap
	if err := os.WriteFile(path, []byte(huge), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Error("oversized config accepted; want size rejection")
	}
}
