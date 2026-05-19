# ADR-012: Global write-lock with FIFO amendment queue

**Status:** Accepted (2026-05-18; D22)

**Context:**

Validation-pass-2 finding #1 and #2: when ContextA's diamond is
mid-flight and ContextB's analyst lands a grid amendment, the
schema didn't say whether ContextA must abort, where the amendment
originates (cell vs queue), or how concurrent amendments serialize.
ADR-008 fixed arrow identity to include `grid-version`; that made
the version-bump operation load-bearing for arrow identity. Two
concurrent amendments producing ambiguous v(N+1) would break arrow
identity.

The choice was between:

- **Global serialization:** all amendments take a project-wide
  lock; in-flight cells affected by the amendment abort.
- **Optimistic with rebase:** cells run on whatever version they
  started with; recording a completion against a now-stale version
  triggers a rebase.
- **Per-context locking:** amendments scoped to a context lock
  only that context.

Per-context locking was rejected: the integrator detects
*cross-context* defects by definition, so amendments by definition
touch at least two contexts. Optimistic-with-rebase was rejected:
"rebase failure" is a new state the schema would have to define,
and it pushes complexity onto every component that records a pass
completion.

**Decision:**

**Global serialization via a project-wide write-lock and a FIFO
amendment queue.**

When an integrator finding triggers an amendment:

1. The amendment is enqueued (FIFO).
2. The amendment component acquires the project-wide write-lock
   (per ADR-009).
3. The analyst is re-engaged; the amended spec is produced; the
   v(N+1) arrow grid is computed.
4. Dependency check against in-flight passes:
   - **Affected** (dependency on a changed artifact, or no
     dependencies declared — conservative fallback): pass is
     `aborted` with `reason: invalidated`; arrow status becomes
     `invalidated`.
   - **Unaffected**: pass continues against vN and records
     completion normally.
5. Findings discovered before abort are retained, **tagged with
   their original `grid-version`** so they're distinguishable from
   findings on the new vN+1 arrow.
6. Grid v(N+1) is written atomically per ADR-010.
7. Lock releases; next queued amendment may proceed.

The next pass on an `invalidated` arrow starts fresh — phases
re-run from adversarial. There is no "resume mid-phase."

**Consequences:**

- **Rules in:** unambiguous v(N+1) per commit (FIFO ordering);
  serializable amendment history; "amendment took the lock for X
  seconds" is a meaningful telemetry signal; orphaned-lock recovery
  via dead-PID-check on boot (FM-27).
- **Rules out:** concurrent amendments racing on the same vN→vN+1
  bump; per-context partial amendments that touch cross-context
  state; rebase semantics on pass completion.
- **Performance:** amendments are slow under heavy contention
  (FIFO blocks). Mitigated by: amendments are expected to be
  infrequent (require integrator-level work); queue-growth alert
  surfaces pathological rates (FM-28).
- **Conservative-fallback signal:** arrows that declared no
  dependencies get aborted on every amendment. The count of
  conservatively-invalidated arrows is a quality signal that
  dependency declarations are missing. Operator tooling surfaces
  this for triage.
- **Mid-phase invalidation:** if a pass is in the adversarial,
  remediation, or verification phase when an amendment lands, the
  pass aborts mid-phase. Useful signal (findings) is preserved
  with the grid-version tag; the partial iteration state is
  discarded. State-space-frame rationale (ADR-002): invalidation
  is an operator that resets cells.

See `gates.md` §7.2 "Mid-phase invalidation";
`specs/architecture/components/amendment.md`;
`specs/features/amendment.feature`;
`specs/failure-modes.md` FM-25, 27, 28.
