package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SyncError wraps git operation failures.
type SyncError struct {
	Op      string // "fetch", "push", "pull", "init"
	Attempt int
	Err     error
}

func (e *SyncError) Error() string {
	return fmt.Sprintf("memory: sync %s (attempt %d): %v", e.Op, e.Attempt, e.Err)
}

func (e *SyncError) Unwrap() error {
	return e.Err
}

// Syncer manages the git orphan branch worktree for memory sync.
type Syncer struct {
	repoDir     string
	branchName  string
	deviceID    string
	worktreeDir string
	initialized bool
}

// NewSyncer creates a syncer for the given repository.
func NewSyncer(repoDir string, branchName string, deviceID string) (*Syncer, error) {
	// Use a temp dir for the worktree to avoid conflicts with the main repo
	worktreeDir, err := os.MkdirTemp("", "ghyll-memory-*")
	if err != nil {
		return nil, fmt.Errorf("memory: create worktree dir: %w", err)
	}
	// Remove so git worktree add can create it
	_ = os.Remove(worktreeDir)

	return &Syncer{
		repoDir:     repoDir,
		branchName:  branchName,
		deviceID:    deviceID,
		worktreeDir: worktreeDir,
	}, nil
}

// WorktreePath returns the path to the worktree directory.
func (s *Syncer) WorktreePath() string {
	return s.worktreeDir
}

// InitBranch creates the orphan branch and worktree if they don't exist.
// Invariant 12: orphan branch shares no history with code branches.
// Uses a temporary clone to avoid disturbing the main repo's index/HEAD.
func (s *Syncer) InitBranch() error {
	// Check if branch already exists locally
	if s.branchExists() {
		return s.setupWorktree()
	}

	// Check if branch exists on remote
	out, err := s.git("ls-remote", "origin", s.branchName)
	if err == nil && strings.TrimSpace(out) != "" {
		if _, err := s.git("fetch", "origin", s.branchName+":"+s.branchName); err != nil {
			return &SyncError{Op: "init", Err: fmt.Errorf("fetch existing branch: %w", err)}
		}
		return s.setupWorktree()
	}

	// Create orphan branch in a temporary clone to avoid touching the main repo's index
	tmpDir, err := os.MkdirTemp("", "ghyll-init-*")
	if err != nil {
		return &SyncError{Op: "init", Err: err}
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	origin := s.repoDir
	// If there's a remote, clone from it; otherwise clone local
	if remoteURL, err := s.git("remote", "get-url", "origin"); err == nil {
		origin = strings.TrimSpace(remoteURL)
	}

	// Shallow clone just to get a git repo context
	tmpRepo := filepath.Join(tmpDir, "repo")
	cmd := exec.Command("git", "clone", "--no-checkout", "--depth=1", origin, tmpRepo)
	cmd.Env = cleanGitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		return &SyncError{Op: "init", Err: fmt.Errorf("clone for init: %s: %w", out, err)}
	}

	// Configure git in tmp repo
	cfgEmail := exec.Command("git", "-C", tmpRepo, "config", "user.email", "ghyll@local")
	cfgEmail.Env = cleanGitEnv()
	_ = cfgEmail.Run()
	cfgName := exec.Command("git", "-C", tmpRepo, "config", "user.name", "ghyll")
	cfgName.Env = cleanGitEnv()
	_ = cfgName.Run()

	// Create orphan branch in the tmp repo
	cmd = exec.Command("git", "-C", tmpRepo, "checkout", "--orphan", s.branchName)
	cmd.Env = cleanGitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		return &SyncError{Op: "init", Err: fmt.Errorf("create orphan: %s: %w", out, err)}
	}

	cmd = exec.Command("git", "-C", tmpRepo, "rm", "-rf", "--cached", ".")
	cmd.Env = cleanGitEnv()
	_, _ = cmd.CombinedOutput()

	// Create initial structure
	devicesDir := filepath.Join(tmpRepo, "devices")
	_ = os.MkdirAll(devicesDir, 0755)
	_ = os.WriteFile(filepath.Join(devicesDir, ".gitkeep"), []byte{}, 0644)

	cmd = exec.Command("git", "-C", tmpRepo, "add", "devices/.gitkeep")
	cmd.Env = cleanGitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		return &SyncError{Op: "init", Err: fmt.Errorf("git add: %s: %w", out, err)}
	}

	cmd = exec.Command("git", "-C", tmpRepo, "commit", "-m", fmt.Sprintf("init: device %s", s.deviceID))
	cmd.Env = cleanGitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		return &SyncError{Op: "init", Err: fmt.Errorf("commit: %s: %w", out, err)}
	}

	// Push orphan branch to remote
	cmd = exec.Command("git", "-C", tmpRepo, "push", "origin", s.branchName)
	cmd.Env = cleanGitEnv()
	_, _ = cmd.CombinedOutput() // may fail if no remote

	// Fetch the newly created branch into main repo
	if _, err := s.git("fetch", "origin", s.branchName+":"+s.branchName); err != nil {
		return &SyncError{Op: "init", Err: fmt.Errorf("fetch after init: %w", err)}
	}

	return s.setupWorktree()
}

