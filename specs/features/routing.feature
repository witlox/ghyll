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

  Scenario: /deep temporary override
    Given the active model is "m25"
    When the user types "/deep"
    Then the active model is "glm5"
    And the terminal prompt shows "[glm5]"
    And auto-routing continues to evaluate

  Scenario: /deep reverts when conditions clear
    Given the user typed "/deep" and the model switched to "glm5"
    And the context window is at 40000 tokens
    When compaction reduces context to 15000 tokens
    Then auto-routing determines M2.5 is sufficient
    And the active model reverts to "m25"
    And the terminal shows "[glm5→m25]"

  Scenario: /deep ignored when --model flag is set
    Given ghyll was started with --model m25
    When the user types "/deep"
    Then the active model remains "m25"
    And the terminal shows "ℹ /deep ignored, model locked via --model flag"

  Scenario: Explicit model flag overrides routing
    When ghyll starts with --model glm5
    Then the active model is "glm5" for the entire session
    And the dialect router does not change models
    And tier fallback does not apply

  Scenario: De-escalation after context compaction
    Given the active model is "glm5"
    And context was auto-escalated due to depth
    When compaction reduces context to 15000 tokens
    Then the active model is "m25"
    And the terminal shows "[glm5→m25]"

  Scenario: Drift backfill triggers escalation
    Given the active model is "m25"
    And drift detection triggers backfill
    Then the active model escalates to "glm5"
    And the backfill context is formatted for the glm5 dialect
