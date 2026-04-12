Feature: Drift detection and memory backfill
  The context manager monitors semantic drift from the original task
  and injects relevant checkpoint summaries when drift exceeds threshold.

  Scenario: No drift detected
    Given a session working on "fix the auth module race condition"
    And 8 turns have completed, all related to auth module
    When drift is measured at turn 8
    Then cosine similarity to checkpoint 0 is 0.85
    And no backfill is triggered

  Scenario: Drift detected and backfill triggered
    Given a session that started on "fix auth module race condition"
    And the conversation has drifted to discussing CSS styling
    When drift is measured
    Then cosine similarity to checkpoint 0 is 0.45
    And this is below the threshold of 0.7
    And the top-2 most relevant checkpoints are retrieved
    And their summaries are injected as system context
    And the terminal displays "ℹ drift detected, backfilling from checkpoints 1, 3"

  Scenario: Backfill from team memory
    Given developer alice created checkpoint "auth module has race condition in session.refresh()"
    And developer bob is working on the same repo
    And bob's session drifts into auth-related territory
    When drift triggers backfill
    And local checkpoints are insufficient (similarity < 0.5)
    Then team checkpoints from ghyll/memory branch are searched
    And alice's checkpoint is retrieved (similarity 0.82)
    And the terminal displays "ℹ backfill from @alice checkpoint 5: auth module session refresh race condition"
    And alice's checkpoint hash chain is verified before use

  Scenario: Backfill respects token budget
    Given the context window has 28000 tokens out of 32000 limit
    And backfill would add 3 checkpoint summaries totaling 5000 tokens
    When backfill is requested
    Then compaction runs first to make room
    Then only the top-2 summaries (3200 tokens) are injected
    And the third is skipped with a log message

  Scenario: Embedding model not available
    Given the ONNX embedding model has not been downloaded
    When drift measurement is attempted
    Then drift detection is skipped with warning "ℹ embedding model not available, drift detection disabled"
    And the session continues normally without drift protection

  Scenario: Drift check frequency
    Given drift_check_interval is set to 5 turns
    Then drift is measured at turns 5, 10, 15, etc.
    And drift is also measured when compaction is triggered
    And drift is also measured when model switch occurs
