# Validation pass 3 — component specs cold read

Fresh-context implementer-lens audit of the seven component specs +
`gates.md` + role files on 2026-05-18. Same pattern as
`validation-pass-1.md` and `validation-pass-2.md` but on a different
surface: this pass walks an implementer through "I'm about to write
code from this; what blocks me?"

**Verdict.** Not ready for implementation. The schema is internally
tight (4 operator-decision rounds + 3 validation passes); the
component layer is a first draft and shows it. Day-one blockers
cluster around three load-bearing things the schema gestures at but
no component pins down:

1. **On-disk grid/clause format** that init writes and everyone else
   reads.
2. **API surface between components** (runner / state-machine /
   adversarial / attestation all reference each other but no
   signatures, no concurrency contract).
3. **Enum-set divergences** where a component invented a status /
   reason / concept that the schema doesn't define.

One reconciliation pass closes most of this without re-opening
operator decisions.

---

## Findings (hardest first)

### Day-one blockers

1. **[schema-drift]** `unable-to-hint` finding type undefined.
   `gates.md` §7.1/§9 and `runner.md` F-2 scenario 3 require the
   producer to "raise a finding of type `unable-to-hint` against
   itself," but `gates.md` §7.3 only enumerates
   `clause-falsification` / `open-sweep` / `depth-below-min` as
   finding types, and `adversarial.md` is the only documented
   finding-producer.

2. **[schema-drift]** N-round default contradicts schema. `gates.md`
   §10 mandates `N=3` rounds for `insufficient-basis`;
   `adversarial.md` invariant 6 and F-5 scenario 4 declare `N=5`
   for remediation non-convergence. Both are called "N" and init
   treats N as one configurable knob. The implementer cannot tell
   whether one N or two.

3. **[implementability]** Producer↔orchestrator messaging
   unspecified. `adversarial.md` F-5 says "the producer signals the
   orchestrator that F1 is addressed" — but no API, queue, message
   shape, or transport is given for producer-fix-signal,
   accepted-risk-proposal, or hint-emission-request.

4. **[schema-drift]** `reason: requires-deeper-artifact` is not in
   the pass-abort enum. `attestation.md` F-3 scenario 3 sets pass
   status `aborted` with that reason, but `gates.md` §7.1a enum is
   `{invalidated, operator-interrupt, crash, manual-stop}`.

5. **[component-drift]** Single-`grid.yaml` vs versioned files.
   `init.md` F-6 writes `.ghyll/grid.yaml` (one file, overwritten);
   `amendment.md` F-3 writes `.ghyll/grid.v(N+1).yaml` + a
   `grid.current` symlink. Two different on-disk layouts for the
   same artifact.

6. **[schema-drift]** `accepted-risk` clause status is invented.
   `attestation.md` F-3 scenario 2 says the clause status becomes
   `pass` with `accepted-risk` metadata. But `gates.md` §7.1 has
   no such metadata, and `accepted-risk` is only a *finding* status
   in §7.3.

7. **[component-drift]** Init's bounded-context discovery has no
   clauses or arrow. `init.md` F-1 has init interrogate the
   operator to identify contexts, but `gates.md` §2.3 lists init's
   exit gate without any bounded-context-existence clause; and
   `single-active-role-instance` requires `(role, context)` keys
   before any context exists. Circular dependency.

8. **[schema-drift]** Clause `running` status undefined.
   `runner.md` invariant 3 names `pending / running / post-evaluation`
   and F-1 scenario 1 transitions `pending → running → pass`.
   `state-machine.md` lists only the §7.1 set (no `running`).

9. **[implementability]** Concurrency primitives inconsistent.
   `runner.md` declares a "per-clause lock"; `state-machine.md`
   FM-3 says "if concurrent updates somehow reach the engine, it
   accepts the later timestamp"; `attestation.md` FM-2 has "its own
   serialization." Three different locks alluded to; ownership
   unclear.

10. **[component-drift]** Init exemption from the queue vs init
    taking the lock. `amendment.md` invariant 8 says init takes the
    lock at the end; `init.md` cross-component says the lock is
    always available. Implementer cannot tell whether init competes
    or bypasses.

### High-impact gaps

11. **[role-drift]** Role-file clause-ID references don't appear in
    init's auto-propose schema. `init.md` F-5 references
    `analyst.G1 = unique-definition(...)`, but no component specifies
    how role-file clause IDs are persisted in `grid.yaml` or
    resolved by the runner — and role files use markdown tables,
    not YAML.

12. **[operator-flow]** Init's adversarial phase acknowledged as
    unspecified. `init.md` open question 3 admits the adversarial
    attack on init's proposed grid is "unusual ... may need extra
    detail" — yet `gates.md` §2.3 requires it. Blocks whoever
    builds init first per build-notes order.

13. **[implementability]** Attestation path encoding for
    `adversary` arrow segment. `gates.md` §10.2 / §1.1 say
    `<role-pair>` may be `analyst→adversary→architect`;
    `attestation.md` F-2 writes plain `<pass-id>.jsonl` without
    showing how a three-segment role-pair becomes a single path
    segment.

14. **[internal-consistency]** Severity-on-clause-falsification rule
    unstated. `adversarial.md` invariant 4 says "severity assigned
    per finding"; F-2 scenario 2 hard-codes `severity: high`.
    `gates.md` §7.3 says severity follows "a stated rule" — no
    component states it.

15. **[internal-consistency]** Re-attack scope ambiguity.
    `adversarial.md` F-5 scenario 1 says re-attack is "scoped to
    F1's target"; F-5 scenario 3 says "next round R1 re-attacks
    all three" findings. Open question 1 admits the contradiction.

