Feature: Key management
  Ed25519 key pairs for checkpoint signing and verification.
  Keys are generated locally, public keys distributed via the memory branch.

  Scenario: First-run key generation
    Given no key pair exists at ~/.ghyll/keys/
    When ghyll starts a session for the first time
    Then an ed25519 key pair is generated
    And the private key is written to ~/.ghyll/keys/<device-id>.key with mode 0600
    And the public key is written to ~/.ghyll/keys/<device-id>.pub
    And the terminal shows "ℹ generated signing key for device <device-id>"

  Scenario: Public key pushed to memory branch
    Given a key pair exists locally
    And the ghyll/memory branch exists
    When ghyll starts a session
    And the device's public key is not yet on the memory branch
    Then the public key is written to devices/<device-id>.pub on ghyll/memory
    And it is committed and pushed in the next sync cycle

  Scenario: Remote public keys fetched during sync
    Given developer alice has pushed her public key to ghyll/memory
    And developer bob runs ghyll on the same repo
    When bob's session starts and syncs
    Then alice's public key is fetched from devices/alice.pub
    And it is available for verifying alice's checkpoints

  Scenario: Checkpoint signed with device key
    Given a key pair exists at ~/.ghyll/keys/
    When a checkpoint is created
    Then the checkpoint hash is signed with the device's private key
    And the signature is stored in the checkpoint's sig field
    And the checkpoint's device field matches the key's device-id

  Scenario: Key exists but has wrong permissions
    Given a private key exists at ~/.ghyll/keys/<device-id>.key with mode 0644
    When ghyll starts
    Then ghyll exits with error "private key has insecure permissions (0644), expected 0600"

  Scenario: Verification with known public key
    Given a checkpoint from device "alice-laptop"
    And devices/alice-laptop.pub exists on the memory branch
    When signature verification runs
    Then ed25519.Verify succeeds
    And the checkpoint is trusted for backfill

  Scenario: Verification with unknown public key
    Given a checkpoint from device "unknown-device"
    And no public key exists for "unknown-device"
    When signature verification runs
    Then the checkpoint is marked unverified
    And it is not used for backfill
    And the terminal shows "⚠ unverified checkpoint from unknown-device (no public key)"

  Scenario: Device ID derivation
    Given a machine with hostname "alice-laptop"
    When the device ID is computed
    Then it is derived from hostname + a stable machine identifier
    And it is stable across sessions on the same machine
