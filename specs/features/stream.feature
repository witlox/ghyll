Feature: Streaming LLM client
  The stream client sends messages to SGLang endpoints via OpenAI-compatible
  SSE and assembles responses including tool calls.

  Background:
    Given ghyll is configured with endpoint "https://inference.internal:8001/v1"
    And the active dialect is "minimax_m25"

  Scenario: Successful streaming response
    Given the context window contains a user prompt
    When the stream client sends a request
    Then it receives SSE events with delta content
    And content deltas are rendered to the terminal in real time
    And the complete response is assembled and returned to the context manager

  Scenario: Response with tool calls
    Given the model responds with a tool call for "bash" with command "ls src/"
    When the stream client finishes receiving
    Then it returns a structured tool call with name="bash" and arguments
    And the tool call is passed to tool/ for execution
    And the tool result is added to context as a tool response message

  Scenario: Multiple tool calls in one response
    Given the model responds with 3 tool calls in sequence
    When the stream client finishes receiving
    Then all 3 tool calls are returned in order
    And each is executed and its result added to context

  Scenario: Partial response on stream cut
    Given the stream is delivering a response
    And 200 tokens have been received
    When the connection drops mid-stream
    Then the partial content is preserved
    And the terminal shows "⚠ stream interrupted after 200 tokens"
    And the partial response is surfaced to the user
    And the user can retry with the same context

  Scenario: Retry with exponential backoff on 5xx
    Given the endpoint returns HTTP 503
    When the stream client retries
    Then it waits 1s, then 2s, then 4s between attempts
    And after 3 failed attempts it triggers tier fallback

  Scenario: Rate limit handling
    Given the endpoint returns HTTP 429 with Retry-After: 5
    When the stream client receives the response
    Then it waits 5 seconds before retrying
    And the terminal shows "ℹ rate limited, retrying in 5s"

  Scenario: Tier fallback on persistent failure (auto-routing)
    Given auto-routing is active (no --model flag)
    And the active model is "m25"
    And the m25 endpoint has failed 3 consecutive requests
    When tier fallback triggers
    Then the request is sent to the "glm5" endpoint instead
    And the terminal shows "⚠ m25 unreachable, falling back to glm5"
    And the context is reformatted for the glm5 dialect

  Scenario: Tier fallback in reverse (auto-routing)
    Given auto-routing is active (no --model flag)
    And the active model is "glm5"
    And the glm5 endpoint has failed 3 consecutive requests
    When tier fallback triggers
    Then the request is sent to the "m25" endpoint instead
    And the terminal shows "⚠ glm5 unreachable, falling back to m25"

  Scenario: No fallback with explicit model lock
    Given ghyll was started with --model m25
    And the m25 endpoint has failed 3 consecutive requests
    When retry is exhausted
    Then the terminal shows "✗ m25 unreachable (model locked via --model flag)"
    And the session stays open for manual retry
    And no tier fallback occurs

  Scenario: Both tiers unreachable (auto-routing)
    Given auto-routing is active (no --model flag)
    And both m25 and glm5 endpoints have failed 3 consecutive requests each
    When the user submits a prompt
    Then the terminal shows "✗ all model endpoints unreachable"
    And the session stays open for manual retry
    And no checkpoint is created for the failed turn

  Scenario: Malformed SSE frame
    Given the endpoint returns an SSE frame with invalid JSON in the data field
    When the stream client parses the frame
    Then the malformed frame is skipped
    And parsing continues with subsequent frames
    And a warning is logged
