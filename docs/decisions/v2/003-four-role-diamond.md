# ADR-003: Four-role diamond; no standalone adversary or auditor

**Status:** Accepted (2026-05-18; round 4 Q1, then phase 4 corrections)

**Context:**

The initial design considered a five- or six-role diamond
(analyst → architect → [adversary?] → implementer → auditor →
integrator). Operator decisions then dropped the standalone
adversary role in favor of an adversarial *phase* of every arrow.
Phase 4's mid-flight correction further dropped the standalone
auditor role on the same logic: depth classification, like
adversarial scrutiny, should be a phase of every arrow rather than
a role in the sequence.

The discriminator: "a role in a sequence can be skipped; a phase of
a transition cannot."

**Decision:**

The diamond is exactly four roles:

```
analyst → architect → implementer → integrator
```

There is **no standalone adversary role** and **no standalone
auditor role**. The work those roles would have done becomes the
universal **adversarial phase** on every depth-sensitive arrow:

1. Clause-falsification (try to make declared depth-sensitive
   clauses fail).
2. Open sweep (find defects no clause names).
3. Depth classification (walk requirements on the depth ladder).

Each diamond arrow that carries any `depth-sensitive` clause runs
this adversarial phase first. Pure-machine arrows run verification
only.

**Consequences:**

- **Rules in:** uniform adversarial scrutiny on every arrow that
  matters; depth classification on every arrow that has
  depth-sensitive requirements; no skippable role.
- **Rules out:** writing `adversary.md` or `auditor.md` as role
  files; bypass paths where "this arrow doesn't need an audit"
  becomes possible.
- **Implementation:** the adversarial-phase orchestrator is a
  separate component (`adversarial.md`); spawns the synthetic
  `adversary` role-id per `ADR-004`.
- **Constraint:** the producer of an arrow cannot attack itself.
  Enforced structurally via the synthetic role-id mechanism.

See `gates.md` §1, §11; `specs/architecture/components/adversarial.md`.
