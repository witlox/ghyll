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

## v2026.30.x — polish sprint (post-v2026.30.243)

Closes the 6 Medium+Low adversarial findings flagged in
`specs/v4/post-prod-readiness-adversarial.md`, lifts 3 small
attestation `@deferred` scenarios, and annotates the remaining
12 `@deferred` Gherkin scenarios with GitHub-issue tracking
pointers so future readers know where the design work lives.

### Med+Low remediations (`d5315fa`)

All six landed in a single commit with 8 new tests including a
race-stress on the `/run-arrow` event subscriber:

- **M-A**: `scanBrownfieldContexts` caps at `MaxBoundedContexts`
  (256), matching `DeclareContext`. Returns
  `ErrProfileTooManyContexts` on overflow.
- **M-B**: `Grid.Write` creates `.ghyll/` at `0o700` (was `0o755`)
  and `Chmod`s pre-existing dirs to the same — `engine.db` stat
  no longer world-readable. YAML file itself stays `0o644`.
- **M-C**: per-session `/op-id` REPL handler now routes through
  the canonical `validateAndNormalizeOpID` shim; stores NFC.
- **L-A**: `/run-arrow` subscriber no longer drops trailing
  events; explicit `unsubscribe()` before the snapshot.
- **L-B**: `ghyll init` success summary appends "(default
  context auto-declared — no bounded contexts detected)" when
  the operator's repo had no detected bounded contexts.
- **L-C**: residue reason text is now byte-identical across
  runs — `mapKeys` sorted before formatting.

### Small attestation `@deferred` lifts (`517ca2d`)

- **YAML-loader sentinel**: `bootstrap.ParseInsufficientBasisRoundsMax`
  maps non-integer input to `ErrInsufficientBasisRoundsMaxNotInteger`;
  ≤0 stays on `ErrInsufficientBasisRoundsMaxNonPositive`.
- **Meta-described op-id rows**: dropped from Gherkin (5000-rune
  string, U+202E RTL override, whitespace-only); now covered by
  bootstrap unit tests as the single source of truth.
- **Session-ends-mid-attestation**: wired via the existing
  modalDriver pending queue + `SessionRegistry.Close` →
  `Declare` path; round counter stays at 0 because the operator
  never submitted a verdict.

### Strict mode + dedup (already shipped earlier, confirmed)

- `tests/acceptance/acceptance_test.go` runs with `Strict: true`.
- Zero duplicate step regexes across 1150 registrations in 40
  step files.

### Spec annotations (`32eeebd`)

Remaining 12 `@deferred` scenarios annotated with `Tracked:
github.com/witlox/ghyll/issues/<N>` so the next design step is
discoverable from the spec:

- **#23** — artifact-level arrow dependencies (3 scenarios in
  `amendment.feature`).
- **#24** — residue imputed-cost calculator (4 scenarios in
  `state-machine.feature`).
- **#25** — invalidated-status enum + history (5 scenarios in
  `state-machine.feature`).

None of the three is a current-use blocker; see issue bodies
for operational-impact framing.

### Suite

345 active scenarios passing under `Strict: true`. Race-clean.
Coverage 78.9%.

## v2026.30.x — prod-readiness sprint (post-v2026.30.228)

A cold-context integrator pass against the v2026.30.228 release
found that the binary shipped library code without operator-facing
wiring — every fresh `ghyll run` exited at step 1, the bootstrap
pipeline had no CLI driver, and gate-and-arrow execution was
unreachable from the chat loop. This minor-version bump fixes all
six findings (3 Critical + 3 High) and lifts 21 BDD scenarios from
`@deferred`.

### Production wiring (integrator findings)

- **C-1** (`b3bd710`) — `ghyll init --op-id <id> [project-dir]`. Wires
  `bootstrap.ParseRoleFileEmbedded` → `catalogue.LoadEmbedded` →
  `ProfileRepo` → `BuildProposal` → operator-acceptance loop
  (auto-accept v1, skip-with-residue for clauses missing required
  args) → `BuildInitGrid` → `Grid.Write` to `.ghyll/grid.v1.yaml`.
  Auto-creates a "default" bounded context for empty repos.
