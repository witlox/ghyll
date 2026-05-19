# Ghyll

[![CI](https://github.com/witlox/ghyll/actions/workflows/ci.yml/badge.svg)](https://github.com/witlox/ghyll/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/witlox/ghyll/branch/main/graph/badge.svg)](https://codecov.io/gh/witlox/ghyll)
[![Go Report Card](https://goreportcard.com/badge/github.com/witlox/ghyll)](https://goreportcard.com/report/github.com/witlox/ghyll)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A coding agent that optimizes for **correctness over speed and breadth**,
and pays for it in **friction**. Self-hosted, open-weight, sandbox-only.

ghyll is correct for a narrow class of work — novel architecture,
correctness-critical systems, long-horizon projects where a defect
reaching deployment is expensive. ghyll is **wrong** for CRUD,
migrations, glue code, and rapid prototyping, where throughput is the
win and ghyll's gate ceremony is pure overhead. Stating the second half
is the position.

> **Status.** The correctness mechanism — typed gate clauses, role
> transitions as first-class arrows, the integrator feedback cycle —
> is implemented. Dialects, memory, drift detection, and streaming
> support it as continuity infrastructure. Design lives in
> [`specs/architecture/`](specs/architecture/).

> ## :warning: SANDBOX REQUIRED
>
> **ghyll executes tool calls from LLM output directly** --- no confirmation, no permission checks, no filtering. This is by design.
>
> **You MUST run ghyll inside a sandbox.** Without one, a compromised model endpoint can execute arbitrary code with your user privileges.
>
> | Platform | Sandbox | Description |
> |----------|---------|-------------|
> | **macOS** | [Sandbox]([https://github.com/anthropic-experimental/sandbox-runtime](https://igorstechnoclub.com/sandbox-exec/)) | MacOS Native Sandbox Runtime. |
> | **Linux** | [bubblewrap](https://github.com/containers/bubblewrap) | Unprivileged namespace sandboxing. |
> | **Any** | Docker/Podman | Container isolation. |
>
> <details><summary>Sandbox-exec example</summary>
>
> sandbox-exec uses a sandbox file (`~/.config/sandbox/ghyll.sb`) for fine-grained control:
>
> ```sandbox-file (permissive)
> (version 1)
> (allow default)
> (deny network*)
> (deny file-read-data (regex "^/Users/[^/]+/(Documents|Pictures|Desktop)"))
> ```
>
> ```bash
> sandbox-exec -f ~/.config/sandbox/ghyll.sh ghyll
> ```
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

## Correctness mechanism

The differentiator is **behavioral, not infrastructural**. ghyll's
correctness mechanism is a gate system, not drift detection.

- **Roles are fixed and first-class.** A diamond workflow (analyst →
  architect → implementer → auditor → integrator) is embedded and
  enforced as the default.
- **Role transitions are arrows.** Every transition is a first-class
  artifact with typed gate clauses. Transitions are legal only along
  declared arrows; undeclared transitions suspend, not silently proceed.
- **Clauses have two types.** Evaluation type (`machine` /
  `attested`) — who decides. Depth type (`depth-robust` /
  `depth-sensitive`) — what model depth is required to produce or
  evaluate the clause honestly.
- **`unevaluated` is a first-class status.** A `depth-sensitive` clause
  produced by an under-depth model is `unevaluated` — not
  `provisional`, not `fail`. It is the status that most resembles
  "green but will break on deployment" and must never be hidden.
- **Routing follows the gate, not the model.** A pass runs at the
  lowest tier meeting the maximum depth requirement of the clauses on
  the arrow. Self-assessed task complexity is not a routing input.
- **The workflow is a cycle.** Integrator findings of the type
  "missing/wrong cross-context specification" route back to the analyst
  for a grid amendment. Completion is revocable.
- **ghyll can refuse.** The definition phase detects projects where
  ghyll's friction is pure cost and recommends a fast agent instead.

Full design reference: [`specs/architecture/`](specs/architecture/).

## Continuity infrastructure

The gate system is the correctness mechanism. Dialects, memory, and
drift detection support continuity across sessions. Drift detection
is useful for recovering lost context; it is not what catches
shallow work.

| Model | Active params | Context | Tier |
|-------|--------------|---------|------|
| MiniMax M2.5 | 10B / 230B | 1M tokens | Fast |
| GLM-5 | 40B / 744B | 200K tokens | Deep |
| Kimi K2 | 32B / 1T | 256K tokens | Planned |

- Model-specific dialects — hand-tuned prompts, tool parsing, compaction per model
- Real-time streaming — tokens appear as they arrive, tool calls rendered inline
- Drift-aware memory — cosine similarity drift detection with checkpoint backfill (continuity, not correctness)
- Tamper-evident checkpoints — Merkle DAG with ed25519 signatures, git orphan branch sync
- Team memory — searchable checkpoints from all developers via vault server

## Documentation

**[witlox.github.io/ghyll](https://witlox.github.io/ghyll)**

Architectural reference (current code, not aspirational) lives in
[`specs/architecture/`](specs/architecture/).

## Development

```bash
make setup           # install tools + git hooks
make                 # lint + test + build
make coverage-check  # enforce 70% coverage
make docs-serve      # preview docs locally
```

## License

MIT
