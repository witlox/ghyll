# fetch-embedder + first-run sync warning — validation pass 1

22 findings raised → 22 remediated → 11 refuted at first verification.
All work in a single commit per "no deferrals" feedback. Cold-context
adversarial sweep across 4 lenses (security, correctness, integration,
ux-and-failure-modes) → adversarial verify-as-refute per finding → only
findings that survived adversarial verification are listed below.

## Production deltas

| Finding | Severity | Where | Fix |
|---------|----------|-------|-----|
| FE-SEC-1 | high | `cmd/ghyll/memory_cmd.go` streamDownload | `http.Client.CheckRedirect` re-runs `validateEmbedderURL` on every hop. A 302 from https→http public host is refused. Test: `TestScenario_FetchEmbedder_RejectsHTTPRedirect`. |
| FE-SEC-2 | medium | `memory/sync_test.go` regression test | Added `git ls-remote` empty-assert AFTER `InitBranch` to prove the pre-receive hook actually rejected the push and we exercised the fallback path. |
| FE-SEC-4 | low | `cmd/ghyll/memory_cmd.go` defaults + streamDownload | Added `defaultEmbedderSHA256` constant pinning the published GTE-micro hash; `model_sha256` config key for operator overrides; SHA verified post-download via tee-hash. Test: `SHAMatch` / `SHAMismatch`. |
| FE-SEC-6 | low | `cmd/ghyll/memory_cmd_test.go` | Added `OversizedContentLength` test asserting Content-Length above cap is rejected before body read. |
| CORR-1 | high | streamDownload | Reject 0-byte body explicitly. Test: `RejectsEmptyBody`. |
| CORR-2 | high | streamDownload | Reject `Content-Type: text/html` (gated-repo login walls). Test: `RejectsHTMLContentType`. |
| CORR-3 | medium | dispatch in cmdMemoryMain | Fail loud when HOME is unset rather than writing `.ghyll/` into CWD. |
| CORR-4 + INT-1 | medium + high | `cmd/ghyll/main.go` step 5 | Applies `expandUserHome` so a TOML `model_path = "~/.ghyll/..."` resolves the same in the live session as it does at fetch time. |
| CORR-5 | medium | `cmd/ghyll/main.go` step 5 | Replaced hardcoded path literal with `defaultEmbedderPath()` — single source of truth, drift-proof. |
| CORR-8 | low | `memory/sync.go` InitBranch | Push error captured; included in fallback-fetch error message for debugging. |
| INT-2 | medium | `tests/acceptance/steps_memory_fetch.go` | `sync.Once`-guarded package-level binary cache; build runs once per test process instead of per scenario. Acceptance dropped from 143s → 131s. |
| INT-3 | medium | `cmd/ghyll/memory_cmd.go` readEmbedderConfig | Uses `safefile.ReadCappedFile` instead of `os.ReadFile`: symlink refusal + 1 MiB cap, matching what `config.Load` enforces. |
| INT-4 | low | streamDownload | `os.CreateTemp` produces unique `<base>.<rand>.tmp` so concurrent invocations don't truncate each other's in-flight downloads. |
| UX-FM-1 | high | streamDownload | Print `ℹ size: <N> bytes` immediately after headers arrive so slow links don't read as hangs. |
| UX-FM-2 | high | cmdMemoryFetchEmbedder skip path | Skip message now shows file size AND the URL that would have been hit. |
| UX-FM-3 | medium | cmdMemoryFetchEmbedder skip path | `printOnnxRuntimeHint` extracted; runs on both success AND skip paths so the runtime install reminder is always visible. |
| UX-FM-4 | high | streamDownload | Non-2xx errors include both the URL and a 4 KiB body excerpt. 403/401 debugging no longer needs curl. |
| UX-FM-5 | medium | embedderFetchTimeout | Raised 5m → 15m. The ~60 MB blob on 100 KB/s CSCS uplinks takes 5-10 minutes; the old cap fired mid-transfer. |
| UX-FM-6 | medium | `memory/sync.go` InitBranch fallback | `slog.Warn` when push-to-origin fails — operator has a breadcrumb that team-memory sync silently degraded to local-only. |
| UX-FM-7 | medium | flag parser | `--help` / `-h` print usage and return. |
| UX-FM-10 | low | expandUserHome | Returns a directed error for `$VAR/...` and `~user/...` instead of silently treating them as literal directory names. |

## Refuted (11 findings)

The verification pass refuted 11 raw findings as either (a) misreads of
the code (e.g. claiming the size-cap was unenforced when both
Content-Length and io.LimitReader guards already existed), (b)
hypotheticals without grounding (e.g. "what if the operator pipes the
output through `tr`?"), or (c) duplicates of more specific findings
that were merged into a single remediation. Full verdict text is in
the workflow transcript at
`/tmp/claude-1000/-home-witlox-ghyll/7669d195-12cb-4e0d-b2ec-0d43b658a77c/tasks/wogtpkhhq.output`.

## Test surface

Before this pass: 11 fetch-embedder unit tests + 2 BDD scenarios + 1
sync regression test.

After this pass:
- **20 fetch-embedder unit tests** (added: RejectsHTTPRedirect,
  RejectsEmptyBody, RejectsHTMLContentType, SHAMismatch, SHAMatch,
  OversizedContentLength, HelpFlag, ExpandUserHome_RejectsShellVarsAndOtherUser,
  ExpandUserHome_PreservesAbsolute)
- **2 BDD scenarios** (unchanged, but binary cache now sync.Once-shared)
- **1 sync regression test** (strengthened with ls-remote empty-assert)

Total acceptance suite runtime: 131s (down from 143s before INT-2).

## Pre-commit gates

- `go build ./...` — clean
- `go vet ./...` — clean
- `go test ./...` — all green including 357 acceptance scenarios
