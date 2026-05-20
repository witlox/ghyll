# Changelog

Versioning: `v<year>.<sum-of-ADRs>.<commit-count>`. The `<sum-of-ADRs>`
counter is the total number of architecture decision records under
`docs/decisions/` (main + v2). It is bumped manually in
`.github/workflows/release.yml` when a new ADR lands. The
`<commit-count>` is filled in by the release workflow at tag time.

Tagging is automated: the `release.yml` workflow runs weekly (and
on-demand via `workflow_dispatch`), generates the version, creates the
tag, builds cross-arch binaries, and cuts a GitHub release. Do NOT
run `git tag` by hand.

## v2026.30.x — Tier 2 + Tier 3 + Tier 4

The first release on the new ADR-sum versioning scheme (17 main ADRs
+ 13 v2 ADRs = 30). Covers the gate-and-arrow enforcement spine
brought to production readiness, end-to-end.

### Tier 2 — gate-and-arrow enforcement spine

- v2 design landed: gate-and-arrow state machine, typed clause
  evaluation, depth routing (§7.1), arrow status derivation,
  adversarial phase, on-the-spot arrow creation.
- Diamond roles fixed as contracts (ADR-008) — analyst → architect
  → implementer → integrator, no runtime overlay.
- Tier 1 → Tier 2 attestation migration with passes table, JSONL
  source of truth (ADR-015), and crash recovery reconciliation.
- Operator verdict modal (ADR-016): interactive `pass/fail/
  insufficient-basis/skip` flow with escalation, opID-at-call-time
  binding, inFlight dedup, snapshot-then-iterate with 8-round cap.
- Engine schema v5 migration with `CHECK (pass_id <> '')` and
  backfill for legacy rows.
- Adversarial pass (12 critical + 25 high + medium + low) — every
  finding remediated, no deferrals.
- Integrator pass — cross-package seam issues remediated.
- 312 BDD scenarios passing (20 lifted from @deferred in-batch).

### Tier 3 — operational completeness

- `internal/safefile`: shared path/size/URL guards
  (`SafeSegment`, `ReadCappedFile`, `ValidateURLScheme`) for
  network-facing surfaces.
- Vault (`cmd/ghyll-vault`):
  - ed25519 signature verification against
    `<keysDir>/<deviceID>.pub` (test mode with logged warning when
    keysDir is empty).
  - Per-device chain-root pinning rejects re-rooted chains.
  - 4 MiB request body cap; embedding length cap 4096; TopK cap
    100.
  - DeviceID path-traversal guards.
- Memory:
  - Public key length checks (`UnmarshalPublicKey`,
    `MarshalPublicKey`, `loadKey`).
  - Atomic `.key`/`.pub` writes (temp + sync + rename).
  - `canonicalJSON` propagates marshal errors via panic at the
    `CanonicalHash` boundary — no silent nil segments.
  - `Syncer` validates `repoHash` / `DeviceID` / `Hash` via
    `safefile.SafeSegment`.
  - `ReadCheckpoints` caps each file at 1 MiB.
- Stream client:
  - 1 MiB `bufio.Scanner` buffer cap; 16 MiB aggregate content
    cap; 1 MiB per-tool-call args cap; `scanner.Err` surfaced as
    `ErrStreamSizeCap`.
  - Backoff shift clamped at 6 (max 64× base).
  - `ExtraHeaders` exposed on `ClientOptions` for live tests.
- Dialect:
  - HandoffSummary routes `cp.Summary` + `cp.ActiveModel` through
    `sanitizeHandoffField` (8 KiB cap, ANSI strip, line-separator
    scrub, header-marker neutralization).
- Tool/web:
  - 1 MiB response body cap on web search.
  - `htmlToMarkdown` uses `html.UnescapeString` +
    `stripDangerousRunes` (control + Cf class) and caps input at
    256 KiB before regex.
  - Search-result URL allowlist (`http`/`https`).
- Bootstrap:
  - `ReadVersion` caps grid file at 4 MiB; rejects > 256 bounded
    contexts; rejects `ResidueNoteMaxBytes` outside [1024, 1 MiB].
- Workflow loader:
  - 256 KiB cap + symlink refusal (system-prompt-bound files).
- Config:
  - 1 MiB cap on TOML load; model + vault-endpoint URL scheme
    validation.
- Performance baselines:
  - Engine + runner benchmarks
    (`BenchmarkJournal_FindingsRaiseDrain`,
    `BenchmarkReplay_NAttestations`,
    `BenchmarkCatchUpAttestations_NRows`).
  - `perf/baselines.md` with watch-list drift gates.
- Live-endpoint integration tests behind the `live` build tag
  (`make test-live`).
- `ghyll init attest --op-id <id> [--dir <path>]` — emits one
  on-the-spot attestation per arrow at session init.
- Coverage threshold raised 70% → 78%.

### Tier 4 — docs + release polish

- Operator guide refreshed: Tier 2 verdict modal flow,
  `ghyll init attest`, sandbox detection table
  (Docker/Podman/bubblewrap/sandbox-exec/Firejail/Kubernetes),
  vault setup walkthrough, `ghyll memory` subcommand reference.
- Architecture flows: Flow 4 — Tier 2 verdict modal sequence
  diagram (request → drain → present → record → tree writer →
  fanout, with escalation path and invariants).
- ADR-017: ProjectStatus aggregator (pure read function, no
  cache) — rationale documented.
- Documentation index (`docs/SUMMARY.md`) refreshed with
  operator-guide, architecture-flows, ADR-008 through ADR-017,
  and v2-ADR-001 through v2-ADR-013.
- Release workflow updated: MAJOR bumped from 1 to 30 (sum of
  ADRs); future ADR additions bump the major manually.

### Design pushback — reverts

In-process control layers that contradicted ghyll's sandbox-only
contract (`CLAUDE.md`: "Tools are direct OS calls — no permission
layer (sandbox handles security)") were reverted:

- `tool/bash.go` env scrub.
- `tool/git.go` subcommand allowlist.
- `tool/edit.go` / `tool/file.go` symlink refusal, atomic write,
  permission preservation.
- `dialect/parse.go` tool-name shape regex.
- `runner/subprocess.go` `BindingEvaluator.WorkingDir` containment.
- `memory/sync.go` `validateGitOrigin` + git env allowlist.

Network-facing surfaces (vault), availability (size caps), and
correctness (crypto length checks, canonical-hash error
propagation) were KEPT — they are not policy, they are
correctness/availability. See
`specs/v3/tier3-security-review-remediation.md` for the full
disposition table.
