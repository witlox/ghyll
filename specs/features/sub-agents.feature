Feature: Sub-agents
  The model can spawn focused sub-agent inference calls
  as a tool, with isolated context and their own turn-loop.

  Background:
    Given a running session with model "glm5"
    And the workspace is "/tmp/ghyll-test-agents"
    And model "m25" is available at its configured endpoint

  Scenario: Model spawns a sub-agent for file exploration
    When the model calls agent with task "Find all test files and summarize the test structure"
    Then a sub-agent is created on model "m25"
    And the sub-agent context contains only the system prompt and task description
    And the sub-agent does not have the parent's conversation history
    And the sub-agent runs its turn-loop until completion
    And the sub-agent result is returned to the parent as a tool result

  Scenario: Sub-agent has access to all tools
    When the model calls agent with task "Read main.go and grep for TODO comments"
    Then the sub-agent can call read_file
    And the sub-agent can call grep
    And the sub-agent can call bash

  Scenario: Sub-agent defaults to fast tier
    When the model calls agent with task "List files in src/"
    Then the sub-agent runs on model "m25"

  Scenario: Sub-agent respects maximum turn count
    Given the sub-agent turn limit is 20
    When the model calls agent with task "Explore everything in the entire codebase recursively"
    And the sub-agent reaches 20 turns without completing
    Then the sub-agent returns a partial result to the parent
    And the partial result includes what was accomplished

  Scenario: Sub-agent cannot spawn sub-agents
    When the model calls agent with task "Spawn another agent to help"
    Then the sub-agent's available tools do not include "agent"

  Scenario: Sub-agent shares the session lockfile
    When the model calls agent with task "Check something"
    Then no additional lockfile is created
    And the session lockfile remains held

  Scenario: Sub-agent inherits project instructions but not role
    Given project instructions exist at "/tmp/ghyll-test-agents/.ghyll/instructions.md"
    And the parent session has role "analyst" active
    When the model calls agent with task "Review the code"
    Then the sub-agent's system prompt includes the project instructions
    And the sub-agent's system prompt does not include the analyst role overlay

  Scenario: Sub-agent failure returns error to parent
    Given model "m25" endpoint is unreachable
    When the model calls agent with task "Do something"
    Then the tool result indicates error "sub-agent model unreachable"
    And the parent session continues normally

  Scenario: Sub-agent tool calls respect timeouts
    When the model calls agent with task "Run a long build"
    And the sub-agent calls bash with command "sleep 120"
    Then the bash call times out after the configured bash timeout
    And the sub-agent receives the timeout error and continues

  Scenario: Sub-agent respects token budget
    Given the sub-agent token budget is 50000
    When the model calls agent with task "Read every file in the project"
    And the sub-agent accumulates 50000 tokens of prompt and completion
    Then the sub-agent terminates with a partial result
    And the partial result includes what was accomplished before budget exhaustion

  Scenario: Multiple sub-agents can run sequentially
    When the model calls agent with task "Explore tests"
    And the sub-agent completes
    And the model calls agent with task "Explore configs"
    And the sub-agent completes
    Then both results are available in the parent context

  Scenario: Sub-agent wall-clock timeout
    Given the sub-agent timeout is 300 seconds
    When the model calls agent with task "Run an extremely long analysis"
    And the sub-agent runs for 300 seconds without completing
    Then the sub-agent is terminated
    And a partial result is returned to the parent
    And the partial result indicates "wall-clock timeout"

  Scenario: Sub-agent does not inherit plan mode
    Given plan mode is active in the parent session
    When the model calls agent with task "Explore the codebase"
    Then the sub-agent's system prompt does not include planning instructions

  Scenario: Sub-agent tool set excludes plan mode tools
    When the model calls agent with task "Analyze something"
    Then the sub-agent's available tools do not include "enter_plan_mode"
    And the sub-agent's available tools do not include "exit_plan_mode"

  Scenario: Sub-agent has access to new tools
    When the model calls agent with task "Find and edit a file"
    Then the sub-agent can call edit_file
    And the sub-agent can call glob
    And the sub-agent can call web_fetch
    And the sub-agent can call web_search

  Scenario: Sub-agent has no checkpoint creation
    When the model calls agent with task "Do work across 10 turns"
    And the sub-agent completes after 10 turns
    Then no checkpoints were created during sub-agent execution

  Scenario: Sub-agent has no drift detection
    When the model calls agent with task "Work on something for many turns"
    And the sub-agent completes
    Then no drift measurement was performed during sub-agent execution

  Scenario: Sub-agent result is included in parent checkpoint
    When the model calls agent with task "Analyze the bug"
    And the sub-agent completes with a result
    And a checkpoint is created
    Then the checkpoint summary includes the sub-agent's findings
