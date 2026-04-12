# Enforcement Map

Every invariant → where it is enforced in code.

## Memory Integrity

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 1 | Hash chain unbroken | memory/store.go: VerifyChain() | Called on import of remote checkpoints |
| 2 | Signatures valid | memory/store.go: VerifySignature() | Called on import and before backfill use |
| 3 | Append-only | memory/store.go: no UPDATE/DELETE in SQL | Schema + code review, no update methods exposed |
| 4 | Hash deterministic | memory/checkpoint.go: CanonicalHash() | Single function, sorted keys, tested with fixtures |

## Context Management

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 5 | Single owner | context/manager.go | Only type with mutating methods on message slice |
| 6 | Token budget | cmd/ghyll → context/manager.go: PreTurnCheck() | cmd/ghyll calls dialect.TokenCount(), passes result to PreTurnCheck(). Manager cannot count independently — dialect is model-specific. |
| 7 | Compaction preserves recent | context/compactor.go: Compact() | Slice messages, preserve tail N |
| 8 | Backfill is additive | context/manager.go: ApplyBackfill() | Prepend only, no remove calls |

## Routing

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 9 | One model per turn | dialect/router.go: Evaluate() | Returns decision, applied between turns by cmd/ghyll |
| 10 | Handoff creates checkpoint | cmd/ghyll: HANDOFF state | Orchestrates: memory.Create() then dialect.HandoffSummary(). Checkpoint creation is step 2, before formatting. |
| 11 | --model absolute | dialect/router.go: Evaluate() | First row in decision table, short-circuits all other logic |
| 11a | /deep temporary | dialect/router.go: Evaluate() | Row 2 + row 6: set override, evaluate revert on each turn |

## Sync

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 12 | Orphan branch isolation | memory/sync.go: InitBranch() | git checkout --orphan, verified on init |
| 13 | Non-blocking sync | memory/sync.go: syncLoop() | Goroutine, channel-based, never blocks main loop |
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
| 18 | Retry before fallback | stream/client.go: Send() | Retry loop (3x) before returning fallback-eligible error |
| 19 | Fallback reformats | cmd/ghyll: fallback handler | Calls dialect/handoff before resending |
| 20 | Partial surfaced | stream/client.go: Send() | Returns StreamResponse with Partial=true on interrupt |
| 20a | Fallback requires auto-routing | cmd/ghyll: fallback handler | Checks RoutingState.ModelLocked before fallback |

## Compaction

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 21 | Proactive before reactive | context/manager.go: PreTurnCheck() | Called before every Send(); reactive path only on StreamError |
| 22 | Compaction creates checkpoint | context/compactor.go: Compact() | Calls memory/checkpoint.Create() before returning |
| 23 | Reactive retry once | cmd/ghyll: main loop | Counter, max 1 retry after reactive compaction |
| 24a | Separate API call | context/compactor.go: Compact() | Builds CompactionRequest, executes via CompactionCall callback provided by cmd/ghyll (wires stream.Send). No direct context/ → stream/ dependency. |
| 24b | Compact before handoff | cmd/ghyll: TURN step 3 + HANDOFF | Router returns NeedCompaction=true, cmd/ghyll runs COMPACT then HANDOFF |

## Vault

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 25 | Vault optional | cmd/ghyll + memory/vault_client | nil VaultClient when config has no [vault], all callers check nil |
| 26 | Localhost no token | memory/vault_client.go: NewClient() | Parse URL, detect localhost, skip auth header |
| 27 | No unverified backfill | context/manager.go: ApplyBackfill() | Calls memory/store.Verify() before injecting |

## Drift

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 28 | Most recent checkpoint | context/drift.go: Measure() | Query store for latest checkpoint by session, fall back to c0 |

## Keys

| # | Invariant | Enforcement point | Mechanism |
|---|-----------|------------------|-----------|
| 29 | Key before checkpoint | memory/checkpoint.go: Create() | Loads key, returns ErrKeyPermissions or generates if missing |
| 30 | Public keys sync | memory/sync.go: SyncKeys() | Reads/writes devices/ directory on memory branch |
