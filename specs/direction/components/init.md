# Component: project initialization

The initialization component is **step one** when ghyll is invoked on
a project. It refines the harness's v0 baseline into the project's
arrow grid vN. Without init, no other arrow runs. See `gates.md` §2.

This document is the component-level spec for the init flow:
the analyst's outputs (per `roles/analyst.md`) applied to init's own
domain.

> Status: design intent. Subject to refinement once init is built and
> the first project is initialized through it.

---

## Scope

**In scope.** The init flow from invocation to first recorded grid
vN. The auto-propose flow, operator-confirm loop, language-binding
declaration, refusal flow, re-entry on missing bindings.

**Out of scope.** Subsequent grid amendments (those are
integrator→analyst arrows running against an existing grid, not
init). The diamond execution itself.

---

## Domain model

| Term | Definition |
|---|---|
| **Project** | A directory ghyll has been invoked on. Identified by its absolute path. |
| **Bounded context** | A sub-area within the project with its own ubiquitous language and invariants (DDD sense). Declared at init; named uniquely within the project. |
| **Project state** | The repo's current state at init time: file tree, language(s) present, existing specs (brownfield), prior grids (re-init). |
| **Stratum** | One of the six uniform layers (`gates.md` §3.1). Fixed at v0; not declarable at init. |
| **Arrow grid** | The set of declared arrows across `(stratum × bounded-context)` cells. Init's primary output. |
| **Residue** | Undeclared `(stratum, context)` cells the operator marked acceptable. First-class quantity in the grid. |
| **Language binding** | A per-concept, per-language configuration providing the concrete instrument (`lint-clean.go = staticcheck && go vet`, etc.). |
| **Depth ladder** | The 4-tier classification labels for the adversarial phase's depth-classification sub-activity. Harness ships defaults; project may override labels (not the tier count). |
| **Severity threshold** | The project-wide threshold above which open findings block arrows. Default `medium`; init may raise. |
| **Auto-propose** | The harness-initiated proposal of role-file-declared clauses to the operator, per `(role, context)` arrow. |
| **Operator-confirm verdict** | One of `confirm` / `modify` / `extend` / `skip` per auto-proposed clause. `skip` requires a residue entry. |
| **`op-id`** | Operator identity declared at session start; recorded in every attestation. |
| **Init arrow** | The arrow `(init → analyst)` ridden by init's own exit gate. Synthetic role-id `init` is the producer (`gates.md` §1.1). |

---

## Invariants

1. **No diamond before grid.** A project cannot enter the diamond
   workflow unless the init arrow has status `complete` (per
   `gates.md` §7.2). Attempts are refused.
2. **Unique context names.** Every declared bounded-context name
   appears exactly once within the project. Enforced by
   `unique-definition`.
3. **Cells are declared or residue.** Every `(stratum, context)`
   cell is either represented in the arrow grid or appears in the
   residue list. No silent omissions.
4. **Binding completeness.** Every language used by any declared
   `scope` in any clause has a binding declared for every concept
   that names that language. Missing binding → re-entry into init
   (§F-3).
5. **Init follows its own schema.** Init's exit gate is gated by
   the same machinery as any other gate: machine clauses,
   `attested` clauses with hints, the §11 phases. Init is not a
   special case.
6. **Depth-ladder shape.** If the project overrides the depth-ladder
   labels, the override has exactly 4 tiers, each with a non-empty
   string label, ordered from least-deep (tier 0) to deepest
   (tier 3).
7. **Severity threshold floor.** The project-wide severity
   threshold is at least `medium` (harness floor; init may raise,
   never lower).
8. **Atomic grid write.** The grid is written to disk atomically;
   either v(N) is fully recorded or unchanged. Partial-write states
   are not observable to the diamond.
9. **`op-id` declared at session start.** Every attestation record
   contains a non-empty `op-id`; init refuses to begin until an
   `op-id` is declared.

---

## Behaviors (features)

Each feature is a Gherkin-style scenario set. Implementation will
materialize these into `.feature` files under
`tests/init/*.feature` (or equivalent).

### F-1: Greenfield init

```gherkin
Feature: Greenfield init

  Scenario: Empty repository
    Given a project directory with no source files and no prior grid
    When the operator runs `ghyll init`
    And declares `op-id = "alice@example.com"`
    Then init proposes the diamond as the declared arrow set
    And init proposes 0 bounded contexts initially
    And init interrogates the operator (analyst-style) to identify
        bounded contexts from intent

  Scenario: Operator declares contexts via interrogation
    Given a greenfield init in progress
    And the operator has answered the context-identification
        interrogation with `["ContextA", "ContextB"]`
    Then init records 2 bounded contexts
    And init auto-proposes role exit-gate clauses for each
        (role-pair, context) arrow per F-5

  Scenario: Greenfield refusal
    Given a project the operator describes as "throwaway script,
        one bounded context, no novel architecture"
    When init evaluates the project profile
    Then init proposes refusal with rationale "ghyll's friction
        will be pure cost here; use a fast agent"
    And the operator either accepts refusal (init exits) or
        overrides (init proceeds with a residue note recording the
        override)
```

