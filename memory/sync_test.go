package memory

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func initBareRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	run(t, "", "git", "init", "--bare", dir)
	return dir
}

func initWorkRepo(t *testing.T, remote string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "work")
	run(t, "", "git", "clone", remote, dir)
	run(t, dir, "git", "config", "user.email", "test@test.com")
	run(t, dir, "git", "config", "user.name", "Test")
	// Ensure at least one commit on main
	readmePath := filepath.Join(dir, "README.md")
	if _, err := os.Stat(readmePath); os.IsNotExist(err) {
		_ = os.WriteFile(readmePath, []byte("test\n"), 0644)
		run(t, dir, "git", "add", ".")
		run(t, dir, "git", "commit", "-m", "init")
		run(t, dir, "git", "push", "origin", "main")
	}
	return dir
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = cleanGitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

func repoHash(remote string) string {
	h := sha256.Sum256([]byte(remote))
	return hex.EncodeToString(h[:])
}

// TestScenario_Sync_InitMemoryBranch maps to:
// Scenario: Initialize memory branch
func TestScenario_Sync_InitMemoryBranch(t *testing.T) {
	remote := initBareRepo(t)
	workDir := initWorkRepo(t, remote)

	syncer, err := NewSyncer(workDir, "ghyll/memory", "test-device")
	if err != nil {
		t.Fatalf("failed to create syncer: %v", err)
	}

	if err := syncer.InitBranch(); err != nil {
		t.Fatalf("init branch failed: %v", err)
	}

	// Verify orphan branch exists locally
	out := run(t, workDir, "git", "branch", "-a")
	if !containsLine(out, "ghyll/memory") {
		t.Errorf("ghyll/memory branch not found in:\n%s", out)
	}

	// Verify it was pushed to remote
	out = run(t, workDir, "git", "ls-remote", "origin", "ghyll/memory")
	if out == "" {
		t.Error("ghyll/memory not pushed to remote")
	}
}

// TestScenario_Sync_OrphanIsolation maps to:
// Scenario: Orphan branch isolation
func TestScenario_Sync_OrphanIsolation(t *testing.T) {
	remote := initBareRepo(t)
	workDir := initWorkRepo(t, remote)

	syncer, err := NewSyncer(workDir, "ghyll/memory", "test-device")
	if err != nil {
		t.Fatal(err)
	}
	if err := syncer.InitBranch(); err != nil {
		t.Fatal(err)
	}

	// Verify main log doesn't include memory commits
	out := run(t, workDir, "git", "log", "--oneline", "main")
	if containsLine(out, "ghyll/memory") {
		t.Error("memory commits visible in main log")
	}
}

// TestScenario_Sync_WriteCheckpoint maps to:
// Scenario: Checkpoint triggers sync
func TestScenario_Sync_WriteCheckpoint(t *testing.T) {
	remote := initBareRepo(t)
	workDir := initWorkRepo(t, remote)

	syncer, err := NewSyncer(workDir, "ghyll/memory", "test-device")
	if err != nil {
		t.Fatal(err)
	}
	if err := syncer.InitBranch(); err != nil {
		t.Fatal(err)
	}

	_, priv, _ := ed25519.GenerateKey(nil)
	cp := &Checkpoint{
		Version: 1, ParentHash: "0000000000000000000000000000000000000000000000000000000000000000",
		DeviceID: "test-device", AuthorID: "alice", Timestamp: 1,
		RepoRemote: remote, SessionID: "s1", Turn: 1,
		ActiveModel: "m25", Summary: "test checkpoint",
	}
	SignCheckpoint(cp, priv)

	rh := repoHash(remote)
	if err := syncer.WriteCheckpoint(cp, rh); err != nil {
		t.Fatalf("write checkpoint failed: %v", err)
	}

	if err := syncer.CommitAndPush(context.Background()); err != nil {
		t.Fatalf("commit and push failed: %v", err)
	}

	// Verify checkpoint file exists in worktree
	cpPath := filepath.Join(syncer.WorktreePath(), "repos", rh, "checkpoints", cp.Hash+".json")
	if _, err := os.Stat(cpPath); err != nil {
		t.Errorf("checkpoint file not found: %v", err)
	}
}

