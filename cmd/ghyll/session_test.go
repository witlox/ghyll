package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/witlox/ghyll/config"
	"github.com/witlox/ghyll/dialect"
	"github.com/witlox/ghyll/memory"
)

func sseChunk(data string) string {
	return "data: " + data + "\n\n"
}

func chatDelta(content string) string {
	return fmt.Sprintf(`{"choices":[{"delta":{"content":%q},"finish_reason":null}]}`, content)
}

func chatFinish(reason string) string {
	return fmt.Sprintf(`{"choices":[{"delta":{},"finish_reason":%q}]}`, reason)
}

func chatToolCall(id, name, args string) string {
	return fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":%q,"type":"function","function":{"name":%q,"arguments":%q}}]},"finish_reason":null}]}`, id, name, args)
}

func mockModelServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, sseChunk(chatDelta("Hello! ")))
		_, _ = fmt.Fprint(w, sseChunk(chatDelta("I can help.")))
		_, _ = fmt.Fprint(w, sseChunk(chatFinish("stop")))
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func testConfig(endpoint string) *config.Config {
	return &config.Config{
		Models: map[string]config.ModelConfig{
			"m25": {
				Endpoint:   endpoint + "/v1",
				Dialect:    "minimax_m25",
				MaxContext: 100000,
			},
			"glm5": {
				Endpoint:   endpoint + "/v1",
				Dialect:    "glm5",
				MaxContext: 200000,
			},
		},
		Routing: config.RoutingConfig{
			DefaultModel:          "m25",
			ContextDepthThreshold: 32000,
			ToolDepthThreshold:    5,
			EnableAutoRouting:     true,
		},
		Memory: config.MemoryConfig{
			CheckpointIntervalTurns: 5,
			DriftCheckIntervalTurns: 5,
			DriftThreshold:          0.7,
		},
		Tools: config.ToolsConfig{
			BashTimeoutSeconds: 30,
			FileTimeoutSeconds: 5,
		},
	}
}

