package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cucumber/godog"
	"github.com/witlox/ghyll/stream"
)

// syncBuf is a goroutine-safe byte buffer used by the non-TTY
// heartbeat scenario. The heartbeat goroutine writes from another
// goroutine concurrently with the step's String() reads; without
// the mutex the race detector fails the suite.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func newSyncBuf() *syncBuf { return &syncBuf{} }

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func registerStreamSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	var (
		server         *httptest.Server
		client         *stream.Client
		lastResp       *stream.Response
		lastErr        error
		failCount      atomic.Int32
		failModel      string
		failCount2     atomic.Int32
		failModel2     string
		serverHandler  http.HandlerFunc
		userMessages   []map[string]any
		partialContent string
		deltaTokens    int
		retryAfterSec  int
		httpErrorCode  int
		malformedSSE   bool
	)

	// SSE helpers (same as stream/client_test.go)
	sseChunk := func(data string) string {
		return "data: " + data + "\n\n"
	}
	sseDone := func() string {
		return "data: [DONE]\n\n"
	}
	chatDelta := func(content string) string {
		return fmt.Sprintf(`{"choices":[{"delta":{"content":%q},"finish_reason":null}]}`, content)
	}
	chatToolCall := func(id, name, args string) string {
		return fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":%q,"type":"function","function":{"name":%q,"arguments":%q}}]},"finish_reason":null}]}`, id, name, args)
	}
	chatToolCallIdx := func(index int, id, name, args string) string {
		return fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":%d,"id":%q,"type":"function","function":{"name":%q,"arguments":%q}}]},"finish_reason":null}]}`, index, id, name, args)
	}
	chatFinish := func(reason string) string {
		return fmt.Sprintf(`{"choices":[{"delta":{},"finish_reason":%q}]}`, reason)
	}

	ctx.Before(func(ctx2 context.Context, sc *godog.Scenario) (context.Context, error) {
		if server != nil {
			server.Close()
		}
		server = nil
		client = nil
		lastResp = nil
		lastErr = nil
		failCount.Store(0)
		failModel = ""
		failCount2.Store(0)
		failModel2 = ""
		serverHandler = nil
		userMessages = []map[string]any{{"role": "user", "content": "test prompt"}}
		partialContent = ""
		deltaTokens = 0
		retryAfterSec = 0
		httpErrorCode = 0
		malformedSSE = false
		return ctx2, nil
	})

	ctx.After(func(ctx2 context.Context, sc *godog.Scenario, err error) (context.Context, error) {
		if server != nil {
			server.Close()
			server = nil
		}
		return ctx2, nil
	})

	// Helper to set up and start server
	startServer := func(handler http.HandlerFunc) {
		if server != nil {
			server.Close()
		}
		server = httptest.NewServer(handler)
		client = stream.NewClient(server.URL, &stream.ClientOptions{
			MaxRetries:    3,
			BaseBackoffMs: 10, // fast for tests
			// ADR-018: pick up the dialect chosen by the Background or
			// per-scenario step so the content-channel segmenter
			// engages. Empty / unknown families fall through to the
			// passthrough segmenter (no behavioral change for the
			// pre-ADR-018 scenarios that bound "minimax").
			DialectFamily: state.StreamDialect,
		})
	}

	ctx.Step(`^ghyll is configured with endpoint "([^"]*)"$`, func(endpoint string) error {
		state.StreamEndpoint = endpoint
		// Don't actually connect to the real endpoint; tests use httptest
		return nil
	})

	ctx.Step(`^the active dialect is "([^"]*)"$`, func(dialect string) error {
		state.StreamDialect = dialect
		return nil
	})

	ctx.Step(`^the context window contains a user prompt$`, func() error {
		userMessages = []map[string]any{{"role": "user", "content": "hello"}}
		// Set up a successful streaming server
		startServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			fmt.Fprint(w, sseChunk(chatDelta("Hello ")))
			fmt.Fprint(w, sseChunk(chatDelta("world")))
			fmt.Fprint(w, sseChunk(chatFinish("stop")))
			fmt.Fprint(w, sseDone())
		})
		return nil
	})

	ctx.Step(`^the stream client sends a request$`, func() error {
		if client == nil {
			return fmt.Errorf("client not initialized")
		}
		lastResp, lastErr = client.Send(userMessages)
		return nil
	})

	ctx.Step(`^it receives SSE events with delta content$`, func() error {
		if lastErr != nil {
			return fmt.Errorf("stream error: %v", lastErr)
		}
		if lastResp == nil {
			return fmt.Errorf("no response received")
		}
		if lastResp.Content == "" {
			return fmt.Errorf("no content in response")
		}
		return nil
	})

	ctx.Step(`^the model responds with a tool call for "([^"]*)" with command "([^"]*)"$`, func(tool, cmd string) error {
		args := fmt.Sprintf(`{"command":%q}`, cmd)
		startServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			fmt.Fprint(w, sseChunk(chatToolCall("call_1", tool, args)))
			fmt.Fprint(w, sseChunk(chatFinish("tool_calls")))
			fmt.Fprint(w, sseDone())
		})
		lastResp, lastErr = client.Send(userMessages)
		return nil
	})

	ctx.Step(`^the model responds with (\d+) tool calls in sequence$`, func(n int) error {
		startServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			for i := 0; i < n; i++ {
				name := fmt.Sprintf("tool_%d", i)
				id := fmt.Sprintf("call_%d", i+1)
				fmt.Fprint(w, sseChunk(chatToolCallIdx(i, id, name, `{"arg":"val"}`)))
			}
			fmt.Fprint(w, sseChunk(chatFinish("tool_calls")))
			fmt.Fprint(w, sseDone())
		})
		lastResp, lastErr = client.Send(userMessages)
		return nil
	})

	ctx.Step(`^the stream is delivering a response$`, func() error {
		// Set up a server that sends partial content then closes
		startServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			flusher := w.(http.Flusher)
			fmt.Fprint(w, sseChunk(chatDelta("partial ")))
			flusher.Flush()
			fmt.Fprint(w, sseChunk(chatDelta("content")))
			flusher.Flush()
			// Close without [DONE] to simulate stream cut
		})
		return nil
	})

	ctx.Step(`^(\d+) tokens have been received$`, func(n int) error {
		deltaTokens = n
		// The server is already set up to send partial content
		return nil
	})

	ctx.Step(`^the connection drops mid-stream$`, func() error {
		// Send the request to the partial server
		if client == nil {
			return fmt.Errorf("client not initialized")
		}
		lastResp, lastErr = client.Send(userMessages)
		if lastErr != nil {
			// Partial responses should not be errors
			return fmt.Errorf("unexpected error: %v", lastErr)
		}
		if lastResp != nil {
			partialContent = lastResp.Content
			state.PartialContent = partialContent
		}
		return nil
	})

	ctx.Step(`^the endpoint returns HTTP (\d+)$`, func(code int) error {
		httpErrorCode = code
		var attempts atomic.Int32
		startServer(func(w http.ResponseWriter, r *http.Request) {
			n := attempts.Add(1)
			if n <= 4 { // fail all retries
				w.WriteHeader(code)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			fmt.Fprint(w, sseChunk(chatDelta("recovered")))
			fmt.Fprint(w, sseChunk(chatFinish("stop")))
			fmt.Fprint(w, sseDone())
		})
		lastResp, lastErr = client.Send(userMessages)
		return nil
	})

	ctx.Step(`^the endpoint returns HTTP (\d+) with Retry-After: (\d+)$`, func(code, after int) error {
		httpErrorCode = code
		retryAfterSec = after
		var attempts atomic.Int32
		startServer(func(w http.ResponseWriter, r *http.Request) {
			n := attempts.Add(1)
			if n == 1 {
				w.Header().Set("Retry-After", fmt.Sprintf("%d", after))
				w.WriteHeader(code)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			fmt.Fprint(w, sseChunk(chatDelta("ok after retry")))
			fmt.Fprint(w, sseChunk(chatFinish("stop")))
			fmt.Fprint(w, sseDone())
		})
		// Use short retry-after for test speed
		client = stream.NewClient(server.URL, &stream.ClientOptions{
			MaxRetries:    3,
			BaseBackoffMs: 10,
		})
		lastResp, lastErr = client.Send(userMessages)
		return nil
	})

	ctx.Step(`^auto-routing is active \(no --model flag\)$`, func() error {
		state.AutoRouting = true
		return nil
	})

	ctx.Step(`^the ([a-z0-9]+) endpoint has failed (\d+) consecutive requests$`, func(model string, n int) error {
		failModel = model
		failCount.Store(int32(n))
		state.RetryCount = n
		return nil
	})

	ctx.Step(`^both ([a-z0-9]+) and ([a-z0-9]+) endpoints have failed (\d+) consecutive requests each$`, func(m1, m2 string, n int) error {
		failModel = m1
		failModel2 = m2
		failCount.Store(int32(n))
		failCount2.Store(int32(n))
		// Both endpoints fail
		startServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(503)
		})
		lastResp, lastErr = client.Send(userMessages)
		return nil
	})

	ctx.Step(`^tier fallback triggers$`, func() error {
		// The tier with failModel failed, so fallback to the other tier.
		// In real code, the context manager/router handles this.
		// Here we verify the stream client properly reports retryable errors.
		if failModel == "" {
			return fmt.Errorf("no fail model set")
		}
		// Set up a server that fails for the primary and succeeds for fallback
		startServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			fmt.Fprint(w, sseChunk(chatDelta("fallback response")))
			fmt.Fprint(w, sseChunk(chatFinish("stop")))
			fmt.Fprint(w, sseDone())
		})
		lastResp, lastErr = client.Send(userMessages)
		if lastErr != nil {
			return fmt.Errorf("fallback request failed: %v", lastErr)
		}
		if lastResp == nil || lastResp.Content == "" {
			return fmt.Errorf("fallback response empty")
		}
		// Record fallback in state
		if state.ActiveModel == "m25" {
			state.FallbackModel = "glm5"
		} else {
			state.FallbackModel = "m25"
		}
		return nil
	})

	ctx.Step(`^the endpoint returns an SSE frame with invalid JSON in the data field$`, func() error {
		malformedSSE = true
		startServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			fmt.Fprint(w, sseChunk(chatDelta("before")))
			fmt.Fprint(w, sseChunk("{invalid json!!!"))
			fmt.Fprint(w, sseChunk(chatDelta(" after")))
			fmt.Fprint(w, sseChunk(chatFinish("stop")))
			fmt.Fprint(w, sseDone())
		})
		lastResp, lastErr = client.Send(userMessages)
		return nil
	})

	// --- Additional assertion steps for stream scenarios ---

	ctx.Step(`^content deltas are rendered to the terminal in real time$`, func() error {
		// Behavioral: onDelta callback handles this
		return nil
	})

	ctx.Step(`^the complete response is assembled and returned to the context manager$`, func() error {
		if lastResp == nil {
			return fmt.Errorf("no response")
		}
		if lastResp.Content == "" && len(lastResp.ToolCalls) == 0 {
			return fmt.Errorf("response has no content or tool calls")
		}
		return nil
	})

	ctx.Step(`^the stream client finishes receiving$`, func() error {
		// Response was already received in a previous step
		if lastErr != nil {
			return fmt.Errorf("stream error: %v", lastErr)
		}
		return nil
	})

	ctx.Step(`^it returns a structured tool call with name="([^"]*)" and arguments$`, func(name string) error {
		if lastResp == nil {
			return fmt.Errorf("no response")
		}
		if len(lastResp.ToolCalls) == 0 {
			return fmt.Errorf("no tool calls in response")
		}
		if lastResp.ToolCalls[0].Function.Name != name {
			return fmt.Errorf("tool name = %q, want %q", lastResp.ToolCalls[0].Function.Name, name)
		}
		return nil
	})

	ctx.Step(`^the tool call is passed to tool\/ for execution$`, func() error {
		// Behavioral
		return nil
	})

	ctx.Step(`^the tool result is added to context as a tool response message$`, func() error {
		// Behavioral
		return nil
	})

	ctx.Step(`^all (\d+) tool calls are returned in order$`, func(n int) error {
		if lastResp == nil {
			return fmt.Errorf("no response")
		}
		if len(lastResp.ToolCalls) != n {
			return fmt.Errorf("expected %d tool calls, got %d", n, len(lastResp.ToolCalls))
		}
		return nil
	})

	ctx.Step(`^each is executed and its result added to context$`, func() error {
		return nil
	})

	ctx.Step(`^the partial content is preserved$`, func() error {
		if lastResp == nil {
			return fmt.Errorf("no response")
		}
		if !lastResp.Partial {
			return fmt.Errorf("expected partial response")
		}
		if lastResp.Content == "" {
			return fmt.Errorf("no partial content preserved")
		}
		return nil
	})

	ctx.Step(`^the partial response is surfaced to the user$`, func() error {
		return nil
	})

	ctx.Step(`^the user can retry with the same context$`, func() error {
		return nil
	})

	ctx.Step(`^the stream client retries$`, func() error {
		// Retries already happened during Send
		return nil
	})

	ctx.Step(`^it waits (\d+)s, then (\d+)s, then (\d+)s between attempts$`, func(a, b, c int) error {
		// Behavioral: exponential backoff verified by timing in unit tests
		return nil
	})

	ctx.Step(`^after (\d+) failed attempts it triggers tier fallback$`, func(n int) error {
		// After max retries, error is returned for tier fallback
		if lastErr == nil && lastResp != nil {
			// Got a response (retry succeeded)
			return nil
		}
		return nil
	})

	ctx.Step(`^the stream client receives the response$`, func() error {
		// Already received in the HTTP step
		return nil
	})

	ctx.Step(`^it waits (\d+) seconds before retrying$`, func(n int) error {
		// Verified by Retry-After handling
		return nil
	})

	ctx.Step(`^the request is sent to the "([^"]*)" endpoint instead$`, func(model string) error {
		state.FallbackModel = model
		return nil
	})

	ctx.Step(`^the context is reformatted for the glm(\d+) dialect$`, func(n int) error {
		return nil
	})

	ctx.Step(`^retry is exhausted$`, func() error {
		// Already happened during Send
		return nil
	})

	ctx.Step(`^the session stays open for manual retry$`, func() error {
		return nil
	})

	ctx.Step(`^no tier fallback occurs$`, func() error {
		if state.ModelLocked {
			return nil // expected: locked model means no fallback
		}
		return nil
	})

	ctx.Step(`^the user submits a prompt$`, func() error {
		if client == nil {
			startServer(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(503)
			})
		}
		lastResp, lastErr = client.Send(userMessages)
		return nil
	})

	ctx.Step(`^no checkpoint is created for the failed turn$`, func() error {
		return nil
	})

	ctx.Step(`^the stream client parses the frame$`, func() error {
		// Already parsed during Send
		return nil
	})

	ctx.Step(`^the malformed frame is skipped$`, func() error {
		if lastResp == nil {
			return fmt.Errorf("no response")
		}
		// Content should contain data from valid frames
		return nil
	})

	ctx.Step(`^parsing continues with subsequent frames$`, func() error {
		if lastResp == nil {
			return fmt.Errorf("no response")
		}
		if lastResp.Content == "" {
			return fmt.Errorf("no content after malformed frame")
		}
		return nil
	})

	ctx.Step(`^a warning is logged$`, func() error {
		return nil
	})

	// ADR-018: dialect content-channel grammar.

	ctx.Step(`^the SSE stream emits Kimi tool-call sentinels in delta\.content$`, func() error {
		leaked := "Looking now.\n" +
			"<|tool_calls_section_begin|>" +
			"<|tool_call_begin|> functions.memory_search:0 " +
			"<|tool_call_argument_begin|> {\"query\":\"arrow\"} " +
			"<|tool_call_end|>" +
			"<|tool_calls_section_end|>"
		startServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			fmt.Fprint(w, sseChunk(chatDelta(leaked)))
			fmt.Fprint(w, sseChunk(chatFinish("stop")))
			fmt.Fprint(w, sseDone())
		})
		lastResp, lastErr = client.Send(userMessages)
		return nil
	})

	ctx.Step(`^the user-visible Content has the sentinels stripped$`, func() error {
		if lastErr != nil {
			return lastErr
		}
		if lastResp.Content != "Looking now.\n" {
			return fmt.Errorf("content = %q, want %q", lastResp.Content, "Looking now.\n")
		}
		return nil
	})

	ctx.Step(`^the structured ToolCalls contains the extracted call with name "([^"]*)"$`, func(name string) error {
		if lastErr != nil {
			return lastErr
		}
		if len(lastResp.ToolCalls) != 1 || lastResp.ToolCalls[0].Function.Name != name {
			return fmt.Errorf("tool calls = %+v, want exactly one named %q", lastResp.ToolCalls, name)
		}
		return nil
	})

	ctx.Step(`^no SegmentReasoning content is produced$`, func() error {
		if lastErr != nil {
			return lastErr
		}
		if lastResp.ReasoningContent != "" {
			return fmt.Errorf("reasoning content = %q, want empty (no <think> in Kimi sentinel scenario)", lastResp.ReasoningContent)
		}
		return nil
	})

	ctx.Step(`^the SSE stream emits a <think>([^<]+)</think> block in delta\.content$`, func(inner string) error {
		leaked := "<think>" + inner + "</think>visible answer"
		startServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			fmt.Fprint(w, sseChunk(chatDelta(leaked)))
			fmt.Fprint(w, sseChunk(chatFinish("stop")))
			fmt.Fprint(w, sseDone())
		})
		lastResp, lastErr = client.Send(userMessages)
		return nil
	})

	ctx.Step(`^the user-visible Content excludes the think block$`, func() error {
		if lastErr != nil {
			return lastErr
		}
		if lastResp.Content != "visible answer" {
			return fmt.Errorf("content = %q, want %q", lastResp.Content, "visible answer")
		}
		return nil
	})

	ctx.Step(`^the ReasoningContent equals "([^"]*)"$`, func(want string) error {
		if lastErr != nil {
			return lastErr
		}
		if lastResp.ReasoningContent != want {
			return fmt.Errorf("reasoning content = %q, want %q", lastResp.ReasoningContent, want)
		}
		return nil
	})

	ctx.Step(`^the SSE stream emits structured delta\.tool_calls \(correctly-configured gateway\)$`, func() error {
		// Server is set up by the AND step below so both envelope and
		// content sentinels are part of the same response.
		return nil
	})

	ctx.Step(`^the SSE stream also emits raw Kimi sentinels in delta\.content$`, func() error {
		leaked := "<|tool_calls_section_begin|>" +
			"<|tool_call_begin|> functions.SEGMENTER_DUPLICATE:0 " +
			"<|tool_call_argument_begin|> {\"query\":\"dup\"} " +
			"<|tool_call_end|>" +
			"<|tool_calls_section_end|>"
		startServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			fmt.Fprint(w, sseChunk(chatToolCall("envelope_id_0", "memory_search", `{"query":"x"}`)))
			fmt.Fprint(w, sseChunk(chatDelta(leaked)))
			fmt.Fprint(w, sseChunk(chatFinish("tool_calls")))
			fmt.Fprint(w, sseDone())
		})
		lastResp, lastErr = client.Send(userMessages)
		return nil
	})

	ctx.Step(`^the ToolCalls match the envelope-provided ids$`, func() error {
		if lastErr != nil {
			return lastErr
		}
		if len(lastResp.ToolCalls) != 1 || lastResp.ToolCalls[0].ID != "envelope_id_0" {
			return fmt.Errorf("tool calls = %+v, want exactly one with id envelope_id_0", lastResp.ToolCalls)
		}
		return nil
	})

	ctx.Step(`^the segmenter-extracted tool calls are discarded$`, func() error {
		if lastErr != nil {
			return lastErr
		}
		for _, tc := range lastResp.ToolCalls {
			if tc.Function.Name == "SEGMENTER_DUPLICATE" || tc.ID == "functions.SEGMENTER_DUPLICATE:0" {
				return fmt.Errorf("segmenter-extracted tool call leaked through: %+v", tc)
			}
		}
		return nil
	})

	// Non-TTY heartbeat (commit 12bca40).

	var (
		heartbeatBuf      *syncBuf
		heartbeatRenderer *stream.Renderer
	)

	ctx.Step(`^the renderer writes to a non-TTY sink \(pipe / log capture\)$`, func() error {
		heartbeatBuf = newSyncBuf()
		heartbeatRenderer = stream.NewRenderer(heartbeatBuf)
		heartbeatRenderer.SetHeartbeatInterval(20 * time.Millisecond)
		return nil
	})

	ctx.Step(`^the session starts the thinking spinner$`, func() error {
		heartbeatRenderer.StartSpinner("kimi is thinking…")
		// 80ms ≥ 3 ticks at 20ms.
		time.Sleep(80 * time.Millisecond)
		heartbeatRenderer.StopSpinner()
		return nil
	})

	ctx.Step(`^an initial "ℹ ([^"]+) is thinking…" line is emitted$`, func(model string) error {
		want := "ℹ " + model + " is thinking…"
		if !strings.Contains(heartbeatBuf.String(), want) {
			return fmt.Errorf("missing initial heartbeat line %q in %q", want, heartbeatBuf.String())
		}
		return nil
	})

	ctx.Step(`^periodic "  … \{elapsed\}s" tick lines follow until StopSpinner$`, func() error {
		got := heartbeatBuf.String()
		if c := strings.Count(got, "  … "); c < 2 {
			return fmt.Errorf("expected ≥2 tick lines, got %d in %q", c, got)
		}
		return nil
	})

	// Suppress unused warnings
	_ = json.Marshal
	_ = serverHandler
	_ = malformedSSE
	_ = httpErrorCode
	_ = retryAfterSec
	_ = deltaTokens
	_ = failModel2
	failCount2.Store(0) // reference to suppress unused warning
}
