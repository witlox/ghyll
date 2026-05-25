# ADR-v4-008: Engine schema migrations via explicit ALTER TABLE

**Status:** Accepted (2026-05-25)

**Context:**

The v4 diamond adds two engine-persistence surfaces:

1. `passes.remediation_outcome` + `passes.remediation_rounds_used`
   columns for the adversarial cycle's per-pass summary.
2. A new `arrow_invalidations` table for the `/invalidate-arrow`
   operator escape verb.

`engine/store.go` uses `CREATE TABLE IF NOT EXISTS` for table
construction. Pre-existing databases (operators upgrading across
v4) do NOT pick up new columns automatically — `IF NOT EXISTS`
won't add columns to an already-existing table.

R8 of the v2 adversarial flagged the migration path was unflagged;
R28 added the `arrow_invalidations` table to the same migration
scope.

**Decision:**

Both schema additions ship as explicit ALTER TABLE migrations
called from `engine.OpenStore` after the `CREATE TABLE IF NOT
EXISTS` block:

- `migrateAddRemediationColumns(db *sql.DB) error`: PRAGMA
  `table_info(passes)` to detect column existence; on miss, run
  `ALTER TABLE passes ADD COLUMN remediation_outcome TEXT NULL`
  and `ALTER TABLE passes ADD COLUMN remediation_rounds_used
  INTEGER NULL`. Idempotent (safe to call on fresh DBs).
- `migrateAddArrowInvalidations(db *sql.DB) error`: PRAGMA
  `table_list` detection; on miss, `CREATE TABLE arrow_invalidations
  (arrow_id TEXT PRIMARY KEY, op_id TEXT NOT NULL, reason TEXT,
  invalidated_at TIMESTAMP, grid_version INTEGER)`.

Backfill: pre-migration `passes` rows have NULL in both new
columns; the cycle never ran for those passes so NULL is correct.

**Consequences:**

- A fresh session against a pre-v4 `engine.db` migrates on first
  open with no operator action.
- `JSONL` audit records remain the authoritative attestation log
  per ADR-015; the `passes` table extension is a search shortcut
  only. The cycle's full report is logged as a JSONL record of kind
  `adversarial-cycle-report`.
- The migration is the only schema-mutating path on open; a future
  v5 adds new tables / columns the same way.
- The `arrow_invalidations` table is read at Replay to populate
  `runner.Grid.Invalidations`; `/run-arrow` consults this map and
  forces re-traversal even if the cached arrow status is
  `complete`.

**Producer:**

The `/invalidate-arrow <arrow-id> [--reason <text>]` slash command
(implemented at `cmd/ghyll/invalidate_arrow_cmd.go`) is the
operator-facing producer for `OpEventArrowInvalidated`. The handler:

1. Refuses without an active `/op-id` (the row carries operator
   identity).
2. Refuses on unknown arrow-id (looked up via the live `Grid`).
3. Publishes `OpEventArrowInvalidated` with the typed Payload per
   ADR-v4-005: `arrow_id`, `op_id`, `reason`, `source=operator`.

The subscriber wired in `cmd/ghyll/session_engine.go:attachJournal`
fans out the publish to `engine.Store.InsertArrowInvalidation`
synchronously, so by the time the operator sees the confirmation
line the row is on disk. Integrator-pass I-C-1 (2026-05-25) closed
the original "consumer chain without producer" gap; the table now
receives rows from operator input, not only from direct-test
publishes.
