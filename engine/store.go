// Package engine is the v2 persistence layer. It persists the
// runner-layer entities (findings, classifications, grid arrows,
// amendments, evaluation runs) into a sqlite database and offers
// structured-query reader endpoints for the vault server.
//
// Per the architect's phase-5 preflight verdict:
//   - One implementation; no FindingsLedger interface extraction.
//   - DeriveArrowStatus is pure — the engine does NOT cache
//     ArrowStatus. Status is always re-derived from persisted inputs.
//   - The runner exposes Observer hooks (FindingsStore.Observe,
//     ClassificationsStore.Observe, Grid.Observe) and the engine
//     subscribes to them. Mutations journal under the runner's
//     write lock.
//
// Tables are append-mostly (transitions / overwrites are pure
// audit logs; findings / classifications / arrows have an
// upsert-on-update pattern so the current state is queryable
// without replaying the whole log).
package engine

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// Engine errors.
var (
	ErrEngineClosed    = errors.New("engine: store closed")
	ErrEngineDuplicate = errors.New("engine: duplicate primary key")
)

// Store is the sqlite-backed v2 entity store.
type Store struct {
	db *sql.DB
}

// OpenStore opens or creates a sqlite store at path. Schema is
// created on first open; subsequent opens are idempotent.
//
// Concurrency: validation-pass-9 E1 + E15. PRAGMAs are passed via
// the DSN so every pooled connection has them applied (per-Exec
// PRAGMAs only affect one connection, which under load silently
// disables FK cascades). WAL mode + busy_timeout reduce reader
// contention under concurrent writes.
//
// Validation-pass-10 C7: a schema_version row in `engine_meta` is
// upserted to the current schemaVersion; an existing higher value
// (future binary wrote this DB) causes Open to fail with a typed
// error so the operator gets a clean upgrade message rather than
// internal sqlite column names.
func OpenStore(path string) (*Store, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)",
		path,
	)
	return openStoreDSN(dsn, false)
}

// OpenStoreReadOnly opens an existing sqlite store WITHOUT running
// the schema DDL (validation-pass-10 W5). Used by `ghyll engine
// status` and `ghyll engine replay` so a CLI invocation against a
// live session does not race CREATE TABLE / CREATE INDEX against
// concurrent writes. Returns an error if path does not exist.
func OpenStoreReadOnly(path string) (*Store, error) {
	dsn := fmt.Sprintf(
		"file:%s?mode=ro&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
		path,
	)
	return openStoreDSN(dsn, true)
}

func openStoreDSN(dsn string, readOnly bool) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("engine: open: %w", err)
	}
	if readOnly {
		s := &Store{db: db}
		if err := s.verifySchemaVersion(); err != nil {
			_ = db.Close()
			return nil, err
		}
		return s, nil
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("engine: schema: %w", err)
	}
	if _, err := db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_findings_sort
		ON findings(arrow_id ASC, severity DESC, id ASC);
	`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("engine: sort index: %w", err)
	}
	s := &Store{db: db}
	if err := s.ensureSchemaVersion(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// schemaVersion is the engine's current schema generation. Bumped
// when a migration that can't be expressed by `CREATE ... IF NOT
// EXISTS` ships. Validation-pass-10 C7. Tier 1 (ADR-015) bumps
// from 2 to 3 for the evaluation_runs.recovery_source ALTER.
// Tier 2 (ADR-016) bumps from 3 to 4 for 7 new attestation columns:
// pass_id, context, stratum, adversary_role, unit, unit_payload_json,
// hint_json (default '{}').
// Tier 3 (gate-2 CORR-A-27) bumps from 4 to 5 for the
// CHECK (pass_id != ”) constraint added via table-rebuild
// migration. SQLite ALTER TABLE can't add CHECK on its own; the
// migration recreates the attestations table with the constraint
// in a single transaction.
const schemaVersion = 5

// ErrEngineSchemaMismatch is returned when a DB written by a newer
// ghyll binary is opened by an older one. The operator-friendly
// message names the expected/actual versions and suggests upgrading.
var ErrEngineSchemaMismatch = errors.New("engine: schema version mismatch")

