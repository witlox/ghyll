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
	"strings"
	"testing"
	"time"
)

func initBareRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	run(t, "", "git", "init", "--bare", "-b", "main", dir)
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
		run(t, dir, "git", "checkout", "-b", "main")
		run(t, dir, "git", "add", ".")
		run(t, dir, "git", "commit", "-m", "init")
		run(t, dir, "git", "push", "-u", "origin", "main")
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

// TestScenario_Sync_InitBranch_ReadOnlyRemoteFallsBackToLocal —
// regression for the CSCS startup warning: when the operator has no
// push permission on origin (HPC shared clone, read-only remote, or
// origin path gone), InitBranch was emitting
// "fetch after init: couldn't find remote ref ghyll/memory" because
// the optional push silently failed and the subsequent fetch from
// origin had nothing to fetch.
//
// Fix: when push fails, fetch the branch from the tmp repo we just
// created it in. This test simulates the failure by removing the
// bare remote after cloning — push then errors but the local branch
// still has to land cleanly so every subsequent session start is
// quiet.
func TestScenario_Sync_InitBranch_ReadOnlyRemoteFallsBackToLocal(t *testing.T) {
	remote := initBareRepo(t)
	workDir := initWorkRepo(t, remote)

	// Simulate a read-only remote: a pre-receive hook that refuses
	// every push. Clone/fetch still work (those are reads), but
	// `git push origin ghyll/memory` returns non-zero. This is the
	// production analog of "operator has no push permission" /
	// "shared HPC clone" / "Gerrit gating intercepts the branch".
	hookPath := filepath.Join(remote, "hooks", "pre-receive")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("install pre-receive hook: %v", err)
	}

	syncer, err := NewSyncer(workDir, "ghyll/memory", "test-device")
	if err != nil {
		t.Fatalf("new syncer: %v", err)
	}
	if err := syncer.InitBranch(); err != nil {
		t.Fatalf("init branch must succeed even when origin is unreachable, got: %v", err)
	}

	// Verify the local orphan branch exists.
	out := run(t, workDir, "git", "branch", "--list", "ghyll/memory")
	if out == "" {
		t.Errorf("ghyll/memory branch not created locally; got branch list: %q", out)
	}

	// FE-SEC-2 remediation: assert the push was actually rejected
	// (origin ref absent). The original test only checked that the
	// local branch landed — but BOTH the success path (fetch from
	// origin) and the fallback path (fetch from tmpRepo) end in the
	// local branch landing. Without this assertion, a flake in the
	// pre-receive hook (perm flake, git version drift) lets the test
	// pass via the canonical push-then-fetch-from-origin path and
	// the regression — noisy first-run warning on read-only remotes
	// — silently returns. Asserting `git ls-remote origin
	// ghyll/memory` is empty proves the hook fired AND we exercised
	// the local-fallback code path under test.
	lsOut := run(t, workDir, "git", "ls-remote", "origin", "ghyll/memory")
	if strings.TrimSpace(lsOut) != "" {
		t.Errorf("pre-receive hook didn't reject push — origin has ghyll/memory: %q; this test no longer exercises the fallback path", lsOut)
	}
}

// TestScenario_Sync_Fetch_NoRemoteRefIsQuiet — regression for the
// "⚠ initial sync failed: couldn't find remote ref ghyll/memory"
// warning that fired at every session startup when InitBranch's
// optional push to origin had silently degraded to local-only
// (read-only remote, no push perms). Fetch must now treat
// "remote doesn't have this branch yet" as a no-op, not an error.
func TestScenario_Sync_Fetch_NoRemoteRefIsQuiet(t *testing.T) {
	remote := initBareRepo(t)
	workDir := initWorkRepo(t, remote)

	// Same setup as the InitBranch fallback test: a pre-receive
	// hook that refuses pushes simulates the read-only-remote case.
	hookPath := filepath.Join(remote, "hooks", "pre-receive")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("install pre-receive hook: %v", err)
	}

	syncer, err := NewSyncer(workDir, "ghyll/memory", "test-device")
	if err != nil {
		t.Fatalf("new syncer: %v", err)
	}
	if err := syncer.InitBranch(); err != nil {
		t.Fatalf("init branch: %v", err)
	}

	// Origin must NOT have the ref (push was refused).
	lsOut := run(t, workDir, "git", "ls-remote", "origin", "ghyll/memory")
	if strings.TrimSpace(lsOut) != "" {
		t.Fatalf("test precondition: origin should NOT carry the branch, got: %q", lsOut)
	}

	// THIS is what regressed: Fetch must not return an error just
	// because origin has nothing for us yet.
	if err := syncer.Fetch(); err != nil {
		t.Errorf("Fetch must be quiet when origin lacks the branch, got: %v", err)
	}
}

