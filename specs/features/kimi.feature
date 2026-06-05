Feature: Kimi 2.5/2.6 dialect
  Operators run ghyll against the CSCS-hosted Kimi-K2.6 endpoint
  with the moonshotai/Kimi-K2.6 literal model id, and the wire
  contract (reasoning_content round-trip, OpenAI tool_calls with
  the functions.<name>:<idx> ID shape) is preserved across turns.

  Background:
    Given a Kimi mock SSE endpoint is configured
    And the session uses dialect "kimi" and model id "moonshotai/Kimi-K2.6"

  Scenario: Multi-turn round-trip preserves reasoning_content and the tool-call ID contract
    When the model emits an assistant turn with reasoning_content "I should call bash with ls" and tool_call id "functions.bash:0" calling "bash"
    And the session executes the bash tool and the mock returns a follow-up assistant turn with content "tests/ exists"
    Then the captured request body for the SECOND model call contains an assistant message with field "reasoning_content" equal to "I should call bash with ls"
    And the captured request body for the SECOND model call contains an assistant message with a tool_calls entry whose id equals "functions.bash:0"
    And the parsed assistant Message in the session context has ReasoningContent "I should call bash with ls"
    And the parsed ToolCall has ID "functions.bash:0" and Function.Name "bash"

  Scenario: Non-conformant tool-call ID surfaces ErrParseToolCall not a silent fallthrough
    When the parser receives an assistant tool_call with id "550e8400-e29b-41d4-a716-446655440000"
    Then KimiParseToolCalls returns a wrapped ErrParseToolCall sentinel
    And the operator-facing diagnostic names the offending id shape

  Scenario: SSE parser reads inbound reasoning_content and surfaces it on the parsed Response
    When the mock emits an assistant turn with reasoning_content "I should call bash with ls" and content "calling bash now"
    Then the parsed stream.Response carries ReasoningContent equal to "I should call bash with ls"
    And the parsed stream.Response carries Content equal to "calling bash now"

  Scenario: A Kimi config pasted from operator-guide.md docs loads successfully
    Given a config.toml carries the canonical Kimi block with dialect "kimi" and model "moonshotai/Kimi-K2.6"
    When config.Load is invoked on the file
    Then no validation error is returned
    And the loaded model's wire model id equals "moonshotai/Kimi-K2.6"
