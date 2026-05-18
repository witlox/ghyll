# Invariants

Things that must always be true in v2 ghyll. Violations are bugs.

Consolidated from `specs/direction/gates.md` (the schema) and the
seven component specs in `specs/direction/components/`. Where a
component spec already declares an invariant, this document cites
the source rather than re-stating in detail.

Grouped by topic. Each invariant has a short name (for cross-ref)
and a one-line statement of what must hold.

---

## Workflow shape

1. **Diamond is four roles.** The default workflow is
   `analyst → architect → implementer → integrator`. There is no
   standalone adversary role and no standalone auditor role.
   (`gates.md` §1)
2. **Synthetic role-ids are identities, not contracts.** `init` and
   `adversary` are identities used in attestation paths and finding
   provenance. They are NOT role files. (`gates.md` §1.1)
3. **Project initialization is mandatory before any other arrow
   runs.** A project that has not been through init cannot enter the
   diamond. (`gates.md` §2; `init.md` INV-1)
4. **Transitions only along declared arrows.** A switch to a role
   with no declared arrow from the current role is refused; falls
   into on-the-spot arrow creation per §12. (`gates.md` §1)
5. **Init runs as a normal arrow against v0.** There is no special
   bootstrap mechanism; v0 ships with the harness. (`gates.md` §2.3,
   §3.4)
6. **Init is operator-owned, agent-assisted.** Auto-propose +
   operator-confirm-or-modify-or-extend-or-skip-with-residue. Grid
   is recorded only after every proposal has a verdict. (`gates.md`
   §2.2; `init.md` F-5)

## Identity and versioning

7. **Arrow identity is structural and immutable.** Arrow-id is
   `(role-pair, stratum, bounded-context, grid-version)`. Re-traversal
   after `invalidated` against the same grid version is a *new pass*
   on the *same arrow*. A new grid version produces new arrow-ids.
   (`gates.md` §7.1a)
8. **Pass-id is unique within a project.** Each pass has its own
   pass-id, `started-at`, `completed-at`, `pass-status`.
   (`gates.md` §7.1a)
9. **Bounded-context names unique within a project.** Enforced via
   `unique-definition` on the grid's `bounded-contexts` field.
   (`init.md` INV-2)
10. **Grid version is monotonic.** vN+1 is the only legal successor
    to vN. No rollback at the schema level. (`amendment.md`
    invariant 3)
11. **Cells declared OR residue.** Every `(stratum, bounded-context)`
    cell either has at least one arrow in the grid OR is named in
    the residue list. No silent omissions. (`gates.md` §3.3;
    `init.md` INV-3)

## State machine integrity

12. **Clause-status set is closed.** `pending | running | pass | fail
    | awaiting-attestation | insufficient-basis | unevaluated`. No
    other clause status exists. (`gates.md` §7.1)
13. **Arrow-status set is closed.** `complete | provisional |
    unevaluated | blocked | invalidated`. Derived from clause +
    finding state per §7.2 precedence; `invalidated` is the only
    status set externally (by the amendment process). (`gates.md`
    §7.2; `state-machine.md` invariants 2 and 4)
14. **Finding-status set is closed.** `open | running | resolved |
    accepted-risk | unevaluated`. (`gates.md` §7.3)
15. **Pass-status set is closed.** `running | completed | aborted`.
    `aborted` carries a required `reason` field from
    `{invalidated | operator-interrupt | crash | manual-stop |
    requires-deeper-artifact}`. (`gates.md` §7.1a)
16. **Finding-type catalogue is closed.** `clause-falsification |
    open-sweep | depth-below-min | unable-to-hint`. New types enter
    only via deliberate harness changes. (`gates.md` §7.3)
17. **Severity enum is closed.** `info | low | medium | high |
    critical`, plus `unevaluated` for severity that could not be
    assigned. (`gates.md` §7.3)
18. **Transitions are validated.** Every status change goes through
    the state-machine engine; illegal transitions are rejected.
    Direct mutation of the state store is impossible from outside.
    (`state-machine.md` invariant 1)
19. **Derivation is pure.** Arrow status derivation is a pure
    function of clause statuses + finding states + invalidation
    events. No side effects; same inputs always yield same output.
    (`state-machine.md` invariant 2)
20. **Per-pass clause status.** The store keys clause status by
    `(pass-id, clause-id)`. The same clause has independent status
    on each pass. (`state-machine.md` invariant 3)
21. **Clause status `pass` is independent of finding disposition.**
    A clause becomes `pass` when its evaluation succeeds; findings
    live on the arrow. An arrow with `pass` clauses but `open`
    findings above threshold is `blocked` via auto-inserted
    `no-open-finding`. (`gates.md` §7.3)

## Catalogue

