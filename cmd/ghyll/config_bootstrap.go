package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/witlox/ghyll/config"
)

// errConfigBootstrapped is returned by ensureConfig when it has just
// written a fresh default config and the caller should exit cleanly
// (so the operator can edit endpoints before re-running). It is NOT
// an error condition in the failure sense — main inspects it and
// returns nil to the OS.
var errConfigBootstrapped = errors.New("config: default written, exit cleanly")

// ensureConfig loads the config at configPath. When config.Load
// reports a not-found error, the embedded default template
// (config.DefaultTemplate) is written to that path with 0o600
// permission (config may contain endpoint URLs that look like
// secrets), the dir is created with 0o700 if needed, and the
// sentinel errConfigBootstrapped is returned so the caller can
// print a hint and exit 0.
//
// Existing files are never clobbered: the auto-write only fires on
// ErrConfigNotFound. Other errors (malformed TOML, validation
// failures, permission denied, file too large, …) propagate
// unchanged so the operator sees the real diagnostic.
func ensureConfig(configPath string) (*config.Config, error) {
	cfg, err := config.Load(configPath)
	if err == nil {
		return cfg, nil
	}
	if !config.IsNotFound(err) {
		return nil, err
	}

	dir := filepath.Dir(configPath)
	if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
		return nil, fmt.Errorf("create config dir %s: %w", dir, mkErr)
	}

	// O_EXCL guards against a concurrent writer racing us — if the
	// file appeared between Load and WriteFile we refuse to clobber.
	f, openErr := os.OpenFile(configPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if openErr != nil {
		return nil, fmt.Errorf("write default config %s: %w", configPath, openErr)
	}
	tmpl := config.DefaultTemplate()
	if _, wErr := f.Write(tmpl); wErr != nil {
		_ = f.Close()
		return nil, fmt.Errorf("write default config %s: %w", configPath, wErr)
	}
	if cErr := f.Close(); cErr != nil {
		return nil, fmt.Errorf("close default config %s: %w", configPath, cErr)
	}
	return nil, errConfigBootstrapped
}
