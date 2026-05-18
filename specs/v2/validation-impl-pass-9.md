# Validation pass 9 — adversarial review of phase 9 work

Cold-context adversarial pass on the v2 persistence engine
(engine package + vault v2 endpoints + runner observer hooks).
Three parallel adversaries, 45 findings total.

**Severity distribution:** 2 Critical, 12 High, 19 Medium, 12 Low.

**Per user direction:** fix all findings, no deferrals.

Adversary numbering preserved: `En` = engine store + queries
adversary; `Jn` = journal + replay adversary; `Vn` = vault v2
adversary.

---

## Critical

### J1 — Replay silently truncates findings + amendments at 1000 rows
`engine/replay.go:128/158/173` + `engine/queries.go:55`. Replay
passes `Limit: 1000000` but `normalizePaging` caps at 1000. A
long-running session with >1000 findings or >1000 drained
amendments restarts with only 1000 of them in memory; the rest live
on disk but the runner doesn't know. Subsequent Raise of a
"persisted but not replayed" ID succeeds in memory and journals an
UPSERT that overwrites the persistent row — silent state divergence.
For amendments, F44 dedup breaks: a drained `am-foo` can re-enqueue.

**Fix:** add internal `iterFindings` / `iterAmendments` paging
helpers that bypass the 1000 cap; replay loops until exhausted.

### J2 — `AmendmentEventReset` wipes legitimately-drained rows, breaking F44 dedup across restart
`engine/journal.go:250-256` + `runner/amendment.go:261-268`. Reset is
documented as "in-memory dedup reset" — but the journal
unconditionally `UPDATE amendments SET drained_at = NULL`. After
Reset + restart, replay sees zero drained amendments → `LoadDrained`
not called → dedup gone.

**Fix:** Reset is in-memory-only. Journal does NOT touch sqlite on
the Reset event (or writes an audit-log entry, not a wipe).

---

## High

### E1 — `PRAGMA foreign_keys` is per-connection; pool checkout silently disables FK
`engine/store.go:48-64`. modernc.org/sqlite pools connections; every
new pool conn starts with `foreign_keys = OFF`. `DeleteFinding`'s FK
cascade silently breaks under multi-connection load.

**Fix:** open with DSN `?_pragma=foreign_keys(1)&_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)`. Also covers E15.

### E2 — `UpsertFinding` and friends accept invalid severity / status / type / IDs
`engine/records.go`. The runner's `FindingsStore.Raise` validates
severity 0..4, type pattern, status enum, non-empty ID. The engine
write API doesn't — operator code bypassing the runner can corrupt
persisted state.

**Fix:** `validateFinding` / `validateRequirement` /
`validateClassification` at writer boundaries. Reject with typed
sentinel errors.

### E3 — Upsert has no monotonic store_version guard; out-of-order events overwrite newer state with older
`engine/records.go`. `ON CONFLICT(id) DO UPDATE SET …` is
unconditional. A retried/delayed event with older store_version
overwrites a newer one.

**Fix:** add `WHERE excluded.store_version > findings.store_version`
to each upsert's `DO UPDATE`. Mirror for classifications, amendments.

### E4 — JSON blob columns accept any string; corrupt JSON crashes replay
`engine/records.go:221-255, 257-289, 291-334`. ClausesJSON /
RequirementsJSON / ContextsJSON / FindingIDsJSON / ResultJSON
written without `json.Valid` check. Replay errors with cryptic
messages.

**Fix:** validate `json.Valid` at write boundary in each insert/upsert
that takes JSON-typed fields. Typed sentinel `ErrEngineInvalidJSON`.

### J3 — Transition audit row uses `e.After.RaisedAt`, not the transition timestamp
`engine/journal.go:88-96`. Every `finding_transitions.at` is stamped
with the original RAISE time, not the transition time. Audit log is
unusable.

