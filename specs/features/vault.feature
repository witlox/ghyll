Feature: Team memory vault
  Optional HTTP service for team memory search across repos.
  Bearer token auth for remote, no auth for localhost.
  Checkpoint signatures provide integrity and attribution.

  Scenario: Search team memory via vault
    Given vault is configured at "https://vault.internal:9090"
    And the vault contains checkpoints from developers alice, bob, charlie
    When ghyll searches for "race condition in auth module"
    Then the vault returns the top-k most similar checkpoints
    And results include author attribution and similarity scores
    And checkpoint signatures are verified before use

  Scenario: Vault search with bearer token
    Given vault.token = "team-secret" in config
    When the vault client sends a search request
    Then the request includes header Authorization: Bearer team-secret

  Scenario: Vault on localhost without token
    Given vault.url = "http://localhost:9090" in config
    And no vault.token is configured
    When the vault client sends a search request
    Then no Authorization header is included
    And the request succeeds

  Scenario: Vault unreachable
    Given vault is configured but the server is not responding
    When ghyll needs team memory search
    Then the vault request times out after 5 seconds
    And ghyll falls back to local git-synced checkpoints only
    And the terminal shows "ℹ vault unreachable, using local memory only"

  Scenario: Vault returns unverified checkpoint
    Given the vault returns a checkpoint from developer dave
    And dave's public key is not in devices/dave.pub
    When signature verification runs
    Then the checkpoint is marked as unverified
    And it is not used for backfill
    And the terminal shows "⚠ unverified checkpoint from @dave (unknown key)"

  Scenario: Vault returns checkpoint with broken hash chain
    Given the vault returns checkpoint c3 from alice
    And c3.parent_hash does not match any known checkpoint
    When hash chain verification runs
    Then c3 is marked as unverified
    And it is not used for backfill

  Scenario: Push checkpoint to vault
    Given vault is configured and reachable
    And auto_push is enabled in config
    When a new checkpoint is created
    Then the checkpoint is POSTed to vault at /v1/checkpoints
    And push failure is logged but does not interrupt the session

  Scenario: Vault serves search API
    Given ghyll-vault is running with a checkpoint store
    When a client POSTs to /v1/search with a query embedding and repo hash
    Then the server returns checkpoints ranked by cosine similarity
    And results are filtered to the requested repo

  Scenario: Vault accepts checkpoint push
    Given ghyll-vault is running
    When a client POSTs to /v1/checkpoints with a signed checkpoint
    Then the server verifies the checkpoint signature
    And stores the checkpoint if valid
    And rejects with 403 if signature verification fails
