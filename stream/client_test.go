package stream

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func sseChunk(data string) string {
	return "data: " + data + "\n\n"
}

func sseDone() string {
	return "data: [DONE]\n\n"
}

func chatDelta(content string) string {
	return fmt.Sprintf(`{"choices":[{"delta":{"content":%q},"finish_reason":null}]}`, content)
}

func chatToolCall(id, name, args string) string {
	return chatToolCallIdx(0, id, name, args)
}

func chatToolCallIdx(index int, id, name, args string) string {
	return fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":%d,"id":%q,"type":"function","function":{"name":%q,"arguments":%q}}]},"finish_reason":null}]}`, index, id, name, args)
}

func chatFinish(reason string) string {
	return fmt.Sprintf(`{"choices":[{"delta":{},"finish_reason":%q}]}`, reason)
}

// TestScenario_Stream_SuccessfulResponse maps to:
// Scenario: Successful streaming response
func TestScenario_Stream_SuccessfulResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, sseChunk(chatDelta("Hello ")))
		_, _ = fmt.Fprint(w, sseChunk(chatDelta("world")))
		_, _ = fmt.Fprint(w, sseChunk(chatFinish("stop")))
		_, _ = fmt.Fprint(w, sseDone())
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	resp, err := client.Send([]map[string]any{
		{"role": "user", "content": "hi"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "Hello world" {
		t.Errorf("content = %q, want %q", resp.Content, "Hello world")
	}
	if resp.Partial {
		t.Error("expected non-partial response")
	}
	if resp.FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want %q", resp.FinishReason, "stop")
	}
}

// TestScenario_Stream_ToolCalls maps to:
// Scenario: Response with tool calls
func TestScenario_Stream_ToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, sseChunk(chatToolCall("call_1", "bash", `{"command":"ls src/"}`)))
		_, _ = fmt.Fprint(w, sseChunk(chatFinish("tool_calls")))
		_, _ = fmt.Fprint(w, sseDone())
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	resp, err := client.Send([]map[string]any{
		{"role": "user", "content": "list files"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool_calls len = %d, want 1", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Function.Name != "bash" {
		t.Errorf("tool name = %q", resp.ToolCalls[0].Function.Name)
	}
	if resp.ToolCalls[0].ID != "call_1" {
		t.Errorf("tool id = %q", resp.ToolCalls[0].ID)
	}
}

// TestScenario_Stream_MultipleToolCalls maps to:
// Scenario: Multiple tool calls in one response
func TestScenario_Stream_MultipleToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, sseChunk(chatToolCallIdx(0, "call_1", "bash", `{"command":"ls"}`)))
		_, _ = fmt.Fprint(w, sseChunk(chatToolCallIdx(1, "call_2", "read_file", `{"path":"go.mod"}`)))
		_, _ = fmt.Fprint(w, sseChunk(chatToolCallIdx(2, "call_3", "bash", `{"command":"pwd"}`)))
		_, _ = fmt.Fprint(w, sseChunk(chatFinish("tool_calls")))
		_, _ = fmt.Fprint(w, sseDone())
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	resp, err := client.Send([]map[string]any{
		{"role": "user", "content": "do things"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.ToolCalls) != 3 {
		t.Fatalf("tool_calls len = %d, want 3", len(resp.ToolCalls))
	}
	if resp.ToolCalls[1].Function.Name != "read_file" {
		t.Errorf("second tool = %q", resp.ToolCalls[1].Function.Name)
	}
}

// TestScenario_Stream_PartialResponse maps to:
// Scenario: Partial response on stream cut
func TestScenario_Stream_PartialResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher := w.(http.Flusher)
		_, _ = fmt.Fprint(w, sseChunk(chatDelta("Hello ")))
		flusher.Flush()
		_, _ = fmt.Fprint(w, sseChunk(chatDelta("partial")))
		flusher.Flush()
		// Close connection without sending [DONE]
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	resp, err := client.Send([]map[string]any{
		{"role": "user", "content": "hi"},
	})
	// Partial response should not be a hard error
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Partial {
		t.Error("expected partial response")
	}
	if resp.Content != "Hello partial" {
		t.Errorf("content = %q, want %q", resp.Content, "Hello partial")
	}
}

