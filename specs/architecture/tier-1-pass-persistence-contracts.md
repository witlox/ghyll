# Tier 1: Pass persistence + crash recovery — Go contracts

Architect output for the Tier 1 implementation phase. Every type
and function signature below traces to an invariant in
`specs/architecture/components/pass-persistence.md` or a decision
in `docs/decisions/015-pass-persistence-and-jsonl-source-of-truth.md`.
No method bodies — implementer fills those.

**Gate-1 adversary review** (2026-05-20) produced 18 findings,
all remediated in this document, the spec, and the ADR. See
`specs/v2/validation-impl-pass-tier1-remediation.md` for the
disposition log; F-N refs below cite the finding being addressed.

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

// schemaVersion bumps from 2 to 3 per F-17 of the gate-1 review.
// The new column on evaluation_runs is a real migration that
// CREATE ... IF NOT EXISTS cannot apply to an existing v2 DB;
// ensureSchemaVersion runs it once on a v2 → v3 transition.
const schemaVersion = 3

const evaluationRunsRecoveryMigration = `
ALTER TABLE evaluation_runs ADD COLUMN recovery_source TEXT NOT NULL DEFAULT '';
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

// GetPass returns a single row by pass_id. NOT FOUND signals via
// the bool return (matches GetFinding). Used by /passes <id>
// slash command (F-16).
func (s *Store) GetPass(ctx context.Context, id string) (PassRecord, bool, error)

// PassListFilter narrows ListPasses results. Empty = all rows.
// (F-16)
type PassListFilter struct {
    State   string // "" | "open" | "closed" | "aborted"
    ArrowID string
    Role    string
    Context string
    Limit   int
    Offset  int
}

// ListPasses returns rows matching filter, ordered by opened_at
// ASC. Used by /passes (no arg) and by Recovery's orphanScan.
// (F-16)
func (s *Store) ListPasses(ctx context.Context, f PassListFilter) ([]PassRecord, error)

// UpdateEvaluationRunReconciled writes the recovery-reconciled
// end_status + provenance. Distinct from InsertEvaluationRun
// (which is ON CONFLICT DO NOTHING per the one-shot semantics of
// Runner.Evaluate). The source argument is stored in
// recovery_source for audit (e.g., "recovery-attestation-replay").
// (F-2, F-7)
func (s *Store) UpdateEvaluationRunReconciled(
    ctx context.Context,
    runID string,
    endStatus runner.ClauseStatus,
    source string,
    at string,
) error

var ErrEngineEvaluationRunNotFound = errors.New("engine: evaluation_run not found")
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

// PassObserver fires on every mutation. Per the FindingsStore /
// Grid / AmendmentQueue pattern: observers MUST be fast and
// non-blocking (chan-send only). Long work hands off to a
// goroutine.
//
// CRITICAL (F-4): emit runs WITHOUT acquiring any registry lock.
// The observer list is registered one-shot at session start and
// never mutated after that, so unlocked fanout is safe. This
// breaks the AB/BA deadlock with PassRegistry.All() → p.State().
type PassObserver func(event PassEvent)

// Observe registers an observer. Mirrors FindingsStore.Observe
// for shape but NOT for lock semantics (FindingsStore acquires
// its mutex; PassRegistry does not at emit). Registration
// happens at session start; concurrent Observe + emit is a
// caller bug.
func (r *PassRegistry) Observe(fn PassObserver)

// Resume reconstitutes a *runner.Pass from a persisted PassRecord
// and re-acquires the per-(role, context) lock token. Called by
// engine.Recovery for every attestation-pending pass preserved
// across a restart (F-3). Returns the *Pass for live-state
// queries (/passes lists it; dispatcher refuses to open a
// competing pass on the same tuple).
//
// Fires a PassEvent{Kind: PassEventRecover} on emit (unlocked
// fanout per the lock semantics above).
//
// Errors:
//   - *ErrRoleContextBusy if the (role, context) tuple is
//     already held in this process. Should not happen for
//     orphan recovery (process is fresh), but the call is
//     defensive.
//   - ErrPassResumeInvalidState if rec.State != "open".
func (r *PassRegistry) Resume(
    rec engine.PassRecord,
    lockTable *RoleContextLockTable,
) (*Pass, error)

var ErrPassResumeInvalidState = errors.New("pass-resume-invalid-state")
```

