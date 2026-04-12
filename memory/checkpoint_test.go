package memory

import (
	"crypto/ed25519"
	"testing"
)

// TestScenario_Memory_CanonicalHash maps to:
// Scenario: Hash chain integrity (hash determinism, invariant 4)
func TestScenario_Memory_CanonicalHash(t *testing.T) {
	cp := Checkpoint{
		Version:    1,
		ParentHash: "0000000000000000000000000000000000000000000000000000000000000000",
		DeviceID:   "dev1",
		AuthorID:   "alice",
		Timestamp:  1700000000000000000,
		RepoRemote: "https://github.com/example/repo",
		Branch:     "main",
		SessionID:  "sess-1",
		Turn:       5,
		ActiveModel: "m25",
		Summary:    "Working on auth module",
		Embedding:  []float32{0.1, 0.2, 0.3},
		FilesTouched: []string{"auth.go"},
		ToolsUsed:  []string{"bash", "read_file"},
	}

	hash1 := CanonicalHash(&cp)
	hash2 := CanonicalHash(&cp)

	if hash1 != hash2 {
		t.Errorf("hash not deterministic: %s != %s", hash1, hash2)
	}
	if len(hash1) != 64 { // hex-encoded sha256
		t.Errorf("hash length = %d, want 64", len(hash1))
	}
}

// TestScenario_Memory_HashChangesOnModification
// Scenario: Tampered checkpoint detected
func TestScenario_Memory_HashChangesOnModification(t *testing.T) {
	cp := Checkpoint{
		Version:    1,
		ParentHash: "0000000000000000000000000000000000000000000000000000000000000000",
		DeviceID:   "dev1",
		AuthorID:   "alice",
		Timestamp:  1700000000000000000,
		SessionID:  "sess-1",
		Turn:       1,
		ActiveModel: "m25",
		Summary:    "original",
	}

	hash1 := CanonicalHash(&cp)
	cp.Summary = "tampered"
	hash2 := CanonicalHash(&cp)

	if hash1 == hash2 {
		t.Error("hash should change when content changes")
	}
}

// TestScenario_Memory_SignAndVerify maps to:
// Scenario: Signature verification
func TestScenario_Memory_SignAndVerify(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	cp := &Checkpoint{
		Version:    1,
		ParentHash: "0000000000000000000000000000000000000000000000000000000000000000",
		DeviceID:   "dev1",
		AuthorID:   "alice",
		Timestamp:  1700000000000000000,
		SessionID:  "sess-1",
		Turn:       1,
		ActiveModel: "m25",
		Summary:    "test checkpoint",
	}

	SignCheckpoint(cp, priv)

	if cp.Hash == "" {
		t.Fatal("hash not set after signing")
	}
	if cp.Signature == "" {
		t.Fatal("signature not set after signing")
	}

	result := VerifyCheckpoint(cp, pub)
	if !result.Valid {
		t.Errorf("verification failed: %s", result.Reason)
	}
}

// TestScenario_Memory_VerifyFailsTampered
// Scenario: Tampered checkpoint detected
func TestScenario_Memory_VerifyFailsTampered(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)

	cp := &Checkpoint{
		Version:    1,
		ParentHash: "0000000000000000000000000000000000000000000000000000000000000000",
		DeviceID:   "dev1",
		AuthorID:   "alice",
		Timestamp:  1700000000000000000,
		SessionID:  "sess-1",
		Turn:       1,
		ActiveModel: "m25",
		Summary:    "original",
	}

	SignCheckpoint(cp, priv)

	// Tamper after signing
	cp.Summary = "tampered"

	result := VerifyCheckpoint(cp, pub)
	if result.Valid {
		t.Error("expected verification to fail for tampered checkpoint")
	}
	if result.Reason != "hash_mismatch" {
		t.Errorf("reason = %q, want %q", result.Reason, "hash_mismatch")
	}
}

// TestScenario_Memory_VerifyFailsWrongKey
func TestScenario_Memory_VerifyFailsWrongKey(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	otherPub, _, _ := ed25519.GenerateKey(nil)

	cp := &Checkpoint{
		Version:    1,
		ParentHash: "0000000000000000000000000000000000000000000000000000000000000000",
		DeviceID:   "dev1",
		AuthorID:   "alice",
		Timestamp:  1700000000000000000,
		SessionID:  "sess-1",
		Turn:       1,
		ActiveModel: "m25",
		Summary:    "test",
	}

	SignCheckpoint(cp, priv)

	result := VerifyCheckpoint(cp, otherPub)
	if result.Valid {
		t.Error("expected verification to fail with wrong key")
	}
	if result.Reason != "bad_signature" {
		t.Errorf("reason = %q, want %q", result.Reason, "bad_signature")
	}
}

// TestScenario_Memory_ChainVerification maps to:
// Scenario: Hash chain integrity
func TestScenario_Memory_ChainVerification(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	zeroHash := "0000000000000000000000000000000000000000000000000000000000000000"

	c0 := &Checkpoint{Version: 1, ParentHash: zeroHash, DeviceID: "dev1", AuthorID: "alice",
		Timestamp: 1, SessionID: "s1", Turn: 1, ActiveModel: "m25", Summary: "first"}
	SignCheckpoint(c0, priv)

	c1 := &Checkpoint{Version: 1, ParentHash: c0.Hash, DeviceID: "dev1", AuthorID: "alice",
		Timestamp: 2, SessionID: "s1", Turn: 2, ActiveModel: "m25", Summary: "second"}
	SignCheckpoint(c1, priv)

	c2 := &Checkpoint{Version: 1, ParentHash: c1.Hash, DeviceID: "dev1", AuthorID: "alice",
		Timestamp: 3, SessionID: "s1", Turn: 3, ActiveModel: "m25", Summary: "third"}
	SignCheckpoint(c2, priv)

	result := VerifyChain([]Checkpoint{*c0, *c1, *c2})
	if !result.Valid {
		t.Errorf("chain verification failed: %s at %s", result.Reason, result.FailedAt)
	}
}

// TestScenario_Memory_BrokenChain
func TestScenario_Memory_BrokenChain(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	zeroHash := "0000000000000000000000000000000000000000000000000000000000000000"

	c0 := &Checkpoint{Version: 1, ParentHash: zeroHash, DeviceID: "dev1", AuthorID: "alice",
		Timestamp: 1, SessionID: "s1", Turn: 1, ActiveModel: "m25", Summary: "first"}
	SignCheckpoint(c0, priv)

	// c1 with wrong parent hash
	c1 := &Checkpoint{Version: 1, ParentHash: "aaaa" + zeroHash[4:], DeviceID: "dev1", AuthorID: "alice",
		Timestamp: 2, SessionID: "s1", Turn: 2, ActiveModel: "m25", Summary: "second"}
	SignCheckpoint(c1, priv)

	result := VerifyChain([]Checkpoint{*c0, *c1})
	if result.Valid {
		t.Error("expected broken chain detection")
	}
	if result.Reason != "broken_chain" {
		t.Errorf("reason = %q, want %q", result.Reason, "broken_chain")
	}
}
