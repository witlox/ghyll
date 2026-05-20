package memory

import (
	"errors"
	"strings"
	"testing"
)

// Tier 3 coverage push — SyncError + helper functions.

func TestScenario_SyncError_FormatAndUnwrap(t *testing.T) {
	inner := errors.New("boom")
	se := &SyncError{Op: "fetch", Attempt: 2, Err: inner}
	msg := se.Error()
	if !strings.Contains(msg, "fetch") || !strings.Contains(msg, "attempt 2") {
		t.Errorf("Error() = %q; want fetch + attempt 2", msg)
	}
	if !errors.Is(se, inner) {
		t.Error("Unwrap doesn't propagate inner")
	}
}

func TestScenario_cleanGitEnvSlice_StripsGitDirVars(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"GIT_DIR=/tmp/repo/.git",
		"HOME=/home/alice",
		"GIT_WORK_TREE=/tmp/work",
		"GIT_INDEX_FILE=/tmp/idx",
		"USER=alice",
	}
	out := cleanGitEnvSlice(in)
	for _, e := range out {
		if strings.HasPrefix(e, "GIT_DIR=") ||
			strings.HasPrefix(e, "GIT_WORK_TREE=") ||
			strings.HasPrefix(e, "GIT_INDEX_FILE=") {
			t.Errorf("output retains git var: %s", e)
		}
	}
	if len(out) != 3 {
		t.Errorf("len = %d; want 3 (PATH+HOME+USER)", len(out))
	}
}

func TestScenario_GitRemoteURL_NonRepoReturnsEmpty(t *testing.T) {
	// Run against a temp dir that is not a git repo.
	dir := t.TempDir()
	got := GitRemoteURL(dir)
	if got != "" {
		t.Errorf("non-repo dir: GitRemoteURL = %q; want empty", got)
	}
}
