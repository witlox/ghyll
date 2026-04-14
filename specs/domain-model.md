# Domain Model

## Core Concepts

### Session
A single interactive coding conversation between a developer and ghyll. Has a lifecycle: start → turns → (optional: model switch, compaction, backfill) → end. Produces checkpoints.

### Turn
One request-response cycle within a session. A turn may involve multiple tool calls. Each turn has an input (user prompt + tool results) and an output (model response + tool calls).

### Dialect
Model-specific code that handles prompt formatting, tool call parsing, token counting, system prompts, and compaction prompts. Each supported model has exactly one dialect. Dialects are concrete — no shared interface.

### Context Window
The current set of messages being sent to the model. Managed by the context manager. Subject to compaction when approaching the model's limit. Subject to backfill when drift is detected.

### Checkpoint
A snapshot of session state at a point in time. Contains: structured summary, vector embedding, metadata (files touched, tools used, turn number), hash chain link, and ed25519 signature. Append-only — never modified after creation.

### Hash Chain (Merkle DAG)
Checkpoints are linked by hash: each checkpoint contains the hash of its parent. Multiple branches are allowed (one per device). Tamper-evident — modifying any checkpoint breaks all subsequent hashes. Signed — each checkpoint carries an ed25519 signature from its author.

### Drift
Semantic divergence between the current conversation context and recent work. Measured by cosine similarity between the current context embedding and the most recent checkpoint's embedding (or checkpoint 0 if no other checkpoints exist). When similarity drops below a threshold, backfill is triggered.

### Backfill
Injection of relevant checkpoint summaries into the current context to correct drift. Retrieves the top-k most semantically similar checkpoints and prepends them as context. Model-aware — the backfill format matches the active dialect.

### Compaction
Reduction of context size when approaching the model's token limit. Summarizes older turns while preserving recent turns and key decisions. Model-aware — uses the dialect's compaction prompt for the active model.

### Routing
Selection of which model handles the current turn. Based on context depth (token count), tool call depth, and explicit user override. Handled by the dialect router, not an external service.

### Handoff
Transfer of session context from one model to another during a model switch. Uses checkpoint-based approach: create a checkpoint, start the new model with checkpoint summary + recent turns in the target dialect's format. Intentionally lossy.

### Memory Sync
Replication of checkpoints between ghyll instances via git. Uses an orphan branch (`ghyll/memory`) in the project's git repo. Append-only — no merge conflicts. Background sync on configurable interval.

### Team Memory
Checkpoints from all developers working on the same repo, accessible via vector similarity search. Attributed — each checkpoint shows who wrote it and when. Tamper-evident via hash chain verification.

### Injection Signal
Detection of prompt injection patterns in conversation turns. Checked at checkpoint creation time. Patterns include: instruction override attempts, base64 payloads, requests for files outside workspace, attempts to modify system prompts. Reported to developer, not blocked (Tarn handles enforcement).

### Device Key
An ed25519 key pair stored at `~/.ghyll/keys/`. Generated on first run. The private key signs checkpoint hashes; the public key is distributed to the team via the memory branch at `devices/<device-id>.pub`. Key presence is required for checkpoint creation.

### Tier Fallback
When the active model's endpoint is unreachable after retries, the stream client falls back to the other tier. If both tiers are unreachable, the session stays open for manual retry. Fallback reformats context for the alternate dialect.

### Vault Auth
Bearer token from config.toml for remote vault access. Localhost vault requires no auth. The token controls access; checkpoint ed25519 signatures provide integrity and attribution. One shared token per team.

### Edit Tool
A tool that applies a surgical string replacement to a file. Given a path, an old string, and a new string, it finds the exact match in the file and replaces it. Fails if the old string is not found or matches multiple locations (ambiguous). More token-efficient than rewriting entire files via write_file.

### Glob Tool
A tool that returns file paths matching a glob pattern within a directory. Supports standard glob syntax (`**/*.go`, `src/**/*.ts`). Returns structured output (list of paths) sorted by modification time. Separates file discovery (glob) from content search (grep).

### Plan Mode
A dialect-level behavioral toggle that instructs the model to reason more deeply before acting. When active, the system prompt is augmented with planning-oriented instructions per dialect. All tools remain available — plan mode is not a permission gate. Can be activated by the user (`/plan` REPL command) or by the model (via `enter_plan_mode` tool call). Deactivated by `/fast` or `exit_plan_mode` tool call.

