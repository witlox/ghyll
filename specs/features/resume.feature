Feature: Session resume
  A session can be started with continuity from the
  previous session's final checkpoint summary.

  Background:
    Given a workspace directory "/tmp/ghyll-test-resume"
    And the checkpoint store contains a previous session's final checkpoint:
      | field       | value                                           |
      | session_id  | dev1-1713100000000000000                         |
      | turn        | 15                                                |
      | summary     | "Refactored the stream client retry logic. Added exponential backoff. Tests passing." |
      | reason      | shutdown                                          |
      | active_model| m25                                               |
      | files_touched| ["stream/client.go", "stream/client_test.go"]   |

  Scenario: Resume loads previous checkpoint as backfill
    When I run "ghyll run /tmp/ghyll-test-resume --resume"
    Then a new session is created
    And the context contains a system message with the previous checkpoint summary
    And the system message includes "Refactored the stream client retry logic"
    And the system message includes files touched: "stream/client.go", "stream/client_test.go"
    And the session starts with model "m25"

  Scenario: Resume with no previous checkpoint starts fresh
    Given the checkpoint store has no checkpoints for "/tmp/ghyll-test-resume"
    When I run "ghyll run /tmp/ghyll-test-resume --resume"
    Then a new session is created with no backfill
    And a warning is displayed: "no previous session found, starting fresh"

  Scenario: Resume selects the most recent final checkpoint
    Given the checkpoint store contains multiple sessions:
      | session_id                     | turn | reason    | timestamp           |
      | dev1-1713000000000000000       | 8    | shutdown  | 2026-04-13T10:00:00 |
      | dev1-1713100000000000000       | 15   | shutdown  | 2026-04-14T09:00:00 |
    When I run "ghyll run /tmp/ghyll-test-resume --resume"
    Then the context is backfilled from session "dev1-1713100000000000000"

  Scenario: Resume does not restore raw message history
    When I run "ghyll run /tmp/ghyll-test-resume --resume"
    Then the context does not contain the original user prompts from the previous session
    And the context does not contain the original model responses from the previous session
    And only the structured checkpoint summary is present

  Scenario: Resume works alongside normal session start
    When I run "ghyll run /tmp/ghyll-test-resume --resume"
    And the user types "continue with the retry logic"
    Then the model receives the backfilled summary plus the new user message
    And the session proceeds normally

  Scenario: Resume creates a new session ID linked to predecessor
    When I run "ghyll run /tmp/ghyll-test-resume --resume"
    Then the new session ID is different from "dev1-1713100000000000000"
    And the new session's first checkpoint contains resumed_from session_id "dev1-1713100000000000000"
    And the new session's first checkpoint contains resumed_from checkpoint hash matching the source's final checkpoint

  Scenario: Resume restores plan mode from checkpoint
    Given the previous session's final checkpoint has plan_mode = true
    When I run "ghyll run /tmp/ghyll-test-resume --resume"
    Then plan mode is active in the new session

  Scenario: Resume filters by current repo
    Given the checkpoint store contains sessions for different repos:
      | session_id                  | repo_remote                        | reason   |
      | dev1-1713000000000000000    | https://github.com/other/repo.git  | shutdown |
      | dev1-1713100000000000000    | https://github.com/witlox/ghyll.git| shutdown |
    And the current repo remote is "https://github.com/witlox/ghyll.git"
    When I run "ghyll run /tmp/ghyll-test-resume --resume"
    Then the context is backfilled from session "dev1-1713100000000000000"

  Scenario: Resume skips non-shutdown checkpoints
    Given the checkpoint store contains:
      | session_id                  | turn | reason     | timestamp           |
      | dev1-1713100000000000000    | 10   | compaction | 2026-04-14T09:00:00 |
      | dev1-1713100000000000000    | 12   | handoff    | 2026-04-14T09:05:00 |
      | dev1-1713100000000000000    | 15   | shutdown   | 2026-04-14T09:10:00 |
    When I run "ghyll run /tmp/ghyll-test-resume --resume"
    Then the context is backfilled from turn 15 (shutdown) not turn 12 (handoff)

  Scenario: Resume without --resume flag ignores previous sessions
    When I run "ghyll run /tmp/ghyll-test-resume"
    Then no previous checkpoint summary is loaded
    And the session starts fresh
