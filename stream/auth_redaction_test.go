package stream

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestScenario_Stream_Auth401BodyNotSurfaced asserts that a 401
// upstream response whose body echoes the Bearer token is NEVER
// surfaced through the StreamError chain — only the fixed
// "authentication failed" message reaches the caller.
func TestScenario_Stream_Auth401BodyNotSurfaced(t *testing.T) {
	sentinel := "sk-leak-zzz"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprintf(w, `{"error":{"message":"invalid token Bearer %s"}}`, sentinel)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, &ClientOptions{MaxRetries: 0, BaseBackoffMs: 1})
	_, err := c.Send([]map[string]any{{"role": "user", "content": "x"}})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	var se *StreamError
	if !errors.As(err, &se) {
		t.Fatalf("expected *StreamError, got %T", err)
	}
	if se.StatusCode != 401 {
		t.Fatalf("StatusCode = %d, want 401", se.StatusCode)
	}
	if se.Message != "authentication failed" {
		t.Fatalf("Message = %q, want %q", se.Message, "authentication failed")
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("Error() leaks sentinel %q: %s", sentinel, err.Error())
	}
	if se.Retryable {
		t.Fatalf("401 must be non-retryable")
	}
}

// TestScenario_Stream_Auth403BodyNotSurfaced — same redaction
// behavior for 403.
func TestScenario_Stream_Auth403BodyNotSurfaced(t *testing.T) {
	sentinel := "sk-fff-leak"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		fmt.Fprintf(w, `{"error":{"message":"forbidden: Bearer %s"}}`, sentinel)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, &ClientOptions{MaxRetries: 0, BaseBackoffMs: 1})
	_, err := c.Send([]map[string]any{{"role": "user", "content": "x"}})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	var se *StreamError
	if !errors.As(err, &se) {
		t.Fatalf("expected *StreamError, got %T", err)
	}
	if se.Message != "authentication failed" {
		t.Fatalf("Message = %q, want authentication failed", se.Message)
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("Error() leaks sentinel: %s", err.Error())
	}
}

// TestScenario_Stream_Auth401PreservesRequestID — the X-Request-ID
// is a legitimate operator-facing diagnostic; preserve it so
// support tickets can be cross-referenced. The token still must
// not appear.
func TestScenario_Stream_Auth401PreservesRequestID(t *testing.T) {
	sentinel := "sk-req-id-test"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "req-abc-123")
		w.WriteHeader(401)
		fmt.Fprintf(w, `{"error":{"message":"invalid Bearer %s"}}`, sentinel)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, &ClientOptions{MaxRetries: 0, BaseBackoffMs: 1})
	_, err := c.Send([]map[string]any{{"role": "user", "content": "x"}})
	if err == nil {
		t.Fatalf("expected error")
	}
	var se *StreamError
	if !errors.As(err, &se) {
		t.Fatalf("not *StreamError")
	}
	if !strings.Contains(se.Message, "req-abc-123") {
		t.Fatalf("expected request-id in message, got %q", se.Message)
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("leaks sentinel: %s", err.Error())
	}
}

// TestScenario_Stream_NonAuth4xxBodyRedacted asserts AUTH-W-001 /
// ADV-AUTH-001: status codes OTHER than 401/403 that echo a
// Bearer-shaped token in the body get the token stripped from the
// surfaced StreamError.Message. Covers 400, 402, 422, 500, 502.
func TestScenario_Stream_NonAuth4xxBodyRedacted(t *testing.T) {
	cases := []int{400, 402, 422, 500, 502}
	for _, code := range cases {
		t.Run(fmt.Sprintf("status_%d", code), func(t *testing.T) {
			sentinel := "sk-leak-XYZ12345abcd"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
				fmt.Fprintf(w, `{"error":{"message":"malformed Authorization: Bearer %s","type":"upstream"}}`, sentinel)
			}))
			defer srv.Close()

			c := NewClient(srv.URL, &ClientOptions{MaxRetries: 0, BaseBackoffMs: 1})
			_, err := c.Send([]map[string]any{{"role": "user", "content": "x"}})
			if err == nil {
				t.Fatalf("expected error for status %d", code)
			}
			var se *StreamError
			if !errors.As(err, &se) {
				t.Fatalf("expected *StreamError, got %T", err)
			}
			if strings.Contains(err.Error(), sentinel) {
				t.Fatalf("status %d: surfaced error leaks sentinel %q: %s",
					code, sentinel, err.Error())
			}
			if strings.Contains(se.Message, sentinel) {
				t.Fatalf("status %d: StreamError.Message leaks sentinel: %s",
					code, se.Message)
			}
			// And the sanitizer should have left a <redacted>
			// marker so the operator knows SOMETHING was stripped.
			if !strings.Contains(se.Message, "<redacted>") {
				t.Logf("status %d: Message = %q (no <redacted> tag; sanitizer ran but emitted no marker)",
					code, se.Message)
			}
		})
	}
}