// TestScenario_Sync_ReadCheckpoints maps to:
// Scenario: Pull on session start
func TestScenario_Sync_ReadCheckpoints(t *testing.T) {
	remote := initBareRepo(t)
	workDir := initWorkRepo(t, remote)

	// Device A writes a checkpoint
	syncerA, err := NewSyncer(workDir, "ghyll/memory", "device-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := syncerA.InitBranch(); err != nil {
		t.Fatal(err)
	}

	_, priv, _ := ed25519.GenerateKey(nil)
	cp := &Checkpoint{
		Version: 1, ParentHash: "0000000000000000000000000000000000000000000000000000000000000000",
		DeviceID: "device-a", AuthorID: "alice", Timestamp: 1,
		RepoRemote: remote, SessionID: "s1", Turn: 1,
		ActiveModel: "m25", Summary: "from device A",
	}
	SignCheckpoint(cp, priv)

	rh := repoHash(remote)
	if err := syncerA.WriteCheckpoint(cp, rh); err != nil {
		t.Fatal(err)
	}
	if err := syncerA.CommitAndPush(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Device B clones and reads
	workDirB := initWorkRepo(t, remote)
	syncerB, err := NewSyncer(workDirB, "ghyll/memory", "device-b")
	if err != nil {
		t.Fatal(err)
	}
	// Don't init — just fetch existing
	if err := syncerB.Fetch(); err != nil {
		t.Fatal(err)
	}

	checkpoints, err := syncerB.ReadCheckpoints(rh)
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoints) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(checkpoints))
	}
	if checkpoints[0].Summary != "from device A" {
		t.Errorf("summary = %q", checkpoints[0].Summary)
	}
}

// TestScenario_Sync_OfflineOperation maps to:
// Scenario: Offline operation (write without push)
func TestScenario_Sync_OfflineOperation(t *testing.T) {
	remote := initBareRepo(t)
	workDir := initWorkRepo(t, remote)

	syncer, err := NewSyncer(workDir, "ghyll/memory", "test-device")
	if err != nil {
		t.Fatal(err)
	}
	if err := syncer.InitBranch(); err != nil {
		t.Fatal(err)
	}

	_, priv, _ := ed25519.GenerateKey(nil)
	cp := &Checkpoint{
		Version: 1, ParentHash: "0000000000000000000000000000000000000000000000000000000000000000",
		DeviceID: "test-device", AuthorID: "alice", Timestamp: 1,
		RepoRemote: remote, SessionID: "s1", Turn: 1,
		ActiveModel: "m25", Summary: "offline checkpoint",
	}
	SignCheckpoint(cp, priv)

	rh := repoHash(remote)
	// Write checkpoint locally but don't push
	if err := syncer.WriteCheckpoint(cp, rh); err != nil {
		t.Fatal(err)
	}

	// Checkpoint file should exist in worktree even without push
	cpPath := filepath.Join(syncer.WorktreePath(), "repos", rh, "checkpoints", cp.Hash+".json")
	if _, err := os.Stat(cpPath); err != nil {
		t.Errorf("checkpoint file should exist locally: %v", err)
	}
}