22. **Catalogue is closed at the concept layer.** 17 named concepts
    (`compiles`, `lint-clean`, `no-todo-marker`, `every-step-bound`,
    `no-orphan-symbol`, `mutation-score`,
    `kill-server-fails-integration`, `trace-link-present`,
    `acyclic-dependency-graph`, `unique-definition`,
    `predicate-form`, `arrow-artifact-present`, `no-open-finding`,
    `cardinality-check`, `mode-determinable-from-repo`,
    `single-active-role-instance`, `every-requirement-meets-min-depth`).
    Concepts are language-agnostic. (`gates.md` §5.1)
23. **Universal base set is inherited.** `compiles`, `lint-clean`,
    `no-todo-marker`, `every-step-bound` apply to every arrow
    automatically. Roles may not opt out. (`gates.md` §5.2)
24. **Per-arrow instances are init-declared.** Non-base clause
    instances are derived per-arrow during init from the role file's
    exit-gate template; not predefined. (`gates.md` §5.3; D36)
25. **No language bindings shipped.** The harness ships concept
    schemas but no language bindings. Bindings are project-declared
    at init. Missing binding → init re-entry. (`gates.md` §5.1; D18)
26. **Auto-insertion on adversarial arrows.** Verification phase
    auto-inserts `no-open-finding` and
    `every-requirement-meets-min-depth` on arrows that ran an
    adversarial phase. (`gates.md` §11.3)

## Producer / operator separation

27. **Producers cannot self-certify attested clauses.** Operator
    verdict is required. (`gates.md` §5.4)
28. **Producers cannot accept their own risk.** Only the operator
    may attest `accepted-risk` on a finding. (`gates.md` §7.3)
29. **Adversary is structurally distinct from the producer.** The
    adversarial phase spawns a fresh model instance with clean
    context. The producer of an arrow cannot be its adversary.
    (`gates.md` §11; `adversarial.md` invariant 5)
30. **Fresh adversary per remediation round.** Each round spawns a
    new instance; no persistent adversary memory across rounds.
    (`adversarial.md` invariant 1)
31. **Clean adversary context.** Adversary input is bounded to: the
    upstream artifact, the arrow's clause definitions, the depth
    ladder, the routing config. Nothing from the producer's prior
    reasoning. (`adversarial.md` invariant 2)
32. **Full re-attack always.** Each remediation round re-attacks the
    entire upstream artifact, not only prior finding targets. Catches
    regressions. (`adversarial.md` D32)
33. **No silent finding dismissal.** A finding transitions to
    `resolved` only when a fresh adversary's re-attack confirms it
    is no longer reproducible. Producer reporting "fixed" is not
    sufficient. (`adversarial.md` invariant 7)

## Operator attestation

34. **`op-id` required for every attestation.** Each record has a
    non-empty `op-id`. The component refuses verdict capture if no
    operator session is active. (`gates.md` §10.2; `attestation.md`
    invariant 1)
35. **Append-only attestation log.** Records are appended, never
    modified or deleted. Multiple verdicts on the same clause in a
    single pass appear as multiple records in chronological order;
    the latest is authoritative. (`attestation.md` invariant 3)
36. **Unit-typed records.** Every attestation record matches one of
    the three unit shapes per `gates.md` §10.2 (`confirm`,
    `record-locations-inspected`, `write-residue-note`). The
    verifier checks for required fields per unit. (`gates.md` §10.2;
    `attestation.md` invariant 4)
37. **`insufficient-basis` counter per-clause-per-pass.** Resets when
    the pass aborts or completes. (`attestation.md` invariant 6)

## Hints

38. **Hints carry no verdicts.** Forbidden strings: `approve`,
    `looks good`, `sufficient`, `LGTM`, etc. The producing role
    points; the operator judges. (`gates.md` §9)
39. **Hints disclose residue.** A hint that highlights without
    saying what it skipped silently steers the operator away from
    unclassified content. Residue field is mandatory. (`gates.md`
    §9)
40. **Hints are rule-selected.** Locations are selected by a stated
    rule, not by the model's impression of risk. (`gates.md` §9)
41. **Unable-to-hint produces both a clause-status and a finding.**
    Clause status: `unevaluated` with reason
    `no-rule-selectable-locations`. Finding: type `unable-to-hint`
    raised by the role against itself. (`gates.md` §7.1, §7.3, §9)

## Concurrency

42. **Per-clause transition lock owned by state-machine engine.**
    Only one transition per `(pass-id, clause-id)` can be in flight
    at a time. (`state-machine.md` invariant 1; D34)
43. **Per-`(role, context)` lock owned by runner.** Enforces
    `single-active-role-instance` at pre-spawn time. (`runner.md`
    invariant 5; D34)
44. **Project-wide grid write-lock owned by amendment component.**
    Held during grid v(N+1) commit; init takes it once at end-of-init
    for the v1 write. (`amendment.md` invariant 1; D34, D35)
