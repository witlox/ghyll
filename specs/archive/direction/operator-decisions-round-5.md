# Operator decisions — round 5

Closes `validation-pass-3.md` (component-spec cold read). Three
operator decisions (D30–D32) plus a body of mechanical reconciliations
(D33–D44) applied in the same commit to bring the components into
alignment with `gates.md`.

The reconciliations are mostly enum closures, lock-ownership
clarifications, format definitions, and small drift fixes. The
operator decisions are the items where multiple resolutions were
defensible.

---

## Operator decisions

### D30 — Two distinct N knobs with distinct names

**Source:** finding #2.

The N rounds-before-escalation has two unrelated uses:

- `insufficient-basis-rounds-max` — used by attestation flow.
  Default `3`. After N rounds of `insufficient-basis` on the same
  clause, escalation: operator attests `accepted-risk` or routes
  upstream for deeper rework.
- `remediation-rounds-max` — used by adversarial phase. Default
  `5`. After N rounds of producer-fix → adversary-re-attack
  failing to resolve a finding, escalation to operator.

Both configurable at init. Both honest about their distinct purposes.

### D31 — Versioned grid files with `grid.current` pointer

**Source:** finding #5.

On-disk grid layout:

- Each commit writes a new `.ghyll/grid.v<N>.yaml`.
- `.ghyll/grid.current` is a small pointer file (one line:
  `v<N>`) naming the active version.
- Atomic update: temp file write + rename for `grid.v<N>.yaml`,
  then atomic rename of `grid.current.tmp` → `grid.current`.
- Init's first write produces `grid.v1.yaml` and
  `grid.current = "v1"`.
- Retention policy is operator-decided at init (default: keep all;
  may declare a max-age or max-count).

Aligns with `gates.md` §7.1a arrow-id including `grid-version`.

### D32 — Always full re-attack

**Source:** finding #15.

Each remediation round, the fresh adversary attacks the entire
upstream artifact from scratch — not scoped to prior findings. The
prior findings may be re-raised (`open`), confirmed-resolved
(`resolved`), or new ones may surface. The producer's fix to one
finding can inadvertently regress another; full re-attack catches it.

Cost is more model invocations per round. Mitigated by the
`remediation-rounds-max = 5` bound.

`adversarial.md` open question 1 resolved by D32.

---

## Mechanical reconciliations

Applied in the same commit. Each closes one or more cold-pass
findings; none reopens an operator decision.

### D33 — Enum closures (schema)

**Source:** findings #1, #4, #6, #8, #18, #33.

`gates.md` additions:

| Set | Addition |
|---|---|
| §7.1 clause-status set | Add `running` (active evaluation in progress) |
| §7.1 `unevaluated` reasons | Add `producer-no-response` (producer timed out responding to a hint request) |
| §7.1a pass-abort reasons | Add `requires-deeper-artifact` (operator routed the artifact for deeper upstream rework via D23 escalation) |
| §7.3 finding-type catalogue (new explicit enum) | `clause-falsification`, `open-sweep`, `depth-below-min`, `unable-to-hint` |
| §7.3 finding-status set | Add `unevaluated` (severity could not be assigned at sufficient depth) |

`attestation.md` correction: clause status does NOT become `pass`
"with `accepted-risk` metadata" — the metadata lives on the
*finding*, which gets status `accepted-risk` per §7.3; the *clause*
becomes `pass` once all its findings are disposed (resolved or
accepted-risk). Two distinct objects; remove the metadata-on-clause
confusion.

### D34 — Lock ownership

**Source:** findings #9, #20, #21.

Three locks, three owners. No others.

| Lock | Owner | Scope | Used by |
|---|---|---|---|
| Per-clause transition lock | state-machine engine | `(pass-id, clause-id)` | All callers proposing clause-status transitions |
| Per-`(role, context)` lock | runner | `(role-id, bounded-context-id)`; expires on pass-completion or abort | Enforces `single-active-role-instance` at pre-spawn |
| Project-wide grid write-lock | amendment component | Project | Held during grid v(N+1) commit; init holds at end of init for v1 write |

