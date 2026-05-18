# Build Notes — ghyll gate/arrow enforcement

A brief for the Claude Code session that will build this. **This is not a
role file. Do not load it into the harness.** It states what is designed,
what is deliberately not yet built, and what decisions remain with the
operator. Its purpose is to stop a builder from confabulating the
unbuilt parts.

---

## What these documents are

- **`direction.md`** — the v2 positioning rationale. Read first.
- **`gates.md`** — the harness-wide gate schema. The design. Build
  *from* it. Reconciled to operator-decisions rounds 1, 2, and 3.
- **`roles/{analyst,architect,implementer,integrator}.md`** — the
  four role contracts reconciled to `gates.md`.
- **`validation-pass-1.md`** — cold-read findings against the
  pre-reconciliation `gates.md` + `roles/analyst.md`.
- **`phase-3-architect-findings.md`** — architect-lens findings on
  the reconciled schema (15 items).
- **`operator-decisions-round-1.md`** — D1–D7 (catalogue, strata,
  weight, v0 grid, residue).
- **`operator-decisions-round-2.md`** — D8–D10 (`unable-to-hint`
  collapses; entry preconditions are upstream exit clauses;
  invalidation is hybrid).
- **`operator-decisions-round-3.md`** — D11–D20 (artifact IDs,
  arrow/pass identity, per-concept schemas, state-space framing,
  severity enum, dependency granularity, mid-phase invalidation,
  language-binding init policy, attestation records, init
  auto-propose).

## Provenance

This design was derived from a diagnosis of the Kiseki project, where
work reported complete proved substantially incomplete on GCP
deployment. The key finding, established by an artifact-level audit:

**The role definitions were already good.** The Kiseki `auditor.md`
already defined a STUB/SHALLOW/MOCK/NETWORK depth ladder and a
falsifiability gate. The failure was not missing definitions. It was
that definitions were **prose, not enforced** — no machine check
required the auditor to run, no precondition required an adversarial
gate to pass before the implementer started, and the integrator role
was strong but simply never evidenced.

So this work is **not** a redesign of the roles. It is the enforcement
layer the roles always needed. Do not rewrite the role definitions'
*content*; wire their *gates*.

The audit bucket counts (one NFS bounded context, 40 requirements):
never-specified 0 (but the audit only walked spec→code, so this is
unmeasured, not zero), specified-never-attempted 1, specified-but-shallow
~17, claimed-done-but-absent 1 row covering 23 untested ops. The dominant
failure is **shallow** — tests that run the code but assert too little.
The schema's defenses against this are `mutation-score` machine clauses
and `depth-sensitive` + `attested` clauses. They are defenses, not
proofs.

---

## What is designed and ready to build

- The gate schema: evaluation types, depth types, the clause / arrow /
  finding state machines, routing, hints, attestation, the arrow grid,
  on-the-spot arrow creation, the concept-named machine-clause
  catalogue, the v0 grid bootstrap.
- The analyst role contract, reconciled to the schema.

All 15 validation-pass-1 findings are resolved across rounds 1+2 +
prior commits. Of the 15 phase-3 architect-lens findings, 13 are
resolved by round 3 + prior commits; the remaining 2 are
implementation concurrency primitives (see below), not schema work.

## What is NOT built — do not confabulate these

- **The harness enforcement itself.** `gates.md` describes behavior;
  nothing yet enforces it. The machine clauses are only real once
  ghyll actually runs the checks (build targets, mutation runs,
  trace-link checks) and refuses transitions on failure. Until then
  every clause is self-reported prose — the original Kiseki failure.
- **The integrator gate.** The forensic root cause of the motivating
  failure was the integrator transition being unenforced. The
  non-skippable cross-context integrator gate is the single
  highest-value enforcement target and is not yet specified beyond
  the role contract.
- **Project initialization tooling.** `gates.md` §2 describes it;
  no implementation exists. This is the must-have step-one when ghyll
  is invoked on a new project; the schema cannot run without it.
- **The per-concept schema files** (`gates/concepts/<concept>.yaml`,
  one per catalogue concept). `gates.md` §5.1 describes the shape;
  no schema files exist yet. The harness ships these but they are
  not written.
- **The catalogue's per-language instrument bindings.** `gates.md`
  §5.1 names the concept; bindings (`lint-clean.go`,
  `no-orphan-symbol.rust`, etc.) are per-project config declared at
  initialization (D18). Harness ships NO defaults; first projects
  will exercise this path.
- **Concurrency primitives.** Two architect-pass findings (#9, #11)
  call for process-level coordination: locking against concurrent
  traversals on adjacent arrows; the coordinator backing
  `single-active-role-instance`. These are implementation, not
  schema, but need to be designed before the spine ships.

## Build order (recommendation, not instruction)

1. **Per-concept schema files** (`gates/concepts/*.yaml`). Without
   them the catalogue is unimplementable. Smallest piece; do first.
2. **Project initialization** (`gates.md` §2). The auto-propose flow
   that turns v0 + role-file templates into a project's vN grid.
   Without init, no other arrow can run.
3. **Harness machine-clause runner + transition refusal.** The
   enforcement spine. Reads per-concept schemas, runs evaluators,
   refuses transitions on failure.
4. **The integrator gate** — highest-value enforcement target; was
   the motivating-project root cause.
5. **Per-arrow adversarial phase** including the depth-classification
   sub-activity (`gates.md` §11).
6. **On-the-spot arrow creation.**
7. **Routing** (`gates.md` §8) — can be stubbed at single-tier
   initially; made real once the spine works.

Do not build routing before enforcement; an unenforced router is
decoration. Do not build adversarial-phase before the machine-clause
runner; findings without a way to block arrows are decoration.

---

## Decisions still owned by the operator — do NOT decide these in code

- **Model tiers.** The schema is tier-agnostic. The type→model config is
  separate and unwritten. Open question: ghyll's three-model lineup vs.
  a two-model (one fast, one deep) set to cut per-model tuning surface.
- **On-the-spot ceremony weight** (`gates.md` §12). Heavy (operator
  stops and thinks, expensive on purpose) vs. light-provisional (agent
  proposes, operator rubber-stamps now, real attestation forced before
  merge). The light option is only safe if the pre-merge attestation is
  itself machine-enforced and unskippable.
- **Whether the definition phase is operator-authored or
  agent-authored.** The schema assumes operator-owned, agent-assisted.
  Agent-authored would need its own gate and risks regress.
- **`unevaluated` escalation policy.** When a `depth-sensitive` clause
  exceeds every available model tier, it routes to operator attestation.
  Whether that is acceptable at volume, or whether such clauses should
  block the project, is an operator call.

---

## Known limits — carry these forward, do not let the harness obscure them

1. The schema enforces *that work happened and was checked*. It does not
   enforce *that the work is good*. Depth remains human-guarded.
2. `attested` clauses depend on operator attention. Attestation weight
   allocates attention; it cannot manufacture it. A rigid harness that
   demands many attestations trains rubber-stamping.
3. The arrow grid is only as good as the definition phase. Residue count
   and on-the-spot interruption count are the signals of a weak
   definition phase — surface them, do not smooth them.
4. This design was produced in a long conversation — the medium whose
   drift motivated the project. Validation pass 1 was a cold read of
   the original `gates.md` + `roles/analyst.md`; rounds 1–2 reconciled
   to operator decisions; phase 3 was an architect-lens cold pass;
   round 3 (phase 4) resolved the architect-found gaps. The schema's
   contracts are now typed. Before any code: spawn another cold-read
   pass against the round-3-reconciled `gates.md` to confirm the
   typing actually holds.