45. **Boot recovery order is fixed.** On harness restart:
    state-machine engine recovers first (marks orphan `running`
    passes `aborted: crash`), then amendment component recovers
    (validates `grid.current`), then runner becomes ready. (D34)
46. **No silent pass abort.** Every aborted pass records a `reason`
    field per `gates.md` §7.1a. (D29)

## Persistence

47. **Atomic grid write.** Each grid commit writes `.ghyll/grid.v<N>.yaml`
    (temp + rename), then updates `.ghyll/grid.current` (temp +
    rename). Partial-write states are not observable. (`gates.md`
    §2; `amendment.md` invariant 2; D31)
48. **Versioned grid files preserved.** Past grid versions stay on
    disk until an explicit retention policy removes them. Default:
    keep all. (D31)
49. **Per-pass attestation file at structured path.** One JSONL file
    per pass at
    `attestations/v<N>/<context>/stratum-<S>/<role-pair>/<pass-id>.jsonl`,
    where `<role-pair>` uses `__` as the separator between role-ids.
    (`gates.md` §10.2; D37)
50. **Findings preserve grid-version tag.** Findings carried over
    from an aborted-by-invalidation pass retain their original
    `grid-version` so they are distinguishable from findings on the
    new vN+1 arrow. (`gates.md` §7.2; D22)

## Refusal and enforcement

51. **Total refusal.** When an arrow's derived status is not
    `complete`, every downstream role transition is refused. No
    warnings-only mode. (`runner.md` invariant 2)
52. **Refusal is structural.** Refused transitions return a typed
    error including the failing clauses and the blocking arrow's
    id. The error is consumable by tooling. (`runner.md` invariant 7)
53. **Project status is never "complete."** It is reported as
    `(complete-against-grid-vN, residue R, unevaluated C)`. C > 0 is
    the "green but will break on deployment" signal and must never
    be hidden inside an aggregate pass. (`gates.md` §7.4)
54. **Residue R is computable.** Each undeclared cell's imputed cost
    is the sum of operator-action costs of the role's full
    exit-gate template under the project's language bindings. The
    formula has no free parameters. (`gates.md` §3.3; D27, D44)

## Depth model

55. **Depth-type is per-clause, declared at init, attested.**
    `depth-robust` vs `depth-sensitive` is assigned when the clause
    is authored. It is itself an `attested` item on init's exit
    gate. (`gates.md` §6)
56. **Routing is gate-driven, never self-assessed.** A pass runs at
    the lowest tier whose depth meets the maximum
    depth-sensitivity requirement across the clauses on its arrow.
    A model never decides its own routing. (`gates.md` §8)
57. **Below-depth evaluation produces `unevaluated`.** A
    `depth-sensitive` clause evaluated by a model below its required
    depth is `unevaluated`, not `provisional`. Never silently
    elevated. (`gates.md` §7.1)
58. **Depth ladder has exactly 4 tiers.** Project may rename labels
    at init; the tier count is fixed. (`gates.md` §11.1; `init.md`
    INV-6)
59. **Depth-classification is part of every adversarial phase.** It
    raises a finding for any requirement classified below its
    declared minimum. (`gates.md` §11; D26)

## Severity

60. **Severity assignment is depth-sensitive.** Assigning the
    correct severity to a finding is itself a judgement; below
    required depth, severity is `unevaluated` and the finding's
    status reflects that. (`gates.md` §7.3; D15)
61. **Severity threshold floor is `medium`.** Init may raise the
    project-wide threshold but not lower it below `medium`. The
    init arrow itself uses the harness-default `medium`.
    (`gates.md` §7.3; `init.md` INV-7; D25)

## Init-specific

62. **Init follows its own schema.** Init's exit gate is gated by
    the same machinery as any other arrow (machine clauses,
    attested clauses with hints, the three phases). It is not a
    special case. (`init.md` INV-5)
63. **`op-id` declared at session start.** Every attestation record
    contains a non-empty `op-id`; init refuses to begin until one
    is declared. (`init.md` INV-9; D43)
64. **Binding completeness.** Every language used by any declared
    scope has a binding declared for every concept that names that
    language. Missing binding → init re-entry. (`init.md` INV-4)

---

## Invariants the schema does NOT enforce

Stated explicitly so the harness is not mistaken for a guarantee it
does not provide (per `gates.md` §13):

- **Artifact depth.** The schema makes "was the check run, and did
  it pass" structural. It cannot make a thin spec deep or a shallow
  test meaningful.
- **Operator attention.** `attested` clauses depend on operator
  attention. Weight allocates attention; it does not create it.
- **Init quality.** The grid is only as good as init. Residue and
  on-the-spot interruption counts are signals of weak init; they do
  not prevent one.

These remain human-guarded responsibilities, made visible rather
than eliminated.
