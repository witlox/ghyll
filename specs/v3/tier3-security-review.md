# Tier 3 Security Review

Cold-context adversarial pass covering the entire codebase
(scope: pre-Tier-2 surfaces NOT closed in
`specs/v2/validation-impl-pass-tier2-adversarial-remediation.md`).

## Counts

| Tier | Total |
|------|------:|
| Critical | 7 |
| High | 13 |
| Medium | 13 |
| Low | 9 |
| Test gaps | 6 |

## Critical

### C-1 — Vault accepts unsigned checkpoints
`vault/server.go:107-137` — `handleCheckpoints` validates only the
hash recomputation; no ed25519 verification. Attacker with a vault
token (or local-network access to a tokenless vault — `isLocalhost`
bypass) POSTs `/v1/checkpoints` with a forged Summary +
self-recomputed Hash. Server accepts + persists. Combined with C-2
this is cross-session prompt injection via "team memory."

### C-2 — Checkpoint Summary flows into system prompt unsanitized
`dialect/glm.go:67-68`, `dialect/minimax.go:64-65`, analogous in
deepseek/qwen. Operator resumes from a poisoned checkpoint;
Summary text interpolates into the system prompt and drives the
compromised model into tool calls before any operator observes.

### C-3 — `WriteCheckpoint` joins attacker-controlled segments
`memory/sync.go:217-237, 326-348`. DeviceID like `"../../../etc/cron.d/evil"`
flows into `filepath.Join` paths. Same path-traversal class as the
gate-2 attestation tree (closed via SEC-C-1); needs the safeSegment
helper hoisted to a shared `internal/safepath`.

### C-4 — `UnmarshalPublicKey` accepts arbitrary-length keys
`memory/keys.go:98-104` casts `block.Bytes` to `ed25519.PublicKey`
with no length check. Same for `loadKey` at line 83. Attacker who
plants a 0-byte / 63-byte / 65-byte pub file causes `ed25519.Verify`
to panic or silently fail-true.

### C-5 — Stream client has no response cap, no read timeout, no scanner-err
`stream/client.go:92,215,283-354`. `bufio.NewScanner(body)` defaults
to 64 KiB; oversize lines silently truncate (scanner.Err discarded).
`contentBuilder.WriteString` unbounded — infinite SSE stream → OOM.
`http.Client.Timeout = 0`.

### C-6 — Vault handlers accept unlimited request bodies
`vault/server.go:87,116`. `json.NewDecoder(r.Body).Decode` with no
`http.MaxBytesReader`. Memory-exhaustion DoS even on tokened
vaults.

### C-7 — `tool/web.go WebSearch` reads response body unbounded
`tool/web.go:190` — `io.ReadAll(resp.Body)` with no LimitReader
(unlike WebFetch at line 95). OOM via malicious search backend.

## High

### H-1 — `BindingEvaluator.Command` executed via `sh -c` with project-controlled string
`runner/subprocess.go:265`. Malicious grid `language-bindings`
trigger RCE on session start; sandbox-only execution accepted by
threat model, but no operator-confirmation gate between grid-load
and first binding execution.

### H-2 — `bootstrap.Read*` no size cap on grid YAML
`bootstrap/grid.go:399-414` unbounded `os.ReadFile` + `yaml.Unmarshal`.
2 GB / alias-bomb grid yaml OOMs the agent at session start.

### H-3 — `workflow.Load` reads instruction files unbounded + follows symlinks
`workflow/loader.go:77-83`. `.ghyll/instructions.md -> /etc/shadow`
flows the content into the system prompt → data exfiltration via
prompt. Same with `.claude/CLAUDE.md` and `commands/*.md`.

### H-4 — `memory.vault_client.go` response decoder unbounded
`memory/vault_client.go:68`. Hostile vault returns multi-MB
Checkpoint.Summary; client OOMs. No scheme allowlist (`https://`-only).

