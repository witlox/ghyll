package main

import (
	"os"
	"path/filepath"
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
