package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

// isProcessAlive reports whether a process at pid exists AND is a
// ghyll process. The second half matters in PID namespaces (bwrap,
// docker, podman) where pid 2 is "alive" in every fresh sandbox
// because that's the position the wrapper sits at — a stale
// lockfile from a previous sandboxed session recording pid 2 would
// otherwise false-positive every subsequent launch.
//
// On Linux we read /proc/<pid>/comm and require the executable's
// basename to be "ghyll". On other OSes (or when /proc isn't
// readable — e.g. EPERM for a process owned by a different uid)
// we degrade to the signal-0 check, accepting the pre-existing
// false-positive risk there.
func isProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if process.Signal(syscall.Signal(0)) != nil {
		return false
	}
	// Same-process check covers: (a) AcquireLock called twice from
	// the same test binary, (b) the rare case where ghyll
	// pre-recorded its own PID and the comm-check would otherwise
	// reject because os.Args[0] != "ghyll" (go test binary, manual
	// `go run`, etc.). os.Getpid() == pid is a definitive "yes,
	// alive, and us".
	if pid == os.Getpid() {
		return true
	}
	if runtime.GOOS != "linux" {
		return true
	}
	commPath := fmt.Sprintf("/proc/%d/comm", pid)
	data, readErr := os.ReadFile(commPath)
	if readErr != nil {
		// /proc/<pid>/comm unreadable: most likely EPERM on a
		// process we don't own. Fall back to signal-0 result
		// (true) — the pre-existing behavior.
		return true
	}
	return strings.TrimSpace(string(data)) == "ghyll"
}
