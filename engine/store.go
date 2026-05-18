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
// Concurrency: the underlying *sql.DB is safe for concurrent use,
// but the v2 entities have invariants (e.g., a finding's
// transitions must reference an existing row) that the engine
// enforces at the journaling layer.
func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("engine: open: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("engine: schema: %w", err)
	}
	// Foreign keys are off by default in sqlite; enable them so
	// audit-log tables enforce their references.
	if _, err := db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("engine: enable foreign_keys: %w", err)
	}
	return &Store{db: db}, nil
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
`

// joinPlaceholders returns "?,?,?,..." for n columns. Used by
// dynamic INSERT statements.
func joinPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}
