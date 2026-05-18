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
    And init interrogates the operator to identify bounded contexts
        from intent

  Scenario: Operator declares contexts via interrogation
    Given a greenfield init in progress
    And the operator has answered the context-identification
        interrogation with ["ContextA", "ContextB"]
    Then init records 2 bounded contexts
    And init auto-proposes role exit-gate clauses for each
        (role-pair, context) arrow per the auto-propose feature

  Scenario: Greenfield refusal recommendation
    Given a project the operator describes as "throwaway script, one
        bounded context, no novel architecture"
    When init evaluates the project profile
    Then init proposes refusal with rationale
    And the operator either accepts refusal (init exits) or
        overrides (init proceeds with a residue note recording the
        override)

  # ---- Brownfield init ----

  Scenario: Existing code with no prior ghyll grid
    Given a project directory with existing source code in
        src/contextA/ and src/contextB/
    And no prior .ghyll/grid.current file exists
    When the operator runs "ghyll init"
    Then init runs the mode-determinable-from-repo rule and
        determines mode = brownfield
    And init proposes bounded contexts ["contextA", "contextB"]
        based on directory structure
    And the operator confirms or refines the proposal

  Scenario: Init runs orphan-symbol extraction during brownfield discovery
    Given a brownfield init with declared bounded contexts
    When init walks each context's source
    Then init extracts the exported-symbol list per language
    And presents orphans (symbols with no clear spec mapping) as
        residue candidates for operator triage

  # ---- Re-init on missing binding ----

  Scenario: Language used but binding undeclared
    Given a project with an initialized grid
    And a declared mutation-score clause references language "rust"
    But the project has no mutation-score.rust binding
    When the harness attempts to evaluate the clause
    Then the harness suspends the current pass with reason
        "missing-binding"
    And the harness re-enters init scoped to the missing binding only
    And the operator declares the binding
    And the suspended pass resumes against the now-complete config

  Scenario: Multiple bindings missing in one pass
    Given a pass that references three bindings, two of which are missing
    When the harness suspends and re-enters init
    Then init collects all missing bindings and presents them
        together for operator declaration in a single re-entry

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
    And the operator may accept (init exits, ghyll terminates) or
        override (residue note required, init proceeds)

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
    Then for each (role-pair, context) arrow in the diamond, init
        proposes the role file's full exit-gate clause set with
        clause id, description, eval type, depth type, default cost,
        and default arguments

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
    When the operator returns "extend" with a new clause not in the
        role file
    Then the new clause is recorded alongside the role-file defaults

  Scenario: Operator skips a clause with residue
    Given a proposed clause
    When the operator returns "skip" with residue entry
        {reason: "<text>"}
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
    And ".ghyll/grid.current" is then written atomically containing
        the single line "v1"
    And subsequent ghyll invocations read grid.current to find the
        active version, then load grid.v<N>.yaml

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
