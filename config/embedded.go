package config

import _ "embed"

// defaultTemplate holds the canonical example config TOML. The
// example.toml file lives next to this file inside the config/
// package directory so go:embed can resolve it without a build tag
// or generator step.
//
// Shipped to operators verbatim on first run when ~/.ghyll/config.toml
// is missing (C-2): the binary is self-sufficient and does not depend
// on the release tarball carrying example.toml.
//
//go:embed example.toml
var defaultTemplate []byte

// DefaultTemplate returns the embedded default config TOML. The
// returned slice is a fresh copy — callers may mutate it freely
// (e.g., to inject env-specific endpoints) without poisoning the
// shared backing array.
func DefaultTemplate() []byte {
	out := make([]byte, len(defaultTemplate))
	copy(out, defaultTemplate)
	return out
}
