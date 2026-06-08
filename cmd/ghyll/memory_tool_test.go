package main

import (
	"crypto/ed25519"
	"path/filepath"
	"strings"
	"testing"

	"github.com/witlox/ghyll/memory"
)

// seedSessionWithMemory builds a minimal Session with a real
// memory store containing two checkpoints — enough to exercise
// hash-prefix + text-search paths via the memory_search tool.
func seedSessionWithMemory(t *testing.T) (*Session, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := memory.OpenStore(filepath.Join(dir, "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	_, priv, _ := ed25519.GenerateKey(nil)
	zeroHash := "0000000000000000000000000000000000000000000000000000000000000000"

	c0 := &memory.Checkpoint{
		Version: 1, ParentHash: zeroHash, DeviceID: "dev1", AuthorID: "alice",
		Timestamp: 1000, SessionID: "sess-1", Turn: 1, ActiveModel: "kimi",
		Summary: "fixed auth race condition in session.go",
	}
	memory.SignCheckpoint(c0, priv)
	if err := store.Append(c0); err != nil {
		t.Fatal(err)
	}

	c1 := &memory.Checkpoint{
		Version: 1, ParentHash: c0.Hash, DeviceID: "dev1", AuthorID: "alice",
		Timestamp: 2000, SessionID: "sess-1", Turn: 5, ActiveModel: "kimi",
		Summary: "added mutex to session refresh, compaction at turn 5",
	}
	memory.SignCheckpoint(c1, priv)
	if err := store.Append(c1); err != nil {
		t.Fatal(err)
	}

	return &Session{store: store}, c0.Hash
}

// TestScenario_MemorySearchTool_TextHit — model asks "what did we
// fix in auth?" → memory_search returns the matching checkpoint
// rendered as a model-readable block.
func TestScenario_MemorySearchTool_TextHit(t *testing.T) {
	s, _ := seedSessionWithMemory(t)
	res := s.memorySearchTool("auth race", 5)
	if res.Error != "" {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	if !strings.Contains(res.Output, "fixed auth race condition") {
		t.Errorf("missing matching summary in output:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "Found 1 checkpoint") {
		t.Errorf("missing match-count header:\n%s", res.Output)
	}
}

// TestScenario_MemorySearchTool_HashPrefix — model recalls a hash
// it saw earlier and queries by prefix.
func TestScenario_MemorySearchTool_HashPrefix(t *testing.T) {
	s, hash := seedSessionWithMemory(t)
	res := s.memorySearchTool(hash[:12], 5)
	if res.Error != "" {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	if !strings.Contains(res.Output, hash[:12]) {
		t.Errorf("output should include the matched hash prefix:\n%s", res.Output)
	}
}

// TestScenario_MemorySearchTool_EmptyQuery — defensive: empty
// query is an operator-error / model bug. Return Error not Output
// so the model retries with content.
func TestScenario_MemorySearchTool_EmptyQuery(t *testing.T) {
	s, _ := seedSessionWithMemory(t)
	res := s.memorySearchTool("", 5)
	if res.Error == "" {
		t.Errorf("empty query should return Error, got Output=%q", res.Output)
	}
}

// TestScenario_MemorySearchTool_NoMatch — query with no hits
// returns a friendly Output ("no matching checkpoints"), not an
// Error — the search ran fine, it just found nothing.
func TestScenario_MemorySearchTool_NoMatch(t *testing.T) {
	s, _ := seedSessionWithMemory(t)
	res := s.memorySearchTool("nonexistent xyzzy term", 5)
	if res.Error != "" {
		t.Errorf("no-match should not be Error: %s", res.Error)
	}
	if !strings.Contains(res.Output, "no matching") {
		t.Errorf("expected 'no matching checkpoints', got: %s", res.Output)
	}
}

// TestScenario_MemorySearchTool_LimitClamp — limit is clamped to
// [1, 20]. A model asking for 0 or 1000 results gets the safe
// default / cap instead of an error.
func TestScenario_MemorySearchTool_LimitClamp(t *testing.T) {
	s, _ := seedSessionWithMemory(t)
	// 0 → default 5
	res := s.memorySearchTool("session", 0)
	if res.Error != "" {
		t.Errorf("limit=0 should clamp not error: %s", res.Error)
	}
	// 1000 → capped to 20, still works
	res = s.memorySearchTool("session", 1000)
	if res.Error != "" {
		t.Errorf("limit=1000 should clamp not error: %s", res.Error)
	}
}
