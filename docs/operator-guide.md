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

## Related architecture documents

- `specs/architecture/gates.md` — the gate schema; §3.7
  (amendment), §11 (adversarial cycle), §12.2 (self-cert)
- `docs/decisions/009-014` — the v2 ADRs (self-cert, attestation
  store, role-context lock, operator bus, pass entity, orchestrator)
- `specs/architecture/components/attestation.md` — operator
  attestation flow spec
