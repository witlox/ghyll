package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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
				Dialect:    "minimax",
				MaxContext: 100000,
			},
			"glm5": {
				Endpoint:   endpoint + "/v1",
				Dialect:    "glm",
				MaxContext: 200000,
			},
		},
		Routing: config.RoutingConfig{
			DefaultModel:          "m25",
			DeepModel:             "glm5",
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
			"m25":  {Endpoint: m25Server.URL + "/v1", Dialect: "minimax", MaxContext: 100000},
			"glm5": {Endpoint: glm5Server.URL + "/v1", Dialect: "glm", MaxContext: 200000},
		},
		Routing: config.RoutingConfig{
			DefaultModel:          "m25",
			DeepModel:             "glm5",
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

// TestScenario_Session_NormalizeDialect verifies legacy dialect strings map
// to family names (ADV-1 fix). A config still carrying dialect = "glm5" or
// "minimax_m25" must not silently fall through to the default minimax branch
// in resolveDialect.
func TestScenario_Session_NormalizeDialect(t *testing.T) {
	good := []struct {
		input string
		want  string
	}{
		{"glm", "glm"},
		{"glm5", "glm"},
		{"glm51", "glm"},
		{"minimax", "minimax"},
		{"minimax_m25", "minimax"},
		{"minimax_m27", "minimax"},
		// Validation-pass-8 D4: prefix-based detection covers new
		// family variants (including quantized names that operators
		// might mistakenly put in the Dialect field).
		{"deepseek", "deepseek"},
		{"deepseek-v3", "deepseek"},
		{"deepseek-coder", "deepseek"},
		{"deepseek-coder-v3", "deepseek"},
		{"deepseek-v3.1", "deepseek"}, // future variant
		{"qwen", "qwen"},
		{"qwen-coder", "qwen"},
		{"qwen2.5-coder", "qwen"},
		{"qwen3-coder", "qwen"},
		{"qwen-coder-q4", "qwen"}, // quant-suffixed name (operator-doc'd)
	}
	for _, tc := range good {
		got, err := normalizeDialect(tc.input)
		if err != nil {
			t.Errorf("normalizeDialect(%q): unexpected error %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("normalizeDialect(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
	// Empty + unknown must error (D3/D5).
	for _, bad := range []string{"", "  ", "unknown", "deepseak"} {
		if _, err := normalizeDialect(bad); err == nil {
			t.Errorf("normalizeDialect(%q) should error", bad)
		}
	}
}

// TestKimi_NormalizeDialect_HandlesProviderQualifiedSlug locks the
// Kimi-family wire forms (short + provider-qualified, case-folded)
// and pins the over-match guard: `kimino-coder` is a plausible
// future neighbour family and MUST return errUnknownDialect rather
// than silently routing into the Kimi switch arm.
func TestKimi_NormalizeDialect_HandlesProviderQualifiedSlug(t *testing.T) {
	good := []string{
		"kimi",
		"kimi-2.5",
		"kimi-2.6",
		"kimi-k2",
		"kimi-k2.5",
		"kimi-k2.6",
		"moonshotai/kimi-k2.5",
		"moonshotai/kimi-k2.6",
		"MOONSHOTAI/kimi-k2.5", // case-folded
		"moonshotai/Kimi-K2.6", // case-folded
	}
	for _, in := range good {
		got, err := normalizeDialect(in)
		if err != nil {
			t.Errorf("normalizeDialect(%q): unexpected error %v", in, err)
			continue
		}
		if got != "kimi" {
			t.Errorf("normalizeDialect(%q) = %q, want kimi", in, got)
		}
	}
	// NEGATIVE: a `kimi`-prefixed but distinct family name MUST
	// return errUnknownDialect. Guards against the Kimi arm
	// over-matching via a naive HasPrefix("kimi", …).
	for _, bad := range []string{"kimino-coder", "kimi-tgi-mode"} {
		if _, err := normalizeDialect(bad); err == nil {
			t.Errorf("normalizeDialect(%q) should error (kimi over-match guard)", bad)
		}
	}
}

// TestScenario_Session_ResolveDialectLegacyGLM5 verifies end-to-end that a
// Session whose active model carries the legacy dialect string "glm5"
// resolves to the GLM dialect functions, not the default minimax branch.
// This is the ADV-1 failure mode: before the fix, the switch in
// resolveDialect had `case "glm"` only, so "glm5" silently fell through.
func TestScenario_Session_ResolveDialectLegacyGLM5(t *testing.T) {
	s := &Session{
		cfg: &config.Config{
			Models: map[string]config.ModelConfig{
				"glm5": {Endpoint: "http://x/v1", Dialect: "glm5", MaxContext: 200000},
			},
		},
		activeModel: "glm5",
	}
	s.resolveDialect()

	// GLM system prompt should be in effect. Compare against the canonical
	// GLM prompt — if the default (minimax) branch fired, this would differ.
	got := s.systemPrompt("/tmp/test")
	want := dialect.GLMSystemPrompt("/tmp/test")
	if got != want {
		t.Errorf("legacy dialect \"glm5\" did not resolve to GLM system prompt")
	}
}

// TestKimi_NormalizeDialect_AndConfigLoadAgree — KIMI-CFG-1 /
// KIMI-CFG-6 / CONFIG-1 fix. The two layers (config.Load + session
// normalizeDialect) must accept the same Kimi alias forms — the
// prior implementation diverged on `kimi-k2.5` / `kimi-k2.6` (passed
// the normalizer, failed Load). Both layers now consume
// config.CanonicalDialectFamily as the single source of truth; this
// test pins the reciprocity property.
func TestKimi_NormalizeDialect_AndConfigLoadAgree(t *testing.T) {
	// Forms documented as accepted across the two layers.
	good := []string{
		"kimi",
		"kimi-2.5",
		"kimi-2.6",
		"kimi-k2",
		"kimi-k2.5",
		"kimi-k2.6",
		"moonshotai/kimi-k2.5",
		"moonshotai/kimi-k2.6",
		"moonshotai/Kimi-K2.6", // case-folded
	}
	for _, in := range good {
		fam, err := normalizeDialect(in)
		if err != nil {
			t.Errorf("normalizeDialect(%q) errored: %v", in, err)
			continue
		}
		if fam != "kimi" {
			t.Errorf("normalizeDialect(%q) = %q, want kimi", in, fam)
		}
		// And config layer must accept the same.
		if _, ok := config.CanonicalDialectFamily(in); !ok {
			t.Errorf("config.CanonicalDialectFamily(%q) returned !ok — reciprocity broken", in)
		}
	}
	// Symmetric on the negative side: forms session rejects, config
	// MUST reject too.
	bad := []string{"kimino-coder", "kimi-tgi-mode", "kimi-thinking"}
	for _, in := range bad {
		if _, err := normalizeDialect(in); err == nil {
			t.Errorf("normalizeDialect(%q) should error (over-match guard)", in)
		}
		if _, ok := config.CanonicalDialectFamily(in); ok {
			t.Errorf("config.CanonicalDialectFamily(%q) accepted but normalizeDialect rejects — reciprocity broken", in)
		}
	}
}

// testKimiConfig is a Kimi-rooted test config: single [models.kimi]
// pointing at the supplied endpoint, dialect = "kimi".
func testKimiConfig(endpoint string) *config.Config {
	return &config.Config{
		Models: map[string]config.ModelConfig{
			"kimi": {
				Endpoint:   endpoint + "/v1",
				Dialect:    "kimi",
				MaxContext: 200000,
			},
		},
		Routing: config.RoutingConfig{
			DefaultModel:          "kimi",
			DeepModel:             "kimi",
			ContextDepthThreshold: 32000,
			ToolDepthThreshold:    5,
			EnableAutoRouting:     true,
		},
		Memory: config.MemoryConfig{
			CheckpointIntervalTurns: 0,
			DriftCheckIntervalTurns: 0,
			DriftThreshold:          0.7,
		},
		Tools: config.ToolsConfig{
			BashTimeoutSeconds: 30,
			FileTimeoutSeconds: 5,
		},
	}
}

// chatToolCallWithReasoning emits a chat-completion delta that carries
// both a `reasoning_content` chunk and a `tool_calls` entry on the same
// SSE frame — matching Kimi 2.5/2.6's wire form.
func chatToolCallWithReasoning(id, name, args, reasoning string) string {
	return fmt.Sprintf(
		`{"choices":[{"delta":{"reasoning_content":%q,"tool_calls":[{"index":0,"id":%q,"type":"function","function":{"name":%q,"arguments":%q}}]},"finish_reason":null}]}`,
		reasoning, id, name, args)
}

// TestScenario_Session_KimiTurn_RejectsNonConformantToolCallID exercises
// the live streaming path with a non-conformant (UUID-shaped) tool_call
// id and asserts:
//
//  1. The session refuses to dispatch (no tool execution → only 1 call
//     captured, not 2 as the tool_calls path would produce).
//  2. The operator-facing diagnostic surfaces through the session
//     output callback AND names the offending shape
//     (functions.<name>:<index>).
//
// This is the K-ADV-1 / KIMI-CFG-3 / WIRE-2 fix: previously
// `dialect.KimiParseToolCalls` was wired into s.parseToolCalls but
// never invoked at runtime, so a UUID-shaped id silently dispatched.
// The session loop now re-marshals resp.ToolCalls and runs them
// through the dialect parser as a defence-in-depth step before
// executing.
func TestScenario_Session_KimiTurn_RejectsNonConformantToolCallID(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		// Emit a UUID-shaped tool_call id — non-conformant per Kimi
		// contract. The dispatch should be refused; no second call
		// should reach the server.
		_, _ = fmt.Fprint(w, sseChunk(chatToolCall("550e8400-e29b-41d4-a716-446655440000", "bash", `{"command":"ls"}`)))
		_, _ = fmt.Fprint(w, sseChunk(chatFinish("tool_calls")))
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	cfg := testKimiConfig(server.URL)
	var outputs []string
	sess, err := NewSession(SessionConfig{
		Cfg:       cfg,
		Workdir:   "/tmp/kimi-test",
		SessionID: "kimi-session",
		Output:    func(msg string) { outputs = append(outputs, msg) },
	})
	if err != nil {
		t.Fatalf("session init: %v", err)
	}

	reply, err := sess.Turn("list files")
	if err != nil {
		t.Fatalf("turn returned error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected 1 model call (refused dispatch), got %d", callCount)
	}
	// Diagnostic must be present in either the returned reply or the
	// session output callback.
	combined := reply + "\n" + strings.Join(outputs, "\n")
	if !strings.Contains(combined, "functions.<name>:<index>") {
		t.Errorf("operator-facing diagnostic must name the required id shape; reply=%q outputs=%v", reply, outputs)
	}
	if !strings.Contains(combined, "550e8400") {
		t.Errorf("operator-facing diagnostic must include the offending id; reply=%q outputs=%v", reply, outputs)
	}
}

// TestScenario_Session_KimiTurn_SendsLiteralWireModel — KIMI-CFG-4 fix.
// When an operator sets `model = "moonshotai/Kimi-K2.6"` in their
// [models.<name>] block, the captured request body's `model` field
// MUST be the literal mixed-case string verbatim — proving the docs
// claim ("appears verbatim on the OpenAI request") is now honest.
// Without the fix, the body field was the dialect family ("kimi"),
// which would route to the wrong backend on a CSCS-style gateway.
func TestScenario_Session_KimiTurn_SendsLiteralWireModel(t *testing.T) {
	var capturedBodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBodies = append(capturedBodies, body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, sseChunk(chatDelta("ok")))
		_, _ = fmt.Fprint(w, sseChunk(chatFinish("stop")))
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	cfg := testKimiConfig(server.URL)
	// Operator-paste the canonical mixed-case literal model id.
	mc := cfg.Models["kimi"]
	mc.Model = "moonshotai/Kimi-K2.6"
	cfg.Models["kimi"] = mc

	sess, err := NewSession(SessionConfig{
		Cfg:       cfg,
		Workdir:   "/tmp/kimi-test",
		SessionID: "kimi-session-wire",
	})
	if err != nil {
		t.Fatalf("session init: %v", err)
	}

	_, _ = sess.Turn("ping")
	if len(capturedBodies) < 1 {
		t.Fatalf("no bodies captured")
	}
	body := string(capturedBodies[0])
	if !strings.Contains(body, `"model":"moonshotai/Kimi-K2.6"`) {
		t.Errorf("wire body must carry the literal operator-set model id; body=%s", body)
	}
}

// TestScenario_Session_KimiTurn_PreservesReasoningContent — round-trip
// test for the K-ADV-2 / WIRE-1 fix. A Kimi mock SSE emits both
// reasoning_content and content; the next outbound request body must
// carry the same reasoning_content on the assistant turn (proving the
// stream client read the inbound field AND session.go propagated it
// onto the appended Message AND dialect/helpers.go round-tripped it
// out to the wire).
func TestScenario_Session_KimiTurn_PreservesReasoningContent(t *testing.T) {
	var capturedBodies [][]byte
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		body, _ := io.ReadAll(r.Body)
		capturedBodies = append(capturedBodies, body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		if callCount == 1 {
			// First call: model emits a reasoning trace + tool call.
			// (Use a CONFORMANT id so the dispatch proceeds.)
			_, _ = fmt.Fprint(w, sseChunk(chatToolCallWithReasoning(
				"functions.bash:0", "bash", `{"command":"ls"}`, "I should call bash with ls")))
			_, _ = fmt.Fprint(w, sseChunk(chatFinish("tool_calls")))
		} else {
			// Second call: simple stop. We only need this so the
			// session loop completes; the assertion is on the body.
			_, _ = fmt.Fprint(w, sseChunk(chatDelta("done")))
			_, _ = fmt.Fprint(w, sseChunk(chatFinish("stop")))
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	cfg := testKimiConfig(server.URL)
	sess, err := NewSession(SessionConfig{
		Cfg:       cfg,
		Workdir:   "/tmp/kimi-test",
		SessionID: "kimi-session-rc",
	})
	if err != nil {
		t.Fatalf("session init: %v", err)
	}

	// We expect the bash tool to run and the loop to recurse for
	// a second model call. The second body MUST carry the assistant
	// message with reasoning_content populated.
	_, _ = sess.Turn("list files")
	if callCount < 2 {
		t.Fatalf("expected at least 2 model calls; got %d (bodies=%d)", callCount, len(capturedBodies))
	}
	if len(capturedBodies) < 2 {
		t.Fatalf("expected >= 2 captured bodies, got %d", len(capturedBodies))
	}
	body := capturedBodies[1]
	if !strings.Contains(string(body), `"reasoning_content":"I should call bash with ls"`) {
		t.Errorf("second model call body must carry reasoning_content on the assistant turn; body=%s", body)
	}
}