Boot order on harness restart: state-machine engine recovers first
(reads checkpoint log; marks orphan `running` passes `aborted` with
`reason: crash`); then amendment component recovers (validates
`.ghyll/grid.current` matches an existing file; alerts on
divergence); then runner becomes ready to accept new passes.

### D35 — Init takes the lock; D34's clarification

**Source:** finding #10.

Init takes the project-wide grid write-lock once, at the end of init,
to write `grid.v1.yaml` and `grid.current = "v1"`. At init-start
the lock is uncontested (no other arrow can run before init
completes — invariant 1 of `init.md`). The amendment queue is empty
at init time.

Statement in `amendment.md` invariant 8 stays correct; the
`init.md` cross-component reference is updated to acknowledge the
end-of-init lock acquire.

### D36 — Role-file → typed clause extraction

**Source:** findings #11, #27.

Role files (`roles/*.md`) are the authoring surface for the
operator-facing role contracts. At init time, the harness extracts
each role's exit-gate table into a typed in-memory clause set:

```yaml
# Extracted shape (illustrative)
role: analyst
exit-gate:
  - id: G1
    description: "Every term in ubiquitous-language.md appears exactly once"
    concept: unique-definition
    arguments: { artifact: "ubiquitous-language.md" }
    eval-type: machine
    depth-type: depth-robust
  - id: G7
    description: "Every feature has Gherkin scenarios for failure paths"
    concept: null  # attested judgements have no concept
    eval-type: attested
    depth-type: depth-sensitive
  ...
```

Parsing rule: each table row is one clause. The `Eval` and `Depth`
columns are required. The `Concept (machine) or attested judgement`
column carries either a concept invocation (machine clauses) or the
`(judgement)` literal (attested clauses). The clause ID is the
table's first column.

`init.md` will reference this extraction in F-5 auto-propose.
`runner.md` will reference it when invoking machine evaluators.

### D37 — Attestation path encoding for adversary segment

**Source:** finding #13.

`<role-pair>` directory segment in §10.2 paths uses `__` separator
between role-ids (filesystem-safe, no Unicode glyphs):

```
attestations/v1/contextA/stratum-3/analyst__adversary__architect/<pass-id>.jsonl
```

Two-segment role-pair: `analyst__architect`. Three-segment with
adversary: `analyst__adversary__architect`. Init's path:
`init__analyst`. Always one or more segments separated by `__`.

### D38 — `grid.yaml` schema

**Source:** findings #11, #26, #29.

The on-disk grid file shape (illustrative; the full schema lives in
`gates/grid.schema.yaml` shipped with the harness):

```yaml
# .ghyll/grid.v1.yaml
grid-version: 1
created-at: 2026-05-18T14:00:00Z
created-by-op-id: "alice@example.com"

bounded-contexts:
  - id: contextA
    description: "Payment processing"
  - id: contextB
    description: "User accounts"

language-bindings:
  lint-clean.go: "staticcheck && go vet"
  mutation-score.go: "go-mutesting"
  ...

depth-ladder:
  - tier: 0
    label: NONE
  - tier: 1
    label: SHALLOW
  - tier: 2
    label: MOCKED
  - tier: 3
    label: REALISTIC

severity-threshold: medium
insufficient-basis-rounds-max: 3
remediation-rounds-max: 5

arrows:
  - role-pair: init__analyst
    producer-role: init
    stratum: null   # init arrow is project-scoped, not stratum-scoped
    context: null   # nor context-scoped
    exit-gate-clauses: [...]
    dependencies: [...]
  - role-pair: analyst__architect
    producer-role: analyst
    stratum: 1
    context: contextA
    exit-gate-clauses: [...]
    dependencies:
      - artifact: "specs/contextA/domain-model.md"
        granularity: file
        on-change: invalidate
  ...

residue:
  - cell: { stratum: 5, context: contextA }
    reason: "deferred — low-stakes operational scenarios"
  ...
```