### H-5 — `config.Load` unbounded; no endpoint scheme allowlist
`config/config.go:137,236-296`. 100 MB config.toml OOMs; `file://`
or other scheme endpoints accepted.

### H-6 — `tool/edit.go` permission preservation strips setuid + no symlink guard
`tool/edit.go:68-128`. `os.ReadFile` follows symlinks; CAS doesn't
protect against TOCTOU symlink swap; `Perm()` discards setuid/setgid/
sticky bits silently.

### H-7 — Pathglob exponential blowup
`internal/pathglob/pathglob.go:41-69`. Multi-`**` patterns are
O(n^k). Malicious grid scope DoSes the no-todo-marker scan.

### H-8 — `memory/sync.go` + `tool/git.go` allow `git clone $origin` injection
`memory/sync.go:112-122`. `origin` accepted with no `--` separator;
git accepts magic schemes like `ext::sh -c evil`.

### H-9 — `memory.canonicalJSON` swallows marshal errors
`memory/crypto.go:60-81`. `keyBytes, _ := json.Marshal(k)` —
unmarshalable types produce nil segments → hash collisions →
checkpoint substitution.

### H-10 — Sandbox detection trivially env-spoofable
`cmd/ghyll/sandbox.go:65-104`. `container=docker` / `KUBERNETES_SERVICE_HOST` /
`FIREJAIL_FILE` env vars are operator-writable. Compromised model can
set them and pass `GHYLL_REQUIRE_SANDBOX=1` enforcement.

### H-11 — `BindingEvaluator.WorkingDir` not validated against project root
`runner/subprocess.go:267-269`. `cmd.Dir = b.WorkingDir` with no
containment check.

### H-12 — `memory/sync.go cleanGitEnv` leaks SSH_AUTH_SOCK / GIT_ASKPASS
`memory/sync.go:350-362`. Positive-deny list misses
`SSH_AUTH_SOCK` etc. Combined with H-8 → ssh-agent leak to remote.

### H-13 — Engine `recordReplay` accepts placeholder pass_id permanently
Tier 1→2 migration (`_legacy` / `migrated-*`) is correct for the
migration window but is a permanent acceptance path with no
expiry signal.

## Medium

### M-1 — `tool/web.go` HTML entity decode incomplete; UTF-8 corruption in regex strip
`tool/web.go:291-296, 237-302`. Missing `&#x202E;` (RTL override)
unescape → context-prompt corruption.

### M-2 — `tool/web.go` HTML regex backtracking with no input cap
`tool/web.go:224-235`. `(?is)<script[^>]*>.*?</script>` over MiB
buffer can CPU-stall.

### M-3 — `tool/bash.go` inherits full parent env
`tool/bash.go:20`. ANTHROPIC_API_KEY / GHYLL_VAULT_TOKEN /
SSH_AUTH_SOCK leak to model-issued bash calls.

### M-4 — `tool/file.go WriteFile` clobbers existing permissions to 0644
`tool/file.go:58`. Secrets file goes 0600→0644 silently. Not atomic.

### M-5 — `memory/sync.go ReadCheckpoints` decodes unbounded
`memory/sync.go:312-321`. 2 GB checkpoint file OOMs on read.

### M-6 — `dialect.parseOpenAIToolCalls` no name validation
`dialect/parse.go:18-24`. Tool name with control runes / U+202E
spoofs different tool in UI.

### M-7 — `tool/glob.go` silently dereferences symlinks
`tool/glob.go:99-133`. Symlink-resolved content leaks to subsequent
Read tool calls.

### M-8 — Vault `handleHealth` unauthenticated, leaks deployment state
`vault/server.go:69-74`. Health endpoint helps port-scanning.

### M-9 — `bootstrap/orphan.go ExtractContextSymbols` allows relative projectDir
`bootstrap/orphan.go:81-107`. Race-window for confused-deputy if
CWD changes mid-extraction.