`runner.Pass` itself gains an internal `registry *PassRegistry`
pointer (set by `Register` and `Resume`):

```go
// closeWith is updated to capture the event payload while p.mu
// is held, RELEASE p.mu + the lock token, THEN emit. Per F-4
// the lock order is p.mu → release → emit (never p.mu → emit
// with r.mu nested inside).
//
// NEW behavior:
//   p.mu.Lock()
//   if p.state != PassStateOpen { p.mu.Unlock(); return }
//   p.state = finalState
//   p.closeReason = reason
//   p.closedAt = time.Now()
//   payload := PassEvent{Kind: kindFromState(finalState), ...}
//   p.mu.Unlock()
//   p.lockToken.Release()
//   if p.registry != nil { p.registry.emit(payload) }
//   // bus.Publish removed — N-1 / N-2 dedupe; PassEvent is the
//   // single audit path. Journal observer bridges to bus if
//   // downstream code subscribes to OpEventPassClosed.
```

`OpenPass` calls `Register` which emits `PassEventOpen` (under
no registry lock). The duplicate `bus.Publish(OpEventPassOpened)`
inside `OpenPass` is **removed** per N-1 / N-2.

---

## `engine/journal.go` — AttachPasses

```go
// AttachPasses registers a PassObserver that journals every open /
// close / abort / recover. Constant-time chan send.
func (j *Journal) AttachPasses(reg *runner.PassRegistry)

// handlePass routes the event to UpsertPass. State, CloseReason,
// OpenedAt/ClosedAt timestamps come from the PassEvent payload;
// the journal does not call back into the registry.
func (j *Journal) handlePass(ctx context.Context, e runner.PassEvent)

// jKindPass is critical-priority on the journal channel per F-11:
// enqueue for this kind BLOCKS INDEFINITELY rather than dropping
// after the 100ms budget. Invariant 1 demands no lost pass
// transitions; other event kinds keep the drop semantics.
const jKindPass = "pass"
```

---

## `engine/replay.go` — passes load + recovery call

```go
// ReplayTargets gains an optional Passes field. When nil, the
// passes step is skipped (for tests that don't exercise the path).
//
// F-8: All four production callers (cmd/ghyll/session_engine.go,
// cmd/ghyll/engine_cmd.go cmdEngineReplay, cmd/ghyll/arrow_cmd.go,
// and engine/replay_test.go fixtures) MUST be updated to pass a
// non-nil Passes when the workflow needs pass state. Implementer
// note: a zero-value ReplayTargets{} compiles today and would
// silently skip the new step — review every literal call site.
type ReplayTargets struct {
    Findings        *runner.FindingsStore
    Classifications *runner.ClassificationsStore
    Grid            *runner.Grid
    Amendments      *runner.AmendmentQueue
    Attestations    *runner.AttestationStore
    Passes          *runner.PassRegistry // NEW
}

// ReplayCounts gains pass counters. Recovery counters live in
// RecoveryReport (see engine/recovery.go).
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
// reconcile → start journal. Recovery REFUSES TO RUN if
// ReplayCounts.Errors is non-empty (F-13).
```

---

## `engine/recovery.go` — new file

