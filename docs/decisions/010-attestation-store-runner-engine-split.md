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

## Scope (what this ADR owns vs. what it does not)

This ADR owns two attestation *kinds*:

- **`depth-type`** — operator confirms a clause's `DepthType` /
  `MinDepthTier` assignment during init. The record is the source of
  truth for the assignment going forward.
- **`on-the-spot`** — operator approves an on-the-spot arrow
  definition (per §12.2 and ADR-009). The record captures the
  attesting role and the suspension's identity.

It does **not** own:

- **Clause-verdict transitions** during running passes (operator
  marks a finding as `accepted-risk`, `fixed`, `invalidated`). Those
  remain in `runner.FindingsStore.TransitionByOperator` and the
  `finding_transitions` engine table. The two surfaces capture
  different semantics: findings model defect lifecycle (mutable
  status); attestations record an immutable operator verdict on a
  schema element.

## Decision

Attestation records (depth-type + on-the-spot) live in **two
coordinated places**:

1. **Engine (sqlite)** — authoritative, persistent. A new
   `attestations` table on `engine.Store`. Records are immutable
   once written.
2. **Runner (in-memory)** — `runner.AttestationStore` is the
   in-memory cache populated by:
   - Direct `Record(rec)` calls during runtime as operator verdicts
     arrive (publishes an Observer event so the journal persists).
   - Engine replay at session start (loads the `attestations` table
     back into the cache before evaluation begins).

`Clause.DepthTypeAttestationRef` resolves through
`AttestationStore.Lookup(ref)` returning the cached record. The
caller validates the record's `verdict`, `timestamp`, and `op_id`.

## Schema

```sql
CREATE TABLE attestations (
    attestation_id     TEXT PRIMARY KEY,
    kind               TEXT NOT NULL,    -- 'depth-type' | 'on-the-spot'
    arrow_id           TEXT NOT NULL,
    clause_id          TEXT,             -- present iff kind='depth-type' AND attestation is per-clause
    op_id              TEXT NOT NULL,
    attested_by_role   TEXT NOT NULL,
    source_role        TEXT NOT NULL,    -- the arrow's source role (for §12.2 validation)
    target_role        TEXT NOT NULL,    -- the arrow's target role (for §12.2 validation)
    verdict            TEXT NOT NULL,    -- 'pass' | 'fail' | 'insufficient-basis'
    reason             TEXT,
    timestamp          INTEGER NOT NULL,
    grid_version       INTEGER NOT NULL,
    CHECK (kind IN ('depth-type', 'on-the-spot')),
    CHECK ((kind = 'on-the-spot' AND clause_id IS NULL) OR kind = 'depth-type'),
    CHECK (verdict IN ('pass', 'fail', 'insufficient-basis'))
);
CREATE INDEX idx_attestations_arrow ON attestations(arrow_id);
CREATE INDEX idx_attestations_clause ON attestations(clause_id) WHERE clause_id IS NOT NULL;
```

The `clause_id` is nullable specifically because on-the-spot
attestations attest the *whole arrow definition* (there is no
per-clause grain at that point). Depth-type attestations attest a
specific clause and always populate `clause_id`. The kind/clause_id
CHECK constraint pins this.

`source_role` and `target_role` are recorded so the store can
validate the §12.2/ADR-009 constraint at Record time (see
"Self-cert enforcement" below).

`schema_version` increments.

## Self-cert enforcement (ADR-009 integration)

`AttestationStore.Record(rec)` rejects records where
`attested_by_role` equals `source_role` or `target_role`
(case-insensitive, trimmed). The sentinel error is
`ErrAttestationSelfCert`. This is the single enforcement point for
§12.2; `runner.ResolveOnTheSpot` still validates upstream so the
on-the-spot site can fail with the specific
`ErrSelfCertification` / `ErrSelfCertImpossible` errors before
attempting to record.

Centralizing the constraint at the store boundary means
out-of-band recording paths (init's depth-type confirmation,
future operator UX endpoints) cannot bypass it.

## Attestation IDs

Attestation IDs are deterministic so a clause's
`DepthTypeAttestationRef` can be assigned at init *before* the
record is persisted by the journal consumer goroutine:

```
attestation_id = "att-" + arrow_id + "-" + clause_id + "-v" + grid_version
                                                      (clause_id omitted for on-the-spot)
```

This collapses the replay ordering problem (next section): the ref
is computable from clause + grid state alone; the store fills in
the body when the operator verdict arrives. Until then,
`Lookup(ref)` returns `(zero, false)` and the caller treats the
clause as having no attestation yet.

