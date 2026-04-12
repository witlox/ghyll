# Getting Started

## Prerequisites

- Go 1.22+
- Git (for memory sync)
- Access to SGLang endpoints serving GLM-5 and/or MiniMax M2.5
- [Tarn](https://github.com/witlox/tarn) sandbox (recommended)

## Installation

### From source

```bash
git clone https://github.com/witlox/ghyll
cd ghyll
make build-bin
```

Binaries are placed in `bin/ghyll` and `bin/ghyll-vault`.

### From release

Download the latest release from [GitHub Releases](https://github.com/witlox/ghyll/releases).

## Configuration

Create `~/.ghyll/config.toml`:

```toml
[models.m25]
endpoint = "https://inference.internal:8001/v1"
dialect = "minimax_m25"
max_context = 1000000

[models.glm5]
endpoint = "https://inference.internal:8002/v1"
dialect = "glm5"
max_context = 200000

[routing]
default_model = "m25"
context_depth_threshold = 32000
tool_depth_threshold = 5
enable_auto_routing = true

[memory]
branch = "ghyll/memory"
auto_sync = true
sync_interval_seconds = 60
checkpoint_interval_turns = 5
drift_threshold = 0.7

[tools]
bash_timeout_seconds = 30
file_timeout_seconds = 5
prefer_ripgrep = true
```

## First Session

```bash
cd ~/repos/myproject
ghyll run .
```

The prompt shows the active model and working directory:

```
ghyll [m25] ~/repos/myproject >
```

Type a coding request and ghyll will use the model to help, executing tool calls as needed.

## Key Commands

| Command | Effect |
|---------|--------|
| `/deep` | Temporarily switch to GLM-5 (deep tier) |
| `/fast` | Restore auto-routing |
| `/status` | Show current model, turn count, tool depth |
| `/exit` | End session |

## Optional: Embedding Model

Drift detection requires an ONNX embedding model and the ONNX Runtime shared library.

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

This downloads the GTE-micro model (~60MB) to `~/.ghyll/models/gte-micro.onnx`.

### Build with CGO

The ONNX embedder requires CGO. The default `make build-bin` uses `CGO_ENABLED=0` (static binaries, no ONNX). To build with ONNX support:

```bash
CGO_ENABLED=1 go build -ldflags="-s -w" -o bin/ghyll ./cmd/ghyll
```

Without ONNX, ghyll works fine --- drift detection is disabled gracefully.

## Configuration Reference

A complete example configuration is at [`config/example.toml`](https://github.com/witlox/ghyll/blob/main/config/example.toml). Copy it to get started:

```bash
cp config/example.toml ~/.ghyll/config.toml
```

See [Configuration](configuration.md) for all options and defaults.

## Optional: Vault Server

For team memory search across repos:

```bash
# Add to ~/.ghyll/config.toml:
[vault]
url = "https://vault.internal:9090"
token = "team-shared-secret"

# Run the vault server:
ghyll-vault
```
