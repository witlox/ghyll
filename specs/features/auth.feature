Feature: Endpoint Bearer-token authentication

  Ghyll forwards a Bearer token to every model endpoint when an
  api_key is configured, and never surfaces the token in operator-
  visible outputs.

  Scenario: TOML api_key reaches the wire on every chat completion
    Given a recording HTTP server that captures inbound Authorization headers
    And ghyll config defines model "cscs-glm5" with api_key "sk-test-fixture-9f2a"
    When the dispatcher sends one streaming request to "cscs-glm5"
    Then the captured Authorization header equals "Bearer sk-test-fixture-9f2a"
    And the captured Content-Type header equals "application/json"
    And the captured Accept header equals "text/event-stream"

  Scenario: env-scoped key beats env-global which beats TOML
    Given a recording HTTP server that captures inbound Authorization headers
    And ghyll config defines model "cscs-glm5" with api_key "sk-toml-aaaa"
    And env "GHYLL_API_KEY" is "sk-global-bbbb"
    And env "GHYLL_API_KEY_CSCS_GLM5" is "sk-scoped-cccc"
    When the dispatcher sends one streaming request to "cscs-glm5"
    Then the captured Authorization header equals "Bearer sk-scoped-cccc"

  Scenario: the api_key never appears in operator-visible output
    Given a recording HTTP server that returns HTTP 401 with the configured api_key echoed in the body
    And ghyll config defines model "cscs-glm5" with api_key "sk-canary-cccc-must-not-leak"
    When the dispatcher sends one streaming request to "cscs-glm5"
    Then the surfaced StreamError message equals "authentication failed"
    And the surfaced error string does not contain "sk-canary-cccc-must-not-leak"
    And the redacted provenance for model "cscs-glm5" equals "<toml>"

  # AUTH-10 / AUTH-1 — operator typed a known misspelling of api_key
  # under [models.*]. The config loader rejects it with a directed
  # hint instead of silently dropping the value.
  Scenario: misspelled api_token TOML key surfaces a directed validation error
    Given a config file containing the misspelled key "api_token" under [models.cscs-glm5]
    When ghyll loads the config
    Then the load fails with a validation error
    And the validation error mentions "api_key"

  # AUTH-10 / AUTH-2 — two model keys that normalize to the same
  # env-var bucket are caught at Load time.
  Scenario: two model keys normalizing to the same env var are rejected at Load
    Given a config file with models "cscs-glm5" and "cscs_glm5"
    When ghyll loads the config
    Then the load fails with a validation error
    And the validation error mentions "normalize to env var"

  # AUTH-10 / AUTH-3 — trailing whitespace in api_key is silently
  # trimmed at the resolver so the eventual Authorization header is
  # well-formed.
  Scenario: trailing whitespace in api_key is trimmed before reaching the wire
    Given a recording HTTP server that captures inbound Authorization headers
    And ghyll config defines model "cscs-glm5" with api_key "sk-trimmed-bbbb"
    And env "GHYLL_API_KEY_CSCS_GLM5" is "sk-env-with-trailing-whitespace  "
    When the dispatcher sends one streaming request to "cscs-glm5"
    Then the captured Authorization header equals "Bearer sk-env-with-trailing-whitespace"
