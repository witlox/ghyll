package main

import (
	"bytes"
	"crypto/ed25519"
	"path/filepath"
	"strings"
	"testing"

	"github.com/witlox/ghyll/memory"
)

func seedStore(t *testing.T, dir string) *memory.Store {
	t.Helper()
	store, err := memory.OpenStore(filepath.Join(dir, "memory.db"))
	if err != nil {
		t.Fatal(err)
	}

	_, priv, _ := ed25519.GenerateKey(nil)
	zeroHash := "0000000000000000000000000000000000000000000000000000000000000000"

	c0 := &memory.Checkpoint{
		Version: 1, ParentHash: zeroHash, DeviceID: "dev1", AuthorID: "alice",
		Timestamp: 1000, SessionID: "sess-1", Turn: 1, ActiveModel: "m25",
		Summary: "fixed auth race condition in session.go",
	}
	memory.SignCheckpoint(c0, priv)

	c1 := &memory.Checkpoint{
		Version: 1, ParentHash: c0.Hash, DeviceID: "dev1", AuthorID: "alice",
		Timestamp: 2000, SessionID: "sess-1", Turn: 5, ActiveModel: "m25",
		Summary: "added mutex to session refresh, compaction at turn 5",
	}
	memory.SignCheckpoint(c1, priv)

	c2 := &memory.Checkpoint{
		Version: 1, ParentHash: zeroHash, DeviceID: "dev2", AuthorID: "bob",
		Timestamp: 3000, SessionID: "sess-2", Turn: 3, ActiveModel: "glm5",
		Summary: "refactored payment module error handling",
	}
	memory.SignCheckpoint(c2, priv)

	for _, cp := range []*memory.Checkpoint{c0, c1, c2} {
		if err := store.Append(cp); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

// TestScenario_MemoryLog
func TestScenario_MemoryLog(t *testing.T) {
	dir := t.TempDir()
	store := seedStore(t, dir)
	defer func() { _ = store.Close() }()

	var buf bytes.Buffer
	err := cmdMemoryLog(store, &buf)
	if err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	if !strings.Contains(output, "fixed auth race condition") {
		t.Errorf("missing checkpoint summary in output:\n%s", output)
	}
	if !strings.Contains(output, "refactored payment module") {
		t.Errorf("missing bob's checkpoint:\n%s", output)
	}
}

// TestScenario_MemorySearch
func TestScenario_MemorySearch(t *testing.T) {
	dir := t.TempDir()
	store := seedStore(t, dir)
	defer func() { _ = store.Close() }()

	var buf bytes.Buffer
	err := cmdMemorySearch(store, "auth race condition", &buf)
	if err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	// Text search should find checkpoints containing the query terms
	if !strings.Contains(output, "auth race condition") {
		t.Errorf("search didn't find relevant checkpoint:\n%s", output)
	}
}

// TestScenario_MemorySearch_NoResults
func TestScenario_MemorySearch_NoResults(t *testing.T) {
	dir := t.TempDir()
	store := seedStore(t, dir)
	defer func() { _ = store.Close() }()

	var buf bytes.Buffer
	err := cmdMemorySearch(store, "nonexistent xyz abc", &buf)
	if err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	if !strings.Contains(output, "no matching checkpoints") {
		t.Errorf("expected no-results message:\n%s", output)
	}
}
