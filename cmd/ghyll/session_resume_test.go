package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/witlox/ghyll/memory"
	"github.com/witlox/ghyll/types"
)

// newResumeTestStore opens a fresh sqlite-backed memory.Store at a
// per-test path, plus generates a device key. The store is closed
// via t.Cleanup.
func newResumeTestStore(t *testing.T) (*memory.Store, *memory.DeviceKey) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "memory.db")
	store, err := memory.OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	key, err := memory.LoadOrGenerateKey(t.TempDir(), "test-device")
	if err != nil {
		t.Fatalf("LoadOrGenerateKey: %v", err)
	}
	return store, key
}

// seedFinalCheckpoint writes a "shutdown"-reason checkpoint for the
// given repo + session into the store. Returns the appended hash.
func seedFinalCheckpoint(t *testing.T, store *memory.Store, key *memory.DeviceKey,
	repo, sessionID, model, summary string, planMode bool, turn int,
	files []string,
) string {
	t.Helper()
	cp := &memory.Checkpoint{
		Version:      2,
		ParentHash:   strings.Repeat("0", 64),
		DeviceID:     key.DeviceID,
		AuthorID:     key.DeviceID,
		Timestamp:    time.Now().UnixNano(),
		SessionID:    sessionID,
		Turn:         turn,
		ActiveModel:  model,
		PlanMode:     planMode,
		Summary:      summary,
		FilesTouched: files,
		RepoRemote:   repo,
	}
	memory.SignCheckpoint(cp, key.PrivateKey)
	if err := store.Append(cp); err != nil {
		t.Fatalf("Append: %v", err)
	}
	return cp.Hash
}

