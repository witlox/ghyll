# ADR-008: Arrow identity is structural; passes are iterations

**Status:** Accepted (2026-05-18; D12, D29)

**Context:**

Phase-3 architect finding #10: "Per-pass status with checkpoint
history is asserted, but pass identity is undefined. Is a
re-traversal after `invalidated` a new pass on the same arrow, or
a new arrow instance?" Without a clear answer, the state machine
has no identity to apply state to, and history-replay / finding
carryover are undefined.

In the state-space-iteration frame (ADR-002), arrows are *operators*
and passes are *iterations*. They are conceptually distinct and
should have distinct identities.

**Decision:**

**Arrow identity** = `(role-pair, stratum, bounded-context, grid-version)`.
Immutable.

- Re-traversal after `invalidated` against the same grid version
  is a *new pass on the same arrow*.
- A new grid version produces new arrow-ids for every dependent
  cell. The schema is monotone in grid version.

**Pass identity** = a unique `pass-id` (UUID or timestamp-based)
with attributes:

```
{ pass-id, arrow-id, started-at, completed-at, pass-status }
```

**Pass status set:** `running`, `completed`, `aborted`.

`aborted` carries a `reason` field (D29 added the enum to handle
non-invalidation aborts):

- `invalidated` — upstream amendment per ADR-012; arrow status
  becomes `invalidated`; findings preserved with grid-version tag.
- `operator-interrupt` — operator stopped the pass; arrow status
  unchanged.
- `crash` — harness or model failure; arrow status unchanged.
- `manual-stop` — operator closed session mid-pass; arrow status
  unchanged.
- `requires-deeper-artifact` — operator routed an
  `insufficient-basis` clause upstream after N rounds (ADR-009
  + `gates.md` §10).

Only `invalidated` aborts change arrow status. Other abort reasons
leave the arrow at whatever the latest *completed* pass
established.

**Consequences:**

- **Rules in:** clean separation of "what operator is being applied"
  (arrow) from "which application" (pass); per-pass checkpoint
  history; deterministic state transitions on re-traversal.
- **Rules out:** "the arrow's status changed mid-pass" semantics
  (status changes are between completed passes); invalidation as a
  hidden side effect (only changes arrow status when explicitly
  signaled).
- **Implementation:** an arrow's *current* status is the latest
  pass's derived status. Pass history lives in the checkpoint log
  (per ADR-009 ownership); in-memory state holds only `running`
  passes.
- **Cross-version queries:** "current status of arrow A" returns
  the status of the latest pass that matches the arrow's
  `(role-pair, stratum, context)` triple, irrespective of
  `grid-version`. Historical pass queries can scope to a specific
  grid-version.

See `gates.md` §7.1a; `specs/domain-model.md` §"Arrow", §"Pass";
`specs/ubiquitous-language.md` §G.