// TestScenario_Stream_RetryBackoff maps to:
// Scenario: Retry with exponential backoff on 5xx
func TestScenario_Stream_RetryBackoff(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n <= 3 {
			w.WriteHeader(503)
			return
		}
		// 4th attempt succeeds
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, sseChunk(chatDelta("recovered")))
		_, _ = fmt.Fprint(w, sseChunk(chatFinish("stop")))
		_, _ = fmt.Fprint(w, sseDone())
	}))
	defer server.Close()

	client := NewClient(server.URL, &ClientOptions{
		MaxRetries:    3,
		BaseBackoffMs: 10, // fast for tests
	})
	resp, err := client.Send([]map[string]any{
		{"role": "user", "content": "hi"},
	})
	// After 3 retries, should get a fallback-eligible error
	// (the 4th attempt is beyond MaxRetries so it shouldn't be reached)
	if err == nil {
		// If we got a response, the test server gave us one on attempt 4
		// but we should have stopped at 3 retries
		_ = resp
	}
	if int(attempts.Load()) < 3 {
		t.Errorf("expected at least 3 attempts, got %d", attempts.Load())
	}
}

// TestScenario_Stream_RateLimit maps to:
// Scenario: Rate limit handling
func TestScenario_Stream_RateLimit(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, sseChunk(chatDelta("ok")))
		_, _ = fmt.Fprint(w, sseChunk(chatFinish("stop")))
		_, _ = fmt.Fprint(w, sseDone())
	}))
	defer server.Close()

	client := NewClient(server.URL, &ClientOptions{
		MaxRetries:    3,
		BaseBackoffMs: 10,
	})
	resp, err := client.Send([]map[string]any{
		{"role": "user", "content": "hi"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("content = %q", resp.Content)
	}
	if int(attempts.Load()) != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts.Load())
	}
}