`runner.md`, `init.md`, `amendment.md`, `state-machine.md` all
reference this shape.

### D39 — Producer role attribution per arrow

**Source:** finding #29.

Every arrow in the grid carries an explicit `producer-role` field
(per the schema in D38). For diamond arrows the producer is the
upstream role:

| Arrow | Producer-role |
|---|---|
| `init→analyst` | `init` (synthetic, §1.1) |
| `analyst→architect` | `analyst` |
| `architect→implementer` | `architect` |
| `implementer→integrator` | `implementer` |
| `integrator→(completion or amendment)` | `integrator` |

For on-the-spot arrows: declared at the time of on-the-spot creation;
operator attests.

For amendment-resulting arrows: the producer is the *re-engaged
analyst*; the amendment component records this.

### D40 — Producer↔orchestrator messages

**Source:** finding #3.

Three message types between the adversarial-phase orchestrator and a
producer role:

```yaml
# Hint emission request (orchestrator → producer)
type: hint-request
pass-id: <id>
clause-id: <id>
upstream-artifact-ref: <path>

# Producer fix signal (producer → orchestrator)
type: producer-fix-signal
pass-id: <id>
addressed-findings: [<finding-id>, ...]

# Accepted-risk proposal (producer → orchestrator → attestation flow)
type: accepted-risk-proposal
pass-id: <id>
finding-id: <id>
rationale: <text>
inspected-context: <text>  # what the producer reviewed before proposing
```

Transport is in-process function calls (single-process v1 per
`init.md` A-4). Schemas live in `components/messaging.md` (a future
sub-spec); for now, the shapes above are the spec.

### D41 — Init's adversarial phase + bounded-context bootstrap

**Source:** findings #7, #12, #22, #23.

Init runs in two sub-phases internally, both within the single init
arrow:

**Sub-phase A — Project profile + context discovery.** Init
interrogates the operator (or scans the repo in brownfield mode)
to determine: bounded contexts, languages used, refusal-or-proceed.
The producer is the operator; the artifact is the proposed
context list. `single-active-role-instance(init, *)` skips the
context check (init is project-scoped, not context-scoped).

**Sub-phase B — Per-(role, context) auto-propose.** Once contexts
are declared, init iterates all `(role-pair, context)` arrows and
runs the auto-propose flow per F-5.

Init's adversarial phase attacks the proposed grid:

- A fresh `adversary` instance reads the proposed grid + the
  operator's context discovery rationale.
- Three sub-activities (per `adversarial.md` F-2/F-3/F-4):
  - **Clause-falsification:** are any declared arrows missing
    clauses that the role file template included? (Operator
    skipped without residue.)
  - **Open sweep:** is the residue list complete? Are there cells
    the operator didn't even consider?
  - **Depth classification:** for each declared dependency, is
    the granularity (`file` / `section` / `clause-id`) appropriate
    to the artifact's stability?
- Findings raise per the normal flow; init goes through
  remediation; verification auto-inserts `no-open-finding` +
  `every-requirement-meets-min-depth`.

Brownfield `divergences.md`: at init time, residue candidates from
sub-phase A become divergence-candidates that the analyst arrow
will materialize into `divergences.md` entries on its first
traversal.

### D42 — `unevaluated` finding handling

**Source:** finding #33.

A finding's status `unevaluated` (severity could not be assigned —
adversary was below required depth) is distinct from `open`. The
finding-status enum is `{open, resolved, accepted-risk, unevaluated}`.

`no-open-finding` blocks the arrow if any finding has status
`unevaluated` OR `open` above threshold. Re-traversal at deeper
tier may clear `unevaluated` (severity re-assigned) → finding
becomes `open` with its real severity → must then be `resolved` or
`accepted-risk`.

