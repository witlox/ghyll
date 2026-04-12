package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/witlox/ghyll/config"
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