// TestScenario_Sync_PublicKeyDistribution maps to:
// Scenario: Public key pushed to memory branch + Remote public keys fetched
func TestScenario_Sync_PublicKeyDistribution(t *testing.T) {
	remote := initBareRepo(t)
	workDirA := initWorkRepo(t, remote)

	syncerA, err := NewSyncer(workDirA, "ghyll/memory", "alice-laptop")
	if err != nil {
		t.Fatal(err)
	}
	if err := syncerA.InitBranch(); err != nil {
		t.Fatal(err)
	}

	// Alice writes her public key
	alicePub := []byte("alice-public-key-data")
	if err := syncerA.WritePublicKey("alice-laptop", alicePub); err != nil {
		t.Fatal(err)
	}
	if err := syncerA.CommitAndPush(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Bob clones and fetches
	workDirB := initWorkRepo(t, remote)
	syncerB, err := NewSyncer(workDirB, "ghyll/memory", "bob-desktop")
	if err != nil {
		t.Fatal(err)
	}
	if err := syncerB.Fetch(); err != nil {
		t.Fatal(err)
	}

	// Bob should be able to read Alice's public key
	data, err := syncerB.ReadPublicKey("alice-laptop")
	if err != nil {
		t.Fatalf("failed to read alice's key: %v", err)
	}
	if string(data) != "alice-public-key-data" {
		t.Errorf("key data = %q", string(data))
	}
}

// TestScenario_Sync_PartialChainImport maps to:
// Scenario: Partial chain import
func TestScenario_Sync_PartialChainImport(t *testing.T) {
	remote := initBareRepo(t)
	workDir := initWorkRepo(t, remote)

	syncer, err := NewSyncer(workDir, "ghyll/memory", "dev-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := syncer.InitBranch(); err != nil {
		t.Fatal(err)
	}

	_, priv, _ := ed25519.GenerateKey(nil)
	rh := repoHash(remote)
	zeroHash := "0000000000000000000000000000000000000000000000000000000000000000"

	// Write 3 checkpoints in a chain
	c0 := &Checkpoint{Version: 1, ParentHash: zeroHash, DeviceID: "dev-a", AuthorID: "alice",
		Timestamp: 1, RepoRemote: remote, SessionID: "s1", Turn: 1, ActiveModel: "m25", Summary: "first"}
	SignCheckpoint(c0, priv)

	c1 := &Checkpoint{Version: 1, ParentHash: c0.Hash, DeviceID: "dev-a", AuthorID: "alice",
		Timestamp: 2, RepoRemote: remote, SessionID: "s1", Turn: 2, ActiveModel: "m25", Summary: "second"}
	SignCheckpoint(c1, priv)

	c2 := &Checkpoint{Version: 1, ParentHash: c1.Hash, DeviceID: "dev-a", AuthorID: "alice",
		Timestamp: 3, RepoRemote: remote, SessionID: "s1", Turn: 3, ActiveModel: "m25", Summary: "third"}
	SignCheckpoint(c2, priv)

	for _, cp := range []*Checkpoint{c0, c1, c2} {
		if err := syncer.WriteCheckpoint(cp, rh); err != nil {
			t.Fatal(err)
		}
	}
	if err := syncer.CommitAndPush(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Read all — should get 3
	all, err := syncer.ReadCheckpoints(rh)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 checkpoints, got %d", len(all))
	}

	// Verify chain integrity
	result := VerifyChain([]Checkpoint{*c0, *c1, *c2})
	if !result.Valid {
		t.Errorf("chain verification failed: %s", result.Reason)
	}
}

// TestScenario_Sync_DeviceID maps to:
// Scenario: Device ID derivation
func TestScenario_Sync_DeviceID(t *testing.T) {
	dir := t.TempDir()
	keysDir := filepath.Join(dir, "keys")

	// Generate key with specific device ID
	key, err := LoadOrGenerateKey(keysDir, "alice-laptop")
	if err != nil {
		t.Fatal(err)
	}
	if key.DeviceID != "alice-laptop" {
		t.Errorf("device ID = %q, want alice-laptop", key.DeviceID)
	}

	// Reload — should be stable
	key2, err := LoadOrGenerateKey(keysDir, "alice-laptop")
	if err != nil {
		t.Fatal(err)
	}
	if key2.DeviceID != key.DeviceID {
		t.Error("device ID should be stable across loads")
	}
}

func containsLine(output, substr string) bool {
	for _, line := range splitLines(output) {
		if contains(line, substr) {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchStr(s, substr)
}

func searchStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Helper to suppress unused import warnings
var _ = time.Second
var _ = json.Marshal