### D43 — Operator event bus

**Source:** finding #19.

A single **operator event bus** is the channel for all operator
communication. Owned by the attestation flow component (since it's
the primary operator-interaction component). Other components
publish events to the bus:

```yaml
# Examples
- type: attestation-request
  pass-id, clause-id, hint
- type: escalation-request
  pass-id, clause-id, reason: insufficient-basis-N-reached
- type: refusal-prompt
  project-profile, rationale
- type: pass-aborted
  pass-id, reason
- type: amendment-conflict
  grid-versions, alert
```

The operator UI subscribes to the bus. Implementation-detail; the
bus shape is small and stable; events are typed.

### D44 — Misc reconciliations

**Source:** findings #14, #17, #24, #25, #28, #30, #32, #35.

- **#14 — Severity rule.** `adversarial.md` adds: severity is
  assigned per a rule the adversary states in each finding's
  `basis` field (already required). The rule itself is at the
  adversary's discretion subject to depth-sensitivity routing; the
  hint emits the rule for operator review per §9.
- **#17 — Residue R formula.** `state-machine.md` clarifies: each
  undeclared cell's cost is `sum-over-clauses(default-cost)` for
  the role's exit-gate template (D36) under the project's
  language bindings (some concepts have per-binding default costs).
- **#24 — `mutation-score` depth-type.** Per-arrow declared at
  init. Default `depth-robust` for machine evaluation; an arrow
  can declare `depth-sensitive` if the threshold or scope choice
  involves judgement.
- **#25 — `predicate-form` on `findings.type`.** Replace with
  `cardinality-check(query: findings.type ∈ enum, expected: ≥1)`
  in `integrator.md` G4 — `predicate-form` is for prose-vs-predicate
  shape, not enum-field checks.
- **#28 — Attestation flow does NOT set arrow status.**
  `attestation.md` updated to say it signals state-machine
  engine to update *clause* status; arrow status derivation is
  pure from clause+finding state.
- **#30 — `no-orphan-symbol` over specs.** Concept's `extractor`
  is generalized: can run over source code OR specs (for the
  analyst's G4 use). Per-arrow `extractor` is declared with the
  clause instance.
- **#32 — op-id precedence.** Declared at session start (one
  surface, owned by the attestation flow per D43). Init reads
  the current session's op-id; no second declaration.
- **#35 — Cross-binding aggregation.** Arrows with multi-language
  scopes split per-language at init: operator declares one
  clause instance per language (e.g., `mutation-score.go(...)`
  AND `mutation-score.typescript(...)`). The arrow's exit gate
  has both; both must pass.

---

## Mapping back to validation-pass-3 findings

