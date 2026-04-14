Feature: Web fetch and search tools
  The model can fetch web pages and search the web,
  subject to Tarn's network whitelist with retry on denial.

  Background:
    Given a running session with model "m25"

  Scenario: Successful web fetch
    Given the URL "https://pkg.go.dev/context" is reachable
    When the model calls web_fetch with url "https://pkg.go.dev/context"
    Then the tool result contains the page content as markdown
    And the result does not contain JavaScript or HTML tags

  Scenario: Web fetch with Tarn blocking — retries and fails
    Given Tarn blocks outbound HTTP to "https://example.com"
    When the model calls web_fetch with url "https://example.com/docs"
    Then the tool retries 3 times with exponential backoff
    And the tool result indicates error "domain not reachable after 3 attempts, may need Tarn approval"

  Scenario: Web fetch with Tarn blocking — approved mid-retry
    Given Tarn blocks outbound HTTP to "https://docs.example.com"
    And Tarn approves "https://docs.example.com" after the second retry
    When the model calls web_fetch with url "https://docs.example.com/api"
    Then the third retry succeeds
    And the tool result contains the page content as markdown

  Scenario: Web fetch with 404 does not retry
    Given the URL "https://pkg.go.dev/nonexistent" returns HTTP 404
    When the model calls web_fetch with url "https://pkg.go.dev/nonexistent"
    Then the tool result indicates error "HTTP 404"
    And no retries are attempted

  Scenario: Web fetch respects tool timeout
    Given the URL "https://slow.example.com" takes 60 seconds to respond
    And the tool timeout is 30 seconds
    When the model calls web_fetch with url "https://slow.example.com"
    Then the tool result indicates error "timed out"

  Scenario: Successful web search
    Given the search backend is reachable
    When the model calls web_search with query "golang context package best practices"
    Then the tool result contains structured results with title, url, and snippet fields
    And the result contains at least 1 result

  Scenario: Web search with Tarn blocking
    Given Tarn blocks outbound HTTP to the search backend
    When the model calls web_search with query "golang error handling"
    Then the tool retries 3 times with exponential backoff
    And the tool result indicates error "search backend not reachable after 3 attempts, may need Tarn approval"

  Scenario: Web fetch returns markdown not binary
    Given the URL "https://example.com/image.png" returns binary content
    When the model calls web_fetch with url "https://example.com/image.png"
    Then the tool result indicates error "binary content not supported"

  Scenario: Web fetch truncates large responses
    Given the URL "https://en.wikipedia.org/wiki/Go_(programming_language)" is reachable
    And the page content exceeds 10000 tokens
    When the model calls web_fetch with url "https://en.wikipedia.org/wiki/Go_(programming_language)"
    Then the tool result contains the first 10000 tokens of content
    And the tool result ends with "[truncated]"

  Scenario: Web fetch retries on 5xx server error
    Given the URL "https://api.example.com/docs" returns HTTP 503 twice then succeeds
    When the model calls web_fetch with url "https://api.example.com/docs"
    Then the tool retries with exponential backoff
    And the third attempt succeeds
    And the tool result contains the page content

  Scenario: Web search returns empty results
    Given the search backend is reachable
    When the model calls web_search with query "xyzzy_nonexistent_term_12345"
    Then the tool result contains 0 results
    And the result is not an error

  Scenario: Web fetch max response tokens configurable
    Given web_max_response_tokens is configured as 5000
    And the URL "https://example.com/long-page" is reachable
    And the page content exceeds 5000 tokens
    When the model calls web_fetch with url "https://example.com/long-page"
    Then the tool result contains the first 5000 tokens
    And the tool result ends with "[truncated]"

  Scenario: Web search results limited to 10
    Given the search backend is reachable
    And the query matches more than 10 results
    When the model calls web_search with query "golang"
    Then the tool result contains at most 10 results

  Scenario: Web fetch with redirect
    Given the URL "https://example.com/old" redirects to "https://example.com/new"
    And "https://example.com/new" is reachable
    When the model calls web_fetch with url "https://example.com/old"
    Then the tool result contains the content from "https://example.com/new"
