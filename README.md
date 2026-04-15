# Ghyll

[![CI](https://github.com/witlox/ghyll/actions/workflows/ci.yml/badge.svg)](https://github.com/witlox/ghyll/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/witlox/ghyll/branch/main/graph/badge.svg)](https://codecov.io/gh/witlox/ghyll)
[![Go Report Card](https://goreportcard.com/badge/github.com/witlox/ghyll)](https://goreportcard.com/report/github.com/witlox/ghyll)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Purpose-built coding agent CLI for self-hosted open-weight models. Hyper-optimized for GLM-5 and MiniMax M2.5 on Cray EX infrastructure.

> ## :warning: SANDBOX REQUIRED
>
> **ghyll executes tool calls from LLM output directly** --- no confirmation, no permission checks, no filtering. This is by design.
>
> **You MUST run ghyll inside a sandbox.** Without one, a compromised model endpoint can execute arbitrary code with your user privileges.
>
> | Platform | Sandbox | Description |
> |----------|---------|-------------|
> | **macOS / Linux** | [SRT](https://github.com/anthropic-experimental/sandbox-runtime) | **Recommended.** Anthropic's Sandbox Runtime — OS-level filesystem and network isolation via Seatbelt (macOS) and bubblewrap (Linux). |
> | **macOS** | [Ash](https://github.com/nicholasgasior/ash) | App Sandbox profiles via `sandbox-exec`. |
> | **Linux** | [bubblewrap](https://github.com/containers/bubblewrap) | Unprivileged namespace sandboxing. |
> | **Linux** | [firejail](https://github.com/netblue30/firejail) | SUID sandbox with seccomp-bpf. |
> | **Any** | Docker/Podman | Container isolation. |
>
> <details><summary>SRT example (recommended)</summary>
>
> ```bash
> # Install SRT
> npm install -g @anthropic-ai/sandbox-runtime
>
> # Run ghyll inside SRT
> srt ghyll run .
> ```
>
> SRT uses a settings file (`~/.srt-settings.json`) for fine-grained control:
>
> ```json
> {
>   "network": {
>     "allowedDomains": ["inference.internal"]
>   },
>   "filesystem": {
>     "denyRead": ["~/.ssh", "~/.aws"],
>     "allowWrite": [".", "~/.ghyll"],
>     "denyWrite": [".env"]
>   }
> }
> ```
>
> Everything is denied by default — network, filesystem writes, and sensitive paths are blocked unless explicitly allowed. This makes it ideal for containing LLM-driven tool execution.
>
> </details>
>
> <details><summary>Linux example with bubblewrap</summary>
>
> ```bash
> bwrap \
>   --ro-bind /usr /usr --ro-bind /bin /bin \
>   --ro-bind /lib /lib --ro-bind /lib64 /lib64 \
>   --proc /proc --dev /dev --tmpfs /tmp \
>   --bind "$HOME/.ghyll" "$HOME/.ghyll" \
>   --bind "$(pwd)" "$(pwd)" --chdir "$(pwd)" \
>   --unshare-net --die-with-parent \
>   -- ghyll run .
> ```
> </details>

## Quick Start

```bash
make build-bin
cp config/example.toml ~/.ghyll/config.toml
# Edit ~/.ghyll/config.toml with your SGLang endpoints
ghyll run .
```

## Supported Models

| Model | Active params | Context | Tier |
|-------|--------------|---------|------|
| MiniMax M2.5 | 10B / 230B | 1M tokens | Fast |
| GLM-5 | 40B / 744B | 200K tokens | Deep |
| Kimi K2 | 32B / 1T | 256K tokens | Planned |

## Features

- **Model-specific dialects** --- hand-tuned prompts, tool parsing, and compaction per model
- **Context-depth routing** --- auto-escalation from fast to deep tier based on complexity
- **Real-time streaming** --- tokens appear as they arrive, tool calls rendered inline
- **Drift-aware memory** --- cosine similarity drift detection with checkpoint backfill
- **Tamper-evident checkpoints** --- Merkle DAG with ed25519 signatures, git orphan branch sync
- **Team memory** --- searchable checkpoints from all developers via vault server

## Documentation

**[witlox.github.io/ghyll](https://witlox.github.io/ghyll)**

## Development

```bash
make setup           # install tools + git hooks
make                 # lint + test + build
make coverage-check  # enforce 50% coverage
make docs-serve      # preview docs locally
```

## License

MIT
