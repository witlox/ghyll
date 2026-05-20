# Operator Guide — gate-and-arrow flow

This guide shows the human-operator surface of ghyll's
gate-and-arrow runtime. It assumes you've already:

- Installed ghyll and configured a model endpoint
  (`~/.ghyll/config.toml`).
- Run `ghyll run .` once so the project's
  `.ghyll/engine.db` exists.
- Initialized the project's grid (`gates/concepts/`,
  `.ghyll/grid.v1.yaml`).

The operator surface is divided into four interactive commands
inside `ghyll run` and three offline CLI commands.

## In-session commands

Run `ghyll run <project>` and use these slash commands in the
REPL.

### `/op-id <identity>`

Declare your operator identity for the session. Required before
`/attest`. Identities are arbitrary strings (typically an email
or username); whitespace is rejected.

```
ghyll [m25] /path/to/project ▸ /op-id alice@example.com
op-id set: alice@example.com
```

To clear: `/op-id clear` or `/op-id none`. To inspect: `/op-id`
with no argument.

### `/attest <attestation-id> <verdict> [reason]`

Record an operator verdict on a depth-type or on-the-spot
attestation. The attestation-id is the deterministic ID the
runtime computes from `(arrow-id, clause-id, grid-version)`:

- `att-<arrow-id>-<clause-id>-v<N>` for depth-type
  attestations.
- `att-<arrow-id>-v<N>` for on-the-spot attestations.

The `verdict` is one of `pass`, `fail`, `insufficient-basis`
(plus aliases: `p`, `ok`, `f`, `no`, `ib`).

```
ghyll [m25] /path/to/project ▸ /attest att-A-checkout-C1-v1 pass "verified test coverage"
✓ attestation att-A-checkout-C1-v1 recorded: verdict=pass by op-id=alice@example.com
```

The verdict flows through the AttestationStore — persisted to
the engine sqlite table, audited to the flat
`.ghyll/attestations.jsonl` AND to the per-pass tree at
`.ghyll/attestations/v<N>/<context>/stratum-<S>/<role-pair>/
<pass-id>.jsonl`. fsync runs before the verdict is reported
accepted (operator-spec durability invariant).

Three consecutive `insufficient-basis` verdicts on the same
clause fire the escalation event configured by
`insufficient-basis-rounds-max` in the grid file.

### `/attestations [<arrow-id>]`

List recorded attestations. With no argument, every attestation
in the store. With an arrow-id, filter to that arrow.

```
ghyll [m25] /path/to/project ▸ /attestations A-checkout
attestations (2):
  att-A-checkout-C1-v1  arrow=A-checkout clause=C1 verdict=pass op=alice
  att-A-checkout-C2-v1  arrow=A-checkout clause=C2 verdict=insufficient-basis op=alice
```

### `/passes`

List currently-open passes from the PassRegistry. Useful for
seeing what the runtime is in the middle of.

```
ghyll [m25] /path/to/project ▸ /passes
open passes (1):
  p-7  role=analyst context=checkout arrow=A-checkout state=open
```

An empty registry surfaces as `no open passes`.

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

Reject criteria mirror `/op-id`: control bytes, path separators,
".." substring, Unicode format runes (RTL override, ZWSP, ZWJ,
BOM), leading dot or dash, trailing dot, or > 256 bytes.

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