// ensureSchemaVersion creates the engine_meta table if missing and
// records the current schemaVersion. If a higher version already
// exists (future binary), returns ErrEngineSchemaMismatch.
func (s *Store) ensureSchemaVersion() error {
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS engine_meta (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("engine_meta create: %w", err)
	}
	var existing string
	err := s.db.QueryRow(`SELECT value FROM engine_meta WHERE key = ?`, "schema_version").Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("engine_meta read: %w", err)
	}
	if existing != "" {
		var existingVer int
		if _, scanErr := fmt.Sscanf(existing, "%d", &existingVer); scanErr != nil {
			return fmt.Errorf("engine_meta schema_version unreadable: %q", existing)
		}
		if existingVer > schemaVersion {
			return fmt.Errorf("%w: db schema_version=%d > binary schema_version=%d (upgrade ghyll)",
				ErrEngineSchemaMismatch, existingVer, schemaVersion)
		}
	}
	// v2 → v3 migration: add recovery_source to evaluation_runs.
	// IF NOT EXISTS can't apply to columns, so this is conditional
	// on the column being absent. Safe to re-run; PRAGMA returns
	// the column list and the migration is idempotent.
	if err := s.ensureRecoverySourceColumn(); err != nil {
		return fmt.Errorf("v3 migration: %w", err)
	}

	// v3 → v4 migration: 7 new attestation columns (Tier 2 / ADR-016).
	// Wrapped in a single transaction (gate-1 F-10) so a partial
	// failure rolls back cleanly.
	if err := s.ensureUnitColumns(); err != nil {
		return fmt.Errorf("v4 migration: %w", err)
	}

	// v4 → v5 migration: table-rebuild adds CHECK (pass_id != '')
	// to attestations. SQLite ALTER TABLE can't add CHECK; the
	// rebuild is wrapped in a single transaction so a failure
	// rolls back to v4. Idempotent: skips when the constraint
	// is already present.
	if err := s.ensurePassIDCheck(); err != nil {
		return fmt.Errorf("v5 migration: %w", err)
	}

	_, err = s.db.Exec(`
		INSERT INTO engine_meta(key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
		WHERE CAST(excluded.value AS INTEGER) > CAST(engine_meta.value AS INTEGER)
	`, "schema_version", fmt.Sprintf("%d", schemaVersion))
	if err != nil {
		return fmt.Errorf("engine_meta write: %w", err)
	}
	return nil
}

