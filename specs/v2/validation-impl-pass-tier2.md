# Tier 2 adversarial review (gate 1, pre-implementation)

Reviewer: cold-context adversary, 2026-05-20.
Documents reviewed:

- `specs/architecture/components/operator-attestation.md` (analyst, 455 lines).
- `docs/decisions/016-tier-2-operator-modal-and-tree-primary.md` (architect ADR).
- `specs/architecture/tier-2-operator-attestation-contracts.md` (Go contracts).

Cross-references consulted:

- `specs/features/attestation.feature` (12 deferred scenarios claimed to lift).
- `docs/decisions/015-pass-persistence-and-jsonl-source-of-truth.md`
  (Part C, the inversion Tier 2 amends).
- `runner/attestation_tree.go` (Tier 1 tree writer, currently
  Observer-only; keys files on `rec.ID`, not `pass_id`).
- `runner/attestation_jsonl.go` (current `PrimaryWriter`; lines 87,
  267-298, plus `TruncateTrailingPartial` lines 202-265).
- `runner/attestationstore.go` (`Record` critical section
  lines 192-214; `LoadFromJSONL` lines 385-455).
- `runner/dispatcher.go:236-250` (the only publisher of
  `OpEventAttestationRequested`).
- `runner/operatorbus.go` (synchronous fan-out under bus RLock,
  lines 122-136).
- `runner/insufficient_basis_tracker.go:56-81` (`rounds == max`
  fires once and ONLY ONCE — but state survives the round, see F-7).
- `runner/grid.go:32-83` (`ArrowDefinition` has SourceRole +
  TargetRole, NO adversary chain).
- `cmd/ghyll/session.go:1108-1178` (slash-command dispatch),
  1112-1238 (`/op-id`), 1244-1327 (`/attest`).
- `cmd/ghyll/session_engine.go:345-422` (`attachJournal`),
  389-407 (writer wiring).
- `engine/store.go:100-199` (`schemaVersion`, `ensureSchemaVersion`,
  `ensureRecoverySourceColumn`).
- `runner/attestation_verifier.go:96-171` (`checkLine`; unaware of
  Tier 2 columns).
- `cmd/ghyll/engine_cmd.go:57-73` (`cmdEngineVerifyAttestations`
  reads the flat file).
- `bootstrap/init.go:14-49` (`GridDefaults`; no
  `ResidueNoteMaxBytes` field exists yet).
- `runner/pass.go:140-194` (`closeWith`; lock release ordering).

Total findings: **22**. Severity breakdown: **6 critical, 9 high,
7 medium**.

Most load-bearing finding: **F-1 — Tier 2 contracts retain the
Tier 1 invariant "JSONL fsync first" while specifying a tree
PrimaryWriter that writes only the per-pass tree file; the
aggregate JSONL is reduced to an Observer that can silently
diverge on disk-full. The recovery code path
(`engine/recovery.go:evaluationRunReconcile`) and the existing
`ghyll engine verify-attestations` CLI both consult the FLAT
file. After Tier 2 ships, a clean verify can pass while the
authoritative (tree) state contains lines the flat file never
saw. This is an inversion of trust between Tier 1's
`ErrAttestationAuditLost` semantics and Tier 2's "tree is
primary, flat is observer-only" stance.**

---

## Critical findings

### F-1: Recovery + verifier still read the flat aggregate after the primary-writer swap; the audit trust inverts

**Affects**: ADR-016 Part B (lines 74-115); contracts doc
`attachJournal` swap (Part E, Enforcement Map row 2);
`docs/decisions/015-pass-persistence-and-jsonl-source-of-truth.md`
Part D (`evaluationRunReconcile`); `cmd/ghyll/engine_cmd.go:57-73`.

**Claim**: "Tree JSONL is primary. Verdicts append to the per-pass
tree file FIRST, with fsync, before the in-memory
AttestationStore mutates. The flat JSONL is a fanout secondary"
(spec invariant 2). ADR-016 Part B's "Before/After" snippet
flips `SetPrimaryWriter` from `r.jsonlWriter.PrimaryWriter()` to
`r.treeWriter.PrimaryWriter(grid)`, demoting the flat file to
`Observe(r.jsonlWriter.Observer())`.

**Counterclaim**: Three concrete code paths still treat the flat
file as authoritative:

1. **`engine.Recovery.evaluationRunReconcile`** (ADR-015 Part D,
   `engine/recovery.go`) feeds off `LoadFromJSONL(jsonlPath, …)`
   to reconstitute attestation rows for "JSONL has verdict but
   engine doesn't" reconciliation. If the operator's verdict
   landed on the tree but the flat-file Observer failed (disk
   full mid-fsync, EIO, writer closed), the next-session
   Recovery scan sees no row in the flat file, never inserts the
   missing engine row, and the clause's evaluation_run stays at
   `end_status='running'`. The verdict exists in the tree;
   nobody reads it on restart.
2. **`cmd/ghyll/engine_cmd.cmdEngineVerifyAttestations`**
   (engine_cmd.go:73) calls
   `runner.AttestationVerifier.VerifyFile(jsonlPath)` against
   the flat aggregate. An audit that reports a clean flat file
   while the tree has 47 verdicts the flat lost is a silent
   correctness failure of a CLI ghyll publishes as the audit
   surface.
3. **`runner/arrow_cmd.go:102`** also computes
   `filepath.Join(filepath.Dir(dbPath), "attestations.jsonl")`
   as its source.

The Observer (`AttestationJSONLWriter.Observer()`) has no error
channel; `recordFailure` only increments a counter. Per the
existing comment at `attestation_jsonl.go:151-154` the Observer
"surfaces failure via the bus AND the WriteErrors counter so
the operator path sees it — but the Observer returns normally
because the AttestationObserver contract has no error channel".
That was acceptable when the flat file was a secondary audit;
it is NOT acceptable when Recovery and the verify CLI continue
treating it as authoritative.

**Reproduction**: Boot a session with Tier 2 wiring. Cause the
flat Observer's fsync to EIO mid-verdict (LD_PRELOAD an
fsync(2) wrapper that returns -1, or use a tmpfs with quota
set tight). Submit a verdict via the modal. The tree write
succeeds, the in-memory map mutates, the engine table row
lands via the journal observer. The flat Observer's
`recordFailure` ticks a counter, publishes
`OpEventAttestationAuditDurabilityFailed`, returns. Now
restart. `engine.Recovery.evaluationRunReconcile` reads the
flat file, finds N-1 verdicts (the lost one missing).
`evaluation_runs.end_status` for the missed clause stays at
`'running'`. `ghyll engine verify-attestations` returns
PASS — verifying only against the file that's missing the
verdict. The verdict exists only in the tree; no recovery
path reads the tree.

**Remediation**: One of the following:

- (a) Recovery must consult the tree, not the flat, after Tier 2:
  walk `attestations/v<N>/**/*.jsonl` and union into the in-memory
  store. Update `LoadFromJSONL` to a `LoadFromTree(root)` helper or
  add it alongside. Make `engineRuntime.openEngineWithOptions`
  call the tree loader.
- (b) The flat Observer must be ELEVATED to a second-primary
  with a wrap-error guarantee: if the flat write fails after the
  tree succeeds, the Record call must still fail-close. That
  requires extending `AttestationObserver` to return an error
  OR wiring the flat writer's PrimaryWriter as a secondary inline
  call (chained primaryWriter list rather than a single).
- (c) Forbid the swap and adopt Alt 1 from the ADR (keep flat as
  primary, add a tree-rebuild CLI). ADR-016 rejects Alt 1
  because "scenarios explicitly assert the per-pass tree is
  the primary write surface" — but the scenarios' text actually
  reads "a record is appended to `attestations/v<N>/...`" which
  is satisfied by Alt 1's hybrid (flat-primary + tree-fanout)
  as long as the tree append succeeds. Reconsider Alt 1.

Pick (a). It preserves the ADR-016 spec wording and fixes the
recovery + verify paths in one move.

### F-2: `EncodeAttestationPath` needs the Grid, but `AttestationStore.Record` doesn't have it

**Affects**: contracts doc lines 91-113 (`PrimaryWriter(grid *Grid)`,
`EncodeAttestationPath(root, rec, grid *Grid)`);
`runner/attestationstore.go:192-214` (Record critical section);
`runner/attestation_tree.go:71-80` (current writer constructor).

**Claim**: "Path encoding moves to a new
`EncodeAttestationPath(rec AttestationRecord) (string, error)`
helper. The helper: Reads `arrow_id` to look up source/target
roles + stratum + context via `Grid.Lookup(arrow_id)`."

