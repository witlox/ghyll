package tool

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestScenario_Web_SuccessfulFetch maps to:
// Scenario: Successful web fetch
func TestScenario_Web_SuccessfulFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body><h1>Hello</h1><p>World</p></body></html>")
	}))
	defer srv.Close()

	result := WebFetch(context.Background(), srv.URL, 10000, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	// Should contain markdown, not HTML tags
	if strings.Contains(result.Output, "<html>") {
		t.Error("output contains raw HTML")
	}
	if !strings.Contains(result.Output, "Hello") {
		t.Error("expected content in output")
	}
}

// TestScenario_Web_RetriesAndFails maps to:
// Scenario: Web fetch with Tarn blocking — retries and fails
func TestScenario_Web_RetriesAndFails(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		// Simulate connection refused by closing immediately
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, _ := hj.Hijack()
			conn.Close()
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	result := WebFetch(context.Background(), srv.URL, 10000, 30*time.Second)
	if result.Error == "" {
		t.Fatal("expected error")
	}
	if !strings.Contains(result.Error, "not reachable") && !strings.Contains(result.Error, "failed") {
		t.Errorf("error = %q, want reachability error", result.Error)
	}
}

// TestScenario_Web_ApprovedMidRetry maps to:
// Scenario: Web fetch with Tarn blocking — approved mid-retry
func TestScenario_Web_ApprovedMidRetry(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>Success</body></html>")
	}))
	defer srv.Close()

	result := WebFetch(context.Background(), srv.URL, 10000, 30*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "Success") {
		t.Error("expected success content after retry")
	}
}

// TestScenario_Web_404NoRetry maps to:
// Scenario: Web fetch with 404 does not retry
func TestScenario_Web_404NoRetry(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	result := WebFetch(context.Background(), srv.URL, 10000, 5*time.Second)
	if result.Error == "" {
		t.Fatal("expected error")
	}
	if !strings.Contains(result.Error, "404") {
		t.Errorf("error = %q, want 404", result.Error)
	}
	if attempts.Load() != 1 {
		t.Errorf("attempts = %d, want 1 (no retry on 4xx)", attempts.Load())
	}
}

// TestScenario_Web_Timeout maps to:
// Scenario: Web fetch respects tool timeout
func TestScenario_Web_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer srv.Close()

	result := WebFetch(context.Background(), srv.URL, 10000, 200*time.Millisecond)
	if !result.TimedOut {
		if result.Error == "" {
			t.Fatal("expected timeout or error")
		}
	}
}

// TestScenario_Web_SuccessfulSearch maps to:
// Scenario: Successful web search
func TestScenario_Web_SuccessfulSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>
			<a class="result__a" href="https://example.com/go">Go Programming</a>
			<a class="result__snippet">Go is a language.</a>
		</body></html>`)
	}))
	defer srv.Close()

	result := WebSearch(context.Background(), "golang", srv.URL, 10, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Output == "" {
		t.Fatal("expected search results")
	}
}

// TestScenario_Web_SearchBlocked maps to:
// Scenario: Web search with Tarn blocking
func TestScenario_Web_SearchBlocked(t *testing.T) {
	// Use a URL that won't connect
	result := WebSearch(context.Background(), "test", "http://127.0.0.1:1", 10, 5*time.Second)
	if result.Error == "" {
		t.Fatal("expected error")
	}
}

// TestScenario_Web_BinaryContent maps to:
// Scenario: Web fetch returns markdown not binary
func TestScenario_Web_BinaryContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte{0x89, 0x50, 0x4E, 0x47}) // PNG magic bytes
	}))
	defer srv.Close()

	result := WebFetch(context.Background(), srv.URL, 10000, 5*time.Second)
	if result.Error == "" {
		t.Fatal("expected error for binary content")
	}
	if !strings.Contains(result.Error, "binary") {
		t.Errorf("error = %q, want binary error", result.Error)
	}
}

// TestScenario_Web_TruncateLargeResponse maps to:
// Scenario: Web fetch truncates large responses
func TestScenario_Web_TruncateLargeResponse(t *testing.T) {
	// Generate a large page
	largeContent := "<html><body>" + strings.Repeat("<p>Lorem ipsum dolor sit amet. </p>", 5000) + "</body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, largeContent)
	}))
	defer srv.Close()

	result := WebFetch(context.Background(), srv.URL, 500, 5*time.Second) // very small limit
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if !strings.HasSuffix(strings.TrimSpace(result.Output), "[truncated]") {
		t.Error("expected [truncated] marker")
	}
}

// TestScenario_Web_Redirect maps to:
// Scenario: Web fetch with redirect
func TestScenario_Web_Redirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusMovedPermanently)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>New page</body></html>")
	}))
	defer srv.Close()

	result := WebFetch(context.Background(), srv.URL+"/old", 10000, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "New page") {
		t.Error("expected content from redirect target")
	}
}

// TestScenario_Web_5xxRetry maps to:
// Scenario: Web fetch retries on 5xx server error
func TestScenario_Web_5xxRetry(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>Recovered</body></html>")
	}))
	defer srv.Close()

	result := WebFetch(context.Background(), srv.URL, 10000, 30*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "Recovered") {
		t.Error("expected content after 5xx retry")
	}
	if attempts.Load() != 3 {
		t.Errorf("attempts = %d, want 3", attempts.Load())
	}
}

// TestScenario_Web_EmptySearchResults maps to:
// Scenario: Web search returns empty results
func TestScenario_Web_EmptySearchResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>No results found</body></html>")
	}))
	defer srv.Close()

	result := WebSearch(context.Background(), "xyzzy_nonexistent", srv.URL, 10, 5*time.Second)
	// Should not be an error — just empty results
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
}

// TestScenario_Web_ConfigurableMaxTokens maps to:
// Scenario: Web fetch max response tokens configurable
func TestScenario_Web_ConfigurableMaxTokens(t *testing.T) {
	content := "<html><body>" + strings.Repeat("<p>Word </p>", 1000) + "</body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, content)
	}))
	defer srv.Close()

	// Small budget
	result := WebFetch(context.Background(), srv.URL, 100, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if !strings.HasSuffix(strings.TrimSpace(result.Output), "[truncated]") {
		t.Error("expected truncation at small budget")
	}
}

// TestScenario_Web_SearchLimit maps to:
// Scenario: Web search results limited to 10
func TestScenario_Web_SearchLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := "<html><body>"
		for i := 0; i < 20; i++ {
			html += fmt.Sprintf(`<a class="result__a" href="https://example.com/%d">Result %d</a>`, i, i)
		}
		html += "</body></html>"
		fmt.Fprint(w, html)
	}))
	defer srv.Close()

	result := WebSearch(context.Background(), "test", srv.URL, 10, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	// Count result lines (each result is on its own line)
	lines := strings.Split(strings.TrimSpace(result.Output), "\n")
	count := 0
	for _, l := range lines {
		if strings.Contains(l, "http") {
			count++
		}
	}
	if count > 10 {
		t.Errorf("got %d results, want at most 10", count)
	}
}
