Feature: Glob tool
  As a model, I can discover files by pattern matching
  without scanning file contents, for fast navigation.

  Background:
    Given a workspace directory "/tmp/ghyll-test-glob"
    And the following file structure:
      | path                          |
      | src/main.go                   |
      | src/handler.go                |
      | src/handler_test.go           |
      | internal/store/store.go       |
      | internal/store/store_test.go  |
      | docs/README.md                |
      | .ghyll/instructions.md        |

  Scenario: Match all Go files recursively
    When I call glob with pattern "**/*.go" path "/tmp/ghyll-test-glob"
    Then the result contains 5 paths
    And the result includes "src/main.go"
    And the result includes "internal/store/store_test.go"

  Scenario: Match test files only
    When I call glob with pattern "**/*_test.go" path "/tmp/ghyll-test-glob"
    Then the result contains 2 paths
    And the result includes "src/handler_test.go"
    And the result includes "internal/store/store_test.go"

  Scenario: Match in subdirectory
    When I call glob with pattern "*.go" path "/tmp/ghyll-test-glob/src"
    Then the result contains 3 paths

  Scenario: No matches returns empty list
    When I call glob with pattern "**/*.rs" path "/tmp/ghyll-test-glob"
    Then the result contains 0 paths

  Scenario: Pattern with directory wildcard
    When I call glob with pattern "internal/**/*.go" path "/tmp/ghyll-test-glob"
    Then the result contains 2 paths

  Scenario: Invalid path returns error
    When I call glob with pattern "**/*.go" path "/tmp/ghyll-test-glob/nonexistent"
    Then the tool result indicates error "directory not found"

  Scenario: Results sorted by modification time
    Given "src/handler.go" was modified more recently than "src/main.go"
    When I call glob with pattern "**/*.go" path "/tmp/ghyll-test-glob"
    Then "src/handler.go" appears before "src/main.go" in the result

  Scenario: Glob skips broken symlinks
    Given a symlink "src/broken" pointing to "/tmp/nonexistent-target"
    When I call glob with pattern "**/*" path "/tmp/ghyll-test-glob"
    Then the result does not include "src/broken"

  Scenario: Glob does not follow symlinks outside workspace
    Given a symlink "src/external" pointing to "/etc/hosts"
    When I call glob with pattern "**/*" path "/tmp/ghyll-test-glob"
    Then the result does not include "src/external"

  Scenario: Glob includes hidden files when pattern matches
    When I call glob with pattern "**/*.md" path "/tmp/ghyll-test-glob"
    Then the result includes ".ghyll/instructions.md"

  Scenario: Glob with empty pattern returns error
    When I call glob with pattern "" path "/tmp/ghyll-test-glob"
    Then the tool result indicates error "empty pattern"

  Scenario: Glob follows valid symlinks within workspace
    Given a symlink "src/alias.go" pointing to "/tmp/ghyll-test-glob/src/main.go"
    When I call glob with pattern "**/*.go" path "/tmp/ghyll-test-glob"
    Then the result includes "src/alias.go"

  Scenario: Glob respects tool timeout
    Given the tool timeout is 5 seconds
    When I call glob with pattern "**/*" path "/tmp/ghyll-test-glob"
    Then the result is returned within the timeout