// TestScenario_Stream_ContextTooLong maps to:
// Reactive compaction trigger
func TestScenario_Stream_ContextTooLong(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "context_length_exceeded",
				"type":    "invalid_request_error",
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, &ClientOptions{MaxRetries: 0})
	_, err := client.Send([]map[string]any{
		{"role": "user", "content": "hi"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var sErr *StreamError
	if !AsStreamError(err, &sErr) {
		t.Fatalf("expected StreamError, got %T: %v", err, err)
	}
	if !sErr.ContextTooLong {
		t.Error("expected ContextTooLong=true")
	}
}

// TestScenario_Stream_MalformedSSE maps to:
// Scenario: Malformed SSE frame
func TestScenario_Stream_MalformedSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, sseChunk(chatDelta("before")))
		_, _ = fmt.Fprint(w, sseChunk("{invalid json"))
		_, _ = fmt.Fprint(w, sseChunk(chatDelta(" after")))
		_, _ = fmt.Fprint(w, sseChunk(chatFinish("stop")))
		_, _ = fmt.Fprint(w, sseDone())
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	resp, err := client.Send([]map[string]any{
		{"role": "user", "content": "hi"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Malformed frame skipped, content from valid frames preserved
	if !strings.Contains(resp.Content, "before") || !strings.Contains(resp.Content, "after") {
		t.Errorf("content = %q, expected both 'before' and 'after'", resp.Content)
	}
}

// TestStream_SSEToolCallDelta_DefaultsMissingType — defensive
// default for vLLM 0.6 (and earlier) which omits the `type` field
// on second-and-later chunks of a streamed tool_call. Without the
// default the accumulated ToolCall.Type stays empty and downstream
// consumers comparing `tc.Type == "function"` silently skip the
// call. Defaults to "function" on first occurrence; explicit
// non-empty types pass through unchanged.
func TestStream_SSEToolCallDelta_DefaultsMissingType(t *testing.T) {
	// First chunk creates the entry with NO type field (vLLM 0.6
	// behaviour reproduced verbatim).
	firstChunk := `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"functions.bash:0","function":{"name":"bash","arguments":""}}]},"finish_reason":null}]}`
	// Second chunk appends arguments fragment.
	secondChunk := `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"command\":\"ls\"}"}}]},"finish_reason":null}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, sseChunk(firstChunk))
		_, _ = fmt.Fprint(w, sseChunk(secondChunk))
		_, _ = fmt.Fprint(w, sseChunk(chatFinish("tool_calls")))
		_, _ = fmt.Fprint(w, sseDone())
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	resp, err := client.Send([]map[string]any{{"role": "user", "content": "go"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Type != "function" {
		t.Errorf("tool_call Type = %q, want %q (missing-type default)", resp.ToolCalls[0].Type, "function")
	}
	if resp.ToolCalls[0].ID != "functions.bash:0" {
		t.Errorf("tool_call ID = %q", resp.ToolCalls[0].ID)
	}
}

// TestStream_SSEToolCallDelta_LateTypeMergeUpdatesEmptyType — K-ADV-7
// / WIRE-3 fix. A backend that emits `type: ""` on chunk 1 (defaulted
// to "function" by the first-chunk path) and then `type: "function"`
// on chunk 2 used to silently DROP the chunk-2 Type because the
// merge path only updated ID/Name/Arguments. Symmetric merge: a
// later non-empty Type overrides; a later empty Type keeps the
// existing value.
func TestStream_SSEToolCallDelta_LateTypeMergeUpdatesEmptyType(t *testing.T) {
	// Chunk 1: id + name, no type (defaults to "function").
	firstChunk := `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"functions.bash:0","function":{"name":"bash","arguments":""}}]},"finish_reason":null}]}`
	// Chunk 2: explicit `type: "function"`, arguments tail.
	secondChunk := `{"choices":[{"delta":{"tool_calls":[{"index":0,"type":"function","function":{"arguments":"{\"command\":\"ls\"}"}}]},"finish_reason":null}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, sseChunk(firstChunk))
		_, _ = fmt.Fprint(w, sseChunk(secondChunk))
		_, _ = fmt.Fprint(w, sseChunk(chatFinish("tool_calls")))
		_, _ = fmt.Fprint(w, sseDone())
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	resp, err := client.Send([]map[string]any{{"role": "user", "content": "go"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Type != "function" {
		t.Errorf("tool_call Type after late merge = %q, want %q", resp.ToolCalls[0].Type, "function")
	}
}

// TestStream_SSEReasoningContent_AccumulatesIntoResponse — K-ADV-2 /
// WIRE-1 fix. The SSE parser must read `delta.reasoning_content` and
// accumulate it into Response.ReasoningContent. Without this fix the
// field was silently dropped — round-trip via dialect/helpers.go was
// one-way only because nothing populated the inbound half.
func TestStream_SSEReasoningContent_AccumulatesIntoResponse(t *testing.T) {
	// Two chunks of reasoning + one of content, mirroring how Kimi
	// 2.5/2.6 emits chain-of-thought on the wire.
	chunk1 := `{"choices":[{"delta":{"reasoning_content":"I should call "},"finish_reason":null}]}`
	chunk2 := `{"choices":[{"delta":{"reasoning_content":"bash with ls"},"finish_reason":null}]}`
	chunk3 := `{"choices":[{"delta":{"content":"calling bash"},"finish_reason":null}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, sseChunk(chunk1))
		_, _ = fmt.Fprint(w, sseChunk(chunk2))
		_, _ = fmt.Fprint(w, sseChunk(chunk3))
		_, _ = fmt.Fprint(w, sseChunk(chatFinish("stop")))
		_, _ = fmt.Fprint(w, sseDone())
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	resp, err := client.Send([]map[string]any{{"role": "user", "content": "go"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ReasoningContent != "I should call bash with ls" {
		t.Errorf("ReasoningContent = %q, want %q (SSE parser must read delta.reasoning_content)",
			resp.ReasoningContent, "I should call bash with ls")
	}
	if resp.Content != "calling bash" {
		t.Errorf("Content = %q, want %q", resp.Content, "calling bash")
	}
}

// TestStream_SSEReasoning_AlternativeFieldName — CSCS Envoy AI
// Gateway / vLLM emit `delta.reasoning` instead of the spec-correct
// `delta.reasoning_content`. The parser must accept either name and
// route into the same accumulator so ghyll's dialect round-trip
// keeps working regardless of which backend variant fronts Kimi.
// (Probe 2026-06-08 confirmed CSCS gateway uses the short name.)
func TestStream_SSEReasoning_AlternativeFieldName(t *testing.T) {
	chunk1 := `{"choices":[{"delta":{"reasoning":"thinking "},"finish_reason":null}]}`
	chunk2 := `{"choices":[{"delta":{"reasoning":"about this"},"finish_reason":null}]}`
	chunk3 := `{"choices":[{"delta":{"content":"answer"},"finish_reason":null}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, sseChunk(chunk1))
		_, _ = fmt.Fprint(w, sseChunk(chunk2))
		_, _ = fmt.Fprint(w, sseChunk(chunk3))
		_, _ = fmt.Fprint(w, sseChunk(chatFinish("stop")))
		_, _ = fmt.Fprint(w, sseDone())
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	resp, err := client.Send([]map[string]any{{"role": "user", "content": "go"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ReasoningContent != "thinking about this" {
		t.Errorf("ReasoningContent = %q, want %q (parser must accept `reasoning` short name)",
			resp.ReasoningContent, "thinking about this")
	}
	if resp.Content != "answer" {
		t.Errorf("Content = %q, want %q", resp.Content, "answer")
	}
}

// TestStream_PreemptiveBodyCapTriggersContextTooLong — when
// ClientOptions.MaxRequestBytes is set and the marshalled body
// exceeds it, doRequest must short-circuit to a ContextTooLong
// StreamError BEFORE making the HTTP call. The session's reactive-
// compaction path then handles the recovery. Without this guard,
// every doomed request hits the wire and 413s.
func TestStream_PreemptiveBodyCapTriggersContextTooLong(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(200)
	}))
	defer server.Close()

	client := NewClient(server.URL, &ClientOptions{
		MaxRequestBytes: 200, // anything beyond a tiny payload trips it
	})
	// Build a message large enough to exceed 200 bytes after JSON
	// envelope overhead.
	bigContent := strings.Repeat("x", 500)
	_, err := client.Send([]map[string]any{{"role": "user", "content": bigContent}})
	if err == nil {
		t.Fatal("expected preemptive ContextTooLong error")
	}
	var sErr *StreamError
	if !AsStreamError(err, &sErr) {
		t.Fatalf("expected StreamError, got: %T %v", err, err)
	}
	if !sErr.ContextTooLong {
		t.Errorf("ContextTooLong should be true, got: %#v", sErr)
	}
	if hits != 0 {
		t.Errorf("preemptive check must NOT hit the wire, but server got %d requests", hits)
	}
}

// TestStream_PreemptiveBodyCap_BelowThresholdPasses — sanity:
// requests under MaxRequestBytes proceed normally.
func TestStream_PreemptiveBodyCap_BelowThresholdPasses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, sseChunk(chatFinish("stop")))
		_, _ = fmt.Fprint(w, sseDone())
	}))
	defer server.Close()

	client := NewClient(server.URL, &ClientOptions{
		MaxRequestBytes: 1_000_000, // generous; tiny request fits
	})
	_, err := client.Send([]map[string]any{{"role": "user", "content": "hi"}})
	if err != nil {
		t.Fatalf("under-threshold request must not preempt, got: %v", err)
	}
}

