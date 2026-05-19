# ADR-012: OperatorEvent bus is in-process pub/sub

Date: 2026-05-19
Status: Accepted

## Context

Tier-1 of the prod-readiness rollout shipped a runtime-wide event
bus (`runner.OperatorBus`) that several components publish to:

- `runner.Pass` publishes pass-opened / pass-closed.
- `runner.AmendmentCommitter` publishes amendment-drained.
- `runner.AdversarialOrchestrator` publishes adversarial-round-start /
  producer-fix-signal / remediation-converged / remediation-escalated.
- `runner.InsufficientBasisTracker` publishes
  insufficient-basis-rounds-exceeded.
- `runner.AttestationJSONLWriter` / `AttestationTreeWriter` publish
  attestation-audit-durability-failed.
- `runner.ProducerFixHarness` publishes producer-fix-signal.

The bus has 14 wire-stable `OperatorEventKind` values. Subscribers
include the JSONL writer, the engine status CLI's future
operator-event surface, and (eventually) an HTTP endpoint for
remote operators.

This ADR pins the design choices the bus has codified through Tier
1: in-process pub/sub, synchronous fanout outside the bus mutex,
no event persistence at the bus layer.

## Decision

### 1. In-process pub/sub, not a queue

The bus is `sync.RWMutex`-protected map of subscribers. `Publish`
fans out under a snapshot of the subscriber slice taken inside a
read lock; the slice is copied so a slow subscriber doesn't hold
the bus lock during its callback. There is no queue, no
backpressure, no dropped-event counter.

Rationale: every subscriber today is in-process (the JSONL writer
runs in the same Go runtime as the publisher). Adding a queue
would complicate ordering guarantees (subscribers see events in
publish order today) without solving a real problem.

When cross-process subscribers land (HTTP forwarder, external
audit-pipe), they get their own queue at the subscriber's edge —
not at the bus.

### 2. Fanout is synchronous; subscribers must be fast

Subscriber callbacks run inside the publisher's goroutine. The
spec for subscriber implementations is: do the minimum, hand off
to a goroutine if anything blocks. The journal-style observer
pattern that the AttestationStore uses on its own surface is the
template.

A slow subscriber today would block all publishers. We accept
this because the runtime's publishers (Pass, Orchestrator, etc.)
are themselves single-goroutine — there's no contention to
unblock.

### 3. Events are NOT persisted at the bus layer

Persistence is the subscriber's concern. The JSONL writer persists
attestation events via the AttestationStore Observer (a separate,
narrower channel). The engine.Journal persists state-machine
events via the runner-store Observer pattern. The bus is for
events that don't have a durable home — operator-facing signals,
escalations, lifecycle markers.

A future "operator event log" subscriber can opt into persisting
every bus event, but the bus itself remains volatile.

### 4. Wire-stable event kinds

`OperatorEventKind` is `string`. Adding a new event kind is
additive (subscribers that don't recognize the kind ignore it).
Renaming or repurposing a kind is a breaking change requiring an
ADR. The 14 current kinds are listed in `runner/operatorbus.go`.

## Consequences

### Code

`runner/operatorbus.go` (~130 LOC) implements `OperatorBus`,
`OperatorEvent`, the 14 `OperatorEventKind` constants, and
`OperatorEventSubscriber`. `NewOperatorBus()` + `WithClock(fn)`
construct one bus per session.

`engineRuntime` (the session-scoped runtime) holds one bus and
exposes it via `Bus()`. The bus is constructed in
`openEngineWithOptions`, fanned out to publishers + subscribers
in `attachJournal`, and lives until `closeEngine`.

### Test impact

Eight tests in `operatorbus_test.go` pin the design (publish fans
out, multiple subscribers each see, subscribe-during-publish
does not deadlock, custom clock, no-subscribers no-op, concurrent
publish).

### Alternatives considered

1. **A Go channel per subscriber.** Each Subscribe returns a
   chan; subscribers consume. Rejected: complicates the
   publisher's fanout (needs select+default for non-blocking
   send) and forces every subscriber to manage its own consumer
   goroutine. The callback API is simpler.

2. **A typed event interface with method dispatch instead of a
   Kind enum.** Rejected: the events have a shared envelope
   (ArrowID, ClauseID, PassID, OpID, Detail, Payload) that fits
   one struct better than 14 types. Adding a new kind shouldn't
   require a new type.

3. **Persistent bus (write every event to a log).** Rejected:
   the JSONL writer + engine.Journal already persist the events
   that matter; the bus carries ephemeral signals.

## Related

- `runner/operatorbus.go` — implementation
- `runner/operatorbus_test.go` — design pins
- ADR-010 (AttestationStore Observer) — the channel persistent
  attestation events flow through
- ADR-011 (per-(role, context) lock) — Pass publishes lifecycle
  events to the bus
