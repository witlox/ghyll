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
  *from* it. Reconciled to operator-decisions rounds 1 and 2.
- **`roles/analyst.md`** — the analyst role file, reconciled to
  `gates.md`. The only role reconciled so far.
- **`validation-pass-1.md`** — cold-read findings against the
  pre-reconciliation `gates.md` + `roles/analyst.md`.
- **`operator-decisions-round-1.md`** — D1–D7 (catalogue, strata,
  weight, v0 grid, residue).
- **`operator-decisions-round-2.md`** — D8–D10 (`unable-to-hint`
  collapses; entry preconditions are upstream exit clauses;
  invalidation is hybrid).

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

The schema closes 12 of 15 validation-pass-1 findings. The remaining
three are phase 3 work:

- Severity enum for findings (the threshold that triggers `blocked`).
- `no-orphan-symbol` language binding mechanism (G4 in `analyst.md`).
- Per-concept default attestation cost values (the *defaults*
  themselves — the unit set is fixed in `gates.md` §10.1).

## What is NOT built — do not confabulate these

- **The other three role files.** `architect.md`, `implementer.md`,
  `integrator.md` are drafted but have not yet been re-validated after
  the phase-3 architect pass. They must remain reconciled to the
  evolving `gates.md`; expect another reconciliation after phase 4.

  (There is NO standalone adversary role and NO standalone auditor
  role. Adversarial scrutiny and depth classification are *phases* of
  every depth-sensitive arrow per `gates.md` §11. Do not write
  `adversary.md` or `auditor.md`.)
- **The harness enforcement itself.** `gates.md` describes behavior;
  nothing yet enforces it. The machine clauses are only real once ghyll
  actually runs the checks (build targets, mutation runs, trace-link
  checks) and refuses transitions on failure. Until then every clause is
  self-reported prose — the original Kiseki failure.
- **The integrator gate.** The forensic root cause of the Kiseki GCP
  failure was the integrator transition being unenforced. The
  non-skippable cross-context integrator gate is the single
  highest-value enforcement target and is not yet specified.
- **Project initialization tooling.** `gates.md` §2 describes it;
  no implementation exists. This is the must-have step-one when ghyll
  is invoked on a new project; the schema cannot run without it.
- **The catalogue's per-language instrument bindings.** `gates.md`
  §5.1 names the concept; bindings (`lint-clean.go`, `compiles.rust`,
  etc.) are per-project config declared at initialization and not yet
  written.

## Build order (recommendation, not instruction)

1. Harness machine-clause runner + transition refusal — the enforcement
   spine. Without it nothing else is real.
2. The integrator gate — highest-value, was the GCP root cause.
3. Reconcile the remaining four role files to `gates.md`.
4. The definition phase.
5. On-the-spot arrow creation.

Routing (§7) and the depth-type model can be stubbed initially — a
single tier — and made real once the spine works. Do not build routing
before enforcement; an unenforced router is decoration.

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
   the original `gates.md` + `roles/analyst.md`; phase 2 reconciled to
   operator decisions; phase 3 (architect-lens pass on the reconciled
   schema) is the next checkpoint before any code is written.
