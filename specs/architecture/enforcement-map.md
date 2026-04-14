# Enforcement Map

Every invariant → where it is enforced in code.

## Memory Integrity

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 1 | Hash chain unbroken | memory/crypto.go: VerifyChain() | Called on import of remote checkpoints |
| 2 | Signatures valid | memory/crypto.go: VerifyCheckpoint() | Called on import and before backfill use |
| 3 | Append-only | memory/store.go: no UPDATE/DELETE in SQL | Schema + code review, no update methods exposed |
| 4 | Hash deterministic | memory/crypto.go: CanonicalHash() | Single function, sorted keys, tested with fixtures |

## Context Management

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 5 | Single owner | context/manager.go | Only type with mutating methods on message slice |
| 6 | Token budget | cmd/ghyll → context/manager.go: PreTurnCheck() | cmd/ghyll calls dialect.TokenCount(), passes result to PreTurnCheck(). Manager cannot count independently — dialect is model-specific. |
| 7 | Compaction preserves recent | context/manager.go: compact() | Slice messages, preserve tail N |
| 8 | Backfill is additive | context/manager.go: ApplyBackfill() | Prepend only, no remove calls |

## Routing

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 9 | One model per turn | dialect/router.go: Evaluate() | Returns decision, applied between turns by cmd/ghyll |
| 10 | Handoff creates checkpoint | cmd/ghyll/session.go: handleHandoff() | Orchestrates: createCheckpoint() then dialect HandoffSummary(). Checkpoint creation is step 1, before formatting. |
| 11 | --model absolute | dialect/router.go: Evaluate() | First row in decision table, short-circuits all other logic |
| 11a | /deep temporary | dialect/router.go: Evaluate() | Row 2 + row 6: set override, evaluate revert on each turn |

## Sync

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 12 | Orphan branch isolation | memory/sync.go: InitBranch() | Creates orphan via temp clone, verified on init |
| 13 | Non-blocking sync | memory/syncloop.go: SyncLoop() | Goroutine with ticker, never blocks main loop |
| 14 | Idempotent sync | memory/sync.go + store.go | Content-hash filenames, INSERT OR IGNORE in sqlite |

