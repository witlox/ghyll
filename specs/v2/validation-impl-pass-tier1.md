# Tier 1 adversarial review (gate 1, pre-implementation)

Reviewer: cold-context adversary, 2026-05-20.
Documents reviewed: `specs/architecture/components/pass-persistence.md`,
`docs/decisions/015-pass-persistence-and-jsonl-source-of-truth.md`,
`specs/architecture/tier-1-pass-persistence-contracts.md`.
Cross-references: `runner/pass.go`, `runner/projectstatus.go`,
`runner/attestationstore.go`, `runner/attestation_jsonl.go`,
`runner/attestation_verifier.go`, `runner/dispatcher.go`,
`runner/operatorbus.go`, `engine/store.go`, `engine/records.go`,
`engine/journal.go`, `engine/replay.go`, `engine/attestations.go`,
`cmd/ghyll/session_engine.go`, `cmd/ghyll/session.go`,
`cmd/ghyll/engine_cmd.go`, `cmd/ghyll/arrow_cmd.go`,
`cmd/ghyll/lockfile.go`, `specs/features/state-machine.feature:120-233`,
`specs/features/runner.feature:167-181`.

Total findings: 18. Severity: 6 critical / 8 high / 4 medium.

Headline finding (most load-bearing): **F-1 — F-2 third scenario
("Restart preserves attestation-pending open passes") cannot pass
because OpEventAttestationRequested is declared but never published
anywhere in the codebase, so there is no signal for recovery to
detect "attestation-pending" and no fact for it to re-publish.**

---

## Critical findings (must remediate before implementer starts)

### F-1: "attestation-pending" has no detection signal in current code

**Affects**: `pass-persistence.md:55,82,123,165-175`,
`ADR-015 Part D:155-158`, `tier-1-pass-persistence-contracts.md:218,256-260,355`.

**Claim** (`pass-persistence.md:55`): "Attestation-pending pass — An
orphan pass whose `arrow_id` has at least one clause with a
published-but-unanswered hint on the operator bus (an attestation
request without a corresponding record in the attestation table)."

**Claim** (F-2 third scenario, `pass-persistence.md:165-175`):
"Given the engine store has 1 open pass P1 / And P1's arrow has
clause C5 with an attestation request that has no corresponding
record in the attestation table / When engine.Replay runs / Then
P1's state remains open / And the attestation request is
re-published on the OperatorBus".

**Counterclaim**: `OpEventAttestationRequested` is declared at
`runner/operatorbus.go:42` but **grep across the entire codebase
finds zero publishers**:

```
$ grep -rn "OpEventAttestationRequested" /home/witlox/ghyll
/home/witlox/ghyll/runner/operatorbus.go:42:  OpEventAttestationRequested OperatorEventKind = "attestation-requested"
```