```go
// RecoveryDeps bundles every dependency Recovery needs. Distinct
// from ReplayTargets so the Recovery signature carries its own
// surface (F-9).
type RecoveryDeps struct {
    Store        *Store
    Passes       *runner.PassRegistry             // for Resume (F-3)
    Attestations *runner.AttestationStore         // for JOIN-based detection (F-1)
    LockTable    *runner.RoleContextLockTable     // for Resume's lock re-acquire
    IBTracker    *runner.InsufficientBasisTracker // optional; may reset counters
    JSONLPath    string                           // for re-truncation per F-6
    Now          func() time.Time                 // injection for F-12 idempotence
}

// RecoveryReport summarizes one Recovery invocation. Counts are
// for telemetry; Events is the audit trail (one entry per
// reconciliation action). session.Open surfaces these to the
// operator on chat-loop startup; Recovery does NOT publish to
// the OperatorBus (F-18 — bus has no subscribers at recovery
// time).
type RecoveryReport struct {
    OrphansAborted        int
    OrphansPreserved      int
    AttestationsReplayed  int
    EvaluationRunsFlipped int
    JSONLTruncatedSkipped int      // bytes-after-last-complete-line skipped (F-6)
    Events                []runner.OperatorEvent
}

// Recovery scans the engine + JSONL state at session start,
// reconciles split-brain conditions, and returns a
// RecoveryReport whose Events list session.Open surfaces.
//
// Refuses to run if replayCounts.Errors is non-empty (F-13):
// returns ErrRecoveryReplayDirty so the operator gets a clear
// "previous start left malformed rows; investigate before
// restart" message.
//
// Wraps its five-step scan in ONE BeginTx/Commit so concurrent
// read-only callers see pre- or post-recovery atomically (F-10).
//
// MUST be called AFTER engine.Replay and BEFORE the live Journal
// observers attach (the journal would re-journal Recovery's
// writes back into the engine, doubling rows).
//
// Idempotent per F-12: passes with recovered_at != '' are
// skipped; re-invocations return an empty RecoveryReport.
func Recovery(
    ctx context.Context,
    deps RecoveryDeps,
    replayCounts ReplayCounts,
) (RecoveryReport, error)

var ErrRecoveryReplayDirty = errors.New(
    "recovery: ReplayCounts.Errors non-empty; refuse to proceed")

// Internal recoveryRun struct (N-9):
//
//   type recoveryRun struct {
//       tx     *sql.Tx                          // single-transaction wrap
//       deps   RecoveryDeps
//       report RecoveryReport
//   }
//
// Methods (each scans inside the transaction):
//
//   orphanScan        — Store.ListPasses(tx, {State: "open"}). Returns
//                       []PassRecord. (Live in-process passes don't
//                       exist yet at recovery time.)
//   attestationPendingScan(orphans) — per-orphan, run the JOIN from
//                       ADR-015 Part E. For each pending row, call
//                       Passes.Resume(rec, deps.LockTable); append
//                       PassEventRecover via the resumed pass; stamp
//                       recovered_at via Store.UpsertPass; emit
//                       recovery-attestation-republished in
//                       report.Events with hint payload from the run row.
//   orphanAbort(remaining) — UPDATE passes SET state='aborted',
//                       closed_at=deps.Now(), close_reason='crash'.
//                       Emit recovery-pass-aborted-crash per pass.
//   evaluationRunReconcile — SELECT runs with end_status='running'
//                       AND depth_type_attestation_ref != ''.
//                       For each, Lookup the verdict in
//                       deps.Attestations. If found, call
//                       Store.UpdateEvaluationRunReconciled with
//                       the mapped ClauseStatus (per ADR-015 Part D
//                       table). Emit recovery-attestation-replay
//                       per flip.
//   loadJSONLTruncation — if loadFromJSONL reported truncated=true
//                       at session start (passed in via deps),
//                       emit recovery-jsonl-truncated and record
//                       JSONLTruncatedSkipped.
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
// Record returns ErrAttestationAuditWriteFailed and the in-memory
// map is unchanged; downstream observers (engine journal, tree
// writer) never fire.
//
// Implementation: the JSONL writer is the FIRST observer fired,
// inline within Record's critical section. Other observers'
// signature stays unchanged (no error return per N-5).
func (s *AttestationStore) Record(rec AttestationRecord) error

var ErrAttestationAuditWriteFailed = errors.New(
    "attestation: JSONL audit write failed; record not accepted")

// loadFromJSONL is the replay entry point per ADR-015 Part C.
// Reads the JSONL file at session start; for every complete
// record, calls Record (which fires all observers including the
// engine journal — but the journal isn't attached yet, so this
// just rebuilds the in-memory store).
//
// Returns:
//   loaded     — count of valid records loaded.
//   truncated  — true if the file ended with a partial line; the
//                load stopped at the last complete record.
//   err        — non-nil only on unrecoverable cases (file
//                permission, corrupt header, JSON parse on a
//                NON-trailing line). See ADR-015 Part C for the
//                full case table:
//                  missing+engine-empty → (0, false, nil)
//                  missing+engine-has-rows → ErrAttestationAuditLost
//                  unreadable → ErrAttestationAuditLost
//                  trailing truncation → (N, true, nil)
//                  mid-file JSON parse error → ErrAttestationAuditLost
//
// On truncated=true, session.Open emits
// OpEventAttestationAuditDurabilityFailed with offset detail,
// and the next AttestationJSONLWriter.Record truncates the
// file at the last complete offset before appending.
func (s *AttestationStore) loadFromJSONL(path string, engineHasRows bool) (loaded int, truncated bool, err error)

var ErrAttestationAuditLost = errors.New(
    "attestation: JSONL audit trail missing or unreadable; restore or rebuild --force")
```

