package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/witlox/ghyll/config"
)

// TestScenario_EnsureConfig_WritesDefaultWhenMissing covers the C-2
// auto-write path: a fresh install with no ~/.ghyll/config.toml gets
// the embedded template written verbatim, and the bootstrap sentinel
// is returned so cmdRun exits cleanly.
func TestScenario_EnsureConfig_WritesDefaultWhenMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	configPath := filepath.Join(os.Getenv("HOME"), ".ghyll", "config.toml")

	cfg, err := ensureConfig(configPath)
	if !errors.Is(err, errConfigBootstrapped) {
		t.Fatalf("want errConfigBootstrapped, got cfg=%v err=%v", cfg, err)
	}
	if cfg != nil {
		t.Fatalf("expected nil cfg on bootstrap, got %+v", cfg)
	}

	written, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("read written config: %v", readErr)
	}
	if !bytes.Equal(written, config.DefaultTemplate()) {
		t.Fatalf("written config does not match embedded template (len %d vs %d)",
			len(written), len(config.DefaultTemplate()))
	}

	// 0o600 perms on the file itself — config may carry endpoint
	// tokens / URLs that look like secrets. Skip on platforms
	// without POSIX permission bits.
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(configPath)
		if statErr != nil {
			t.Fatalf("stat config: %v", statErr)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("config perm = %o, want 0600", perm)
		}
	}
}

// TestScenario_EnsureConfig_DoesNotClobberExisting guards the invariant
// that the auto-write fires only on ErrConfigNotFound. A pre-existing
// file (even an invalid one) must propagate its own error, never be
// overwritten by the template.
func TestScenario_EnsureConfig_DoesNotClobberExisting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".ghyll")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	configPath := filepath.Join(configDir, "config.toml")

	// Existing file: malformed TOML — Load returns a parse error,
	// NOT not-found. ensureConfig must surface that error rather
	// than overwriting with the template.
	original := []byte("this is not = valid [ toml\n")
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	_, err := ensureConfig(configPath)
	if err == nil {
		t.Fatal("expected error from malformed existing config, got nil")
	}
	if errors.Is(err, errConfigBootstrapped) {
		t.Fatal("ensureConfig clobbered an existing (malformed) file")
	}

	after, _ := os.ReadFile(configPath)
	if !bytes.Equal(after, original) {
		t.Fatal("ensureConfig overwrote an existing config")
	}
}

// TestScenario_EnsureConfig_LoadsValidExisting confirms the happy path:
// when the file is present and valid, ensureConfig returns the parsed
// config and no error.
func TestScenario_EnsureConfig_LoadsValidExisting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".ghyll")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	configPath := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configPath, config.DefaultTemplate(), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	cfg, err := ensureConfig(configPath)
	if err != nil {
		t.Fatalf("ensureConfig on valid existing file: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected cfg, got nil")
	}
	if _, ok := cfg.Models["m25"]; !ok {
		t.Fatalf("template-derived config missing m25 model: %+v", cfg.Models)
	}
}

// TestScenario_EnsureConfig_CreatesParentDir confirms the bootstrap
// path also creates the ~/.ghyll directory when it does not yet
// exist. Belt-and-braces: a brand-new $HOME has no .ghyll/ subdir.
func TestScenario_EnsureConfig_CreatesParentDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".ghyll", "config.toml")
	// Sanity: parent must not exist yet.
	if _, statErr := os.Stat(filepath.Dir(configPath)); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("expected fresh $HOME with no .ghyll dir, got stat err=%v", statErr)
	}

	_, err := ensureConfig(configPath)
	if !errors.Is(err, errConfigBootstrapped) {
		t.Fatalf("want bootstrap sentinel, got %v", err)
	}
	if _, statErr := os.Stat(configPath); statErr != nil {
		t.Fatalf("config not written: %v", statErr)
	}
}
