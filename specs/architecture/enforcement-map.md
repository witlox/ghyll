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
