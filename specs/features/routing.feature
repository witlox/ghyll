Feature: Context-depth routing
  The dialect router selects models based on context state,
  tool depth, and explicit user override.

  Background:
    Given ghyll is configured with endpoints for "m25" and "glm5"
    And the routing thresholds are context_depth=32000 and tool_depth=5

  Scenario: Fresh session starts on fast tier
    When a new session starts
    Then the active model is "m25"
    And the terminal prompt shows "[m25]"

  Scenario: Context depth escalates to deep tier
    Given the active model is "m25"
    And the context window contains 35000 tokens
    When the next turn begins
    Then the active model is "glm5"
    And a checkpoint is created before the switch
    And the terminal shows "⟳ switched to glm5, loaded from checkpoint 2"

  Scenario: Tool depth escalates to deep tier
    Given the active model is "m25"
    And the current chain has 6 sequential tool calls without user input
    When the next turn begins
    Then the active model is "glm5"

  Scenario: Explicit override to deep tier
    Given the active model is "m25"
    When the user types "/deep"
    Then the active model is "glm5"
    And the terminal prompt shows "[glm5]"

  Scenario: Explicit model flag overrides routing
    When ghyll starts with --model glm5
    Then the active model is "glm5" for the entire session
    And the dialect router does not change models

  Scenario: De-escalation after context compaction
    Given the active model is "glm5"
    And context was escalated due to depth
    When compaction reduces context to 15000 tokens
    And the user has not set an explicit override
    Then the active model is "m25"
    And the terminal shows "[glm5→m25]"

  Scenario: Drift backfill triggers escalation
    Given the active model is "m25"
    And drift detection triggers backfill
    Then the active model escalates to "glm5"
    And the backfill context is formatted for the glm5 dialect
