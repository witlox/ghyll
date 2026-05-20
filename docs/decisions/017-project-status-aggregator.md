# ADR-017: ProjectStatus aggregator — pure read, no cache

**Status**: Accepted (2026-05-20)
**Context**: Tier 4 polish — backfilling the ADR for a component
shipped earlier (`runner/projectstatus.go`) without one.

## Context

The operator-facing surfaces (`ghyll engine status`, `ghyll arrow
show`, the operator HTTP endpoint planned for Tier 4) all need
the same project-level snapshot: arrow count, open passes,
finding counts by status, amendment backlog, attestation count
by kind. Each of these aggregations touches multiple runner-layer
stores (Findings, Classifications, Grid, AmendmentQueue,
PassRegistry, AttestationStore).

Three options were on the table:

1. **Cached aggregator** — `ProjectStatus` lives in the engine
   runtime, updated by observers on every store mutation.
   Surface reads are O(1). Cost: extra goroutine + cache
   invalidation logic across 5+ stores.
2. **Pure read function** — `CaptureProjectStatus(sources)` walks
   the stores at call time, returns a value. Surface reads are
   O(N stores). Cost: each call duplicates work.
3. **Lazy-cached aggregator** — cache the result with a TTL;
   re-walk when expired. Surface reads are O(1) within the TTL.
   Cost: staleness window the operator can't easily reason about.

## Decision

Option **2 — pure read function**.

`ProjectStatus` is an immutable snapshot returned by
`CaptureProjectStatus(StatusSources)`. The aggregator holds no
state across calls. Each call walks all the input stores anew.

```go
func CaptureProjectStatus(src StatusSources) ProjectStatus
```

`StatusSources` is a struct of pointers to the runner-side
stores; the caller assembles it from the engine runtime.

## Rationale

The aggregator surfaces hit at human cadence — a few times per
second at most (operator typing `/status`, an HTTP poller every
few seconds). Walking 5 stores costs sub-millisecond on the
benchmark dev host. The cache invariant — "the snapshot reflects
the stores' state at THIS moment" — is more valuable than the
CPU saved, because operator diagnosis depends on it. A cache that
lags the stores by even a few hundred ms produces "I just
attested but /status shows zero" confusion.

The pure-function design also eliminates the failure mode where a
mutation observer registration is forgotten — a new store added
in a later phase that doesn't subscribe its observer would leak
into a stale cache forever. The pure function picks up new
stores at the call site (the engine runtime explicitly threads
them in) without observer plumbing.

## Consequences

**Positive**:
- Operator surfaces always see fresh state — no "I see a phantom
  finding because the cache is stale" debugging.
- No goroutine + no cache-invalidation surface area.
- A new store added later just needs threading into
  `StatusSources`; no observer wiring.
- The aggregator is trivially testable: pass in mock stores,
  assert on the returned snapshot.

**Negative**:
- Each call duplicates work. Mitigated by the human-cadence call
  pattern + the small store sizes.
- A pathological caller (HTTP poller at 100 Hz) would burn CPU.
  We accept this — operators don't poll that fast; an automated
  HTTP forwarder is the only realistic case and it can subscribe
  to OperatorBus events directly (already a higher-resolution
  surface than the snapshot).

## Alternatives considered

- **Cached aggregator** (option 1): rejected per the staleness
  rationale above.
- **Lazy-cached aggregator** (option 3): rejected for the same
  reason; the operator's mental model demands "what I see is what
  is", not "what I see is what was up to N seconds ago."

## Implementation

- `runner/projectstatus.go` defines `ProjectStatus`,
  `PassSnapshot`, `FindingStatusCounts`, `StatusSources`, and
  `CaptureProjectStatus`.
- The engine runtime exposes a `ProjectStatus()` accessor that
  builds `StatusSources` from its embedded stores and calls
  `CaptureProjectStatus`.
- The `/status` REPL command + `ghyll engine status` CLI both
  render the snapshot via `Render(out)` on the result.
- Benchmark: `runner/projectstatus_test.go BenchmarkCapture`
  — sub-microsecond on typical projects (≤ 100 arrows).

## References

- `runner/projectstatus.go` (production)
- `runner/projectstatus_test.go` (contract)
- `cmd/ghyll/engine_status_cmd.go` (CLI consumer)
- Related ADRs: 010 (attestation store split), 013 (pass
  entity), 015 (pass persistence)