func (s *Syncer) setupWorktree() error {
	// Check if worktree already exists
	if _, err := os.Stat(s.worktreeDir); err == nil {
		s.initialized = true
		return nil
	}

	if _, err := s.git("worktree", "add", s.worktreeDir, s.branchName); err != nil {
		return &SyncError{Op: "init", Err: fmt.Errorf("add worktree: %w", err)}
	}

	s.initialized = true
	return nil
}

func (s *Syncer) branchExists() bool {
	out, err := s.git("branch", "--list", s.branchName)
	return err == nil && strings.TrimSpace(out) != ""
}

// Fetch pulls the latest memory branch from remote.
func (s *Syncer) Fetch() error {
	if _, err := s.git("fetch", "origin", s.branchName+":"+s.branchName); err != nil {
		return &SyncError{Op: "fetch", Err: err}
	}
	// Setup worktree if needed
	if !s.initialized {
		return s.setupWorktree()
	}
	// Update worktree
	if _, err := s.gitInWorktree("merge", "--ff-only", "origin/"+s.branchName); err != nil {
		// May fail if no tracking — try reset
		_, _ = s.gitInWorktree("reset", "--hard", s.branchName)
	}
	return nil
}

// WriteCheckpoint writes a checkpoint JSON file to the worktree.
func (s *Syncer) WriteCheckpoint(cp *Checkpoint, repoHash string) error {
	if !s.initialized {
		return &SyncError{Op: "write", Err: fmt.Errorf("worktree not initialized")}
	}

	// Ensure directory structure
	cpDir := filepath.Join(s.worktreeDir, "repos", repoHash, "checkpoints")
	chainDir := filepath.Join(s.worktreeDir, "repos", repoHash, "chains")
	if err := os.MkdirAll(cpDir, 0755); err != nil {
		return &SyncError{Op: "write", Err: err}
	}
	if err := os.MkdirAll(chainDir, 0755); err != nil {
		return &SyncError{Op: "write", Err: err}
	}

	// Write checkpoint JSON
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return &SyncError{Op: "write", Err: err}
	}
	cpPath := filepath.Join(cpDir, cp.Hash+".json")
	if err := os.WriteFile(cpPath, data, 0644); err != nil {
		return &SyncError{Op: "write", Err: err}
	}

	// Append to chain file
	chainPath := filepath.Join(chainDir, cp.DeviceID+".jsonl")
	entry := struct {
		Hash   string `json:"hash"`
		Parent string `json:"parent"`
		TS     int64  `json:"ts"`
	}{cp.Hash, cp.ParentHash, cp.Timestamp}
	entryJSON, _ := json.Marshal(entry)
	f, err := os.OpenFile(chainPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return &SyncError{Op: "write", Err: err}
	}
	_, err = f.Write(append(entryJSON, '\n'))
	_ = f.Close()
	if err != nil {
		return &SyncError{Op: "write", Err: err}
	}

	return nil
}