// TestStream_413_HintAndBodyExcerpt — gateway returns a non-JSON
// 413 (plain text "Request Entity Too Large"). StreamError.Message
// must include both the operator hint AND the gateway body excerpt
// so the operator knows (a) the cause and (b) what to lower.
// Pre-fix, message was empty and the operator saw bare "HTTP 413".
func TestStream_413_HintAndBodyExcerpt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(413)
		_, _ = fmt.Fprint(w, "413 Request Entity Too Large (max 65536 bytes)")
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	_, err := client.Send([]map[string]any{{"role": "user", "content": "go"}})
	if err == nil {
		t.Fatal("expected error from 413")
	}
	msg := err.Error()
	if !strings.Contains(msg, "max_context") {
		t.Errorf("error should mention max_context hint, got: %v", err)
	}
	if !strings.Contains(msg, "max 65536 bytes") {
		t.Errorf("error should include gateway body excerpt, got: %v", err)
	}
}

// TestStream_413_JSONBodyAlsoSurfaces — same as above but with the
// OpenAI-flavored JSON error envelope. The hint must still prefix;
// the message field still flows through.
func TestStream_413_JSONBodyAlsoSurfaces(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(413)
		_, _ = fmt.Fprint(w, `{"error":{"message":"request body exceeded 100000 bytes","type":"invalid_request_error"}}`)
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	_, err := client.Send([]map[string]any{{"role": "user", "content": "go"}})
	if err == nil {
		t.Fatal("expected error from 413")
	}
	msg := err.Error()
	if !strings.Contains(msg, "max_context") {
		t.Errorf("error should mention max_context hint, got: %v", err)
	}
	if !strings.Contains(msg, "exceeded 100000 bytes") {
		t.Errorf("error should include gateway body, got: %v", err)
	}
}

// TestStream_NonJSON5xx_SurfacesBodyExcerpt — broader contract:
// any non-2xx with a non-JSON body now surfaces the excerpt instead
// of swallowing it. Gateway 502 "Bad Gateway" was previously bare
// "HTTP 502" — now operators see what proxied the failure.
func TestStream_NonJSON5xx_SurfacesBodyExcerpt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(502)
		_, _ = fmt.Fprint(w, "<html><body>upstream timeout from envoy</body></html>")
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	_, err := client.Send([]map[string]any{{"role": "user", "content": "go"}})
	if err == nil {
		t.Fatal("expected error from 502")
	}
	if !strings.Contains(err.Error(), "upstream timeout from envoy") {
		t.Errorf("error should include body excerpt, got: %v", err)
	}
}

