# Tool Execution

ghyll provides four tools for direct OS operations. All tools execute immediately with no permission checks --- Tarn handles sandboxing.

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

## Timeout Enforcement

Every tool execution uses `context.WithTimeout`. When the timeout fires:

- The process is killed (SIGKILL)
- `ToolResult.TimedOut = true`
- An error message is returned to the model

## Error Handling

Tool errors (non-zero exit code, file not found, etc.) are returned in `ToolResult.Error`. The error string is added to the context as the tool response, so the model can see what went wrong and adjust.

If the model sends malformed tool arguments (invalid JSON), ghyll returns a parse error instead of executing with empty arguments.