// ensureUnitColumns runs the Tier 2 (ADR-016 Part A) migration:
// adds 7 columns to the attestations table inside a single
// transaction (gate-1 F-10). Idempotent — PRAGMA-checks each
// column's existence and skips already-present columns.
//
// Columns added (default ” unless noted):
//
//	pass_id, context, stratum, adversary_role,
//	unit, unit_payload_json,
//	hint_json (default '{}' per gate-1 F-25)
func (s *Store) ensureUnitColumns() error {
	type column struct {
		name  string
		alter string
	}
	cols := []column{
		{"pass_id", `ALTER TABLE attestations ADD COLUMN pass_id TEXT NOT NULL DEFAULT ''`},
		{"context", `ALTER TABLE attestations ADD COLUMN context TEXT NOT NULL DEFAULT ''`},
		{"stratum", `ALTER TABLE attestations ADD COLUMN stratum TEXT NOT NULL DEFAULT ''`},
		{"adversary_role", `ALTER TABLE attestations ADD COLUMN adversary_role TEXT NOT NULL DEFAULT ''`},
		{"unit", `ALTER TABLE attestations ADD COLUMN unit TEXT NOT NULL DEFAULT ''`},
		{"unit_payload_json", `ALTER TABLE attestations ADD COLUMN unit_payload_json TEXT NOT NULL DEFAULT ''`},
		{"hint_json", `ALTER TABLE attestations ADD COLUMN hint_json TEXT NOT NULL DEFAULT '{}'`},
	}

	// Gate-2 CORR-A-24: read the column set INSIDE the migration
	// transaction so the existence check + ALTERs see the same
	// snapshot. SQLite serializes writers, but the inside-tx read
	// closes the residual race window if shared-writer mode is
	// ever enabled.
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("v4 migration: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := attestationColumnsTx(tx)
	if err != nil {
		return fmt.Errorf("v4 migration: read columns in tx: %w", err)
	}

	for _, col := range cols {
		if _, present := existing[col.name]; present {
			continue
		}
		if _, err := tx.Exec(col.alter); err != nil {
			return fmt.Errorf("v4 migration: ALTER %s: %w", col.name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("v4 migration: commit: %w", err)
	}
	return nil
}

// attestationColumnsTx is the tx-bound variant of
// attestationColumns. Gate-2 CORR-A-24 — keeps the existence
// check + DDL inside one transaction so the snapshot is
// internally consistent.
func attestationColumnsTx(tx *sql.Tx) (map[string]struct{}, error) {
	rows, err := tx.Query(`PRAGMA table_info(attestations)`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	cols := map[string]struct{}{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		cols[name] = struct{}{}
	}
	return cols, rows.Err()
}

// ensurePassIDCheck runs the v4 → v5 table-rebuild migration that
// adds CHECK (pass_id != ”) to the attestations table. SQLite
// ALTER TABLE can't add a CHECK; we recreate the table inside one
// transaction and copy the rows over.
//
// Idempotent: detects the constraint via PRAGMA-foreign-key-list
// /sqlite_master and skips the rebuild if already present.
//
// Tolerates legacy rows with empty pass_id by first updating them
// to a placeholder ('legacy-<id>') so the new CHECK doesn't reject
// the migration itself. This matches the bootstrap-time
// "_legacy"/"migrated-<id>" pattern from gate-2 CORR-A-3.
func (s *Store) ensurePassIDCheck() error {
	// Detect existing constraint via the table's CREATE SQL.
	var ddl string
	err := s.db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type='table' AND name='attestations'`,
	).Scan(&ddl)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Table doesn't exist yet — fresh schema includes it.
			return nil
		}
		return fmt.Errorf("v5: read attestations DDL: %w", err)
	}
	if strings.Contains(ddl, "pass_id <> ''") || strings.Contains(ddl, "pass_id != ''") {
		return nil // already migrated
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("v5: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// First: backfill any rows with empty pass_id so the new
	// CHECK accepts the migration's INSERT.
	if _, err := tx.Exec(
		`UPDATE attestations SET pass_id = 'legacy-' || attestation_id WHERE pass_id = ''`,
	); err != nil {
		return fmt.Errorf("v5: backfill pass_id: %w", err)
	}

	// Recreate the table with the new constraint.
	if _, err := tx.Exec(`
		CREATE TABLE attestations_new (
			attestation_id     TEXT PRIMARY KEY,
			kind               TEXT NOT NULL,
			arrow_id           TEXT NOT NULL,
			clause_id          TEXT,
			op_id              TEXT NOT NULL,
			attested_by_role   TEXT NOT NULL,
			source_role        TEXT NOT NULL DEFAULT '',
			target_role        TEXT NOT NULL DEFAULT '',
			verdict            TEXT NOT NULL,
			reason             TEXT NOT NULL DEFAULT '',
			timestamp          INTEGER NOT NULL,
			grid_version       INTEGER NOT NULL,
			pass_id            TEXT NOT NULL DEFAULT '',
			context            TEXT NOT NULL DEFAULT '',
			stratum            TEXT NOT NULL DEFAULT '',
			adversary_role     TEXT NOT NULL DEFAULT '',
			unit               TEXT NOT NULL DEFAULT '',
			unit_payload_json  TEXT NOT NULL DEFAULT '',
			hint_json          TEXT NOT NULL DEFAULT '{}',
			CHECK (kind IN ('depth-type', 'on-the-spot')),
			CHECK ((kind = 'on-the-spot' AND clause_id IS NULL)
			    OR (kind = 'depth-type'  AND clause_id IS NOT NULL)),
			CHECK (verdict IN ('pass', 'fail', 'insufficient-basis')),
			CHECK (source_role = '' OR attested_by_role <> source_role),
			CHECK (target_role = '' OR attested_by_role <> target_role),
			-- Tier 3 / gate-2 CORR-A-27: pass_id must be non-empty.
			CHECK (pass_id <> '')
		)
	`); err != nil {
		return fmt.Errorf("v5: create attestations_new: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO attestations_new
		SELECT attestation_id, kind, arrow_id, clause_id, op_id,
		       attested_by_role, source_role, target_role, verdict,
		       reason, timestamp, grid_version,
		       pass_id, context, stratum, adversary_role,
		       unit, unit_payload_json, hint_json
		FROM   attestations
	`); err != nil {
		return fmt.Errorf("v5: copy rows: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE attestations`); err != nil {
		return fmt.Errorf("v5: drop old: %w", err)
	}
	if _, err := tx.Exec(`ALTER TABLE attestations_new RENAME TO attestations`); err != nil {
		return fmt.Errorf("v5: rename: %w", err)
	}
	// Re-create the indexes (DROP TABLE removed them).
	if _, err := tx.Exec(`
		CREATE INDEX IF NOT EXISTS idx_attestations_arrow
			ON attestations(arrow_id)
	`); err != nil {
		return fmt.Errorf("v5: index arrow: %w", err)
	}
	if _, err := tx.Exec(`
		CREATE INDEX IF NOT EXISTS idx_attestations_clause
			ON attestations(clause_id) WHERE clause_id IS NOT NULL
	`); err != nil {
		return fmt.Errorf("v5: index clause: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("v5: commit: %w", err)
	}
	return nil
}

// attestationColumns returns the set of column names currently
// on the attestations table. DEPRECATED post-gate-2 CORR-A-24:
// the in-tx variant attestationColumnsTx is now the production
// path. Kept as a build-time symbol for callers that don't have
// a tx handy.
//
//nolint:unused
func (s *Store) attestationColumns() (map[string]struct{}, error) {
	rows, err := s.db.Query(`PRAGMA table_info(attestations)`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]struct{}{}
	for rows.Next() {
		var (
			cid          int
			name         string
			ctype        string
			notnull      int
			defaultValue sql.NullString
			pk           int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		out[name] = struct{}{}
	}
	return out, rows.Err()
}

// ensureRecoverySourceColumn adds evaluation_runs.recovery_source if
// it doesn't exist yet. Idempotent: skips when the column is already
// present (so v3-fresh and v2-upgraded DBs converge).
func (s *Store) ensureRecoverySourceColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(evaluation_runs)`)
	if err != nil {
		return fmt.Errorf("PRAGMA table_info: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			cid          int
			name         string
			ctype        string
			notnull      int
			defaultValue sql.NullString
			pk           int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("PRAGMA scan: %w", err)
		}
		if name == "recovery_source" {
			return nil // already present
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("PRAGMA rows: %w", err)
	}
	if _, err := s.db.Exec(
		`ALTER TABLE evaluation_runs ADD COLUMN recovery_source TEXT NOT NULL DEFAULT ''`,
	); err != nil {
		return fmt.Errorf("ALTER TABLE evaluation_runs: %w", err)
	}
	return nil
}

// verifySchemaVersion is the read-only counterpart: it expects the
// engine_meta row to exist (writer created it) and rejects future
// versions.
func (s *Store) verifySchemaVersion() error {
	var existing string
	err := s.db.QueryRow(`SELECT value FROM engine_meta WHERE key = ?`, "schema_version").Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		// Pre-schema_version DB or empty engine_meta — accept it for
		// backwards compatibility with phase-9 stores.
		return nil
	}
	if err != nil {
		// Read-only opens often hit "no such table" on a pre-C7 db;
		// don't treat that as fatal.
		if strings.Contains(err.Error(), "no such table") {
			return nil
		}
		return fmt.Errorf("engine_meta read: %w", err)
	}
	var existingVer int
	if _, scanErr := fmt.Sscanf(existing, "%d", &existingVer); scanErr != nil {
		return fmt.Errorf("engine_meta schema_version unreadable: %q", existing)
	}
	if existingVer > schemaVersion {
		return fmt.Errorf("%w: db schema_version=%d > binary schema_version=%d (upgrade ghyll)",
			ErrEngineSchemaMismatch, existingVer, schemaVersion)
	}
	return nil
}

// Close releases the database connection.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB returns the underlying *sql.DB. Exposed for tests and for
// the journal layer's transactional writes. Production callers
// should prefer the typed Insert/Update methods.
func (s *Store) DB() *sql.DB {
	return s.db
}

// schema is the v2 entity DDL. Append-on-update for the current-
// state tables (findings, classifications, grid_arrows) so the
// readers don't have to replay logs. Pure append for the audit
// tables (finding_transitions, classification_overwrites).
const schema = `
CREATE TABLE IF NOT EXISTS findings (
	id                TEXT PRIMARY KEY,
	arrow_id          TEXT NOT NULL,
	type              TEXT NOT NULL,
	severity          INTEGER NOT NULL,
	status            TEXT NOT NULL,
	description       TEXT NOT NULL DEFAULT '',
	raised_at         TEXT NOT NULL DEFAULT '',
	raised_by_role    TEXT NOT NULL DEFAULT '',
	transition_count  INTEGER NOT NULL DEFAULT 0,
	grid_version      INTEGER NOT NULL DEFAULT 0,
	store_version     INTEGER NOT NULL DEFAULT 0,
	updated_at        TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_findings_arrow    ON findings(arrow_id);
CREATE INDEX IF NOT EXISTS idx_findings_status   ON findings(status);
CREATE INDEX IF NOT EXISTS idx_findings_severity ON findings(severity);

CREATE TABLE IF NOT EXISTS finding_transitions (
	seq         INTEGER PRIMARY KEY AUTOINCREMENT,
	finding_id  TEXT NOT NULL,
	from_status TEXT NOT NULL,
	to_status   TEXT NOT NULL,
	role        TEXT NOT NULL DEFAULT '',
	reason      TEXT NOT NULL DEFAULT '',
	store_version INTEGER NOT NULL DEFAULT 0,
	at          TEXT NOT NULL DEFAULT '',
	FOREIGN KEY(finding_id) REFERENCES findings(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_transitions_finding ON finding_transitions(finding_id);

CREATE TABLE IF NOT EXISTS requirements (
	arrow_id      TEXT NOT NULL,
	req_id        TEXT NOT NULL,
	min_depth     INTEGER NOT NULL,
	description   TEXT NOT NULL DEFAULT '',
	store_version INTEGER NOT NULL DEFAULT 0,
	declared_at   TEXT NOT NULL DEFAULT '',
	PRIMARY KEY(arrow_id, req_id)
);
CREATE INDEX IF NOT EXISTS idx_requirements_arrow ON requirements(arrow_id);

CREATE TABLE IF NOT EXISTS classifications (
	arrow_id      TEXT NOT NULL,
	req_id        TEXT NOT NULL,
	observed      INTEGER NOT NULL,
	evidence      TEXT NOT NULL DEFAULT '',
	overwrite_count INTEGER NOT NULL DEFAULT 0,
	store_version INTEGER NOT NULL DEFAULT 0,
	classified_at TEXT NOT NULL DEFAULT '',
	PRIMARY KEY(arrow_id, req_id)
);
CREATE INDEX IF NOT EXISTS idx_classifications_arrow ON classifications(arrow_id);

CREATE TABLE IF NOT EXISTS classification_overwrites (
	seq             INTEGER PRIMARY KEY AUTOINCREMENT,
	arrow_id        TEXT NOT NULL,
	req_id          TEXT NOT NULL,
	before_observed INTEGER NOT NULL,
	after_observed  INTEGER NOT NULL,
	store_version   INTEGER NOT NULL DEFAULT 0,
	at              TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_overwrites_arrow_req
	ON classification_overwrites(arrow_id, req_id);

CREATE TABLE IF NOT EXISTS grid_arrows (
	id                TEXT NOT NULL,
	grid_version      INTEGER NOT NULL,
	source_role       TEXT NOT NULL,
	target_role       TEXT NOT NULL,
	stratum           TEXT NOT NULL,
	context           TEXT NOT NULL,
	clauses_json      TEXT NOT NULL,
	requirements_json TEXT NOT NULL,
	kind              TEXT NOT NULL,
	declared_at       TEXT NOT NULL DEFAULT '',
	PRIMARY KEY(id, grid_version)
);
CREATE INDEX IF NOT EXISTS idx_arrows_id   ON grid_arrows(id);
CREATE INDEX IF NOT EXISTS idx_arrows_kind ON grid_arrows(kind);

CREATE TABLE IF NOT EXISTS amendments (
	id               TEXT PRIMARY KEY,
	reason           TEXT NOT NULL,
	source_arrow     TEXT NOT NULL,
	target_role      TEXT NOT NULL,
	contexts_json    TEXT NOT NULL,
	description      TEXT NOT NULL DEFAULT '',
	finding_ids_json TEXT NOT NULL,
	created_at       TEXT NOT NULL DEFAULT '',
	drained_at       TEXT
);
CREATE INDEX IF NOT EXISTS idx_amendments_source ON amendments(source_arrow);

CREATE TABLE IF NOT EXISTS evaluation_runs (
	id                          TEXT PRIMARY KEY,
	clause_id                   TEXT NOT NULL,
	pass_id                     TEXT NOT NULL,
	arrow_id                    TEXT NOT NULL DEFAULT '',
	grid_version                INTEGER NOT NULL DEFAULT 0,
	depth_type_attestation_ref  TEXT NOT NULL DEFAULT '',
	actual_tier                 INTEGER NOT NULL DEFAULT 0,
	min_depth_tier              INTEGER NOT NULL DEFAULT 0,
	evaluator_concept           TEXT NOT NULL DEFAULT '',
	evaluator_generation        INTEGER NOT NULL DEFAULT 0,
	started_at                  TEXT NOT NULL DEFAULT '',
	completed_at                TEXT NOT NULL DEFAULT '',
	start_status                TEXT NOT NULL DEFAULT '',
	end_status                  TEXT NOT NULL DEFAULT '',
	result_json                 TEXT NOT NULL DEFAULT '',
	run_error                   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_runs_clause ON evaluation_runs(clause_id);
CREATE INDEX IF NOT EXISTS idx_runs_pass   ON evaluation_runs(pass_id);
CREATE INDEX IF NOT EXISTS idx_runs_arrow  ON evaluation_runs(arrow_id);

-- Attestations: operator-verdict records (ADR-010).
-- Kind 'depth-type' attests a clause's depth-type assignment
-- (clause_id NOT NULL). Kind 'on-the-spot' attests an arrow
-- definition produced by the definer hook (clause_id NULL).
-- attested_by_role MUST NOT equal source_role or target_role
-- (§12.2 / ADR-009) — enforced at the runner.AttestationStore
-- boundary; the schema records the source/target roles for audit.
CREATE TABLE IF NOT EXISTS attestations (
	attestation_id     TEXT PRIMARY KEY,
	kind               TEXT NOT NULL,
	arrow_id           TEXT NOT NULL,
	clause_id          TEXT,
	op_id              TEXT NOT NULL,
	attested_by_role   TEXT NOT NULL,
	source_role        TEXT NOT NULL DEFAULT '',
	target_role        TEXT NOT NULL DEFAULT '',
	verdict            TEXT NOT NULL,
	reason             TEXT NOT NULL DEFAULT '',
	timestamp          INTEGER NOT NULL,
	grid_version       INTEGER NOT NULL,
	-- Tier 2 (ADR-016) columns. ensureUnitColumns ALTERs these in
	-- on upgrade from v3; CREATE TABLE bakes them in for fresh v4+
	-- databases.
	pass_id            TEXT NOT NULL DEFAULT '',
	context            TEXT NOT NULL DEFAULT '',
	stratum            TEXT NOT NULL DEFAULT '',
	adversary_role     TEXT NOT NULL DEFAULT '',
	unit               TEXT NOT NULL DEFAULT '',
	unit_payload_json  TEXT NOT NULL DEFAULT '',
	hint_json          TEXT NOT NULL DEFAULT '{}',
	CHECK (kind IN ('depth-type', 'on-the-spot')),
	CHECK ((kind = 'on-the-spot' AND clause_id IS NULL)
	    OR (kind = 'depth-type'  AND clause_id IS NOT NULL)),
	CHECK (verdict IN ('pass', 'fail', 'insufficient-basis')),
	-- §12.2 / ADR-009: attested_by_role MUST NOT equal source or
	-- target role. Case-sensitivity differs from runner.AttestationStore
	-- (which does a case-insensitive EqualFold check); the schema
	-- enforces the exact-string variant as a backstop against
	-- out-of-band SQL inserts that bypass the runner-layer guard.
	-- Empty source/target columns (the default '') skip the check
	-- since the runner did not record those identities.
	CHECK (source_role = '' OR attested_by_role <> source_role),
	CHECK (target_role = '' OR attested_by_role <> target_role),
	-- Tier 3 / gate-2 CORR-A-27: pass_id must be non-empty.
	-- Fresh databases (created at v4+) include the constraint
	-- directly; v4→v5 ensurePassIDCheck rebuilds older tables.
	CHECK (pass_id <> '')
);
CREATE INDEX IF NOT EXISTS idx_attestations_arrow
	ON attestations(arrow_id);
CREATE INDEX IF NOT EXISTS idx_attestations_clause
	ON attestations(clause_id) WHERE clause_id IS NOT NULL;

-- Passes table (ADR-015 Part A, Tier 1). One row per pass_id.
-- runner.Pass mirrors columns 1:1 plus recovered_at (set-once by
-- engine.Recovery for attestation-pending preservation).
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
CREATE INDEX IF NOT EXISTS idx_passes_state    ON passes(state);
CREATE INDEX IF NOT EXISTS idx_passes_arrow    ON passes(arrow_id);
CREATE INDEX IF NOT EXISTS idx_passes_role_ctx ON passes(role, context);
`
