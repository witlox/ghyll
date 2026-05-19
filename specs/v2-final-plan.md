# v2-final consolidation plan

**Goal**: collapse the v1↔v2 split into a single ghyll codebase with a
BDD suite that is THOROUGH on every critical path (a few MODERATE
allowed on non-critical paths). Drop the "v1" / "v2" phasing language
once consolidation is verified.

**Driving observation (audit, 2026-05-19)**: 350 BDD scenarios across
34 features today. 77 THOROUGH (22%), 40 MODERATE (11%), 114 SHALLOW
(33%), 119 NONE (34%). 80 v2 scenarios have zero step impls;
~75 v1 scenarios are state-theater. This is the baseline to fix.

---

## Architectural decisions — confirmed 2026-05-19

Operator confirmed all three recommended positions plus phase-11
deferral and operational settings. The provisional answers below are
now the actual decisions.

### D-1. Free-form workflow roles → DEPRECATED ✓

**v1**: `workflow.Load()` reads `.claude/roles/*.md` and `.ghyll/roles/*.md`
at runtime and lets operators redefine roles freely. `session.activeRole`
switches between them.

**v2 direction §3.3**: roles are a FIXED set (analyst, architect,
implementer, integrator). No free redefinition at runtime — "a role you
can argue with is a gate you can argue with."

**Decision (ADR-008)**: deprecate runtime free-form roles. `workflow.Load()`
keeps **project instructions** (the `.claude/CLAUDE.md` body) and **slash
commands** (operator-defined extensions like `/ultrareview`), but drops
the per-role markdown files as a runtime concept. The four v2 roles
embed as Go data, not as loadable files.

**Blast radius**: `workflow/` package shrinks; `session.SwitchRole`
goes away; `workflow.feature` loses ~10 of its 28 scenarios; CLAUDE.md
project-instructions path stays. See `docs/decisions/008-v2-fixed-roles-deprecate-runtime-workflow-roles.md`.

### D-2. v1 sub-agents → narrowed to "task delegation" TOOL ✓

**v1**: `RunSubAgent(parentSession, task)` spawns a focused sub-agent
as a tool call. Used by the model to delegate a self-contained search
or analysis.

**v2 direction**: no explicit deprecation. The diamond workflow's
arrow phases (adversarial / remediation / verification) are the
structured collaboration; sub-agents are an unstructured tool.

**Decision**: keep `RunSubAgent` as a TOOL (parallel to `bash`, `grep`).
Drop the v1 "sub-agent inheritance" Gherkin that implies a parent/child
session topology. Sub-agents are a one-shot delegation; nothing more.

**Blast radius**: `sub-agents.feature` scenarios drop from 18 to ~6
(the ones that test the tool call itself, not inheritance).

### D-3. Consolidate the spec tree ✓

**Current layout (audited 2026-05-19)**:

```
specs/
├── architecture/      10 v1 arch docs (data-structures, routing-logic, ...)
├── direction/         v2 design (direction.md, gates.md, build-notes.md,
│                       roles/, components/)
├── v2/
│   ├── features/      v2 .feature files
│   ├── domain-model.md, invariants.md, ubiquitous-language.md,
│   │   failure-modes.md, cross-context.md (v2 versions of v1 docs at
│   │   specs/ root)
│   ├── validation-impl-pass-{1..10}.md
│   └── decisions/
├── features/          v1 .feature files
├── fidelity/, findings/, integration/, cross-context/
├── domain-model.md, invariants.md, ubiquitous-language.md,
│   failure-modes.md, assumptions.md
└── README.md
```

**Decision**: at consolidation, simplify to:

```
specs/
├── architecture/
│   ├── (current v1 arch docs unchanged)
│   ├── design.md             ← was direction/direction.md
│   ├── gates.md              ← was direction/gates.md
│   ├── components/           ← was direction/components/
│   └── roles/                ← was direction/roles/
├── features/                 ← merge v1 features + v2/features
├── archive/
│   ├── validation-passes/    ← v2/validation-impl-pass-*.md
│   ├── direction/build-notes.md (historical)
│   └── direction/operator-decisions-round-*.md
├── (the v2 top-level docs supersede the v1 root docs — domain-model.md,
│   invariants.md, ubiquitous-language.md, failure-modes.md,
│   cross-context.md merge with the v2 content winning)
├── findings/, integration/, fidelity/
└── README.md (rewritten — drops v1/v2 disclaimers)
```