The `AttestationJSONLWriter` gains a `TruncateAt(offset int64)`
method that the post-recovery first-Record path calls to
overwrite the bad bytes:

```go
// TruncateAt rewinds the file to offset and seeks the write
// position there. Called by session.Open once after recovery
// surfaces a truncated-trailing-line signal. The next Write
// overwrites the partial line; new records append cleanly.
func (w *AttestationJSONLWriter) TruncateAt(offset int64) error
```

---

## `cmd/ghyll/session_engine.go` — orchestration

The existing `engineRuntime` gains an `attestationJSONLPath` field
+ a `Passes *runner.PassRegistry` field + a `recoveryReport`
field for surfacing events post-Open. Open sequencing changes:

```go
// engineRuntime.Open changes (sketch — implementer writes the body):
//
//   1. open sqlite store (ensureSchemaVersion runs v2→v3 ALTER if needed)
//   2. construct in-memory stores (Findings, Classifications, Grid,
//      Amendments, Attestations, Passes — Passes is NEW)
//   3. engineHasAttestations := store.CountAttestations(ctx) > 0
//      loaded, truncated, err := attestations.loadFromJSONL(
//          attestationJSONLPath, engineHasAttestations)   [F-5, F-6]
//      if err != nil → return err (e.g., ErrAttestationAuditLost)
//      record truncated for the recovery report
//   4. replayCounts, err := engine.Replay(store, targets)
//   5. if err != nil → return err
//   6. report, err := engine.Recovery(ctx, RecoveryDeps{
//          Store: store, Passes: passes, Attestations: attestations,
//          LockTable: lockTable, IBTracker: ibTracker,
//          JSONLPath: attestationJSONLPath, Now: time.Now,
//      }, replayCounts)
//      if errors.Is(err, ErrRecoveryReplayDirty) → surface to operator
//   7. if truncated: jsonlWriter.TruncateAt(lastCompleteOffset)
//   8. attachJournal — including AttachPasses        [observers go
//                                                     live AFTER 6]
//   9. r.recoveryReport = report  (chat-loop drains on first iteration)
//
// Between steps 4 and 8, the journal is NOT attached. Recovery
// writes go DIRECTLY to the store (Store.UpsertPass,
// Store.UpdateEvaluationRunReconciled — F-2); these don't go
// through the runner-layer observers and so don't re-journal.
// The in-memory AttestationStore is repopulated by loadFromJSONL
// at step 3; the in-memory PassRegistry is repopulated by
// PassRegistry.Resume calls inside Recovery for preserved passes.
//
// engineRuntime.RecoveryReport() returns the captured report so
// the chat-loop's first iteration can render a banner:
//   "Recovery: N orphans aborted (crash), M preserved (awaiting
//    attestation), K runs reconciled from JSONL."
```

