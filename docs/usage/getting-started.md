# Getting Started

This page walks you through installing ghyll, configuring it,
initializing your first project, and running your first session.
The deep walkthrough — verdict modals, sandbox setup, vault
deployment — lives in the [Operator Guide](../operator-guide.md);
read this page first.

## Prerequisites

- **Go 1.25+** (only required if you build from source).
- **Git** (for memory sync via the `ghyll/memory` orphan branch).
- **A sandbox.** ghyll runs in YOLO mode by design — tool calls
  execute directly. You MUST run ghyll inside a sandbox. See the
  [Sandbox setup](../operator-guide.md#sandbox-setup) section of
  the operator guide for the full table
  (Docker / Podman / bubblewrap / sandbox-exec / Firejail /
  Kubernetes). Without a sandbox, a compromised model endpoint
  can execute arbitrary code with your user privileges.
- **Access to SGLang endpoints** serving at least one of the
  supported models (MiniMax M2.5, GLM-5, DeepSeek, Qwen).

## Installation

### From a release

Download the latest tarball from
[GitHub Releases](https://github.com/witlox/ghyll/releases). Each
release ships `ghyll` and `ghyll-vault` binaries for
`linux_amd64`, `linux_arm64`, and `darwin_arm64`.

Each tarball is flat — `tar czf ... -C <dir> .` packs `ghyll`,
`ghyll-vault`, `LICENSE`, and `README.md` at the archive root.
Extract into a per-release directory and add it (or a symlink to
the `ghyll` binary) to your `PATH`:

```bash
mkdir -p ~/.local/share/ghyll-v2026.30.247
tar xzf ghyll_linux_amd64_v2026.30.247.tar.gz \
    -C ~/.local/share/ghyll-v2026.30.247
ln -sf ~/.local/share/ghyll-v2026.30.247/ghyll       ~/.local/bin/ghyll
ln -sf ~/.local/share/ghyll-v2026.30.247/ghyll-vault ~/.local/bin/ghyll-vault
~/.local/bin/ghyll version
```

### From source

```bash
git clone https://github.com/witlox/ghyll
cd ghyll
make build-bin
```

Binaries land in `bin/ghyll` and `bin/ghyll-vault`.

## First run — config bootstrap

The first time you run `ghyll`, it auto-writes a default config
template to `~/.ghyll/config.toml` (mode `0o600`) and exits with
a hint:

```
$ ghyll run
ℹ wrote default config at /home/you/.ghyll/config.toml; edit the model
  endpoints and re-run.
```

Edit `~/.ghyll/config.toml` and point the model endpoints at your
SGLang instances. The shipped template has commentary on every
field; the [Configuration](configuration.md) page is the
reference.

The auto-written file IS the shipped template
([`config/example.toml`](https://github.com/witlox/ghyll/blob/main/config/example.toml)).
In practice only the `endpoint = "..."` lines under
`[models.m25]` and `[models.glm5]` need to change for a minimal
working setup. Resist the urge to keep a hand-rolled "minimal
config" stashed somewhere — the template is the canonical
starting point, and every field has a documented default on the
[Configuration](configuration.md) page.

## Bootstrap a project — `ghyll init`

The gate-and-arrow runtime needs a **grid** — a per-project file
that declares the [arrows](../glossary.md#arrow) (role-to-role
transitions) the runtime will dispatch. Arrows live in
`.ghyll/grid.v1.yaml` after `ghyll init`. You can `cat` the file
to see the raw declarations, or use `/list-arrows` in a session
for the rendered view.

> **Note on "context":** in the rest of this page (and the
> operator guide), "context" means a **bounded context** in the
> DDD sense — a logical scope of the project, like `checkout` or
> `inventory`. This is NOT the LLM context window. The
> LLM-context-window meaning still applies in
> [`docs/internals/context.md`](../internals/context.md). See
> the [glossary entry](../glossary.md#context) for the worked
> example.

`ghyll init` builds the grid for you:

```bash
cd ~/repos/myproject
ghyll init --op-id you@example.com
```

What this does:

1. Scans the project for bounded contexts (auto-declares a
   `default` context if none detected).
2. Walks the four role pairs (init → analyst → architect →
   implementer → integrator) using the role contracts embedded
   in the binary.
3. Loads the closed concept catalogue (also embedded —
   `gates/concepts/*.yaml`).
4. Builds a proposal for each (role-pair, context) tuple.
5. Auto-accepts proposed clauses; clauses missing required
   catalogue args are skipped with a residue note for a future
   amendment.
6. Writes `.ghyll/grid.v1.yaml` atomically.

What the operator actually sees on a fresh greenfield repo:

```
$ ghyll init --op-id you@example.com .
init complete: 4 arrows across 1 contexts; grid at .ghyll/grid.v1.yaml
  (default context auto-declared — no bounded contexts detected)
  residue: 2 clauses auto-skipped (required args without defaults)
```

Clauses the bootstrap couldn't auto-confirm (because their
catalogue schemas require args ghyll has no default for) land in
the grid's `residue:` list with a machine-parseable reason —
for example `init-v1: auto-skipped (required args without
defaults): path-glob`. A later grid
[amendment](../glossary.md#amendment) can supply the missing
values; until then, the affected clauses stay `unevaluated`.

If you re-run `ghyll init` and the grid already exists, ghyll
refuses (no clobber). Use a grid amendment to evolve it.

### What's embedded vs. on-disk

`ghyll init` reads its role contracts and concept catalogue from
the binary — there is no source tree to install alongside. The
table below shows what comes from which surface; bookmark it
when you're chasing down "where did *that* come from?".

| Artifact | Where it lives | How it gets there |
|---|---|---|
| Role contracts (analyst / architect / implementer / integrator) | embedded in the `ghyll` binary | `//go:embed specs/architecture/roles/*.md` at build time |
| Concept catalogue (clause schemas) | embedded in the `ghyll` binary | `//go:embed gates/concepts/*.yaml` at build time |
| Default config template | embedded in the `ghyll` binary; written to `~/.ghyll/config.toml` on first run | `//go:embed config/example.toml`; copied verbatim at mode `0o600` |
| Grid (arrow declarations) | per-project at `.ghyll/grid.v1.yaml` | written by `ghyll init`; immutable after write |
| Engine DB (passes, findings, attestations index) | per-project at `.ghyll/engine.db` | sqlite, opened on session start |
| Attestation audit | per-project at `.ghyll/attestations.jsonl` plus the per-pass tree under `.ghyll/attestations/` | appended on every verdict, fsync'd before accept |
| Checkpoints | global at `~/.ghyll/memory.db` and the `ghyll/memory` git orphan branch | sqlite + Merkle DAG + ed25519 signing |

## First session — `ghyll run`

```bash
ghyll run .
```

The prompt shows the active model and working directory:

```
ghyll [m25] ~/repos/myproject >
```

The minimum daily-driver slash commands are:

| Command | Effect |
|---------|--------|
| `/list-arrows` | Show the project's arrows from `.ghyll/grid.v1.yaml`. |
| `/run-arrow <id> [--context <ctx>]` | Dispatch a pass on the arrow. |
| `/op-id <id>` | Declare an operator session bound to an op-id (for attestations). |
| `/attest <ref> <verdict>` | Attest a pending clause without going through the modal. |
| `/deep` | Temporarily force the deep tier (GLM-5). |
| `/fast` | Restore auto-routing. |
| `/plan` | Enter plan mode (deeper reasoning). |
| `/status` | Show active model, lock state, deep/plan flags, turn count, and tool depth. |
| `/exit` | End the session (creates a final checkpoint). |

This is the subset you'll touch every day; the full set
(including `/attestations`, `/passes`, `/op-id none`, `/quit`,
and the on-disk user commands) is documented in the
[Operator Guide](../operator-guide.md#slash-commands).

### Your first attestation

The shortest path from a fresh `ghyll init` to a recorded
[verdict](../glossary.md#verdict) is six commands. Every step
below shows the actual terminal output, not abbreviated `...`.

**1. Initialize the grid.**

```
$ ghyll init --op-id you@example.com .
init complete: 4 arrows across 1 contexts; grid at .ghyll/grid.v1.yaml
  (default context auto-declared — no bounded contexts detected)
  residue: 2 clauses auto-skipped (required args without defaults)
```

**2. Start the session.**

```
$ ghyll run .
ghyll [m25] ~/repos/myproject ▸
```

**3. List arrows to confirm the grid.**

```
ghyll [m25] ~/repos/myproject ▸ /list-arrows
grid arrows (4, version=1):
  init→analyst/default         init → analyst         stratum=L0 context=default clauses=3
  analyst→architect/default    analyst → architect    stratum=L1 context=default clauses=6
  architect→implementer/default architect → implementer stratum=L1 context=default clauses=8
  implementer→integrator/default implementer → integrator stratum=L2 context=default clauses=5
```

Each column: the **arrow id** (`<source>→<target>/<context>`),
the human-readable source→target pair, the
[stratum](../glossary.md#stratum) (`L0` = fast tier, `L1`+ =
escalates as the clauses demand), the bounded `context`, and
the clause count (how many things the operator may be asked to
attest, plus the machine clauses ghyll evaluates itself).

**4. Declare the operator identity.**

`/op-id` is required before `/attest` and before any pass that
records an attestation:

```
ghyll [m25] ~/repos/myproject ▸ /op-id you@example.com
op-id set: you@example.com
```

**5. Run an arrow.**

```
ghyll [m25] ~/repos/myproject ▸ /run-arrow analyst→architect/default
  · pass-opened   pass=p-7 role=analyst context=default
  · pass-closed   pass=p-7 role=analyst state/reason=closed:ok
✓ arrow analyst→architect/default dispatched: pass=p-7 status=valid clauses=6 blocking-clauses=0 blocking-findings=0
```

If one of the clauses is `attested` + `depth-sensitive`, the
runtime drains a **Tier 2 verdict modal** before the next prompt:

```
── attestation request ─────────────────
  arrow:           analyst→architect/default
  clause:          C2
  concept:         lint-clean
  attestation-ref: att-analyst→architect/default-C2-v1
────────────────────────────────────────
verdict? [pass / fail / insufficient-basis / skip]:
```

The four verdicts mean:

- **pass** / `p` — I looked at the work, it meets the clause's
  contract. `confirm` unit, no payload.
- **fail** / `f` — I looked, it does not. Prompts for inspected
  locations (CSV).
- **insufficient-basis** / `ib` — I cannot evaluate from what I
  have. Prompts for a [residue note](../glossary.md#residue);
  the field is capped by the grid's `residue-note-max-bytes`.
  Example residue note: `"needs the analyst's threat-model doc
  for the checkout flow; not present in this branch yet"`.
- **skip** / `s` — punt to the next round; the modal re-presents
  on the next turn.

Three consecutive `insufficient-basis` verdicts on the same
clause fire the escalation prompt described in the
[Operator Guide](../operator-guide.md#verdict-modal-tier-2--adr-016).

**6. Confirm the verdict was recorded.**

```
ghyll [m25] ~/repos/myproject ▸ /exit
ghyll: session ended; final checkpoint written.

$ ghyll arrow show analyst→architect/default --dir .
arrow: analyst→architect/default
  source-role:  analyst
  target-role:  architect
  stratum:      L1
  context:      default
  clauses:      6
    [0] no-todo-marker (id=C1, depth=depth-robust, min-tier=0)
    [1] lint-clean (id=C2, depth=depth-sensitive, min-tier=2)
    ...
  requirements: 1
  findings:     0
  attestations: 1
    att-analyst→architect/default-C2-v1  kind=depth-type  clause=C2  verdict=pass  op=you@example.com
```

That last line is the recorded attestation. It is now persisted
in `.ghyll/engine.db` AND appended to `.ghyll/attestations.jsonl`
(plus the per-pass tree under `.ghyll/attestations/`).
Attestations are **immutable** — to correct a verdict, you
record a new attestation on a later pass; you do not edit the
existing one.

The full Tier 2 modal flow (escalation prompt, `record-locations-inspected`
unit, `write-residue-note` unit) lives in the
[Operator Guide](../operator-guide.md#verdict-modal-tier-2--adr-016).

## Optional: drift detection

Drift detection requires an ONNX embedding model and the ONNX
Runtime shared library.

### Install ONNX Runtime

```bash
# macOS
brew install onnxruntime

# Linux (Ubuntu/Debian)
# Download from https://github.com/microsoft/onnxruntime/releases
# Extract and place libonnxruntime.so in /usr/local/lib
```

### Download the model

```bash
make embedder
```

This downloads the GTE-micro model (~60 MB) to
`~/.ghyll/models/gte-micro.onnx`.

### Build with CGO

The ONNX embedder requires CGO. The default `make build-bin`
uses `CGO_ENABLED=0` (static binaries, no ONNX). To build with
ONNX support:

```bash
CGO_ENABLED=1 go build -ldflags="-s -w" -o bin/ghyll ./cmd/ghyll
```

Without ONNX, ghyll works fine — drift detection is disabled
gracefully. The warning at startup includes the path it tried.

## Optional: vault server

For team memory search across repos:

```bash
# Add to ~/.ghyll/config.toml:
[vault]
url = "https://vault.internal:9090"
token = "team-shared-secret"

# Run the vault server (currently reads from ~/.ghyll/config.toml;
# --config flag tracked in github.com/witlox/ghyll/issues/26):
ghyll-vault
```

The vault deployment walkthrough — device keys, ed25519 setup,
HTTPS — lives in the
[Operator Guide](../operator-guide.md#vault-team-memory-setup).

## Where to go next

- [Operator Guide](../operator-guide.md) — the canonical deep
  walkthrough.
- [Configuration](configuration.md) — every config field, with
  defaults.
- [CLI Reference](cli-reference.md) — all subcommands.
- [Architecture Flows](../architecture-flows.md) — sequence
  diagrams for init, dispatch, verdict modal, amendment,
  recovery.
