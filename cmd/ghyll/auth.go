package main

import (
	"net/http"

	"github.com/witlox/ghyll/config"
)

// buildAuthHeader returns an http.Header carrying the resolved
// Bearer token for the given model, or nil when no api_key is
// configured (neither TOML nor env).
//
// Concrete function, no interface — single source of truth used by
// session.go (3 call sites: initial NewClient, model-switch handoff,
// compaction call), subagent.go, and any future stream.NewClient
// caller.
//
// When the resolved key is empty, we return nil (NOT an empty
// http.Header) so stream.NewClient.opts.ExtraHeaders stays nil and
// the merge-loop in doRequest is a true no-op. An empty Header{}
// would still iterate; nil short-circuits.
func buildAuthHeader(cfg *config.Config, modelName string) http.Header {
	key := config.ResolveAPIKey(cfg, modelName)
	if key == "" {
		return nil
	}
	return http.Header{"Authorization": []string{"Bearer " + key}}
}

// redactKeySource returns one of three fixed provenance literals:
// "<unset>", "<env>", or "<toml>". NEVER returns the value, the
// length, or a prefix — a length leak narrows brute-force; a
// prefix leak narrows tenant identification.
//
// Used by `ghyll config show` so operators can see WHERE a key
// came from (debugging precedence) without seeing the key itself.
func redactKeySource(cfg *config.Config, modelName string) string {
	_, src := config.ResolveAPIKeyWithSource(cfg, modelName)
	switch src {
	case config.APIKeyFromEnv:
		return "<env>"
	case config.APIKeyFromTOML:
		return "<toml>"
	default:
		return "<unset>"
	}
}