`specs/v2/` and `specs/direction/` directories are removed at the end
of Phase D.

### D-4. Phase-11 attestation flow → DEFERRED ✓

The full operator attestation flow (event bus, multi-operator handoff,
verdict JSONL records, insufficient-basis-rounds-max escalation) is
NOT YET BUILT (build-notes step). Phase B3 wires only the attestation
scenarios whose code exists today (§7.1 dispatch block, op-id session
start, on-the-spot creation suspension). Remaining ~12 scenarios tag
`@phase11` and skip; they wire when phase 11 ships.

### D-5. Phase order → STRICTLY SEQUENTIAL ✓

A → B (B1..B6) → C (C1..C4) → D (D1..D5) → E → F. Each phase ends
with adversarial pass + remediation per the established workflow.
Trade-off: slower wall-clock; benefit: every phase has a clean
baseline.

### D-6. Coverage target at v2-final tag → 80% TARGET / 70% FLOOR ✓

CI threshold raises from 50% to 70% at Phase F. Stretch target 80%.
Forces remaining SHALLOW/NONE scenarios to be lifted (or explicitly
flagged MODERATE on non-critical paths).

---

## Phasing

Six phases, each ending with the project's standard adversarial pass +
remediation: spawn parallel cold-context auditor/adversary agents over
disjoint file sets, consolidate findings into
`specs/v2-final-pass-N.md` (severity-bucketed: Critical/High/Medium/
Low), fix every finding in the same commit batch — no deferrals, no
severity filtering. Pattern established in `validation-impl-pass-1`
through `validation-impl-pass-10`; same pattern for the v2-final
passes.

### Phase A — Header reconciliation + decisions baseline (SMALL, doc only)

**Goal**: stop lying in the docs.

**Scope**:
1. Update every `specs/v2/features/*.feature` header that reads
   `Implementation: v2 (not yet built; ...)` to reflect actual phase
   shipped:
   - `runner.feature`, `runner-step3.feature` → built (phase 3)
   - `init.feature` → built (phase 2)
   - `state-machine.feature` → built (phase 5)
   - `adversarial.feature` → built (phase 5)
   - `amendment.feature` → built (phase 5)
   - `attestation.feature` → built (phase 6 — on-the-spot creation +
     §7.1 dispatch; full operator attestation flow partial)
2. Record D-1/D-2/D-3 architectural decisions in this file (or an ADR)
   so subsequent phases reference them.
3. Update `specs/fidelity/INDEX.md` to call the BDD gaps out explicitly
   (no more "9 with real assertions" misleading wording).

**Adversarial scope**: cold-read this plan document and the updated
headers for internal contradictions. ~5 findings expected.

**Exit criteria**: headers match reality; D-1/D-2/D-3 confirmed; INDEX
acknowledges the 119 NONE scenarios.

**Size**: small (~100-200 lines of doc).

---

### Phase B — Wire v2 acceptance to real v2 code (LARGE, seven sub-phases)

**Goal**: 143 v2-new scenarios all classified THOROUGH (or explicitly
MODERATE on non-critical paths).

Today's baseline (audited 2026-05-19):

| Feature | Scenarios | THOROUGH today | SHALLOW/NONE today |
|---------|-----------|----------------|--------------------|
| state-machine.feature | 24 | 0 | 24 |
| amendment.feature | 17 | 0 | 17 |
| attestation.feature | 22 | 0 | 22 (12 are @phase11-deferred per D-4) |
| adversarial.feature | 17 | 0 | 17 |
| init.feature | 26 | ~13 | ~13 PENDING |
| runner.feature | 28 | 22 (F-3 arrow-status) | 6 PENDING (subprocess) |
| runner-step3.feature | 9 | 7 | 2 SHALLOW |
| **TOTAL v2-new** | **143** | **42** | **101** |

Phase B writes ~101 new step implementations across seven sub-phases.
Each sub-phase is its own slice with its own adversarial pass.

#### B1. state-machine.feature (24 scenarios — all SHALLOW → THOROUGH)

