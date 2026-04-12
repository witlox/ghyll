# Configuration

ghyll reads its configuration from `~/.ghyll/config.toml`. A complete example is provided at [`config/example.toml`](https://github.com/witlox/ghyll/blob/main/config/example.toml):

```bash
cp config/example.toml ~/.ghyll/config.toml
# Edit endpoints to match your SGLang instances
```

## Models

Each model requires an endpoint, dialect, and max context size:

```toml
[models.m25]
endpoint = "https://inference.internal:8001/v1"
dialect = "minimax_m25"
max_context = 1000000

[models.glm5]
endpoint = "https://inference.internal:8002/v1"
dialect = "glm5"
max_context = 200000
```

Available dialects: `minimax_m25`, `glm5`.

## Routing

Controls automatic model selection:

```toml
[routing]
default_model = "m25"              # Start sessions on this model
context_depth_threshold = 32000     # Escalate to deep tier above this
tool_depth_threshold = 5            # Escalate after N sequential tool calls
enable_auto_routing = true          # Set false to disable routing
```

Override with `--model` flag: `ghyll run . --model glm5`

## Memory

Controls checkpointing and drift detection:

```toml
[memory]
branch = "ghyll/memory"             # Git orphan branch name
auto_sync = true                    # Background push/pull
sync_interval_seconds = 60          # Sync frequency
checkpoint_interval_turns = 5       # Checkpoint every N turns
drift_check_interval_turns = 5      # Check drift every N turns
drift_threshold = 0.7               # Cosine similarity threshold

[memory.embedder]
model_url = "https://huggingface.co/Xenova/gte-micro/resolve/main/model.onnx"
model_path = "~/.ghyll/models/gte-micro.onnx"
dimensions = 384
```

## Tools

Tool execution timeouts:

```toml
[tools]
bash_timeout_seconds = 30
file_timeout_seconds = 5
prefer_ripgrep = true
```

## Vault (optional)

Team memory search server:

```toml
[vault]
url = "https://vault.internal:9090"
token = "team-shared-secret"
```

Localhost vault (`http://localhost:9090`) doesn't require a token.

## Defaults

If a field is omitted, these defaults apply:

| Field | Default |
|-------|---------|
| `routing.default_model` | `"m25"` |
| `routing.context_depth_threshold` | `32000` |
| `routing.tool_depth_threshold` | `5` |
| `memory.branch` | `"ghyll/memory"` |
| `memory.sync_interval_seconds` | `60` |
| `memory.checkpoint_interval_turns` | `5` |
| `memory.drift_threshold` | `0.7` |
| `tools.bash_timeout_seconds` | `30` |
| `tools.file_timeout_seconds` | `5` |
