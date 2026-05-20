# Operator Guide — gate-and-arrow flow

This guide shows the human-operator surface of ghyll's
gate-and-arrow runtime. The four canonical role contracts and the
machine-clause concept catalogue are embedded into the binary
(integrator findings H-1 / H-2), so a fresh install needs no
manual file-placement to get to a working grid.

## First run / bootstrap

The first-time path is four commands:

```
# 1. install ghyll, then run once to seed the config
ghyll run .
# → "wrote default config at ~/.ghyll/config.toml; edit the model
#    endpoints and re-run"

# 2. edit ~/.ghyll/config.toml — drop in real model endpoints
#    (the template ships with the canonical four entries; only the
#    base_url + api_key fields typically need to change)
$EDITOR ~/.ghyll/config.toml

# 3. produce the project's first grid
ghyll init --op-id alice@example.com .
# → "init complete: <N> arrows across <M> contexts; grid at
#    /path/to/project/.ghyll/grid.v1.yaml"

# 4. start the session
ghyll run .
```

What happens under the hood:

- **Step 1**: `ghyll run` calls the config bootstrap. When
  `~/.ghyll/config.toml` is missing, the embedded
  `config/example.toml` template is written verbatim at mode 0o600
  (it may carry endpoint URLs that look like secrets), and the
  process exits cleanly so you can fill in the real values
  before reconnecting. Existing files are never clobbered — a
  malformed TOML surfaces the parse error, not an overwrite. (C-2)
- **Step 3**: `ghyll init` runs the four-stage bootstrap pipeline
  end-to-end (C-1):
  1. Profile the project directory (greenfield vs brownfield,
     bounded-context detection, language detection).
  2. Load the embedded concept catalogue (H-1).
  3. For each of the four diamond role-pair arrows
     (`init → analyst`, `analyst → architect`,
     `architect → implementer`, `implementer → integrator`) and
     for each bounded context, build a clause proposal from the
     upstream role's exit-gate clauses (H-2 — the role files are
     embedded; you do NOT need a copy of
     `specs/architecture/roles/` on disk).
  4. Auto-confirm every clause whose default args satisfy the
     concept schema; auto-skip any clause whose schema requires
     args that have no default. Skipped clauses are NOT silently
     dropped — they land in the grid's residue list with a
     machine-parseable reason (`init-v1: auto-skipped (required
     args without defaults): …`) so a later amendment can supply
     the missing values.
  5. Persist `.ghyll/grid.v1.yaml` atomically (temp → fsync →
     rename, ADR-010). The grid file is immutable after write;
     subsequent amendments produce `grid.v2.yaml`, `grid.v3.yaml`,
     etc.

  If `.ghyll/grid.v1.yaml` already exists, `ghyll init` refuses
  to clobber it. If profiling finds no bounded contexts (typical
  for a greenfield repo), a single `default` context is
  auto-declared so the resulting grid is non-empty; you can
  rename or split it later via an amendment.

- **Step 4**: The session opens, restores any prior attestation
  + pass + finding state from `.ghyll/engine.db`, and presents
  the prompt. Use `/list-arrows` to see what's been declared and
  `/run-arrow <id>` to drive a specific arrow.

```
ghyll [m25] /path/to/project ▸ /list-arrows
grid arrows (4, version=1):
  init→analyst/default        init → analyst        stratum=L0 context=default clauses=3
  analyst→architect/default   analyst → architect   stratum=L1 context=default clauses=5
  ...
ghyll [m25] /path/to/project ▸ /run-arrow analyst→architect/default
  · pass-opened   pass=p-7 role=analyst context=default
  · pass-closed   pass=p-7 role=analyst state/reason=closed:ok
✓ arrow analyst→architect/default dispatched: pass=p-7 status=valid clauses=5 ...
```

Pass-open / pass-close and insufficient-basis-rounds-exceeded
events surface inline; the operator-verdict modal (Tier 2) drains
through the standard REPL pre-prompt drain so an attestation
prompt that fires mid-dispatch still gets your attention before
the next input.

## Slash commands

All commands accepted in the REPL:

| Command | Effect |
|---|---|
| `/deep` | Temporarily switch to the deep-tier model. Refused when `--model` was passed. |
| `/fast` | Restore auto-routing and clear plan mode. |
| `/plan` | Enter plan mode (deeper reasoning, higher tier preference). |
| `/status` | Show active model, lock state, deep/plan flags, turn count, tool depth. |
| `/exit` | End the session cleanly. Cancels any in-flight modal read; creates a final checkpoint. |
| `/quit` | REPL alias for `/exit`. |
| `/op-id <id>` | Declare the operator identity for this session. Required before `/attest`. |
| `/op-id` | Show the active op-id (or "(none)"). |
| `/op-id none` (or `clear`) | Clear the active op-id. |
| `/attest <ref> <verdict> [reason]` | Record an attestation verdict on a depth-type or on-the-spot attestation. |
| `/attestations [<arrow-id>]` | List recorded attestations, optionally filtered by arrow. |
| `/passes` | List currently-open passes from the PassRegistry. |
| `/passes <pass-id>` | Show one pass's full state. |
| `/list-arrows` | Render the grid snapshot (sorted arrow IDs + source→target / stratum / context / clause count). Hints when the grid is empty. |
| `/run-arrow <arrow-id> [--context <ctx>]` | Dispatch one arrow synchronously; surface pass-open/close + IB-rounds-exceeded events inline. (C-3) |
| `/<name>` | User-defined slash command loaded from `.ghyll/commands/<name>.md`. The file contents are injected as user input for the next turn. |

### `/op-id <identity>` — example

```
ghyll [m25] /path/to/project ▸ /op-id alice@example.com
op-id set: alice@example.com
```

To clear: `/op-id clear` or `/op-id none`. To inspect: `/op-id`
with no argument.

### `/attest <attestation-id> <verdict> [reason]` — example

```
ghyll [m25] /path/to/project ▸ /attest att-A-checkout-C1-v1 pass "verified test coverage"
✓ attestation att-A-checkout-C1-v1 recorded: verdict=pass by op-id=alice@example.com
```

Attestation IDs come from the runtime:

- `att-<arrow-id>-<clause-id>-v<N>` for depth-type attestations.
- `att-<arrow-id>-v<N>` for on-the-spot attestations.

The `verdict` is one of `pass`, `fail`, `insufficient-basis`
(plus aliases: `p`, `ok`, `f`, `no`, `ib`).

The verdict flows through the AttestationStore — persisted to
the engine sqlite table, audited to the flat
`.ghyll/attestations.jsonl` AND to the per-pass tree at
`.ghyll/attestations/v<N>/<context>/stratum-<S>/<role-pair>/<pass-id>.jsonl`.
fsync runs before the verdict is reported accepted (operator-spec
durability invariant).

Three consecutive `insufficient-basis` verdicts on the same
clause fire the escalation event configured by
`insufficient-basis-rounds-max` in the grid file.

### `/run-arrow` — example

```
ghyll [m25] /path/to/project ▸ /run-arrow A-checkout
  · pass-opened   pass=p-7 role=analyst context=checkout
  · pass-closed   pass=p-7 role=analyst state/reason=closed:ok
✓ arrow A-checkout dispatched: pass=p-7 status=valid clauses=2 blocking-clauses=0 blocking-findings=0
```

The depth tier is resolved by `runner.RouteArrow` over the
arrow's clauses (max-over-clauses per gates.md §8). The context
defaults to the arrow's own declared context; pass
`--context <ctx>` to override. When the role/context lock is
held by another pass the command surfaces
`ErrRoleContextBusy` with the holding pass ID; when the grid is
empty, the hint is `no grid; run \`ghyll init\` first`.

### Verdict modal (Tier 2 / ADR-016)

When the dispatcher signals that a clause is awaiting attestation,
the REPL drains a verdict modal BEFORE the next prompt. You see:

```
── attestation request ─────────────────
  arrow:           A-checkout
  clause:          C2
  concept:         lint-clean
  attestation-ref: att-A-checkout-C2-v1
────────────────────────────────────────
verdict? [pass / fail / insufficient-basis / skip]:
```

- `pass` / `p` — `confirm` unit, no payload.
- `fail` / `f` — prompts for inspected locations
  (`record-locations-inspected` unit; CSV).
- `insufficient-basis` / `ib` — prompts for a residue note
  (`write-residue-note` unit; capped by the grid's
  `residue-note-max-bytes`).
- `skip` / `s` — clause stays pending; the next turn re-presents.

After **three** consecutive `insufficient-basis` verdicts the
escalation prompt fires:

```
── escalation: 3 insufficient-basis rounds ──
  arrow:    A-checkout
  clause:   C2
  options:
    1) accept risk     (record residue note; finding → accepted-risk)
    2) route upstream  (record rationale; pass aborts; deeper-tier retry)
─────────────────────────────────────────────
choice (1 or 2):
```

The chosen verdict is recorded as an `AttestationRecord` with
the residue/rationale as the payload. There is no default — you
must choose.

`/exit` cancels any in-flight modal cleanly; the queued items
re-present on the next session start (Recovery republishes).

## Offline CLI commands

These work without a running session.

### `ghyll init --op-id <id> [project-dir]`

The bootstrap pipeline driver covered in detail above. The
positional `project-dir` defaults to `.` when omitted. Refuses
to overwrite an existing grid; rejects op-ids that contain
control bytes, path separators, ".." substrings, Unicode format
runes (RTL override, ZWSP, ZWJ, BOM), a leading dot or dash,
a trailing dot, or > 256 bytes.