// TestScenario_Sync_SetupWorktree_PrunesStaleRegistration —
// regression for "add worktree: ... 'ghyll/memory' is already used
// by worktree at '<old-temp-path>'". NewSyncer picks a fresh temp
// worktree dir each session, but git's worktree DB persists every
// registration. After enough sessions whose temp dirs got cleaned
// (or were on a /tmp that the kernel purged at boot), the next
// `git worktree add` fatals because the branch is "already used"
// by a worktree pointing at a now-empty path.
//
// Fix: prune stale entries before add. This test simulates the
// failure by manually registering a worktree at a temp path, then
// removing that path from disk so it becomes a dead DB row.
func TestScenario_Sync_SetupWorktree_PrunesStaleRegistration(t *testing.T) {
	remote := initBareRepo(t)
	workDir := initWorkRepo(t, remote)

	// Seed the branch (via a first syncer) so the test starts from
	// a state where ghyll/memory exists locally.
	first, err := NewSyncer(workDir, "ghyll/memory", "test-device-1")
	if err != nil {
		t.Fatalf("first syncer: %v", err)
	}
	if err := first.InitBranch(); err != nil {
		t.Fatalf("first init: %v", err)
	}
	staleWT := first.WorktreePath()

	// Simulate the stale-temp-dir scenario: delete the live
	// worktree directory but leave the git worktree DB entry
	// alone. The next `git worktree add` for the same branch will
	// now fail with the "already used by" fatal under the OLD
	// code; under the fix it succeeds because prune drops the
	// dangling entry.
	if err := os.RemoveAll(staleWT); err != nil {
		t.Fatalf("remove stale wt: %v", err)
	}

	// A fresh syncer (new temp wt dir) calling Fetch/InitBranch
	// has to exercise setupWorktree.
	second, err := NewSyncer(workDir, "ghyll/memory", "test-device-2")
	if err != nil {
		t.Fatalf("second syncer: %v", err)
	}
	// Branch exists locally → InitBranch jumps straight to setupWorktree.
	if err := second.InitBranch(); err != nil {
		t.Errorf("second InitBranch must succeed after pruning stale registrations, got: %v", err)
	}
	// Worktree should be live.
	if _, err := os.Stat(second.WorktreePath()); err != nil {
		t.Errorf("new worktree should exist after setup, got stat err=%v", err)
	}
}

// TestScenario_Sync_SetupWorktree_StaleOnDiskWorktree — second
// regression on the same code path. The previous fix (prune)
// covered the case where the stale temp dir was GONE; this one
// covers the case where the stale temp dir STILL EXISTS. CSCS
// login nodes don't clear /tmp across sessions, so the previous
// session's worktree directory sits there as a live (but
// orphaned-by-lockfile) registration. prune is a no-op; only
// `git worktree add --force` can steal the branch back.
//
// ADR-006 guarantees safety: the .ghyll.lock check (with the new
// PID validation in cmd/ghyll/lockfile.go) means no other ghyll
// is running, so the on-disk worktree is by definition stale.
func TestScenario_Sync_SetupWorktree_StaleOnDiskWorktree(t *testing.T) {
	remote := initBareRepo(t)
	workDir := initWorkRepo(t, remote)

	// First syncer creates and registers a worktree.
	first, err := NewSyncer(workDir, "ghyll/memory", "test-device-1")
	if err != nil {
		t.Fatalf("first syncer: %v", err)
	}
	if err := first.InitBranch(); err != nil {
		t.Fatalf("first init: %v", err)
	}
	// Don't delete first.WorktreePath() — leave it on disk to
	// simulate the live-stale case. prune will be a no-op here.

	second, err := NewSyncer(workDir, "ghyll/memory", "test-device-2")
	if err != nil {
		t.Fatalf("second syncer: %v", err)
	}
	if err := second.InitBranch(); err != nil {
		t.Errorf("second InitBranch must succeed even when stale worktree dir is still on disk, got: %v", err)
	}
	if _, err := os.Stat(second.WorktreePath()); err != nil {
		t.Errorf("new worktree should exist after setup, got stat err=%v", err)
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