### F-2: Brownfield init

```gherkin
Feature: Brownfield init detects existing structure

  Scenario: Existing code with no prior ghyll grid
    Given a project directory with existing source code in
        `src/contextA/` and `src/contextB/`
    And no prior `.ghyll/grid.yaml` file exists
    When the operator runs `ghyll init`
    Then init runs the `mode-determinable-from-repo` rule and
        determines `mode = brownfield`
    And init proposes bounded contexts `["contextA", "contextB"]`
        based on the directory structure
    And the operator confirms or refines the proposal

  Scenario: Init runs orphan-symbol-extraction during brownfield discovery
    Given a brownfield init with declared bounded contexts
    When init walks each context's source
    Then init extracts the exported-symbol list per language
    And presents orphans (symbols with no clear spec mapping) as
        residue candidates for operator triage
```

### F-3: Re-init on missing binding

```gherkin
Feature: Re-entry into init when a binding is missing

  Scenario: Language used but binding undeclared
    Given a project with an initialized grid
    And a declared `mutation-score` clause references
        `language = "rust"`
    But the project has no `mutation-score.rust` binding
    When the harness attempts to evaluate the clause
    Then the harness suspends the current pass with reason
        `missing-binding`
    And the harness re-enters init scoped to the missing binding
        only (the rest of the grid is preserved)
    And the operator declares the binding
    And the suspended pass resumes against the now-complete config

  Scenario: Multiple bindings missing in one pass
    Given a pass that references three bindings, two of which are
        missing
    When the harness suspends and re-enters init
    Then init collects all missing bindings and presents them
        together for operator declaration in a single re-entry
```

### F-4: Refusal flow

```gherkin
Feature: Init may refuse to proceed

  Scenario: Low-risk profile detected
    Given a project profile with
      | property | value |
      | bounded contexts | 1 |
      | cross-context seams | 0 |
      | novel architecture | false |
      | correctness-critical | false |
    When init evaluates the profile
    Then init proposes refusal
    And the operator may accept (init exits, ghyll terminates) or
        override (residue note required, init proceeds)

  Scenario: High-risk profile bypasses refusal
    Given a project profile with
      | property | value |
      | bounded contexts | 4 |
      | cross-context seams | 6 |
      | correctness-critical | true |
    When init evaluates the profile
    Then init proceeds without proposing refusal
    And the auto-propose flow runs per F-5
```

### F-5: Auto-propose + operator-confirm loop

```gherkin
Feature: Auto-propose with operator confirmation

  Scenario: Per (role-pair, context) arrow proposal
    Given init has declared 2 bounded contexts
    When init enters the auto-propose flow
    Then for each (role-pair, context) arrow in the diamond, init
        proposes the role file's full exit-gate clause set with:
          - clause id / description
          - evaluation type (machine/attested)
          - depth type (depth-robust/depth-sensitive)
          - default cost (per concept schema)
          - default arguments (per role file template)

  Scenario: Operator confirms a clause unchanged
    Given a proposed clause `analyst.G1 = unique-definition(...)`
    When the operator returns `confirm`
    Then the clause is recorded into the grid with the proposed
        arguments

  Scenario: Operator modifies a clause (raises threshold)
    Given a proposed clause `mutation-score(threshold=0.7)`
    When the operator returns `modify` with `{threshold: 0.85}`
    Then the clause is recorded with threshold 0.85
    And the modification is allowed because 0.85 > 0.7 (raise only,
        per D20)

  Scenario: Operator cannot lower a threshold
    Given a proposed clause `mutation-score(threshold=0.7)`
    When the operator returns `modify` with `{threshold: 0.5}`
    Then init refuses the modification with
        `cannot-weaken-default`

  Scenario: Operator extends with a per-context clause
    Given a proposed exit gate
    When the operator returns `extend` with a new clause not in
        the role file
    Then the new clause is recorded alongside the role-file
        defaults

  Scenario: Operator skips a clause (with residue)
    Given a proposed clause
    When the operator returns `skip`
    And provides a residue entry: `{reason: "<text>"}`
    Then the clause is dropped from this (role-pair, context) arrow
    And the residue entry is recorded in the grid's residue list

  Scenario: Operator skips without residue
    Given a proposed clause
    When the operator returns `skip` without a residue entry
    Then init refuses the skip with `residue-required-for-skip`
    And re-prompts the operator
```

### F-6: Atomic grid write

```gherkin
Feature: Grid is written atomically

  Scenario: Successful init
    Given the operator has provided a verdict for every proposed
        clause
    And every binding referenced by the grid is declared
    And the init arrow's exit gate is `complete`
    When init writes the grid
    Then `.ghyll/grid.yaml` (or equivalent) is written atomically
        (temp file + rename)
    And the grid file contains version `v1`
    And subsequent ghyll invocations read the grid from this file
        instead of re-running init

  Scenario: Init crashes mid-write
    Given init is partway through writing the grid file
    When the process is killed
    Then the next ghyll invocation observes no partial grid (the
        temp file is unlinked or never renamed)
    And init re-runs from scratch (no resume from partial state)
```

