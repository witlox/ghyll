# Ubiquitous Language

Every term used in v2 ghyll has one meaning across all documents,
code, attestation records, and operator-facing UI. This document is
the authoritative glossary. If two specs disagree, this document
wins — but in practice they should not disagree, because every
component spec was reconciled to `gates.md` and `roles/*.md`.

Terms are grouped by category. A separate **disambiguation** section
at the end pairs confusable terms with explicit "X vs Y" notes.

---

## A. The state-space frame

`gates.md` §0 frames the runtime as a state-transition system over an
extensible grid. The vocabulary below is the abstract layer; the
data-shape vocabulary (entities, value objects) is in
`domain-model.md`.

| Term | Definition |
|---|---|
| **State space** | The set of all possible project states. Indexed by `(stratum, bounded-context)` cells, plus the global `(grid-version, residue, unevaluated-count)` tuple. |
| **Cell** | A point in the state space: a `(stratum, bounded-context)` pair, holding clause statuses, findings, and a derived arrow status. |
| **Operator** | A transition function. Every **arrow** is an operator. **Invalidation** and **grid amendment** are also operators (the latter extends the state space). |
| **Iteration** | One application of an operator on the state. Every **pass** is an iteration. |
| **Fixed point** | The condition where every cell in the declared grid has arrow status `complete`, residue R = 0, unevaluated-count C = 0. Convergence is not guaranteed; the grid can keep growing. |
| **Residue (R)** | The visible distance from convergence. The sum of operator-action cost across undeclared `(stratum, context)` cells. First-class headline number. |
| **Unevaluated-count (C)** | The number of arrows currently at status `unevaluated`. First-class headline number — the "green but will break on deployment" signal. |
| **State-transition system** | The formal name for the schema. Not a vector space (no linearity, no addition over cells); a Kripke structure over an extensible state space. |

## B. Workflow

| Term | Definition |
|---|---|
| **Project** | A repository ghyll is invoked on. One per directory. |
| **Bounded context** | A DDD bounded context within a project: a sub-area with its own ubiquitous language and invariants. Declared at init. Indexes the grid alongside stratum. |
| **Stratum** | One of six uniform layers per `gates.md` §3.1: Structure (1), Invariants (2), Behavior (3), Composition (4), Failure (5), Assumptions/risk (6). Each role specializes the same six layers into its own medium. |
| **Grid** | The full set of declared arrows across all `(stratum, bounded-context)` cells, at a specific version. The arrow grid is declared, versioned, and open (carries explicit residue). |
| **Grid version (vN)** | A monotonically increasing integer identifying a specific grid state. Embedded in arrow identity. Created by init (v1) and amendments (v2, v3, …). |
| **Diamond** | The default workflow: `analyst → architect → implementer → integrator`. Four roles; no standalone adversary or auditor. |
| **Arrow** | A declared transition between two roles for one cell at one grid version. Arrow identity = `(role-pair, stratum, bounded-context, grid-version)`. Immutable. The unit the harness gates. |
| **Pass** | One traversal of an arrow — one iteration of the operator. Identified by a unique pass-id. The pass's status (`running` / `completed` / `aborted`) is independent of the arrow status it derives. |
| **Phase** | One of three structural stages of an arrow traversal: **adversarial** (attack the upstream artifact), **remediation** (producer addresses findings), **verification** (final gate evaluation). Pure-machine arrows skip adversarial and remediation. |
| **Sub-activity** | One of three things the adversarial phase does: **clause-falsification** (attack each declared depth-sensitive clause), **open sweep** (find defects no clause names), **depth classification** (walk requirements on the depth ladder). |
| **Grid amendment** | An operator that produces a new grid version, typically triggered by an integrator finding of type `missing-cross-context-spec`. Serialized through the project-wide write-lock. Affected arrows become `invalidated`. |
| **Project initialization** | The mandatory first step when ghyll is invoked on a new project. Produces grid v1 from the harness's v0 baseline. Auto-propose + operator-confirm-or-modify-or-extend-or-skip-with-residue per declared `(role-pair, bounded-context)` arrow. Owns no arrow until end-of-init, when it takes the project-wide write-lock to write v1. |
| **v0 grid** | The harness's baseline that exists before any project init runs. Contains the diamond, the universal-base machine clauses, the stratum vocabulary, the depth ladder defaults, the per-concept schemas, and the init arrow's exit gate. Ships with the harness. |
| **Refusal** | Init's recommendation that ghyll's friction is wrong for this project (low-risk profile, no novel architecture, no cross-context seams). Operator may accept (ghyll exits) or override (residue note required). |

