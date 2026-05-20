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

// errConfigBootstrapRace is returned when two ghyll processes attempt
// the first-run config bootstrap simultaneously: the loser races the
// O_EXCL open AND finds the file unparseable / missing on the
// follow-up Load. Distinct from errConfigBootstrapped because the
// loser did NOT write — it lost the race. main treats both as
// "non-fatal, ask the operator to retry"; downstream tooling can
// distinguish via errors.Is.
var errConfigBootstrapRace = errors.New("config: concurrent first-run bootstrap detected, retry")

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
//
// Race handling (H-B post-prod-readiness adversarial): if a second
// process writes the file between our Load and our O_EXCL open, the
// open fails with EEXIST. Rather than surfacing a confusing "write
// default config: file exists" error to the operator, we re-Load and
// return the parsed cfg — the racing process wrote the same template
// we would have written, so the loser is free to proceed. If the
// follow-up Load surfaces a real error (corrupted file, partial
// write, vanished entry), we propagate it so the operator sees the
// underlying diagnostic.
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
	// file appeared between Load and OpenFile we treat the EEXIST as
	// "racing process won the write" and re-Load instead of erroring.
	f, openErr := os.OpenFile(configPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if openErr != nil {
		if errors.Is(openErr, os.ErrExist) {
			cfg, loadErr := config.Load(configPath)
			if loadErr == nil {
				// Racing process wrote a valid config; proceed as if
				// we'd done the write ourselves but skip the "edit
				// endpoints" hint — the user already saw it (or is
				// about to via the racing process).
				return cfg, nil
			}
			if config.IsNotFound(loadErr) {
				// File disappeared between EEXIST and our re-Load —
				// probably manual cleanup mid-race. Surface a clear
				// race sentinel so the operator retries.
				return nil, errConfigBootstrapRace
			}
			// Parse or validation error on the racing process's
			// write. Surface the real diagnostic.
			return nil, loadErr
		}
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
