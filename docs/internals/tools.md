# Tool Execution

ghyll provides twelve tools for direct OS operations and model coordination. All tools execute immediately with no permission checks --- Tarn handles sandboxing.

## Available Tools

### bash

Executes a shell command via `exec.Command("bash", "-c", command)`.

- **Timeout**: configurable (default 30s)
- **Output**: stdout captured, stderr captured separately
- **On timeout**: process killed, error returned

### read_file

Reads a file via `os.ReadFile(path)`.

- **Timeout**: configurable (default 5s)
- **Output**: file contents as string

### write_file

Writes content to a file via `os.WriteFile(path, content, 0644)`.

- **Timeout**: configurable (default 5s)
- **Output**: confirmation message

### grep

Searches for a pattern in a path. Prefers ripgrep (`rg`) if available, falls back to standard `grep -rn`.

- **Timeout**: configurable (default 30s)
- **No matches**: returns empty output (not an error)

### git

Executes git commands in the working directory via `exec.Command("git", args...)`.

- **Timeout**: configurable (default 30s)
- **Output**: stdout captured

### edit_file

Surgically replaces a string in a file. Uses compare-and-swap with SHA256 content hashing to prevent overwriting concurrent modifications.

- **Parameters**: `path`, `old_string`, `new_string`
- **Timeout**: configurable (default 5s)
- **Atomicity**: reads file, computes hash, writes to temp, re-reads and verifies hash, renames atomically
- **Errors**: old_string not found, ambiguous match (multiple occurrences), file modified during edit
- **Empty new_string**: deletes the matched text

### glob

Returns file paths matching a glob pattern, sorted by modification time (most recent first).

- **Parameters**: `pattern`, `path` (base directory)
- **Supports**: `**` for recursive matching (single `**` segment)
- **Symlinks**: broken and external-pointing symlinks are excluded; valid workspace symlinks are followed
- **Timeout**: configurable (default 30s)

### web_fetch

Fetches a URL and returns the content converted to markdown. Subject to Tarn's network whitelist.

- **Parameters**: `url`
- **Retry**: 3 attempts with exponential backoff on connection errors and 5xx responses
- **No retry**: on 4xx errors (immediate failure)
- **Truncation**: response capped at `web_max_response_tokens` (default 10,000) with `[truncated]` marker
- **Binary**: rejected with error
- **Timeout**: configurable (default 30s)

### web_search

Queries a search backend and returns structured results (numbered URLs).

- **Parameters**: `query`
- **Backend**: configurable (default: DuckDuckGo)
- **Retry**: same as web_fetch
- **Limit**: 10 results maximum
- **Timeout**: configurable (default 30s)

### agent

Spawns a focused sub-agent on the fast tier model. The sub-agent has its own isolated context (no parent conversation history), runs a mini turn-loop, and returns a final answer.

- **Parameters**: `task`
- **Model**: configurable (default: fast tier)
- **Turn limit**: configurable (default 20)
- **Token budget**: configurable (default 50,000)
- **Wall-clock timeout**: configurable (default 300s)
- **Tool access**: all tools except `agent`, `enter_plan_mode`, `exit_plan_mode`
- **Synchronous**: parent session blocks until sub-agent completes

### enter_plan_mode / exit_plan_mode

Model-initiated toggles for plan mode. Augments the system prompt with dialect-specific planning instructions. All tools remain available.

## Timeout Enforcement

Every tool execution uses `context.WithTimeout`. When the timeout fires:

- The process is killed (SIGKILL)
- `ToolResult.TimedOut = true`
- An error message is returned to the model

## Error Handling

Tool errors (non-zero exit code, file not found, etc.) are returned in `ToolResult.Error`. The error string is added to the context as the tool response, so the model can see what went wrong and adjust.

If the model sends malformed tool arguments (invalid JSON), ghyll returns a parse error instead of executing with empty arguments.
