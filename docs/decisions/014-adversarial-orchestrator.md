# ADR-014: Adversarial orchestrator with factory + producer-fix harness

Date: 2026-05-19
Status: Accepted

## Context

gates.md §11 describes a bounded multi-round remediation cycle:

1. The adversary attacks the upstream artifact (clause
   falsification, open sweep, depth classification).
2. Findings raised this round trigger a producer-fix signal.
3. The producer attempts remediation (transitions findings,
   re-emits artifacts).
4. The next round re-attacks the entire artifact.
5. After `remediation-rounds-max` rounds without convergence,
   the orchestrator escalates to the operator.

Tier-1 of the prod-readiness rollout shipped two coordinated
components: `runner.AdversarialOrchestrator` (the bounded loop)
and `runner.ProducerFixHarness` (the producer-side hook with
loop-bomb detection). This ADR pins the design.

## Decision

### 1. Orchestrator uses an AdversaryFactory, not a single Adversary

`runner.Adversary` is single-shot per validation-pass-3 F3: an
`atomic.Bool` flips on first `Attack` so a second call errors. The
orchestrator runs N rounds; it cannot reuse one Adversary instance.

Therefore the orchestrator takes an `AdversaryFactory` — a
function that returns a fresh Adversary per round. The factory
typically wraps `NewAdversary(findings, classifications, runner)`
and applies the same `OpenSweep` / `Classify` hooks each call.

### 2. ProducerRemediate is a separate hook, optional

Some test paths drive remediation directly through the
FindingsStore without a producer (e.g., the operator manually
attests `accepted-risk`). Those paths pass `ProducerRemediate =
nil` and the orchestrator skips the producer call between rounds,
just re-checking convergence.

When a real producer participates, callers wrap it in
`ProducerFixHarness` and pass `harness.ProducerRemediate()` as
the hook. The harness adds two safety mechanisms:

- **Loop-bomb detection** via SHA-256 digest of the producer's
  response artifact. Same digest across two consecutive rounds
  + open findings → `ErrProducerLoopBomb`. The cycle aborts; the
  operator intervenes.
- **producer-fix-signal event** on the OperatorBus per round so
  observers (status CLI, JSONL writer, future operator UI) see
  the cycle's progress.

### 3. Convergence is checked twice per round

Round N flow:
1. Adversary fires; reports any newly-raised findings.
2. If no open-above-threshold findings AND no new findings this
   round → `OutcomeRemediationConverged` (early exit).
3. ProducerRemediate runs (if set).
4. Re-check open-above-threshold; if zero →
   `OutcomeRemediationConverged` (mid-round exit).
5. Otherwise loop.

The double check matters: a producer that resolves all findings
within its hook ends the cycle without an extra adversary round
(saves time + tier budget).

### 4. Outcomes are typed

`OrchestratorOutcome` is a wire-stable string enum:
`converged` / `escalated-after-max-rounds` / `producer-error` /
`canceled`. Callers (CLI surfaces, future operator UI) switch on
the outcome.

## Consequences

### Code

`runner/orchestrator.go` (~190 LOC) implements
`AdversarialOrchestrator`, `AdversaryFactory`, `DispatchRequest`,
`OrchestratorResult`, the four outcomes.

`runner/producer_fix.go` (~130 LOC) implements
`ProducerFixHarness`, `ProducerFn`, `ErrProducerLoopBomb`.

### Tests

`runner/orchestrator_test.go` — 8 tests covering happy
convergence, producer remediation, max-rounds escalation,
producer error, context cancel, lifecycle events.

`runner/producer_fix_test.go` — 8 tests covering happy path,
loop-bomb detection, same-artifact-with-no-open-findings
exception, fix-signal event, nil producer / harness, error
propagation, end-to-end integration with the orchestrator.

### Alternatives considered

1. **Single reusable Adversary with a Reset method.** Rejected:
   the spec's F3 invariant (single-use Adversary) ensures clean
   adversarial state per round; relaxing it would let stale
   per-round state leak across rounds.

2. **ProducerRemediate as a required (not optional) hook.**
   Rejected: many test scenarios drive remediation through
   manual finding transitions; forcing a producer would
   complicate them. nil is a clean "no remediation step"
   signal.

3. **Loop-bomb detection at the orchestrator layer (not the
   harness).** Rejected: the harness is the natural home for
   producer-side concerns (digest tracking is a producer
   property). The orchestrator stays focused on the cycle.

## Related

- gates.md §11 — adversarial cycle spec
- ADR-012 (OperatorEvent bus) — producer-fix-signal,
  remediation-converged, remediation-escalated kinds
- ADR-013 (Pass entity) — the orchestrator runs ABOVE the pass
  layer; one cycle may span multiple passes
- `runner/orchestrator.go`, `runner/producer_fix.go`,
  `runner/adversarial.go`