**Counterclaim**: `AttestationStore.Record` (the only legitimate
caller of `primaryWriter`) lives in package `runner` and has no
Grid reference. The primaryWriter is a `func(AttestationRecord)
error` closure (line 168 of `attestationstore.go`). The
architect's contract `func (w *AttestationTreeWriter)
PrimaryWriter(grid *Grid) func(AttestationRecord) error` proposes
to close over the Grid pointer at session-start wiring time. But
the Grid is mutable (`Grid.Append` per `runner/grid.go:176`):
its `version` counter changes during a session, and new arrows
land. The closed-over Grid pointer is fine for new lookups
(it's a pointer, not a snapshot) — but ALSO means a Record for
an arrow that doesn't exist yet in the Grid (race between
on-the-spot arrow creation and verdict capture) sees a
`Lookup(arrowID) → (zero, false)` and falls into the contract's
"defensive fallback" branch ("If not found → use rec.SourceRole
/ rec.TargetRole as fallback"). The fallback reads
`rec.SourceRole` / `rec.TargetRole` which are already on the
record — but `rec.Context` and `rec.Stratum` are NOT on the
AttestationRecord schema (see `runner/attestationstore.go:46-59`).
So the fallback path can't construct the path; it must hit the
byte-cap-hash escape. Every verdict that races on-the-spot arrow
creation goes to the hash fallback.

Worse: on-the-spot verdicts already legitimately produce
attestation records (kind=`AttestationKindOnTheSpot`) WHERE the
arrow is BEING DEFINED by the operator's verdict. The Grid
typically lacks the arrow at the moment Record fires.

**Reproduction**: 
1. Open a session, declare op-id.
2. Trigger on-the-spot arrow creation (§12.2 flow).
3. The dispatcher's Record call fires with a fresh AttestationRecord
   whose ArrowID has just been minted; Grid hasn't ingested it.
4. `EncodeAttestationPath` calls `grid.Lookup(rec.ArrowID)` →
   `(zero, false)`.
5. Fallback uses `rec.SourceRole/TargetRole`, but context/stratum
   are NOT on the record. Path encoding either panics on the
   missing field or yields `v<N>//stratum-/role/...` (empty
   segments).
6. The byte-cap-hash fallback then masks the empty-segment
   case to `h-<digest>` for each empty component, producing
   four `h-<digest>` segments. The audit value is destroyed:
   no operator can find this file by context-name lookup.

**Remediation**: Add `Context`, `Stratum`, `PassID` to
`AttestationRecord` ahead of any Grid lookup — they belong on the
record because they describe the SITE OF THE VERDICT, not the
arrow that's still being negotiated. The contracts doc adds
PassID but not Context/Stratum; add both. Then the path
encoder doesn't need a Grid argument at all (a pure function of
`rec`). Strike `grid *Grid` from the signature.

### F-3: The "3-role chain" encoding has no source signal — `ArrowDefinition` has 2 role fields only

**Affects**: ADR-016 Part F (line 263 `"{source}__{adversary}__{target}"`);
spec invariant 7 (line 134); contracts doc adversary surface #8
(lines 421-427); `runner/grid.go:34-42` (current `ArrowDefinition`).

**Claim**: "If arrow def has 3-role chain (adversary-augmented):
`{source}__{adversary}__{target}`" (ADR-016 Part F step 2a) and
"three-role chain `analyst__adversary__architect` when an
adversary phase participates" (spec line 39).

**Counterclaim**: `ArrowDefinition` has exactly two role fields:
`SourceRole`, `TargetRole` (`runner/grid.go:36-37`). The
adversary phase (runner/orchestrator.go, see
`OpEventAdversarialRoundStart`) adds findings against the arrow;
it does NOT extend the ArrowDefinition with an adversary role
field. There is no field on the arrow def, the pass, the
clause, or the attestation record that says "this verdict was
attested during an adversary-phase run".

The architect's own contracts doc surface #8 admits this:
"The role-pair is heuristic: if the arrow's adversarial phase
ran, the chain is 3-role. Operator-tooling needs to consult
the arrow's pass history to know. Trace how
EncodeAttestationPath gets this signal."

It doesn't get the signal. The encoder is called inside
`AttestationStore.Record`'s critical section; no access to the
orchestrator state, no access to pass history. Step 2a never
fires under any current code path; the 3-role branch is dead.

Yet the spec scenario "Three-role chain path encoding"
(`attestation.feature:225-230`) is in the lift-list of 12
scenarios. It cannot lift without a source signal.

**Reproduction**: Read `runner/grid.go` for the ArrowDefinition
shape. Search the codebase for any field that names an adversary
role on a per-arrow or per-pass basis:

```
grep -r "AdversaryRole\|adversary_role\|adversaryRole" runner/ cmd/ specs/
```

Result: zero matches in code. The spec text exists; the data
flow doesn't.

**Remediation**: Either:

- (a) Add `AdversaryRole string` to `AttestationRecord` (where
  the verdict was attested under an adversary-phase pass; empty
  otherwise). Population path: the orchestrator's
  `runner/adversarial.go` annotates the record at construction
  time. EncodeAttestationPath reads this field.
- (b) Defer the 3-role scenario to Tier 3 alongside the producer
  hint hook. Strike "Three-role chain path encoding" from the
  Tier 2 lift list; revise the 12-of-15 claim down to 11-of-15.
- (c) Add `Roles []string` to AttestationRecord and let the
  caller stamp the full chain. The orchestrator owns this.

Pick (a) for minimum surface change. Also: validate at Record
time that `AdversaryRole` (if non-empty) doesn't equal
SourceRole or TargetRole and isn't named `__` — the §12.2
self-cert path already does this for the 2-role pair; extend
it.

### F-4: Modal driver subscription has no defined order vs Recovery's republish of pending attestation requests

**Affects**: contracts doc lines 235-241 (`newModalDriver`
subscribes the bus); ADR-016 Part E "REPL turn-loop integration";
spec invariant 6 (lines 122-128); `engine/recovery.go`
(Recovery emits `recovery-attestation-republished` events into
`RecoveryReport.Events`, NOT the bus per F-18 of ADR-015's
remediation).

**Claim**: "Session-ends-mid-attestation preserves the request.
If the chat process exits while a modal is open, the request
stays in `evaluation_runs.depth_type_attestation_ref` (Tier 1's
persistent signal). On next session start, the chat REPL
re-presents the modal at the first turn."

**Counterclaim**: Tier 1's Recovery does NOT publish to the bus
(per ADR-015 F-18: "the bus has zero subscribers at recovery
time. session.Open is responsible for surfacing these on the
chat-loop's first iteration"). Recovery's republish lives in
`RecoveryReport.Events` slice, surfaced via the
`OpEventRecoveryAttestationRepublished` event kind.

The Tier 2 modal driver subscribes the bus. It will NEVER see
the recovery republish event because Recovery doesn't go to the
bus. The contracts-doc Enforcement Map row 6 acknowledges this
weakly: "modalDriver's `DrainPending` consults the engine table
at session start to re-queue pending requests." Where? The
contracts doc spec lists no method on modalDriver that reads
the engine table; OnEvent only handles bus events.

Additionally: even if you add a `LoadPendingFromEngine` method,
the ordering is: `engine.Recovery` runs BEFORE `attachJournal`
(per session_engine.go), and `modalDriver.subscribe` runs AFTER
attachJournal. The Recovery report is built first, then the bus
subscriber registers. The modal driver must consume the
RecoveryReport.Events list explicitly, not via bus.

**Reproduction**:
1. Read `engine/recovery.go` (or its caller in
   `cmd/ghyll/session_engine.go:290-320`). Find the Recovery
   call. Observe `r.recoveryReport = report` is stored;
   `RecoveryReport()` accessor exists for session.go to drain.
2. Read the contracts doc Enforcement Map row 6: "modalDriver's
   DrainPending consults the engine table at session start".
3. There is no specified path through which modalDriver consumes
   `RecoveryReport.Events`. The contract specifies it must
   subscribe `OperatorBus`, which never sees recovery events.

**Remediation**: Specify the cross-wire explicitly in the
contracts doc:

```go
// session.go after attachJournal + before s.modalDriver.OnEvent
// can fire on bus traffic:
for _, ev := range s.engine.RecoveryReport().Events {
    if ev.Kind == runner.OpEventRecoveryAttestationRepublished {
        s.modalDriver.EnqueueFromRecovery(ev)
    }
}
```

Add `EnqueueFromRecovery(ev OperatorEvent) error` to the
`modalDriver` contract. Specify it constructs a Hint from the
event's Detail (which today carries `att-ref=<ref>`; the modal
driver needs ArrowID/ClauseID/Concept — Recovery should
populate these from the JOIN row, see ADR-015 Part E lines
303-311 — confirm those fields ARE in `OperatorEvent`).
`OperatorEvent` does carry ArrowID + ClauseID + PassID, so the
data exists; the Detail-only handling in the contracts doc is
underspecified.

### F-5: Modal driver's `DrainPending` re-entry under recursive attestation requests is undefined

**Affects**: Contracts doc lines 247 (DrainPending blocks until
all queued requests answered), 322 (enforcement); adversary
surface #9 of the architect's tail list (lines 429-434);
`runner/dispatcher.go:222-251` (where the publish lives);
`runner/operatorbus.go:122-136` (synchronous publish-under-lock).

**Claim**: "DrainPending blocks until all queued modal-
presentation requests are answered. Each Present* call records
via `AttestationStore.Record`; the tree writer's PrimaryWriter
persists; the chat loop continues."

**Counterclaim**: `DrainPending` is called pre-model. The
operator presents a verdict; `AttestationStore.Record(rec)`
fires; the tree writer writes; the in-memory map mutates;
observers fan out. The dispatcher's per-arrow loop on the next
clause may set `AwaitingAttestation=true` AGAIN (the verdict
just submitted doesn't change `clause.DepthTypeAttestationRef`
for OTHER clauses on the same arrow), which republishes
`OpEventAttestationRequested`. Now the bus, in publish-under-lock
mode, calls `modalDriver.OnEvent(ev)` synchronously. OnEvent's
contract is to enqueue. But the enqueue happens from inside
`PresentVerdict`'s caller goroutine (because the dispatch
loop ran inside DrainPending's iteration → PassDispatcher fired
in a previous turn that hasn't actually completed yet because
the dispatch path went pass-pending-attestation).