### `ghyll init attest --op-id <id> [--dir <path>]`

Tier 3 / gate-2 CORR-A-18: the production producer for init
AttestationRecords. Reads the project grid via `bootstrap.Read`,
emits one on-the-spot record per arrow with
`AttestedByRole=init`, persists through the standard Record
path (tree writer primary + engine catch-up). Idempotent on
re-run.

```
$ ghyll init attest --op-id alice@example.com --dir /path/to/project
ghyll init attest: 3 init attestations recorded for op-id=alice@example.com (grid v1, 3 arrows)
```

Op-id reject criteria mirror `ghyll init` and the `/op-id`
slash command (same validator).

### `ghyll engine status [--dir <path>]`

Render a project-level summary: arrow count, finding count,
amendment backlog, attestation count, evaluation runs.

```
$ ghyll engine status --dir /path/to/project
ghyll-engine-status: present
engine: /path/to/project/.ghyll/engine.db
  arrows:           12
  findings:         3
  requirements:     8
  classifications:  8
  amendments:       0 pending, 2 drained
  evaluation runs:  47
  attestations:     5
```

A project that has never initialized v2 emits
`ghyll-engine-status: missing` and exits cleanly.

### `ghyll engine recover [--dry-run] [--dir <path>]`

Preview what crash recovery would do at the next session start.
Opens the engine read/write, runs the reconciliation logic
inside a transaction that is **always rolled back**, prints the
report. The real recovery happens automatically when you start
a session with `ghyll run`; this CLI exists so you can preview
it first.

```
$ ghyll engine recover --dry-run --dir /path/to/project
recover (dry-run): /path/to/project/.ghyll/engine.db
  orphans aborted:        2
  orphans preserved:      1 (attestation-pending)
  evaluation_runs flipped: 3 (from JSONL verdicts)
  events:
    - recovery-pass-aborted-crash pass=P-1 arrow=A1 clause= no live process at restart; closed_at=...
    - recovery-attestation-republished pass=P-3 arrow=A1 clause=C5 att-ref=att-X preserved at ...
    - recovery-attestation-replay pass=P-2 arrow=A2 clause=C7 att-ref=att-Y verdict=pass mapped=pass

note: --dry-run; no changes persisted. Start a session
      with `ghyll run` to apply recovery for real.
```

The output covers three reconciliation classes (per ADR-015 Part D):

- **orphans aborted** — open passes whose runner process is gone;
  marked `aborted:crash`.
- **orphans preserved** — open passes with a pending depth-type
  attestation (so the operator can still deliver a verdict). The
  pass row stays `open` and `recovered_at` is stamped.
- **evaluation_runs flipped** — clauses with `end_status=running`
  AND a verdict in the JSONL audit log; `end_status` is reconciled
  to match the verdict (`pass` → `pass`, `fail` → `fail`,
  `insufficient-basis` → `running` so the dispatcher re-emits
  the hint).

The `--commit` flag is explicitly refused — apply recovery via
`ghyll run`.

### `ghyll engine verify-attestations [--dir <path>]`

Walk the project's `.ghyll/attestations.jsonl` audit trail and
report any record that violates the schema (missing required
fields, kind/clause_id pairing wrong, unknown verdict, §12.2
self-cert). Useful for compliance / audit review.

```
$ ghyll engine verify-attestations --dir /path/to/project
attestation-verify: 5/5 records OK
```

A failed audit returns a non-zero exit code and prints each
issue's line number + reason.

### `ghyll memory <subcommand>`

Memory sync + search subcommands.

| Subcommand | Effect |
|---|---|
| `ghyll memory search <query>` | Vector-search past checkpoints by semantic similarity. |
| `ghyll memory log` | Show the local checkpoint chain (hash, parent, turn, summary). |
| `ghyll memory sync` | Manual sync to/from the vault. |

### `ghyll arrow show <arrow-id> [--dir <path>]`

Render one arrow's live state: definition (source / target
role, clauses, requirements), open findings on the arrow, and
all recorded attestations.

```
$ ghyll arrow show A-checkout --dir /path/to/project
arrow: A-checkout
  source-role:  analyst
  target-role:  architect
  stratum:      L1
  context:      checkout
  clauses:      2
    [0] no-todo-marker (id=C1, depth=depth-robust, min-tier=0)
    [1] lint-clean (id=C2, depth=depth-sensitive, min-tier=2)
  requirements: 1
    [0] R1 (min-depth=2)
  findings:     0
  attestations: 2
    att-A-checkout-C1-v1  kind=depth-type  clause=C1  verdict=pass  op=alice
    att-A-checkout-C2-v1  kind=depth-type  clause=C2  verdict=insufficient-basis  op=alice
```

## Environment variables

