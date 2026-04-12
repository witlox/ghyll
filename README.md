# Ghyll

A purpose-built coding agent CLI for self-hosted open-weight models. Hyper-optimized for GLM-5 and MiniMax M2.5 on Cray EX infrastructure. Runs inside a [Tarn](https://github.com/witlox/tarn) sandbox.

## Features

- **Model-specific dialects** — no abstraction layer, direct optimization per model
- **Context-depth routing** — automatic escalation from fast tier (M2.5) to deep tier (GLM-5)
- **Drift-aware memory** — vector embedding checkpoints with cosine similarity drift detection
- **Git-native sync** — team memory via orphan branch, no additional infrastructure
- **Tamper-evident history** — Merkle DAG checkpoints with ed25519 signatures
- **Always-yolo execution** — Tarn handles sandboxing at the kernel level

## Quick Start

```bash
make
export GHYLL_CONFIG=~/.ghyll/config.toml
ghyll run .
```

## Documentation

- [Architecture Design](docs/architecture/design.md)
- [Architecture Decisions](docs/decisions/001-architecture.md)
- [Domain Model](specs/domain-model.md)
- [Behavioral Specs](specs/features/)

## Development Workflow

This project uses a phased workflow with Claude Code profiles. See [.claude/WORKFLOW.md](.claude/WORKFLOW.md).

## License

MIT
