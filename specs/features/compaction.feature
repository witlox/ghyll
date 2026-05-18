Feature: Context compaction
  The context manager reduces context size when approaching the model's
  token limit. Compaction is a separate API call containing only the
  turns to summarize and the dialect's compaction prompt — not the full
  context window. Preserves recent turns and key decisions.

  Background:
    Given the active model is "m25" with max_context 1000000
    And compaction preserves the last 3 turns

  Scenario: Proactive compaction before turn
    Given the context window contains 920000 tokens (92% of max)
    When the next turn is about to begin
    Then compaction triggers before sending the request
    And a separate API call is made with only the turns to summarize
    And the compaction prompt is the active dialect's CompactionPrompt()
    And the summary replaces the original turns in context
    And the context window is now below 90% of max

  Scenario: Reactive compaction on context-too-long rejection
    Given the context window contains 960000 tokens
    And the proactive check estimated 940000 tokens (below 95% threshold)
    When the model rejects with context_length_exceeded
    Then compaction triggers immediately
    And the request is retried once with the compacted context

  Scenario: Compaction preserves recent turns
    Given the context has 50 turns
    When compaction triggers
    Then turns 1-47 are sent to the model as a separate compaction call
    And the model returns a summary
    And the summary replaces turns 1-47 in context
    And turns 48-50 remain unchanged in context

  Scenario: Compaction call is separate from main context
    Given the context window is at 92% of max
    When compaction triggers
    Then a new API request is created containing only:
      | content                           |
      | dialect CompactionPrompt          |
      | turns 1 through N-3              |
    And this request is sent to the same model endpoint
    And the response is a summary, not a tool-calling turn
    And the main context window is not sent in this request

  Scenario: Compaction summary uses dialect prompt
    Given the active model is "glm5"
    When compaction triggers
    Then the compaction call uses glm5's CompactionPrompt()
    And the summary accounts for GLM-5's DSA attention characteristics

  Scenario: Compaction before routing escalation
    Given the active model is "m25"
    And the context window exceeds the routing escalation threshold (32K tokens)
    When the dialect router decides to escalate to "glm5"
    Then compaction runs first on the m25 endpoint
    And the compaction checkpoint is created
    Then the handoff to glm5 occurs with the compacted context
    And the handoff summary is formatted for the glm5 dialect

  Scenario: Compaction triggers drift check
    Given drift_check_interval is 5 turns
    And the last drift check was at turn 8
    When compaction triggers at turn 10
    Then drift is measured after compaction completes
    And the drift check counter resets

  Scenario: Compaction creates checkpoint
    Given compaction triggers
    When the compaction summary is generated
    Then a checkpoint is created capturing the pre-compaction state
    And the checkpoint summary includes "compaction at turn N"

  Scenario: Repeated compaction within session
    Given compaction has already run once at turn 20
    And the session continues to turn 45
    And the context again reaches 92% of max
    When compaction triggers again
    Then the previous compaction summary is itself included in the new compaction call
    And the last 3 turns are still preserved unchanged
