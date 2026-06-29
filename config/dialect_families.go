package config

import "strings"

// KnownDialectFamilies is the canonical list of supported dialect
// family names. It is the SINGLE source of truth consumed by both
// config.Load() (for validation + error-message formatting) and
// cmd/ghyll/session.go's normalizeDialect (for routing).
//
// Adding a new dialect family is one append here. Adding a new
// alias to an existing family is one entry in dialectAliases below.
// The two validators stay in lockstep by construction; the prior
// drift hazard (KIMI-CFG-1 / KIMI-CFG-6 / CONFIG-1) is structurally
// removed.
var KnownDialectFamilies = []string{
	"minimax",
	"glm",
	"deepseek",
	"qwen",
	"kimi",
	"kimi-code",
}

// dialectAliases maps every accepted wire-form alias (case-folded)
// to its canonical family name. The lookup is performed by
// CanonicalDialectFamily which lowercases the input first, so an
// operator pasting `moonshotai/Kimi-K2.6` (the literal mixed-case
// model id the docs show) matches `moonshotai/kimi-k2.6` and routes
// to family `kimi`.
//
// The empty string deliberately maps to `minimax` per the historical
// "no dialect field" default (see config/example.toml).
var dialectAliases = map[string]string{
	// minimax family
	"":            "minimax",
	"minimax":     "minimax",
	"minimax_m25": "minimax",
	"minimax_m27": "minimax",

	// glm family
	"glm":   "glm",
	"glm5":  "glm",
	"glm51": "glm",

	// deepseek family
	"deepseek":          "deepseek",
	"deepseek-v3":       "deepseek",
	"deepseek-coder":    "deepseek",
	"deepseek-coder-v3": "deepseek",

	// qwen family
	"qwen":          "qwen",
	"qwen-coder":    "qwen",
	"qwen2.5-coder": "qwen",
	"qwen3-coder":   "qwen",

	// kimi family — short, hyphenated, and provider-qualified forms.
	// Both `kimi-k2.5` / `kimi-k2.6` AND the provider-prefixed
	// `moonshotai/kimi-k2.x` are accepted; case is folded at the
	// lookup boundary so the documented mixed-case literal
	// `moonshotai/Kimi-K2.6` is honoured too.
	"kimi":                 "kimi",
	"kimi-2.5":             "kimi",
	"kimi-2.6":             "kimi",
	"kimi-k2":              "kimi",
	"kimi-k2.5":            "kimi",
	"kimi-k2.6":            "kimi",
	"moonshotai/kimi-k2.5": "kimi",
	"moonshotai/kimi-k2.6": "kimi",

	// kimi-code family — Moonshot Cloud API models accessed via
	// api.moonshot.cn/v1 (or api.moonshot.ai/v1). These return standard
	// OpenAI-compatible tool-call IDs (UUIDs / opaque strings) rather
	// than the `functions.<name>:<index>` shape emitted by self-hosted
	// K2 (vLLM/SGLang). The kimi-code dialect does NOT enforce any ID
	// shape. Use the kimi family for self-hosted K2 deployments.
	//
	// kimi-k2.7-code (kimi-k2-7-code) is a 1T-parameter MoE model
	// optimized for coding. It is served via the Moonshot Cloud API
	// and uses standard OpenAI-compatible tool calls.
	"kimi-code":                            "kimi-code",
	"kimi-for-coding":                      "kimi-code",
	"moonshot-v1-8k":                       "kimi-code",
	"moonshot-v1-32k":                      "kimi-code",
	"moonshot-v1-128k":                     "kimi-code",
	"moonshotai/kimi-for-coding":           "kimi-code",
	"kimi-code/kimi-for-coding":            "kimi-code",
	"kimi-k2.7-code":                       "kimi-code",
	"kimi-k2-7-code":                       "kimi-code",
	"kimi-k2.7-code-highspeed":             "kimi-code",
	"kimi-k2-7-code-highspeed":             "kimi-code",
	"moonshotai/kimi-k2.7-code":            "kimi-code",
	"moonshot/kimi-k2-7-code":              "kimi-code",
	"moonshot/kimi-k2-7-code-highspeed":    "kimi-code",
	"@cf/moonshotai/kimi-k2.7-code":        "kimi-code",
}

// CanonicalDialectFamily returns the canonical family name for an
// alias, case-insensitive. ok=false means the alias is not known
// (the caller should emit a "known families: ..." error).
//
// This is the chokepoint that fixes K-ADV-3 / KIMI-CFG-2:
// `moonshotai/Kimi-K2.6` (the literal mixed-case model id every doc
// shows) used to fail the case-sensitive lookup; we now lowercase
// before consulting dialectAliases.
//
// Resolution order:
//  1. Exact match in dialectAliases (handles kimi's explicit
//     whitelist, the empty-string→minimax default, and known
//     historical aliases like minimax_m25 / glm5).
//  2. Prefix match against the permissive families (minimax / glm /
//     deepseek / qwen). This is the legacy permissive contract
//     documented in validation-pass-8 D4: `deepseek-v3.1` /
//     `qwen-coder-q4` and other future variants resolve without
//     having to register every new suffix in dialectAliases.
//
// Kimi deliberately does NOT participate in step 2. The Kimi family
// uses an explicit whitelist to guard against over-matching neighbour
// names like `kimino-coder` and `kimi-tgi-mode` (which should fail
// loudly per the operator-decision in validation-pass-8).
func CanonicalDialectFamily(alias string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(alias))
	if fam, ok := dialectAliases[key]; ok {
		return fam, true
	}
	// Step 2: permissive prefix match. Order does not matter — the
	// prefixes are mutually exclusive (no permissive family is a
	// prefix of another). Kimi is excluded by design.
	for _, prefix := range permissivePrefixes {
		if strings.HasPrefix(key, prefix) {
			return prefix, true
		}
	}
	return "", false
}

// permissivePrefixes is the list of families that accept any alias
// starting with their family name (validation-pass-8 D4 contract).
// Kimi is intentionally absent; it relies on the explicit alias
// whitelist instead.
var permissivePrefixes = []string{
	"minimax",
	"glm",
	"deepseek",
	"qwen",
}

// KnownDialectFamiliesList returns a stable, comma-separated list of
// the family names for use in error messages. Both config and
// session emit the same string so operators chasing a refusal
// across the two layers see identical UX.
func KnownDialectFamiliesList() string {
	return strings.Join(KnownDialectFamilies, ", ")
}
