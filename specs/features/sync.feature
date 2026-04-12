Feature: Git-based memory sync
  Memory checkpoints sync via a git orphan branch in the project repo.
  No custom network protocol. No vault required for basic team use.

  Scenario: Initialize memory branch
    Given a project repo at ~/repos/myproject with remote "origin"
    And the ghyll/memory branch does not exist
    When ghyll starts a session
    Then an orphan branch "ghyll/memory" is created locally
    And an initial empty commit is made
    And the branch is pushed to origin

  Scenario: Checkpoint triggers sync
    Given auto_sync is enabled
    And a checkpoint is created
    Then the checkpoint JSON is written to ghyll/memory branch worktree
    And git add + commit runs in background
    And git push runs in background (non-blocking)
    And push failure is logged but does not interrupt the session

  Scenario: Pull on session start
    Given ghyll/memory branch exists on origin with remote checkpoints
    When ghyll starts a new session
    Then git fetch origin ghyll/memory runs
    And new remote checkpoints are imported into local sqlite
    And hash chains are verified for imported checkpoints
    And the terminal shows "ℹ synced 12 checkpoints from 3 developers"

  Scenario: Concurrent push conflict
    Given developer alice and bob both push to ghyll/memory simultaneously
    And alice's push succeeds first
    When bob's push is rejected
    Then bob's ghyll pulls (fast-forward, append-only means no conflicts)
    And bob retries push
    And the retry succeeds

  Scenario: Orphan branch isolation
    Given ghyll/memory branch exists
    When a developer runs "git log main"
    Then no ghyll/memory commits appear
    When a developer runs "git branch"
    Then ghyll/memory is listed but is clearly separate
    And "git merge ghyll/memory" from main would fail (no common ancestor)

  Scenario: Offline operation
    Given the git remote is unreachable
    When checkpoints are created during the session
    Then checkpoints are stored locally in sqlite
    And checkpoint files accumulate in the local ghyll/memory worktree
    And when connectivity returns, the next sync pushes all pending checkpoints

  Scenario: Large repo clone optimization
    Given ghyll/memory has 10000 checkpoint files over 6 months
    When a new developer clones the project
    Then ghyll performs a shallow fetch of ghyll/memory (depth=1)
    And only the latest checkpoint chain is fully available
    And older checkpoints can be fetched on demand via "ghyll memory fetch --full"
