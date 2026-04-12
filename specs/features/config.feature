Feature: Configuration
  TOML-based configuration with sensible defaults and validation.

  Scenario: Load valid config
    Given a config file at ~/.ghyll/config.toml with valid model endpoints
    When ghyll starts
    Then the config is loaded with all specified values
    And model endpoints are resolved

  Scenario: Default values applied
    Given a minimal config with only model endpoints defined
    When ghyll starts
    Then routing.default_model defaults to "m25"
    And routing.context_depth_threshold defaults to 32000
    And routing.tool_depth_threshold defaults to 5
    And memory.checkpoint_interval_turns defaults to 5
    And memory.drift_threshold defaults to 0.7
    And tools.bash_timeout_seconds defaults to 30

  Scenario: Config file missing
    Given no config file exists at ~/.ghyll/config.toml
    When ghyll starts
    Then ghyll exits with error "no config found at ~/.ghyll/config.toml"
    And the error message includes a link to example config

  Scenario: Malformed TOML
    Given a config file with invalid TOML syntax
    When ghyll starts
    Then ghyll exits with error showing the TOML parse error
    And the line number of the syntax error is included

  Scenario: Missing required model endpoint
    Given a config with routing.default_model = "m25"
    And no [models.m25] section defined
    When ghyll starts
    Then ghyll exits with error "default model 'm25' has no endpoint configured"

  Scenario: Model override via flag
    Given a config with default_model = "m25"
    When ghyll starts with --model glm5
    Then the active model is "glm5"
    And routing is disabled for the session

  Scenario: Vault config optional
    Given a config with no [vault] section
    When ghyll starts
    Then vault features are disabled
    And team memory search falls back to local git sync only

  Scenario: Vault config with token
    Given a config with vault.url = "https://vault.internal:9090"
    And vault.token = "team-secret"
    When the vault client initializes
    Then requests to vault include Authorization: Bearer team-secret

  Scenario: Vault on localhost skips auth
    Given a config with vault.url = "http://localhost:9090"
    And no vault.token configured
    When the vault client initializes
    Then requests to vault include no Authorization header
