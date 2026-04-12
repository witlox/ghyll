package memory

import (
	"os"
	"path/filepath"
	"testing"
)

// TestScenario_Keys_FirstRunGeneration maps to:
// Scenario: First-run key generation
func TestScenario_Keys_FirstRunGeneration(t *testing.T) {
	dir := t.TempDir()
	keysDir := filepath.Join(dir, "keys")

	key, err := LoadOrGenerateKey(keysDir, "test-device")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key.DeviceID != "test-device" {
		t.Errorf("device_id = %q", key.DeviceID)
	}
	if key.PrivateKey == nil {
		t.Fatal("private key is nil")
	}
	if key.PublicKey == nil {
		t.Fatal("public key is nil")
	}

	// Verify files created
	privPath := filepath.Join(keysDir, "test-device.key")
	pubPath := filepath.Join(keysDir, "test-device.pub")

	info, err := os.Stat(privPath)
	if err != nil {
		t.Fatalf("private key file not created: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("private key mode = %o, want 0600", info.Mode().Perm())
	}
	if _, err := os.Stat(pubPath); err != nil {
		t.Fatalf("public key file not created: %v", err)
	}
}

// TestScenario_Keys_LoadExisting
// Scenario: Keys persist across sessions
func TestScenario_Keys_LoadExisting(t *testing.T) {
	dir := t.TempDir()
	keysDir := filepath.Join(dir, "keys")

	key1, err := LoadOrGenerateKey(keysDir, "test-device")
	if err != nil {
		t.Fatal(err)
	}

	key2, err := LoadOrGenerateKey(keysDir, "test-device")
	if err != nil {
		t.Fatal(err)
	}

	if !key1.PublicKey.Equal(key2.PublicKey) {
		t.Error("loaded key doesn't match generated key")
	}
}

// TestScenario_Keys_WrongPermissions maps to:
// Scenario: Key exists but has wrong permissions
func TestScenario_Keys_WrongPermissions(t *testing.T) {
	dir := t.TempDir()
	keysDir := filepath.Join(dir, "keys")

	// Generate first
	_, err := LoadOrGenerateKey(keysDir, "test-device")
	if err != nil {
		t.Fatal(err)
	}

	// Loosen permissions
	privPath := filepath.Join(keysDir, "test-device.key")
	if err := os.Chmod(privPath, 0644); err != nil {
		t.Fatal(err)
	}

	// Should fail on reload
	_, err = LoadOrGenerateKey(keysDir, "test-device")
	if err == nil {
		t.Fatal("expected error for insecure permissions")
	}
}

// TestScenario_Keys_PublicKeyExport
// Verify public key can be exported for the memory branch
func TestScenario_Keys_PublicKeyExport(t *testing.T) {
	dir := t.TempDir()
	keysDir := filepath.Join(dir, "keys")

	key, err := LoadOrGenerateKey(keysDir, "test-device")
	if err != nil {
		t.Fatal(err)
	}

	pubBytes, err := MarshalPublicKey(key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := UnmarshalPublicKey(pubBytes)
	if err != nil {
		t.Fatal(err)
	}

	if !key.PublicKey.Equal(loaded) {
		t.Error("round-tripped public key doesn't match")
	}
}