Actually re-read: the dispatch loop runs INSIDE
`PassDispatcher.Dispatch` which holds the per-(role,context)
lock token. The modal is shown on the NEXT REPL turn AFTER the
Dispatch returns with an attestation-pending result. So
DrainPending fires on turn N+1 for verdicts originating from
turn N's dispatch. So the recursion isn't immediate.

BUT: the contract reads "DrainPending blocks until every queued
request has been presented + answered". And there can be
multiple queued requests (multiple clauses on one arrow). The
operator answers verdict 1; Record fires; the bus publishes
`OpEventAttestationRecorded`; the bus also fires
`OpEventAttestationRequested` for the NEXT clause via a fresh
dispatch (if the operator's verdict was the last blocker, the
dispatcher re-runs traversal). At that moment we're INSIDE
DrainPending iterating its pending list. The bus subscriber
(OnEvent) appends to the same pending list under
`d.mu`. The iteration may or may not see the appended item;
contracts doc doesn't specify whether the iterator is
snapshot-copy or live-mutate. With a slice index loop and
mid-iteration append: depends on Go slice semantics. Defined
behavior NOT in contract.

**Reproduction**: 
1. Arrow A has clauses C1, C2 each blocked on attestation.
2. The dispatch publishes `OpEventAttestationRequested` for
   both; OnEvent enqueues 2 modal requests.
3. Operator opens the modal for C1, submits verdict pass.
4. The dispatcher (now or on next turn) re-runs the arrow's
   clauses; C1 now passes; C2's traversal publishes
   `OpEventAttestationRequested` AGAIN (the dispatcher's
   per-clause check fires per traversal).
5. DrainPending is mid-iteration of its pending list; the
   re-publish fires OnEvent → append. Does the iterator
   see the new entry?

**Remediation**: Specify DrainPending semantics:

- (a) Snapshot the pending list under `d.mu`, drop the lock,
  iterate the snapshot. New publishes go to a fresh list
  that DrainPending drains on a future call.
- (b) Loop: snapshot, drain, then re-snapshot until empty.
  Caps the per-turn cost; bounds risk of infinite republish
  (a malformed dispatcher that publishes on every traversal
  could loop forever).
- (c) Disallow recursive enqueue from OnEvent while
  DrainPending is iterating: queue grows but a `draining`
  flag deferred them. New events on a "deferred" list drained
  next turn.

Pick (b) with a hard cap (e.g. 8 drain rounds per turn) and
emit a typed error on cap-overflow so a dispatcher bug
surfaces.

### F-6: `EncodeAttestationPath` with empty PassID (pre-Tier-2 rows OR `/attest` CLI escape hatch) collapses to anonymous `h-<digest>.jsonl`

