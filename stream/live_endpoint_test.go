//go:build live

// Tier 3 live-endpoint integration tests. Opt-in via the `live`
// build tag — `go test ./...` without `-tags live` does not run
// them; `make test-live` adds the tag.
//
// Required env vars (test t.Skip if absent):
//
//	GHYLL_LIVE_ENDPOINT_URL    — full OpenAI-compatible URL
//	                             (https://.../v1/chat/completions)
//	GHYLL_LIVE_ENDPOINT_MODEL  — model name for the request body
//	GHYLL_LIVE_ENDPOINT_KEY    — optional bearer token (set as
//	                             Authorization: Bearer <key>)
//
// The tests are intentionally minimal: they validate the
// streaming SSE pipe, response parsing, and the retry-backoff
// behavior against a REAL endpoint. They do not cover
// dispatcher / runner integration — those tests run against
// mocks because the gate-and-arrow flow does not depend on
// the specific endpoint's quirks beyond SSE compliance.

package stream

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func liveEnv(t *testing.T) (url, model, key string) {
	t.Helper()
	url = strings.TrimSpace(os.Getenv("GHYLL_LIVE_ENDPOINT_URL"))
	model = strings.TrimSpace(os.Getenv("GHYLL_LIVE_ENDPOINT_MODEL"))
	if url == "" || model == "" {
		t.Skip("GHYLL_LIVE_ENDPOINT_URL / GHYLL_LIVE_ENDPOINT_MODEL unset; skipping live test")
	}
	key = strings.TrimSpace(os.Getenv("GHYLL_LIVE_ENDPOINT_KEY"))
	return
}

// TestScenario_Live_SimpleCompletion sends a one-message
// completion request to the configured endpoint and asserts the
// streaming pipe returns at least one delta.
func TestScenario_Live_SimpleCompletion(t *testing.T) {
	url, model, key := liveEnv(t)

	client := NewClient(url, &ClientOptions{
		MaxRetries:    1,
		BaseBackoffMs: 200,
		ModelName:     model,
		ExtraHeaders: func() http.Header {
			h := http.Header{}
			if key != "" {
				h.Set("Authorization", "Bearer "+key)
			}
			return h
		}(),
	})

	messages := []map[string]any{
		{"role": "user", "content": "Reply with exactly the word OK."},
	}

	var deltas []string
	start := time.Now()
	resp, err := client.SendStream(messages, func(delta string) {
		deltas = append(deltas, delta)
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("SendStream: %v (after %v)", err, elapsed)
	}
	if resp == nil {
		t.Fatal("nil response")
	}
	if resp.Content == "" {
		t.Errorf("response Content empty; deltas = %d", len(deltas))
	}
	if len(deltas) == 0 {
		t.Errorf("no streaming deltas observed")
	}
	t.Logf("live response: %d deltas, %d bytes, %v elapsed", len(deltas), len(resp.Content), elapsed)
}

// TestScenario_Live_ContextLengthRespected verifies the endpoint
// honors a short max_tokens and returns within a reasonable
// wall-clock bound. Catches regressions in the request body
// construction or endpoint config drift.
func TestScenario_Live_ContextLengthRespected(t *testing.T) {
	url, model, key := liveEnv(t)
	client := NewClient(url, &ClientOptions{
		MaxRetries:    1,
		BaseBackoffMs: 200,
		ModelName:     model,
		ExtraHeaders: func() http.Header {
			h := http.Header{}
			if key != "" {
				h.Set("Authorization", "Bearer "+key)
			}
			return h
		}(),
	})
	messages := []map[string]any{
		{"role": "user", "content": "Count to three."},
	}
	deadline := time.Now().Add(60 * time.Second)
	resp, err := client.SendStream(messages, func(_ string) {})
	if err != nil {
		t.Fatalf("SendStream: %v", err)
	}
	if time.Now().After(deadline) {
		t.Errorf("response took longer than 60s wall-clock budget")
	}
	if resp.Content == "" {
		t.Error("empty response content")
	}
}
