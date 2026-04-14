Feature: Workflow system
  Project instructions, roles, and slash commands are loaded
  from .ghyll/ to guide model behavior during a session.

  Background:
    Given a workspace directory "/tmp/ghyll-test-workflow"

  # --- Project Instructions ---

  Scenario: Load project instructions from .ghyll/
    Given a file "/tmp/ghyll-test-workflow/.ghyll/instructions.md" with content:
      """
      Always use BDD with TDD.
      Follow conventional commits.
      """
    When I start a session in "/tmp/ghyll-test-workflow"
    Then the system prompt contains "Always use BDD with TDD"
    And the system prompt contains "Follow conventional commits"

  Scenario: Load global instructions from ~/.ghyll/
    Given a file "~/.ghyll/instructions.md" with content:
      """
      Be concise and direct.
      """
    And no file exists at "/tmp/ghyll-test-workflow/.ghyll/instructions.md"
    When I start a session in "/tmp/ghyll-test-workflow"
    Then the system prompt contains "Be concise and direct"

  Scenario: Global and project instructions concatenated — project last
    Given a file "~/.ghyll/instructions.md" with content:
      """
      Use verbose logging in tests.
      """
    And a file "/tmp/ghyll-test-workflow/.ghyll/instructions.md" with content:
      """
      Use minimal logging in tests.
      """
    When I start a session in "/tmp/ghyll-test-workflow"
    Then the system prompt contains "Use verbose logging in tests"
    And the system prompt contains "Use minimal logging in tests"
    And "Use minimal logging in tests" appears after "Use verbose logging in tests" in the system prompt

  Scenario: Instructions survive compaction
    Given project instructions are loaded
    When proactive compaction is triggered
    Then the system prompt still contains the project instructions

  Scenario: Instruction budget enforced
    Given the instruction budget is 500 tokens
    And the combined instructions and role content is 800 tokens
    When I start a session in "/tmp/ghyll-test-workflow"
    Then the instructions are truncated to fit within 500 tokens
    And a warning is displayed: "instructions truncated to fit budget"

  # --- Roles ---

  Scenario: Load and activate role
    Given a file "/tmp/ghyll-test-workflow/.ghyll/roles/analyst.md" with content:
      """
      Extract and formalize specifications through interrogation.
      Do not write code. Produce specs only.
      """
    When the model activates role "analyst"
    Then the system prompt is appended with the analyst role content
    And the system prompt contains "Do not write code"

  Scenario: Role switch replaces previous role
    Given role "analyst" is active with content "Produce specs only."
    When the model activates role "implementer"
    Then the system prompt no longer contains "Produce specs only."
    And the system prompt contains the implementer role content

  Scenario: Role switch does not create checkpoint
    Given role "analyst" is active
    When the model activates role "implementer"
    Then no checkpoint is created for the role switch

  Scenario: Role switch does not trigger compaction
    Given role "analyst" is active
    And the context is at 50% capacity
    When the model activates role "implementer"
    Then compaction is not triggered

  Scenario: Project roles override global roles
    Given a file "~/.ghyll/roles/reviewer.md" with content "Be lenient."
    And a file "/tmp/ghyll-test-workflow/.ghyll/roles/reviewer.md" with content "Be strict."
    When the model activates role "reviewer"
    Then the system prompt contains "Be strict."
    And the system prompt does not contain "Be lenient."

  Scenario: No roles defined — bare dialect prompt
    Given no roles directory exists in ".ghyll/" or "~/.ghyll/"
    When I start a session in "/tmp/ghyll-test-workflow"
    Then the system prompt is the bare dialect system prompt only

  # --- Slash Commands ---

  Scenario: User-defined slash command injects prompt
    Given a file "/tmp/ghyll-test-workflow/.ghyll/commands/review.md" with content:
      """
      Review this code critically. Focus on bugs, security issues, and performance.
      """
    When the user types "/review"
    Then the content of review.md is injected as a user message
    And the model receives it as the next user input

  Scenario: Unknown slash command shows error
    When the user types "/nonexistent"
    Then an error is displayed: "unknown command: nonexistent"

  Scenario: Slash command does not modify session state
    Given a file "/tmp/ghyll-test-workflow/.ghyll/commands/review.md" exists
    When the user types "/review"
    Then plan mode is unchanged
    And the active role is unchanged
    And no checkpoint is created

  Scenario: Built-in REPL commands take precedence
    Given a file "/tmp/ghyll-test-workflow/.ghyll/commands/exit.md" exists
    When the user types "/exit"
    Then the session exits normally
    And the custom exit.md is not injected

  # --- Fallback Loading ---

  Scenario: Fallback to .claude/ when .ghyll/ absent — instructions
    Given no ".ghyll/" directory exists in "/tmp/ghyll-test-workflow"
    And a file "/tmp/ghyll-test-workflow/.claude/CLAUDE.md" with content:
      """
      Use diamond workflow for features.
      """
    When I start a session in "/tmp/ghyll-test-workflow"
    Then the system prompt contains "Use diamond workflow for features"

  Scenario: Fallback to .claude/ — roles loaded from roles/
    Given no ".ghyll/" directory exists in "/tmp/ghyll-test-workflow"
    And a file "/tmp/ghyll-test-workflow/.claude/roles/analyst.md" with content:
      """
      Do not write code. Produce specs only.
      """
    When I start a session in "/tmp/ghyll-test-workflow"
    And the model activates role "analyst"
    Then the system prompt contains "Do not write code"

  Scenario: Fallback to .claude/ — commands loaded from commands/
    Given no ".ghyll/" directory exists in "/tmp/ghyll-test-workflow"
    And a file "/tmp/ghyll-test-workflow/.claude/commands/verify.md" with content:
      """
      Run the full verification checklist.
      """
    When the user types "/verify"
    Then the content of verify.md is injected as a user message

  Scenario: Fallback mapping — CLAUDE.md treated as instructions.md
    Given no ".ghyll/" directory exists in "/tmp/ghyll-test-workflow"
    And a file "/tmp/ghyll-test-workflow/.claude/CLAUDE.md" exists
    And no file "/tmp/ghyll-test-workflow/.claude/instructions.md" exists
    When I start a session in "/tmp/ghyll-test-workflow"
    Then CLAUDE.md content is loaded as project instructions

  Scenario: .ghyll/ takes precedence over .claude/
    Given a file "/tmp/ghyll-test-workflow/.ghyll/instructions.md" with content "Use ghyll workflow."
    And a file "/tmp/ghyll-test-workflow/.claude/CLAUDE.md" with content "Use claude workflow."
    When I start a session in "/tmp/ghyll-test-workflow"
    Then the system prompt contains "Use ghyll workflow"
    And the system prompt does not contain "Use claude workflow"

  Scenario: No workflow folder — session starts with bare prompt
    Given no ".ghyll/" or ".claude/" directory exists in "/tmp/ghyll-test-workflow"
    When I start a session in "/tmp/ghyll-test-workflow"
    Then the system prompt is the bare dialect system prompt only
    And no warning is displayed
