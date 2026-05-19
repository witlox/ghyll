# Architecture flows — sequence diagrams

ASCII sequence diagrams for the three load-bearing flows in
ghyll's gate-and-arrow runtime. Each shows which components
participate and the order of operations.

---

## Flow 1: Operator attestation

The path a `/attest` slash command takes from REPL keystroke to
durable engine row + JSONL audit + tracker pulse.

```
Operator       Session       Attestation     Journal        Engine        JSONL          Tree           IBTracker
 (REPL)                       Store         (Consumer)      Store         Writer         Writer
   |              |              |              |              |              |              |              |
   | /attest      |              |              |              |              |              |              |
   |─────────────>|              |              |              |              |              |              |
   |              | parseAttestationRef                                                                        |
   |              | (id → arrow, clause, version)                                                              |
   |              |              |              |              |              |              |              |
   |              | Grid.Lookup(arrowID) → source/target roles                                                 |
   |              |              |              |              |              |              |              |
   |              | Record(rec)  |              |              |              |              |              |
   |              |─────────────>| validate     |              |              |              |              |
   |              |              | (§12.2 self- |              |              |              |              |
   |              |              | cert check)  |              |              |              |              |
   |              |              | + insert     |              |              |              |              |
   |              |              | + version++  |              |              |              |              |
   |              |              | + fanout-->-(observer slice copy under lock)                                |
   |              |              |              |              |              |              |              |
   |              |              | observer 1: ─>journal.enqueue(rec)         |              |              |
   |              |              | observer 2: ────────────────────────────>Write(line)+fsync                  |
   |              |              | observer 3: ──────────────────────────────────────────>Write(line)+fsync    |
   |              |              | observer 4: ──────────────────────────────────────────────────────>Record() |
   |              |              |              |              |              |              |              |
   |              |              |              | dequeue      |              |              |              |
   |              |              |              | INSERT OR IGNORE                                            |
   |              |              |              |─────────────>| persist     |              |              |
   |              |              |              |              | (immutable)  |              |              |
   |              |              |              |              |              |              |              |
   |   "✓ recorded"               |              |              |              |              |              |
   |<─────────────|              |              |              |              |              |              |
```

Key invariants:

- **Step 4** (fsync inside the JSONL Writer observer) returns
  BEFORE Record's caller proceeds. Per ADR-010 and the
  operator-attestation spec, "the file is fsync'd before the
  verdict is reported as accepted."
- **§12.2 enforcement** fires inside `validate` BEFORE any write.
  A self-cert attempt errors out with `ErrAttestationSelfCert`;
  no row hits any storage layer.
- **The Journal observer** is the durable path. The JSONL writers
  are the audit-trail path. The engine table is the source of
  truth per ADR-010.
- **The IBTracker** receives every verdict. Three consecutive
  `insufficient-basis` on the same clause emit
  `OpEventInsufficientBasisRoundsExceeded` on the bus.

---

## Flow 2: Adversarial cycle with producer-fix harness

The bounded multi-round remediation cycle from gates.md §11.

```
Dispatcher   AdversarialOrchestrator   Factory   Adversary   ProducerFixHarness   Producer   FindingsStore   OperatorBus
    |               |                     |          |              |                |             |              |
    | Run(attack)   |                     |          |              |                |             |              |
    |──────────────>|                     |          |              |                |             |              |
    |               |─ Round 1 ──────────>|          |              |                |             |              |
    |               |  factory.New(1)─────|          |              |                |             |              |
    |               |                     |          |              |                |             | round-start  |
    |               |                     |          |              |                |             |<─────────────|
    |               | Attack(ctx, attack) |          |              |                |             |              |
    |               |─────────────────────|─────────>|              |                |             |              |
    |               |                     |          | falsify      |                |             |              |
    |               |                     |          | open-sweep   |                |             | findings     |
    |               |                     |          | classify     |                |             | raised       |
    |               |                     |          |─Raise(F1)────|──────────────────────────────>|              |
    |               |<────────────────────|──────────|              |                |             |              |
    |               | report              |          |              |                |             |              |
    |               | open findings? YES  |          |              |                |             | producer-fix-|
    |               |─publish─────────────|──────────|──────────────|────────────────|─────────────|>signal       |
    |               |                     |          |              |                |             |              |
    |               | ProducerRemediate(ctx, open)                                                                |
    |               |──────────────────────────────────────────────>|                |             |              |
    |               |                     |          |              | round++        |             |              |
    |               |                     |          |              | digest-prev    |             |              |
    |               |                     |          |              | producer.Run() |             |              |
    |               |                     |          |              |────────────────|>(transition findings)        |
    |               |                     |          |              |<─artifact──────|             |              |
    |               |                     |          |              | sha256(artifact)              |             |
    |               |                     |          |              | loop-bomb check               |             |
    |               |                     |          |              |                |             |              |
    |               |<──────────────────────────────────────────────| nil OR ErrProducerLoopBomb                  |
    |               |                     |          |              |                |             |              |
    |               | re-check convergence|          |              |                |             |              |
    |               | (no open ≥ threshold? → exit)                                                                |
    |               |                     |          |              |                |             | converged    |
    |               |                     |          |              |                |             |<─────────────|
    |<──────────────|                     |          |              |                |             |              |
    |  result       |                     |          |              |                |             |              |
```

