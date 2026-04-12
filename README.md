# Ghyll

[![CI](https://github.com/witlox/ghyll/actions/workflows/ci.yml/badge.svg)](https://github.com/witlox/ghyll/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/witlox/ghyll/branch/main/graph/badge.svg)](https://codecov.io/gh/witlox/ghyll)
[![Go Report Card](https://goreportcard.com/badge/github.com/witlox/ghyll)](https://goreportcard.com/report/github.com/witlox/ghyll)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A purpose-built coding agent CLI for self-hosted open-weight models. Hyper-optimized for GLM-5 and MiniMax M2.5 on Cray EX infrastructure.

> ## :warning: SANDBOX REQUIRED
>
> **ghyll executes arbitrary commands on your machine.** It runs tool calls from LLM output directly --- no confirmation prompts, no permission checks, no filtering. This is by design (always-yolo execution).
>
> **You MUST run ghyll inside a sandbox.** Without one, a compromised or misbehaving model endpoint can read, modify, or delete any file your user account has access to, exfiltrate data, or execute arbitrary code.
>
> ### Recommended sandboxes
>
> | Platform | Sandbox | Description |
> |----------|---------|-------------|
> | **macOS** | [Tarn](https://github.com/witlox/tarn) | Kernel-level sandboxing via Endpoint Security framework. Purpose-built for ghyll. |
> | **macOS** | [Ash](https://github.com/nicholasgasior/ash) | Lightweight App Sandbox profiles using `sandbox-exec`. |
> | **Linux** | [bubblewrap](https://github.com/containers/bubblewrap) | Unprivileged namespace sandboxing. See example below. |
> | **Linux** | [firejail](https://github.com/netblue30/firejail) | SUID sandbox with seccomp-bpf filtering. |
> | **Any** | Docker/Podman | Container isolation. Mount your project as a volume. |
>
> ### Linux example with bubblewrap
>
> ```bash
> bwrap \
>   --ro-bind /usr /usr \
>   --ro-bind /bin /bin \
>   --ro-bind /lib /lib \
>   --ro-bind /lib64 /lib64 \
>   --proc /proc \
>   --dev /dev \
>   --tmpfs /tmp \
>   --bind "$HOME/.ghyll" "$HOME/.ghyll" \
>   --bind "$(pwd)" "$(pwd)" \
>   --chdir "$(pwd)" \
>   --unshare-net \
>   --die-with-parent \
>   -- ghyll run .
> ```
>
> This gives ghyll read-only access to system binaries, read-write to the project directory and ghyll config, and **no network access** (model calls go through a pre-configured proxy or the sandbox is adjusted to allow specific endpoints).
>
> **Do not run ghyll unsandboxed on a machine with access to production systems, credentials, or sensitive data.**

## Features

- **Model-specific dialects** --- no abstraction layer, direct optimization per model
- **Context-depth routing** --- automatic escalation from fast tier (M2.5) to deep tier (GLM-5)
- **Drift-aware memory** --- vector embedding checkpoints with cosine similarity drift detection
- **Git-native sync** --- team memory via orphan branch, no additional infrastructure
- **Tamper-evident history** --- Merkle DAG checkpoints with ed25519 signatures
- **Always-yolo execution** --- sandbox handles security at the kernel level

## Quick Start

```bash
# Build
make build-bin

# Configure
mkdir -p ~/.ghyll
cat > ~/.ghyll/config.toml << 'EOF'
[models.m25]
endpoint = "https://inference.internal:8001/v1"
dialect = "minimax_m25"
max_context = 1000000

[routing]
default_model = "m25"
EOF

# Run (inside your sandbox)
ghyll run .
```

## Documentation

Full documentation: [witlox.github.io/ghyll](https://witlox.github.io/ghyll)

- [Getting Started](docs/usage/getting-started.md)
- [Configuration](docs/usage/configuration.md)
- [CLI Reference](docs/usage/cli-reference.md)
- [Architecture](docs/architecture/design.md)
- [Decisions](docs/decisions/001-architecture.md)

## Supported Models

| Model | Parameters | Context | Tier | SWE-bench |
|-------|-----------|---------|------|-----------|
| MiniMax M2.5 | 230B/10B active | 1M tokens | Fast | 80.2% |
| GLM-5 | 744B/40B active | 200K tokens | Deep | — |
| Kimi K2 | 1T/32B active | 256K tokens | Planned | — |

## Development

```bash
make setup           # install tools + git hooks
make                 # lint + test + build
make test-race       # tests with race detector
make coverage-check  # enforce 50% coverage threshold
make docs-serve      # preview documentation locally
```

## License

MIT
