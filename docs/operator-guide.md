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

### What to edit in `~/.ghyll/config.toml`

The seeded config ships placeholder endpoints. Step 2 ("drop in real
model endpoints") means changing the `endpoint = "..."` line under
each `[models.*]` block to your provider's base URL. A minimal edit
looks like:

```toml
[models.minimax]
endpoint = "https://api.example.com/v1/minimax-m25"
api_key  = "sk-..."

[models.glm]
endpoint = "https://api.example.com/v1/glm5"
api_key  = "sk-..."

# Kimi 2.5 / 2.6 via the CSCS gateway. The `dialect` field is the
# routing key (lowercase, one of: minimax, glm, deepseek, qwen, kimi).
# The `model` field is the literal id sent on the OpenAI request
# body's `model` field — operators paste the canonical mixed-case id
# so the CSCS gateway routes to the right backend.
[models.kimi]
endpoint = "https://ai-gateway.svc.cscs.ch/v1"
dialect  = "kimi"
model    = "moonshotai/Kimi-K2.6"
api_key  = "sk-..."
```

If `model` is omitted, the dialect string falls back as the wire
`model` field (preserves legacy behaviour for the 4 other dialects).
When `model` is set, it appears verbatim on the request — that is the
"appears on the OpenAI request" contract.

Leave the rest of the template alone unless you have a specific
reason to override defaults; the depth ladder, routing thresholds,
and sandbox settings ship with sane values.

### Endpoint authentication — api_key precedence

When the endpoint requires a Bearer token, ghyll resolves the value
at request time from three sources, highest to lowest:

| Layer | Source | Example |
|------|--------|---------|
| 1 | `GHYLL_API_KEY_<MODEL>` env var | `GHYLL_API_KEY_CSCS_GLM5=sk-...` |
| 2 | `GHYLL_API_KEY` env var (global fallback) | `GHYLL_API_KEY=sk-...` |
| 3 | `api_key = "..."` under `[models.<name>]` in TOML | `api_key = "sk-..."` |

`<MODEL>` in the scoped env var name is the TOML model key
upper-cased with any non-`[A-Z0-9_]` rune replaced by `_` — so
`[models.cscs-glm5]` becomes `GHYLL_API_KEY_CSCS_GLM5`. The TOML
file is written at mode `0o600` on first seed, but a checked-in
config can safely ship with a placeholder and override via env on
production hosts.

`ghyll config show` prints the resolved provenance as
`api_key: <unset>` / `<env>` / `<toml>` — the value itself is
never printed, logged, or surfaced through error messages
(401/403 upstream responses are replaced with a fixed
`authentication failed` string before any logging occurs).

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
| `/run-arrow <arrow-id> [--context <ctx>]` | Dispatch one arrow synchronously; surface pass-open/close + IB-rounds-exceeded + adversarial-cycle round events inline. (C-3 / I-H-2) |
| `/drain-amendments` | FIFO-drain the pending amendment queue under the active op-id; refuses without `/op-id`. Each commit fires `OpEventAmendmentDrained` with typed `outcome` per ADR-v4-005. (diamond v4 / Gap 2) |
| `/adversary {enable\|disable\|status}` | Toggle the §11 adversarial-cycle hook bundle the dispatcher consults on depth-sensitive arrows. `enable` refuses with `no-dialect-configured` when no active model resolves to a configured endpoint. (diamond v4 / Gap 1) |
| `/invalidate-arrow <arrow-id> [--reason <text>]` | Invalidate one arrow; persists an audit row to `.ghyll/engine.db arrow_invalidations` (ADR-v4-008). Refuses without `/op-id`. The audit row carries operator identity, reason, and timestamp. |
| `/<name>` | User-defined slash command loaded from `.ghyll/commands/<name>.md`. The file contents are injected as user input for the next turn. |

### `/op-id <identity>` — what it is and why it matters

An **op-id** is an email-like string identifying the human at the
keyboard. You set it once per session with `/op-id you@example.com`.
Every attestation you record is tagged with this op-id. §12.2
(self-cert) enforces that you cannot attest your own work — if you
played the analyst role on an arrow, your op-id cannot also serve as
the attesting authority on that arrow's clauses. The CLI rejects
op-ids with control bytes, separators, or Unicode-format runes (see
`validateAndNormalizeOpID`).

Example:

```
ghyll [m25] /path/to/project ▸ /op-id alice@example.com
op-id set: alice@example.com
```

To clear: `/op-id clear` or `/op-id none`. To inspect: `/op-id`
with no argument.

### `/attest <attestation-id> <verdict> [reason]` — example

`/op-id` must be set first; the slash command stamps every attestation
with the active op-id:

```
ghyll [m25] /path/to/project ▸ /op-id alice@example.com
op-id set: alice@example.com
ghyll [m25] /path/to/project ▸ /attest att-analyst→architect/checkout-C1-v1 pass "verified test coverage"
✓ attestation att-analyst→architect/checkout-C1-v1 recorded: verdict=pass by op-id=alice@example.com
```

Attestation IDs come from the runtime:

- `att-<arrow-id>-<clause-id>-v<N>` for depth-type attestations.
- `att-<arrow-id>-v<N>` for on-the-spot attestations.

The canonical `<arrow-id>` shape emitted by the bootstrap is
`<source-role>→<target-role>/<context>` (for example,
`analyst→architect/default`); use that form anywhere an arrow ID is
expected.

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
ghyll [m25] /path/to/project ▸ /run-arrow analyst→architect/checkout
  · pass-opened   pass=p-7 role=analyst context=checkout
  · pass-closed   pass=p-7 role=analyst state/reason=closed:ok
✓ arrow analyst→architect/checkout dispatched: pass=p-7 status=valid clauses=2 blocking-clauses=0 blocking-findings=0
```

The depth tier is resolved by `runner.RouteArrow` over the
arrow's clauses (max-over-clauses per gates.md §8). The context
defaults to the arrow's own declared context; pass
`--context <ctx>` to override. When the role/context lock is
held by another pass the command surfaces
`ErrRoleContextBusy` with the holding pass ID; when the grid is
empty, the hint is `no grid; run \`ghyll init\` first`.

### Verdict modal (Tier 2 / ADR-016)

The four verdicts, in operator terms:

- **pass**: I looked at the work, it meets the clause's contract.
- **fail**: I looked, it does not — record what I inspected.
- **insufficient-basis**: I cannot evaluate from what I have — record
  a residue note describing what's missing.
- **skip**: punt to next round (the same prompt re-presents).

When the dispatcher signals that a clause is awaiting attestation,
the REPL drains a verdict modal BEFORE the next prompt. You see:

```
── attestation request ─────────────────
  arrow:           analyst→architect/checkout
  clause:          C2
  concept:         lint-clean
  attestation-ref: att-analyst→architect/checkout-C2-v1
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
  arrow:    analyst→architect/checkout
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

### Residue notes

When you pick `insufficient-basis`, you're recording why you couldn't
decide — what evidence was missing, what would change your verdict.
A good residue note names the artifact you wanted to inspect and
didn't have. The grid's `residue-note-max-bytes` caps the field.

### Bootstrap residue

Clauses the bootstrap couldn't auto-confirm (required args missing)
land in the grid's residue list with a machine-parseable reason. A
later grid amendment can supply the missing values — until then,
the affected arrow's clause stays `unevaluated`.

## State at a glance

A novice reading `ghyll arrow show` needs to know how three layered
statuses interact: clause, pass, arrow. Sketch:

| Status family | Values | Where you see it |
|---|---|---|
| **Clause status** | `unevaluated`, `pass`, `fail`, `insufficient-basis`, `skipped` | `ghyll arrow show` per-clause line; verdict modal |
| **Pass status** | `open`, `closed:ok`, `closed:failed`, `aborted` | `/passes` listing; pass-opened / pass-closed events |
| **Arrow status** | `unevaluated`, `valid`, `invalid` (derived from the pass + the clause set) | `/list-arrows`; `ghyll arrow show` header |

An arrow with any `unevaluated` clause cannot transition to `valid`;
a pass cannot close `ok` while any of its clauses still need an
attestation.

## Amendments

When the grid needs to evolve (a new bounded context appears, an
arrow needs a clause added, a residue note is being resolved), the
integrator role enqueues an **amendment**. The amendment serializes
through a global lock, aborts any open passes on the affected arrow,
appends the new arrow definitions, and bumps the grid version. The
CLI for triggering one manually is not yet exposed — today it's
runtime-driven by the integrator role.

## The adversarial cycle (operator's view)

ghyll periodically runs an adversarial pass — a fresh adversary
attacks the current state of an arrow's evidence, raises findings,
and the original producer is asked to fix them. The operator sees
`producer-fix-signal` events; when the producer plateaus (loop-bomb
detected, see below), the operator is asked to break the deadlock.

## Op-ids on a team

Use stable, distinct op-ids per operator (email addresses are
recommended). The self-cert rule (§12.2) means two operators on a
project MUST have different op-ids — sharing one breaks the
cross-check.

## Offline CLI commands

These work without a running session.

### `ghyll init --op-id <id> [--language <lang>] [--force-traits] [project-dir]`

The bootstrap pipeline driver covered in detail above. The
positional `project-dir` defaults to `.` when omitted. Refuses
to overwrite an existing grid; rejects op-ids that contain
control bytes, path separators, ".." substrings, Unicode format
runes (RTL override, ZWSP, ZWJ, BOM), a leading dot or dash,
a trailing dot, or > 256 bytes.

`--language` accepts `go`, `python`, `cpp`, `rust`, `auto`
(default — derives from the profile's detected file
extensions), or `none` (skip the trait block). Comma-separated
for polyglot repos: `--language go,python`. The chosen
guideline + the language-agnostic `engineering.md` are inlined
into `<project>/.ghyll/instructions.md` inside
`<!-- ghyll-traits-begin --> ... <!-- ghyll-traits-end -->`
markers. Re-running without `--force-traits` leaves an existing
trait block alone; with `--force-traits` rewrites just that
slice (operator prose above + below is preserved).

The library of opt-in guidelines lives at
`~/.ghyll/guidelines/{engineering,ci,go,python,cpp,rust}.md`,
seeded on first `ghyll run`. Edit them; the next `ghyll init`
on a project picks up the edits.

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
$ ghyll arrow show analyst→architect/checkout --dir /path/to/project
arrow: analyst→architect/checkout
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
    att-analyst→architect/checkout-C1-v1  kind=depth-type  clause=C1  verdict=pass  op=alice
    att-analyst→architect/checkout-C2-v1  kind=depth-type  clause=C2  verdict=insufficient-basis  op=alice
```

## Environment variables

| Variable | Effect |
|---|---|
| `GHYLL_REQUIRE_SANDBOX` | `1`/`true`/`yes`/`on`: refuse to start outside a recognized sandbox. Unset: warn only. |
| `GHYLL_SANDBOX_ASSUME_SAFE` | Bypass the sandbox check with the given reason string (audited in the warning). |
| `GHYLL_LOG_LEVEL` | `debug`/`info`/`warn` (default)/`error`. Routes diagnostics. |
| `GHYLL_LOG_FORMAT` | `text` (default) or `json`. |

## §12.2 self-cert in plain terms

You can't grade your own homework. If you were the analyst on an
arrow, you cannot also be the attestor — someone else (or,
equivalently, a session using a different op-id and declaring a
different role) must verify.

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

When the adversarial cycle runs and the model is asked to fix what
the adversary surfaced, ghyll fingerprints the model's output each
round. Two rounds with identical output and unresolved findings =
the model is stuck; ghyll aborts and asks the operator to step in.

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
| `/drain-amendments` refuses with "no op-id set" | Declare your operator identity first via `/op-id you@example.com`; the audit row needs it. |
| `/adversary enable` returns "no-dialect-configured" | No active model resolves to a configured endpoint. Set `routing.default_model` in `~/.ghyll/config.toml` to a model whose `endpoint` is reachable, OR start with `--model <name>` that maps to a dialect. |
| `/invalidate-arrow` refuses with "no op-id set" | Declare the operator identity first; `arrow_invalidations` rows carry op-id, reason, timestamp. |
| `/invalidate-arrow` refuses with "arrow ... not in grid" | Use `/list-arrows` to confirm the arrow id; the wire form is the canonical `<source-role>→<target-role>/<context>` shape produced by `ghyll init`. |
| I made a wrong verdict | Attestations are immutable; the only correction is a new attestation on a later pass OR a grid amendment that supersedes the arrow. |
| Pass / clause / attestation state looks wrong | `ghyll engine status` first, then `ghyll arrow show <id>` for specifics. |

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
