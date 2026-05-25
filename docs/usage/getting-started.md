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

```bash
tar xzf ghyll_linux_amd64_v2026.30.247.tar.gz -C ~/.local/bin
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

A minimal config:

```toml
[models.m25]
endpoint = "https://inference.internal:8001/v1"
dialect = "minimax"
max_context = 1000000

[models.glm5]
endpoint = "https://inference.internal:8002/v1"
dialect = "glm"
max_context = 200000

[routing]
default_model = "m25"
deep_model = "glm5"
context_depth_threshold = 32000
tool_depth_threshold = 5
enable_auto_routing = true
```

## Bootstrap a project — `ghyll init`

The gate-and-arrow runtime needs a **grid** — a per-project file
that declares the arrows (role-to-role transitions) the runtime
will dispatch. `ghyll init` builds the grid for you:

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

A success summary like:

```
init complete: 4 arrows across 1 contexts; grid at .ghyll/grid.v1.yaml
  (default context auto-declared — no bounded contexts detected)
```

If you re-run `ghyll init` and the grid already exists, ghyll
refuses (no clobber). Use a grid amendment to evolve it.

## First session — `ghyll run`

```bash
ghyll run .
```

The prompt shows the active model and working directory:

```
ghyll [m25] ~/repos/myproject >
```

Use the in-session slash commands to drive the gate-and-arrow
runtime:

| Command | Effect |
|---------|--------|
| `/list-arrows` | Show the project's arrows from `.ghyll/grid.v1.yaml`. |
| `/run-arrow <id> [--context <ctx>]` | Dispatch a pass on the arrow. |
| `/op-id <id>` | Declare an operator session bound to an op-id (for attestations). |
| `/attest <ref> <verdict>` | Attest a pending clause without going through the modal. |
| `/deep` | Temporarily force the deep tier (GLM-5). |
| `/fast` | Restore auto-routing. |
| `/plan` | Enter plan mode (deeper reasoning). |
| `/status` | Show active model, turn count, tool depth, pending attestations. |
| `/exit` | End the session (creates a final checkpoint). |

A first end-to-end flow:

```
ghyll [m25] ~/repos/myproject > /list-arrows
A-analyst-architect-default  analyst → architect  stratum=L1  context=default  clauses=6
A-architect-implementer-default  architect → implementer  stratum=L1  context=default  clauses=8
…
ghyll [m25] ~/repos/myproject > /op-id you@example.com
ghyll [m25] ~/repos/myproject > /run-arrow A-analyst-architect-default
pass-opened A-analyst-architect-default …
pass-closed A-analyst-architect-default verdict=pass
```

Attested clauses block waiting for an operator verdict; the Tier
2 verdict modal opens between REPL turns. See the [Operator
Guide](../operator-guide.md#tier-2-verdict-modal) for the modal
flow.

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
[Operator Guide](../operator-guide.md#vault-setup).

## Where to go next

- [Operator Guide](../operator-guide.md) — the canonical deep
  walkthrough.
- [Configuration](configuration.md) — every config field, with
  defaults.
- [CLI Reference](cli-reference.md) — all subcommands.
- [Architecture Flows](../architecture-flows.md) — sequence
  diagrams for init, dispatch, verdict modal, amendment,
  recovery.
