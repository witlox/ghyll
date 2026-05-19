package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newDispatchSession returns a minimal *Session for slash-command
// dispatch tests. Uses an httptest server for the model endpoint so
// NewSession succeeds; the stream client is never actually invoked
// by DispatchSlashCommand.
func newDispatchSession(t *testing.T) *Session {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	t.Cleanup(server.Close)
	cfg := testConfig(server.URL)
	sess, err := NewSession(SessionConfig{
		Cfg:           cfg,
		Workdir:       t.TempDir(),
		SessionID:     "dispatch-test",
		DisableEngine: true,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return sess
}

func TestDispatchSlashCommand_PlanActivates(t *testing.T) {
	sess := newDispatchSession(t)
	if sess.PlanMode() {
		t.Fatal("plan mode should start inactive")
	}
	res := sess.DispatchSlashCommand("/plan")
	if !res.Handled {
		t.Fatal("/plan not handled")
	}
	if !sess.PlanMode() {
		t.Error("plan mode did not activate")
	}
	if !strings.Contains(res.Output, "plan mode activated") {
		t.Errorf("unexpected output: %q", res.Output)
	}
}

func TestDispatchSlashCommand_PlanIdempotent(t *testing.T) {
	sess := newDispatchSession(t)
	sess.SetPlanMode(true)
	res := sess.DispatchSlashCommand("/plan")
	if !res.Handled {
		t.Fatal("/plan not handled")
	}
	if !sess.PlanMode() {
		t.Error("plan mode flipped off on second /plan")
	}
	if !strings.Contains(res.Output, "already active") {
		t.Errorf("expected 'already active' message; got %q", res.Output)
	}
}

func TestDispatchSlashCommand_FastClearsBoth(t *testing.T) {
	sess := newDispatchSession(t)
	sess.SetPlanMode(true)
	sess.SetDeepOverride(true)
	res := sess.DispatchSlashCommand("/fast")
	if !res.Handled {
		t.Fatal("/fast not handled")
	}
	if sess.PlanMode() {
		t.Error("plan mode still active after /fast")
	}
	if sess.DeepOverride() {
		t.Error("deep override still active after /fast")
	}
}

func TestDispatchSlashCommand_DeepActivates(t *testing.T) {
	sess := newDispatchSession(t)
	res := sess.DispatchSlashCommand("/deep")
	if !res.Handled {
		t.Fatal("/deep not handled")
	}
	if !sess.DeepOverride() {
		t.Error("deep override did not activate")
	}
}

func TestDispatchSlashCommand_DeepIgnoredWhenLocked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	t.Cleanup(server.Close)
	cfg := testConfig(server.URL)
	sess, err := NewSession(SessionConfig{
		Cfg:           cfg,
		ModelFlag:     "glm5",
		Workdir:       t.TempDir(),
		SessionID:     "locked",
		DisableEngine: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sess.ModelLocked() {
		t.Fatal("ModelFlag should have set ModelLocked")
	}
	res := sess.DispatchSlashCommand("/deep")
	if sess.DeepOverride() {
		t.Error("deep override activated despite model lock")
	}
	if !strings.Contains(res.Output, "ignored, model locked") {
		t.Errorf("expected 'ignored, model locked' output; got %q", res.Output)
	}
}

func TestDispatchSlashCommand_FastIgnoredWhenLocked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	t.Cleanup(server.Close)
	cfg := testConfig(server.URL)
	sess, err := NewSession(SessionConfig{
		Cfg:           cfg,
		ModelFlag:     "m25",
		Workdir:       t.TempDir(),
		SessionID:     "locked2",
		DisableEngine: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	sess.SetPlanMode(true)
	res := sess.DispatchSlashCommand("/fast")
	// Plan mode SHOULD NOT clear when locked — the /fast handler
	// refuses the entire operation under lock.
	if !sess.PlanMode() {
		t.Error("plan mode cleared despite model lock")
	}
	if !strings.Contains(res.Output, "ignored, model locked") {
		t.Errorf("expected 'ignored, model locked'; got %q", res.Output)
	}
}

func TestDispatchSlashCommand_Status(t *testing.T) {
	sess := newDispatchSession(t)
	sess.SetPlanMode(true)
	res := sess.DispatchSlashCommand("/status")
	if !res.Handled {
		t.Fatal("/status not handled")
	}
	if !strings.Contains(res.Output, "plan: true") {
		t.Errorf("status output missing plan:true; got %q", res.Output)
	}
	if !strings.Contains(res.Output, "model: m25") {
		t.Errorf("status output missing model line; got %q", res.Output)
	}
}

func TestDispatchSlashCommand_ExitRequested(t *testing.T) {
	sess := newDispatchSession(t)
	res := sess.DispatchSlashCommand("/exit")
	if !res.Handled || !res.ExitRequested {
		t.Errorf("expected Handled+ExitRequested; got %+v", res)
	}
}

func TestDispatchSlashCommand_UnknownSlashNotHandled(t *testing.T) {
	sess := newDispatchSession(t)
	res := sess.DispatchSlashCommand("/notarealcommand")
	if res.Handled {
		t.Error("unknown command should NOT be handled by built-in dispatcher")
	}
}

func TestDispatchSlashCommand_PlainTextNotHandled(t *testing.T) {
	sess := newDispatchSession(t)
	res := sess.DispatchSlashCommand("hello world")
	if res.Handled {
		t.Error("plain text should not be handled by slash dispatcher")
	}
}

func TestComposedSystemPrompt_PlanModeOverlay(t *testing.T) {
	sess := newDispatchSession(t)
	basePrompt := sess.ComposedSystemPrompt()
	sess.SetPlanMode(true)
	planPrompt := sess.ComposedSystemPrompt()
	if planPrompt == basePrompt {
		t.Error("plan mode did not change the system prompt")
	}
	if !strings.Contains(planPrompt, basePrompt) {
		t.Error("plan-mode prompt should APPEND to base; lost base content")
	}
}
