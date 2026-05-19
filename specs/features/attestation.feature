# Implementation: PARTIAL.
#   Built today: §7.1 dispatch-pending response, op-id session start,
#   on-the-spot creation suspension. Deferred surface (full operator
#   event bus, multi-operator handoff, typed JSONL verdict records,
#   insufficient-basis-rounds-max escalation) is tagged `@deferred`
#   and skipped until those flows ship.
Feature: Operator attestation flow

  # Coordinates operator verdicts on attested clauses. Owns the operator
  # event bus. Captures verdicts, emits typed JSONL records, handles
  # insufficient-basis-rounds-max escalation, supports multi-operator
  # handoff within a single pass.
  # See specs/architecture/components/attestation.md.

  # ---- Session lifecycle ----

  Scenario: Session start with op-id
    Given the harness has no active operator session
    When the operator declares op-id "alice@example.com"
    Then the component creates a session bound to that op-id
    And the session is active for subsequent verdicts
    And verdict-capture API calls without an active session are refused with "no-active-session"

  @deferred
  Scenario: Multi-operator handoff in one pass
    Given operator Alice is active and attests clause C1
    When Alice ends her session
    And operator Bob declares op-id "bob@example.com" and starts
    Then Bob is now active
    And Bob may attest clauses C2, C3 within the same pass
    And the attestation file for the pass records Alice's verdict on C1 with op-id alice and Bob's verdicts on C2, C3 with op-id bob

  Scenario: Empty op-id is refused
    Given operator attempts to declare op-id ""
    Then the attestation flow refuses session start with "op-id-required"

  # ---- Hint presentation and verdict capture ----

  @deferred
  Scenario: Operator returns pass
    Given an attestation request for clause C5 with hint { locations: [features/contextA/payment.feature:42-67], basis: "all failure-path scenarios in this region", residue: "happy-path tests not scanned" }
    And operator Alice is active
    When Alice inspects the locations and submits verdict "pass" with unit "confirm"
    Then a record is appended (O_APPEND) to the per-pass attestation file at "attestations/v<N>/contextA/stratum-<S>/<role-pair>/<pass-id>.jsonl" where <role-pair> uses "__" as the separator (e.g., "analyst__architect", "analyst__adversary__architect", "init__analyst")
    And the append is atomic up to PIPE_BUF (records are < 4KB so atomic on POSIX)
    And the file is fsync'd before the verdict is reported as accepted (durability before status flip)
    And the record carries unit "confirm", clause C5, verdict "pass", ts (ISO8601 UTC), op-id "alice@example.com"
    And the JSONL line is valid JSON with newline terminator (no trailing comma, no missing newline)
    And the component signals the state-machine engine to transition C5 to "pass" ONLY AFTER the fsync returns successfully

  @deferred
  Scenario: Operator returns fail with record-locations
    Given an attestation request for clause C5
    When Alice submits verdict "fail" with unit "record-locations-inspected" and inspected list [features/contextA/payment.feature:42-50]
    Then a record is appended with the inspected list
    And C5's status becomes "fail"
    And the producer is notified of the failure to remediate

  @deferred
  Scenario: Operator returns insufficient-basis with residue note
    Given an attestation request for clause C5
    When Alice submits verdict "insufficient-basis" with unit "write-residue-note" and residue-note "feature file is too large to manually inspect; need a deeper artifact"
    Then a record is appended with the residue note
    And the attestation flow signals state-machine engine to transition C5 to "insufficient-basis"
    And the engine derives the arrow's status (attestation flow does NOT directly set arrow status; arrow status is always derived)
    And the round counter for C5 increments to 1

  # ---- Insufficient-basis escalation (insufficient-basis-rounds-max) ----

  @deferred
  Scenario: Three rounds, then escalation
    Given clause C5 has received "insufficient-basis" from rounds 1 and 2
    And the producer has re-emitted the hint at a deeper depth tier each round
    When round 3 also returns "insufficient-basis"
    Then the component records the escalation
    And presents the operator with two options: (1) attest "accepted-risk" with "write-residue-note" recording why the basis remains insufficient, OR (2) route the artifact back upstream for deeper rework with rationale "requires-deeper-artifact"
    And neither option is the default — operator must choose

  @deferred
  Scenario: Operator accepts risk on the third round
    Given the escalation prompt
    When operator chooses option 1 with residue note
    Then a record is appended with unit "write-residue-note", verdict "accepted-risk", op-id, inspected list, and residue-note
    And the FINDING associated with C5 transitions to status "accepted-risk"
    And C5's CLAUSE-status transitions to "pass" once all findings on the clause are disposed (resolved or accepted-risk)
    And the round counter resets

  @deferred
  Scenario: Operator routes upstream
    Given the escalation prompt
    When operator chooses option 2 with rationale
    Then the component signals the runner that C5's upstream artifact requires deeper rework
    And the arrow's pass is aborted with reason "requires-deeper-artifact"
    And the producer role is re-routed at a deeper tier to produce a richer artifact

  @deferred
  Scenario: insufficient-basis-rounds-max is configurable
    Given init declared insufficient-basis-rounds-max=5 for this project
    When clause C5 receives "insufficient-basis" for the 4th time
    Then no escalation is triggered yet (max not reached)
    And the round counter is 4

  @deferred
  Scenario: insufficient-basis-rounds-max escalation actually fires at max
    Given init declared insufficient-basis-rounds-max=3 for this project
    And clause C5 has received "insufficient-basis" for the 2nd time
    When clause C5 receives "insufficient-basis" for the 3rd time
    Then escalation IS triggered (round counter reached max)
    And the operator event bus publishes an "escalation-request" for clause C5

  Scenario Outline: Invalid insufficient-basis-rounds-max rejected at init
    # Wired against bootstrap.GridDefaults.validate which rejects
    # non-positive integers with the typed sentinel
    # ErrInsufficientBasisRoundsMaxNonPositive. See bootstrap/init.go.
    Given init proposes insufficient-basis-rounds-max="<value>"
    Then init rejects the value with "<error>"

    Examples:
      | value | error                                          |
      | 0     | insufficient-basis-rounds-max-must-be-positive |
      | -1    | insufficient-basis-rounds-max-must-be-positive |

  @deferred
  Scenario Outline: Invalid insufficient-basis-rounds-max — non-integer (deferred YAML loader)
    # The "abc" row requires a YAML/TOML loader that maps a string-into-
    # int parse failure to the spec's typed sentinel
    # "insufficient-basis-rounds-max-must-be-integer". The GridDefaults
    # struct's int field rejects via yaml.Unmarshal, but the error name
    # surfaces as a generic Go decode error, not the spec wire form.
    # Phase-11 ships the canonical loader; until then this row is
    # deferred.
    Given init proposes insufficient-basis-rounds-max="<value>"
    Then init rejects the value with "<error>"

    Examples:
      | value | error                                          |
      | abc   | insufficient-basis-rounds-max-must-be-integer  |

  # ---- Accepted-risk for findings ----

  Scenario: Producer proposes accepted-risk
    Given finding F1 with status "open"
    And the producer proposes accepted-risk for F1
    When the adversarial component hands F1 to attestation flow
    Then this component presents F1's evidence and the producer's rationale to the active operator
    And captures the operator's verdict

  Scenario: Operator attests accepted-risk on F1
    Given the operator inspects F1's evidence
    When operator submits "accepted-risk" verdict
    Then a record is appended (unit per severity)
    And F1's status becomes "accepted-risk"

  Scenario: Operator rejects accepted-risk proposal
    Given the operator finds the producer's proposal weak
    When operator submits "fail" on the accepted-risk request
    Then F1 stays "open"
    And the producer must continue remediation

  # ---- Verifier-driven verdict replay ----

  @deferred
  Scenario: Verifier reads attestation log
    Given a pass-id and a clause-id
    When the verifier component reads the attestation file
    Then it finds all records for that clause in chronological order
    And can reconstruct the operator's decision chain
    And can verify that the required fields per unit are present

  @deferred
  Scenario: Missing required field is detected
    Given an attestation record with unit "record-locations-inspected" but no "inspected" array
    When the verifier reads it
    Then the record is flagged as malformed
    And the operator session that produced it is alerted

  # ---- Adversarial additions: op-id adversarial input ----

  Scenario Outline: op-id with dangerous characters is rejected
    Given the operator attempts to declare op-id "<op-id>"
    Then the attestation flow refuses session start with "<error>"
    And no path on disk is ever created using the raw op-id (op-id is recorded in record JSON only, never used as a filesystem component)

    Examples:
      | op-id          | error                    |
      | ../etc/passwd  | op-id-invalid-characters |
      | alice/bob      | op-id-invalid-characters |
      | alice\x00null  | op-id-invalid-characters |
      | alice\nbob     | op-id-invalid-characters |

  @deferred
  Scenario Outline: op-id rejection — meta-described inputs
    # The original outline included rows like `(string of length 5000)`
    # and `(unicode RTL override U+202E)` which are META-DESCRIPTORS,
    # not literal Gherkin cell values. They require a programmatic
    # driver to materialize the actual rune sequence. Belongs in a
    # unit test against ValidateOpID; the BDD layer can't construct
    # these row values inline.
    Given the operator attempts to declare op-id "<op-id>"
    Then the attestation flow refuses session start with "<error>"

    Examples:
      | op-id                                | error                    |
      | (string of length 5000)              | op-id-too-long           |
      | (unicode RTL override U+202E)        | op-id-invalid-characters |
      | (empty after whitespace trim: "   ") | op-id-required           |

  Scenario: op-id leaks JSON injection are escaped
    Given the operator declares op-id 'alice","verdict":"pass' (containing JSON-syntactic characters)
    When a verdict is captured
    Then the JSONL record properly escapes the op-id value
    And re-parsing the record yields exactly the original op-id string (no injection succeeded)
    And the resulting verdict field is the operator's actual verdict, not the injected "pass"

  # ---- Adversarial additions: oversized residue note ----

  @deferred
  Scenario: Oversized residue note rejected
    Given an escalation prompt requesting a residue note
    When the operator submits a residue note longer than 16KB
    Then the component refuses with "residue-note-too-long" (configurable threshold)
    And re-prompts the operator
    And no oversized record is appended

  # ---- Adversarial additions: path canonicalization for three-role chain ----

  @deferred
  Scenario: Three-role chain path encoding
    Given an arrow with role-pair containing the adversary segment (e.g., analyst→adversary→architect)
    When an attestation record is written
    Then the path component for the role-pair is "analyst__adversary__architect" (double-underscore separator)
    And NOT "analyst-adversary-architect" or "analyst→adversary→architect"
    And the path is filesystem-portable (no Unicode glyphs, no path separators, ≤ 255 bytes per component)

  @deferred
  Scenario: init arrow path encoding
    Given an attestation record for the init arrow
    When the path is constructed
    Then the role-pair component is "init__analyst"
    And the context and stratum components are empty / "_" (init is project-scoped, not context-scoped — per components/init.md sub-phase A)
    And the path is consistently chosen (not sometimes "v<N>/_/_/init__analyst/..." and sometimes "v<N>/init__analyst/...")

  # ---- Adversarial additions: multi-operator handoff edge cases ----

  @deferred
  Scenario: Two operators submit verdicts on the same clause near-simultaneously
    Given Alice's session is active and Bob's session is also active
    And both submit verdicts on clause C5 within 10ms of each other
    When the component serializes verdict-capture (per-clause lock from state-machine.md)
    Then both verdicts are recorded as separate JSONL records in chronological order
    And the later record's verdict is authoritative for clause status
    And the audit log shows the conflict (two verdicts; later wins)
    And neither operator's submission is silently dropped

  @deferred
  Scenario: Operator's session ends mid-attestation
    Given Alice has read a hint and started typing a residue note
    When Alice's session is closed (network drop, explicit close, timeout) before she submits the verdict
    Then the attestation request is preserved on the operator event bus
    And the next operator session (Bob or Alice rejoining) sees the pending request
    And the round counter for the clause is NOT incremented (the attempt didn't complete; no insufficient-basis recorded)
