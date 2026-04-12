package memory

import (
	"crypto/ed25519"
	"path/filepath"
	"testing"
)

// TestScenario_Memory_StoreAppend maps to:
// Scenario: Checkpoint creation — store append
func TestScenario_Memory_StoreAppend(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")

	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	_, priv, _ := ed25519.GenerateKey(nil)
	cp := &Checkpoint{
		Version:     1,
		ParentHash:  "0000000000000000000000000000000000000000000000000000000000000000",
		DeviceID:    "dev1",
		AuthorID:    "alice",
		Timestamp:   1700000000,
		RepoRemote:  "https://github.com/example/repo",
		SessionID:   "sess-1",
		Turn:        1,
		ActiveModel: "m25",
		Summary:     "test checkpoint",
		Embedding:   []float32{0.1, 0.2, 0.3},
	}
	SignCheckpoint(cp, priv)

	if err := store.Append(cp); err != nil {
		t.Fatalf("append failed: %v", err)
	}

	// Retrieve by hash
	got, err := store.GetByHash(cp.Hash)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got.Summary != "test checkpoint" {
		t.Errorf("summary = %q", got.Summary)
	}
}

// TestScenario_Memory_StoreIdempotent maps to:
// Invariant 14: sync is idempotent
func TestScenario_Memory_StoreIdempotent(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	_, priv, _ := ed25519.GenerateKey(nil)
	cp := &Checkpoint{
		Version: 1, ParentHash: "0000000000000000000000000000000000000000000000000000000000000000",
		DeviceID: "dev1", AuthorID: "alice", Timestamp: 1, SessionID: "s1",
		Turn: 1, ActiveModel: "m25", Summary: "test",
	}
	SignCheckpoint(cp, priv)

	if err := store.Append(cp); err != nil {
		t.Fatal(err)
	}
	// Second append should not error (idempotent)
	if err := store.Append(cp); err != nil {
		t.Fatalf("second append should be idempotent: %v", err)
	}
}

// TestScenario_Memory_StoreListBySession
func TestScenario_Memory_StoreListBySession(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	_, priv, _ := ed25519.GenerateKey(nil)
	zeroHash := "0000000000000000000000000000000000000000000000000000000000000000"

	c0 := &Checkpoint{Version: 1, ParentHash: zeroHash, DeviceID: "dev1", AuthorID: "alice",
		Timestamp: 1, SessionID: "sess-1", Turn: 1, ActiveModel: "m25", Summary: "first"}
	SignCheckpoint(c0, priv)

	c1 := &Checkpoint{Version: 1, ParentHash: c0.Hash, DeviceID: "dev1", AuthorID: "alice",
		Timestamp: 2, SessionID: "sess-1", Turn: 2, ActiveModel: "m25", Summary: "second"}
	SignCheckpoint(c1, priv)

	c2 := &Checkpoint{Version: 1, ParentHash: zeroHash, DeviceID: "dev1", AuthorID: "alice",
		Timestamp: 3, SessionID: "sess-2", Turn: 1, ActiveModel: "m25", Summary: "other session"}
	SignCheckpoint(c2, priv)

	for _, cp := range []*Checkpoint{c0, c1, c2} {
		if err := store.Append(cp); err != nil {
			t.Fatal(err)
		}
	}

	list, err := store.ListBySession("sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 checkpoints for sess-1, got %d", len(list))
	}
}

// TestScenario_Memory_StoreLatestBySession maps to:
// Invariant 28: drift measures against most recent checkpoint
func TestScenario_Memory_StoreLatestBySession(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	_, priv, _ := ed25519.GenerateKey(nil)
	zeroHash := "0000000000000000000000000000000000000000000000000000000000000000"

	c0 := &Checkpoint{Version: 1, ParentHash: zeroHash, DeviceID: "dev1", AuthorID: "alice",
		Timestamp: 1, SessionID: "sess-1", Turn: 1, ActiveModel: "m25", Summary: "first"}
	SignCheckpoint(c0, priv)

	c1 := &Checkpoint{Version: 1, ParentHash: c0.Hash, DeviceID: "dev1", AuthorID: "alice",
		Timestamp: 2, SessionID: "sess-1", Turn: 5, ActiveModel: "m25", Summary: "latest"}
	SignCheckpoint(c1, priv)

	_ = store.Append(c0)
	_ = store.Append(c1)

	latest, err := store.LatestBySession("sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if latest.Summary != "latest" {
		t.Errorf("summary = %q, want %q", latest.Summary, "latest")
	}
}
