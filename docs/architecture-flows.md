# Architecture flows — sequence diagrams

ASCII sequence diagrams for the three load-bearing flows in
ghyll's gate-and-arrow runtime. Each shows which components
participate and the order of operations.

---

## Flow 1: Operator attestation (CLI `/attest`)

The path a `/attest` slash command takes from REPL keystroke to
durable engine row + JSONL audit + tracker pulse. This is the
CLI escape-hatch path the operator types when they want to attest
a clause out-of-band; the routine path is Flow 4 (the Tier 2
verdict modal).

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
  BEFORE `Record` returns to the CLI handler. Per ADR-010 and the
  operator-attestation spec, the JSONL line is fsync'd before
  `/attest` prints its success message back to the operator — so
  a crash between Record and the prompt cannot leave the operator
  with a "succeeded" message and no on-disk record. (The modal
  surface in Flow 4 has the same invariant phrased in modal terms:
  the verdict is reported as accepted only after the writers
  return.)
- **§12.2 enforcement** fires inside `validate` BEFORE any write.
  A self-cert attempt errors out with `ErrAttestationSelfCert`;
  no row hits any storage layer.
- **The Journal observer** is the durable path. The JSONL writers
  are the audit-trail path. The engine table is the source of
  truth per ADR-010.
- **The IBTracker** receives every verdict — whether the verdict
  came in via the CLI here or via the modal in Flow 4. Three
  consecutive `insufficient-basis` on the same clause emit
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

## Flow 4: Tier 2 verdict modal (ADR-016)

The path a dispatcher-requested attestation takes from "clause
is awaiting verdict" through the interactive REPL modal to a
persisted `AttestationRecord`. Distinct from Flow 1 (which is
the `/attest` CLI escape hatch); this is the routine path.

```
Dispatcher    OperatorBus    modalDriver    LineReader    TermModal    AttestationStore   Tree+JSONL
   |              |              |              |             |              |              |
   | clause c flips AwaitingAttestation=true                                                  |
   | Publish(OpEventAttestationRequested, payload={src/tgt/ctx/stratum/grid_ver/adv_role,    |
   |          Detail=hint-json with arrow_id, clause_id, concept, attest_ref})               |
   |─────────────>|              |              |             |              |              |
   |              | fanout       |              |             |              |              |
   |              |─────────────>| OnEvent(ev)  |             |              |              |
   |              |              | enqueueVerdict(ev):        |             |              |
   |              |              |   - parse hint json (capped at 64 KiB)                   |
   |              |              |   - copy Payload onto modalRequest                       |
   |              |              |   - dedup via inFlight[attest_ref]                       |
   |              |              |   - cap-check pending queue                              |
   |              |              | <─enqueue or drop+OpEventModalBackpressure               |
   |              |              |              |             |              |              |
   |  --- REPL turn boundary: DrainPending fires before ui.Print(prompt) ----                |
   |              |              |              |             |              |              |
   |              |              | DrainPending(sessionCtx):                                |
   |              |              |   snapshot pending; pending=nil                          |
   |              |              |   for req in snapshot:                                   |
   |              |              |     if ibTracker.IsCrossed(c) → handleEscalation         |
   |              |              |     else → handleVerdict                                 |
   |              |              | PresentVerdict(ctx, hint)                                |
   |              |              |─────────────>| Next(ctx)   |              |              |
   |              |              |              |────────────>| (blocks)     |              |
   |              |              |              |             | operator types verdict      |
   |              |              |              |<────────────| "pass"       |              |
   |              |              |<─VerdictSubmission{Pass, Confirm, payload}               |
   |              |              | buildRecord(req, sub):                                   |
   |              |              |   opID = opIDProvider()  (read AT CALL TIME — gate-1 F-20) |
   |              |              |   resolve src/tgt/ctx/stratum via arrowResolver fallback   |
   |              |              | ValidateUnitPayload(unit, payload, residueMaxBytes)        |
   |              |              | store.Record(rec)         |             |              |
   |              |              |───────────────────────────────────────>|              |
   |              |              |                            |             | validateAttestation
   |              |              |                            |             | + validateAttestationTier2
   |              |              |                            |             | (PassID + Unit + payload +
   |              |              |                            |             |  HintJSON + adv-role "__")
   |              |              |                            |             | primaryWriter (tree)─────>|
   |              |              |                            |             |              | EncodeAttestationPath(rec)
   |              |              |                            |             |              | (purefn; safeSegment hashes
   |              |              |                            |             |              |  '.'/'..'/oversize; init special-case)
   |              |              |                            |             |              | append + fsync (gate-1 F-11)
   |              |              |                            |             |              | publish OpEventPathTruncated
   |              |              |                            |             |              | if hash-substituted
   |              |              |                            |             |<─────────────| ok
   |              |              |                            |             | byID[id] = rec; version++
   |              |              |                            |             | snapshot observers; release s.mu
   |              |              |                            |             | (CONC-H-3: fanout OUTSIDE lock)
   |              |              |                            |             | observers run:
   |              |              |                            |             |  - JSONL writer (forward-only Observer)
   |              |              |                            |             |  - IBTracker.Record (verdict → reset/inc)
   |              |              |                            |             |  - Journal observer → engine row
   |              |              | If sub.Verdict == AttestationFail:                       |
   |              |              |   bus.Publish(OpEventClauseFailVerdict) BEFORE Record    |
   |              |              |   (CONC-M-1: fail signal survives Record reject)         |
   |              |              | clear inFlight[attest_ref]                               |
   |              |              | continue snapshot iter; bound at 8 rounds                |
```

