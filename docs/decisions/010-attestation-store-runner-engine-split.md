# ADR-010: Attestation records — runner cache + engine persistence

Date: 2026-05-19
Status: Accepted

## Context

`runner.Clause.DepthTypeAttestationRef` is a string field that links a
clause back to an operator-attested verdict (typically captured during
init's depth-type assignment, since depth-type is itself
depth-sensitive and requires operator confirmation).

Today the runner carries the ref through to the engine layer
(`engine/journal.go:455`, `engine/records.go:433`,
`engine/queries.go:399`). The ref is opaque: it is read and persisted,
but no component owns the *records* the ref points at.

The previous-session note read:

> DepthType attestation linkage — runner carries
> `Clause.DepthTypeAttestationRef`; engine layer resolves it against
> the attestation store. Where the attestation store lives is open.

## Decision

Attestation records live in **two coordinated places**:

1. **Engine (sqlite)** — authoritative, persistent. A new
   `attestations` table on `engine.Store` keyed by attestation ID,
   carrying: attestation-id, kind (depth-type / clause-verdict /
   on-the-spot), arrow-id, clause-id (nullable), op-id, attested-by
   role, verdict (pass/fail/insufficient-basis), reason, timestamp,
   grid-version. Records are immutable once written; the operator
   verdict is recorded exactly once.
2. **Runner (in-memory)** — `runner.AttestationStore` is the
   in-memory cache populated by:
   - Direct writes during runtime as operator verdicts arrive
     (publishes an Observer event so the journal persists).
   - Engine replay at session start (loads the
     `attestations` table back into the cache).

`Clause.DepthTypeAttestationRef` resolves through
`AttestationStore.Lookup(ref)` returning the cached record. The
caller then validates the record's verdict, timestamp, and op-id.

## Rationale

The existing pattern across the runtime is **runner-cache +
engine-journal + replay-on-startup**. `FindingsStore`,
`ClassificationsStore`, `Grid`, and `AmendmentQueue` all follow this
shape (see ADR-006 for one-session-per-repo and ADR-009 in v2 for
three-locks). Attestations are conceptually the same: append-only
records with a lookup-by-ID surface, owned by the runner at the hot
path, persisted by the engine for durability and cross-session
replay.

The alternative — putting attestations on `FindingsStore` — would
overload that surface. Findings model *defects* with status
transitions (open → fixed → accepted-risk → invalidated). Attestation
records model *operator verdicts* that, once recorded, don't
transition. Different semantics, different store.

## Consequences

### Schema

`engine.Store` gains:

```sql
CREATE TABLE attestations (
    attestation_id     TEXT PRIMARY KEY,
    kind               TEXT NOT NULL,   -- 'depth-type'|'clause-verdict'|'on-the-spot'
    arrow_id           TEXT NOT NULL,
    clause_id          TEXT,            -- nullable for on-the-spot
    op_id              TEXT NOT NULL,
    attested_by_role   TEXT NOT NULL,
    verdict            TEXT NOT NULL,   -- 'pass'|'fail'|'insufficient-basis'
    reason             TEXT,
    timestamp          INTEGER NOT NULL,
    grid_version       INTEGER NOT NULL
);
CREATE INDEX idx_attestations_arrow ON attestations(arrow_id);
CREATE INDEX idx_attestations_clause ON attestations(clause_id) WHERE clause_id IS NOT NULL;
```

`schema_version` increments.

### Code

- New file `runner/attestationstore.go`: `AttestationStore` type with
  `Record(rec AttestationRecord)`, `Lookup(ref string) (AttestationRecord, bool)`,
  `Observe(o AttestationObserver)`.
- New file `engine/attestations.go`: store methods + record marshal.
- `engine.Journal.AttachAttestations(store *runner.AttestationStore)`.
- `engine.Replay` loads attestation rows into `runner.AttestationStore`.

### Replay

Attestations replay before clauses. The `DepthTypeAttestationRef` on
each clause record can now be resolved as the clause flows back into
the grid.

### Tests

- Unit tests on `runner/attestationstore.go` for Lookup / Record /
  Observe.
- Engine integration tests on the new table.
- BDD: `attestation.feature` scenarios using
  `DepthTypeAttestationRef` resolve through the new store, removing
  some `@deferred` tags.

## Alternatives considered

1. **Attestations on `FindingsStore`.** Overloads a defect-tracking
   surface with verdict records. Rejected (different semantics).
2. **Engine-only, no runtime cache.** Every clause evaluation that
   needs to resolve a ref pays a sqlite hit. Rejected — the hot path
   of clause evaluation is too tight for that. Cache stays.
3. **Two parallel writes, no journal.** Engine + runner write
   directly with no observer. Rejected — would break the
   replay-on-startup invariant and split the source of truth.

## Related

- `runner/runner.go:152` — `DepthTypeAttestationRef` field
- `engine/journal.go`, `engine/records.go`, `engine/queries.go` —
  current journal serialization of the ref
- `engine/replay.go` — replay path the new attestations replay slots
  into