## Tools

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 15 | No permission logic | tool/*.go | Code review: no os.Stat permission checks, no prompts |
| 16 | Timeout enforced | tool/*.go: all exec paths | context.WithTimeout wrapping every exec.Command |

## Embedding

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 17 | Graceful unavailability | memory/embedder.go: Embed() | Returns (nil, ErrEmbedderUnavail), callers degrade |

## Stream

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 18 | Retry before fallback | stream/client.go: SendStream() | Retry loop (3x) before returning fallback-eligible error |
| 19 | Fallback reformats | cmd/ghyll/session.go: handleHandoff() | Calls dialect HandoffSummary before resending |
| 20 | Partial surfaced | stream/client.go: SendStream() | Returns Response with Partial=true on interrupt |
| 20a | Fallback requires auto-routing | cmd/ghyll/session.go | Checks modelLocked before fallback |

## Compaction

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 21 | Proactive before reactive | context/manager.go: PreTurnCheck() | Called before every Send(); reactive path only on StreamError |
| 22 | Compaction creates checkpoint | context/manager.go: compact() | Calls CreateCheckpoint callback with model's summary |
| 23 | Reactive retry once | cmd/ghyll/session.go: sendAndProcess() | Single retry after ReactiveCompact |
| 24a | Separate API call | context/manager.go: compact() | Builds CompactionRequest, executes via CompactionCall callback provided by cmd/ghyll (wires stream.Send). No direct context/ → stream/ dependency. |
| 24b | Compact before handoff | cmd/ghyll/session.go: handleHandoff() | Router returns NeedCompaction=true, session runs compact then handoff |

## Vault

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 25 | Vault optional | cmd/ghyll + memory/vault_client | nil VaultClient when config has no [vault], all callers check nil |
| 26 | Localhost no token | memory/vault_client.go: NewVaultClient() | No auth header when token is empty |
| 27 | No unverified backfill | context/manager.go: ApplyBackfill() | Caller verifies via memory/crypto.VerifyCheckpoint() before injecting |

## Drift

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 28 | Most recent checkpoint | context/drift.go: MeasureDrift() | Caller queries store for latest checkpoint by session |

## Keys

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 29 | Key before checkpoint | cmd/ghyll/main.go: cmdRun() | LoadOrGenerateKey at startup, before any checkpoint creation |
| 30 | Public keys sync | memory/sync.go: WritePublicKey() / ReadPublicKey() | Reads/writes devices/ directory on memory branch |

## Session

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 31 | One session per repo | cmd/ghyll/lockfile.go: AcquireLock() | O_CREATE\|O_EXCL atomic file, PID staleness check |
| 32 | Lockfile released | cmd/ghyll/main.go: defer ReleaseLock() | Deferred in main + signal handler |

## Edit Tool

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 33 | Edit is CAS-atomic | tool/edit.go: EditFile() | Read file + compute SHA256 of content, find match, write replacement to temp file, re-read original and compare SHA256, rename temp to original only if hash unchanged. Immune to filesystem timestamp resolution issues. |
| 34 | Edit match unambiguous | tool/edit.go: EditFile() | strings.Count(content, old_string) must equal 1 |

## Glob Tool

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 35 | Existing workspace-local paths only | tool/glob.go: Glob() | filepath.WalkDir with symlink check: skip if target outside workspace or broken |

## Plan Mode

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 36 | Advisory only | cmd/ghyll/session.go | Plan mode modifies system prompt only; tool dispatch has no plan mode check |
| 37 | Survives compaction | cmd/ghyll/session.go: RoutingState.PlanMode | Session-level flag, not in context messages; compaction cannot touch it |

## Sub-Agents

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 38 | Isolated and role-free | cmd/ghyll/subagent.go: RunSubAgent() | Creates new context/manager with system prompt + task only, no role overlay, no parent messages |
| 39 | Shares session lockfile | cmd/ghyll/subagent.go: RunSubAgent() | Does not call AcquireLock(); operates within parent's lock scope |
| 40 | Turn-loop terminates | cmd/ghyll/subagent.go: RunSubAgent() | Counter incremented per turn; if >= config.SubAgent.MaxTurns → return partial |
| 41 | Tool calls bounded | cmd/ghyll/subagent.go: RunSubAgent() | Agent tool excluded from sub-agent tool set at dispatch |
| 41a | Token budget enforced | cmd/ghyll/subagent.go: RunSubAgent() | Accumulate stream.Usage.TotalTokens per turn; if > config.SubAgent.TokenBudget → return partial |

## Session Resume

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 42 | Loads checkpoint not history | cmd/ghyll/main.go: cmdRun() | Queries store for latest reason="shutdown" checkpoint; injects summary only |
| 43 | Requires existing checkpoint | cmd/ghyll/main.go: cmdRun() | If query returns nil → warn "no previous session", start fresh |

## Web Fetch / Search

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 44 | Retry on transient failure | tool/web.go: WebFetch(), WebSearch() | Retry loop (3x, exponential backoff) on connection error / 5xx; immediate fail on 4xx |
| 45 | Plain text, size-bounded | tool/web.go: WebFetch() | HTML→markdown conversion, truncate at config.Tools.WebMaxResponseTokens with "[truncated]" marker; reject binary Content-Type |

## Workflow

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 46 | Instructions survive compaction | cmd/ghyll/session.go | Instructions injected as system prompt prefix; context/manager never includes system messages in compaction input |
| 47 | Project instructions authoritative | workflow/loader.go: Load() | Concatenate global first, project appended; no algorithmic conflict detection |
| 48 | Instruction budget enforced | cmd/ghyll/session.go: buildSystemPrompt() | workflow/ returns raw merged content. cmd/ghyll calls dialect.TokenCount on combined text, truncates if over budget (two-phase: drop global first, then project from end). workflow/ has no dialect dependency. |
| 49 | Slash commands are user messages | cmd/ghyll/repl.go: handleCommand() | Load command file content, pass to context/manager as role="user" message |
| 50 | Role switch non-destructive | cmd/ghyll/session.go: switchRole() | Replace role portion of system prompt string; no context/manager mutations, no checkpoint |
| 51 | Workflow folder fallback | workflow/loader.go: Load() | Try .ghyll/ first; if absent, iterate config.Workflow.FallbackFolders; map CLAUDE.md → instructions |