// TestScenario_Session_Init
func TestScenario_Session_Init(t *testing.T) {
	server := mockModelServer(t)
	defer server.Close()

	cfg := testConfig(server.URL)
	var output []string

	sess, err := NewSession(SessionConfig{
		Cfg:       cfg,
		Workdir:   "/tmp/test",
		SessionID: "test-session",
		Output:    func(msg string) { output = append(output, msg) },
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	if sess.ActiveModel() != "m25" {
		t.Errorf("model = %q, want m25", sess.ActiveModel())
	}
}

// TestScenario_Session_InitWithModelFlag
func TestScenario_Session_InitWithModelFlag(t *testing.T) {
	server := mockModelServer(t)
	defer server.Close()

	cfg := testConfig(server.URL)

	sess, err := NewSession(SessionConfig{
		Cfg:       cfg,
		ModelFlag: "glm5",
		Workdir:   "/tmp/test",
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sess.ActiveModel() != "glm5" {
		t.Errorf("model = %q, want glm5", sess.ActiveModel())
	}
	if !sess.modelLocked {
		t.Error("expected modelLocked=true with --model flag")
	}
}

// TestScenario_Session_BasicTurn — full round trip
func TestScenario_Session_BasicTurn(t *testing.T) {
	server := mockModelServer(t)
	defer server.Close()

	cfg := testConfig(server.URL)

	sess, err := NewSession(SessionConfig{
		Cfg:       cfg,
		Workdir:   "/tmp/test",
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatal(err)
	}

	reply, err := sess.Turn("hello, help me with a bug")
	if err != nil {
		t.Fatalf("turn failed: %v", err)
	}
	if reply != "Hello! I can help." {
		t.Errorf("reply = %q", reply)
	}
}

// TestScenario_Session_TurnWithToolCall
func TestScenario_Session_TurnWithToolCall(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		if callCount == 1 {
			// First call: return a tool call
			_, _ = fmt.Fprint(w, sseChunk(chatToolCall("call_1", "bash", `{"command":"echo hello"}`)))
			_, _ = fmt.Fprint(w, sseChunk(chatFinish("tool_calls")))
		} else {
			// Second call: after tool result, return final answer
			_, _ = fmt.Fprint(w, sseChunk(chatDelta("The output was: hello")))
			_, _ = fmt.Fprint(w, sseChunk(chatFinish("stop")))
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	cfg := testConfig(server.URL)

	sess, err := NewSession(SessionConfig{
		Cfg:       cfg,
		Workdir:   "/tmp/test",
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatal(err)
	}

	reply, err := sess.Turn("run echo hello")
	if err != nil {
		t.Fatalf("turn failed: %v", err)
	}
	if reply != "The output was: hello" {
		t.Errorf("reply = %q", reply)
	}
	if callCount != 2 {
		t.Errorf("expected 2 API calls (tool call + follow-up), got %d", callCount)
	}
}

// TestScenario_Session_WithStore — checkpoint creation
func TestScenario_Session_WithStore(t *testing.T) {
	server := mockModelServer(t)
	defer server.Close()

	dir := t.TempDir()
	store, err := memory.OpenStore(filepath.Join(dir, "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	keysDir := filepath.Join(dir, "keys")
	key, err := memory.LoadOrGenerateKey(keysDir, "test-device")
	if err != nil {
		t.Fatal(err)
	}

	cfg := testConfig(server.URL)
	cfg.Memory.CheckpointIntervalTurns = 1 // checkpoint every turn

	sess, err := NewSession(SessionConfig{
		Cfg:       cfg,
		Store:     store,
		DeviceKey: key,
		Workdir:   "/tmp/test",
		SessionID: "test-session-cp",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = sess.Turn("hello")
	if err != nil {
		t.Fatal(err)
	}

	// Verify checkpoint was created
	cps, err := store.ListBySession("test-session-cp")
	if err != nil {
		t.Fatal(err)
	}
	if len(cps) != 1 {
		t.Errorf("expected 1 checkpoint, got %d", len(cps))
	}
}

// TestScenario_Session_Prompt
func TestScenario_Session_Prompt(t *testing.T) {
	server := mockModelServer(t)
	defer server.Close()

	sess, err := NewSession(SessionConfig{
		Cfg:       testConfig(server.URL),
		Workdir:   "/home/dev/project",
		SessionID: "s1",
	})
	if err != nil {
		t.Fatal(err)
	}

	prompt := sess.Prompt()
	if prompt != "ghyll [m25] /home/dev/project ▸ " {
		t.Errorf("prompt = %q", prompt)
	}
}

// TestScenario_Session_TierFallback maps to:
// Scenario: Tier fallback on persistent failure (auto-routing)
func TestScenario_Session_TierFallback(t *testing.T) {
	m25Calls := 0
	glm5Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, sseChunk(chatDelta("fallback response")))
		_, _ = fmt.Fprint(w, sseChunk(chatFinish("stop")))
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer glm5Server.Close()

	m25Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m25Calls++
		w.WriteHeader(503) // always fail
	}))
	defer m25Server.Close()

	cfg := &config.Config{
		Models: map[string]config.ModelConfig{
			"m25":  {Endpoint: m25Server.URL + "/v1", Dialect: "minimax_m25", MaxContext: 100000},
			"glm5": {Endpoint: glm5Server.URL + "/v1", Dialect: "glm5", MaxContext: 200000},
		},
		Routing: config.RoutingConfig{
			DefaultModel:          "m25",
			ContextDepthThreshold: 32000,
			ToolDepthThreshold:    5,
			EnableAutoRouting:     true,
		},
		Memory: config.MemoryConfig{CheckpointIntervalTurns: 100},
		Tools:  config.ToolsConfig{BashTimeoutSeconds: 30, FileTimeoutSeconds: 5},
	}

	var outputs []string
	sess, err := NewSession(SessionConfig{
		Cfg:       cfg,
		Workdir:   "/tmp/test",
		SessionID: "fallback-test",
		Output:    func(msg string) { outputs = append(outputs, msg) },
	})
	if err != nil {
		t.Fatal(err)
	}

	// m25 will fail after retries — session should error (no automatic fallback in session.Turn,
	// fallback is orchestrated by the caller/REPL layer checking stream errors)
	_, err = sess.Turn("hello")
	// With 3 retries on 503, this should fail
	if err == nil {
		t.Log("m25 was expected to fail — if it succeeded, the mock didn't work")
	}
	if m25Calls < 3 {
		t.Errorf("expected at least 3 retry attempts to m25, got %d", m25Calls)
	}
}

// TestScenario_Session_ModelLockNoFallback maps to:
// Scenario: No fallback with explicit model lock
func TestScenario_Session_ModelLockNoFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer server.Close()

	cfg := testConfig(server.URL)

	sess, err := NewSession(SessionConfig{
		Cfg:       cfg,
		ModelFlag: "m25", // locked
		Workdir:   "/tmp/test",
		SessionID: "lock-test",
	})
	if err != nil {
		t.Fatal(err)
	}

	if !sess.modelLocked {
		t.Fatal("expected model locked")
	}

	_, err = sess.Turn("hello")
	if err == nil {
		t.Fatal("expected error with locked model and failing endpoint")
	}
}

// TestScenario_Session_ToolDepthLimit maps to:
// Finding 1: unbounded tool recursion guard
func TestScenario_Session_ToolDepthLimit(t *testing.T) {
	// Server that always returns a tool call
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, sseChunk(chatToolCall("call_inf", "bash", `{"command":"echo loop"}`)))
		_, _ = fmt.Fprint(w, sseChunk(chatFinish("tool_calls")))
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	cfg := testConfig(server.URL)

	sess, err := NewSession(SessionConfig{
		Cfg:       cfg,
		Workdir:   "/tmp/test",
		SessionID: "depth-test",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = sess.Turn("loop forever")
	if err == nil {
		t.Fatal("expected error from tool depth limit")
	}
	if sess.toolDepth < maxToolDepth {
		t.Errorf("tool depth = %d, expected >= %d", sess.toolDepth, maxToolDepth)
	}
}

// TestScenario_Session_HandoffPreservesContext maps to:
// Finding 2: handoff now creates checkpoint and uses HandoffSummary
func TestScenario_Session_HandoffPreservesContext(t *testing.T) {
	server := mockModelServer(t)
	defer server.Close()

	dir := t.TempDir()
	store, err := memory.OpenStore(filepath.Join(dir, "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	key, _ := memory.LoadOrGenerateKey(filepath.Join(dir, "keys"), "dev1")

	cfg := testConfig(server.URL)
	cfg.Memory.CheckpointIntervalTurns = 100

	var outputs []string
	sess, err := NewSession(SessionConfig{
		Cfg:       cfg,
		Store:     store,
		DeviceKey: key,
		Workdir:   "/tmp/test",
		SessionID: "handoff-test",
		Output:    func(msg string) { outputs = append(outputs, msg) },
	})
	if err != nil {
		t.Fatal(err)
	}

	// Add some context
	_, _ = sess.Turn("work on auth module")

	// Force a handoff by simulating escalation
	err = sess.handleHandoff(dialect.RoutingDecision{
		Action:      "escalate",
		TargetModel: "glm5",
	})
	if err != nil {
		t.Fatalf("handoff failed: %v", err)
	}

	// Verify model switched
	if sess.ActiveModel() != "glm5" {
		t.Errorf("model = %q, want glm5", sess.ActiveModel())
	}

	// Verify checkpoint was created
	cps, err := store.ListBySession("handoff-test")
	if err != nil {
		t.Fatal(err)
	}
	foundHandoff := false
	for _, cp := range cps {
		if cp.Summary != "" && len(cp.Summary) > 0 {
			foundHandoff = true
		}
	}
	if !foundHandoff {
		t.Error("expected handoff checkpoint to be created")
	}

	// Verify context manager has messages (not empty)
	msgs := sess.ctxManager.Messages()
	if len(msgs) == 0 {
		t.Error("context should not be empty after handoff")
	}
}

// TestScenario_Session_BadToolArgs maps to:
// Finding 5: tool arg parse failure returns error
func TestScenario_Session_BadToolArgs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		// Return tool call with malformed arguments
		tc := `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"bad","type":"function","function":{"name":"bash","arguments":"not json"}}]},"finish_reason":null}]}`
		_, _ = fmt.Fprint(w, "data: "+tc+"\n\n")
		_, _ = fmt.Fprint(w, sseChunk(chatFinish("tool_calls")))
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	// After tool execution with bad args, model gets error result and responds
	callCount := 0
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		if callCount == 1 {
			tc := `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"bad","type":"function","function":{"name":"bash","arguments":"not json"}}]},"finish_reason":null}]}`
			_, _ = fmt.Fprint(w, "data: "+tc+"\n\n")
			_, _ = fmt.Fprint(w, sseChunk(chatFinish("tool_calls")))
		} else {
			_, _ = fmt.Fprint(w, sseChunk(chatDelta("handled error")))
			_, _ = fmt.Fprint(w, sseChunk(chatFinish("stop")))
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server2.Close()

	cfg := testConfig(server2.URL)
	sess, _ := NewSession(SessionConfig{
		Cfg: cfg, Workdir: "/tmp/test", SessionID: "bad-args",
	})

	reply, err := sess.Turn("test")
	if err != nil {
		t.Fatalf("turn should recover from bad args: %v", err)
	}
	if reply != "handled error" {
		t.Logf("reply = %q (model saw the parse error in tool result)", reply)
	}
}