## C. Roles

| Term | Definition |
|---|---|
| **Role** | A behavioral contract with an exit gate. Four diamond roles have role files in `specs/direction/roles/`; two synthetic role-ids (`init`, `adversary`) do not. |
| **Diamond roles** | analyst, architect, implementer, integrator. Have role files. Run as the default workflow. |
| **Analyst** | Produces specs for one bounded context. Owns the upstream artifact for every other role. Specs cover the six strata: domain model (L1), invariants (L2), behavioral features (L3), cross-context interactions (L4), failure modes (L5), assumptions log (L6). |
| **Architect** | Produces architecture (modules, interfaces, contracts, error model) for one bounded context. Allocates analyst L3 behavior to modules. |
| **Implementer** | Produces code and tests for one bounded context. Materializes the architect's interfaces. Test depth is the implementer's responsibility; defended by `mutation-score` + adversarial depth-classification. |
| **Integrator** | Detects cross-context defects only visible in composition. Findings are typed: `local-bug` (routes to implementer for fix) or `missing-cross-context-spec` (triggers grid amendment back to analyst). |
| **Synthetic role-id** | An identity used in attestation paths and finding provenance, NOT a role file. Two exist: `init` (producer for init arrow's clauses) and `adversary` (producer for per-arrow adversarial phase). Adding a synthetic role-id requires harness changes, not project config. |
| **Producer** | The role-id that produces an arrow's upstream artifact. For diamond arrows, the upstream diamond role. For init arrow: `init`. For amendment-resulting arrows: re-engaged analyst. |
| **Adversary** | The synthetic role-id used to label the adversarial phase's attacks. Bound to a fresh model instance with clean context per round. Distinct from producer; producer cannot attack itself. |

## D. Clause and finding state machines

| Term | Definition |
|---|---|
| **Clause** | An exit-gate condition on an arrow. Has an evaluation type (`machine` / `attested`), a depth type (`depth-robust` / `depth-sensitive`), a concept (machine) or judgement-prose (attested), and arguments. |
| **Evaluation type** | How a clause is decided: `machine` (deterministic check by the harness) or `attested` (explicit operator verdict). `machine` is not "objective" and `attested` is not "subjective" — the split is about *who can decide*, not reliability. |
| **Depth type** | The minimum model depth required to *honestly* produce or evaluate a clause: `depth-robust` (any tier can do it) or `depth-sensitive` (requires a declared minimum tier). |
| **Clause status** | One of `pending`, `running`, `pass`, `fail`, `awaiting-attestation`, `insufficient-basis`, `unevaluated`. Per-pass, stored as `(pass-id, clause-id) → status`. |
| **`unevaluated`** | A clause-status (also a finding-status) meaning "the instrument was not sharp enough for the claim to carry meaning." Carries a `reason` field: `depth-below-required` / `no-rule-selectable-locations` / `producer-no-response`. First-class — never silently elevated to `provisional`. |
| **`provisional`** | An arrow-status meaning "all evaluated clauses passed, but at least one attested clause is `awaiting-attestation` or `insufficient-basis`." Does NOT satisfy the next role's input. |
| **`insufficient-basis`** | A clause-status meaning the operator could not attest (artifact ambiguous, context too large to judge). Not a failure. Routes through N-round escalation per `gates.md` §10 (default `insufficient-basis-rounds-max = 3`). |
| **Finding** | A defect raised by the adversarial phase, by depth-classification, or by a producer against itself (`unable-to-hint`). Lives on the arrow artifact, not on a clause. Has a `type` from the closed enum (`clause-falsification`, `open-sweep`, `depth-below-min`, `unable-to-hint`), a `severity`, a `status`, and a `grid-version` tag. |
| **Finding status** | One of `open`, `running` (re-attack in progress), `resolved` (re-attack confirmed gone), `accepted-risk` (operator attested), `unevaluated` (severity could not be assigned). |
| **`accepted-risk`** | A finding-status. NEVER a clause-status. The operator (not the producer) attests `accepted-risk` on a finding via the attestation flow. |
| **Arrow status** | Derived from clause + finding state, plus invalidation events. One of `complete`, `provisional`, `unevaluated`, `blocked`, `invalidated`. Precedence (most severe first): `invalidated` > `blocked` > `unevaluated` > `provisional` > `complete`. |
| **Pass status** | One of `running`, `completed`, `aborted`. `aborted` carries a `reason`: `invalidated`, `operator-interrupt`, `crash`, `manual-stop`, or `requires-deeper-artifact`. Only `invalidated` aborts change arrow status. |
| **Hint** | A structured pointer emitted by a producer for each attested clause: `{clause-id, locations, basis, residue}`. Must NOT contain a verdict. Hint-correctness is itself a depth-sensitive judgement. |
| **`unable-to-hint`** | A finding-type. Raised by a producer against itself when it cannot select hint locations by a stated rule. The clause is recorded `unevaluated` with reason `no-rule-selectable-locations`, AND the finding is raised. |
| **Severity** | A property of a finding: `info | low | medium | high | critical` (or `unevaluated`). Assigned by the adversarial phase per a stated rule (recorded in the finding's `basis`). Severity assignment is `depth-sensitive`. |
| **Severity threshold** | The level at which an `open` finding blocks an arrow (auto-inserted `no-open-finding` clause). Default `medium`. Configurable at init; raise-only. |

## E. Catalogue

| Term | Definition |
|---|---|
| **Catalogue** | The closed set of 17 concept names that machine clauses can reference. Language-agnostic at the concept layer. New concepts enter only via deliberate harness changes. |
| **Concept** | A named machine-clause type with a typed argument schema and an evaluator contract. Ships with the harness as `gates/concepts/<concept-name>.yaml`. |
| **Concept schema** | The YAML file specifying a concept's arguments, types, evaluator contract, and default cost. Introspectable; validated against at init. |
| **Language binding** | Per-concept per-language evaluator configuration. The shape is concept-specific (e.g., `lint-clean.go = "staticcheck && go vet"`; `no-orphan-symbol.rust = {extractor, index, mapper}`). Project-declared at init. Harness ships NO language defaults. |
| **Universal base set** | Four concepts (`compiles`, `lint-clean`, `no-todo-marker`, `every-step-bound`) automatically inherited by every arrow. Roles may not opt out. |
| **Auto-inserted** | A machine clause the harness adds to an arrow's verification phase without operator declaration. Two such clauses exist: `no-open-finding` and `every-requirement-meets-min-depth` — both inserted on arrows that ran an adversarial phase. |
| **Per-arrow instance** | An instantiation of a catalogue concept with specific arguments for one arrow's exit gate. Declared at init (auto-proposed from role file, then operator-confirmed). |
| **Evaluator** | The concrete instrument that decides a machine clause. For language-bound concepts: the binding (e.g., `go-mutesting` for `mutation-score.go`). For universal concepts: a built-in harness function. |
| **Evaluator contract** | The shape an evaluator's output must take, declared in the concept schema. Generic shape: `{pass: boolean, details: object}`. Concept-specific `details` fields. |

## F. Operator interaction

| Term | Definition |
|---|---|
| **Operator** | A human using ghyll. Identified by `op-id` (a declared string, conventionally email or handle). Returns verdicts on attested clauses; attests accepted-risk on findings; declares grid configuration at init. |
| **`op-id`** | Operator identity. Non-empty string. Declared at session start. Recorded in every attestation record. Validated for path-safety (no `/`, `..`, NUL, control chars, unicode RTL override, or excessive length). Never used as a filesystem path component. |
| **Operator session** | A logical session bound to an `op-id`. Active until the operator closes it or the harness terminates. Multi-operator handoff within a single pass is allowed; chains are recorded. |
| **Attestation request** | A pending operator decision: a hint awaiting verdict. Carries `(clause-id, pass-id, hint, severity-context)`. Published on the operator event bus. |
| **Attestation record** | One JSONL line in a per-pass file at `attestations/v<N>/<context>/stratum-<S>/<role-pair>/<pass-id>.jsonl`. Append-only. Unit-typed: `confirm`, `record-locations-inspected`, `write-residue-note`. |
| **Verdict** | An operator's decision on an attested clause: `pass`, `fail`, or `insufficient-basis`. Or, on a finding: `accepted-risk`. |
| **Operator action unit** | The measure of operator friction per attestation. Three units, each with a fixed cost: `confirm` (cost 1), `record-locations-inspected` (cost 3), `write-residue-note` (cost 5). Sum-of-clause-costs is an arrow's weight. |
| **Operator event bus** | The single channel for all operator-facing communication. Owned by the attestation flow component. Events: `attestation-request`, `escalation-request`, `refusal-prompt`, `pass-aborted`, `amendment-conflict`, `init-prompt`, plus failure-mode notifications. |
| **Auto-propose** | The init mechanism by which the harness drafts each `(role-pair, context)` arrow's exit-gate clauses from the role file templates; operator confirms / modifies / extends / skips-with-residue. |
| **Re-init** | Re-entry into the initialization flow during an active project, triggered by a missing language binding or operator-driven schema change. Scoped to the missing item; the rest of the grid is preserved. |

## G. Locks and concurrency

| Term | Definition |
|---|---|
| **Per-clause transition lock** | Owned by the state-machine engine. Scope: `(pass-id, clause-id)`. Serializes status transitions on a single clause. |
| **Per-`(role, context)` lock** | Owned by the runner. Enforces `single-active-role-instance` at pre-spawn. Expires on pass termination. |
| **Project-wide grid write-lock** | Owned by the amendment component. Held during grid v(N+1) commit. Init takes it once at end-of-init for the v1 write. |
| **Amendment queue** | FIFO queue of pending grid amendments waiting for the write-lock. Concurrent amendments serialize through the queue. |

## H. Persistence and filesystem

| Term | Definition |
|---|---|
| **`.ghyll/`** | The project's ghyll-managed directory. Contains the grid files, attestation records, checkpoint log, lock files. |
| **`grid.v<N>.yaml`** | A versioned grid file. Immutable after write. Contains the declared arrows, bindings, residue, etc. for grid version N. |
| **`grid.current`** | A small pointer file containing one line: `v<N>` (the active grid version). Atomically updated on amendment. The single source of truth for "which version is now." |
| **Attestation file** | One JSONL file per pass at `attestations/v<N>/<context>/stratum-<S>/<role-pair>/<pass-id>.jsonl`. Append-only. Path uses `__` separator between role-ids (filesystem-safe). |
| **Checkpoint log** | The Merkle DAG chain of pass-finalization records. Reused from v1 infrastructure. Source of truth for completed-pass state. |
| **Path encoding (`__`)** | The double-underscore separator used between role-ids in the `<role-pair>` path component (e.g., `analyst__architect`, `analyst__adversary__architect`, `init__analyst`). Filesystem-portable; no Unicode glyphs; no path separators. |

---

## Disambiguation: confusable pairs

| Pair | Distinction |
|---|---|
| **Depth type** vs **Depth ladder** vs **Severity** | Three different things. **Depth type** is about the *clause* (what model is required to evaluate it honestly): `depth-robust` / `depth-sensitive`. **Depth ladder** is about the *artifact* (how deep its implementation actually reaches): 4 tiers, default `NONE` / `SHALLOW` / `MOCKED` / `REALISTIC`. **Severity** is about a *finding* (how serious the defect is): `info` / `low` / `medium` / `high` / `critical`. |
| **Clause status** vs **Finding status** vs **Arrow status** vs **Pass status** | Four different state machines. **Clause status** is per-pass-per-clause (`pending`, `running`, `pass`, ...). **Finding status** is per-finding (`open`, `running`, `resolved`, `accepted-risk`, `unevaluated`). **Arrow status** is per-arrow-per-grid-version, derived from clause + finding state (`complete`, `provisional`, ...). **Pass status** is per-pass (`running`, `completed`, `aborted`). |
| **`unevaluated`** as clause status vs as finding status vs as severity | Three places. **Clause `unevaluated`**: the instrument that produced this clause was too thin. **Finding `unevaluated`**: severity could not be assigned (adversary below required depth). **Severity `unevaluated`**: assigned to a finding when severity-assignment couldn't reach depth. All three are first-class and block arrow closure. |
| **`accepted-risk`** clause vs **`accepted-risk`** finding | The clause never has status `accepted-risk`. Only findings do. When an operator attests `accepted-risk` on a finding, the finding transitions to `accepted-risk` AND the clause may transition to `pass` (if all its findings are disposed). Two different objects; one verdict serves both transitions. |
| **`provisional`** arrow vs **`insufficient-basis`** clause | A `provisional` arrow has any attested clause not yet decided (either `awaiting-attestation` or `insufficient-basis`). An `insufficient-basis` clause is one *specific* clause; `provisional` is the *arrow*'s rolled-up state. |
| **`aborted`** pass vs **`invalidated`** arrow | Different objects. **`aborted` pass** = pass status with a reason field. **`invalidated` arrow** = arrow status set by grid amendment. Most often co-occur (a pass aborted with `reason: invalidated` causes the arrow to become `invalidated`), but other abort reasons (`crash`, `operator-interrupt`) don't change arrow status. |
| **Concept** vs **Concept instance** vs **Concept schema** | **Concept** is the named type (e.g., `mutation-score`). **Concept schema** is the YAML file declaring the type's argument signature and evaluator contract (`gates/concepts/mutation-score.yaml`). **Concept instance** is one clause's specific invocation (e.g., `mutation-score(scope: "src/**", threshold: 0.7)` on a specific arrow). |
| **Phase** vs **Sub-activity** | **Phase** is one of three: adversarial, remediation, verification. **Sub-activity** is one of three things the *adversarial* phase does internally: clause-falsification, open sweep, depth classification. |
| **Producer** vs **Adversary** | Different identities. The **producer** is the role that produces the upstream artifact (e.g., analyst is producer for the analyst→architect arrow). The **adversary** is the synthetic role-id used by the adversarial phase to attack the producer's artifact. Producer cannot attack itself (`adversary` role-id is structurally distinct). |
| **`insufficient-basis-rounds-max`** vs **`remediation-rounds-max`** | Two distinct N knobs. **`insufficient-basis-rounds-max`** (default 3) bounds *attestation*-side escalation: after N rounds of `insufficient-basis` on a clause, operator escalates. **`remediation-rounds-max`** (default 5) bounds *adversarial*-side loop: after N rounds of producer-fix-and-re-attack failing to resolve a finding, escalate. Same name pattern; different machinery. |
| **Grid version** vs **Pass id** vs **Arrow id** | Three different identifiers. **Grid version** (`vN`) is a monotonic integer per-project. **Arrow id** is `(role-pair, stratum, bounded-context, grid-version)`. **Pass id** is a unique per-traversal UUID/timestamp. One project has many grid versions; one grid has many arrows; one arrow has many passes. |
| **Init arrow** vs **diamond arrows** | The init arrow is the very first arrow ghyll runs on a project (producer `init`, consumer `analyst`). It runs once per project (v1 grid). Diamond arrows are the four canonical transitions; each runs many times per project. |
| **`grid.current`** vs **`grid.v<N>.yaml`** | `grid.current` is a one-line pointer ("v3"). `grid.v3.yaml` is the actual grid content. Two files for one fact (separation of pointer and content) so atomic rename can replace the pointer without overwriting the content. |
| **Refusal** vs **Transition refusal** | **Refusal** (init) is a recommendation that ghyll's friction is wrong for this project. Operator can accept (ghyll exits) or override. **Transition refusal** (runner) is a structural error returned when an arrow's status doesn't satisfy the next role's input. Different layers; different verbs.
| **Hint** vs **Finding** | A **hint** is a structured pointer emitted by a producer for an attested clause; doesn't contain a verdict; intended to guide operator attestation. A **finding** is a defect raised by the adversarial phase or by a self-raising producer; has a severity and a status; lives on the arrow artifact. Hints flow producer→operator; findings flow adversary→producer / operator. |
| **`fail` clause** vs **`open` finding above threshold** | Both block the arrow but differently. A **`fail` clause** is a clause whose evaluation returned negative. An **`open` finding above threshold** is a defect the adversarial phase raised that hasn't been resolved or accepted-risk. The auto-inserted `no-open-finding` clause synthesizes a `fail` when this condition holds, so they converge at arrow-status `blocked` — but they're distinct in causation. |

---

## Forbidden language

These phrases must NOT appear in hints, attestation records, or
operator-facing UI per `gates.md` §9:

- `approve`, `approved`, `approval`
- `looks good`, `LGTM`, `seems fine`
- `sufficient`, `acceptable` (when used as a verdict)
- `passed`, `PASS` (as freestanding text; only as a structured field)

Reason: these substitute for the operator's judgement rather than
guiding it. Producer-role outputs (hints) point; the operator
judges. The producing role records evidence and rule; the verdict
field carries the decision.

---

## Stable across documents

This glossary is the cross-document anchor. If any spec, role file,
component spec, or code identifier diverges from a definition here,
that's drift and should be reconciled — pointing back here as the
truth.
