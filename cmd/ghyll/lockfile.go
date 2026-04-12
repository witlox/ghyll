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
// Uses O_CREATE|O_EXCL for atomic creation to avoid TOCTOU race.
func AcquireLock(repoDir string) (*Lockfile, error) {
	path := filepath.Join(repoDir, ".ghyll.lock")

	// Try atomic create first
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if !os.IsExist(err) {
			return nil, fmt.Errorf("acquire lock: %w", err)
		}
		// File exists — check if stale
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("acquire lock: read existing: %w", readErr)
		}
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr == nil && isProcessAlive(pid) {
			return nil, fmt.Errorf("another ghyll session is active (pid %d)", pid)
		}
		// Stale lock — remove and retry atomically
		_ = os.Remove(path)
		f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return nil, fmt.Errorf("acquire lock after stale removal: %w", err)
		}
	}

	// Write our PID
	_, _ = fmt.Fprintf(f, "%d", os.Getpid())
	_ = f.Close()

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
	return process.Signal(syscall.Signal(0)) == nil
}