// CommitAndPush commits all changes in the worktree and pushes.
// Invariant 13: non-blocking when called from background goroutine.
// Invariant 14: idempotent — content-hash filenames.
func (s *Syncer) CommitAndPush(ctx context.Context) error {
	if !s.initialized {
		return &SyncError{Op: "push", Err: fmt.Errorf("worktree not initialized")}
	}

	// Stage all changes
	if _, err := s.gitInWorktree("add", "."); err != nil {
		return &SyncError{Op: "push", Err: fmt.Errorf("git add: %w", err)}
	}

	// Check if there's anything to commit
	out, _ := s.gitInWorktree("status", "--porcelain")
	if strings.TrimSpace(out) == "" {
		return nil // nothing to commit
	}

	if _, err := s.gitInWorktree("commit", "-m", fmt.Sprintf("checkpoint by %s", s.deviceID)); err != nil {
		return &SyncError{Op: "push", Err: fmt.Errorf("git commit: %w", err)}
	}

	// Push with retry on conflict
	for attempt := 1; attempt <= 3; attempt++ {
		if _, err := s.gitInWorktree("push", "origin", s.branchName); err == nil {
			return nil
		}
		// Pull and retry
		_, _ = s.gitInWorktree("pull", "--ff-only", "origin", s.branchName)
	}

	return &SyncError{Op: "push", Attempt: 3, Err: fmt.Errorf("push failed after 3 retries")}
}

// ReadCheckpoints reads all checkpoint files for a repo from the worktree.
func (s *Syncer) ReadCheckpoints(repoHash string) ([]Checkpoint, error) {
	if !s.initialized {
		return nil, &SyncError{Op: "read", Err: fmt.Errorf("worktree not initialized")}
	}

	cpDir := filepath.Join(s.worktreeDir, "repos", repoHash, "checkpoints")
	entries, err := os.ReadDir(cpDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, &SyncError{Op: "read", Err: err}
	}

	var checkpoints []Checkpoint
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(cpDir, entry.Name()))
		if err != nil {
			continue
		}
		var cp Checkpoint
		if err := json.Unmarshal(data, &cp); err != nil {
			continue
		}
		checkpoints = append(checkpoints, cp)
	}
	return checkpoints, nil
}

// WritePublicKey writes a device public key to the worktree.
func (s *Syncer) WritePublicKey(deviceID string, pubKeyPEM []byte) error {
	if !s.initialized {
		return &SyncError{Op: "write", Err: fmt.Errorf("worktree not initialized")}
	}
	devicesDir := filepath.Join(s.worktreeDir, "devices")
	if err := os.MkdirAll(devicesDir, 0755); err != nil {
		return &SyncError{Op: "write", Err: err}
	}
	return os.WriteFile(filepath.Join(devicesDir, deviceID+".pub"), pubKeyPEM, 0644)
}

// ReadPublicKey reads a device public key from the worktree.
func (s *Syncer) ReadPublicKey(deviceID string) ([]byte, error) {
	if !s.initialized {
		return nil, &SyncError{Op: "read", Err: fmt.Errorf("worktree not initialized")}
	}
	path := filepath.Join(s.worktreeDir, "devices", deviceID+".pub")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// cleanGitEnv returns env vars with GIT_DIR/GIT_WORK_TREE removed
// to prevent leaking from parent processes (e.g., git hooks).
func cleanGitEnv() []string {
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "GIT_DIR=") &&
			!strings.HasPrefix(e, "GIT_WORK_TREE=") &&
			!strings.HasPrefix(e, "GIT_INDEX_FILE=") {
			env = append(env, e)
		}
	}
	return append(env, "GIT_TERMINAL_PROMPT=0")
}

func (s *Syncer) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = s.repoDir
	cmd.Env = cleanGitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %s", err, stderr.String())
	}
	return stdout.String(), nil
}

func (s *Syncer) gitInWorktree(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = s.worktreeDir
	cmd.Env = cleanGitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %s", err, stderr.String())
	}
	return stdout.String(), nil
}