The v2 clause/arrow status state machine is in `runner/states.go`,
`runner/arrow.go`, `runner/findings.go`. The .feature scenarios cover:
- clause transitions (pending → running → pass/fail/unevaluated)
- arrow status derivation (`DeriveArrowStatus`)
- invalidation supersedes everything
- depth-below-required produces unevaluated
- awaiting-attestation produces provisional

**New step file**: `tests/acceptance/steps_state_machine.go`. Calls
real `runner.Runner.Evaluate()`, real `runner.DeriveArrowStatus()`,
asserts on returned struct values (not on `state.<field>` after a
no-op).

**Registration**: `registerStateMachineSteps` in `acceptance_test.go`.

**Size baseline**: comparable step files: `steps_runner.go` 398 LoC for
28 scenarios (~14 LoC/scenario), `steps_init.go` 404 LoC for 26
scenarios (~16 LoC/scenario). Estimate B1 ≈ 350-400 LoC.

#### B2. amendment.feature (17 scenarios — all NONE → THOROUGH)

`runner.AmendmentQueue` + `engine.Replay` dedup are built (phase 5,
phase 9). Scenarios cover:
- integrator triggers amendment → enqueued
- FIFO order
- commits successfully → drain
- dedup across restart (replay's LoadDrained)
- dependency check against in-flight passes

**New step file**: `tests/acceptance/steps_amendment.go`. Uses real
`AmendmentQueue` + a real `engine.Store` round-trip for dedup tests.

**Size baseline**: 17 scenarios × ~16 LoC/scenario + helper setup ≈
300 LoC.

#### B3. attestation.feature (22 scenarios — ~10 wirable, ~12 @phase11-deferred)

Phase 6 + phase 7 + phase 10 surfaces. Scenarios cover:
- session start with op-id
- empty op-id refused
- multi-operator handoff in one pass
- attestation captures verdicts → typed JSONL records
- §7.1 attestation-pending dispatch block (already wired in phase 10)
- insufficient-basis-rounds-max escalation

**Caveat**: full operator attestation flow is PARTIAL (build-notes
step says "await attestation flow"). Only wire the scenarios whose
code actually exists. Flag the rest as NONE with a TODO referencing
the missing code, and add them back when phase 11 lands.

**New step file**: `tests/acceptance/steps_attestation.go`.

**Size baseline**: ~10 wirable scenarios × ~20 LoC (op-id handling is
heavier than state-machine) ≈ 220 LoC for the wired subset. The 12
@phase11 scenarios get tagged but no step code.

#### B4. adversarial.feature (17 scenarios — all NONE → THOROUGH)

Phase 5 shipped the Adversary + Findings store. Scenarios:
- arrow enters adversarial phase
- pure-machine arrow skips
- adversary attempts to falsify each depth-sensitive clause
- finding raised with severity
- producer-cannot-self-attack
- remediation loop bounded; non-convergence escalates

**New step file**: `tests/acceptance/steps_adversarial.go`. Calls real
`runner.Adversary` + `FindingsStore`.

**Size baseline**: 17 scenarios × ~25 LoC (adversary phase has more
setup — fixtures for findings, attack scenarios) ≈ 425 LoC.

#### B5. init.feature (26 scenarios — ~13 PENDING → THOROUGH)

Phase 2 bootstrap init shipped — auto-propose, modify-on-unknown,
orphan, risk, profile. `steps_init.go` exists but ~50% of step funcs
return `ErrPending`. Replace the pending bodies with real
`bootstrap.*` calls.

**Step file**: existing `tests/acceptance/steps_init.go` +
`steps_init_driver.go`. Lift pending → real.

**Size baseline**: existing `steps_init.go` is 404 LoC for ~13
wired + 13 pending. Lifting the 13 pending probably ≈ doubles the
file → +400 LoC.

#### B6. runner.feature subprocess scenarios (6 remaining PENDING → THOROUGH)

F-3 arrow-status is THOROUGH already. The subprocess/evaluator
coordination scenarios in `runner.feature` are PENDING. Wire to
`runner.BindingEvaluator` with a real subprocess fixture (echo
script, bash one-liner).

**Step file**: existing `tests/acceptance/steps_runner.go`. Lift
pending → real subprocess.

**Size baseline**: 6 scenarios × ~30 LoC (subprocess fixtures are
heavier — need a real binary to exec) ≈ 180 LoC.

#### B7. runner-step3.feature (9 scenarios — 7 THOROUGH, 2 SHALLOW → THOROUGH)

Lift the 2 SHALLOW scenarios (clause-evaluation in mocked context) to
real `runner.Runner.Evaluate()` calls.

**Step file**: existing `tests/acceptance/steps_runner.go` extension or
new `steps_runner_step3.go`.

**Size baseline**: ~60 LoC for the 2 lift scenarios.

**Phase B sub-phase total estimate**: 350 + 300 + 220 + 425 + 400 + 180 + 60 ≈ 1935 LoC of new step code.

**Phase B adversarial scope**: one pass per sub-phase. Each pass reads
the new step file against the .feature contract: does the step really
call the package, or does it set `state.<field>` and lie? Expected
30-50 findings per sub-phase.

**Phase B exit criteria**: all 143 v2-new scenarios classified
THOROUGH (or @phase11-tagged + skipped per D-4; ≤5 MODERATE allowed
on non-critical paths such as httptest-mocked HTTP).

---

### Phase C — Lift v1 SHALLOW scenarios to THOROUGH (MEDIUM)

**Goal**: ~75 v1 SHALLOW scenarios become THOROUGH. These test code
that EXISTS in v1 and is still load-bearing; they just don't actually
call it today.

**Sub-phases** (each is its own slice + adversarial pass):

- **C1. plan-mode.feature** (14): drive the REPL-equivalent in test.
  Build a fake input source, feed `/plan` / `/fast`, assert
  `session.PlanMode()` flips and `composedSystemPrompt()` reflects.
- **C2. resume.feature** (10): full session round-trip — start
  session A, take a few turns, close, start session B with
  `--resume`, assert checkpoint restored AND turn count continues.
- **C3. workflow.feature** (28, scoped per D-1): for the scenarios
  that test instruction loading + budget enforcement, drive a real
  `.ghyll/CLAUDE.md` + assert composed prompt. The role-loading
  scenarios get DROPPED per D-1.
- **C4. sub-agents.feature** (18 → ~6, scoped per D-2): wire the
  surviving 6 to real `RunSubAgent()`; drop the inheritance
  scenarios.

**Size**: medium-large (~1000-1500 lines total step code).

**Adversarial scope**: same pattern as Phase B.

---

### Phase D — Consolidate v1 ↔ v2 (MEDIUM-LARGE)

**Goal**: drop the v1/v2 split. One features directory, one set of step
files, no duplicate inherited features.

**Sub-phases**:

- **D1. Code surgery for D-1 deprecation**:
  - Remove `workflow.Load()`'s role-loading path; keep instruction
    loading + slash commands.
  - Remove `Session.activeRole` / `SwitchRole`.
  - Embed the four v2 roles as Go data (per `specs/direction/roles/`).
  - Adversarial: did anything else read `s.wf.Roles`? (grep first.)

- **D2. Drop v1↔v2 inherited duplicates**:
  - The 10 inherited features (config, edit, glob, keys, memory,
    stream, sync, tools, vault, web) have identical scenarios on both
    sides. Drop the `specs/v2/features/<X>.feature` copies — they
    were placeholders during the v1→v2 transition.
  - Where a v2 copy ADDS scenarios (config has v2-specific
    depth-ladder / severity / bindings), MERGE those into the v1
    feature and drop the v2 copy.

- **D3. Move v2-only features into `specs/features/`**:
  - adversarial.feature, amendment.feature, attestation.feature,
    init.feature, runner.feature, runner-step3.feature,
    state-machine.feature → `specs/features/`.
  - Drop `specs/v2/features/` directory entirely.

- **D4. Step file consolidation**: ensure every step file is
  registered in `acceptance_test.go`. Drop the
  `registerStateMachineSteps` / `registerAmendmentSteps` /
  `registerAttestationSteps` / `registerAdversarialSteps` once they
  exist (Phase B) and confirm acceptance_test.go calls them all.

- **D5. Spec tree consolidation per D-3 (revised layout)**:
  - Move `specs/direction/direction.md` → `specs/architecture/design.md`.
  - Move `specs/direction/gates.md` → `specs/architecture/gates.md`.
  - Move `specs/direction/components/` → `specs/architecture/components/`.
  - Move `specs/direction/roles/` → `specs/architecture/roles/`.
  - Archive `specs/direction/build-notes.md`, `operator-decisions-round-*.md`,
    `phase-3-architect-findings.md` → `specs/archive/direction/`.
  - Archive `specs/v2/validation-impl-pass-*.md` and `specs/direction/validation-pass-*.md`
    → `specs/archive/validation-passes/`.
  - Merge the top-level v2 docs (`specs/v2/{domain-model,invariants,
    ubiquitous-language,failure-modes,cross-context}.md`) with the
    `specs/` root equivalents — v2 wins where they diverge; harmonize
    where they overlap.
  - Update `specs/README.md` to describe the consolidated tree (drop v1/v2
    framing). Delete `specs/v2/README.md` and `specs/direction/README.md`.
  - Remove the now-empty `specs/v2/` and `specs/direction/` directories.

**Size**: medium-large (~500-1000 lines of code change in workflow/
+ ~1500 lines of doc movement).

**Adversarial scope**: full integrator pass — does the consolidated
codebase still build + test, and have any tests been silently dropped
in the merge?

---

### Phase E — Final cleanup of "v1" / "v2" language (SMALL)

**Goal**: nothing in the repo refers to "v2" except as historical
context.

**Scope**:
- Update `CLAUDE.md` (root + .claude/) to drop v1/v2 phasing.
- Update `specs/fidelity/INDEX.md` to a single consolidated section.
- Update `docs/` (mdbook) — no v1/v2 phasing in user-facing copy.
- Update code comments that say "v2" / "v1" → context-neutral.
- Update memory files (`project_pivot_v2.md` → `project_status.md`).

**Adversarial scope**: cold-read all top-level docs for stale phasing
language.

**Size**: small (~200 lines doc).

---

### Phase F — Final fidelity audit + integrator + ship (MEDIUM)

**Goal**: certify v2-final ready to tag.

**Scope**:
1. Full fidelity sweep across all step files. Every scenario classified
   THOROUGH / MODERATE / SHALLOW / NONE per `roles/auditor.md`.
2. Acceptance criterion: **zero SHALLOW or NONE on critical paths**.
   Critical paths = gates.md §6/§7/§8/§11/§12 invariants,
   commit-attribution flow, engine persistence + replay, vault v2
   endpoints, runner subprocess, dialect routing including §7.1
   dispatch block.
3. Non-critical paths allowed MODERATE: web-fetch via httptest mock,
   stream client via SSE mock, embedder via fake.
4. Full integrator pass per `roles/integrator.md`.
5. Remediate all findings (no deferrals).
6. `make`, `go test -race ./...`, coverage ≥ 80% (target raised from
   50% baseline).
7. Tag as v1.0.0 (semver — first stable consolidated release).

**Size**: medium (audit produces a finding list; remediation depends
on findings).

---

## Estimated total

- Phase A: 1 commit, ~200 lines, half a session.
- Phase B (6 sub-phases): 6 commits per implementation + 6 commits per
  adversarial remediation = 12 commits. ~3000 lines of step code +
  remediations. Several sessions.
- Phase C (4 sub-phases): 8 commits. ~1500 lines. Two sessions.
- Phase D (5 sub-phases): 5 commits + adversarial. ~2500 lines (mix
  code + docs). One-and-a-half sessions.
- Phase E: 1 commit, ~200 lines doc. Half a session.
- Phase F: 1 audit commit + N remediation commits. One session +
  finding-dependent tail.

Total: ~25-30 commits, ~7500 lines change. 5-7 sessions of work.

## Confirmed decisions

All six recommended positions confirmed by operator on 2026-05-19:

| # | Decision | Confirmed |
|---|----------|-----------|
| D-1 | Deprecate runtime free-form roles (ADR-008) | ✓ |
| D-2 | Sub-agents as tool only, no inheritance | ✓ |
| D-3 | Fold direction/ → architecture/ at consolidation | ✓ |
| D-4 | Defer phase-11 attestation flow scenarios to a later phase | ✓ |
| D-5 | Strictly sequential phase order A→B→C→D→E→F | ✓ |
| D-6 | Raise coverage to 70% floor / 80% target at Phase F | ✓ |