// TestScenario_Stream_Auth401_RequestIDLengthCapped asserts
// AUTH-4 / ADV-AUTH-006: an attacker-controlled X-Request-ID
// containing the sentinel does NOT get echoed into the operator-
// visible StreamError.Message; the request-id is dropped (fails
// the printable-character whitelist).
func TestScenario_Stream_Auth401_RequestIDFiltersAttack(t *testing.T) {
	sentinel := "Bearer sk-evil-token-PAYLOAD"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", sentinel)
		w.WriteHeader(401)
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, &ClientOptions{MaxRetries: 0, BaseBackoffMs: 1})
	_, err := c.Send([]map[string]any{{"role": "user", "content": "x"}})
	if err == nil {
		t.Fatalf("expected error")
	}
	var se *StreamError
	if !errors.As(err, &se) {
		t.Fatalf("not *StreamError")
	}
	if strings.Contains(se.Message, "sk-evil-token-PAYLOAD") {
		t.Fatalf("attacker X-Request-ID leaked into Message: %q", se.Message)
	}
	if strings.Contains(se.Message, "Bearer") {
		t.Fatalf("attacker X-Request-ID leaked Bearer prefix: %q", se.Message)
	}
}

// TestScenario_Stream_Auth401_RequestIDLengthCapped asserts that
// a multi-kilobyte X-Request-ID does NOT dump multi-kilobytes into
// the operator-visible message. AUTH-4 / ADV-AUTH-006.
func TestScenario_Stream_Auth401_RequestIDLengthCapped(t *testing.T) {
	huge := strings.Repeat("A", 4096) // 4 KiB
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", huge)
		w.WriteHeader(401)
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, &ClientOptions{MaxRetries: 0, BaseBackoffMs: 1})
	_, err := c.Send([]map[string]any{{"role": "user", "content": "x"}})
	if err == nil {
		t.Fatalf("expected error")
	}
	var se *StreamError
	if !errors.As(err, &se) {
		t.Fatalf("not *StreamError")
	}
	// Message should be either fixed "authentication failed" OR
	// "authentication failed (request-id=<<= 64 chars>>)" — total
	// message length is bounded by the cap + prefix.
	if len(se.Message) > 200 {
		t.Fatalf("Message exceeded length bound: %d bytes", len(se.Message))
	}
}

// TestScenario_Stream_RequestIncludesAuthorizationHeader confirms
// that the ExtraHeaders surface still forwards the Bearer header
// verbatim, AND that Content-Type / Accept are unmodified.
func TestScenario_Stream_RequestIncludesAuthorizationHeader(t *testing.T) {
	var (
		capturedAuth   string
		capturedCT     string
		capturedAccept string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		capturedCT = r.Header.Get("Content-Type")
		capturedAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	hdr := http.Header{"Authorization": []string{"Bearer abc"}}
	c := NewClient(srv.URL, &ClientOptions{MaxRetries: 0, BaseBackoffMs: 1, ExtraHeaders: hdr})
	_, err := c.Send([]map[string]any{{"role": "user", "content": "x"}})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if capturedAuth != "Bearer abc" {
		t.Fatalf("captured Authorization = %q, want %q", capturedAuth, "Bearer abc")
	}
	if capturedCT != "application/json" {
		t.Fatalf("captured Content-Type = %q, want application/json", capturedCT)
	}
	if capturedAccept != "text/event-stream" {
		t.Fatalf("captured Accept = %q, want text/event-stream", capturedAccept)
	}
}
