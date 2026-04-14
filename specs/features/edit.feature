Feature: File edit tool
  As a model, I can surgically replace a string in a file
  without rewriting the entire file, saving tokens.

  Background:
    Given a workspace directory "/tmp/ghyll-test-edit"
    And a file "/tmp/ghyll-test-edit/main.go" with content:
      """
      package main

      func hello() string {
          return "hello"
      }

      func goodbye() string {
          return "goodbye"
      }
      """

  Scenario: Successful single replacement
    When I call edit_file with path "/tmp/ghyll-test-edit/main.go" old_string "return \"hello\"" new_string "return \"hi\""
    Then the tool result indicates success
    And the file "/tmp/ghyll-test-edit/main.go" contains "return \"hi\""
    And the file "/tmp/ghyll-test-edit/main.go" contains "return \"goodbye\""

  Scenario: Old string not found
    When I call edit_file with path "/tmp/ghyll-test-edit/main.go" old_string "return \"missing\"" new_string "return \"found\""
    Then the tool result indicates error "old_string not found"
    And the file "/tmp/ghyll-test-edit/main.go" is unchanged

  Scenario: Ambiguous match — old string appears multiple times
    When I call edit_file with path "/tmp/ghyll-test-edit/main.go" old_string "return" new_string "yield"
    Then the tool result indicates error "old_string matches 2 locations"
    And the file "/tmp/ghyll-test-edit/main.go" is unchanged

  Scenario: File does not exist
    When I call edit_file with path "/tmp/ghyll-test-edit/nonexistent.go" old_string "x" new_string "y"
    Then the tool result indicates error "file not found"

  Scenario: Edit preserves file permissions
    Given the file "/tmp/ghyll-test-edit/main.go" has permissions 0644
    When I call edit_file with path "/tmp/ghyll-test-edit/main.go" old_string "return \"hello\"" new_string "return \"hi\""
    Then the file "/tmp/ghyll-test-edit/main.go" has permissions 0644

  Scenario: Edit with multiline old and new strings
    When I call edit_file with path "/tmp/ghyll-test-edit/main.go" old_string "func hello() string {\n    return \"hello\"\n}" new_string "func hello(name string) string {\n    return \"hello \" + name\n}"
    Then the tool result indicates success
    And the file "/tmp/ghyll-test-edit/main.go" contains "func hello(name string) string"

  Scenario: Edit with empty new_string deletes matched text
    When I call edit_file with path "/tmp/ghyll-test-edit/main.go" old_string "func goodbye() string {\n    return \"goodbye\"\n}\n" new_string ""
    Then the tool result indicates success
    And the file "/tmp/ghyll-test-edit/main.go" does not contain "func goodbye"
    And the file "/tmp/ghyll-test-edit/main.go" contains "func hello"

  Scenario: Edit fails if file modified during operation
    Given a file "/tmp/ghyll-test-edit/main.go" exists
    And another process modifies "/tmp/ghyll-test-edit/main.go" between read and write
    When I call edit_file with path "/tmp/ghyll-test-edit/main.go" old_string "return \"hello\"" new_string "return \"hi\""
    Then the tool result indicates error "file modified during edit"
    And the file "/tmp/ghyll-test-edit/main.go" retains the other process's changes

  Scenario: Edit respects tool timeout
    Given the tool timeout is 5 seconds
    And the file system is slow (simulated)
    When I call edit_file with path "/tmp/ghyll-test-edit/main.go" old_string "return \"hello\"" new_string "return \"hi\""
    Then the tool result indicates error "timed out"
    And the file "/tmp/ghyll-test-edit/main.go" is unchanged
