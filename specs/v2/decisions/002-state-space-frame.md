# ADR-002: State-space-iteration as the conceptual frame

**Status:** Accepted (2026-05-18; D14)

**Context:**

After three rounds of operator decisions on the schema, the
mechanics (arrows, clauses, passes, statuses, findings,
invalidation, grid amendment) were typed but lacked a unifying
mental model. The operator proposed framing the system as "a vector
space with transitions, that has iterations over the space."

The framing helps several open questions at once: pass/arrow
identity (operators vs iterations), concept schemas (function
signatures in an operator algebra), and the discipline that every
transition must be reproducible from `(state, operator)`.

**Decision:**

`gates.md` is read through the lens of a **state-transition system
over an extensible grid**. The vocabulary is fixed:

- **Cells** are points in the state space:
  `(stratum, bounded-context)` pairs, each holding clause statuses,
  findings, and a derived arrow status.
- **Arrows** are **operators** — transition functions.
- **Passes** are **iterations** — one application of an operator.
- **Invalidation** is an operator that resets cells to an earlier
  state.
- **Grid amendment** is an operator that *extends* the state space
  (adds dimensions).
- The **fixed point** is "every cell in vN has arrow status
  `complete`, R = 0, C = 0." Convergence is not guaranteed.

The accurate formal name is **Kripke structure over an extensible
state space** — not a vector space, since there is no linearity,
scaling, or addition over cells.

**Consequences:**

- **Rules in:** discipline that every operator must be well-defined
  on the state; pass identity = iteration index; arrow identity =
  operator identity. State-space comparisons (distance to fixed
  point, residue R as a scalar) are first-class.
- **Rules out:** silent transitions, hidden global state, time-
  dependent operator behavior that the cell state doesn't capture.
- **Implementation guidance:** concept schemas typed as operator
  signatures; finding state machine bounded by the enumerated set
  (no ad-hoc additions); locks owned at the boundaries between
  state space sub-aggregates (per-clause, per-(role, context),
  project-wide).

See `gates.md` §0 for the explicit framing, and
`specs/v2/domain-model.md` "State-space model" section for the
implementation-facing summary.
