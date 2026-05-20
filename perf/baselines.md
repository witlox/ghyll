# Performance Baselines

Captured 2026-05-20 on an AMD Ryzen 7 6800H (16 logical cores, Linux).
Re-run via `make bench`.

Numbers below are characterization, not regression gates — operators
re-baseline periodically. Watch for >2× drift in a single number;
that's drift worth investigating.

## Engine

| Benchmark | ns/op | B/op | allocs/op | Notes |
|-----------|------:|-----:|----------:|-------|
| Journal_FindingsRaiseDrain   |   ~40 |   16 |  1 | One channel send; sqlite INSERT happens on the journal consumer goroutine |
| Replay_NAttestations (N=500) | ~880k | 8507 | 173 | Per-replay: open store, walk 500 rows, hydrate Tier 2 fields |
| CatchUpAttestations (N=500)  | ~24m  | 2 MB | 49k | Per-Catch-Up: upsert 500 rows + readback-compare for idempotency. Heavy because each row hits sqlite individually |

## Runner

| Benchmark | ns/op | B/op | allocs/op | Notes |
|-----------|------:|-----:|----------:|-------|
| AttestationStore_Record           |    326 |  720 |  4 | Validate + dedup + map insert; gate-2 fanout-outside-lock |
| AttestationStore_Lookup           |     30 |    0 |  0 | RLock + map read |
| RoleContextLockTable_AcquireRel   |    163 |   64 |  1 | TryAcquire + Release on fresh tuple |
| OperatorBus_Publish               |     48 |    0 |  0 | RLock + snapshot copy + 1 subscriber call |
| OperatorBus_PublishConcurrent     |     51 |    0 |  0 | Same path under -RunParallel |
| InsufficientBasisTracker_Record   |     83 |    0 |  0 | Map increment + threshold check |
| Pass_OpenClose                    |    337 |  320 |  2 | OpenPass + Close + lock-table release |
| LockContention_64Goroutines       |  26447 | 5886 | 129 | 64 goroutines contending on the same (role, context) |
| Dispatcher_ClauseEval             |  13663 | 2891 |  37 | One Evaluate call (no I/O) |

## Watch-list (suggested drift gates)

- **AttestationStore_Lookup** > 100 ns/op → RLock contention regression
- **OperatorBus_Publish** > 200 ns/op → likely a subscriber doing
  synchronous work (gate-2 CONC-H-1/H-2/H-3 require subscribers to be fast)
- **Journal_FindingsRaiseDrain** > 200 ns/op → likely an Observer
  taking a lock under Raise's write lock
- **Replay_NAttestations** > 2× current → likely a SCAN regression
  on the attestations table (missing index?)
- **LockContention_64Goroutines** > 100k ns/op → fairness regression
  in RoleContextLockTable

## Methodology

- `make bench` runs `go test -bench=. -benchmem -run=^$ -benchtime=2s`
  against `./engine/...` and `./runner/...`.
- Baselines are recorded at the default `-benchtime=1s` (auto-tuned
  `b.N`). Re-baseline by appending the new run's output to this file
  with a dated header.
- Live-endpoint perf (latency, throughput) is NOT covered here —
  those numbers are endpoint-specific and live in the operator's
  config-tuning runbook.