Furthermore the OperatorBus is volatile by construction
(`runner/operatorbus.go:14-16` "The bus retains no history;
subscribers that join late see only events published after they
subscribed"). So even if some path *did* publish the request,
nothing persists it across a restart. The two facts together mean:

1. **There is no recoverable "attestation pending" state in the
   engine.** The closest signal is the dispatcher's transient
   `input.AwaitingAttestation = true` at
   `runner/dispatcher.go:226,233`, which lives on the goroutine
   stack of a Dispatch call and dies with the process.
2. The `recoveryRun.attestationPendingScan` documented at
   `tier-1-pass-persistence-contracts.md:256-260` ("subset of
   orphans whose clauses have a published-but-unanswered
   attestation request") has nothing to scan against. The only
   surface that could feed it is `evaluation_runs` rows where
   `end_status = "running"` AND `depth_type_attestation_ref` is
   set AND no row exists in `attestations` for that ref. **The
   spec does not name this query.** Without naming it, every
   implementer will invent a different one.

ADR-015 Part E acknowledges this gap ("attestation-request
persistence … deferred to a follow-up ADR") and the analyst's A-3
flagged it explicitly. But the BDD scenario at
`state-machine.feature:204-211` is one of the eight `@deferred`
the work claims to lift. **It will not lift** if recovery falls
through to `aborted:crash` whenever an attestation was pending.

**Reproduction**:

1. Open a session, start an arrow whose first clause has
   `DepthTypeAttestationRef` set, run Dispatch.
2. The clause evaluation succeeds, the dispatcher reaches
   `runner/dispatcher.go:221-234`, finds no Lookup hit, sets
   `input.AwaitingAttestation = true`.
3. Kill -9 the process.
4. Restart. The engine has: a `passes` row with `state=open`, an
   `evaluation_runs` row with `end_status=running` for that
   clause, no `attestations` row. The `attestation-requested`
   event was never published (because nothing publishes it).
5. Recovery runs `attestationPendingScan`. Per the spec's
   definition it looks for "published-but-unanswered attestation
   request" → no published request exists → scan returns empty.
6. `orphanAbort` fires → P1 transitions to `aborted:crash`.
7. The BDD scenario expects state=open. It fails.

**Remediation**:

Two options. Either is acceptable; pick one and bake it into the
spec before implementer touches code.

**Option A** (minimum viable): Define attestation-pending via the
JOIN of `evaluation_runs` × `attestations`. A clause is
attestation-pending iff there is an evaluation_runs row with
`pass_id = P` AND `depth_type_attestation_ref != ''` AND
`end_status = 'running'` AND no `attestations` row with
`attestation_id = depth_type_attestation_ref`. ADR-015 Part D's
`attestationPendingScan` then has an executable contract.
Republishing the hint requires inferring the request payload from
the evaluation_runs row (`arrow_id`, `clause_id`,
`depth_type_attestation_ref`) — feasible.

**Option B** (correct, larger surface): Add the
`attestation_requests` table from `pass-persistence.md` open
question #1 to Tier 1. Without it, FM-7's degraded path is the
*normal* path because nothing currently fires
`OpEventAttestationRequested`. Bumping it into Tier 1 makes the
deferred scenario actually pass.

The current spec sits between the two: it cites the bus as the
detection surface but the bus is empty. Either embrace Option A
explicitly (and tighten F-2's scenario language to say "engine row
state and not bus state") or take Option B and persist requests
now.

Confidence: 95%.

---

### F-2: `engine.Recovery` writes to runner-layer stores without observers attached — Findings/Classifications mutations are silently dropped from the engine.db

**Affects**: `ADR-015 Part D:155-158`,
`tier-1-pass-persistence-contracts.md:236-248,317-325`.

**Claim** (`tier-1-pass-persistence-contracts.md:317-325`):
"Between steps 4 and 5, the journal is NOT attached. Recovery's
writes go through the runner stores' usual Mutator paths; without
observers, they don't re-journal. After step 5, AttachX for every
store starts the steady-state observer fanout."

**Counterclaim**: Recovery does more than mark passes aborted. F-4
requires it to flip `evaluation_runs.end_status` from `running` to
the JSONL verdict (`pass-persistence.md:194-208`, ADR-015 Part D
step 4). F-5 requires rolling back torn rows (might mutate
passes). The spec also says findings under a preserved pass keep
their `grid_version` tag (invariant 5) — implying a possible
write to FindingsStore.

The "go through runner stores without observers" pattern works
**only** if every write Recovery performs writes BOTH the engine
row AND the in-memory store, and only if the engine writes are
done by direct `Store.UpsertX` calls (not via the runner observer
fanout). The spec's recommended path is the opposite — write
through the runner store, rely on no-observer to not re-journal.
But:

1. `evaluation_runs` has no runner-layer store. `EvaluationRun` is
   produced by `Runner.Evaluate` once-per-clause-per-pass
   (`runner/runner.go:580`) and the engine persists via
   `Journal.AttachRunner → handleRun`. There is no in-memory
   "EvaluationRunsStore". Recovery's `evaluationRunReconcile`
   therefore must write **directly** to `Store.UpdateRun` (which
   doesn't exist yet — `InsertEvaluationRun` only does
   `ON CONFLICT(id) DO NOTHING`, see `engine/records.go:464`).
2. `engine/records.go:464` `INSERT INTO evaluation_runs ... ON
   CONFLICT(id) DO NOTHING` means recovery cannot reuse
   `InsertEvaluationRun` to flip the `end_status`. A new
   `UpdateEvaluationRunStatus` is required and the contracts
   don't list it.
3. If Recovery writes via in-memory store and the engine row only
   updates because the journal *would have* fired (but doesn't),
   the engine row stays stale. Restart-then-restart sees the
   stale row and re-runs Recovery — fine for idempotence on the
   first call, but every subsequent call repeats the same write
   work because the engine never caught up.

**Reproduction**:

1. Pre-crash: `evaluation_runs(id=R1, clause_id=C5, pass_id=P1,
   end_status="running", depth_type_attestation_ref="att-X-C5-v1")`.
2. JSONL has `att-X-C5-v1` with verdict=pass.
3. Restart. Recovery does step 4 (evaluationRunReconcile). Per
   the spec it goes "through the runner stores' usual Mutator
   paths" — but there is no Mutator for evaluation_runs. So
   either:
   - Recovery silently does nothing (the engine row keeps
     `end_status=running`, JSONL says pass, split-brain persists).
   - Recovery calls `store.db.Exec("UPDATE evaluation_runs SET ...")`
     directly. This works but bypasses the
     "no-observer-during-recovery" pattern, so observer-attached
     code paths (`AttachRunner` post-step-5) won't see the
     state change. The next clause evaluation that touches R1
     gets a Runner that thinks the run is still `running`.

**Remediation**:

1. Add `Store.UpdateEvaluationRunEndStatus(ctx, runID,
   endStatus, source string) error` to the Tier 1 contract.
   `source` is the recovery marker so a future audit can
   distinguish runner-set from recovery-set values.
2. Document explicitly in ADR-015 that Recovery's writes to
   `evaluation_runs`, `passes`, `attestations` go **directly to
   the Store** (engine layer), not through runner-layer
   mutators. The in-memory `AttestationStore` is repopulated by
   `loadFromJSONL` at step 3 of session_engine.Open; the
   evaluation_runs in-memory representation doesn't exist
   because Runner.Evaluate is one-shot.
3. After Recovery writes, the contract should require Recovery
   to *also* refresh whatever in-memory caches it touched (e.g.,
   pass row that was preserved must be marked in the
   PassRegistry as recovered with the `recovered_at` field). The
   contracts currently don't specify how the in-memory
   PassRegistry gets re-populated for preserved passes — see F-3.

Confidence: 90%.

---

### F-3: Preserved attestation-pending passes never get a `*runner.Pass` in the live PassRegistry — `/passes` doesn't list them, dispatcher can't reuse the lock

**Affects**: `pass-persistence.md:79-84,165-175,52-54`,
`tier-1-pass-persistence-contracts.md:316-325` (Open sequencing),
`runner/projectstatus.go:62-90`, `runner/pass.go:90-134`,
`cmd/ghyll/session.go:1336-1362`.

**Claim** (`pass-persistence.md:79-84`, invariant 4): "A pass with
state `open` AND at least one of its clauses has a published-but-
unanswered attestation request stays `open` after recovery."

**Counterclaim**: "Stays open" in the engine row is one thing.
But the live runtime artifacts of an open pass — a `*runner.Pass`
in `PassRegistry.passes` map (`runner/projectstatus.go:64`) and a
held `RoleContextLockToken` (`runner/pass.go:54`) — are gone after
restart. The session lockfile released; the `RoleContextLockTable`
is freshly constructed in `openEngineWithOptions`
(`session_engine.go:140`) and starts empty.

So the state after Recovery preserves a pass is:

- `passes` table row: `state=open`, `recovered_at` set.
- `runner.PassRegistry`: empty (or, if Recovery is supposed to
  re-populate it, the contract is silent on how — `OpenPass` is
  the only construction path and it acquires a fresh lock and
  publishes `OpEventPassOpened`).

Implications:

1. `/passes` slash command (`session.go:1345 passes := s.engine.Passes().All()`)
   returns the empty in-memory list, claims "no open passes" —
   even though the engine has a row marked open. Operator sees
   inconsistent state.
2. When the operator finally answers the attestation, the
   dispatcher's `Dispatch(req)` for the same (role, context)
   tuple opens a NEW pass with a NEW pass_id. The old preserved
   pass row is now permanently orphaned (state still
   `open`, no live process, no one reads it).
3. The lock table has no entry, so a re-Dispatch on the same
   (role, context) tuple succeeds — meaning the "preserved open
   pass" claim isn't enforced. Any process can race past the
   preserved pass.

**Reproduction**:

1. Pre-crash: pass P1, role=analyst, context=A1, lock held by
   PassRegistry. Engine has open row for P1.
2. Crash; restart. Recovery sees P1 attestation-pending,
   preserves the row.
3. Operator runs `/passes`. Output: "no open passes". (Per
   `session.go:1346` the in-memory registry is empty.)
4. Operator runs the chat-loop; analyst kicks off new analysis on
   context=A1. Dispatch succeeds (RoleContextLockTable is empty),
   creates pass P2 on the same (analyst, A1). Now the engine has
   two open passes for the same tuple — P1 (preserved) and P2
   (live).

**Remediation**:

The contracts must specify how preserved open passes re-enter
runtime presence. Options:

- **Option A (close on restart)**: Drop "preserved open" and
  switch invariant 4 to "passes with attestation-pending clauses
  get marked `aborted` with reason `attestation-pending-on-crash`
  PLUS a `pending_attestation_refs` JSON column listing the
  awaiting attestation ids. The operator's verdict, when it
  arrives, persists to JSONL/engine as usual but does NOT resume
  the pass — it just satisfies an audit-trail surface." This
  simplifies recovery, breaks the "Operator can deliver a
  verdict" promise in `state-machine.feature:208`.

- **Option B (reconstitute)**: Define a `PassRegistry.Resume(rec
  PassRecord, lockTable *RoleContextLockTable) (*Pass, error)`
  that re-acquires the (role, context) lock, reconstructs the
  in-memory `*Pass`, marks the slot as recovered. Recovery calls
  this for every preserved pass. Then `/passes` lists them, the
  dispatcher refuses to open a competing pass on the same tuple
  (ErrRoleContextBusy), and the operator's verdict path
  transitions C5 to pass and closes the pass cleanly.

Option B is the right one. The spec's claim "preserves
attestation-pending passes" implies Option B. But the **contracts
don't list `PassRegistry.Resume`** and the analyst spec's
"recovered_at timestamp is recorded for audit" suggests Option A
in disguise. Clarify before implementer starts.

Confidence: 95%.

---

### F-4: Observer fires under `Pass.mu` (per contract), but `Pass.mu` is held inside the registry write path AND `Bus.Publish` already fans out under no lock — adding `PassRegistry.emit` under the registry lock creates a different deadlock surface

**Affects**: `tier-1-pass-persistence-contracts.md:119-122,138-150,353`,
`runner/pass.go:150-169`, `runner/projectstatus.go:75-90`,
`runner/operatorbus.go:108-126`.

**Claim** (contracts:119-122): "PassObserver is invoked under the
registry's write lock on every mutation. Per the FindingsStore /
Grid / AmendmentQueue pattern: observers MUST be fast and
non-blocking (chan-send only). Long work hands off to a
goroutine."

**Claim** (contracts:138-144): "closeWith is updated to call
r.registry.emit(...) AFTER state mutates but BEFORE
lockToken.Release. pass-persistence.md invariant 1."

**Counterclaim**: Look at `runner/pass.go:150-169` carefully:

```go
func (p *Pass) closeWith(reason string, finalState PassState) {
    p.mu.Lock()
    defer p.mu.Unlock()
    ...
    p.state = finalState
    ...
    p.lockToken.Release()
    if p.bus != nil {
        p.bus.Publish(OperatorEvent{Kind: OpEventPassClosed, ...})
    }
}
```

The bus.Publish call is **already inside `p.mu.Lock()`**. The bus
fanout pattern (`operatorbus.go:117-126`) takes a snapshot of
subscribers, then fans out **outside** the bus mutex. But this
session has wired `r.attestations.Observe(... r.ibTracker.Record
...)` (session_engine.go:303-308) and the JSONL writer observer
and the tree writer observer to the AttestationStore — none of
those go through the bus. The bus fanout itself, however, runs
subscriber callbacks while holding nothing except the slice copy,
so a slow subscriber blocks neither the bus nor publishing — but
**it does keep p.mu held** for the duration of every subscriber
callback in the chain.

Now the spec says PassRegistry.emit runs "under the registry's
write lock" (contracts:119). PassRegistry.emit will fan out the
typed `PassEvent` to N observers (the journal one is the only
attached observer at session-end-state, but at session-start the
JSONL writer and tree writer are also subscribed via
`attachJournal`). Per `journal.go:135-159` the journal observer
issues `j.enqueue(...)` which has a 100ms backpressure budget
before dropping.

**Where is the deadlock?**

The spec mixes two lock holds:

1. `closeWith` already holds `p.mu`.
2. `PassRegistry.emit` MUST be called from `closeWith` (per the
   spec's "fires from Pass.closeWith boundary",
   `pass-persistence.md:264-266`). But emit is defined to fire
   "under the registry's write lock" (contracts:119) —
   `r.mu.Lock()` in `PassRegistry`.

Lock-ordering compositions:

- closeWith path: take `p.mu`, take `r.mu`, call observer.
- A separate `PassRegistry.All()` (called by
  `/status` slash command or status CLI any time) takes `r.mu`
  (read), then loops calling `p.State()` (which takes `p.mu`).

Path 1 takes p.mu → r.mu; Path 2 takes r.mu → p.mu. **Classic AB
/ BA deadlock**.

CaptureProjectStatus today already does `src.Passes.All()` then
`p.ID()`, `p.State()`, `p.ClosedAt()`, `p.CloseReason()` — every
one of which acquires `p.mu`. With the proposed change,
Close-and-Capture from two goroutines deadlock the first time
they race.

**Reproduction**:

1. Goroutine A: Dispatcher closes pass P1. closeWith holds p.mu,
   tries to call registry.emit → blocks on r.mu.
2. Goroutine B: Engine status CLI calls
   PassRegistry.All() → holds r.mu (RLock), iterates passes,
   calls P1.State() → blocks on p.mu.
3. Deadlock.

The current code (without the proposed change) is safe because
`closeWith` doesn't reach into the registry. The proposed change
introduces the cycle.

**Remediation**:

Two options:

- **A (Use existing emit-without-r.mu pattern from FindingsStore)**:
  FindingsStore.emit is called from inside `s.mu` (the FindingsStore
  mutex). It does NOT take a separate registry lock. The
  FindingsStore IS its own registry. Mirror that: don't have a
  separate `r.mu` taken from `closeWith`. The closeWith path can
  call `p.registry.emitDirect(event)` which iterates observers
  with no lock — observers are appended at Observe-time and the
  spec says "registration is one-shot at session start", so a
  no-lock fanout is safe in practice. (FindingsStore takes its lock
  because the slice itself can be appended to mid-run; if
  PassRegistry observers really are one-shot, document it and skip
  the lock.)

- **B (decouple)**: closeWith returns the event payload it would
  emit, releases p.mu and the lock token, then the caller (the
  registry-aware wrapper around `Pass`) does the fanout. This
  changes the public API of `Pass.Close` slightly but breaks the
  cycle cleanly.

The contract as written ("under registry's write lock") triggers
the deadlock. The architect needs to redesign or the implementer
will hit this on the first concurrent test run.

Confidence: 85%.

---

### F-5: ADR-015 Part C "JSONL is source of truth" creates a startup-time read failure mode that the current code path can never recover from

**Affects**: `ADR-015 Part C:119-122,168-170`,
`tier-1-pass-persistence-contracts.md:286-298,313`,
`runner/attestation_jsonl.go:74-94`.

**Claim** (Part C:119-122): "JSONL gone / unreadable: hard error.
`ghyll` refuses to start the session with
`ErrAttestationAuditLost`. Operator must restore the file (or run
`ghyll attestations rebuild --force` which is a separate operator
escape hatch with explicit consent)."

**Counterclaim**: This is wrong for new projects. Today's flow
(`session_engine.go:156-163`):

```go
jsonlPath := filepath.Join(filepath.Dir(dbPath), "attestations.jsonl")
jw, jerr := runner.NewAttestationJSONLWriter(jsonlPath)
if jerr == nil {
    rt.jsonlWriter = jw.WithBus(rt.bus)
} else if logger != nil {
    logger.Warn(...)
}
```

The JSONL writer opens with `os.O_CREATE|os.O_WRONLY|os.O_APPEND`
(`attestation_jsonl.go:84`) so a *missing* file is created
silently — that's the expected first-run behavior. ADR-015 says
"refuses to start with ErrAttestationAuditLost" if the file is
gone. But every fresh `ghyll run .` starts with no
`attestations.jsonl`.

What does "gone / unreadable" mean? The ADR conflates three cases:

- **Case 1 (genuine first run)**: no `.ghyll/` directory at all.
  Refusing to start is wrong; it's a brand-new project.
- **Case 2 (engine.db exists, JSONL deleted)**: the engine has
  attestation rows; the JSONL file is missing. The ADR's logic
  applies — but only if the engine has rows. If the engine is
  also empty, this is a re-init, not a loss.
- **Case 3 (JSONL exists but cannot be opened/read)**: filesystem
  permission error, disk full on read. ADR-015 says refuse.
  Reasonable.

**Reproduction**:

1. `mkdir empty-project && cd empty-project && ghyll run .`
2. Per ADR-015 Part C, attestations.jsonl is "gone".
3. `loadFromJSONL` per `tier-1-pass-persistence-contracts.md:297`
   is called before `Replay` (per session_engine.Open step 3).
   The file doesn't exist; the function returns
   `ErrAttestationAuditLost`.
4. Session.Open fails with that error. The operator cannot start.

This breaks every fresh `ghyll run .` invocation — half the
intended workflows.

**Remediation**:

Specify in ADR-015 Part C explicitly:

```
JSONL missing AND engine.db missing OR engine attestations count == 0:
  treat as fresh start. Create empty JSONL on first AttestationStore.Record.

JSONL missing AND engine.db has attestation rows:
  ErrAttestationAuditLost. Operator must restore or use rebuild --force.

JSONL exists but unreadable / corrupt header:
  ErrAttestationAuditLost.

JSONL exists but trailing line is truncated:
  see F-6.
```

The contract surface for `loadFromJSONL` must say: "Missing file
with empty engine attestations count is not an error; treat as
empty stream."

Confidence: 95%.

---

### F-6: JSONL torn-line handling is unspecified and the architect punts to "deterministic failure mode"

**Affects**: `tier-1-pass-persistence-contracts.md:408-410`,
`ADR-015 Part D:144-146`, `runner/attestation_jsonl.go:32-41,238-250`.

**Claim** (`attestation_jsonl.go:32-41`): "Each line is written
in a single Write call (json + newline pre-joined), so a process
crash mid-syscall cannot leave a partial line without leaving NO
line — O_APPEND on the underlying file guarantees position-
atomicity for writes up to PIPE_BUF size, and a single JSONL
record stays well under that limit."

**Counterclaim**: This is true on Linux ext4. It's **false** on:

- macOS HFS+/APFS in the >4KiB record case
  (`PIPE_BUF` matters for pipes; for regular files, atomicity
  depends on the filesystem and the OS).
- Network filesystems (NFS, SMB) — the spec acknowledges
  these in `pass-persistence.md:291` ("Falsifies on NFS").
- Any case where the write spans an i-node boundary (very large
  attestation records with long `Reason` strings).

So a torn line CAN happen. ADR-015 Part D step 5 says
`tornRowDetect` handles row-hashes. The architect's recommendation
at `tier-1-pass-persistence-contracts.md:374-378` is to drop
invariant 6 entirely ("Sqlite WAL + the JSONL fsync inversion
already covers the load-bearing durability surface").

But invariant 2 (JSONL source of truth) means JSONL torn lines
are now load-bearing for **attestation** correctness, not just
pass-row correctness. If `loadFromJSONL` hits a truncated final
line, what does it do?

The spec says (`tier-1-pass-persistence-contracts.md:408-410`):
"Does `attestations.loadFromJSONL` produce a deterministic
failure mode?" — and notes this as an attack surface for the
adversary. The contract surface itself doesn't answer.

Current verifier behavior is the closest analog
(`runner/attestation_verifier.go:99-145`): a JSON-unmarshal
failure produces an issue but `Verify` continues to next line.
But `loadFromJSONL` is different — it's populating the
authoritative store. A bad record means a missing record in
memory, which means a missed verdict, which means a clause that
should be `pass` looks `awaiting-attestation` to the dispatcher.

**Reproduction**:

1. `printf '{"attestation_id":"att-1","kind":"depth-type","arrow_id":"A1","clause_id":"C1","verdict":"pas' > .ghyll/attestations.jsonl`
   (truncated final line, no newline, no terminating brace).
2. `ghyll run .`
3. `loadFromJSONL` reads the file. What is the contract?

Three behaviors are defensible:

- **Strict**: torn line is `ErrAttestationAuditLost`; refuse to
  start.
- **Lenient**: skip torn line, log a warning, accept all preceding
  lines, truncate the file at the last complete line on first
  successful Record.
- **Recovery-driven**: load up to last complete line, mark the
  truncated record as "lost", emit
  `recovery-torn-row-rollback` event, append a forensic note
  line.

The spec must pick one. The current contract leaves it as "the
adversary will tell us" — but the adversary tells you now: pick
one explicitly. **Recommendation: Lenient.** A strict mode
prevents the operator from booting after a kernel panic mid-
write; recovery-driven adds complexity without forensic value
beyond the audit_durability_failed bus event.

The contract addition: `loadFromJSONL` returns `(loaded int,
truncated bool, err error)`. Truncated true means the file had a
partial trailing line; the loader stopped at the last complete
record. The session emits an
`OpEventAttestationAuditDurabilityFailed` event with
`Detail: "trailing truncated line at offset N skipped"`.
On the first successful subsequent Record, the writer truncates
the file at offset N before appending.

Confidence: 80%.

---

## High findings

### F-7: `evaluation_runs.end_status` flip from `running` → JSONL verdict has no schema support and existing semantics treat the column as set-once

**Affects**: `pass-persistence.md:316-320` (open question),
`ADR-015 Part D:144-146`,
`tier-1-pass-persistence-contracts.md:262-263`,
`engine/records.go:447-476`, `engine/store.go:301-321`,
`engine/journal.go:469-489`.

**Claim**: ADR-015 Part D step 4: "clauses with end_status=running
but a JSONL verdict in the attestation store → flip status, fire
event."

**Counterclaim**: Three problems:

1. **No write API exists**: `engine/records.go:464` does `INSERT
   INTO evaluation_runs ... ON CONFLICT(id) DO NOTHING`. Recovery
   cannot update `end_status` through `InsertEvaluationRun`. The
   spec doesn't add a new write API.

2. **Sentinel pollution**: `EndStatus` is a `ClauseStatus` enum
   (`runner/runner.go:328`). Valid values are
   `StatusRunning|StatusPass|StatusFail|StatusUnevaluated|...`.
   "Pass per the JSONL verdict" maps to `StatusPass` — but the
   recovery write loses the provenance ("this end_status came
   from late JSONL reconciliation, not from the runner") unless
   the spec adds a `recovered_from_jsonl bool` or
   `end_status_source TEXT` column. Without it, an `evaluation_runs`
   row from a clean run and one from a recovery flip look
   identical, so any downstream consumer that wants to verify
   "the runner saw a verdict before stamping" can't.

3. **JSONL verdict ≠ clause status**: the JSONL records an
   *attestation* verdict (`AttestationPass|Fail|InsufficientBasis`).
   The clause `EndStatus` is a runtime status. The mapping today
   (`runner/dispatcher.go:221-234`) is:
   - verdict=pass → DispatchInput.AwaitingAttestation = false
     (clause's EndStatus stays whatever the evaluator returned,
     which was Running, until DeriveArrowStatus translates it).
   - verdict=fail → AwaitingAttestation stays true.
   - verdict=insufficient-basis → InsufficientBasis = true.

   So "flip end_status to verdict" is not even a 1:1 mapping. The
   spec's casual "set end_status = JSONL verdict" hides a
   semantic translation step.

**Reproduction**:

1. Pre-crash: clause C5 ran, evaluator returned (Pass=false,
   nothing), runner set `end_status=StatusRunning`
   (`runner/runner.go:544`), persisted via journal.
2. Operator answered attestation with verdict=insufficient-basis,
   JSONL has the line, engine got the row before crash.
3. Recovery: per the spec, flip end_status to "insufficient-basis".
   But `ClauseStatus` has no `Insufficient` enum value — `InsufficientBasis`
   is a flag on `ClauseDeriveInput` (`runner/arrow.go:163-171`).
   So step 4 has nowhere to land.

**Remediation**:

1. Add `Store.UpdateEvaluationRunReconciled(ctx, runID,
   endStatus ClauseStatus, source string, at string)` to the
   contracts. `source` is "recovery-attestation-replay" so audit
   distinguishes runner-set from recovery-set values.
2. Add a column `recovery_source TEXT NOT NULL DEFAULT ''` to
   `evaluation_runs`. Schema bump → `schemaVersion = 3` in
   `engine/store.go:111` (validation-pass-10 C7's mismatch check
   will then refuse opens from older binaries — desired, the new
   recovery path is not backward-compatible at the data layer).
3. Specify the verdict → ClauseStatus mapping table in ADR-015:
   - `pass` → `StatusPass`
   - `fail` → `StatusFail`
   - `insufficient-basis` → `StatusRunning` (keeps it pending; the
     dispatcher's `InsufficientBasis` flag is process-local and
     cannot be reconstructed from disk anyway — see F-1).

Confidence: 90%.

---

### F-8: `ReplayTargets.Passes` addition is a breaking change for the four existing literal callers

**Affects**: `tier-1-pass-persistence-contracts.md:180-187`,
`engine/replay.go:42-54`, `cmd/ghyll/arrow_cmd.go:94-100`,
`cmd/ghyll/engine_cmd.go:280-285`, `cmd/ghyll/session_engine.go:235-241`,
`engine/replay_test.go:94,176`, `engine/attestations_test.go:303,336`.

**Claim** (contracts:180-187): "Passes Field. When nil, the
passes step is skipped (for tests that don't exercise the path)."

**Counterclaim**: Go struct literals tolerate missing fields
without error, so the four production call sites
(`arrow_cmd.go:94`, `engine_cmd.go:280`, `session_engine.go:235`)
continue to compile. They will all pass `nil` for the new
`Passes` field by virtue of zero-value. Per the contract that's
"skip the passes step" — so the BDD scenarios that drive
`cmd/ghyll/arrow_cmd.go` (e.g., `ghyll arrow show <id>` querying
persisted state) will silently not load passes. The lift of F-3
("Query historical pass") is supposed to work through `/passes
<id>` — which goes via session_engine.Open, which passes
`r.passes` — so that path is fine.

But: `cmd/ghyll/engine_cmd.go:280-285` (`ghyll engine replay`) is
an operator CLI that prints replay counts. It needs to print
`passes loaded: N` AND the new recovery counters. The spec does
not update `engine_cmd.go` to pass `Passes` and does not specify
the CLI output extension. The result: `ghyll engine replay`
silently omits the new entities, so operators can't see them
without `/passes`. That's a regression for the existing
"inspect-via-CLI" workflow.

**Remediation**:

1. Update `cmd/ghyll/engine_cmd.go:cmdEngineReplay` to construct
   a `*runner.PassRegistry` and pass it; add the new counts to
   the CLI output (`passes: N open, M closed, K aborted`,
   `recovery: N orphans-aborted, M preserved, K runs-flipped`).
2. Update `cmd/ghyll/arrow_cmd.go` to pass a PassRegistry so
   `ghyll arrow show <id>` can report "in use by pass P1
   (recovered_at T)" alongside other arrow state. Optional but
   the existing CLI surface is incomplete without it.
3. Document in the contracts that **every literal
   `ReplayTargets{}` call site must be reviewed**; the four
   above are the production ones. The spec currently doesn't
   require this review.

Confidence: 95%.

---

### F-9: `Recovery` is called with `targets ReplayTargets` but recovery needs more state than ReplayTargets carries

**Affects**: `tier-1-pass-persistence-contracts.md:243-248,239-241`,
`engine/replay.go:42-54`.

**Claim** (contracts:243-248): `Recovery(ctx, store, bus, targets
ReplayTargets) (RecoveryReport, error)` — "targets carries the
runner-layer stores Replay populated."

**Counterclaim**: Recovery needs access to:

- The attestation JSONL file path (for invariant 2 reconciliation,
  if it has to re-read past the in-memory load — F-6 case).
- The `engineRuntime.workdir` for the operator-bus events that
  reference filesystem paths.
- The current process's PID and start time for the `recovered_at`
  stamping (today `recovered_at = time.Now()`; for idempotence
  per F-12 it should be derived deterministically).
- A `Now func() time.Time` for testability (the contracts
  enforce idempotence — "running twice yields the same
  RecoveryReport" — which is impossible if `time.Now` is called
  unmocked).
- Optionally the InsufficientBasisTracker (recovery may need to
  reset its counter for clauses whose verdict it just reconciled
  in step 4).
- The PassRegistry — already in ReplayTargets per F-8 — but the
  contract doesn't say Recovery may also call `Resume` on it
  (F-3 dependency).

`ReplayTargets` doesn't carry any of these. The Recovery
function's signature is too narrow. Either expand it (matching
the `engineRuntime` shape) or pass an explicit
`RecoveryDependencies` struct that's distinct from
`ReplayTargets`.

**Reproduction**:

1. Implementer reads contracts:243-248, builds `Recovery(ctx,
   store, bus, targets)`.
2. Inside, needs `time.Now` for `recovered_at` — uses
   `time.Now()` directly.
3. Implementer writes a test: calls Recovery twice on the same
   store. First call stamps `recovered_at = T1`. Second call
   sees the preserved row with `recovered_at` already set, but
   per invariant 3 the spec says "running twice yields the same
   RecoveryReport". If the second call re-stamps `recovered_at =
   T2`, the report differs (the stamps don't match) and the
   invariant fails. If the second call leaves it alone, the
   spec's "deterministic and idempotent" claim is salvaged but
   the underlying mechanism is "skip-if-already-set" — fragile.

**Remediation**:

1. Define `RecoveryDeps` (or expand `ReplayTargets` consistently)
   with `Now func() time.Time`, `JSONLPath string`,
   `IBTracker *runner.InsufficientBasisTracker`.
2. Specify the recovered_at semantics: "set on first preservation;
   never updated on subsequent recoveries even if the pass is
   re-preserved" (the "skip-if-already-set" pattern). Clarifies
   F-12.

Confidence: 80%.

---

### F-10: `engine status` CLI race with in-flight Recovery — read-only Store sees half-recovered state

**Affects**: `tier-1-pass-persistence-contracts.md:423-426`
(adversary surface, unanswered), `engine/store.go:63-74,156-184`,
`cmd/ghyll/engine_cmd.go:178-249`.

**Claim** (contracts:423-426): "CLI `ghyll engine status` race:
the CLI opens a read-only store. Does it see a coherent snapshot
if Recovery is in flight?"

**Counterclaim**: Sqlite WAL mode (set in `OpenStore`,
`store.go:57`) provides snapshot isolation for readers. Readers
don't block writers, writers don't block readers. So the
`OpenStoreReadOnly` call (`store.go:68`) will not race with
in-flight writes per se — it sees a consistent committed
snapshot. **BUT**:

1. **The session lockfile is for the chat session, not for engine
   reads**. `cmd/ghyll/lockfile.go:19` locks
   `.ghyll.lock`; the engine_cmd subcommands
   (`engine_cmd.go:175-194`) do not acquire this lock. They open
   the engine read-only and read. So a session with an in-flight
   Recovery and a concurrent `ghyll engine status` can co-exist.
2. **Recovery's writes per F-7 are direct sqlite UPDATEs**.
   Between the time Recovery has written some rows and finished
   the rest, a reader sees a mid-recovery state: some passes
   marked aborted, others still open. That state is *committed*
   (sqlite WAL would have to be flushed for the reader to see
   it — which depends on commit granularity).
3. **If Recovery uses a single big transaction**: reader sees
   either pre-recovery or post-recovery state, but not in
   between. Spec doesn't say.
4. **If Recovery uses per-row UPDATEs without an outer
   transaction**: reader sees torn state. Spec doesn't say.

The contracts list this as an adversary surface but don't answer
it. The implementer will choose one of (3) or (4) — without
guidance, probably (4) because per-row UPDATEs are simpler.

**Reproduction**:

1. Session crashed with 100 open passes; restart, Recovery
   starts.
2. Operator on another terminal runs `ghyll engine status`.
3. CLI opens read-only, runs `SELECT COUNT(*) FROM passes WHERE
   state='open'`. If the engine has 50 already-flipped and 50
   not-yet-flipped, the count is 50 — internally inconsistent.

**Remediation**:

ADR-015 must specify:

- Recovery runs in a single `BeginTx`/`Commit` so readers see
  pre- or post-recovery atomically. (`engine/journal.go:436-456`
  is the pattern — `drainAmendments` uses a transaction for
  similar reasons.)
- If the recovery work is too large for one transaction (large
  number of passes), batch into smaller transactions and
  document the read-during-recovery semantics ("counts may
  reflect a mid-recovery state; re-run after a moment").

Recommendation: single transaction. Recovery is bounded by orphan
pass count, which under single-operator throughput is small (≤
the count of `(role, context)` tuples).

Confidence: 85%.

---

### F-11: `Journal.enqueue` 100ms backpressure means a burst of pass-aborts (e.g., amendment-drain of 10 arrows) can drop journal events — invariant 1 violated

**Affects**: `engine/journal.go:127-159`,
`pass-persistence.md:62-66` (invariant 1: "Pass state persisted on
every transition"), `tier-1-pass-persistence-contracts.md:160-161`
("the observer body is a constant-time chan send and returns
immediately").

**Claim** (invariant 1): "Pass state is persisted on every
transition. … so a crash between mutate-and-release cannot lose
the transition — the next state read sees the persisted value."

**Counterclaim**: `engine/journal.go:135-159` shows the journal's
enqueue path has a 100ms budget (`enqueueBackpressureBudget`).
After 100ms of channel-full, the event is **dropped** and
`j.dropped` increments. This is a deliberate design choice from
integrator-pass C1 to avoid stalling the runner.

When a grid amendment drains 10 arrows in one shot
(`runner/amendmentqueue.go` Drain — emits `AmendmentEventDrain`
with the full drained list — which then per F-2/F-3 of this spec
also fires N pass-abort events because every pass whose arrow was
amended is supposed to be aborted), the journal channel can fill.

Default buffer is 1024 (`journal.go:71`). 10 pass-aborts +
findings + amendments are nowhere near that. But: if a
consumer-side sqlite write stalls (background fsync, disk
pressure), one slow write pins the consumer goroutine while N
producers race to enqueue. The 100ms budget elapses; events drop.

Per invariant 1, **a dropped pass event means the engine row
never updates**. The user's "correctness over speed" doctrine
calls this unacceptable: the engine row stays
`state=open` though the in-memory pass is `closed`. Next restart,
Recovery sees the row as orphan → aborts as crash. The operator
sees "your closed pass was reported as crashed", which is a
correctness lie.

**Reproduction**:

1. Trigger an amendment-drain that aborts 5 passes.
2. Pin the journal consumer with a 1-second sleep on the sqlite
   write side (simulating disk contention).
3. The 5 pass aborts hit `enqueue`. First fills the buffer (it
   doesn't, with default 1024); but in a stress test with
   buffer=10, 5 events overflow. Within 100ms each, they drop.
4. 5 passes have `state=closed` in memory and `state=open` in
   the engine.
5. Restart. Recovery aborts them as crash. Operator sees
   incorrect history.

This isn't hypothetical — `Dropped()` is exposed
(`journal.go:122`) precisely because the design accepts the
trade-off. But the trade-off is unacceptable for `passes` per
invariant 1.

**Remediation**:

Two options:

- **A (raise pass-event priority)**: When enqueuing a `jKindPass`
  event, block indefinitely (or with a much longer budget like
  30s) rather than drop. The pass transition is durable per
  invariant 1; other events (gridmem, classification overwrite)
  can drop without breaking invariants.

- **B (lift invariant 1)**: Spec says "Pass state is persisted
  on every transition unless the journal drops the event; when
  dropped, Recovery on next start treats the in-memory state as
  authoritative" — which is impossible because in-memory state
  is gone after crash. So B is non-viable.

Take Option A. Update the contracts to specify enqueue semantics
for pass events differently from other events.

Confidence: 90%.

---

### F-12: Recovery idempotence claim is unprovable without timestamp injection — `recovered_at = time.Now()` re-stamping breaks the invariant

**Affects**: `pass-persistence.md:74-78` (invariant 3),
`ADR-015 Part D:130-132,143-146`,
`tier-1-pass-persistence-contracts.md:355` ("Recovery is a pure
function of (store, JSONL) — no time-of-day, no random. Second
invocation returns the same RecoveryReport.").

**Claim**: "Crash recovery is deterministic and idempotent.
Running the recovery pass twice on the same store produces the
same end-state."

**Counterclaim**: Two failure modes:

1. **`recovered_at` re-stamping**: A preserved pass at recovery #1
   gets `recovered_at = T1`. At recovery #2 (e.g., crash during
   the operator's attestation reply), the spec doesn't say
   whether `recovered_at` is updated to T2 or kept at T1. Per
   the contract, "the same end-state" — but T2 ≠ T1. Two
   defensible behaviors; the spec doesn't pick one.

2. **OperatorEvents emitted both times**: Per F-2's third
   scenario, recovery emits
   `recovery-attestation-republished` for every preserved pass.
   Recovery #2 sees the same preserved pass and (per the spec's
   description "the hint is re-published") emits the event
   *again*. RecoveryReport.Events differs between calls.

The contract at `tier-1-pass-persistence-contracts.md:355`
explicitly says "Second invocation returns the same
RecoveryReport." This is false unless:

- `recovered_at` is set-once (and the contract says so).
- Recovery skips passes that already have `recovered_at` (i.e.,
  on second invocation, the preserved set is empty).
- OR `Now` is injected.

**Reproduction**:

1. Restart, Recovery preserves P1 at recovered_at=T1, emits
   `recovery-attestation-republished` with timestamp T1.
2. Re-call Recovery in the same process (e.g., test that
   exercises idempotence).
3. Recovery sees P1 still open, still attestation-pending.
   Re-stamps `recovered_at = T2`. Emits same event with T2.
4. RecoveryReport from #2 differs from #1 in timestamps.

**Remediation**:

Specify in the contract:

1. `recovered_at` is set ONCE — Recovery checks `recovered_at !=
   ''` and skips the pass if set. (Idempotence-on-engine-row.)
2. Recovery's emitted events suppress when `recovered_at` is
   already set (skip the re-publish too). Then RecoveryReport on
   recovery #2 is a no-op: counts all zero, events empty.
3. Add `Now func() time.Time` to RecoveryDeps (see F-9) so tests
   can pin timestamps for the first call.

Alternatively: redefine "deterministic and idempotent" as
"produces the same engine end-state, but RecoveryReport may
differ across calls". Less crisp but matches reality.

Confidence: 90%.

---

### F-13: Replay-then-Recovery sequencing — what if Replay returns with `ReplayCounts.Errors` non-empty?

**Affects**: `pass-persistence.md:305-311` (open question 2),
`ADR-015 Part B:84-95,155-158`, `engine/replay.go:65-203`
(per-row error accumulation, J9), `cmd/ghyll/session_engine.go:220-248`.

**Claim** (`session_engine.go:242-245`): "if err == nil {
r.replayDone = true }". So Replay only marks done when the
top-level error is nil. But per J9 Replay returns nil even when
per-row errors accumulate (`counts.Errors` is non-empty but the
return error is nil).

**Counterclaim**: A Replay with per-row errors leaves some
in-memory stores partially populated (the rows that loaded
succeeded; the failed ones are missing). Recovery then runs
against a half-loaded state:

1. The `passes` step (new) might have had per-row errors. Some
   passes loaded; others not. The unloaded passes are NOT in the
   in-memory registry but ARE in the engine table.
2. Recovery's `orphanScan` queries the engine table directly for
   open passes (the contracts don't say, but that's the natural
   implementation). It sees the un-replayed passes as orphans.
3. Recovery aborts them as crash. But they may not have been
   crashed — they may have been valid open passes whose Replay
   failed to load due to malformed `arrow_id` or similar.
4. Worse: if the operator just answered attestation for one of
   those passes, the answer lands in the JSONL but the in-memory
   pass doesn't exist. Recovery's
   `evaluationRunReconcile` tries to flip the run's `end_status`
   for a pass that isn't in memory — confusing trace state.

**Remediation**:

1. Add a contract assertion: Recovery refuses to run if
   `ReplayCounts.Errors` is non-empty. Operator sees a clear
   "previous start left malformed rows; investigate before
   restart" message.
2. OR: Recovery reports its own per-row errors and propagates
   the union to the operator.
3. Document the order in ADR-015 Part B: Replay errors are
   surfaced to session.Open; session.Open decides whether to
   invoke Recovery based on the per-row error count.

Recommendation: option 1, fail-loud. Half-recovered state is
worse than refusing to start.

Confidence: 80%.

---

### F-14: `engine_cmd.cmdEngineReplay` opens read-only and runs Replay, but Replay writes to in-memory stores AND now Recovery's writes go to a read-only sqlite — needs explicit decoupling

**Affects**: `cmd/ghyll/engine_cmd.go:271-315`,
`ADR-015 Part D:155-158`,
`tier-1-pass-persistence-contracts.md:316-325`.

**Claim**: The contracts say `cmd/ghyll/session_engine.go` calls
Recovery. The contracts don't say what `cmd/ghyll/engine_cmd.go`
(the operator CLI) does.

**Counterclaim**: `ghyll engine replay` is the operator's
introspection tool — "what would happen if I restarted?". It
opens the store read-only, runs Replay, prints counts. If the
ADR makes Recovery part of restart, `ghyll engine replay` should
either:

- ALSO run Recovery (for full diagnostic value), against a
  read-only store — which is impossible because Recovery writes.
- NOT run Recovery, and explicitly disclose "this is a
  replay-only check; recovery would also run on session start".

The current ADR-015 doesn't address this. After implementation
ships, an operator runs `ghyll engine replay` to verify their
db, sees clean output, thinks "great", but the *actual* session
start would have run Recovery and produced different counts
(orphan-aborts, JSONL reconciliations). False confidence.

**Remediation**:

1. Add a new subcommand `ghyll engine recover --dry-run` that
   opens the store read/write but does Recovery against an
   in-memory transaction that rolls back at the end. Prints what
   Recovery would do without committing.
2. Update `ghyll engine replay`'s output to clarify "this is the
   replay-only count; session start additionally runs Recovery
   (use `ghyll engine recover --dry-run` to preview)".
3. Update ADR-015 with the operator-CLI ergonomics section.

Confidence: 75%.

---

## Medium findings

### F-15: F-5 "torn checkpoint records" scenario has no executable mechanism

**Affects**: `pass-persistence.md:90-96` (invariant 6),
`pass-persistence.md:213-222` (F-5 BDD),
`tier-1-pass-persistence-contracts.md:362-378`.

**Claim** (`pass-persistence.md:213-222`): "the engine `passes`
table has a row for P1 whose journal-derived hash does not match
the in-row content … the row is rolled back to the last verified
state".

**Counterclaim**: The architect explicitly recommends dropping
invariant 6 (contracts:374-377: "Sqlite WAL + the JSONL fsync
inversion already covers the load-bearing durability surface").
But the BDD scenario is one of the eight `@deferred` claimed
liftable. If invariant 6 is dropped, F-5 must be retired from the
`@deferred` list — otherwise the lift count drops to 7.

Either:

- Restore invariant 6 with one of options B/C from
  contracts:367-372 (row-hash column, content-addressed schema).
- OR formally remove the F-5 scenario from
  `state-machine.feature` with a note that "torn record detection
  is delegated to sqlite WAL atomicity; the scenario as written
  cannot be exercised."

The spec currently equivocates. Pick.

Confidence: 70%.

---

### F-16: No `Store.GetPass` API specified despite F-3 invariant 7 requiring it

**Affects**: `pass-persistence.md:97-101` (invariant 7),
`pass-persistence.md:178-191` (F-3 BDD),
`tier-1-pass-persistence-contracts.md:359` ("New `Store.GetPass(ctx, id) (PassRecord, bool, error)`").

**Claim** (contracts:359): "New `Store.GetPass(ctx, id)
(PassRecord, bool, error)` SELECTs the row; returns `not-found`
cleanly."

**Counterclaim**: Mentioned once in the enforcement table but not
declared as a top-level contract entry in
`engine/records.go` schema additions. The implementer should not
have to dig through the enforcement table to find write APIs.

The `/passes P5` slash command (F-3 BDD) is also new but not
sketched. `cmd/ghyll/session.go:1093-1094` shows only the
bare-list `/passes` (no argument). The session.go command handler
needs an argument-parsing path, an error message for not-found,
and an output format.

**Remediation**:

1. Add explicit `Store.GetPass(ctx, id) (PassRecord, bool,
   error)` to the records.go section of the contracts.
2. Add a session.go contract section: `handlePassesCommand`
   signature changes to accept an optional ID arg; output
   format specified.
3. Add a `Store.ListPasses(ctx, filter PassListFilter) ([]PassRecord,
   error)` for the list-by-state pattern operators will need
   (`/passes --closed`, `/passes --aborted`).

Confidence: 90%.

---

### F-17: PassRecord schema bumps schemaVersion but contracts don't update it

**Affects**: `tier-1-pass-persistence-contracts.md:18-34`,
`engine/store.go:108-111` (`schemaVersion = 2`),
`engine/store.go:118-153` (`ensureSchemaVersion`).

**Claim** (contracts:18-34): adds the `passes` table via `CREATE
TABLE IF NOT EXISTS`. With the `IF NOT EXISTS` guard, an existing
DB without the table gets it created; an existing DB with it is
unchanged.

**Counterclaim**: The schema is structurally extended.
`schemaVersion` should bump from 2 to 3. Per
`engine/store.go:108-153`'s contract, the bump is required when a
migration ships that newer binaries can't represent via
`CREATE ... IF NOT EXISTS`. The new `passes` table can be created
via IF NOT EXISTS, so technically the bump isn't required for
forward compat — BUT an older binary opening a v3 DB *won't know
about the table* and won't load any passes. The schema-mismatch
check (`engine/store.go:139-143`) refuses the open with a clean
error, which is the right behavior.

If the spec wants newer→older protection, schemaVersion MUST
bump to 3. The contracts don't say.

Also: if F-7 lands (`recovery_source TEXT` on evaluation_runs),
that requires `ALTER TABLE` because the column isn't covered by
IF NOT EXISTS on an existing schema. That's a real migration —
schemaVersion must bump to 3 in any case.

**Remediation**:

Add to contracts:
- `engine/store.go:schemaVersion` bumps from 2 to 3.
- Add `ALTER TABLE evaluation_runs ADD COLUMN recovery_source
  TEXT NOT NULL DEFAULT ''` to the schema string OR run as an
  explicit migration in `ensureSchemaVersion`.
- Migration runs once; idempotent.

Confidence: 80%.

---

### F-18: Recovery events fire into a bus with zero subscribers at recovery time — events silently lost

**Affects**: `tier-1-pass-persistence-contracts.md:427-429`
(adversary surface, unanswered),
`tier-1-pass-persistence-contracts.md:331-340` (4 new event
kinds), `runner/operatorbus.go:14-16,108-126`.

**Claim** (contracts:243-247): "bus is optional — nil disables
event publication (used by tests)."

**Counterclaim**: At Recovery time (between Replay and
attachJournal), nobody has subscribed to the bus yet —
attachJournal (which doesn't subscribe to the bus directly, but
the chat-loop's `s.bus.Subscribe(...)` is called even later in
`session.go`). The 4 new events fire to a bus with zero
subscribers. Per `operatorbus.go:14-16`, "The bus retains no
history; subscribers that join late see only events published
after they subscribed."

So the audit trail Recovery promises — "one OperatorEvent per
reconciliation action" — vanishes into nothing. The operator
sees nothing on the chat-loop output.

**Reproduction**:

1. Restart with 5 orphan passes.
2. Recovery fires 5 `recovery-pass-aborted-crash` events.
3. The bus has zero subscribers; events fan out to nobody.
4. Session.Open completes. Chat-loop subscribes to bus for
   future events. Operator sees no banner about the recovery.
5. Operator runs `/passes` and sees no closed-passes (they're
   in the engine; only open passes are in the registry).
   Operator has no idea Recovery did anything.

**Remediation**:

Two options:

- **Option A**: Recovery returns its RecoveryReport.Events list;
  session.Open prints them (or pushes them through a startup
  banner channel that the chat-loop drains on first iteration).
  The bus doesn't carry them.
- **Option B**: A persistent "recovery_log" table or a small
  in-memory `recoveryEvents []OperatorEvent` buffer that the
  bus's first subscriber drains. Recovery publishes to that
  buffer; the chat-loop's bus.Subscribe call also reads the
  buffer.

Option A is simpler. Specify: Recovery does NOT publish to bus
(bus is for runtime events only). RecoveryReport.Events is
session.Open's responsibility to surface to the operator.

Confidence: 85%.

---

## Notes (not findings — context for the implementer)

### N-1: Existing `Pass.bus.Publish(OperatorEvent{Kind: OpEventPassClosed, ...})` in `runner/pass.go:160-167` already fires from inside `p.mu`. The new emit-from-registry path adds a second emission. The contracts say to call `r.registry.emit(...)` AFTER state mutation but BEFORE lock release — that means BOTH the existing bus.Publish AND the new registry.emit run while p.mu is held. F-4 already covers the deadlock surface; this is just a reminder that we're piling two distinct fanout paths on top of each other.

### N-2: The contracts mention `PassRegistry.Register` will emit `PassEventOpen` (`contracts:148-150`). Today the dispatcher calls `d.Passes.Register(pass)` AFTER `OpenPass` succeeds (`dispatcher.go:178`). If Register also emits, the existing `bus.Publish(OpEventPassOpened, ...)` in `OpenPass` (`pass.go:124-132`) AND the new registry emit are both firing for the same logical open. Duplicate audit trail. Pick one: either pull the bus.Publish out of OpenPass into the registry's emit, or have registry.emit only fire for PassEventClose/Abort/Recover.

### N-3: `runner/projectstatus.go:60-65` PassRegistry has a comment that explicitly says "Crash-recovery does NOT persist passes — open passes from a crashed previous process are gone on restart". This comment must be removed/updated as part of the Tier 1 change. Easy to miss in a contract that focuses on additions.

### N-4: The contracts don't address `runner.PassRegistry.Resume(...)` (see F-3) — but they DO add `PassEventRecover` enum value at contracts:95. So the design half-anticipates recovery emitting a recover event when re-populating the registry. Close the loop by either adding the Resume API or removing the Recover enum.

### N-5: `runner/attestation_jsonl.go:139-146` already does the fsync-before-return pattern that ADR-015 Part C demands. The contracts say "what changes is the invariant docstring + the failure path: today a JSONL write failure is logged but the in-memory mutation proceeds; the new contract aborts the Record call" (`contracts:282-287`). Verify against the FindingsStore observer pattern: observers there cannot return errors either (`runner/findings.go:107-117` — FindingsObserver is `func(event FindingsEvent)`, no error). The contracts demand `AttestationStore.Record` returns `ErrAttestationAuditWriteFailed`, but the observer signature is `AttestationObserver func(event AttestationEvent)` (`runner/attestationstore.go:75`). Either the observer signature changes (breaking — every existing observer including tree writer is `func(AttestationEvent)`), OR the JSONL writer observer is special-cased and called inline within `Record` BEFORE other observers fire. The contracts gesture at this ("the existing inline-fsync via AttestationJSONLWriter is already correct") but don't make the special-casing explicit. Implementer needs guidance: is the JSONL observer "first observer with error channel" or is the contract using shared in-band error reporting?

### N-6: The contracts at line 95 declare `PassEventRecover` but the spec at `tier-1-pass-persistence-contracts.md:331-340` only lists 4 OperatorEventKinds, none of which is a generic "pass-recovered". The PassEvent enum's Recover is distinct from the OperatorBus events. Make sure handlers don't mix the two.

### N-7: The session BDD scenario `state-machine.feature:148-163` "Restart with no open passes" requires Recovery to be a no-op when no open passes exist. Verify the implementer handles the empty-orphans-list path without firing any events or writes — RecoveryReport with all zeros is the expected end state.

### N-8: The "session lockfile guards concurrent recovery" claim (`pass-persistence.md:248` FM-5) is correct for the chat session, but per F-10 the engine_cmd subcommands bypass the lockfile. Documented because the architect mentioned it as a presumed protection.

### N-9: The implementer's contract surface for Recovery's internal scans (orphanScan, attestationPendingScan, orphanAbort, evaluationRunReconcile, tornRowDetect — `contracts:251-265`) is described as methods on a `recoveryRun` struct but no struct definition is given. Sketch the struct: `type recoveryRun struct { store *Store; bus *runner.OperatorBus; passes *runner.PassRegistry; attestations *runner.AttestationStore; now func() time.Time; report RecoveryReport }`.

### N-10: Test coverage gap (mentioned in the prompt). The contracts list BDD scenarios but no unit tests. At minimum the implementer needs:
- `TestRecovery_NoOpenPasses` (empty restart)
- `TestRecovery_OrphanAbort` (1 orphan, attestation-clean)
- `TestRecovery_AttestationPendingPreserved` (1 orphan with awaiting attestation — depends on F-1 resolution)
- `TestRecovery_TornRowRollback` (depends on F-15 resolution)
- `TestRecovery_Idempotent` (twice-call invariant — depends on F-12)
- `TestRecovery_JSONLMissingFreshProject` (depends on F-5)
- `TestRecovery_JSONLTrailingTruncated` (depends on F-6)

Specify these in the contracts so the implementer has a target.