**Affects**: ADR-016 Part F step 5 ("Pass-id segment:
sanitized(rec.PassID) + .jsonl"); Part A "Pre-Tier-2 rows have
`pass_id=''`"; adversary surface #5 from the architect's tail
(lines 398-406); the `/attest` CLI handler at
`cmd/ghyll/session.go:1302-1316` which constructs
AttestationRecord WITHOUT PassID.

**Claim**: "Pass-id segment: sanitized(rec.PassID) + .jsonl"
(ADR-016 Part F step 5). The byte-cap fallback says: "if any
segment > 255 bytes → replace with `h-` + sha256[:16] hex". The
architect's tail acknowledges the empty-PassID degraded
filename ("the file is `h-<digest>.jsonl`. OK but ugly").

**Counterclaim**: "Ugly" understates the impact:

1. **`/attest` CLI verdicts have empty PassID.** Lines
   1302-1316 of session.go construct the AttestationRecord
   directly from a parsed `<ref>` argument with no
   PassID-extraction step (the ref shape `att-<arrow>-<clause>-v<N>`
   doesn't encode pass-id). The ADR's Part F says PassID maps
   into the filename; the `/attest` CLI verdicts every land
   in `h-<digest>.jsonl` — and ALL `/attest` CLI verdicts
   land in the SAME file because sanitize(`""`) = `""` →
   byte-cap fallback applies to empty string, which produces
   ONE hash (sha256 of `""`). Every `/attest` verdict
   ever recorded by this code path collides into one file.

2. Furthermore, sanitize(`""`) is `""` (per
   `attestation_tree.go:266-284`: `if s == "" return ""`).
   The byte-cap check `> 255 bytes` evaluates 0 > 255 = false,
   so the byte-cap fallback does NOT fire. The component stays
   empty. The constructed path is
   `attestations/v<N>/<ctx>/stratum-<S>/<role-pair>/.jsonl` — a
   file named `.jsonl` (dotfile, no name). Multiple
   /attest writes append to the same dotfile inside the
   role-pair directory.

3. The contracts doc's MaxLen check says "any segment > 255
   bytes". Zero-byte segments are also pathological (path
   collisions, hidden dotfiles). Not addressed.

**Reproduction**:
1. Run a Tier 2 session.
2. Type `/attest att-arrow1-clauseA-v1 pass ok`.
3. Inspect: `.ghyll/attestations/v1/<ctx>/stratum-<S>/<role-pair>/.jsonl`
   — a dotfile, hidden from `ls`.
4. Type `/attest att-arrow2-clauseB-v1 pass ok`.
5. Inspect: same `.jsonl` file with two lines for two
   different (arrow, clause) tuples.

The audit value of the per-pass tree is destroyed for the
`/attest` escape hatch.

**Remediation**:

- (a) Forbid empty PassID at write time. `EncodeAttestationPath`
  must return an error if `rec.PassID == ""` (and the byte-cap
  hash fallback shouldn't apply to empty). The /attest CLI
  must populate PassID — either by looking it up from
  `evaluation_runs.pass_id WHERE depth_type_attestation_ref = ref`
  (the Tier 1 reconcile path already does this) OR by minting
  a synthetic "slash-cli" pass-id token.
- (b) Add validation: `ValidateUnitPayload` extended (or new
  `ValidateRecord`) rejects empty PassID for any non-replay
  Record call.
- (c) Migration: pre-Tier-2 rows STAY in the engine table only,
  never round-trip through the tree writer. Adjust replay so
  the tree writer is wired AFTER the LoadFromJSONL replay.
  Actually the current ordering already does this; ensure
  `Record` is the only callsite that writes the tree, and
  `recordReplay` (line 224 of attestationstore.go) does NOT
  invoke the primaryWriter (verified — it doesn't). Good.

Critical because it breaks the audit goal of Tier 2 for the
specifically retained escape hatch (`/attest`).

---

## High findings

### F-7: `InsufficientBasisTracker.Record` fires `OpEventInsufficientBasisRoundsExceeded` exactly once when `rounds == max`; the modal driver's "escalation prompt is FINAL" invariant breaks on insufficient-basis ROUND 4+

**Affects**: spec invariant 5 ("Escalation prompt is final"; lines
115-121); `runner/insufficient_basis_tracker.go:67-79`; contracts
doc Enforcement Map row 5.

**Claim**: "After 3 consecutive `insufficient-basis` verdicts
(per `InsufficientBasisTracker`), the next modal presented is
the escalation prompt — NOT another insufficient-basis verdict.
The operator MUST choose option 1 or 2."

**Counterclaim**: The tracker fires
`OpEventInsufficientBasisRoundsExceeded` **only when
`rounds == max`** (line 69: `if t.max > 0 && rounds == t.max`).
On round 4 (`rounds == 4, max == 3`), the condition is false;
no event fires. But the spec's intent is "after round 3, every
subsequent modal MUST be the escalation prompt" — i.e. the
tracker has crossed and stays-crossed. Yet the tracker
maintains no "crossed" sticky flag; it tracks an integer count.
If the operator skips the escalation (FM-6 says "Dispatch lock
stays held; clause stays pending; next REPL start re-presents
the escalation prompt"), the next session's first traversal
publishes a fresh `OpEventAttestationRequested`. The modal
driver receives this event and presents a vanilla verdict
modal, not the escalation prompt — because the tracker's
"already crossed" state was never re-emitted by the bus, and
the modal driver's switch (verdict-modal vs escalation-modal)
keys off the LATEST bus event observed for that clause.

**Reproduction**:
1. Clause C5 receives insufficient-basis 3 times.
2. Tracker fires `OpEventInsufficientBasisRoundsExceeded` on
   round 3.
3. Modal driver enqueues an escalation request.
4. Operator types `skip` (or process killed).
5. Session restarts. Recovery's republish fires
   `OpEventRecoveryAttestationRepublished`. The modal
   driver sees a vanilla verdict-modal trigger.
6. Operator submits insufficient-basis a 4th time. Tracker's
   `rounds == 4`, `max == 3`, equality false → no event.
   Counter just keeps incrementing forever.

Or simpler: even within one session, if the operator answers
"insufficient-basis" twice in a row after the escalation
already fired (the tracker resets on pass/fail but NOT on
escalation-resolved), nothing fires again.

**Remediation**: 
- (a) Tracker fires on `>=` not `==`. Every subsequent IB
  verdict on an already-crossed clause re-publishes the
  escalation event. The modal driver dedups based on the
  clause-id (no double-presentation).
- (b) Tracker maintains a `crossedClauses` set; subsequent
  Record calls for a crossed clause re-emit the event until
  the clause is "resolved" (modal driver calls
  `tracker.Reset(clauseID)` after an escalation choice lands).
- (c) Add modal-driver-side state: "this clause's NEXT modal
  MUST be escalation, persisted via a runtime flag on the
  evaluation_runs row." Crash-safe.

Pick (b). Cleanest, single-source.

### F-8: `OperatorBus.Publish` fans out synchronously under bus mutex; modal driver's OnEvent enqueue + queue-drain runs in the publisher's goroutine

**Affects**: `runner/operatorbus.go:122-136`; contracts doc lines
240-243 (`OnEvent`); architect adversary surface #2 ("backpressure")
and surface #6 (subscription timing).

**Claim**: Bus is synchronous publish-under-lock with a
snapshotted subscriber list; OnEvent is fast (enqueue +
return).

**Counterclaim**: Two compounding issues:

1. **Publish-under-RLock**: The bus uses RLock then copies
   subscribers (line 126-129) and fans out OUTSIDE the lock
   (line 132). OK on its own — but it means the
   publisher's goroutine runs the subscriber's OnEvent call.
   If OnEvent's enqueue path takes the modal driver's `d.mu`
   AND DrainPending currently holds `d.mu` while iterating
   pending, the publisher deadlocks waiting for OnEvent.
   DrainPending is on the chat-loop goroutine. The publisher
   is on the dispatcher's goroutine (`PassDispatcher.Dispatch`).
   Two goroutines, two locks, opposite acquisition order.
   AB/BA deadlock possible.

2. **`d.pending` unbounded growth**: contracts doc line 224
   declares `pending []modalRequest` with no cap. If the
   dispatcher emits N attestation-request events between two
   REPL turns (e.g. background traversal across many arrows
   in plan mode), `d.pending` grows to N entries. Memory
   bound only by GC. The architect's tail surface #2 asks for
   backpressure design but the contracts doc doesn't specify.

**Reproduction (deadlock)**:
1. Goroutine A: chat-loop, inside DrainPending, holds `d.mu`,
   iterating pending slice.
2. Inside iteration, calls `prompt.PresentVerdict` which can
   block on stdin (operator typing).
3. Goroutine B: dispatcher, calls `bus.Publish` for a new
   `OpEventAttestationRequested`. Publish RLocks bus,
   snapshots subscribers, RUnlocks. Fans out: calls
   `modalDriver.OnEvent`. OnEvent tries to take `d.mu`.
   Blocked: A holds it.
4. A is waiting for stdin (operator). B is blocked on `d.mu`.
   If A's PresentVerdict somehow needs B's result (e.g. a
   later sub-dispatch in the same modal-handler), deadlock.

Even without circular dependency, B blocks all bus
subscribers for any subsequent Publish on the same goroutine,
which can include critical-path writes (
`OpEventClauseFailVerdict` from the verdict-record path).

**Remediation**:
- (a) `modalDriver.OnEvent` MUST NOT take `d.mu` for more
  than the duration of a slice append. DrainPending releases
  `d.mu` before calling `prompt.PresentVerdict` (snapshot the
  pending list under `d.mu`, drop, iterate copy). See F-5
  remediation.
- (b) Bound `d.pending` (e.g. 64 entries); drop newer events
  past the cap and emit a typed
  `OpEventModalBackpressure` so the operator sees the loss.
  Surface the count.

### F-9: `ValidateUnitPayload`'s `MaxResidueBytes` is read at session start; mid-session grid edits aren't observed (but the architect's recommendation in surface #10 is also half-spec)

**Affects**: contracts doc lines 270-272 ("plumbs the cap
through to `AttestationStore.SetResidueNoteMaxBytes(n)` at
open"); architect surface #10 ("Recommend: cap is read once at
session start; mid-session changes apply on next restart").

**Claim**: The cap is read at session start. Mid-session edits
of `residue-note-max-bytes` in the grid file don't affect the
running session — they apply on restart.

**Counterclaim**: The recommendation is consistent but
implementation-incomplete in several dimensions:

1. The `bootstrap.GridDefaults.ResidueNoteMaxBytes` field is
   NEW (does not exist in `bootstrap/init.go` today — see
   lines 14-19; only SeverityThreshold + DepthLadder +
   InsufficientBasisRoundsMax + RemediationRoundsMax). The
   contract doc states it's a new field but does NOT specify
   that `bootstrap/grid.go`'s `GridFile` struct ALSO gains
   the field with a `yaml:"residue-note-max-bytes"` tag, the
   validate() method rejects negative values, AND a v2
   migration plan handles existing on-disk grid files that
   lack the key (default 16KB).
2. Backward compat: existing grid files at version 1 and 2
   (see `bootstrap/grid_test.go:155, 249`) HAVE NO
   `residue-note-max-bytes` key. YAML decode into the new
   field: zero value (`0`). `validate()` per the contract
   "rejects negative values" — does `0` count as negative?
   The architect doesn't say. If `0` rejects, every existing
   grid file fails to load. If `0` accepts, the runtime
   uses `0` as the cap → every residue-note REJECTS as
   "too long" because 0 bytes is the cap.
3. The CLI subprocess case: `ghyll engine recover --dry-run`
   opens the store and runs Recovery in a separate process
   from the running session. Recovery's
   `evaluationRunReconcile` doesn't touch residue notes
   directly, but `engine verify-attestations` does call
   `AttestationVerifier.checkLine`. The verifier (per F-21
   below) is unaware of the cap entirely — but if it were
   extended, two processes with two grid-file reads would
   disagree.

**Reproduction**:
1. Existing project at grid_version 2 (no residue-note-max-
   bytes key). Run Tier 2 session.
2. `bootstrap.GridDefaults` zero-value: `ResidueNoteMaxBytes
   = 0`.
3. `validate()` accepts (zero not negative).
4. Session sets `AttestationStore.SetResidueNoteMaxBytes(0)`.
5. Operator submits a residue note: `len(residue) > 0`,
   exceeds the cap → `ErrVerdictResidueTooLong`.
6. Every residue note rejects. The feature ships broken on
   day-1 for every existing project.

**Remediation**:
- (a) `validate()` rejects `<= 0`, not just `< 0`. The
  contract must say so.
- (b) GridFile decode for `residue-note-max-bytes` must
  default to `DefaultMaxResidueNoteBytes` when the YAML
  field is zero/missing. Use a custom unmarshaler or a
  post-decode normalize step (existing pattern: see
  `bootstrap/grid.go:108`).
- (c) Document the default-fill in the migration ADR.

### F-10: Schema v3 → v4 ALTER TABLE partial-failure leaves the store half-migrated

**Affects**: ADR-016 Part A; contracts doc lines 13-20
(`ensureUnitColumns`); architect attack surface #5 of the
adversary task list.

**Claim**: "`schemaVersion` bumps from 3 to 4. `ensureSchemaVersion`
runs the four ALTERs idempotently (PRAGMA-based column-existence
check, matching Tier 1's `ensureRecoverySourceColumn` pattern)."

**Counterclaim**: `ensureRecoverySourceColumn` (engine/store.go:168-199)
runs ONE ALTER and is naturally atomic (sqlite ALTER TABLE adds
a single column in one statement). The Tier 2 migration runs
FOUR ALTERs. They are NOT wrapped in a transaction (ADR-015
Part D requires Recovery to be transaction-bounded; the schema
migration is not). If `ALTER TABLE attestations ADD COLUMN
pass_id` succeeds but `ALTER TABLE attestations ADD COLUMN
unit` fails (disk full, permission, ENOSPC, schema_change
trigger), the engine_meta `schema_version` STILL UPDATES
because the four ALTERs run BEFORE the meta-row UPSERT (see
the existing flow at engine/store.go:122-162). Wait — re-read.
Lines 146-152: `if err := s.ensureRecoverySourceColumn(); err != nil
{ return fmt.Errorf("v3 migration: %w", err) }` — return BEFORE
the UPSERT. So a failed migration leaves engine_meta unchanged
AND leaves the columns partially-added. Next session restarts;
re-runs the migration. PRAGMA reports `pass_id` exists (from
first attempt); skip. Tries `unit` again — succeeds. Continues
unit_payload_json, hint_json. Now all four exist. Meta-row
updates to v4. Fine, eventually.

BUT: between the failure and the next restart, the in-process
store has 3 columns added (pass_id) and 0 not (unit,
unit_payload_json, hint_json). The Tier 2 code SHOULD NOT be
allowed to read/write those columns because the meta-row
still says v3. The current code structure (Open → migrate →
attach observers) does NOT gate "code reads new columns" on
the meta-version. The verifySchemaVersion check happens at
read-only opens; it doesn't gate per-column access.

**Reproduction**:
1. Inject ALTER failure on the second ALTER (mock the db).
2. First Open fails; Store returns an error.
3. Caller (session.Open) handles the error — but a partial
   migration is on disk.
4. Restart with disk space restored. Open succeeds; PRAGMA
   reports pass_id exists; ensureUnitColumns skips it;
   adds unit, unit_payload_json, hint_json; UPSERTs meta to
   v4. Migration ends consistent. OK.

So the actual risk is recoverable. BUT: if the failed ALTER
is `pass_id` (NOT NULL DEFAULT '' on a non-empty table) — the
sqlite NOT-NULL semantics with existing rows: ALTER TABLE ADD
COLUMN with NOT NULL DEFAULT '' on an existing table works
(default applied to existing rows). What if disk fills
between ALTERs of different sizes? Mid-ALTER, sqlite may
rollback that one statement but the OTHER three already
landed.

Net: migration is mostly self-healing but the contract should
specify (i) wrap the four ALTERs in a single sqlite
transaction (sqlite supports BEGIN; ALTER; ALTER; ALTER;
ALTER; COMMIT), or (ii) accept the half-state and document
the recovery path.

**Remediation**: Wrap in `sql.Tx`. ensureUnitColumns becomes:

```go
tx, err := s.db.Begin()
if err != nil { return err }
defer tx.Rollback()
for _, col := range neededCols {
    if columnExists(tx, "attestations", col.name) { continue }
    if _, err := tx.Exec(col.alter); err != nil { return err }
}
return tx.Commit()
```

Specify this in the contracts doc.

### F-11: Tree writer needs `TruncateTrailingPartial` parity but contract doesn't grant it; tree-file torn lines silently corrupt

**Affects**: `runner/attestation_jsonl.go:202-265`
(TruncateTrailingPartial); contracts doc lines 91 (PrimaryWriter
spec for tree writer). ADR-015 Part C F-6 spec for trailing
truncation.

**Claim**: Tree writer becomes PrimaryWriter. PrimaryWriter
behaviors are documented in `attestation_jsonl.go:267-298`. The
contract for the tree writer mirrors this.

**Counterclaim**: The flat writer has `TruncateTrailingPartial`
because the file is opened `O_RDWR` (line 87 of
attestation_jsonl.go: "Per ADR-015 Part C the writer must
support read-back of its own bytes"). The tree writer
(`attestation_tree.go:162`) opens with `O_WRONLY`:

```go
f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
```

If a crash happens mid-write on a tree file, the trailing
partial line stays. Next session: LoadFromJSONL on the FLAT
file truncates; nobody truncates the tree files. The tree's
per-pass JSONL becomes corrupt for any verifier that walks
the tree (the audit value Tier 2 explicitly promotes).

The contracts doc says nothing about tree-write recovery.
ADR-016 Part B doesn't propose a `LoadFromTree` or a
`TruncateTrailingPartialAll(root)` analogue.

**Reproduction**:
1. Operator submits a verdict.
2. Tree writer writes the line; fsync mid-flush; OS crashes.
3. On-disk tree file ends with a partial JSON object (no
   newline).
4. Next session: tree writer opens the file `O_WRONLY |
   O_APPEND`. Next verdict's write starts where the partial
   ended → corrupt: `{"...partial...{"new...full"}\n`.
5. `ghyll engine verify-attestations` after extension to
   walk the tree: detects unmarshal error on the corrupt
   line.

**Remediation**:
- (a) Tree writer opens `O_RDWR` like the flat writer.
- (b) On session.Open after replay, call
  `AttestationTreeWriter.TruncateTrailingPartialAll()` which
  walks `root/v*/.../<pass-id>.jsonl` and truncates each.
  Expensive on tree-heavy projects; gate behind a config.
- (c) Specify this in the architect's ADR-016 explicitly; add
  to the Implementation seam section.

### F-12: PassRegistry's per-event subscriber list (`Observe`) is one-shot; the contracts doc doesn't specify modal driver registration ordering vs ResumePass

**Affects**: `runner/projectstatus.go:121-126`
(`PassRegistry.Observe` says "One-shot semantics: register all
observers at session start before any emit happens"); contracts
doc lines 232-239 (`newModalDriver` constructor).

**Claim**: The modal driver subscribes the bus (line 215:
`s.engine.Bus().Subscribe(s.modalDriver.OnEvent)`). Done at
`Session.Run` post-attachJournal.

**Counterclaim**: PassRegistry has the one-shot Observe
contract: register before any emit. Recovery's
`PassRegistry.Resume` (per ADR-015 Part A line 120) STAMPS
`recoveredAt` and EMITS `PassEventRecover`. This emit fires
during Recovery (BEFORE attachJournal, BEFORE modal driver
subscribes). So a Pass that was preserved via attestation-
pending preservation emits an event NO ONE HEARS.

That's fine for the modal driver (it cares about
`OpEventAttestationRequested`, not Pass events). BUT: the
recovery scan also emits
`OpEventRecoveryAttestationRepublished` into
`RecoveryReport.Events` (NOT to the bus per F-18 in
ADR-015's remediation). So:

- The modal driver must EXPLICITLY consume RecoveryReport
  events for the republish (see F-4 above).
- It must NOT also subscribe `OpEventAttestationRequested`
  on the bus and double-fire when the dispatcher next
  publishes a same-clause request.

The contracts doc is silent on dedup. If Recovery enqueues a
modal request for clause C5, AND the next dispatch
publishes `OpEventAttestationRequested` for C5 (because the
clause is still pending and a fresh traversal re-publishes),
the modal driver presents TWO modals for the same clause.

**Reproduction**:
1. Session 1: Alice opens modal for C5; process killed.
2. evaluation_runs row has
   `depth_type_attestation_ref=att-...-C5-v3` and
   `end_status='running'`.
3. Session 2 starts. Recovery's
   `evaluationRunReconcile` republishes — adds an event to
   `RecoveryReport.Events`.
4. Modal driver consumes RecoveryReport (per F-4 fix),
   enqueues a verdict-modal request.
5. Session 2 first turn: dispatcher dispatches arrow A; its
   loop hits clause C5; `clause.DepthTypeAttestationRef`
   is non-empty; `Lookup → (zero, false)`; publishes
   `OpEventAttestationRequested`.
6. Modal driver's `OnEvent` enqueues a SECOND request.
7. DrainPending presents two modals; operator confused.

**Remediation**: Modal driver maintains a
`Set[attestation-ref]` of in-flight requests. OnEvent
dedups against this set (existing request for the ref →
drop). Set entry removed on Present completion or modal
dismissal.

### F-13: `/op-id` whitespace validator doesn't enforce path-component-safe op-id; spec scenarios `op-id with dangerous characters is rejected` already-passing claim is incomplete

**Affects**: `cmd/ghyll/session.go:1227-1236` (current `/op-id`
validation only rejects whitespace); spec lines 178-187
(`attestation.feature` op-id rejection rows: `../etc/passwd`,
`alice/bob`, `alice\x00null`, `alice\nbob`).

**Claim**: Tier 2 builds on Tier 1's `ValidateOpID` unit tests.
The non-deferred scenario "op-id with dangerous characters is
rejected" should already work via the slash command.

**Counterclaim**: `handleOpIDCommand` at session.go:1227
rejects whitespace only:

```go
if strings.ContainsAny(arg, " \t\n\r") {
    return … "✗ op-id must not contain whitespace"
}
```

Then sets `s.opID = arg`. `alice/bob` and `../etc/passwd`
are accepted. `alice\x00null` is accepted (null byte not in
the whitespace set). `alice\nbob` IS rejected (`\n` in the
set).

The feature claims op-id is recorded in record JSON only,
NEVER used as a filesystem component — but record JSON in the
new tree writer's path encoding (per ADR-016 Part F)
includes the op-id NOWHERE in the path. Good. BUT: the
record's OpID field is JSON-serialized to disk; an op-id of
`../etc/passwd` in the JSON field is benign but the spec
explicitly REQUIRES rejection at SESSION START
(`attestation.feature:182`: "the attestation flow refuses
session start with op-id-invalid-characters").

Tier 2's modal driver records verdicts using `s.opID`
(current op-id). Tier 2 doesn't introduce new op-id
validation — it INHERITS the broken Tier 1 validation. The
spec lift list says these rows are non-deferred — but the
implementation isn't actually rejecting them.

**Reproduction**:
1. Start a session.
2. Type `/op-id ../etc/passwd`. session.go:1233: `s.opID =
   "../etc/passwd"`. Output: `op-id set: ../etc/passwd`.
3. The feature explicitly requires rejection with
   `op-id-invalid-characters`. Does not happen.

**Remediation**: Add a Tier 2 hardening: `ValidateOpID(s)
error` that the `/op-id` handler calls. Already
implemented somewhere per spec's remark about "the
`ValidateOpID` unit tests already cover the runtime
checks"? Check:

```bash
grep -rn "ValidateOpID\b" /home/witlox/ghyll/ 
```

If absent, the spec's claim is false and Tier 2 inherits a
bug.

This is also a Tier 2 concern because the tree writer
includes op-id in the record JSON body but NOT in the path
encoding (per F-6 the path uses sanitize(rec.PassID), not
op-id). However: if Tier 2 ever decides to add op-id to the
path (e.g. a per-operator partition), the validation gap
becomes a path traversal exploit.

### F-14: `/exit` mid-modal has no defined cleanup path; the modal goroutine may leak

**Affects**: spec FM-1 ("Modal interrupted by Ctrl-C
mid-prompt: Single verdict. Dispatch lock token stays held;
the unfinished modal state is discarded. Next REPL start
re-presents the modal."); contracts doc lines 247-248
(`DrainPending(ctx)`); architect attack surface #19.

**Claim**: FM-1 covers Ctrl-C mid-prompt. The dispatch lock
stays held; the modal state is discarded; next session
re-presents.

**Counterclaim**: `/exit` is a DIFFERENT path from Ctrl-C.
Operator types `/exit` AFTER opening a modal. The chat-loop
goroutine is currently INSIDE `DrainPending(ctx)`, blocked
on `prompt.PresentVerdict(ctx, hint)` which is reading
stdin. The operator types `/exit`. PresentVerdict's stdin
read returns `"/exit"`. Is `/exit` a verdict? The
contracts doc doesn't say. If PresentVerdict treats `/exit`
as a malformed verdict, it returns an error or
`ErrModalSkipped`; DrainPending propagates; the chat-loop
returns. session.Close runs. But:

- The dispatcher's per-pass lock token is still held (Tier
  2 invariant 1 says lock stays held during modal).
- The modal's `pending` slice still has unprocessed
  entries (this was clause 1 of 3).
- The chat-loop goroutine returned; session.Close runs;
  engine.closeEngine() runs.
- closeEngine calls journal.Close(), which DRAINS pending
  events with a 5s budget. If the modal driver's
  unprocessed entries cause Pass.Close to NOT fire (the
  dispatcher is still inside Dispatch, waiting for the
  pass to close, which can't happen because the modal
  never finished) — the dispatcher goroutine leaks.

Worse: the dispatcher's goroutine, if it's the same as the
chat-loop goroutine, isn't running; if it's a different
goroutine (background traversal), it's deadlocked on
something the dying chat loop owned.

**Reproduction**:
1. Operator opens session. Dispatcher publishes
   `OpEventAttestationRequested`.
2. Modal driver enqueues + DrainPending presents.
3. Operator types `/exit` at modal prompt.
4. The contracts doc doesn't say what happens. If
   PresentVerdict's stdin reader interprets `/exit` as a
   verdict line, parse fails, returns
   `ErrVerdictUnitInvalid`. DrainPending logs and returns.
5. session.Close runs. engine.closeEngine. Lock tables
   leaked.

**Remediation**:
- (a) `PresentVerdict` MUST recognize the chat-loop's
  control commands (`/exit`, `/op-id`, etc.) and either
  defer the modal OR exit cleanly.
- (b) DrainPending takes a `ctx context.Context`; on
  ctx-cancel (which session.Close would issue via a
  shutdown channel), the function returns a typed error
  and the modal state is checkpointed (modal-still-pending
  preserved in the engine's evaluation_runs row, which
  it already is — confirm).
- (c) The dispatcher's pass is Abort'd on session close
  with reason=`session-closed-mid-modal`; pass lock is
  released; recovery rebuilds on next start.

Specify this in the contracts doc and in spec FM-1.

### F-15: `OperatorBus.Subscribe` has no Unsubscribe; the modal driver subscription survives close-and-reopen tests

**Affects**: `runner/operatorbus.go:108-116` ("there is no
Unsubscribe"); contracts doc lines 214-215 (Subscribe at
session start).

**Claim**: The session subscribes one bus subscriber per
consumer at startup; "no Unsubscribe" is the contract.

**Counterclaim**: For interactive sessions one bus per
session is fine. For tests: BDD scenarios use
`StubModal.subscribe()` to install a scripted-modal
subscriber on the bus. If a test runs 100 scenarios
sequentially in one binary, 100 subscribers accumulate.
Each Publish fans to all 100. The Publish's "fan out
outside the lock" still does N-times-the-work per
emit. Memory + latency leak.

Also: re-open scenarios. If `session.Reopen` is ever a
thing (it isn't today, but recovery + resume is on the
plan), the bus subscriber count grows across opens.

Worse: each subscriber holds a closure reference to the
modal driver. Garbage collector can't free the dead
session's modal driver while the bus retains the OnEvent
closure. Per-test leak adds up.

**Reproduction**:
1. BDD harness opens session 100 times in one process.
2. Each session subscribes modal driver to bus.
3. The bus is per-session (Tier 1: `runner.NewOperatorBus()`
   created in `NewEngineRuntime`). Re-check.

Actually: `NewEngineRuntime` creates `runner.NewOperatorBus()`
per runtime (session_engine.go:157-167). So the bus IS
per-session. The leak isn't cross-session.

DOWNGRADE this to a soft warning: the architect's adversary
surface #2 should be re-asserted as a non-issue for Tier 2.

**Remediation**: None required; document explicitly that
the bus is per-EngineRuntime and dies with it.

### F-16: Spec invariant 2 says "Verdicts append to the per-pass tree file FIRST, with fsync, before the in-memory AttestationStore mutates" but the existing `AttestationStore.Record` mutates the in-memory map AFTER the primary writer returns; tree-only-primary doesn't break this, but the invariant text understates it

**Affects**: spec invariant 2 (lines 96-103); existing code at
`runner/attestationstore.go:205-213`.

**Claim**: "Verdicts append to the per-pass tree file FIRST,
with fsync, before the in-memory AttestationStore mutates."

**Counterclaim**: Already true in Tier 1 — the primaryWriter
returns BEFORE byID mutation (line 205-210). Tier 2's swap
doesn't change this. The spec wording is correct but
re-asserts what Tier 1 already enforced.

This is informational; downgraded from a finding to a note.
Move to Notes section.

### F-17: `EncodeAttestationPath` byte-cap fallback discards 240+ bytes of context AND emits ErrPathComponentTooLong "via the bus, not as an error return"; this hides the failure from `Record`'s caller

**Affects**: ADR-016 Part F step 6; contracts doc lines 95-99
(`ErrPathComponentTooLong is surfaced via the bus but the
write succeeds with the truncated path`); spec FM-5.

**Claim**: On overflow, "the fallback hashes the role-pair to
a 16-byte hex digest prefix. Operator gets a typed event for
audit."

**Counterclaim**: Tier 2's PrimaryWriter wraps the path
encoder. If the encoder's overflow path is "succeed silently
with hash filename", the verdict lands in
`h-<hash>.jsonl` — the file exists but the operator can't
find it via context-name lookup. The bus event
`ErrPathComponentTooLong` fires; the operator's UI may or
may not surface it depending on subscribers.

Worse: if multiple distinct role-pairs hash to the same
16-byte prefix (collision probability 2^-64 per pair, but
adversarial input can deliberately collide), two distinct
role-pairs write to the same file. Audit trail forensically
broken.

The spec's tone treats this as a degraded mode. It's
correct degraded behavior FOR FILESYSTEMS that hit the
255-byte cap. But the cap is per-component on ext4; on
NTFS it's 255 UTF-16 code units; on btrfs it's 255 bytes
SAME AS EXT4. The fallback is correct in shape. The
hidden-failure-via-bus is the issue.

**Remediation**:
- (a) `EncodeAttestationPath` returns BOTH the path AND
  the typed `ErrPathComponentTooLong`. PrimaryWriter
  publishes the event AND records a flag on the
  `AttestationRecord.Reason` field (e.g.
  `"path-truncated:role-pair"`).
- (b) Operator-tooling: `ghyll engine
  verify-attestations` reports the truncated paths.

### F-18: The `init` arrow's "target role" is ambiguous; ADR-016 Part F step 2b ("init arrow: init__{target_role}") relies on init having a target role, but init arrows are bootstrap-time and don't fit the diamond role contracts

**Affects**: ADR-016 Part F step 2b; spec lines 82 (init's
role-pair `init__analyst`); architect attack surface #18.

**Claim**: "init arrow: `init__{target_role}` (init has no
source role in the chain)."

**Counterclaim**: What IS the target role of an init arrow?
The init pass runs `bootstrap.BuildInitGrid` which composes
arrows from proposals; each PROPOSAL has its own source +
target. The init pass itself is the activity of constructing
the grid; it's not represented as an arrow IN the grid
(`bootstrap/init.go:85-187` BuildInitGrid composes the grid
content but doesn't add an "init" arrow). The
diamond-role contracts in `specs/architecture/roles/` are
analyst/architect/implementer/integrator. `init` is a
synthetic role-id mentioned in the dispatcher contract
("a synthetic role-id like `init` / `adversary`",
dispatcher.go:87-88).

So the "init arrow" referred to in ADR-016 Part F is a
HYPOTHETICAL construct. If the operator attests during the
init phase (depth-type attestation on a proposed clause),
the attestation record's ArrowID is whatever
ComputeAttestationID produced — but the arrow itself isn't
in the grid yet (it's a PROPOSAL).

ADR-016 Part F step 1: "Resolve arrow def via
g.Lookup(rec.ArrowID). If not found → use rec.SourceRole /
rec.TargetRole as fallback". Init arrows never resolve via
Grid.Lookup. The fallback path runs every time.

The fallback uses `rec.SourceRole / rec.TargetRole`. For
an init attestation, what's the source role? Bootstrap's
proposal flow (see `bootstrap/profile.go`,
`bootstrap/propose.go`) doesn't stamp source/target on a
record. The /attest CLI today stamps `SourceRole =
gridArrow.SourceRole` — but for init, gridArrow doesn't
exist yet.

**Reproduction**:
1. Operator runs `ghyll init`.
2. init proposes arrow A.
3. Operator attests proposal A with a depth-type
   attestation.
4. AttestationRecord has empty SourceRole/TargetRole?
   The path encoder's fallback uses empty strings →
   `init__unknown` (per `buildRolePair` returning
   `"unknown"` for empty target) — actually no, the
   architect's new `EncodeAttestationPath` ALGORITHM
   step 2b says "init arrow: `init__{target_role}`"
   which requires target_role.

If target_role is empty, the encoding produces
`init__` (empty after the separator). sanitize → empty
segment. Byte cap fallback doesn't fire (0 < 255).
Path becomes `init/stratum-/init__/<pass-id>.jsonl`
with empty stratum component AND a role-pair that
ends with `__`. Filesystem-portable but
operationally confusing.

**Remediation**: 
- (a) Spec the init flow does NOT produce attestation
  records via the new tree path. The /attest CLI
  handles init attestations and writes to the flat
  aggregate only.
- (b) Init attestations DO go to the tree, but the
  init arrow's role-pair is the literal string
  `"init"` (single segment, no `__`).
- (c) Spec what role string init attaches to OpID and
  AttestedByRole. Currently OpID = the operator's
  email; AttestedByRole = ?? (init handler in
  bootstrap doesn't expose this).

The ADR-016's init special-case at Part F is incomplete.

### F-19: `PrimaryWriter` interface (Tier 2 contracts doc) takes a `*Grid` argument; this couples the writer's lifecycle to the Grid's mutability — closing the Grid before the writer leads to nil panics

**Affects**: contracts doc line 91 (`PrimaryWriter(grid *Grid)`);
session_engine.go closeEngine ordering (lines 438-460).

**Claim**: PrimaryWriter takes a Grid and calls grid.Lookup
during write.

**Counterclaim**: Tier 2's closeEngine teardown order is
(per Tier 1): journal.Close → jsonlWriter.Close →
treeWriter.Close → store.Close. The Grid is not explicitly
closed but the runtime's grid pointer is set to nil
implicitly when the engineRuntime is GC'd.

But there's a window: if a verdict's Record fires DURING
session.Close (the chat goroutine is closing, but a
background dispatch goroutine is still publishing), the
tree writer's PrimaryWriter calls grid.Lookup on a Grid
that's about to be invalidated. If Grid is still alive,
fine. If Grid was already detached: nil pointer
dereference.

Mitigation: closeEngine should NOT close the tree writer
before the journal drains all pending writes. Current
order looks safe (journal first, then writers, then
store). But if the dispatcher's goroutine outlives the
journal (it shouldn't, but isn't guaranteed by the
contract), a stray Record can race.

**Remediation**: The tree writer's PrimaryWriter closure
should capture the Grid at construction; the closure
checks for nil Grid and returns
`ErrAttestationGridNotAvailable` rather than panicking.
Specify in contracts.

### F-20: Modal-handoff: `s.opID` mutation between `OnEvent` enqueue and `DrainPending` consumption is undefined

**Affects**: spec invariant 4 (lines 109-114); contracts doc
Enforcement Map row 4; architect attack surface #13.

**Claim**: "Multi-operator handoff is serial. The session's
`op_id` mutates ONLY via the `/op-id <new>` slash. A
verdict-record carries the op-id active at submission
time. Two records on the same pass with different op-ids
reflect handoff."

**Counterclaim**: The modal driver enqueues a request when
the bus event fires (op-id snapshot at enqueue time? not
specified). DrainPending presents at REPL turn boundary
(op-id snapshot at present time? maybe). The verdict
records use `s.opID` — but where in the modal driver does
`s.opID` reach? `modalDriver` has no field for it
(contracts doc lines 217-225).

The Enforcement Map row 4 says: "modalDriver reads the
current op-id from `s.opID` at each PresentVerdict". But
modalDriver is constructed with NO Session pointer in the
contracts doc constructor (`newModalDriver(prompt, store,
passes, bus, ib)`). How does it reach `s.opID`?

If the modal driver is given `func() string { return
s.opID }` as a getter, the value at PresentVerdict-call
time wins (post-handoff). If the prompt's
`VerdictSubmission` carries the op-id at construction
(snapshotted), pre-handoff wins. Not specified.

Scenario:
1. Alice opens modal for C5. OnEvent enqueues. s.opID = alice.
2. Alice types `/op-id bob`. s.opID = bob.
3. DrainPending runs (next turn). PresentVerdict presents
   to "the operator" (the same person at the terminal, who
   is Alice still typing).
4. The verdict gets recorded with op-id = ??

The spec invariant 4 says "the op-id active at submission
time". Submission is at the moment the operator hits Enter
on the verdict. After step 2, s.opID is bob. So Bob is
recorded as the submitter — but Alice typed the verdict.
Confusing but consistent. The contracts doc must
explicitly state this.

**Remediation**: 
- (a) Specify in the contract: "AttestationRecord.OpID is
  read from `s.opID` AT THE MOMENT
  `AttestationStore.Record` is called, not at modal
  enqueue or modal open".
- (b) Add a `ModalDriver.SetOpIDProvider(func() string)`
  method. The session injects a getter so the driver
  doesn't import session.
- (c) Add a BDD scenario covering exactly this case:
  Alice types verdict, `/op-id bob`, hits Enter →
  Record carries bob.

---

## Medium findings

### F-21: AttestationVerifier (`runner/attestation_verifier.go`) is unaware of Tier 2 unit/unit_payload_json/hint_json fields; `verify-attestations` returns false negatives

**Affects**: `runner/attestation_verifier.go:99-171`;
architect attack surface #20.

**Claim**: The verifier validates JSON wire format + 
self-cert + verdict-kind enums. Tier 2 adds unit +
unit_payload_json + hint_json — the verifier needs
extension.

**Counterclaim**: Already covered as a known gap in
adversary surface #20. The architect's contracts doc lists
"Required unit tests" but does NOT list a verifier
extension as part of Tier 2 implementation seam. The
seam section (lines 372-396) lists 7 code paths to
touch; none is `runner/attestation_verifier.go`.

**Remediation**: Add `runner/attestation_verifier.go` to
the implementation seam. Verifier rules to add:

- If `unit == "record-locations-inspected"`: validate
  `unit_payload_json` parses to `{"inspected": [...]}`
  with non-empty array.
- If `unit == "write-residue-note"`: validate
  `unit_payload_json` parses to `{"residue": "..."}`
  with non-empty, ≤16KB string.
- If `unit == "confirm"`: validate `unit_payload_json`
  is `{}` (or empty string for back-compat).
- If `unit == ""`: pre-Tier-2 row, skip unit validation.

### F-22: Spec scenario "Operator's session ends mid-attestation" requires "the round counter for the clause is NOT incremented (the attempt didn't complete)"; the InsufficientBasisTracker's `Record` increments on every Record call

**Affects**: `attestation.feature:253-258` (deferred
scenario in the 12-lift list); `runner/insufficient_basis_
tracker.go:67-79` (`t.counts[clauseID]++` on every
insufficient-basis verdict).

**Claim**: Scenario: "the round counter for the clause is
NOT incremented (the attempt didn't complete; no
insufficient-basis recorded)".

**Counterclaim**: Tier 2's modal handles `skip` — the
operator skips the verdict; NO Record fires; tracker
unchanged. OK.

But the scenario implies the modal CAN BE INTERRUPTED
MID-TYPING (e.g. Alice typing a residue note when
network drops). At what point does the modal commit?
The contracts doc says `PresentVerdict` blocks until
the operator submits OR returns ErrModalSkipped. If
ErrModalSkipped, no Record. If the process dies mid-
typing, the deferred state is fine. But:

- The modal driver's pending queue may have already
  enqueued the request (before the operator typed
  anything). On crash, the queue is in-memory only;
  next session's recovery rebuilds from
  evaluation_runs. So far so good.
- But spec invariant 1 says "the dispatcher's per-pass
  lock token stays held during the modal so a concurrent
  re-Dispatch cannot race". On crash, the lock dies with
  the process. Lock-table is in-memory only. Next
  session: no lock held; fine.

Re-read the scenario: this scenario is in the "Tier 3"
genuinely-blocked list per ADR-016 ("Operator's session
ends mid-attestation" — actually it IS in the lift-list,
line 414 of operator-attestation.md). The spec says
session-ends-mid-attestation IS lifted by Tier 2.

The spec mechanism: "the request stays in
`evaluation_runs.depth_type_attestation_ref` (Tier 1's
persistent signal). On next session start, the chat REPL
re-presents the modal at the first turn."

The contracts doc Enforcement Map row 6 says
`modalDriver's DrainPending consults the engine table at
session start to re-queue pending requests`.

The implementation question: which engine-table query?
The Tier 1 JOIN (`SELECT … WHERE end_status='running' AND
a.attestation_id IS NULL`) returns pending requests. This
becomes part of `RecoveryReport.Events` (see F-4 above).
Modal driver consumes the report; enqueues a fresh
verdict-modal. Operator presents verdict.

But what about a clause that was MID-WAY through a
residue-note-typing when the process died? The operator
typed half the note; nothing was persisted. Next session:
operator gets a fresh modal, types the note again.
Acceptable degradation.

**Remediation**: Specify the recovery surfaces
acceptably. This is more of a documentation-completeness
note than a finding. Downgrade to medium.

### F-23: Spec scenario "Operator accepts risk on the third round" requires "C5's CLAUSE-status transitions to pass IFF all findings on C5 are disposed (resolved or accepted-risk)" — the disposition check has no clear owner

**Affects**: spec F-4 (lines 257-265); `runner/findings.go`
(the disposition derivation).

**Claim**: After option 1, the FINDING transitions to
accepted-risk; the clause STATUS transitions to pass IFF
all findings on the clause are disposed.

**Counterclaim**: Who runs the "iff all findings on C5 are
disposed" check? The modal driver doesn't enumerate
findings — it has no FindingsStore reference. The
dispatcher's DeriveArrowStatus does include finding
disposition checks (severity threshold), but the clause-
status derivation (per state-machine.md) is `findings.go`'s
job.

The contracts doc doesn't specify this seam. The modal
driver's PresentEscalation returns an `EscalationChoice`;
the caller (DrainPending) must:

1. Record the AttestationRecord with verdict=accepted-risk.
2. Transition the FINDING associated with C5 to
   `accepted-risk` (FindingsStore.UpdateFindingStatus or
   similar).
3. Re-derive the clause status — which requires
   re-running the clause-status derivation, not the
   arrow-status derivation.

Step 2 has no mapped FindingID — which finding ON C5
gets transitioned? Multiple findings can be on one
clause. The escalation prompt selects "the operator
agrees this finding is accepted-risk" but the modal
doesn't track which specific finding.

**Reproduction**:
1. Clause C5 has 3 findings: F1 (open), F2 (open), F3
   (open).
2. Three rounds of insufficient-basis on C5.
3. Operator accepts risk.
4. Which finding gets transitioned? All three? Just
   F1? The contract is silent.

**Remediation**: The escalation prompt MUST surface
the finding IDs to the operator (multi-select or
list-based UX). OR: the escalation is per-finding, not
per-clause — the dispatcher's per-clause aggregation
of "findings outstanding" is the operand. Add
`FindingID string` to `modal.Hint` and
`AttestationRecord` (it's already there per Tier 1
schema). The hint synthesizer reads from the
FindingsStore by clause-id.

### F-24: `SetResidueNoteMaxBytes` is racy if grid reload happens mid-session

**Affects**: contracts doc lines 281-283 (the new accessor);
architect attack surface #10 (recommends "cap is read once
at session start").

**Claim**: The cap is set at session start; mid-session
edits don't apply.

**Counterclaim**: If the recommendation IS "set-once",
then `SetResidueNoteMaxBytes(n)` is a setter that's
called exactly once. But "exactly once" semantics aren't
enforceable; tests will call it multiple times. The
contracts doc lines 281-283 declare the accessor without
specifying it's `setOnce` or freely-mutable. A racy
mid-session edit (between `Record` validates and the
write) lets one verdict use the old cap and another use
the new.

**Remediation**:
- (a) Use atomic.Int64 for the cap. Reads / writes are
  lock-free, no torn read.
- (b) Document the lifecycle: SetResidueNoteMaxBytes
  called once at session.openEngine; subsequent calls
  noop (or panic in dev mode).

### F-25: Default `hint_json` of `'{}'` (or the architect's "JSON object with empty strings") differs from "empty string"; back-compat is unclear

**Affects**: ADR-016 Part A; contracts doc lines 33-43.

**Claim**: ADR Part A: "`hint_json TEXT NOT NULL DEFAULT ''`"
(empty string). Spec lines 51-53: 
"`{arrow_id, clause_id, concept, attestation_ref}`" (i.e. a
JSON object with empty strings). Two different defaults.

**Counterclaim**: Pre-Tier-2 rows have `hint_json = ''`
(per ADR Part A). Tier 2 read paths (modal driver hint
deserialization, future verifier extension) try to
`json.Unmarshal([]byte(""), &Hint{})` — this errors with
"unexpected end of JSON input". The default must be at
least `'{}'` (empty object) to parse.

**Remediation**: Change Part A's DEFAULT to `'{}'`. Update
the contracts doc accordingly.

### F-26: `EncodeAttestationPath` step 7 returns `filepath.Join(treeRoot, "v"+gv, ...)` — but `gv` is a `uint64` and the spec's literal text is "v<N>" with N as the grid_version integer; the formatting must be specified

**Affects**: ADR-016 Part F step 7;
`runner/attestation_tree.go:124`
(`fmt.Sprintf("v%d", e.Record.GridVersion)` — current
Tier 1 form).

**Claim**: "Return filepath.Join(treeRoot, "v"+gv, ctx,
stratum, rolePair, passFile)".

**Counterclaim**: `gv` typed as uint64 doesn't string-concat
in Go. Must be `fmt.Sprintf("v%d", gv)`. Trivially fixable in
implementation. But: leading zero? V0 the empty grid? V0 vs
V1 partitioning? The current tree writer uses `v%d`
unconditionally. ADR-016 Part F is sloppy with the type.

**Remediation**: Spec it as `fmt.Sprintf("v%d", gv)`. Done.

### F-27: Spec line 41 says the flat JSONL "continues as the aggregate tail" but ADR-015 Part C's `LoadFromJSONL` reads ONLY the flat file at session start; how does the aggregate stay consistent post-Tier-2?

**Affects**: spec line 41-42; ADR-015 Part C; ADR-016 Part B.

**Claim**: Tree is primary; flat continues as "tail of all
verdicts" / aggregate.

**Counterclaim**: At session N+1, `LoadFromJSONL(flatPath)`
replays the flat file. But Tier 2's flat is an Observer
(best-effort, no error channel). The session might come up
with a flat that's MISSING verdicts the tree has. Then
`recordReplay` populates byID from the flat. The byID is
incomplete. Next dispatcher traversal sees a clause whose
attestation_id has no Lookup hit → AwaitingAttestation = true
→ modal re-prompts → operator submits AGAIN (double-record).

This compounds F-1. The fix is the same: replay from tree,
not flat.

**Remediation**: Same as F-1. Replay from tree after Tier 2.

---

## Notes

- **F-16** is informational only; the spec's wording is
  consistent with Tier 1 enforcement.
- **F-15** is informational; bus lifecycle is per-session and
  doesn't leak across sessions.
- **Subscriber order on bus**: `OperatorBus.Subscribe`
  appends; fanout is in subscribe order. The modal driver
  must subscribe AFTER the JSONL writer + tree writer
  (so the modal driver's enqueue happens AFTER the audit
  observers see the event). This is implicit in
  session.go's wiring; specify it explicitly.
- **`/attest` escape hatch decision**: keeping it (per ADR-016
  open item 4) is fine, but the escape hatch should publish
  `OpEventAttestationRecorded` (which it does today via the
  store) AND populate PassID — see F-6 remediation.
- **Required additional BDD scenarios** (beyond the
  contracts doc's listed):
  - "Verdict via /attest carries the right pass_id when
    one is currently dispatched"
  - "Modal driver dedups when Recovery republish and bus
    publish race"
  - "Three insufficient-basis rounds + skip escalation +
    fourth insufficient-basis re-presents escalation prompt"
  - "Tree-file trailing partial line truncated on next
    session start"
- **Verifier extension** (F-21) must ship in Tier 2; otherwise
  the `ghyll engine verify-attestations` operator-tooling
  presents a misleading clean bill of health for any v4 row.

---

## Disposition

All findings above must be remediated in-phase per the
project's standing direction (no deferrals, no severity
filtering). The critical findings F-1, F-2, F-3, F-4, F-5,
F-6 are mutually-reinforcing — fixing F-1 (recovery reads
tree, not flat) is required to make F-27 go away; fixing
F-2 (add Context/Stratum to AttestationRecord) is required
to make F-3 (add AdversaryRole) consistent; fixing F-4
(modal driver consumes RecoveryReport) is required to
make F-12 (dedup) tractable.

Recommended remediation order:

1. Schema extension: add PassID, Context, Stratum,
   AdversaryRole, Unit, UnitPayloadJSON, HintJSON to
   AttestationRecord. Update ADR-016 Part A migration to
   include all six new columns. (F-2, F-3, F-6.)
2. Default `hint_json` to `'{}'`. (F-25.)
3. Migration: wrap ALTERs in BEGIN/COMMIT. (F-10.)
4. EncodeAttestationPath: pure function of rec (no Grid
   argument). Specify init special-case + zero-byte
   segment rejection. (F-2, F-18, F-26.)
5. Tree writer: open `O_RDWR`; add
   TruncateTrailingPartialAll. (F-11.)
6. Recovery: union from tree, not flat;
   `LoadFromTree(root)` helper. (F-1, F-27.)
7. Modal driver: consume RecoveryReport on first turn;
   dedup by attestation-ref. (F-4, F-12.)
8. DrainPending: snapshot-then-iterate; bounded
   re-drain loop. (F-5, F-8.)
9. IB tracker: fire on `>=`, sticky-crossed via
   `crossedClauses` set. (F-7.)
10. ValidateOpID at /op-id command. (F-13.)
11. Modal at /exit: PresentVerdict aware of session
    cancellation; clean tear-down. (F-14.)
12. SetResidueNoteMaxBytes: atomic.Int64 +
    document set-once. (F-9, F-24.)
13. Verifier extension. (F-21.)
14. Escalation choice carries FindingID. (F-23.)
15. ModalDriver.opIDProvider injection. (F-20.)
16. Path encoder returns ErrPathComponentTooLong
    inline. (F-17.)
17. Tree writer's PrimaryWriter closure guards
    against nil Grid (if Grid stays as an
    argument). (F-19.)

That ordering keeps each phase's changes self-contained
and each subsequent phase building on a settled foundation.