### Sub-Agent
A focused model inference dispatched as a tool call within the parent session. The parent model calls the `agent` tool with a task description. The sub-agent receives a fresh context (system prompt + task + project instructions), runs a mini turn-loop (prompt → tools → prompt → ... → final answer), and returns the result as a tool result to the parent. Defaults to the fast tier (M2.5). Shares the session lockfile but has its own context manager. Does not inherit the parent's conversation history.

### Session Resume
Restarting a session with continuity from a previous session's final checkpoint. The resume loads the last checkpoint's summary and metadata, injecting it as backfill context in the new session. No raw message history is restored — only the structured summary. The first checkpoint of the resumed session contains a `resumed_from` field linking to the predecessor session's ID and final checkpoint hash, preserving traceability. Activated via `ghyll run . --resume`.

### Web Fetch
A tool that retrieves a URL's content and returns it as markdown. Subject to Tarn's network whitelist — if Tarn blocks the domain, the tool retries with exponential backoff (same pattern as the stream client). After retries exhausted, returns a descriptive error suggesting the user approve the domain in Tarn. No JavaScript execution.

### Web Search
A tool that queries an external search engine (DuckDuckGo or similar self-hostable backend) and returns structured results (title, URL, snippet). Subject to the same Tarn whitelist and retry behavior as web fetch.

### Workflow
The system of project instructions, roles, and slash commands that guide model behavior during a session. Loaded from `.ghyll/` in the project directory, with fallback to `.claude/` or similar. Consists of three parts: instructions (general behavioral guidance), roles (constraint sets that focus model reasoning), and commands (user-defined prompt injections).

### Project Instructions
Markdown files loaded into the system prompt at session start. Two tiers: global (`~/.ghyll/instructions.md`) and project-level (`<repo>/.ghyll/instructions.md`). Global provides baseline behavioral guidance (e.g., "always use BDD with TDD"). Project-level provides repo-specific guidance and overrides global on conflict. Total instruction content is bounded by the instruction budget (configurable, dialect-aware). Survives compaction — injected at system level, never summarized.

### Role
A behavioral constraint set loaded as a system prompt overlay. Defined as markdown files in `.ghyll/roles/` (or `~/.ghyll/roles/`). When a role is active, its content is appended to the dialect's system prompt. Role directives override the base system prompt on conflict. Role switching is automatic — the model reads project instructions (which contain the workflow router) and activates the appropriate role based on project state and intent.

### Slash Command
A user-defined REPL command that injects a structured prompt. Defined as markdown files in `.ghyll/commands/` (or `~/.ghyll/commands/`). When the user types `/name`, the corresponding command file's content is injected into the conversation as a user message. Simple prompt injection — no tool bundling or scripting.

### Instruction Budget
The maximum number of tokens allocated for project instructions + active role overlay in the system prompt. Dialect-aware — smaller for models with shorter context windows. Ensures instructions don't consume excessive context space. Configured in config.toml, with sensible defaults per dialect.

## Package Mapping

| Concept | Primary Package | Secondary |
|---------|----------------|-----------|
| Session | cmd/ghyll | context/ |
| Turn | stream/ | dialect/ |
| Dialect | dialect/ | — |
| Context Window | context/ | dialect/ |
| Checkpoint | memory/ | context/ |
| Hash Chain | memory/ | — |
| Drift | context/ | memory/ |
| Backfill | context/ | memory/ |
| Compaction | context/ | dialect/ |
| Routing | dialect/ | context/ |
| Handoff | dialect/ | memory/ |
| Memory Sync | memory/ | tool/git |
| Team Memory | vault/ | memory/ |
| Injection Signal | context/ | — |
| Device Key | memory/ | cmd/ghyll |
| Tier Fallback | stream/ | dialect/ |
| Vault Auth | vault/ | config/ |
| Edit Tool | tool/ | — |
| Glob Tool | tool/ | — |
| Plan Mode | dialect/ | cmd/ghyll |
| Sub-Agent | cmd/ghyll | dialect/, context/, stream/, tool/ |
| Session Resume | cmd/ghyll | memory/ |
| Web Fetch | tool/ | — |
| Web Search | tool/ | — |
| Workflow | cmd/ghyll | config/, dialect/ |
| Project Instructions | cmd/ghyll | config/ |
| Role | cmd/ghyll | dialect/ |
| Slash Command | cmd/ghyll | — |
| Instruction Budget | config/ | dialect/ |
