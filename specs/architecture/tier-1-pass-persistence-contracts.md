# Tier 1: Pass persistence + crash recovery — Go contracts

Architect output for the Tier 1 implementation phase. Every type
and function signature below traces to an invariant in
`specs/architecture/components/pass-persistence.md` or a decision
in `docs/decisions/015-pass-persistence-and-jsonl-source-of-truth.md`.
No method bodies — implementer fills those.

---

## `engine/store.go` — schema addition

```go
// passes table, appended to the existing CREATE TABLE block.
// Columns map 1:1 to runner.Pass plus recovered_at (set by recovery
// when the pass survived a restart via the attestation-pending
// exception — pass-persistence.md invariant 4).
const passesSchema = `
CREATE TABLE IF NOT EXISTS passes (
    pass_id        TEXT PRIMARY KEY,
    role           TEXT NOT NULL,
    context        TEXT NOT NULL,
    arrow_id       TEXT NOT NULL,
    grid_version   INTEGER NOT NULL DEFAULT 0,
    state          TEXT NOT NULL CHECK (state IN ('open','closed','aborted')),
    opened_at      TEXT NOT NULL DEFAULT '',
    closed_at      TEXT NOT NULL DEFAULT '',
    close_reason   TEXT NOT NULL DEFAULT '',
    recovered_at   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_passes_state ON passes(state);
CREATE INDEX IF NOT EXISTS idx_passes_arrow ON passes(arrow_id);
CREATE INDEX IF NOT EXISTS idx_passes_role_ctx ON passes(role, context);
`
```

---

## `engine/records.go` — PassRecord + UpsertPass

```go
// PassRecord is the persistence shape of a runner.Pass. Mirrors
// the table columns 1:1. Validation runs on the write boundary
// (matching the existing FindingRecord / AmendmentRecord pattern).
type PassRecord struct {
    PassID       string `json:"pass_id"`
    Role         string `json:"role"`
    Context      string `json:"context"`
    ArrowID      string `json:"arrow_id"`
    GridVersion  uint64 `json:"grid_version,string"`
    State        string `json:"state"`         // open | closed | aborted
    OpenedAt     string `json:"opened_at"`     // RFC3339
    ClosedAt     string `json:"closed_at"`     // RFC3339, empty when open
    CloseReason  string `json:"close_reason"`  // empty when open
    RecoveredAt  string `json:"recovered_at"`  // RFC3339, set by Recovery only
}

// knownPassState is the canonical set of state values.
var knownPassState = map[string]struct{}{
    "open":    {},
    "closed":  {},
    "aborted": {},
}

// Validation errors.
var (
    ErrEnginePassIDEmpty      = errors.New("engine: pass-id required")
    ErrEnginePassRoleEmpty    = errors.New("engine: pass role required")
    ErrEnginePassContextEmpty = errors.New("engine: pass context required")
    ErrEnginePassArrowEmpty   = errors.New("engine: pass arrow_id required")
    ErrEnginePassStateInvalid = errors.New("engine: pass state not in {open,closed,aborted}")
)

// UpsertPass inserts a new pass row or updates an existing one
// (idempotent on pass_id). The state-machine transitions
// (open → closed/aborted) are validated at the runner level; the
// engine accepts whatever the runner writes provided it's
// structurally valid.
func (s *Store) UpsertPass(ctx context.Context, r PassRecord) error
```

---

## `runner/projectstatus.go` — PassRegistry.Observe

```go
// PassEventKind names the lifecycle moment producing the event.
// One per Pass state transition.
type PassEventKind string

const (
    PassEventOpen     PassEventKind = "open"
    PassEventClose    PassEventKind = "close"
    PassEventAbort    PassEventKind = "abort"
    PassEventRecover  PassEventKind = "recover" // emitted only by engine.Recovery
)

// PassEvent is the observer payload.
//
// At is the wall-clock timestamp when the registry observed the
// mutation. Distinct from Pass.OpenedAt / Pass.ClosedAt (which
// the runner stamps).
type PassEvent struct {
    Kind        PassEventKind
    PassID      string
    Role        string
    Context     string
    ArrowID     string
    GridVersion uint64
    State       PassState
    OpenedAt    time.Time
    ClosedAt    time.Time
    CloseReason string
    RecoveredAt time.Time
    At          time.Time
}

// PassObserver is invoked under the registry's write lock on every
// mutation. Per the FindingsStore / Grid / AmendmentQueue pattern:
// observers MUST be fast and non-blocking (chan-send only). Long
// work hands off to a goroutine.
type PassObserver func(event PassEvent)

// Observe registers an observer. Mirrors FindingsStore.Observe.
// Order of fanout: registration order.
func (r *PassRegistry) Observe(fn PassObserver)
```

`runner.Pass` itself gains an internal `registry *PassRegistry`
pointer (set by `Register`) so `Pass.closeWith` can emit through
the registry:

```go
// closeWith is updated to call r.registry.emit(...) AFTER state
// mutates but BEFORE lockToken.Release. pass-persistence.md
// invariant 1.
//
// NEW behavior:
//   p.state = finalState
//   p.closeReason = reason
//   p.closedAt = time.Now()
//   if p.registry != nil { p.registry.emit(PassEvent{Kind: ...}) }
//   p.lockToken.Release()
//   if p.bus != nil { p.bus.Publish(...) }
```

`OpenPass` similarly emits `PassEventOpen` AFTER the registry
registers AND BEFORE the function returns. (The Register call
itself becomes the emission point — `PassRegistry.Register` calls
`r.emit(PassEvent{Kind: PassEventOpen, ...})`.)

---

## `engine/journal.go` — AttachPasses

```go
// AttachPasses registers a PassObserver that journals every open /
// close / abort. Per the FindingsStore / Grid / AmendmentQueue
// pattern: the observer body is a constant-time chan send and
// returns immediately.
func (j *Journal) AttachPasses(reg *runner.PassRegistry)

// handlePass routes the event to UpsertPass. State, CloseReason,
// OpenedAt/ClosedAt timestamps come from the PassEvent payload;
// the journal does not call back into the registry.
func (j *Journal) handlePass(ctx context.Context, e runner.PassEvent)

// Internal: jKindPass added to the event kind enum, and a
// passes-shaped field added to the journalEvent struct.
const jKindPass = "pass"
```

---

## `engine/replay.go` — passes load + recovery call

```go
// ReplayTargets gains an optional Passes field. When nil, the
// passes step is skipped (for tests that don't exercise the path).
type ReplayTargets struct {
    Findings        *runner.FindingsStore
    Classifications *runner.ClassificationsStore
    Grid            *runner.Grid
    Amendments      *runner.AmendmentQueue
    Attestations    *runner.AttestationStore
    Passes          *runner.PassRegistry // NEW
}

// ReplayCounts gains pass + recovery counters.
type ReplayCounts struct {
    // ... existing fields
    PassesOpen    int // open at restart (BEFORE recovery)
    PassesClosed  int
    PassesAborted int
    Errors        []string
}

// Replay order becomes:
//   1. Attestations (rebuilt from JSONL — ADR-015 Part C inversion)
//   2. Grid arrows
//   3. Requirements
//   4. Classifications
//   5. Findings
//   6. Amendments (active + drained dedup)
//   7. Passes — NEW
//
// Recovery is called AFTER Replay returns. It is its own function
// so callers can compose: load state → optionally inspect →
// reconcile → start journal.
```

---

## `engine/recovery.go` — new file

```go
// RecoveryReport summarizes one Recovery invocation. Counts are
// for telemetry; Events is the audit trail (one entry per
// reconciliation action).
type RecoveryReport struct {
    OrphansAborted          int      // open passes → aborted:crash
    OrphansPreserved        int      // open passes → stayed open (attestation-pending)
    AttestationsReplayed    int      // engine table caught up from JSONL
    EvaluationRunsFlipped   int      // end_status running → JSONL verdict
    TornRowsDetected        int      // hash mismatch → rollback
    Events                  []runner.OperatorEvent
}

// Recovery scans the engine + JSONL state at session start,
// reconciles split-brain conditions, and emits one OperatorEvent
// per reconciliation action. Idempotent: running twice yields the
// same end-state.
//
// MUST be called AFTER engine.Replay and BEFORE the live Journal
// observers attach (otherwise recovery's writes re-journal back
// into the engine).
//
// targets carries the runner-layer stores Replay populated;
// recovery reads them (for attestation-pending detection) and may
// write to them (e.g., to mark passes aborted).
//
// bus is optional — nil disables event publication (used by tests).
func Recovery(
    ctx context.Context,
    store *Store,
    bus *runner.OperatorBus,
    targets ReplayTargets,
) (RecoveryReport, error)

// Internal scans (each is a method on a recoveryRun struct so they
// share the read-once pass list + JSONL records):
//
//   orphanScan        — open passes in engine, no live process
//                       (always true post-Replay; no live procs yet)
//   attestationPendingScan
//                     — subset of orphans whose clauses have a
//                       published-but-unanswered attestation request
//                       (FM-7: this requires attestation-request
//                       persistence; today it falls through to abort)
//   orphanAbort       — remaining orphans → aborted:crash
//   evaluationRunReconcile
//                     — clauses with end_status=running but JSONL
//                       has a verdict → flip status, fire event
//   tornRowDetect     — row hash vs. journal hash mismatch →
//                       rollback; if no verified state, abort:crash
```

---

## `runner/attestationstore.go` — JSONL-source-of-truth inversion

The current code already fires the JSONL writer synchronously
inside `Record` before the in-memory mutation completes
(`runner/attestation_jsonl.go:139-180`). ADR-015 Part C lifts
this from "operational happens-to-be" to "load-bearing invariant":

```go
// Record validates + appends to byID + fires fanout. Per
// ADR-015 Part C: the JSONL observer MUST succeed (fsync return)
// before the in-memory map mutates. If the JSONL write fails,
// Record returns the error and the in-memory map is unchanged.
//
// Implementation note (architect): the existing inline-fsync via
// AttestationJSONLWriter is already correct. What changes is the
// invariant docstring + the failure path: today a JSONL write
// failure is logged but the in-memory mutation proceeds; the
// new contract aborts the Record call.
func (s *AttestationStore) Record(rec AttestationRecord) error

// New error sentinel.
var ErrAttestationAuditWriteFailed = errors.New(
    "attestation: JSONL audit write failed; record not accepted")

// Read path for replay: lift records from JSONL, not the engine
// table. This shifts authority but keeps the engine table as a
// fast-read cache (Lookup hot path stays unchanged).
func (s *AttestationStore) loadFromJSONL(path string) error
```

---

## `cmd/ghyll/session_engine.go` — orchestration

The existing `engineRuntime` gains an `attestationJSONLPath` field
+ a `Passes *runner.PassRegistry` field. Open sequencing changes:

```go
// engineRuntime.Open changes (sketch — implementer writes the body):
//
//   1. open sqlite store
//   2. construct in-memory stores (Findings, Classifications, Grid,
//      Amendments, Attestations, Passes — Passes is NEW)
//   3. attestations.loadFromJSONL(path)              [Part C]
//   4. engine.Replay(store, targets)                 [reads engine
//                                                     into rest of
//                                                     in-mem stores]
//   5. engine.Recovery(store, bus, targets)          [NEW]
//   6. attachJournal — including AttachPasses        [observers go
//                                                     live AFTER 5]
//
// Between steps 4 and 5, the journal is NOT attached. Recovery's
// writes go through the runner stores' usual Mutator paths;
// without observers, they don't re-journal. After step 5, AttachX
// for every store starts the steady-state observer fanout.
```

---

## Four new OperatorEvent kinds

Per pass-persistence.md F-2, F-4, F-5 and ADR-015 Part D:

```go
const (
    OpEventRecoveryPassAbortedCrash       OperatorEventKind = "recovery-pass-aborted-crash"
    OpEventRecoveryAttestationRepublished OperatorEventKind = "recovery-attestation-republished"
    OpEventRecoveryAttestationReplay      OperatorEventKind = "recovery-attestation-replay"
    OpEventRecoveryTornRowRollback        OperatorEventKind = "recovery-torn-row-rollback"
)
```

These join the existing `OpEvent*` constants in
`runner/operatorbus.go`. The bus is volatile; recovery events
fire once per restart and are consumed by whoever subscribed at
that boot.

---

## Enforcement map (invariants → enforcement points)

| Invariant (pass-persistence.md) | Enforcement |
|---|---|
| 1. Pass state persisted on every transition | `Pass.closeWith` calls `PassRegistry.emit` before `lockToken.Release`; `PassRegistry.Register` emits on Open. |
| 2. JSONL is source of truth for attestations | `AttestationStore.Record` returns `ErrAttestationAuditWriteFailed` if JSONL write fails; `attestations.loadFromJSONL` runs before `Replay` for the engine table. |
| 3. Crash recovery is deterministic + idempotent | `Recovery` is a pure function of `(store, JSONL)` — no time-of-day, no random. Second invocation returns the same `RecoveryReport`. |
| 4. Attestation-pending passes survive recovery | `recoveryRun.attestationPendingScan` filters orphans; preserved set keeps `state=open`, sets `recovered_at`. |
| 5. Other open passes → aborted:crash | `recoveryRun.orphanAbort` writes UPDATE; emits `OpEventRecoveryPassAbortedCrash`. |
| 6. Torn checkpoint records detected | `recoveryRun.tornRowDetect` hashes row contents + compares to journal hash. (Architect note: requires an `engine_meta` column or sidecar hash table; spec says "journal record's hash" — investigate. Possible escalation back to analyst.) |
| 7. Query historical pass is a read, not reconstruction | New `Store.GetPass(ctx, id) (PassRecord, bool, error)` SELECTs the row; returns `not-found` cleanly. |

Invariant 6 needs a concrete mechanism. The current schema has
no `row_hash` column on `passes`. Three options for the
implementer to consider (escalate if any is rejected):

- **A (simplest)**: drop invariant 6. Trust sqlite WAL atomicity.
  Torn rows are vanishingly rare on the platforms ghyll runs on.
- **B**: add a `row_hash TEXT` column computed at write time; on
  recovery, recompute from the other columns and compare. Catches
  *content* corruption but not *atomicity* failures.
- **C**: full content-addressed schema — every row has its
  predecessor's hash, like the v1 memory chain. Heavyweight,
  unjustified for live working state.

Architect recommendation: **A**. Sqlite WAL + the JSONL fsync
inversion already covers the load-bearing durability surface;
adding a row-hash column for a near-impossible case is premature.
If invariant 6 is dropped, `pass-persistence.md` and
`ADR-015` need a one-line note.

---

## Spec gap escalations (to analyst)

1. **Attestation-request persistence (A-3)**. `pass-persistence.md`
   open question #1. Deferred to Tier 2 per ADR-015 Part E.
   Implementer ships with FM-7's degraded behavior; needs a
   feature flag or operator message documenting the gap.
2. **Torn-row mechanism for invariant 6**. Architect recommends
   dropping (option A above). Needs analyst sign-off or
   alternative.
3. **Recovery vs. attestation-flow ordering at replay**. The Replay
   order specified above (attestations → ... → passes →
   Recovery) appears sufficient. If a scenario surfaces that
   needs attestations to load AFTER the recovery scan (none in
   current BDD), re-open this.

---

## Handoff to adversary (gate 1)

Adversary's job before the implementer starts: try to break the
contract surfaces above. Specific attack surfaces:

1. **Recovery-during-recovery**: two `Recovery` calls overlap
   (e.g., one from session.Open, one from `ghyll engine recover`
   CLI). What's the lock? Today's session lockfile guards
   sessions, but does it guard the CLI?
2. **JSONL truncated mid-line**: bytes appended but final `\n`
   missing. Does `attestations.loadFromJSONL` produce a deterministic
   failure mode? Does Recovery treat this as `tornRowDetect`?
3. **Pass observer fires during Pass.Close BEFORE lock release**:
   if the observer blocks (a slow chan send), does the lock stay
   held? Per the FindingsStore pattern observers are constant-time
   chan sends — verify.
4. **Attestation-pending detection ambiguity**: an attestation
   request that was *delivered* (operator answered) but the
   answer was *malformed* JSONL — does the orphan abort scan
   correctly treat the clause as resolved (no longer pending) or
   pending (still awaiting)?
5. **ADR-010 inversion blast radius**: enumerate every code path
   today that reads attestation rows from the engine table as if
   it were authoritative. Are any operations broken when the
   table is empty at session start (before `loadFromJSONL` runs)?
6. **CLI `ghyll engine status` race**: the CLI opens a read-only
   store. Does it see a coherent snapshot if Recovery is in
   flight?
7. **Recovery emits events into a bus with zero subscribers**:
   what happens to those events? Lost? (Probably acceptable but
   pin the expected behavior.)

The adversary's findings → `specs/v2/validation-impl-pass-tier1.md`
or similar. Then implementer.
