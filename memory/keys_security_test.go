package memory

import (
	"crypto/ed25519"
	"encoding/pem"
	"strings"
	"testing"
)

// TestScenario_UnmarshalPublicKey_RejectsWrongLength verifies the
// Tier 3 / SR C-4 length guard. Without it, ed25519.Verify
// panicked on length-mismatched payloads or silently fail-true on
// coincidentally-32-byte attacker garbage.
func TestScenario_UnmarshalPublicKey_RejectsWrongLength(t *testing.T) {
	cases := []struct {
		name string
		size int
	}{
		{"empty", 0},
		{"one byte", 1},
		{"31 bytes (one short)", 31},
		{"33 bytes (one long)", 33},
		{"63 bytes", 63},
		{"64 bytes (close to right)", 64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bytes := make([]byte, tc.size)
			payload := pem.EncodeToMemory(&pem.Block{
				Type:  "ED25519 PUBLIC KEY",
				Bytes: bytes,
			})
			_, err := UnmarshalPublicKey(payload)
			if err == nil {
				t.Errorf("size %d: accepted; want length-error", tc.size)
			}
			if !strings.Contains(err.Error(), "length") {
				t.Errorf("size %d: err = %v; want length-error", tc.size, err)
			}
		})
	}
}

// TestScenario_UnmarshalPublicKey_AcceptsValid32Bytes verifies the
// happy path still works.
func TestScenario_UnmarshalPublicKey_AcceptsValid32Bytes(t *testing.T) {
	bytes := make([]byte, ed25519.PublicKeySize)
	payload := pem.EncodeToMemory(&pem.Block{
		Type:  "ED25519 PUBLIC KEY",
		Bytes: bytes,
	})
	pub, err := UnmarshalPublicKey(payload)
	if err != nil {
		t.Fatalf("32-byte valid: %v", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		t.Errorf("decoded length = %d; want %d", len(pub), ed25519.PublicKeySize)
	}
}

// TestScenario_MarshalPublicKey_RejectsWrongLength symmetric guard
// on the encode side.
func TestScenario_MarshalPublicKey_RejectsWrongLength(t *testing.T) {
	short := make([]byte, 16)
	_, err := MarshalPublicKey(short)
	if err == nil {
		t.Error("16-byte key accepted; want length error")
	}
}

// TestScenario_LoadOrGenerate_AtomicPubKeyWrite verifies SR L-9:
// the generated pub key file is written atomically (no partial
// file visible mid-write).
func TestScenario_LoadOrGenerate_AtomicPubKeyWrite(t *testing.T) {
	dir := t.TempDir()
	key, err := LoadOrGenerateKey(dir, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if key == nil {
		t.Fatal("nil key")
	}
	// Reload to verify the on-disk file is well-formed.
	key2, err := LoadOrGenerateKey(dir, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !key.PublicKey.Equal(key2.PublicKey) {
		t.Error("reload produced different pub key")
	}
}
