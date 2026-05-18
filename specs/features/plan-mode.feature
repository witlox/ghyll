Feature: Plan mode
  Plan mode augments the system prompt to encourage deeper
  reasoning. All tools remain available — it is advisory only.

  Background:
    Given a running session with model "m25"
    And the default system prompt is "You are a coding assistant working in /tmp/test. Be concise and direct."

  Scenario: User activates plan mode via REPL command
    When the user types "/plan"
    Then plan mode is active
    And the system prompt contains the dialect's planning instructions
    And all tools remain available

  Scenario: User deactivates plan mode via /fast
    Given plan mode is active
    When the user types "/fast"
    Then plan mode is inactive
    And the system prompt reverts to the default

  Scenario: Model activates plan mode via tool call
    When the model calls enter_plan_mode with reason "need to analyze architecture before making changes"
    Then plan mode is active
    And the system prompt contains the dialect's planning instructions

  Scenario: Model deactivates plan mode via tool call
    Given plan mode is active via model request
    When the model calls exit_plan_mode
    Then plan mode is inactive
    And the system prompt reverts to the default

  Scenario: Plan mode survives compaction
    Given plan mode is active
    When proactive compaction is triggered
    Then plan mode is still active after compaction

  Scenario: Plan mode does not block tool calls
    Given plan mode is active
    When the model calls bash with command "echo test"
    Then the tool executes successfully
    When the model calls write_file with path "/tmp/test/new.go" and content "package main"
    Then the tool executes successfully

  Scenario: Plan mode persists across model switch
    Given plan mode is active
    When routing escalates to "glm5"
    Then plan mode is still active on the new model
    And the GLM-5 system prompt contains GLM-5's planning instructions

  Scenario: /status shows plan mode state
    Given plan mode is active
    When the user types "/status"
    Then the status output includes "plan: on"

  Scenario: Plan mode has no effect when already active
    Given plan mode is active
    When the user types "/plan"
    Then plan mode remains active
    And no error is displayed

  Scenario: Plan mode recorded in checkpoint metadata
    Given plan mode is active
    When a checkpoint is created
    Then the checkpoint has plan_mode = true

  Scenario: Plan mode not active — checkpoint records false
    Given plan mode is inactive
    When a checkpoint is created
    Then the checkpoint has plan_mode = false

  Scenario: Plan mode does not affect routing decisions
    Given plan mode is active
    And context depth is below the escalation threshold
    When the routing decision is evaluated
    Then the decision is "none"
    And plan mode is not considered in the routing evaluation

  Scenario: /deep and /plan are independent
    Given plan mode is active
    When the user types "/deep"
    Then plan mode is still active
    And the model is escalated to "glm5"
    And the system prompt contains GLM-5's planning instructions

  Scenario: /fast clears both deep override and plan mode
    Given plan mode is active
    And deep override is active
    When the user types "/fast"
    Then plan mode is inactive
    And deep override is inactive
    And the system prompt is the base dialect prompt