CLI ergonomics changes (F-14):

```go
// cmd/ghyll/engine_cmd.go cmdEngineReplay output extends:
//   ... existing replay counters ...
//   passes:  Open=N Closed=M Aborted=K
//   note: this is replay-only; session start additionally runs
//   Recovery. Use `ghyll engine recover --dry-run` to preview.

// NEW subcommand: ghyll engine recover --dry-run
//
// Opens the store read/write, runs Recovery inside a BeginTx
// that calls Rollback at the end. Prints what Recovery WOULD
// do without committing.
func cmdEngineRecover(ctx context.Context, opts engineRecoverOpts) error
```

---

## New OperatorEvent kinds

Per pass-persistence.md F-2, F-4, F-5 and ADR-015 Part D:

```go
const (
    OpEventRecoveryPassAbortedCrash       OperatorEventKind = "recovery-pass-aborted-crash"
    OpEventRecoveryAttestationRepublished OperatorEventKind = "recovery-attestation-republished"
    OpEventRecoveryAttestationReplay      OperatorEventKind = "recovery-attestation-replay"
    OpEventRecoveryJSONLTruncated         OperatorEventKind = "recovery-jsonl-truncated"
)
```

(`OpEventRecoveryTornRowRollback` was originally listed; dropped
per F-15 since the torn-row invariant is gone. The
`-jsonl-truncated` event replaces it for the JSONL-trailing-line
case.)

These join the existing `OpEvent*` constants in
`runner/operatorbus.go`. **Recovery does not publish them to the
bus** (F-18); they are emitted into `RecoveryReport.Events` and
session.Open surfaces them to the operator on chat-loop startup.

---

## Enforcement map (invariants → enforcement points)

| Invariant (pass-persistence.md) | Enforcement |
|---|---|
| 1. Pass state persisted on every transition | `Pass.closeWith` captures payload under `p.mu`, releases mutex + lock token, then calls `PassRegistry.emit` with no locks held (F-4). `Journal.enqueue` for `jKindPass` blocks indefinitely rather than dropping (F-11). |
| 2. JSONL is source of truth for attestations | `AttestationStore.Record` returns `ErrAttestationAuditWriteFailed` if JSONL write fails (JSONL writer is first observer fired inline); `attestations.loadFromJSONL` runs before `engine.Replay` rebuilds the engine cache. |
| 3. Crash recovery is deterministic + idempotent | `Recovery` is a pure function of `(deps.Store, JSONL, deps.Now)`. `recovered_at` is set-once; passes with `recovered_at != ''` skip the preserve-or-abort scan (F-12). Re-invocation produces empty `RecoveryReport`. |
| 4. Attestation-pending passes survive recovery | `recoveryRun.attestationPendingScan` JOINs evaluation_runs ⋈ attestations (F-1) to find pending. Preserved set keeps `state=open`, gets `recovered_at` stamp, AND `PassRegistry.Resume` rebuilds the in-memory `*Pass` + re-acquires the lock token (F-3). |
| 5. Other open passes → aborted:crash | `recoveryRun.orphanAbort` runs UPDATE inside the single Recovery transaction (F-10); emits `OpEventRecoveryPassAbortedCrash` into `RecoveryReport.Events` (F-18). |
| 6. ~~Torn checkpoint records detected~~ | **Dropped** per F-15. Sqlite WAL row-level atomicity covers engine writes; JSONL trailing truncation is handled leniently via `loadFromJSONL` returning `truncated=true` (F-6). |
| 7. Query historical pass is a read, not reconstruction | `Store.GetPass(ctx, id) (PassRecord, bool, error)` SELECTs the row; returns `not-found` cleanly (F-16). |

---

## Spec gap escalations (all resolved at gate 1)