func newResumeSession(t *testing.T, store *memory.Store, key *memory.DeviceKey,
	resume bool, repoRemote string, model string,
) (*Session, []string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	t.Cleanup(server.Close)
	cfg := testConfig(server.URL)
	if model == "" {
		model = "m25"
	}
	var out []string
	sess, err := NewSession(SessionConfig{
		Cfg:           cfg,
		Store:         store,
		DeviceKey:     key,
		ModelFlag:     model,
		Resume:        resume,
		RepoRemote:    repoRemote,
		Workdir:       t.TempDir(),
		SessionID:     "new-session-" + sessionIDSuffix(t),
		Output:        func(msg string) { out = append(out, msg) },
		DisableEngine: true,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return sess, out
}

func sessionIDSuffix(t *testing.T) string {
	t.Helper()
	return t.Name()
}

// TestResume_BackfillsPreviousCheckpoint verifies that --resume
// injects a system message containing the prior checkpoint's summary
// + files-touched.
func TestResume_BackfillsPreviousCheckpoint(t *testing.T) {
	store, key := newResumeTestStore(t)
	const repo = "https://example.com/repo.git"
	seedFinalCheckpoint(t, store, key, repo,
		"dev1-1713100000000000000", "m25",
		"Refactored the stream client retry logic. Tests passing.",
		false, 15,
		[]string{"stream/client.go", "stream/client_test.go"},
	)

	sess, out := newResumeSession(t, store, key, true, repo, "m25")
	// Last system-role message should carry the backfill.
	msgs := sess.ctxManager.Messages()
	var found *types.Message
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "system" && strings.Contains(msgs[i].Content, "Resuming from previous session") {
			found = &msgs[i]
			break
		}
	}
	if found == nil {
		t.Fatal("backfill system message missing")
	}
	if !strings.Contains(found.Content, "Refactored the stream client retry logic") {
		t.Errorf("summary missing from backfill: %q", found.Content)
	}
	if !strings.Contains(found.Content, "stream/client.go") {
		t.Errorf("files-touched missing from backfill: %q", found.Content)
	}
	// Operator-facing notice was emitted.
	want := "resumed from previous session"
	if !containsAny(out, want) {
		t.Errorf("expected output containing %q; got %v", want, out)
	}
}

// TestResume_NoPreviousStartsFresh verifies the no-previous-checkpoint
// fallback path: a warning is emitted; no backfill.
func TestResume_NoPreviousStartsFresh(t *testing.T) {
	store, key := newResumeTestStore(t)
	sess, out := newResumeSession(t, store, key, true,
		"https://example.com/fresh.git", "m25")

	msgs := sess.ctxManager.Messages()
	for _, m := range msgs {
		if strings.Contains(m.Content, "Resuming from previous session") {
			t.Errorf("unexpected backfill content: %q", m.Content)
		}
	}
	if !containsAny(out, "no previous session found") {
		t.Errorf("expected 'no previous session found' warning; got %v", out)
	}
}

// TestResume_WithoutFlagNoBackfill verifies that omitting --resume
// (Resume=false) means no checkpoint lookup or backfill happens.
func TestResume_WithoutFlagNoBackfill(t *testing.T) {
	store, key := newResumeTestStore(t)
	seedFinalCheckpoint(t, store, key, "https://example.com/repo.git",
		"dev1-prior", "m25", "should not appear", false, 10, nil)

	sess, _ := newResumeSession(t, store, key, false,
		"https://example.com/repo.git", "m25")
	for _, m := range sess.ctxManager.Messages() {
		if strings.Contains(m.Content, "Resuming from previous session") {
			t.Errorf("backfill leaked despite Resume=false: %q", m.Content)
		}
	}
}

// TestResume_SelectsLatestShutdown verifies LatestByRepo returns the
// most recent checkpoint when multiple shutdown checkpoints exist,
// and resume uses that one.
func TestResume_SelectsLatestShutdown(t *testing.T) {
	store, key := newResumeTestStore(t)
	const repo = "https://example.com/multi.git"
	seedFinalCheckpoint(t, store, key, repo, "dev1-older", "m25",
		"older session summary", false, 8, nil)
	time.Sleep(2 * time.Millisecond) // ensure newer timestamp distinct
	seedFinalCheckpoint(t, store, key, repo, "dev1-newer", "m25",
		"newer session summary", false, 15, nil)

	sess, _ := newResumeSession(t, store, key, true, repo, "m25")
	msgs := sess.ctxManager.Messages()
	for _, m := range msgs {
		if strings.Contains(m.Content, "older session summary") {
			t.Errorf("resume picked older checkpoint: %q", m.Content)
		}
	}
	found := false
	for _, m := range msgs {
		if strings.Contains(m.Content, "newer session summary") {
			found = true
			break
		}
	}
	if !found {
		t.Error("newer checkpoint summary not in backfill")
	}
}

// TestResume_PlanModeRestored verifies that a prior checkpoint with
// PlanMode=true sets the new session's PlanMode to true.
func TestResume_PlanModeRestored(t *testing.T) {
	store, key := newResumeTestStore(t)
	seedFinalCheckpoint(t, store, key, "https://example.com/plan.git",
		"dev1-plan", "m25", "in plan mode", true, 5, nil)
	sess, _ := newResumeSession(t, store, key, true,
		"https://example.com/plan.git", "m25")
	if !sess.PlanMode() {
		t.Error("plan mode not restored from checkpoint")
	}
}

// TestResume_RepoFilter verifies that resume only matches checkpoints
// for the current repo (LatestByRepo's purpose).
func TestResume_RepoFilter(t *testing.T) {
	store, key := newResumeTestStore(t)
	seedFinalCheckpoint(t, store, key, "https://example.com/other.git",
		"dev1-other", "m25", "other repo session", false, 8, nil)
	seedFinalCheckpoint(t, store, key, "https://example.com/me.git",
		"dev1-me", "m25", "MY repo session", false, 15, nil)

	sess, _ := newResumeSession(t, store, key, true,
		"https://example.com/me.git", "m25")
	msgs := sess.ctxManager.Messages()
	for _, m := range msgs {
		if strings.Contains(m.Content, "other repo session") {
			t.Errorf("cross-repo bleed: %q", m.Content)
		}
	}
}

// TestResume_ResumeRefSetForFirstCheckpoint verifies the new
// session's resumeRef is populated and matches the source's hash.
func TestResume_ResumeRefSetForFirstCheckpoint(t *testing.T) {
	store, key := newResumeTestStore(t)
	const repo = "https://example.com/ref.git"
	priorHash := seedFinalCheckpoint(t, store, key, repo,
		"dev1-source", "m25", "prior", false, 8, nil)

	sess, _ := newResumeSession(t, store, key, true, repo, "m25")
	if sess.resumeRef == nil {
		t.Fatal("resumeRef nil after resume")
	}
	if sess.resumeRef.SessionID != "dev1-source" {
		t.Errorf("resumeRef.SessionID = %q; want dev1-source",
			sess.resumeRef.SessionID)
	}
	if sess.resumeRef.CheckpointHash != priorHash {
		t.Errorf("resumeRef.CheckpointHash mismatch:\n got %s\nwant %s",
			sess.resumeRef.CheckpointHash, priorHash)
	}
}

func containsAny(out []string, want string) bool {
	for _, s := range out {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}