**Fix:** use `time.Now()` (or the journal's clock — see J11) for
the transition `at` field.

### J4 — `TransitionRecord.Reason` hardcoded to empty; `TransitionWithReason` audit data silently lost
`engine/journal.go:93` + `runner/findings.go:249-291`. The reason +
role passed to `TransitionWithReason` get stuffed into the finding's
Description, not the audit log's dedicated columns.

**Fix:** extend `FindingsEvent` with `Role` + `Reason` fields
populated from `transitionImpl`. Journal carries them through to
`finding_transitions`.

### J5 — Journal does synchronous sqlite writes under the FindingsStore write lock
`engine/journal.go`. Observers fire under the runner's `s.mu` write
lock; each handler does a 5s-timeout sqlite write synchronously. A
slow disk stalls every concurrent runner mutation for up to 5s.

**Fix:** single consumer goroutine + bounded channel. Observer
pushes onto the chan and returns immediately; consumer drains and
writes off the hot path. Bounded chan; drop-newest with metric.

### J6 — `Journal.Close` cancels ctx but doesn't detach observers; subsequent mutations silently lose persistence
`engine/journal.go:34/47/59-61`. After Close, observers still fire
synchronously but every write sees the cancelled ctx → instant
timeout. The runner thinks the journal is attached; persistence is
broken silently.

**Fix:** stop dispatching to the journal's handler after Close.
Either expose an `Unregister` on the runner-side stores OR have the
journal short-circuit and log a one-shot "journal closed; events
dropped" warning.

### J7 — Drain stamps drained_at per-row outside a transaction
`engine/journal.go:233-248`. A 10-row drain split across rows means a
concurrent `ListAmendments(drained=true)` reader can see a partial
drain set.

**Fix:** `BeginTx` around the drain loop; `Commit` after the last
row.

### V1 — `AmendmentRecord.DrainedAt` ships `sql.NullString` as `{"String":"","Valid":false}`
`engine/records.go:268`. Every consumer of `/v2/amendments` must
special-case the shape; filter `drained=true/false` can't be
matched back against the response.

**Fix:** Marshal `*string` (or use `omitempty`) so the wire is a
plain string or `null`. Add wire-shape test.

### V2 — `AttachEngine` called twice panics the process
`vault/v2_endpoints.go:26-35`. `mux.HandleFunc` panics on duplicate
registration. Any reload or test-rebuild that calls AttachEngine
twice crashes the server.

**Fix:** track `s.engineAttached`; second call returns an error
(or no-op with log).

### V3 — `writeServerError` leaks sqlite error text to clients
`vault/v2_endpoints.go:75-77`. `err.Error()` includes wrapped sqlite
detail (schema names, file paths). Clients learn the on-disk shape.

**Fix:** log the wrapped error server-side; return generic
`"internal error"` body. Optional `?debug=1` for privileged tokens.

---

## Medium

### E5 — Error messages embed unbounded operator-supplied IDs (information disclosure)
`engine/records.go` various sites. ID strings can be 64KB of
crafted text including terminal escape sequences.

**Fix:** `safeID(id)` helper that trims to ~64 chars and strips
control bytes before formatting into error messages.

### E6 — `DeleteRequirement` / `DeleteArrow` transaction error handling loses information
`engine/records.go:178-194/199-215`. Deferred Rollback silently
swallows rollback errors; multi-table delete failures don't say
which step failed.

**Fix:** capture rollback error via `errors.Join`. Include step
index or SQL fragment in the wrapped error.

### E7 — `MinSeverity == 0` is a no-op filter that pretends to filter
`engine/queries.go:36-39`. Conflates "no floor" with "every row."
Operator confusion.

**Fix:** use `*int` (nil = no floor) OR document `<= 0` as
"no floor" — pick one and validate.

### E8 — `ListFindings` ORDER BY without a covering index; deep offsets are O(N)
`engine/store.go:100-102`. Composite ORDER BY uses single-column
indexes; sort is in-memory.

**Fix:** add composite index
`CREATE INDEX idx_findings_sort ON findings(arrow_id, severity DESC, id);`.
Document the cost.

### E9 — `TestListFindings_DefaultLimit` uses 26x26 collision-prone IDs
`engine/store_test.go:246-260`. Test inserts 200 rows from a
676-cell namespace; future test growth would silently collide.

**Fix:** use `fmt.Sprintf("F%05d", i)`.

### E10 — `engine/store_test.go` test coverage gaps
Multiple. No race-detector test with concurrent writers, no JSON
invalid write-path test, no DeleteArrow rollback test, no NullString
round-trip, no MinSeverity=0 edge.

**Fix:** add the missing tests alongside the structural fixes (E1
concurrent FK pragma, E4 invalid JSON, E6 rollback).

### J8 — EvaluationRun observer encodes `run.Result` without snapshot; races with downstream consumers
`engine/journal.go:271-298`. Same class as F37 — observer shares the
Result pointer with downstream consumers; either side can mutate.

**Fix:** deep-copy Result.Details before MustJSON (or fire observers
on a deep-copied EvaluationRun).

### J9 — Replay aborts mid-flight on any single malformed row
`engine/replay.go`. One unknown finding status aborts ALL subsequent
replay (incl. amendments).

**Fix:** accumulate errors per phase; return `ReplayCounts + []error`
so the operator sees full damage. Continue past per-row errors.

### J10 — Findings replay ordered by `(arrow_id, severity DESC, id)`, not raise-time
`engine/replay.go:128`. The in-memory slice order post-replay differs
from pre-restart order.

**Fix:** replay findings ordered by `raised_at ASC, id ASC` so raise
sequence is preserved.

### J11 — Journal stamps `time.Now()` for DeclaredAt / DrainedAt; ignores runner's clock
`engine/journal.go` various. Tests with mocked runner clock see
divergent engine timestamps.

**Fix:** inject `clock func() time.Time` into `NewJournal`. Default
to `time.Now`; tests can share the runner's fake clock.

### J12 — `MustJSON` returns `"{}"` for slice marshal errors; replay then errors on type mismatch
`engine/records.go:360-366`. Empty object can't unmarshal into a
slice type; the failed marshal becomes a replay-time crash.

**Fix:** rename to `mustJSON(v, fallback)`; callers pick `"[]"` for
slices, `"{}"` for maps. Or remove the fallback and propagate errors.

### J13 — Replay's split queries for pending vs drained amendments race with concurrent writes
`engine/replay.go:154-180`. Two queries can interleave with a journal
mutation; the same ID could appear in both result sets.

**Fix:** single query returning all amendments with their drained_at
state; classify in-process.

### J14 — `classifications.overwrite_count` always written as 0
`engine/journal.go:153-172`. The column is dead weight — audit table
has the per-overwrite history but operators can't `WHERE overwrite_count > 3`.

**Fix:** `SET overwrite_count = overwrite_count + 1` in the
conflict-update path.

### V4 — `queryInt` collapses parse error / negative / zero into "default"
`vault/v2_endpoints.go:39-49`. `limit=abc` and `limit=0` both become
default 100; operator intent lost.

**Fix:** return `400 bad request` on non-empty, non-parseable values.

### V5 — `queryBool` silently treats `"yes"` / `"True"` / `"FALSE"` as "either"
`vault/v2_endpoints.go:54-65`. Case-sensitive and limited token set.

**Fix:** `strings.EqualFold` for the four tokens; `400 bad request`
on anything else non-empty.

### V6 — uint64 fields lose precision in JavaScript clients
`engine/records.go`. JSON.parse coerces to Number (IEEE-754 double);
values past 2^53 round.

**Fix:** `json:",string"` tag on GridVersion / StoreVersion so
they ship as strings.

### V7 — `AttachEngine` writes `s.engine` without synchronization
`vault/v2_endpoints.go:27`. Pointer write visible to request handlers
via unsynchronized read.

**Fix:** either document "call once before serving" + panic on
second call (subsumed by V2 fix), or guard with `atomic.Pointer`.

### V8 — `s.engine == nil` check after method check is dead in correct usage
`vault/v2_endpoints.go:81-87`. Route only registered after
AttachEngine. The nil-check path is unreachable.

**Fix:** remove the nil check (post V2 fix the registration is
gated).

### V9 — `handleClassifications` reuses `RequirementFilter`; future extensions silently miss the endpoint
`vault/v2_endpoints.go:213`. Coupling-by-coincidence.

**Fix:** introduce `engine.ClassificationFilter` even if today it
has the same fields. Decouple the wire surfaces.

---

## Low

### E11 — `joinPlaceholders` is dead code kept alive by a discard assignment
`engine/store.go:206-211` + `engine/queries.go:401`.

**Fix:** delete it; if batch-insert is on the roadmap, leave a TODO.

### E12 — `created_at` / `updated_at` are TEXT; format drift breaks pagination
`engine/store.go`. RFC3339 lex-sort works but isn't enforced.

**Fix:** document the format expectation in the schema comment;
validate `time.Parse(time.RFC3339, ts)` at writer.

### E13 — Duplicate detection uses string-match on driver error message
`engine/records.go:248-251`. Fragile if modernc.org/sqlite changes
its error format.

**Fix:** test SQLite extended error code via the driver's interface
or do a pre-check `SELECT 1 FROM grid_arrows WHERE id=? AND grid_version=?`.

### E14 — `MustJSON` swallows marshal errors silently
Subsumed by J12 fix.

### E15 — No WAL mode / no busy_timeout
Subsumed by E1 DSN fix.

### J15 — Test coverage gaps for slow disk / panic recovery / Reset+replay
`engine/journal_test.go` + `engine/replay_test.go`.

**Fix:** add tests for the structural fixes (J5 goroutine handoff,
J6 Close behavior, J2 Reset+replay round-trip).

### V10 — Bearer scheme comparison case-sensitive + non-constant-time
`vault/server.go:38-44`.

**Fix:** parse Authorization header; `strings.EqualFold(scheme, "bearer")`;
`subtle.ConstantTimeCompare`.

### V11 — No length cap on query-param values; finding_id of 1MB consumes resources
`vault/v2_endpoints.go`.

**Fix:** reject any single query-param > 256 chars with 400.

### V12 — `limit > 1000` silently clamps; operator never learns the cap
Subsumed by V4 fix — out-of-range `limit` becomes 400.

### V13 — `handleArrows` documentation says queryInt clamps negative; it doesn't
`vault/v2_endpoints.go:162-165`.

**Fix:** make queryInt clamp negatives consistently, OR delete the
local guard and document.

### V14 — `writeJSON` discards Encode error; partial JSON can reach client
`vault/v2_endpoints.go:68-71`.

**Fix:** log Encode errors at warn level with request context.

### V15 — `handleTransitions` has no offset paging
`vault/v2_endpoints.go:106-126`. Transitions past first 1000 are
unreachable.

**Fix:** add offset arg to `ListTransitions`; surface on the
handler.

---

## Highest-risk areas

1. **Data loss at restart (J1, J2, J9)** — replay truncation, Reset wiping dedup, abort-on-first-error. Three independent ways to lose state across a restart. The persistence layer's PURPOSE is to survive restart; these are existential bugs.
2. **Audit-log fidelity (J3, J4)** — every transition's timestamp + reason is lost. The audit table is decorative.
3. **Observer-under-lock concurrency (J5)** — synchronous 5s writes under the runner's write lock. Disk pressure stalls the runtime.
4. **Wire-shape contract (V1, V6)** — JSON consumers get `{"String":"","Valid":false}` for nullable timestamps and lose precision on uint64. Every v2 client must implement a custom decoder.

## Remediation plan

No deferrals. Order chosen to minimize churn:

1. DSN pragmas (E1 + E15): WAL + busy_timeout + foreign_keys at open.
2. Writer validation (E2 + E4): validate severity / status / type / JSON shape at every write boundary. Typed sentinel errors.
3. Newer-wins upsert (E3): `WHERE excluded.store_version > store_version` on conflict update.
4. FindingsEvent carries Role + Reason (J4): runner change to populate from transitionImpl; journal carries through.
5. Journal goroutine-handoff (J5 + J6 + J7): single consumer + bounded chan; transactional drain; explicit detach on Close.
6. Reset semantics (J2): journal no-op on AmendmentEventReset (no SQL change); document Reset as in-memory only.
7. Replay paging (J1): internal iterFindings / iterAmendments helpers; replay loops to exhaustion.
8. Replay error accumulation (J9): per-phase error slice; continue past malformed rows.
9. Replay ordering (J10): findings ORDER BY raised_at.
10. Classification overwrite_count increments (J14): conflict-update path.
11. Journal clock injection (J11): `NewJournalWithClock`.
12. MustJSON typed fallback (J12 + E14): per-call fallback or remove silent fallback.
13. Engine reader fixes (E5, E6, E7, E8, J13): error sanitization, transaction error joining, MinSeverity semantics, composite index.
14. Vault endpoints (V1-V15): AttachEngine idempotency, queryInt/queryBool strict, writeServerError sanitize, wire-shape fixes, auth constant-time + case-insensitive, request body caps, paging on transitions.
15. Test coverage adds (E9, E10, J15) covering each structural fix.
