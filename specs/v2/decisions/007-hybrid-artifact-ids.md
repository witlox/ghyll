# ADR-007: Hybrid artifact IDs — path-default with manual IDs where stability matters

**Status:** Accepted (2026-05-18; D11)

**Context:**

Phase-3 architect findings #2 and #3 surfaced that both
`no-orphan-symbol` (the trace from exported symbols to spec
clauses) and the dependency declaration syntax need stable
identifiers across artifacts. Without stable IDs, content
rewording invalidates downstream arrows on every typo, OR
dependencies must be declared at file granularity (overbroad,
forcing conservative invalidation).

Three pure options:

- **Content-hash IDs** — auto-derived from artifact content; zero
  authoring overhead; hashes change on every wording fix.
- **Manually-assigned IDs** — every artifact entry carries an
  explicit `id:` field; survives rewordings; ID-stamping is
  overhead.
- **Path-based addressing** — IDs are paths (`invariants.md#section-3-cardinality-1`);
  stable until file reorg; brittle to refactors.

**Decision:**

**Hybrid: path-default with manual IDs where stability matters.**

- The default addressing is **path-based** (e.g.,
  `invariants.md#section-3-cardinality-1`). Suits most clauses.
- Clauses that other arrows declare dependence on, or that need to
  survive content rewordings, get a **manually-assigned `id:` field**
  (e.g., `INV-001`, `FEAT-USER-001`, `INT-PAYMENTS-ORDER-001`).
- The operator decides per clause whether a manual ID is needed.
  Default is path-based; explicit `id:` is the marked case.
- `unique-definition` (catalogue concept) enforces ID uniqueness
  where manual IDs are present.

Dependency declarations may target any granularity:

- `file` — any change to the file invalidates.
- `section` — path-based; change to the named section invalidates.
- `clause-id` — manual-ID precision; change to the identified clause
  invalidates.

**Consequences:**

- **Rules in:** progressive disclosure of identifier complexity —
  most clauses use cheap path addressing; the high-stability
  clauses pay the manual-ID tax. Operators decide the tradeoff
  per clause.
- **Rules out:** content-hash IDs (hashes shift on typos, making
  dependency tracking trigger-happy); single-mechanism addressing
  (path-only or manual-only).
- **Discoverability signal:** an arrow with many `file`-granularity
  dependencies that gets frequently `invalidated` is a sign the
  operator should upgrade those dependencies to `section` or
  `clause-id`. Operator-tooling should surface this.
- **Naming convention:** manual IDs have no enforced schema; the
  operator picks a namespace convention per project (e.g., `INV-NNN`,
  `FEAT-<area>-NNN`).

See `gates.md` §2.1 "Artifact ID conventions";
`specs/direction/components/concepts.md` (`unique-definition`,
`trace-link-present`).