### F-7: Operator-session declaration

```gherkin
Feature: op-id declared at session start

  Scenario: Operator session start
    Given the operator runs `ghyll init`
    When init first reaches a step that requires attestation
    Then init prompts for `op-id`
    And the operator provides a non-empty string
    And `op-id` is recorded in every attestation thereafter

  Scenario: op-id is empty
    Given the operator provides empty `op-id`
    Then init refuses to proceed with `op-id-required`
```

---

## Failure modes

| ID | Failure | Blast radius | Intended degradation |
|---|---|---|---|
| FM-1 | Operator abandons init mid-flow | Project | No grid recorded; subsequent ghyll invocation re-runs init from scratch. Partial attestations in `attestations/` are kept for forensic value but do not count. |
| FM-2 | Operator declares conflicting verdicts (two confirms with different modifications) | Init session | The last verdict per clause wins; attestation log records both for audit. Operator-tooling can flag the conflict for review post-init. |
| FM-3 | A binding command (e.g., `staticcheck`) is not installed | Init session | Init fails the binding's `compiles`/`lint-clean` self-test; presents the operator with a clear error pointing at the missing tool; init does NOT auto-install. Operator either installs and retries or declares a different binding. |
| FM-4 | Auto-propose response from operator cannot be parsed | Single clause | Init re-prompts with a parse-error message; does not consume a clause's "verdict slot." |
| FM-5 | A bounded-context name collides with an existing one (re-init) | Init session | Init refuses with `context-name-collision`; operator either renames or merges contexts. |
| FM-6 | Grid file write fails (disk full, permissions) | Project | Init exits with `grid-write-failed`; no partial state on disk; project remains uninitialized. |
| FM-7 | Operator declares `op-id` but the harness later detects spoofing (different sessions claim same `op-id` from different machines without coordination) | Multi-operator project | Out of scope for v1: the schema records `op-id` as declared; trust is operator-side. |

---

## Cross-component interactions

- **Init → State machine engine.** Init records the init-arrow's
  clause statuses and pass-status per the canonical state machine
  (`gates.md` §7). The engine treats the init arrow like any other
  arrow.
- **Init → Attestation flow.** Init's `attested` clauses go through
  the standard attestation flow with `op-id` provenance per `gates.md`
  §10.2.
- **Init → Catalogue concepts.** Init reads the per-concept schema
  files (`gates/concepts/*.yaml`) to know what arguments each concept
  takes and how to validate operator modifications.
- **Init → Role files.** Init reads `roles/*.md` to know what each
  role's exit-gate template is. Each role's clauses become
  auto-proposals during F-5.
- **Init → Grid amendment / global lock.** Init's grid write takes
  the project-wide write-lock per D22, but at init time the lock is
  always available (no other arrow is running yet — invariant 1).
- **Init → Adversarial phase.** Init's own arrow has an adversarial
  phase (it carries `depth-sensitive` clauses about depth-type
  assignments and residue honesty). The adversary identity is the
  synthetic `adversary` role-id per `gates.md` §1.1.

---

## Assumptions

| ID | Assumption | Falsifiability |
|---|---|---|
| A-1 | Operators can articulate bounded contexts when interrogated. | Falsifies if operators consistently produce too-coarse or too-fine contexts at init, requiring repeated re-init. |
| A-2 | The diamond's role files are stable enough to serve as auto-propose templates. | Falsifies if role files require per-project rewriting more often than per-project extension. |
| A-3 | Operators will accept refusal recommendations rather than override every one. | Falsifies if refusal acceptance rate is near 0 in practice — refusal becomes dead friction. |
| A-4 | Init's interactive interrogation is acceptable as a single-session activity. | Falsifies if init regularly takes more than ~1 hour, suggesting it should be resumable across sessions. |
| A-5 | Bindings declared at init are stable across the project's lifetime. | Falsifies if projects frequently change languages or binding tools post-init, requiring re-init churn. |

---

## Open questions

- **Resumable init.** A-4 assumes init is a single session; if init
  needs to span sessions (large projects, multiple operators, etc.),
  partial-state persistence becomes required. Currently the schema
  forbids partial grid writes; resumable init would require a
  separate `init-draft.yaml` distinct from the recorded grid.
- **Init for projects with sub-projects.** Mono-repos with multiple
  semantically-distinct sub-projects may want one init per sub-project
  rather than one for the whole repo. The schema indexes the grid
  by bounded-context, which could absorb this; but the directory
  layout for `attestations/` and `grid.yaml` assumes one project.
- **Init's adversarial phase.** §11 says the adversarial phase
  attacks the upstream artifact. Init's "upstream" is the operator's
  intent (no prior artifact). The adversarial attack on init's
  proposed grid is therefore: a fresh `adversary` instance reads
  the proposed grid and attempts to identify residue the operator
  didn't declare. This is a useful but unusual application of the
  phase; its implementation may need extra detail.
- **Init refusal vs override**. F-4 lets the operator override
  refusal. Should the override require an architect-arrow-level
  attestation (heavier than confirm), since it commits the operator
  to ghyll's friction for a project that doesn't need it? Currently
  unspecified.