- **C-2** (`b6c8c8f`) — `config.DefaultTemplate()` via `//go:embed
  config/example.toml`. `cmd/ghyll/config_bootstrap.go:ensureConfig`
  auto-writes `~/.ghyll/config.toml` (mode `0o600`) on first run if
  missing, prints a `ui.Status` hint with the path, and exits 0 so
  the operator can edit endpoints. Race-resilient against concurrent
  `ghyll run` invocations.
- **C-3** (`1c39506`) — `/run-arrow <id> [--context <ctx>]` and
  `/list-arrows` slash commands in the REPL. Bus subscriber surfaces
  `OpEventPassOpened` / `OpEventPassClosed` /
  `OpEventInsufficientBasisRoundsExceeded` inline; existing
  modal driver still owns verdict prompts.
- **H-1** (`f0c79cc`) — `//go:embed gates/concepts/*.yaml` →
  `ghyll.ConceptsFS` (18 schemas, ~24 KB). New
  `catalogue.LoadEmbedded()` reads from the embedded FS.
- **H-2** (`68f9eb1`) — `//go:embed specs/architecture/roles/*.md` →
  `ghyll.RolesFS` (4 role contracts per ADR-008). New
  `bootstrap.ParseRoleFileEmbedded(roleName)` with the
  `ErrRoleNameUnknown` sentinel.
- **H-3** (`dfa5a4d`) — operator guide rewritten. Bootstrap section
  uses real commands; full slash-commands table; obsolete
  hand-place-the-grid prose removed.

### Adversarial pass (post-fix remediation, `d138caa`)

Cold-context review against the six commits above. Zero Critical
findings. Three High, all remediated in-pass:

- **H-A** — Op-id NFC-normalization. `ghyll init` and
  `ghyll init attest` now route through a shared
  `validateAndNormalizeOpID` shim so the canonical form lands in
  `grid.created-by-op-id` and emitted `AttestationRecord`s.
- **H-B** — First-run config-bootstrap race. `ensureConfig` handles
  EEXIST from `O_EXCL` by re-loading; valid template → proceed,
  malformed → surface real parse error, vanished → new
  `errConfigBootstrapRace` sentinel.
- **H-C** — `/run-arrow` nil-guards `s.engine.Bus()` before
  `Subscribe`.

Three Medium + three Low flagged in
`specs/v4/post-prod-readiness-adversarial.md`; none release-blocking.

### BDD `@deferred` lifts (parallel-batch wiring)

Suite at **343 active scenarios** (up from 322 in v2026.30.228).

- **Batch 1** (`f10daf2`) — `attestation.feature`: 9 scenarios.
  Multi-operator handoff, verdict pass/fail/IB, 3-rounds-then-
  escalation, accepted-risk, route-upstream, oversized residue,
  near-simultaneous verdicts.
- **Batch 2** (`0a40690`) — `runner.feature`: 7 scenarios. Machine
  eval success/fail, attested-clause hint, operator verdict,
  producer-cannot-emit-hint, adversarial verification auto-insert,
  pure-machine arrow.
- **Batch 4** (`11de530`) — `edit.feature` + `amendment.feature`:
  3 scenarios. Edit temp-file cleanup, amendment-lock-crash-
  recovery, attestation-waiting-on-aborted-pass.
- **Batch 5** (`f94599c`) — `adversarial.feature`: 2 scenarios.
  Producer-fix re-attack, accepted-risk proposal.

16 `@deferred` scenarios remain (reduced to 12 in the polish sprint
below) — all structurally substrate-gated
(artifact dep-check schema, residue imputed-cost calculator,
invalidated-status enum + history layer, YAML-loader wire-form,
bus-buffered pending-attestation-request semantics).

## v2026.30.228 — Tier 2 + Tier 3 + Tier 4

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
  (→ now 345; see the polish sprint above.)

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
