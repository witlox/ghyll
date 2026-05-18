Feature: Tool execution
  Tools are direct OS operations. No permission layer.
  Tarn handles sandboxing externally.

  Scenario: Bash command execution
    Given the model requests tool call bash with command "ls -la src/"
    When the tool executes
    Then exec.Command("bash", "-c", "ls -la src/") runs directly
    And stdout and stderr are captured
    And the result is returned to the model as tool output

  Scenario: Bash command timeout
    Given the model requests tool call bash with command "sleep 60"
    And the bash timeout is 30 seconds
    When 30 seconds elapse
    Then the process is killed
    And the tool returns error "command timed out after 30s"

  Scenario: File read
    Given the model requests tool call read_file with path "src/main.go"
    When the tool executes
    Then os.ReadFile("src/main.go") is called directly
    And the file contents are returned to the model

  Scenario: File write
    Given the model requests tool call write_file with path "src/util.go" and content
    When the tool executes
    Then os.WriteFile("src/util.go", content, 0644) is called directly
    And confirmation is returned to the model

  Scenario: Git operation
    Given the model requests tool call git with args "diff HEAD~1"
    When the tool executes
    Then exec.Command("git", "diff", "HEAD~1") runs in the workspace directory
    And stdout is returned to the model

  Scenario: Grep with ripgrep
    Given the model requests tool call grep with pattern "TODO" and path "src/"
    And ripgrep is available in PATH
    When the tool executes
    Then exec.Command("rg", "TODO", "src/") runs
    And matches are returned to the model

  Scenario: Grep fallback to standard grep
    Given ripgrep is not available in PATH
    When the model requests tool call grep
    Then exec.Command("grep", "-rn", pattern, path) is used as fallback
