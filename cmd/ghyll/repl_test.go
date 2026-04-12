package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestScenario_REPL_BasicFlow tests the REPL with simulated stdin
func TestScenario_REPL_BasicFlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, sseChunk(chatDelta("reply")))
		_, _ = fmt.Fprint(w, sseChunk(chatFinish("stop")))
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	sess, err := NewSession(SessionConfig{
		Cfg:       testConfig(server.URL),
		Workdir:   "/tmp/test",
		SessionID: "repl-test",
		Output:    func(msg string) {},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate: one prompt, then /exit
	input := strings.NewReader("hello world\n/exit\n")
	REPL(sess, input)
	// If we get here without hanging, the REPL works
}

// TestScenario_REPL_StatusCommand
func TestScenario_REPL_StatusCommand(t *testing.T) {
	server := mockModelServer(t)
	defer server.Close()

	sess, err := NewSession(SessionConfig{
		Cfg:       testConfig(server.URL),
		Workdir:   "/tmp/test",
		SessionID: "repl-test",
		Output:    func(msg string) {},
	})
	if err != nil {
		t.Fatal(err)
	}

	input := strings.NewReader("/status\n/exit\n")
	REPL(sess, input)
}

// TestScenario_REPL_DeepCommand
func TestScenario_REPL_DeepCommand(t *testing.T) {
	server := mockModelServer(t)
	defer server.Close()

	var outputs []string
	sess, err := NewSession(SessionConfig{
		Cfg:       testConfig(server.URL),
		Workdir:   "/tmp/test",
		SessionID: "repl-test",
		Output:    func(msg string) { outputs = append(outputs, msg) },
	})
	if err != nil {
		t.Fatal(err)
	}

	input := strings.NewReader("/deep\n/exit\n")
	REPL(sess, input)

	if !sess.deepOverride {
		t.Error("expected deepOverride=true after /deep")
	}
}