16. **[state-machine-edge]** Finding `grid-version` tag vs arrow-id
    `grid-version`. `gates.md` §7.1a fixes arrow-id as
    `(role-pair, stratum, context, grid-version)`; on a re-traversal
    against v(N+1), it is a different arrow-id. The "arrow's
    status" query semantics across grid versions are undefined.

17. **[component-drift]** Project-status `R` computation diverges.
    `gates.md` §3.3 / §7.4 say R is sum of operator-action cost
    across undeclared cells; `state-machine.md` F-5 scenario 3
    computes `R = 5 cells × 15 = 75` assuming uniform per-cell
    cost. The auto-propose cost lookup (which role? which binding?)
    is not in any component.

18. **[implementability]** Hint-request "timeout" undefined.
    `runner.md` FM-5 says "after timeout, the clause is recorded
    `unevaluated` with reason `producer-no-response`" — but
    `gates.md` §7.1's `unevaluated` reasons are only
    `depth-below-required` and `no-rule-selectable-locations`.

19. **[operator-flow]** Operator UI surface is fragmented across
    init prompts, attestation prompts, escalation prompts, refusal
    prompts, and aborted-pass notifications. No component owns the
    operator-facing channel or guarantees the operator sees an
    aborted pass.

20. **[state-machine-edge]** `single-active-role-instance` cross-pass
    semantics. `concepts.md` says the concept queries "the harness's
    coordination layer (lock/lease registry)"; `runner.md` F-6
    scenario 2 says the runner refuses the second pass — but the
    concept is also a *clause* during verification. Order of
    operations between pre-spawn refusal and clause evaluation is
    unspecified.

21. **[state-machine-edge]** Crash-recovery contention.
    `state-machine.md` F-6, `runner.md` FM-6, and `amendment.md`
    FM-6 all handle restart with no declared order of execution
    at boot.

22. **[component-drift]** Brownfield `divergences.md` flow not
    represented in init. `analyst.md` G5/G13 reference
    `divergences.md`; `init.md` F-2 outputs "residue candidates"
    not divergences. Producer of `divergences.md` is unclear at
    init time vs analyst-arrow time.

23. **[role-drift]** Producer identity for `init` arrow's
    adversarial phase. `gates.md` §1.1 declares `init` and
    `adversary` as synthetic role-ids (no role files), yet
    `adversarial.md` invariant 8 requires the adversary to read
    "the producer's fix output" — for init there is no producer
    role file to fix.

### Medium / structural

24. **[internal-consistency]** `mutation-score` depth-type
    contradiction. `implementer.md` G2 declares it `depth-robust`;
    `runner.md` F-1 scenario 3 shows it as `depth-sensitive`.
    Catalogue is silent.

25. **[schema-drift]** `predicate-form` on `findings.type`.
    `integrator.md` G4 uses `predicate-form(findings.type)`; the
    concept's spec in `concepts.md` is about prose-vs-invariant
    text shape. Applying to an enum-field check stretches it.

26. **[implementability]** `grid.yaml` schema not specified. Init
    writes a grid file, runner reads it, amendment rewrites it —
    no component defines the YAML schema (clauses-list shape,
    dependency-declaration shape, residue-list shape).

27. **[implementability]** Where role-file templates live at
    runtime. Init reads `roles/*.md` to know each role's exit-gate
    template — but role files are markdown tables. No component
    spec covers parsing them into typed clause structures.

28. **[schema-drift]** Two paths to arrow status. `attestation.md`
    F-2 scenario 3 says the attestation flow "sets" arrow status
    `provisional`; `state-machine.md` invariant 2 says derivation
    is pure from clause+finding state.

29. **[implementability]** No spec for how an arrow declares its
    "producer role." `adversarial.md` F-5 talks about "the producer
    role"; `runner.md` F-2 says "the producer role being `analyst`."
    For init / on-the-spot / amendment-resulting arrows, attribution
    is ambiguous.

30. **[internal-consistency]** `no-orphan-symbol` mapper for
    spec→implementation only. `analyst.md` G4 uses
    `no-orphan-symbol(exported-behaviours)` over specs, but
    `concepts.md` describes it as a code-side check requiring an
    `extractor` over source files.

### Lower priority

31. **[implementability]** Coverage-claim artifact path is not
    declared. Analyst G6, architect G6, implementer G6 all use
    `arrow-artifact-present(<role>→<role> <artifact-name>)` — no
    on-disk location convention.

32. **[component-drift]** Init `op-id` declared at session start
    vs attestation flow. `init.md` invariant 9 and `attestation.md`
    F-1 both declare op-id at session start. No precedence rule
    for re-init.

33. **[schema-drift]** `unevaluated`-severity finding is `open` or
    its own status? `gates.md` §7.3 says "treated as blocking by
    `no-open-finding`"; the finding-status enum is only
    `{open, resolved, accepted-risk}`. Where does
    `unevaluated`-severity live?

34. **[implementability]** Adversarial audit-trail file location
    never defined. `adversarial.md` F-2 scenario 3 writes to "an
    adversarial-phase audit trail" — not in `attestations/`, not
    in `findings.md`, not in the checkpoint log.

35. **[implementability]** Cross-binding aggregation explicitly
    deferred. `concepts.md` open question 2 punts the
    Go + TypeScript mix. For a coding agent targeting heterogeneous
    repos, this is structural.

---

## What needs operator decisions (round 5)

Most findings are mechanical reconciliation an architect-equivalent
can apply without operator input. Three are genuinely ambiguous and
worth surfacing:

- **Two-N question** (finding #2): one N or two, with what names?
- **Grid on-disk format** (finding #5): single file vs versioned?
- **Re-attack scope** (finding #15): targeted vs always-full?

The rest are reconciliation work, applied in the same commit as
round 5.
