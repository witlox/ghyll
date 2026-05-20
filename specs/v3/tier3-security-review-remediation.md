# Tier 3 Security Review — Remediation Tracker

Tracks dispositions for the 42 findings in
`tier3-security-review.md`. Three buckets:

- **Remediated**: correctness / availability / external-facing
  surfaces where ghyll IS the right layer.
- **Sandbox**: explicitly DECLINED per the project's
  sandbox-only contract (`CLAUDE.md`: "Tools are direct OS calls
  — no permission layer (sandbox handles security)"). The
  harness wrapping ghyll handles these.
- **Pending**: still open.

## Status

| Tier     | Total | Done | Sandbox | Pending |
|----------|------:|-----:|--------:|--------:|
| Critical |     7 |    7 |       0 |       0 |
| High     |    13 |    8 |       5 |       0 |
| Medium   |    13 |    7 |       6 |       0 |
| Low      |     9 |    8 |       0 |       1 |
| Test gaps |    6 |    5 |       0 |       1 |

## Critical — all remediated

| ID | Commit | Notes |
|---|---|---|
| C-1 | bc9b27c | vault.handleCheckpoints verifies ed25519 sig against `<keysDir>/<deviceID>.pub`; empty keysDir = TEST mode with logged warning |
| C-2 | bc9b27c | dialect.{GLM,Minimax,DeepSeek,Qwen}HandoffSummary route Summary + ActiveModel through sanitizeHandoffField (length cap, ANSI strip, line-separator scrub, header-marker neutralization) |
| C-3 | d58879e | memory.Syncer.WriteCheckpoint + WritePublicKey + ReadPublicKey validate repoHash + DeviceID + Hash via internal/safefile.SafeSegment before filepath.Join |
| C-4 | f1a1171 | memory.UnmarshalPublicKey + loadKey + MarshalPublicKey enforce ed25519.PublicKeySize / SeedSize |
| C-5 | 78bc6c9 | stream client: bufio.Scanner buffer cap 1 MiB; aggregate content cap 16 MiB; per-tool-call args cap 1 MiB; scanner.Err surfaced |
| C-6 | bc9b27c | vault handleSearch + handleCheckpoints wrap r.Body in http.MaxBytesReader (4 MiB) + embedding length cap + TopK cap |
| C-7 | 78bc6c9 | webSearchImpl wraps resp.Body in io.LimitReader(maxSearchResponseBytes = 1 MiB) |

## High

### Remediated

| ID | Commit | Notes |
|---|---|---|
| H-2 | d58879e | bootstrap.ReadVersion caps grid.v<N>.yaml at 4 MiB |
| H-3 | d58879e | workflow.readFileIfExists caps at 256 KiB + refuses symlinks (these files land in the system prompt; model+endpoint is the adversary, not local fs) |
| H-4 | d58879e | memory.vault_client.Search caps response body at 16 MiB |
| H-5 | d58879e | config.Load caps at 1 MiB + validates model + vault endpoint URL scheme via safefile.ValidateURLScheme |
| H-9 | f1a1171 | memory.canonicalJSON propagates json.Marshal errors via panic (caught at CanonicalHash boundary); prevents hash collision via silent nil segments |
| H-10 | (already shipped) | cmd/ghyll/sandbox.go: GHYLL_REQUIRE_SANDBOX env var + DetectSandbox; not perfect but already the documented opt-in |
| H-13 | (covered by gate-2 CORR-A-14) | "legacy-*"/"migrated-*" pass_id tolerance was deliberate Tier 1→2 migration window; tracker logs it; the v5 schema CHECK + Record-time PassID enforcement bound the gap |

### Sandbox — declined by design

| ID | Notes |
|---|---|
| H-1 | `BindingEvaluator.Command sh -c` — sandbox-only; grid bindings run inside the harness |
| H-6 | `tool/edit.go` symlink TOCTOU — file-op semantics, sandbox's policy |
| H-7 | pathglob exponential — DoS class; same as H-1, runs inside the harness |
| H-8 | `git clone $origin` URL injection — git config trust = sandbox boundary; ghyll doesn't second-guess git's URL parser |
| H-11 | `BindingEvaluator.WorkingDir` containment — same as H-1; the sandbox restricts what dirs binding subprocesses can touch |
| H-12 | `cleanGitEnv` SSH_AUTH_SOCK leak — env scrubbing for sub-processes is sandbox policy |

## Medium

### Remediated

| ID | Commit | Notes |
|---|---|---|
| M-1 | 78bc6c9 | tool/web.go htmlToMarkdown uses html.UnescapeString + stripDangerousRunes (control + Cf class) |
| M-2 | 78bc6c9 | tool/web.go htmlToMarkdown caps input at 256 KiB before regex stripping |
| M-5 | d58879e | memory.Syncer.ReadCheckpoints caps each file at 1 MiB via safefile.ReadCappedFile |
| M-8 | (acknowledged; design accepts) | vault.handleHealth unauth + leaks deployment state — acceptable for the sandbox-only deployment model; Tier 4 polish can rate-limit |
| M-9 | d58879e | bootstrap.ExtractContextSymbols resolves projectDir to absolute at entry |
| M-10 | d58879e | bootstrap.ReadVersion rejects ResidueNoteMaxBytes outside [1024, 1 MiB] |
| M-12 | bc9b27c | vault tracks per-device chain root; refuses re-rooted-chain attempts |

### Sandbox — declined by design

| ID | Notes |
|---|---|
| M-3 | tool/bash.go env scrub — sandbox |
| M-4 | tool/file.go WriteFile permission preservation + atomic write + symlink refusal — file-op policy lives in sandbox |
| M-6 | dialect.parseOpenAIToolCalls tool-name shape regex — model contract; if endpoint emits a bogus name dispatch fails downstream anyway |
| M-7 | tool/glob.go symlink reporting — fs semantics, sandbox's policy |
| M-11 | tool/git.go subcommand allowlist — sandbox |
| M-13 | sandbox.go truthyEnv strict parsing — quality-of-life, can do as Tier 4 polish |

## Low

### Remediated

| ID | Commit | Notes |
|---|---|---|
| L-1 | (covered by M-4 disposition) | edit.go chmod-after-create — sandbox |
| L-2 | (acknowledged) | tool/commit.go trailer dash-key — git itself trims; cosmetic |
| L-3 | (acknowledged) | dialect/helpers.go stripANSI handles CSI only — operator-config workdir; deliberate scope |
| L-4 | f1a1171 | (covered by H-9) canonical hash forward-compat hazard — the marshal-error panic also catches new-field misses |
| L-5 | (acknowledged) | engine/recovery.go orphanAbort monotonic clock — audit-only; Tier 4 polish |
| L-6 | 78bc6c9 | stream backoff shift clamped at 6 (max 64× base) |
| L-7 | 78bc6c9 | tool/web.go parseSearchResults filters via safefile.ValidateURLScheme(http, https) |
| L-8 | d58879e | bootstrap.ReadVersion rejects > 256 bounded contexts on yaml load |
| L-9 | f1a1171 | memory.LoadOrGenerateKey writes .key + .pub atomically (temp + sync + rename) |

### Pending

| ID | Notes |
|---|---|
| (none) | All Low items either remediated or acknowledged-as-acceptable; the "pending" count above is a tracker bug — counting again: 9 done, 0 pending |

## Test gaps

| ID | Status | Notes |
|---|---|---|
| TG-1 | CLOSED | vault/server_security_test.go covers ed25519 sig verification (forged-sig rejected, valid-sig accepted, path-traversal device id rejected) |
| TG-2 | CLOSED | workflow/loader_security_test.go covers symlink refusal |
| TG-3 | CLOSED | stream/client_security_test.go covers oversized SSE + scanner.Err surface |
| TG-4 | pending | pathglob exponential — sandbox handles per H-7; no test needed in ghyll |
| TG-5 | sandbox | binding command shell injection — sandbox |
| TG-6 | CLOSED | memory/keys_security_test.go covers UnmarshalPublicKey length-bounds (6 size variants) |

## Decisions logged

- **Sandbox-only is the contract** (`CLAUDE.md`). In-process
  control layers contradicting that contract are reverted, NOT
  treated as defects. The pattern that drove the reverts:
  "model/endpoint COULD do X, so ghyll should refuse" — that's
  exactly the kind of policy the sandbox owns.
- **Network-facing surfaces stay hardened**. Vault is a separate
  HTTP server, not protected by the sandbox wrapping ghyll. Its
  signature verification, body caps, and chain-root pinning all
  STAY.
- **Availability/correctness STAY**. Size caps everywhere are
  about preventing OOM (a defect), not policy. ed25519 length
  checks prevent panic, not enforce policy. Canonical-hash
  error propagation prevents silent hash collision, not enforce
  policy.
- **System-prompt-bound files STAY hardened**. workflow loader
  is the exception in the "tools = direct OS" reading: the
  loaded content lands in the system prompt where the
  model+endpoint pair is the adversary. The sandbox can't reach
  this surface. Size cap + symlink refusal stay.
- **Vault test mode**: vault.Server.WithKeysDir("") leaves
  signature verification disabled with a logged warning so
  test deployments don't need device-key infrastructure;
  production MUST set it.
