package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Lockfile enforces one session per repo (invariant 31).
type Lockfile struct {
	path string
}

// AcquireLock attempts to acquire the repo lockfile.
// Returns error if another session is active.
func AcquireLock(repoDir string) (*Lockfile, error) {
	path := filepath.Join(repoDir, ".ghyll.lock")

	// Check for existing lock
	if data, err := os.ReadFile(path); err == nil {
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr == nil && isProcessAlive(pid) {
			return nil, fmt.Errorf("another ghyll session is active (pid %d)", pid)
		}
		// Stale lock — reclaim
	}

	// Write our PID
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}

	return &Lockfile{path: path}, nil
}

// Release removes the lockfile (invariant 32).
func (l *Lockfile) Release() {
	_ = os.Remove(l.path)
}

func isProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 checks if process exists without actually sending a signal
	return process.Signal(syscall.Signal(0)) == nil
}
