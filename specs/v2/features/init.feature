# Implementation: built (phase 2 — bootstrap/init.go + bootstrap/propose.go
# + bootstrap/modify.go + bootstrap/orphan.go + bootstrap/risk.go +
# bootstrap/profile.go). Step impls partially landed; remaining pending
# bodies lifted in Phase B5 of v2-final consolidation.
Feature: Project initialization

  # Project initialization is step one when ghyll is invoked on a new
  # project. Auto-propose + operator-confirm-or-modify-or-extend-or-skip-with-residue
  # turns the harness's v0 baseline into the project's v1 grid.
  # See specs/direction/components/init.md for the full spec.

  # ---- Greenfield init ----

  Scenario: Empty repository
    Given a project directory with no source files and no prior grid
    When the operator runs "ghyll init"
    And declares op-id "alice@example.com"
    Then init proposes the diamond as the declared arrow set
    And init proposes 0 bounded contexts initially
    And init interrogates the operator to identify bounded contexts from intent

  Scenario: Operator declares contexts via interrogation
    Given a greenfield init in progress
    And the operator has answered the context-identification interrogation with ["ContextA", "ContextB"]
    Then init records 2 bounded contexts
    And init auto-proposes role exit-gate clauses for each (role-pair, context) arrow per the auto-propose feature

  Scenario: Greenfield refusal recommendation
    Given a project the operator describes as "throwaway script, one bounded context, no novel architecture"
    When init evaluates the project profile
    Then init proposes refusal with rationale
    And the operator either accepts refusal (init exits) or overrides (init proceeds with a residue note recording the override)

  # ---- Brownfield init ----

  Scenario: Existing code with no prior ghyll grid
    Given a project directory with existing source code in src/contextA/ and src/contextB/
    And no prior .ghyll/grid.current file exists
    When the operator runs "ghyll init"
    Then init runs the mode-determinable-from-repo rule and determines mode = brownfield
    And init proposes bounded contexts ["contextA", "contextB"] based on directory structure
    And the operator confirms or refines the proposal

  Scenario: Init runs orphan-symbol extraction during brownfield discovery
    Given a brownfield init with declared bounded contexts
    When init walks each context's source
    Then init extracts the exported-symbol list per language
    And presents orphans (symbols with no clear spec mapping) as residue candidates for operator triage

  # ---- Re-init on missing binding ----

  Scenario: Language used but binding undeclared
    Given a project with an initialized grid
    And a declared mutation-score clause references language "rust"
    But the project has no mutation-score.rust binding
    When the harness attempts to evaluate the clause
    Then the harness suspends the current pass with reason "missing-binding"
    And the harness re-enters init scoped to the missing binding only
    And the operator declares the binding
    And the suspended pass resumes against the now-complete config

  Scenario: Multiple bindings missing in one pass
    Given a pass that references three bindings, two of which are missing
    When the harness suspends and re-enters init
    Then init collects all missing bindings and presents them together for operator declaration in a single re-entry

  # ---- Refusal flow ----

  Scenario: Low-risk profile detected
    Given a project profile with
      | property            | value |
      | bounded contexts    | 1     |
      | cross-context seams | 0     |
      | novel architecture  | false |
      | correctness-critical| false |
    When init evaluates the profile
    Then init proposes refusal
    And the operator may accept (init exits, ghyll terminates) or override (residue note required, init proceeds)

  Scenario: High-risk profile bypasses refusal
    Given a project profile with
      | property            | value |
      | bounded contexts    | 4     |
      | cross-context seams | 6     |
      | correctness-critical| true  |
    When init evaluates the profile
    Then init proceeds without proposing refusal
    And the auto-propose flow runs

  # ---- Auto-propose + operator-confirm loop ----

  Scenario: Per (role-pair, context) arrow proposal
    Given init has declared 2 bounded contexts
    When init enters the auto-propose flow
    Then for each (role-pair, context) arrow in the diamond, init proposes the role file's full exit-gate clause set with clause id, description, eval type, depth type, default cost, and default arguments

  Scenario: Operator confirms a clause unchanged
    Given a proposed clause "analyst.G1 = unique-definition(...)"
    When the operator returns "confirm"
    Then the clause is recorded into the grid with the proposed arguments

  Scenario: Operator modifies a clause raising threshold
    Given a proposed clause "mutation-score(threshold=0.7)"
    When the operator returns "modify" with {threshold: 0.85}
    Then the clause is recorded with threshold 0.85
    And the modification is allowed because 0.85 > 0.7 (raise only)

  Scenario: Operator cannot lower a threshold
    Given a proposed clause "mutation-score(threshold=0.7)"
    When the operator returns "modify" with {threshold: 0.5}
    Then init refuses the modification with "cannot-weaken-default"

  Scenario: Operator extends with a per-context clause
    Given a proposed exit gate
    When the operator returns "extend" with a new clause not in the role file
    Then the new clause is recorded alongside the role-file defaults

  Scenario: Operator skips a clause with residue
    Given a proposed clause
    When the operator returns "skip" with residue entry {reason: "<text>"}
    Then the clause is dropped from this (role-pair, context) arrow
    And the residue entry is recorded in the grid's residue list

  Scenario: Operator skips without residue is refused
    Given a proposed clause
    When the operator returns "skip" without a residue entry
    Then init refuses the skip with "residue-required-for-skip"
    And re-prompts the operator

  # ---- Atomic grid write (D31: versioned files + grid.current pointer) ----

  Scenario: Successful init writes versioned grid + pointer
    Given the operator has provided a verdict for every proposed clause
    And every binding referenced by the grid is declared
    And the init arrow's exit gate is "complete"
    When init writes the grid
    Then ".ghyll/grid.v1.yaml" is written atomically (temp file + rename)
    And ".ghyll/grid.current" is then written atomically containing the single line "v1"
    And subsequent ghyll invocations read grid.current to find the active version, then load grid.v<N>.yaml

  Scenario: Init crashes mid-write
    Given init is partway through writing the grid file
    When the process is killed
    Then the next ghyll invocation observes no partial grid
    And init re-runs from scratch (no resume from partial state)

  # ---- op-id session declaration ----

  Scenario: Operator session start
    Given the operator runs "ghyll init"
    When init first reaches a step that requires attestation
    Then init prompts for op-id
    And the operator provides a non-empty string
    And op-id is recorded in every attestation thereafter

  Scenario: Empty op-id is refused
    Given the operator provides empty op-id ""
    Then session start is refused with "op-id-required"

  # ---- Adversarial additions: grid file inconsistency ----

  Scenario: grid.current points at a missing grid file
    Given ".ghyll/grid.current" contains "v3"
    But ".ghyll/grid.v3.yaml" does not exist (deletion, partial restore, operator manually edited grid.current)
    When the harness initializes
    Then the engine refuses to start any pass with "grid-current-points-to-missing-version"
    And presents the operator with options to restore the file, re-point grid.current, or re-run init
    And no pass is started until the operator resolves the inconsistency

  Scenario: grid.current is corrupted or empty
    Given ".ghyll/grid.current" exists but contains garbage (binary, empty, multiple lines, version not matching pattern "v<N>")
    When the harness initializes
    Then the engine refuses to start with "grid-current-malformed"
    And reports the actual content for operator triage
    And operator must repair grid.current or remove it (forcing re-init)

  Scenario: Multiple grid versions on disk, grid.current missing
    Given ".ghyll/grid.v1.yaml", ".ghyll/grid.v2.yaml" exist
    But ".ghyll/grid.current" is absent
    When the harness initializes
    Then the engine does NOT silently pick the latest (refuses to assume)
    And the engine surfaces "grid-current-absent" with a list of available versions
    And operator must declare which is current (or re-init)

  # ---- Adversarial additions: modify edge cases (raise-only) ----

  Scenario Outline: Modify a non-monotonic argument
    Given a proposed clause with argument "<arg>"=<original>
    When the operator returns "modify" with <arg>=<proposed>
    Then init <action>

    Examples:
      | arg                | original      | proposed      | action                                                          |
      | scope              | "src/**"      | "src/main.go" | accepts (narrower scope is tighter, fewer files allowed to fail)|
      | scope              | "src/main.go" | "src/**"      | refuses with "cannot-weaken-default: wider scope"               |
      | regex              | "^TODO"       | "^TODO\|^XXX" | refuses with "regex-modify-not-supported"                       |
      | regex              | "^TODO\|^XXX" | "^TODO"       | refuses with "regex-modify-not-supported"                       |
      | severity-threshold | high          | medium        | refuses with "cannot-weaken-default: lower threshold"           |
      | nonexistent        | (any)         | (any)         | refuses with "modify-on-unknown-field"                          |

  Scenario: Modify on a clause not in the proposal
    Given init has not proposed clause "C99"
    When the operator submits "modify" against "C99"
    Then init refuses with "modify-on-unknown-clause" and lists the proposed clause IDs for orientation

  # ---- Adversarial additions: op-id at init ----

  Scenario: op-id declared once survives init re-entry
    Given the operator declared op-id "alice@example.com" at init start
    And init was suspended for missing-binding and re-entered
    When init re-enters
    Then the active op-id is still "alice@example.com" (single declaration per session per attestation.md F-1)
    And re-init does NOT re-prompt for op-id

  Scenario: op-id mid-session change requires explicit handoff
    Given operator Alice is active mid-init
    When Bob attempts to declare op-id "bob@example.com" without Alice closing first
    Then init refuses with "session-already-active" and lists Alice's op-id
    And Bob must either ask Alice to close her session OR start a new session (multi-operator handoff per attestation.md F-1)
