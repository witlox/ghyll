# Tool Definitions

JSON schema sent to the model in the `tools` array of each API request.
This is the contract between ghyll and the model — the model uses these
definitions to decide which tool to call and with what arguments.

## Existing tools

```json
{
  "name": "bash",
  "description": "Execute a shell command. Returns stdout and stderr.",
  "parameters": {
    "type": "object",
    "properties": {
      "command": { "type": "string", "description": "The shell command to execute" }
    },
    "required": ["command"]
  }
}
```

```json
{
  "name": "read_file",
  "description": "Read the contents of a file.",
  "parameters": {
    "type": "object",
    "properties": {
      "path": { "type": "string", "description": "Absolute or workspace-relative path to the file" }
    },
    "required": ["path"]
  }
}
```

```json
{
  "name": "write_file",
  "description": "Write content to a file, creating it if it doesn't exist.",
  "parameters": {
    "type": "object",
    "properties": {
      "path": { "type": "string", "description": "Absolute or workspace-relative path" },
      "content": { "type": "string", "description": "The full file content to write" }
    },
    "required": ["path", "content"]
  }
}
```

```json
{
  "name": "grep",
  "description": "Search file contents for a pattern. Uses ripgrep if available.",
  "parameters": {
    "type": "object",
    "properties": {
      "pattern": { "type": "string", "description": "Regex pattern to search for" },
      "path": { "type": "string", "description": "Directory or file to search in" }
    },
    "required": ["pattern"]
  }
}
```

```json
{
  "name": "git",
  "description": "Execute a git command in the workspace.",
  "parameters": {
    "type": "object",
    "properties": {
      "args": { "type": "string", "description": "Git subcommand and arguments (e.g. 'status', 'diff HEAD~1')" }
    },
    "required": ["args"]
  }
}
```

## New tools

```json
{
  "name": "edit_file",
  "description": "Surgically replace a string in a file. The old_string must match exactly once. More token-efficient than rewriting the entire file. Use empty new_string to delete matched text.",
  "parameters": {
    "type": "object",
    "properties": {
      "path": { "type": "string", "description": "Path to the file to edit" },
      "old_string": { "type": "string", "description": "The exact text to find and replace (must match exactly once)" },
      "new_string": { "type": "string", "description": "The replacement text (empty string to delete)" }
    },
    "required": ["path", "old_string", "new_string"]
  }
}
```

```json
{
  "name": "glob",
  "description": "Find files matching a glob pattern. Returns paths sorted by modification time.",
  "parameters": {
    "type": "object",
    "properties": {
      "pattern": { "type": "string", "description": "Glob pattern (e.g. '**/*.go', 'src/**/*.ts')" },
      "path": { "type": "string", "description": "Base directory to search in (default: workspace root)" }
    },
    "required": ["pattern"]
  }
}
```

```json
{
  "name": "web_fetch",
  "description": "Fetch a web page and return its content as markdown. Subject to network access approval. Response is truncated if too large.",
  "parameters": {
    "type": "object",
    "properties": {
      "url": { "type": "string", "description": "The URL to fetch" }
    },
    "required": ["url"]
  }
}
```

```json
{
  "name": "web_search",
  "description": "Search the web and return structured results (title, URL, snippet). Subject to network access approval.",
  "parameters": {
    "type": "object",
    "properties": {
      "query": { "type": "string", "description": "The search query" }
    },
    "required": ["query"]
  }
}
```

```json
{
  "name": "agent",
  "description": "Spawn a focused sub-agent to handle a task. The sub-agent runs on the fast model with its own context. Returns the sub-agent's findings. Use for exploration, search, or analysis tasks that don't need the full conversation context.",
  "parameters": {
    "type": "object",
    "properties": {
      "task": { "type": "string", "description": "A clear description of what the sub-agent should do" }
    },
    "required": ["task"]
  }
}
```

```json
{
  "name": "enter_plan_mode",
  "description": "Enter plan mode to think more deeply before acting. All tools remain available. Augments the system prompt with planning instructions.",
  "parameters": {
    "type": "object",
    "properties": {
      "reason": { "type": "string", "description": "Why plan mode is needed" }
    },
    "required": ["reason"]
  }
}
```

```json
{
  "name": "exit_plan_mode",
  "description": "Exit plan mode and return to normal operation.",
  "parameters": {
    "type": "object",
    "properties": {}
  }
}
```

## Tool set per context

| Context | Tools available |
|---------|----------------|
| Parent session | All 12 tools |
| Sub-agent | All except: agent, enter_plan_mode, exit_plan_mode (9 tools) |

## Notes

- Tool definitions are built at session start and included in every API request
- The model sees tool names and descriptions to decide which to call
- Tool argument JSON is parsed by cmd/ghyll and dispatched to tool/ functions
- New tools added here must also be added to dialect BuildMessages functions
