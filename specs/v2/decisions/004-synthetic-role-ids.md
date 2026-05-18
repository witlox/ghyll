# ADR-004: Synthetic role-ids (`init`, `adversary`) distinct from role files

**Status:** Accepted (2026-05-18; D21)

**Context:**

With no standalone adversary or auditor role (ADR-003), the
per-arrow adversarial phase still needs an identity:

- Who is the producer of the init arrow's attested clauses (since
  no role file owns init)?
- What identity appears in the attestation path
  (`<role-pair>`) when the adversarial phase runs?
- How does a fresh model instance with clean context get
  distinguished from the upstream producer in audit logs?

`gates.md` §1 says "no standalone adversary role" but `gates.md`
§10.2 records attestations with a `<role-pair>` path. Cold-pass
finding #5 surfaced the contradiction: §11 needs a "separate
instance from the producer" but no role-id exists for that
separation.

**Decision:**

The harness carries **synthetic role-ids** that are NOT role files:

| Synthetic role-id | Use |
|---|---|
| `init` | Producer for the initialization arrow's `attested` clauses. Bound to a fresh harness instance at project start. |
| `adversary` | Producer for the per-arrow adversarial phase. Bound to a fresh model instance with clean context per round. |

These identities appear in attestation paths and finding
provenance. Path encoding uses `__` (double underscore) as the
separator:

- `init__analyst` — the init arrow's path
- `analyst__adversary__architect` — the analyst→architect arrow's
  adversarial-phase path
- `implementer__adversary__integrator` — same pattern for
  implementer→integrator

This does **not** contradict ADR-003: `adversary` as a *role-id*
(identity) is structurally distinct from `adversary` as a *role
file* (contract). The schema forbids the file, not the identity.

**Consequences:**

- **Rules in:** clean attribution in audit logs; structural
  enforcement that producer cannot attack itself (different
  role-ids); a stable path encoding for filesystem-safe `<role-pair>`
  components.
- **Rules out:** adding a `init.md` or `adversary.md` role file;
  conflating role contract with role identity.
- **Implementation:** the synthetic role-ids are reserved strings
  the harness owns; not declarable at init. Adding a new
  synthetic role-id requires harness changes, not project config.
- **Path-safety:** `__` is filesystem-portable; no Unicode glyphs,
  no path separators, ≤ 255 bytes per component. Cold-pass finding
  #13 was that the original path encoding (using `→`) wasn't
  filesystem-safe; this fixes it.

See `gates.md` §1.1, §10.2;
`specs/direction/operator-decisions-round-4.md` D21.