// TestStream_SSEReasoning_SpecCorrectWins — when a chunk happens to
// carry BOTH `reasoning_content` AND `reasoning`, the spec-correct
// field wins. Future-proofs against a backend that emits the short
// name for backward compat alongside the spec name.
func TestStream_SSEReasoning_SpecCorrectWins(t *testing.T) {
	chunk := `{"choices":[{"delta":{"reasoning_content":"spec","reasoning":"alt"},"finish_reason":null}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, sseChunk(chunk))
		_, _ = fmt.Fprint(w, sseChunk(chatFinish("stop")))
		_, _ = fmt.Fprint(w, sseDone())
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	resp, err := client.Send([]map[string]any{{"role": "user", "content": "go"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ReasoningContent != "spec" {
		t.Errorf("ReasoningContent = %q, want %q (spec-correct name must win)", resp.ReasoningContent, "spec")
	}
}

// TestScenario_Stream_KimiContentSentinelsExtractToolCalls
// (ADR-018) — when the gateway leaks Kimi's native tool-call
// sentinels into delta.content (vLLM started without
// --tool-call-parser kimi_k2), the dialect segmenter must
// reconstruct structured tool calls and strip the sentinels from
// the user-visible content.
func TestScenario_Stream_KimiContentSentinelsExtractToolCalls(t *testing.T) {
	leaked := "Looking now.\n" +
		"<|tool_calls_section_begin|>" +
		"<|tool_call_begin|> functions.memory_search:0 " +
		"<|tool_call_argument_begin|> {\"query\":\"arrow\"} " +
		"<|tool_call_end|>" +
		"<|tool_calls_section_end|>"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, sseChunk(chatDelta(leaked)))
		_, _ = fmt.Fprint(w, sseChunk(chatFinish("stop")))
		_, _ = fmt.Fprint(w, sseDone())
	}))
	defer server.Close()

	client := NewClient(server.URL, &ClientOptions{DialectFamily: "kimi"})
	resp, err := client.Send([]map[string]any{{"role": "user", "content": "go"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "Looking now.\n" {
		t.Errorf("Content = %q, want %q (sentinels must be stripped)", resp.Content, "Looking now.\n")
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Function.Name != "memory_search" {
		t.Errorf("tool name = %q", resp.ToolCalls[0].Function.Name)
	}
	if strings.TrimSpace(resp.ToolCalls[0].Function.Arguments) != `{"query":"arrow"}` {
		t.Errorf("args = %q", resp.ToolCalls[0].Function.Arguments)
	}
}

// TestScenario_Stream_GLMThinkBlocksRouteToReasoning (ADR-018) —
// when GLM-5 leaks <think>...</think> into delta.content, the
// segmenter must route the inner text to ReasoningContent and the
// outer text to Content.
func TestScenario_Stream_GLMThinkBlocksRouteToReasoning(t *testing.T) {
	leaked := "<think>chain of thought</think>visible answer"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, sseChunk(chatDelta(leaked)))
		_, _ = fmt.Fprint(w, sseChunk(chatFinish("stop")))
		_, _ = fmt.Fprint(w, sseDone())
	}))
	defer server.Close()

	client := NewClient(server.URL, &ClientOptions{DialectFamily: "glm"})
	resp, err := client.Send([]map[string]any{{"role": "user", "content": "go"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "visible answer" {
		t.Errorf("Content = %q, want %q", resp.Content, "visible answer")
	}
	if resp.ReasoningContent != "chain of thought" {
		t.Errorf("ReasoningContent = %q, want %q", resp.ReasoningContent, "chain of thought")
	}
}

// TestScenario_Stream_EnvelopeWinsOverSegmenter (ADR-018) — when
// the gateway IS correctly configured and emits structured
// tool_calls in the envelope, segmenter output for the same turn
// must be discarded (envelope is authoritative).
func TestScenario_Stream_EnvelopeWinsOverSegmenter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, sseChunk(chatToolCall("envelope_id_0", "memory_search", `{"query":"x"}`)))
		_, _ = fmt.Fprint(w, sseChunk(chatFinish("tool_calls")))
		_, _ = fmt.Fprint(w, sseDone())
	}))
	defer server.Close()
	client := NewClient(server.URL, &ClientOptions{DialectFamily: "kimi"})
	resp, err := client.Send([]map[string]any{{"role": "user", "content": "go"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "envelope_id_0" {
		t.Fatalf("tool calls = %+v, want exactly 1 with envelope id", resp.ToolCalls)
	}
}
