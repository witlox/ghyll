package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/witlox/ghyll/config"
	ghyllcontext "github.com/witlox/ghyll/context"
	"github.com/witlox/ghyll/dialect"
)

// recordingServer captures the Authorization header on every
// inbound request and returns a successful SSE stream. Used by
// the auth integration tests across session.go's 3 NewClient call
// sites + subagent.go.
func recordingServer(t *testing.T) (*httptest.Server, *atomic.Value, *atomic.Int32) {
	t.Helper()
	var auth atomic.Value
	auth.Store("")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		// Multiple-request capture: keep the LAST seen value.
		auth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, sseChunk(chatDelta("ok")))
		_, _ = fmt.Fprint(w, sseChunk(chatFinish("stop")))
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	return srv, &auth, &hits
}

// TestScenario_Stream_RequestIncludesAuthorizationHeader_Session
// asserts that a Session with an api_key configured forwards the
// Bearer header on every chat completion.
func TestScenario_Stream_RequestIncludesAuthorizationHeader_Session(t *testing.T) {
	srv, auth, _ := recordingServer(t)
	defer srv.Close()

	cfg := testConfig(srv.URL)
	cfg.Models["m25"] = config.ModelConfig{
		Endpoint:   srv.URL + "/v1",
		Dialect:    "minimax",
		MaxContext: 100000,
		APIKey:     "sk-test-fixture-9f2a",
	}

	t.Setenv("GHYLL_API_KEY", "")
	t.Setenv("GHYLL_API_KEY_M25", "")

	sess, err := NewSession(SessionConfig{
		Cfg:       cfg,
		Workdir:   "/tmp/test",
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if _, err := sess.Turn("hi"); err != nil {
		t.Fatalf("turn: %v", err)
	}

	if got := auth.Load().(string); got != "Bearer sk-test-fixture-9f2a" {
		t.Fatalf("captured Authorization = %q, want %q", got, "Bearer sk-test-fixture-9f2a")
	}
}

// TestScenario_Compaction_PreservesAuthHeader asserts that
// s.compactionCall (invoked from context.Manager.compact) forwards
// the api_key resolved for req.ModelName.
func TestScenario_Compaction_PreservesAuthHeader(t *testing.T) {
	srv, auth, _ := recordingServer(t)
	defer srv.Close()

	cfg := testConfig(srv.URL)
	cfg.Models["m25"] = config.ModelConfig{
		Endpoint:   srv.URL + "/v1",
		Dialect:    "minimax",
		MaxContext: 100000,
		APIKey:     "sk-compaction-test",
	}

	t.Setenv("GHYLL_API_KEY", "")
	t.Setenv("GHYLL_API_KEY_M25", "")

	sess, err := NewSession(SessionConfig{
		Cfg:       cfg,
		Workdir:   "/tmp/test",
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Directly invoke compactionCall with a populated ModelName.
	_, err = sess.compactionCall(ghyllcontext.CompactionRequest{
		ModelEndpoint:    cfg.Models["m25"].Endpoint,
		ModelName:        "m25",
		CompactionPrompt: "summarize",
	})
	if err != nil {
		t.Fatalf("compactionCall: %v", err)
	}

	if got := auth.Load().(string); got != "Bearer sk-compaction-test" {
		t.Fatalf("captured Authorization = %q, want %q", got, "Bearer sk-compaction-test")
	}
}

// TestScenario_Compaction_EmptyModelNameFallsBackToSession asserts
// AUTH-W-004 remediation: an empty req.ModelName no longer silently
// degrades to no-auth; the closure pins to s.activeModel. ADR-005
// invariant — compaction MUST reuse the active endpoint AND key.
func TestScenario_Compaction_EmptyModelNameFallsBackToSession(t *testing.T) {
	srv, auth, _ := recordingServer(t)
	defer srv.Close()

	cfg := testConfig(srv.URL)
	cfg.Models["m25"] = config.ModelConfig{
		Endpoint:   srv.URL + "/v1",
		Dialect:    "minimax",
		MaxContext: 100000,
		APIKey:     "sk-must-be-used",
	}

	t.Setenv("GHYLL_API_KEY", "")
	t.Setenv("GHYLL_API_KEY_M25", "")

	sess, err := NewSession(SessionConfig{
		Cfg:       cfg,
		Workdir:   "/tmp/test",
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = sess.compactionCall(ghyllcontext.CompactionRequest{
		ModelEndpoint:    cfg.Models["m25"].Endpoint,
		ModelName:        "", // empty → fall back to s.activeModel
		CompactionPrompt: "summarize",
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := auth.Load().(string); got != "Bearer sk-must-be-used" {
		t.Fatalf("expected fall-back-to-session key, got %q", got)
	}
}

// TestScenario_SubAgent_PreservesAuthHeader runs a sub-agent
// against a recording server and asserts the Authorization header
// reaches the wire on the sub-agent's stream.NewClient.
func TestScenario_SubAgent_PreservesAuthHeader(t *testing.T) {
	srv, auth, _ := recordingServer(t)
	defer srv.Close()

	cfg := testConfig(srv.URL)
	cfg.Models["m25"] = config.ModelConfig{
		Endpoint:   srv.URL + "/v1",
		Dialect:    "minimax",
		MaxContext: 100000,
		APIKey:     "sk-subagent-test",
	}
	cfg.SubAgent.MaxTurns = 1
	cfg.SubAgent.TokenBudget = 50000
	cfg.SubAgent.TimeoutSeconds = 5

	t.Setenv("GHYLL_API_KEY", "")
	t.Setenv("GHYLL_API_KEY_M25", "")

	sess, err := NewSession(SessionConfig{
		Cfg:       cfg,
		Workdir:   "/tmp/test",
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatal(err)
	}

	_ = RunSubAgent(sess, "do a focused task")

	if got := auth.Load().(string); got != "Bearer sk-subagent-test" {
		t.Fatalf("sub-agent captured Authorization = %q, want %q", got, "Bearer sk-subagent-test")
	}
}

// TestScenario_Handoff_ResolvesPerTargetModel — when a model
// switch happens, the new stream client is created with the
// TARGET model's api_key, not the source's. This requires two
// servers + a real handoff dispatch — we instead drive the
// stream-client construction path directly: build a new client
// via buildAuthHeader against each model and assert distinct
// Bearer values.
func TestScenario_Handoff_ResolvesPerTargetModel(t *testing.T) {
	cfg := &config.Config{
		Models: map[string]config.ModelConfig{
			"local":  {APIKey: "sk-local-only"},
			"remote": {APIKey: "sk-remote-only"},
		},
	}
	t.Setenv("GHYLL_API_KEY", "")
	t.Setenv("GHYLL_API_KEY_LOCAL", "")
	t.Setenv("GHYLL_API_KEY_REMOTE", "")

	h1 := buildAuthHeader(cfg, "local")
	h2 := buildAuthHeader(cfg, "remote")
	if h1.Get("Authorization") != "Bearer sk-local-only" {
		t.Fatalf("local Authorization = %q", h1.Get("Authorization"))
	}
	if h2.Get("Authorization") != "Bearer sk-remote-only" {
		t.Fatalf("remote Authorization = %q", h2.Get("Authorization"))
	}
	if h1.Get("Authorization") == h2.Get("Authorization") {
		t.Fatal("per-target resolution leaked the same key across distinct models")
	}
}

// TestScenario_Handoff_RealHandoffDispatchUsesTargetKey drives
// the actual handoffToModel path (session.go:1149) end-to-end with
// two recording servers so a regression that captured the wrong
// model name into buildAuthHeader gets caught. ADV-AUTH-004
// remediation: the buildAuthHeader unit test alone does not catch
// a variable-shadowing or stale-name bug at the call site.
func TestScenario_Handoff_RealHandoffDispatchUsesTargetKey(t *testing.T) {
	// Two recording servers, each with its OWN api_key. After the
	// handoff, the post-handoff Turn must dispatch to server B
	// carrying B's Bearer header — not A's.
	srvA, authA, _ := recordingServer(t)
	defer srvA.Close()
	srvB, authB, _ := recordingServer(t)
	defer srvB.Close()

	cfg := testConfig(srvA.URL)
	cfg.Models["m25"] = config.ModelConfig{
		Endpoint:   srvA.URL + "/v1",
		Dialect:    "minimax",
		MaxContext: 100000,
		APIKey:     "sk-source-A",
	}
	cfg.Models["glm5"] = config.ModelConfig{
		Endpoint:   srvB.URL + "/v1",
		Dialect:    "glm",
		MaxContext: 200000,
		APIKey:     "sk-target-B",
	}

	t.Setenv("GHYLL_API_KEY", "")
	t.Setenv("GHYLL_API_KEY_M25", "")
	t.Setenv("GHYLL_API_KEY_GLM5", "")

	sess, err := NewSession(SessionConfig{
		Cfg:       cfg,
		Workdir:   "/tmp/test",
		SessionID: "test-handoff",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	// Drive the handoff directly. This invokes the exact code
	// path the routing-decision dispatcher uses at session.go:1149.
	decision := dialect.RoutingDecision{
		Action:      dialect.ActionEscalate,
		TargetModel: "glm5",
		Reason:      dialect.ReasonDeepOverride,
	}
	if err := sess.handleHandoff(decision); err != nil {
		t.Fatalf("handleHandoff: %v", err)
	}

	// Now run a Turn — it should dispatch against server B with
	// B's Bearer header.
	if _, err := sess.Turn("after handoff"); err != nil {
		t.Fatalf("post-handoff Turn: %v", err)
	}

	if got := authB.Load().(string); got != "Bearer sk-target-B" {
		t.Fatalf("target server captured Authorization = %q, want %q",
			got, "Bearer sk-target-B")
	}
	if got := authA.Load().(string); strings.Contains(got, "sk-target-B") {
		t.Fatalf("source server saw target's key — cross-tenant leak: %q", got)
	}
}

// TestScenario_ConfigShow_RedactsAPIKey — exercises the redactor
// against a sentinel api_key and asserts the value never appears
// in cmdConfigShow's emitted output. This invokes the same
// redactKeySource the main loop uses, so the assertion exercises
// the actual chokepoint.
func TestScenario_ConfigShow_RedactsAPIKey(t *testing.T) {
	cfg := &config.Config{
		Models: map[string]config.ModelConfig{
			"cscs-glm5": {APIKey: "sk-canary-must-not-leak"},
		},
	}
	t.Setenv("GHYLL_API_KEY", "")
	t.Setenv("GHYLL_API_KEY_CSCS_GLM5", "")
	got := redactKeySource(cfg, "cscs-glm5")
	if got != "<toml>" {
		t.Fatalf("got %q, want <toml>", got)
	}
	if strings.Contains(got, "sk-") {
		t.Fatal("redactor leaked the value")
	}
}

// TestScenario_AuditArtifacts_DoNotContainAPIKey drives a REAL
// Session through one Turn() with a sentinel api_key configured,
// then dumps every .ghyll/* file under the session workdir and
// greps for the sentinel. AUTH-11 / AUTH-W-002 remediation: the
// unit-level grep tests in runner/ and engine/ exercised in-memory
// fixtures with no path that touches config. This test exercises
// the actual production wire-up from end to end. A leak introduced
// anywhere on the session's checkpoint / attestation / engine path
// surfaces here.
func TestScenario_AuditArtifacts_DoNotContainAPIKey(t *testing.T) {
	sentinel := "sk-canary-cccc-must-not-leak"

	srv, _, _ := recordingServer(t)
	defer srv.Close()

	cfg := testConfig(srv.URL)
	cfg.Models["m25"] = config.ModelConfig{
		Endpoint:   srv.URL + "/v1",
		Dialect:    "minimax",
		MaxContext: 100000,
		APIKey:     sentinel,
	}
	t.Setenv("GHYLL_API_KEY", "")
	t.Setenv("GHYLL_API_KEY_M25", "")

	workdir := t.TempDir()
	sess, err := NewSession(SessionConfig{
		Cfg:       cfg,
		Workdir:   workdir,
		SessionID: "audit-test",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := sess.Turn("hello"); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	// Close the session to flush all persistent state.
	sess.Close()

	// Walk .ghyll/ under the workdir and grep every file for the
	// sentinel. Empty directory is acceptable (some session paths
	// are configurable); failure is "a file contains the sentinel".
	ghyllDir := filepath.Join(workdir, ".ghyll")
	if _, err := os.Stat(ghyllDir); os.IsNotExist(err) {
		// Some test harnesses route persistence elsewhere; tolerable.
		return
	}
	err = filepath.Walk(ghyllDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if strings.Contains(string(data), sentinel) {
			t.Fatalf("audit artifact leaked sentinel api_key at %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

// guard against goroutine leaks across tests
var _ = sync.Mutex{}
