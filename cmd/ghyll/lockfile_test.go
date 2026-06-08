package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

func TestScenario_Lockfile_Acquire(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	defer lock.Release()

	// Verify lockfile exists with our PID
	data, err := os.ReadFile(filepath.Join(dir, ".ghyll.lock"))
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(string(data))
	if pid != os.Getpid() {
		t.Errorf("lockfile pid = %d, want %d", pid, os.Getpid())
	}
}

func TestScenario_Lockfile_RejectsSecondSession(t *testing.T) {
	dir := t.TempDir()
	lock1, err := AcquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock1.Release()

	// Second acquire should fail (our PID is alive)
	_, err = AcquireLock(dir)
	if err == nil {
		t.Fatal("expected error for second session")
	}
}

func TestScenario_Lockfile_RecoversStaleLock(t *testing.T) {
	dir := t.TempDir()
	// Write a stale lock with a dead PID
	_ = os.WriteFile(filepath.Join(dir, ".ghyll.lock"), []byte("999999999"), 0644)

	lock, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("should recover stale lock: %v", err)
	}
	lock.Release()
}

// TestScenario_Lockfile_StaleSandboxPID — regression for the bwrap
// sandbox false-positive: a previous PID-namespaced session recorded
// pid 2 in the lockfile, then this session starts in a fresh PID
// namespace where pid 2 happens to be alive too (the wrapper). The
// old signal-0-only check returned "alive" and refused to acquire.
// Linux fix: read /proc/<pid>/comm and require "ghyll" — pid 1 is
// always alive on Linux but is never named ghyll, so we use it as
// the stand-in for a foreign-but-alive PID.
func TestScenario_Lockfile_StaleSandboxPID(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires /proc/<pid>/comm (linux-only)")
	}
	dir := t.TempDir()
	// pid 1 is init / systemd / launchd; always alive, never named
	// "ghyll". Old code: refuses because signal-0 succeeds. New
	// code: recognizes the comm mismatch and treats as stale.
	if err := os.WriteFile(filepath.Join(dir, ".ghyll.lock"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("acquire should succeed when lock pid is not a ghyll process, got: %v", err)
	}
	lock.Release()
}

func TestScenario_Lockfile_Release(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	lock.Release()

	// Should be able to acquire again after release
	lock2, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("should acquire after release: %v", err)
	}
	lock2.Release()
}