| Finding | Status after round 5 |
|---|---|
| 1 (`unable-to-hint` finding type) | resolved (D33) |
| 2 (two-N problem) | resolved (D30) |
| 3 (producer↔orchestrator messages) | resolved (D40) |
| 4 (`requires-deeper-artifact` reason) | resolved (D33) |
| 5 (grid format) | resolved (D31) |
| 6 (`accepted-risk` clause status) | resolved (D33) |
| 7 (bounded-context bootstrap) | resolved (D41) |
| 8 (`running` clause status) | resolved (D33) |
| 9 (concurrency primitives) | resolved (D34) |
| 10 (init exemption from queue) | resolved (D35) |
| 11 (role clause-ID resolution) | resolved (D36) |
| 12 (init's adversarial phase) | resolved (D41) |
| 13 (attestation path encoding) | resolved (D37) |
| 14 (severity rule) | resolved (D44) |
| 15 (re-attack scope) | resolved (D32) |
| 16 (grid-version arrow-id semantics) | resolved (D38 — explicit field) |
| 17 (residue R formula) | resolved (D44) |
| 18 (`producer-no-response`) | resolved (D33) |
| 19 (operator UI fragmented) | resolved (D43) |
| 20 (`single-active-role-instance` semantics) | resolved (D34) |
| 21 (crash-recovery contention) | resolved (D34) |
| 22 (brownfield divergences) | resolved (D41) |
| 23 (init/adversary producer identity) | resolved (D39, D41) |
| 24 (`mutation-score` depth-type) | resolved (D44) |
| 25 (`predicate-form` on `findings.type`) | resolved (D44) |
| 26 (`grid.yaml` schema) | resolved (D38) |
| 27 (role-file templates parsing) | resolved (D36) |
| 28 (two paths to arrow status) | resolved (D44) |
| 29 (arrow producer-role declaration) | resolved (D38, D39) |
| 30 (`no-orphan-symbol` over specs) | resolved (D44) |
| 31 (coverage-claim artifact paths) | resolved (D38 path convention) |
| 32 (op-id precedence) | resolved (D44) |
| 33 (`unevaluated` finding status) | resolved (D42) |
| 34 (adversarial audit trail) | folded into attestation logs (D37 + D40) |
| 35 (cross-binding aggregation) | resolved (D44) |

**Resolved: 35 of 35.** The schema and component contracts are now
aligned. Implementation can begin.

---

## Required document changes

Applied in the same commit:

**`gates.md`:**

- §1.1 — path encoding uses `__` separator (D37).
- §2.1 — `insufficient-basis-rounds-max` and
  `remediation-rounds-max` declared at init (D30).
- §7.1 — clause-status set adds `running` (D33);
  `unevaluated` reasons add `producer-no-response` (D33).
- §7.1a — pass-abort reasons add `requires-deeper-artifact` (D33).
- §7.3 — finding-type enum stated explicitly:
  `clause-falsification`, `open-sweep`, `depth-below-min`,
  `unable-to-hint` (D33); finding-status set adds `unevaluated`
  (D42).
- §10 — references `insufficient-basis-rounds-max` (D30).

**`components/concepts.md`:**

- `no-orphan-symbol` `extractor` generalized to specs OR code (D44).
- `predicate-form` clarified as prose-vs-predicate (D44 + remove
  enum-field overload).

**`components/init.md`:**

- F-5 references the role-file → YAML extraction (D36).
- F-6 writes `grid.v1.yaml` + `grid.current` (D31).
- New sub-phase split (project profile + context discovery vs
  per-context auto-propose) per D41.
- `op-id` declared via the operator event bus (D43).
- Adversarial-phase application clarified (D41).

**`components/runner.md`:**

- Per-`(role, context)` lock ownership (D34).
- `running` clause status (D33).
- `producer-no-response` reason (D33, D44).
- Boot order (D34).
- Producer-role per arrow looked up from the grid (D39).

**`components/state-machine.md`:**

- Clause-status set adds `running` (D33).
- Per-clause lock ownership (D34).
- Residue R formula (D44).
- Boot order (D34).

**`components/adversarial.md`:**

- `remediation-rounds-max` (D30) instead of unnamed N.
- Full re-attack confirmed (D32); open question 1 closed.
- Severity rule via finding-`basis` (D44).
- Init's adversarial phase application (D41).
- Producer-fix-signal / accepted-risk-proposal message shapes (D40).

**`components/amendment.md`:**

- Project-wide write-lock ownership (D34).
- Init takes the lock at end-of-init (D35).
- Boot order (D34).
- Grid-version pointer files (D31).

**`components/attestation.md`:**

- Clause status does NOT become `pass` with `accepted-risk`
  metadata; finding becomes `accepted-risk`; clause becomes `pass`
  only after all its findings are disposed (D33).
- `insufficient-basis-rounds-max` (D30).
- Attestation flow signals state-machine to update *clause* status;
  arrow status remains derived (D44).
- Operator event bus owned here (D43).
- Path encoding uses `__` (D37).

**`roles/integrator.md`:**

- G4 uses `cardinality-check(query: findings.type ∈ enum, expected: ≥1)`
  not `predicate-form` (D44).