1. ~~Attestation-request persistence (A-3)~~ — resolved via
   JOIN-based detection over `evaluation_runs` × `attestations`
   (F-1). The volatile `OperatorBus` is removed as a detection
   surface; the persistent `depth_type_attestation_ref` column
   suffices.
2. ~~Torn-row mechanism for invariant 6~~ — invariant dropped
   (F-15). Sqlite WAL row-level atomicity covers engine writes;
   JSONL trailing truncation is handled leniently.
3. ~~Recovery vs. attestation-flow ordering at replay~~ — JSONL
   loads first (step 3), Replay rebuilds the engine cache (step
   4), Recovery runs in one transaction (step 6). Recovery
   refuses to run on dirty replay (F-13).

---

## Required unit tests (N-10)

The implementer ships:

- `TestRecovery_NoOpenPasses` — empty restart; expect zero-count
  `RecoveryReport`, no events.
- `TestRecovery_OrphanAbort` — one orphan with no
  attestation-pending clauses; expect 1 abort, 1 event.
- `TestRecovery_AttestationPendingPreserved` — one orphan with a
  matching JOIN row; expect 1 preservation, `Resume` called once,
  lock token re-acquired, 1 event.
- `TestRecovery_EvaluationRunReconcile` — clause `end_status=running`
  with matching JSONL verdict; expect status flip + provenance
  column set.
- `TestRecovery_Idempotent` — call Recovery twice; expect first
  call mutates, second call returns empty report.
- `TestRecovery_JSONLMissingFreshProject` — no JSONL + zero
  attestations in engine; expect `loaded=0, truncated=false,
  err=nil`.
- `TestRecovery_JSONLMissingWithRows` — no JSONL but engine has
  attestation rows; expect `ErrAttestationAuditLost`.
- `TestRecovery_JSONLTrailingTruncated` — JSONL with partial last
  line; expect `truncated=true`, event emitted, writer truncates
  on next Record.
- `TestRecovery_ReplayCountsErrorsRefuses` — non-empty
  `replayCounts.Errors`; expect `ErrRecoveryReplayDirty`.
- `TestRecovery_SingleTransactionAtomicity` — concurrent reader
  during Recovery sees pre- or post-state, never mid.
- `TestPass_ClosePostLockReleaseEmit` — verify the emit-after-
  release pattern; race detector flags any cycle.
- `TestPass_ResumeRebuildsRegistry` — engine row + Resume call →
  registry contains the *Pass, lock token held.

---

## Handoff to implementer

Gate 1 adversary review complete; 18 findings remediated
in-document (see `specs/v2/validation-impl-pass-tier1-remediation.md`).
Recommended implementation order:

1. Schema + `PassRecord` + `UpsertPass` + `evaluation_runs.recovery_source` migration + `schemaVersion=3`.
2. `PassRegistry.Observe` + `emit` + `Resume` + lock-order fix in `Pass.closeWith`; remove `OpEventPassOpened`/`OpEventPassClosed` from `OpenPass`/`closeWith` per N-1/N-2.
3. `Journal.AttachPasses` + `handlePass` + `jKindPass` priority enqueue.
4. `Store.GetPass` + `ListPasses` + `UpdateEvaluationRunReconciled`.
5. `AttestationStore.loadFromJSONL` + `AttestationJSONLWriter.TruncateAt`; lift JSONL-writer-first-observer invariant + `ErrAttestationAuditWriteFailed`.
6. `engine/recovery.go` — the big one.
7. `session_engine.go` orchestration changes.
8. `engine_cmd.go` CLI updates + new `engine recover --dry-run` subcommand.
9. Update `runner/projectstatus.go:60-65` comment (N-3) — "passes are persisted across restart" replaces the old "gone on restart" claim.
10. BDD step bindings for the 7 deferred scenarios (retire "Crash mid checkpoint-log write" from `state-machine.feature`).
11. Unit tests per the list above.

If any contract surface here conflicts with reality during
implementation, escalate via `specs/escalations/`. Do not
silently diverge.