| Variable | Effect |
|---|---|
| `GHYLL_REQUIRE_SANDBOX` | `1`/`true`/`yes`/`on`: refuse to start outside a recognized sandbox. Unset: warn only. |
| `GHYLL_SANDBOX_ASSUME_SAFE` | Bypass the sandbox check with the given reason string (audited in the warning). |
| `GHYLL_LOG_LEVEL` | `debug`/`info`/`warn` (default)/`error`. Routes diagnostics. |
| `GHYLL_LOG_FORMAT` | `text` (default) or `json`. |

## §12.2 self-cert in plain terms

When you attest a clause, your declared role MUST NOT equal the
arrow's source role or its target role. ghyll enforces this at
two boundaries:

1. The runtime AttestationStore rejects a `Record` call where
   `AttestedByRole` is either endpoint, case-insensitive +
   trimmed. The slash command always uses `operator` as the
   role, which bypasses the conflict by design.
2. The engine schema has CHECK constraints mirroring the
   runtime check. Out-of-band SQL inserts can't bypass the
   rule.

The verifier (`ghyll engine verify-attestations`) also detects
self-cert in the on-disk JSONL — so a tampered audit file
surfaces failure.

## Loop-bomb detection in the producer-fix cycle

When the adversary loop runs with a producer hook (typically
the model itself responding to findings), the harness computes
a SHA-256 digest of the producer's response artifact each
round. Two consecutive rounds with identical artifacts AND
still-open findings = the producer isn't actually changing
anything = `ErrProducerLoopBomb`. The cycle aborts; the
operator must intervene.

## Where to look when something goes wrong

| Symptom | Look at |
|---|---|
| Attestations aren't persisting | `.ghyll/engine.db` (sqlite) — run `ghyll engine status` |
| Audit trail looks short | `.ghyll/attestations.jsonl` + the per-role-pair tree under `.ghyll/attestations/` |
| Background sync errors | `.ghyll/ghyll.log` (slog file) |
| Operator events lost | The OperatorBus is in-process; check the JSONL writer + status output |
| Lock contention ("role-context-busy") | `ghyll arrow show` the arrow; `/passes` to see who holds the lock |
| `/list-arrows` says "no grid; run `ghyll init` first" | Run `ghyll init --op-id <id>` to produce `.ghyll/grid.v1.yaml`. |
| `ghyll run` exits with "wrote default config" | First-run config bootstrap — edit `~/.ghyll/config.toml` and re-run. |

## Sandbox setup

ghyll is sandbox-only by design (`CLAUDE.md`: "Tools are direct
OS calls — no permission layer (sandbox handles security)"). The
sandbox is the layer that restricts what bash / git / web tools
can touch. Without one, a compromised model endpoint runs
arbitrary code with your privileges.

Recommended sandboxes (detected automatically by ghyll):

| Sandbox | How to wrap |
|---|---|
| Docker / Podman | Run ghyll inside a container with bind-mounted project dir |
| bubblewrap | `bwrap --bind ~ /home/me --bind /tmp /tmp ghyll run .` |
| sandbox-exec (macOS) | `sandbox-exec -f profile.sb ghyll run .` |
| Firejail | `firejail --noprofile ghyll run .` |
| Kubernetes | `KUBERNETES_SERVICE_HOST` triggers detection |

Enforcement modes (`GHYLL_REQUIRE_SANDBOX`):

- unset / `0` / `false`: warning only, ghyll starts.
- `1` / `true` / `yes` / `on`: refuse to start without a detected
  sandbox. Override with `GHYLL_SANDBOX_ASSUME_SAFE=<reason>`
  (the reason is logged for audit).

## Vault (team memory) setup

The vault is an HTTP server (`cmd/ghyll-vault`) that synchronizes
checkpoints across multiple operators. **Production deployments
MUST configure ed25519 verification** via `WithKeysDir(dir)`;
the empty-keysDir mode is TEST-only and logs a warning at
startup.

Layout:

```
<vault-storage>/
  devices/
    alice.pub      # ed25519 public key (PEM)
    bob.pub
  …
```

Each device generates a key pair on first run
(`memory.LoadOrGenerateKey`); the operator hands `.pub` to the
vault admin. Checkpoint signing happens automatically; the
vault refuses any checkpoint whose signature doesn't verify
against the registered device's pub key.

## Related architecture documents

- `specs/architecture/gates.md` — the gate schema; §3.7
  (amendment), §11 (adversarial cycle), §12.2 (self-cert)
- `docs/decisions/009-016` — the v2 ADRs (self-cert, attestation
  store, role-context lock, operator bus, pass entity,
  orchestrator, pass persistence, Tier 2 modal)
- `specs/architecture/components/attestation.md` — operator
  attestation flow spec
- `docs/architecture-flows.md` — sequence diagrams for the
  three load-bearing flows