Key invariants:

- **Round-fresh Adversary** per ADR-014: every round the factory
  builds a new Adversary instance. The previous round's atomic
  `used` flag is dead state.
- **Twice-per-round convergence check**: once after Attack
  returns (no new findings + no above-threshold opens → exit),
  once after ProducerRemediate (zero open above threshold →
  exit mid-round). The double check avoids one wasted
  adversary round when the producer resolves everything.
- **Loop-bomb detection** is inside the harness, not the
  orchestrator. ProducerFn returns an artifact digest; identical
  digest across two rounds + still-open findings → abort with
  `ErrProducerLoopBomb`.

---

## Flow 3: Amendment commit

The `gates.md` §3.7 amendment flow: integrator-raised
"missing-cross-context-spec" finding → analyst response →
grid v(N+1).

```
Integrator   AmendmentQueue   AmendmentCommitter   PassRegistry   Pass   RoleLockTable   Grid   AmendmentObserver   OperatorBus
     |             |                  |                |           |          |             |          |                  |
     | Enqueue(req)|                  |                |           |          |             |          |                  |
     |────────────>|                  |                |           |          |             |          |                  |
     |             | byID + pending append              |           |          |             |          |                  |
     |             | observer fires: AmendmentEventEnqueue                                              |                  |
     |             |─────────────────────────────────────────────────────────────────────────────────>|                  |
     |             |                  |                |           |          |             |          | INSERT amendments  |
     |             |                  |                |           |          |             |          | (drained_at NULL)  |
     |             |                  |                |           |          |             |          |                  |
     | (analyst produces new ArrowDefinitions)                                                           |                  |
     | Commit(req, newArrows)         |                |           |          |             |          |                  |
     |────────────────────────────────>| validate(req)   |           |          |             |          |                  |
     |             |                  | acquire c.mu    |           |          |             |          |                  |
     |             |                  | (serialize commits)         |          |             |          |                  |
     |             |                  |                |           |          |             |          |                  |
     |             |                  | passes.All()──>|           |          |             |          |                  |
     |             |                  |<──────────────|           |          |             |          |                  |
     |             |                  | for each pass on SourceArrow + Open:                            |                  |
     |             |                  | p.Abort("amendment drained")───>(p.lockToken.Release)            |                  |
     |             |                  |                |           |─────────>|             |          | pass-closed       |
     |             |                  |                |           |          |             |          |<──────────────────|
     |             |                  |                |           |          |             |          |                  |
     |             |                  | for each newArrow: Grid.Append(def)─────────────────>|          |                  |
     |             |                  |                |           |          | version++   |          |                  |
     |             |                  |                |           |          |             |          |                  |
     |             |                  | queue.MarkDrained(req.ID)              |             |          |                  |
     |             |─delete byID, add seenIDs, emit AmendmentEventDrain───────────────────>|                              |
     |             |                                                                       | UPDATE amendments              |
     |             |                                                                       | SET drained_at = now WHERE id  |
     |             |                                                                       |                                |
     |             |                  | bus.Publish(amendment-drained)                                                   |
     |             |                  |───────────────────────────────────────────────────────────────────────>|
     |<──────────────────────────────| res = {GridVersionBefore, GridVersionAfter, AppendedArrows, AbortedPasses, ...}    |
```

Key invariants:

- **Pass abort runs BEFORE arrow append.** Per ADR-001x (the
  commit ADR; see `amendment_commit.go`): a partial-append
  failure leaves passes correctly aborted because they ran first.
- **drained_at persists** via the queue's `MarkDrained` →
  `AmendmentEventDrain` → journal observer → UPDATE. Without
  this, the amendment re-replays as pending on next session
  start.
- **`status=complete|partial-append-error`** on the operator
  event distinguishes between a clean commit and one where
  some arrows didn't land. The grid version still bumps forward
  for the arrows that succeeded — gates.md §3.7 forbids regress.

---

## Where the diagrams live in code

| Flow | Primary file(s) |
|---|---|
| Attestation | `runner/attestationstore.go`, `runner/attestation_jsonl.go`, `runner/attestation_tree.go`, `engine/attestations.go`, `engine/journal.go` |
| Adversarial cycle | `runner/orchestrator.go`, `runner/producer_fix.go`, `runner/adversarial.go` |
| Amendment commit | `runner/amendment.go`, `runner/amendment_commit.go`, `engine/journal.go` (handleAmendment) |