## Replay ordering

Replay order on session start:

```
1. attestations          (kind='depth-type' and 'on-the-spot' records)
2. arrows + clauses      (grid)
3. findings
4. classifications
5. amendments
6. evaluation_runs
```

Attestations replay **first** because:

- Their primary keys (deterministic per the scheme above) do not
  depend on any other entity's runtime state.
- Subsequent grid / findings replay may resolve attestation refs
  through `AttestationStore.Lookup`.

Grid replay does not require attestation refs to *resolve*; the
clause stores the opaque ref string. The ref is resolved only when
a clause is evaluated or when the engine surfaces attestation
status via the engine CLI. So even if an attestation row is missing
at replay (e.g., a partial dump), grid replay still succeeds —
`Lookup` returns `(zero, false)` and the caller treats the
attestation as absent.

## JSONL verdict records (audit trail)

A separate component (`runner.AttestationJSONLWriter`) writes one
record per attestation to a project-local JSONL file
(`.ghyll/attestations.jsonl`) for audit. Source of truth:
**the engine table**, not the runtime cache. The writer subscribes
to journal events for `attestations` writes; the JSONL is appended
synchronously in the consumer goroutine so the audit trail and the
sqlite record are atomically consistent.

If the JSONL file is removed, replay reconstructs it from the
engine table (`ghyll engine export-attestations`).

## Rationale

The existing pattern across the runtime is **runner-cache +
engine-journal + replay-on-startup**. `FindingsStore`,
`ClassificationsStore`, `Grid`, and `AmendmentQueue` all follow this
shape. Depth-type and on-the-spot attestations are conceptually the
same: append-only records with a lookup-by-ID surface, owned by the
runner at the hot path, persisted by the engine for durability and
cross-session replay.

The deterministic-ID scheme + replay-first ordering removes the
chicken-egg dependency that an autoincrement ID + after-grid
replay would introduce. Clauses can carry refs to records that may
not yet exist; the surface is robust to that.

Putting clause-verdict attestations on FindingsStore (NOT here) and
schema-verdict attestations on AttestationStore (here) is the right
split because the two have different lifecycles: findings transition
through states, attestations don't.

## Consequences

### Code

- New file `runner/attestationstore.go`: `AttestationStore` type
  with `Record(rec AttestationRecord)`, `Lookup(ref string)
  (AttestationRecord, bool)`, `Observe(o AttestationObserver)`.
  `Record` validates the §12.2/ADR-009 self-cert constraint.
- New file `engine/attestations.go`: store methods + record
  marshal + schema migration.
- `engine.Journal.AttachAttestations(store *runner.AttestationStore)`
  with the standard bounded-channel observer.
- `engine.Replay` loads attestation rows into
  `runner.AttestationStore` BEFORE grid replay.
- New file `runner/attestation_jsonl.go`:
  `AttestationJSONLWriter` reads from journal events.

### Test

- Unit tests on `runner/attestationstore.go` for Lookup / Record /
  Observe / self-cert rejection.
- Unit tests on the deterministic-ID scheme: same inputs → same ID;
  collision absence within a (grid_version, arrow_id, clause_id)
  tuple.
- Engine integration tests on the new table + replay ordering.
- BDD: attestation.feature scenarios using
  `DepthTypeAttestationRef` resolve through the new store, removing
  the matching `@deferred` tags.

## Alternatives considered

1. **Attestations on `FindingsStore`.** Overloads a defect-tracking
   surface with verdict records. Rejected (different lifecycle).
2. **Engine-only, no runtime cache.** Every clause evaluation that
   needs to resolve a ref pays a sqlite hit. Rejected — the hot path
   of clause evaluation is too tight.
3. **Autoincrement attestation IDs.** Simpler ID scheme but
   introduces the replay-ordering chicken-egg. Rejected.
4. **Two parallel writes, no journal.** Engine + runner write
   directly with no observer. Rejected — would break the
   replay-on-startup invariant and split the source of truth.
5. **JSONL writer reads from runtime cache.** In-memory only;
   cache contents not durable. Rejected — audit trail must survive
   crash.

## Related

- `runner/runner.go:152` — `DepthTypeAttestationRef` field
- `engine/journal.go`, `engine/records.go`, `engine/queries.go`
- `engine/replay.go` — replay path
- ADR-009 (self-cert scope) — the constraint AttestationStore enforces
