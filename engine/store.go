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
	"context"
	"database/sql"
	"errors"
	"fmt"

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
	// Diamond v4 / ADR-v4-008: idempotent ALTER TABLE for the
	// passes.remediation_outcome + passes.remediation_rounds_used
	// columns. PRAGMA table_info gates the ADD so re-open is a
	// no-op. Each ALTER runs in its own transaction so a single
	// failure (column already present) doesn't roll back schema.
	if err := migrateAddRemediationColumns(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("engine: migrate remediation columns: %w", err)
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
// EXISTS` ships. The pre-prod baseline collapses the v2→v5
// migration chain into a single fresh CREATE TABLE; pre-prod DBs
// are recreated, no upgrade path is preserved.
const schemaVersion = 1

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

// verifySchemaVersion is the read-only counterpart: it expects the
// engine_meta row to exist (writer created it) and rejects future
// versions.
func (s *Store) verifySchemaVersion() error {
	var existing string
	err := s.db.QueryRow(`SELECT value FROM engine_meta WHERE key = ?`, "schema_version").Scan(&existing)
	if err != nil {
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
	run_error                   TEXT NOT NULL DEFAULT '',
	recovery_source             TEXT NOT NULL DEFAULT ''
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

-- Diamond v4 / ADR-v4-008: arrow_invalidations records every
-- /invalidate-arrow operator action AND every recovery republish
-- where an amendment-driven invalidation didn't make it to disk
-- before crash. One row per (arrow_id, invalidated_at) pair; the
-- table is append-only.
CREATE TABLE IF NOT EXISTS arrow_invalidations (
	seq             INTEGER PRIMARY KEY AUTOINCREMENT,
	arrow_id        TEXT NOT NULL,
	op_id           TEXT NOT NULL DEFAULT '',
	reason          TEXT NOT NULL DEFAULT '',
	source          TEXT NOT NULL DEFAULT '',
	invalidated_at  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_arrow_invalidations_arrow
	ON arrow_invalidations(arrow_id);
`

// migrateAddRemediationColumns is the idempotent ALTER TABLE that
// adds the diamond-v4 / ADR-v4-008 columns to passes:
//   - remediation_outcome    TEXT NOT NULL DEFAULT ”
//   - remediation_rounds_used INTEGER NOT NULL DEFAULT 0
//
// Uses PRAGMA table_info to gate the ADD COLUMN — re-open against
// a migrated DB is a no-op (no error). Each ADD COLUMN runs in its
// own statement so a single conflict does not cascade.
//
// Per ADR-v4-008 "Status: Accepted": this migration MUST run from
// OpenStore so the dispatcher's RemediationReport persistence path
// (Tier-3 follow-up) has its target columns present at next open.
func migrateAddRemediationColumns(db *sql.DB) error {
	cols, err := tableColumns(db, "passes")
	if err != nil {
		return fmt.Errorf("inspect passes: %w", err)
	}
	if _, ok := cols["remediation_outcome"]; !ok {
		if _, err := db.Exec(`ALTER TABLE passes ADD COLUMN remediation_outcome TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("alter passes add remediation_outcome: %w", err)
		}
	}
	if _, ok := cols["remediation_rounds_used"]; !ok {
		if _, err := db.Exec(`ALTER TABLE passes ADD COLUMN remediation_rounds_used INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("alter passes add remediation_rounds_used: %w", err)
		}
	}
	return nil
}

// tableColumns returns the set of column names on the given table.
// Empty map on a missing table (sqlite's PRAGMA table_info returns
// no rows in that case).
func tableColumns(db *sql.DB, table string) (map[string]struct{}, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]struct{}{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		out[name] = struct{}{}
	}
	return out, rows.Err()
}

// InsertArrowInvalidation appends an arrow-invalidation row.
// Diamond v4 / ADR-v4-008 + R28. Source is one of
// {operator, recovery, amendment}; reason carries the operator's
// rationale (sanitized at the call site).
func (s *Store) InsertArrowInvalidation(ctx context.Context, arrowID, opID, reason, source, at string) error {
	if s == nil || s.db == nil {
		return ErrEngineClosed
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO arrow_invalidations (
			arrow_id, op_id, reason, source, invalidated_at
		) VALUES (?, ?, ?, ?, ?)
	`, arrowID, opID, reason, source, at)
	return err
}

// CountArrowInvalidations returns the row count for the named arrow
// across the audit log. Exposed for tests + the future status CLI.
func (s *Store) CountArrowInvalidations(ctx context.Context, arrowID string) (int, error) {
	if s == nil || s.db == nil {
		return 0, ErrEngineClosed
	}
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM arrow_invalidations WHERE arrow_id = ?
	`, arrowID).Scan(&n)
	return n, err
}