Escalation path (after 3 consecutive `insufficient-basis`):

```
   |              |              | handleEscalation(req):                                   |
   |              |              | PresentEscalation(ctx, hint)                             |
   |              |              |─────────────>| Next(ctx) → option (1 or 2) + residue/rationale         |
   |              |              | bus.Publish(OpEventEscalationPresented) AFTER prompt     |
   |              |              |              | (CONC-H-5: paired with Resolved)         |
   |              |              | opt 1 → verdict=pass + residue; opt 2 → verdict=fail + rationale       |
   |              |              | store.Record(rec)         |             |              |
   |              |              | ibTracker.Reset(clause)                                  |
   |              |              | bus.Publish(OpEventEscalationResolved)                   |
```

Key invariants:

- **opID read at PresentVerdict-call time** (gate-1 F-20) —
  not at enqueue. So a mid-pass `/op-id` swap takes effect on
  the next modal.
- **inFlight dedup** by attestation-ref (gate-1 F-12) — the
  same dispatcher republish is presented at most once per drain.
- **DrainPending snapshot-then-iterate** with 8-round cap
  (gate-1 F-5). On any error, the unprocessed tail re-queues
  (gate-2 CONC-C-3/C-4).
- **`/exit` cancels sessionCtx** (gate-1 F-14) so a blocked
  modal read aborts cleanly; the items re-queue on the next
  session start via Recovery's republish.
- **Tree writer is the primary** (gate-1 F-1); flat JSONL is
  a forward-only Observer. The tree is also the authoritative
  load surface (`LoadFromTree`).
- **Path traversal guarded** (gate-2 SEC-C-1): safeSegment
  hash-substitutes `.` and `..` before they reach
  `filepath.Join`.

## Where the diagrams live in code

| Flow | Primary file(s) |
|---|---|
| Attestation (CLI) | `cmd/ghyll/session.go handleAttestCommand`, `runner/attestationstore.go`, `runner/attestation_jsonl.go`, `runner/attestation_tree.go`, `engine/attestations.go`, `engine/journal.go` |
| Adversarial cycle | `runner/orchestrator.go`, `runner/producer_fix.go`, `runner/adversarial.go` |
| Amendment commit | `runner/amendment.go`, `runner/amendment_commit.go`, `engine/journal.go` (handleAmendment) |
| Verdict modal (Tier 2) | `cmd/ghyll/modal_driver.go`, `cmd/ghyll/modal/modal.go`, `cmd/ghyll/modal/linereader.go`, `runner/dispatcher.go` (OpEventAttestationRequested publish), `runner/attestation_tree.go` (PrimaryWriter) |
