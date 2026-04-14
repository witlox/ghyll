# Ubiquitous Language

Every term used once, with one meaning.

| Term | Definition | NOT |
|------|-----------|-----|
| **checkpoint** | An immutable snapshot of session state (summary + embedding + metadata + hash + signature) | Not the full conversation history |
| **compaction** | Reducing context size by summarizing older turns | Not deletion — information is preserved in summary form |
| **backfill** | Injecting checkpoint summaries into context to correct drift | Not replaying history — only relevant summaries |
| **drift** | Semantic divergence from original task, measured by embedding cosine similarity | Not token count or turn count |
| **dialect** | A model-specific module with concrete functions for prompt formatting, tool parsing, etc. | Not a provider adapter or interface implementation |
| **handoff** | Checkpoint-based context transfer between models during a switch | Not full history replay |
| **routing** | Selecting which model handles the current turn based on context state | Not load balancing (that's infrastructure) |
| **escalation** (routing) | Switching from fast tier to deep tier | Not a workflow escalation to analyst/architect |
| **model lock** (`--model`) | CLI flag that fixes the model for the entire session. No routing, no fallback, no revert. | Not the same as `/deep` |
| **temporary override** (`/deep`) | In-session switch to GLM-5 that auto-routing can revert when conditions clear | Not a lock — routing continues to evaluate |
| **escalation** (workflow) | Filing a spec gap from a later phase to an earlier phase | Context makes this unambiguous |
| **fast tier** | MiniMax M2.5 — low active params, high throughput | Not low quality — 80.2% SWE-bench |
| **deep tier** | GLM-5 — high active params, complex reasoning | Not slow — latency is acceptable for hard problems |
| **hash chain** | Sequence of checkpoints linked by parent hash, forming a Merkle DAG | Not a blockchain — no consensus, no tokens |
| **memory branch** | Git orphan branch `ghyll/memory` storing checkpoints | Not a code branch — never merges into main |
| **injection signal** | Detected pattern suggesting prompt injection attempt | Not a block — detection only, Tarn enforces |
| **vault** | Optional HTTP service for team memory search across repos | Not required — git sync works without it |
| **session** | One interactive coding conversation | Not persistent — sessions end when ghyll exits |
| **turn** | One user prompt + model response cycle (may include multiple tool calls) | Not a single API call |
| **tool** | A direct OS operation (bash, file, git, grep) | Not a plugin or extension |
| **context window** | Current messages being sent to the model | Not the full session history |
| **context depth** | Current token count in the context window | Not turn count |
| **tool depth** | Number of sequential tool calls in current chain without user input | Not total tool calls in session |
| **tier fallback** | Sending a request to the other model tier when the active tier is unreachable | Not routing — routing is about task complexity, fallback is about availability |
| **proactive compaction** | Token count check before each turn, compact if >90% of max | Not reactive — this is the normal path |
| **reactive compaction** | Compaction triggered by model rejection (context_length_exceeded) | Fallback for when proactive check underestimates |
| **vault token** | Shared bearer token for vault HTTP access, one per team | Not per-user auth — checkpoint signatures handle attribution |
| **device key** | Ed25519 key pair at ~/.ghyll/keys/, generated on first run | Not a user key — one per machine, not per person |
| **device ID** | Stable identifier for a machine, derived from hostname + machine ID | Not ephemeral — same across sessions on the same machine |
| **edit tool** | A tool that applies a surgical old_string→new_string replacement to a file | Not a full rewrite — only the matched region changes |
| **glob tool** | A tool that returns file paths matching a glob pattern | Not grep — searches file names, not file contents |
| **plan mode** | A dialect toggle that appends deeper-reasoning instructions to the system prompt | Not read-only — all tools remain available |
| **sub-agent** | A fresh model inference spawned by a tool call within the parent session | Not a separate session — shares the lockfile, has its own isolated context |
| **sub-agent context** | The context window of a sub-agent: system prompt + task description only | Not the parent's conversation — no history inheritance |
| **session resume** | Starting a new session pre-loaded with the previous session's final checkpoint summary | Not replay — no raw message history is restored |
| **web fetch** | A tool that retrieves a URL and returns its content as markdown | Not a browser — no JavaScript execution |
| **web search** | A tool that queries a search engine and returns structured results | Not an LLM query — results come from external search |
| **workflow** | The combination of project instructions, roles, and slash commands that guide model behavior | Not configuration — workflows shape reasoning, config shapes infrastructure |
| **role** | A behavioral constraint set loaded as a system prompt overlay | Not a persona — roles restrict and focus, not roleplay |
| **project instructions** | Markdown files in `.ghyll/` (or fallback `.claude/`) loaded into the system prompt at session start | Not configuration — instructions shape model behavior, not ghyll settings |
| **slash command** | A user-typed `/name` that injects a structured prompt from a command definition file | Not a REPL command like `/exit` — slash commands are user-defined workflow steps |
| **instruction budget** | Maximum tokens allocated for project instructions + role overlay in the system prompt | Not context budget — this is a subset reserved for instructions |
| **Tarn whitelist** | External domain approval managed by Tarn's Endpoint Security layer | Not ghyll's responsibility — ghyll retries, Tarn decides |