### M-10 — `bootstrap/grid.go Read` doesn't bound ResidueNoteMaxBytes range
`bootstrap/grid.go:140-150`. NormalizeTier2Defaults only fills zero;
attacker grid with `residue-note-max-bytes: 1` or `: 2147483647`
breaks operator residue flow.

### M-11 — `tool/git.go` no subcommand allowlist
`tool/git.go:15-49`. Model can `git config --global core.sshCommand "..."`
to persist hostile config across sessions.

### M-12 — `memory.crypto.go VerifyChain` accepts attacker-supplied chain root
`memory/crypto.go:128-155`. No per-device chain-root pinning.

### M-13 — `cmd/ghyll/sandbox.go truthyEnv` silent-falses unknown values
`cmd/ghyll/sandbox.go:193-199`. `GHYLL_REQUIRE_SANDBOX=disabled` or
`=true_for_now` silently disables enforcement.

## Low

### L-1 — `tool/edit.go` chmod-after-create window
`tool/edit.go:89-109`. Temp file briefly 0644 before chmod.

### L-2 — `tool/commit.go` accepts trailers with leading dash key
`tool/commit.go:79-94`. `trailerKeyOK` doesn't refuse `-X` as key
first char.

### L-3 — `dialect/helpers.go cleanWorkdir stripANSI` only handles CSI
`dialect/helpers.go:53-78`. OSC / SS3 escape sequences pass through.

### L-4 — `memory.crypto.go CanonicalHash` map ordering conditional fields
`memory/crypto.go:21-57`. Forward-compat hazard: new fields can
silently collide.

### L-5 — `engine/recovery.go orphanAbort` no monotonic-clock protection
`engine/recovery.go:338-365`. Clock skew → `closed_at < started_at`.

### L-6 — `stream/client.go` backoff shift overflows for MaxRetries > 31
`stream/client.go:133`. Capped 3 by default but operator-tunable.

### L-7 — `tool/web.go parseSearchResults` no URL scheme validation
`tool/web.go:305-339`. `data:`/`file:` URLs leak into WebFetch.

### L-8 — `bootstrap/profile.go MaxBoundedContexts` not enforced on grid yaml load
`bootstrap/grid.go:48`. 100k contexts via direct load possible.

### L-9 — `memory/keys.go LoadOrGenerateKey` no atomic-rename pub file
`memory/keys.go:57-58`. Crash mid-write leaves truncated pub.

## Test gaps

| ID | Surface |
|---|---|
| TG-1 | vault signature verification (closing C-1) |
| TG-2 | symlink-following in workflow loader (closing H-3) |
| TG-3 | stream-client OOM on infinite SSE (closing C-5) |
| TG-4 | pathglob exponential blowup (closing H-7) |
| TG-5 | binding command shell injection (closing H-1) |
| TG-6 | UnmarshalPublicKey length-bounds (closing C-4) |

## Recommended remediation order

1. **C-1 + C-2 + C-4 (vault signature + sanitize summary + key length)** —
   the headline-coupled pair. Closes prompt injection across sessions.
2. **C-3 + C-7 + C-5 + C-6 (path traversal + size caps)** — all
   share a "limit untrusted input" theme.
3. **H-2 + H-3 + H-4 + H-5 (size caps + symlink guards)** — same
   class; bulk-fixable with a shared `internal/safefile` helper.
4. **H-7 (pathglob)** — bounded pattern depth at grid-load.
5. **H-6 (edit.go symlink TOCTOU)** — `O_NOFOLLOW` + mode preservation.
6. **H-8 + H-12 + M-3 + M-11 (git/bash/env scrubbing)** — operator
   sandbox hardening.
7. **H-1 + H-11 (binding command + workingdir gating)** — operator-
   confirmation requirement.
8. **H-9 (canonical-hash marshal-error)** — `(string, error)` API.
9. **H-10 + M-13 (sandbox env spoofing)** — kernel-namespace check
   over env-only signals.
10. Medium / Low cleanup.
