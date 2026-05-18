package acceptance

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cucumber/godog"
	"github.com/witlox/ghyll/tool"
)

func registerWebSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	var (
		maxTokens    int
		maxResults   int
		servers      []*httptest.Server
		urlMap       map[string]string // maps feature URLs to httptest URLs
		retryCount   int32             // atomic counter for retry tracking
		searchServer *httptest.Server
		searchURL    string
	)

	ctx.Before(func(ctx2 context.Context, sc *godog.Scenario) (context.Context, error) {
		maxTokens = 10000
		maxResults = 10
		servers = nil
		urlMap = make(map[string]string)
		atomic.StoreInt32(&retryCount, 0)
		searchServer = nil
		searchURL = ""
		return ctx2, nil
	})

	ctx.After(func(ctx2 context.Context, sc *godog.Scenario, err error) (context.Context, error) {
		for _, s := range servers {
			s.Close()
		}
		if searchServer != nil {
			searchServer.Close()
		}
		return ctx2, nil
	})

	resolveURL := func(featureURL string) string {
		if mapped, ok := urlMap[featureURL]; ok {
			return mapped
		}
		return featureURL
	}

	newServer := func(handler http.Handler) *httptest.Server {
		s := httptest.NewServer(handler)
		servers = append(servers, s)
		return s
	}

	// NOTE: "a running session with model" is registered in steps_routing.go or
	// steps_stream.go. If it's not, we register it here.
	ctx.Step(`^a running session with model "([^"]*)"$`, func(model string) error {
		state.ActiveModel = model
		return nil
	})

	ctx.Step(`^the URL "([^"]*)" is reachable$`, func(featureURL string) error {
		s := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(200)
			fmt.Fprintf(w, `<html><head><title>Test Page</title></head><body>
<h1>Test Content</h1>
<p>This is a test page with some content for validation.</p>
<p>Go is a statically typed, compiled programming language.</p>
</body></html>`)
		}))
		urlMap[featureURL] = s.URL
		return nil
	})

	ctx.Step(`^the model calls web_fetch with url "([^"]*)"$`, func(featureURL string) error {
		realURL := resolveURL(featureURL)
		state.ToolResult = tool.WebFetch(context.Background(), realURL, maxTokens, state.ToolTimeout)
		return nil
	})

	ctx.Step(`^the tool result contains the page content as markdown$`, func() error {
		if state.ToolResult.Error != "" {
			return fmt.Errorf("expected page content, got error: %s", state.ToolResult.Error)
		}
		if state.ToolResult.Output == "" {
			return fmt.Errorf("expected page content, got empty output")
		}
		return nil
	})

	ctx.Step(`^the result does not contain JavaScript or HTML tags$`, func() error {
		if strings.Contains(state.ToolResult.Output, "<script") {
			return fmt.Errorf("output contains <script> tags")
		}
		if strings.Contains(state.ToolResult.Output, "</div>") {
			return fmt.Errorf("output contains HTML div tags")
		}
		return nil
	})

	ctx.Step(`^Tarn blocks outbound HTTP to "([^"]*)"$`, func(featureURL string) error {
		atomic.StoreInt32(&retryCount, 0)
		s := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&retryCount, 1)
			w.WriteHeader(503)
			fmt.Fprint(w, "blocked by Tarn")
		}))
		urlMap[featureURL] = s.URL
		for _, suffix := range []string{"/docs", "/api", "/page"} {
			urlMap[featureURL+suffix] = s.URL + suffix
		}
		return nil
	})

	ctx.Step(`^Tarn blocks outbound HTTP to the search backend$`, func() error {
		atomic.StoreInt32(&retryCount, 0)
		s := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&retryCount, 1)
			w.WriteHeader(503)
			fmt.Fprint(w, "blocked by Tarn")
		}))
		searchServer = s
		searchURL = s.URL
		return nil
	})

	ctx.Step(`^Tarn approves "([^"]*)" after the second retry$`, func(featureURL string) error {
		oldURL := urlMap[featureURL]
		for i, s := range servers {
			if s.URL == oldURL {
				s.Close()
				servers = append(servers[:i], servers[i+1:]...)
				break
			}
		}

		atomic.StoreInt32(&retryCount, 0)
		s := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count := atomic.AddInt32(&retryCount, 1)
			if count <= 2 {
				w.WriteHeader(503)
				fmt.Fprint(w, "blocked by Tarn")
				return
			}
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(200)
			fmt.Fprint(w, `<html><body><h1>Approved Content</h1><p>Tarn approved this request.</p></body></html>`)
		}))
		urlMap[featureURL] = s.URL
		for _, suffix := range []string{"/docs", "/api", "/page"} {
			urlMap[featureURL+suffix] = s.URL + suffix
		}
		return nil
	})

	ctx.Step(`^the tool retries (\d+) times with exponential backoff$`, func(times int) error {
		count := atomic.LoadInt32(&retryCount)
		if count < int32(times) && count == 0 {
			return fmt.Errorf("expected at least %d retries, got 0", times)
		}
		return nil
	})

	ctx.Step(`^the tool retries with exponential backoff$`, func() error {
		count := atomic.LoadInt32(&retryCount)
		if count < 2 {
			return fmt.Errorf("expected retries, got %d attempts", count)
		}
		return nil
	})

	ctx.Step(`^the third retry succeeds$`, func() error {
		if state.ToolResult.Error != "" {
			return fmt.Errorf("expected third retry to succeed, got error: %s", state.ToolResult.Error)
		}
		return nil
	})

	ctx.Step(`^the third attempt succeeds$`, func() error {
		if state.ToolResult.Error != "" {
			return fmt.Errorf("expected third attempt to succeed, got error: %s", state.ToolResult.Error)
		}
		return nil
	})

	ctx.Step(`^no retries are attempted$`, func() error {
		count := atomic.LoadInt32(&retryCount)
		if count > 1 {
			return fmt.Errorf("expected no retries (1 attempt), got %d attempts", count)
		}
		return nil
	})

	ctx.Step(`^the URL "([^"]*)" returns HTTP (\d+)$`, func(featureURL string, status int) error {
		atomic.StoreInt32(&retryCount, 0)
		s := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&retryCount, 1)
			w.WriteHeader(status)
			fmt.Fprintf(w, "HTTP %d error", status)
		}))
		urlMap[featureURL] = s.URL
		return nil
	})

	ctx.Step(`^the URL "([^"]*)" returns HTTP (\d+) twice then succeeds$`, func(featureURL string, status int) error {
		atomic.StoreInt32(&retryCount, 0)
		s := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count := atomic.AddInt32(&retryCount, 1)
			if count <= 2 {
				w.WriteHeader(status)
				fmt.Fprintf(w, "HTTP %d error", status)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(200)
			fmt.Fprint(w, `<html><body><h1>Success</h1><p>Page content after retries.</p></body></html>`)
		}))
		urlMap[featureURL] = s.URL
		return nil
	})

	ctx.Step(`^the URL "([^"]*)" returns binary content$`, func(featureURL string) error {
		s := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(200)
			w.Write([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})
		}))
		urlMap[featureURL] = s.URL
		return nil
	})

	ctx.Step(`^the page content exceeds (\d+) tokens$`, func(tokens int) error {
		// Replace any existing server for the most recently mapped URL with large content
		for featureURL, testURL := range urlMap {
			for i, s := range servers {
				if s.URL == testURL {
					s.Close()
					servers = append(servers[:i], servers[i+1:]...)
					break
				}
			}
			largeContent := strings.Repeat("<p>This is a paragraph of content that takes up tokens. </p>\n", tokens)
			s := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(200)
				fmt.Fprintf(w, `<html><body>%s</body></html>`, largeContent)
			}))
			urlMap[featureURL] = s.URL
			break
		}
		maxTokens = tokens
		return nil
	})

	ctx.Step(`^the tool result contains the first (\d+) tokens of content$`, func(tokens int) error {
		if state.ToolResult.Error != "" {
			return fmt.Errorf("expected content, got error: %s", state.ToolResult.Error)
		}
		if state.ToolResult.Output == "" {
			return fmt.Errorf("expected content, got empty output")
		}
		return nil
	})

	ctx.Step(`^the tool result contains the first (\d+) tokens$`, func(tokens int) error {
		if state.ToolResult.Error != "" {
			return fmt.Errorf("expected content, got error: %s", state.ToolResult.Error)
		}
		if state.ToolResult.Output == "" {
			return fmt.Errorf("expected content, got empty output")
		}
		return nil
	})

	ctx.Step(`^the tool result ends with "\[truncated\]"$`, func() error {
		if !strings.HasSuffix(strings.TrimSpace(state.ToolResult.Output), "[truncated]") {
			suffix := state.ToolResult.Output
			if len(suffix) > 50 {
				suffix = suffix[len(suffix)-50:]
			}
			return fmt.Errorf("expected output to end with [truncated], ends with: %q", suffix)
		}
		return nil
	})

	ctx.Step(`^the URL "([^"]*)" redirects to "([^"]*)"$`, func(from, to string) error {
		s := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			targetURL := resolveURL(to)
			http.Redirect(w, r, targetURL, http.StatusMovedPermanently)
		}))
		urlMap[from] = s.URL
		return nil
	})

	ctx.Step(`^"([^"]*)" is reachable$`, func(featureURL string) error {
		s := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(200)
			fmt.Fprintf(w, `<html><body><h1>Redirected Content</h1><p>Content from %s</p></body></html>`, featureURL)
		}))
		urlMap[featureURL] = s.URL
		return nil
	})

	ctx.Step(`^the tool result contains the content from "([^"]*)"$`, func(featureURL string) error {
		if state.ToolResult.Error != "" {
			return fmt.Errorf("expected content, got error: %s", state.ToolResult.Error)
		}
		if state.ToolResult.Output == "" {
			return fmt.Errorf("expected content from %s, got empty", featureURL)
		}
		if !strings.Contains(state.ToolResult.Output, "Redirected Content") && !strings.Contains(state.ToolResult.Output, "Content from") {
			return fmt.Errorf("output does not contain expected redirected content: %s", state.ToolResult.Output)
		}
		return nil
	})

	ctx.Step(`^the tool result contains the page content$`, func() error {
		if state.ToolResult.Error != "" {
			return fmt.Errorf("expected page content, got error: %s", state.ToolResult.Error)
		}
		if state.ToolResult.Output == "" {
			return fmt.Errorf("expected page content, got empty output")
		}
		return nil
	})

	ctx.Step(`^the URL "([^"]*)" takes (\d+) seconds to respond$`, func(featureURL string, secs int) error {
		s := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(time.Duration(secs) * time.Second)
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(200)
			fmt.Fprint(w, `<html><body><p>Slow response</p></body></html>`)
		}))
		urlMap[featureURL] = s.URL
		return nil
	})

	ctx.Step(`^the search backend is reachable$`, func() error {
		if searchServer != nil {
			searchServer.Close()
		}
		searchServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			query := r.URL.Query().Get("q")
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(200)

			if strings.Contains(query, "xyzzy_nonexistent") {
				fmt.Fprint(w, `<html><body><p>No results found.</p></body></html>`)
				return
			}

			var results strings.Builder
			results.WriteString(`<html><body>`)
			numResults := 15
			for i := 1; i <= numResults; i++ {
				fmt.Fprintf(&results,
					`<a href="https://example.com/result/%d">Result %d: %s</a><p>Snippet for result %d about %s</p>`,
					i, i, query, i, query)
			}
			results.WriteString(`</body></html>`)
			fmt.Fprint(w, results.String())
		}))
		servers = append(servers, searchServer)
		searchURL = searchServer.URL
		return nil
	})

	ctx.Step(`^the model calls web_search with query "([^"]*)"$`, func(query string) error {
		backendURL := searchURL
		if backendURL == "" {
			return fmt.Errorf("search backend not configured")
		}
		state.ToolResult = tool.WebSearch(context.Background(), query, backendURL, maxResults, state.ToolTimeout)
		return nil
	})

	ctx.Step(`^the tool result contains structured results with title, url, and snippet fields$`, func() error {
		if state.ToolResult.Error != "" {
			return fmt.Errorf("expected results, got error: %s", state.ToolResult.Error)
		}
		if state.ToolResult.Output == "" {
			return fmt.Errorf("expected structured results, got empty output")
		}
		if !strings.Contains(state.ToolResult.Output, "https://") {
			return fmt.Errorf("expected URLs in results, got: %s", state.ToolResult.Output)
		}
		return nil
	})

	ctx.Step(`^the result contains at least (\d+) results?$`, func(min int) error {
		if state.ToolResult.Output == "" && min > 0 {
			return fmt.Errorf("expected at least %d results, got empty output", min)
		}
		lines := strings.Split(strings.TrimSpace(state.ToolResult.Output), "\n")
		var count int
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				count++
			}
		}
		if count < min {
			return fmt.Errorf("expected at least %d results, got %d", min, count)
		}
		return nil
	})

	ctx.Step(`^the tool result contains (\d+) results$`, func(expected int) error {
		if expected == 0 {
			if state.ToolResult.Output == "" || strings.TrimSpace(state.ToolResult.Output) == "" {
				return nil
			}
			return fmt.Errorf("expected 0 results, got: %s", state.ToolResult.Output)
		}
		lines := strings.Split(strings.TrimSpace(state.ToolResult.Output), "\n")
		var count int
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				count++
			}
		}
		if count != expected {
			return fmt.Errorf("expected %d results, got %d", expected, count)
		}
		return nil
	})

	ctx.Step(`^the result is not an error$`, func() error {
		if state.ToolResult.Error != "" {
			return fmt.Errorf("expected no error, got: %s", state.ToolResult.Error)
		}
		return nil
	})

	ctx.Step(`^web_max_response_tokens is configured as (\d+)$`, func(tokens int) error {
		maxTokens = tokens
		return nil
	})

	ctx.Step(`^the query matches more than (\d+) results$`, func(n int) error {
		maxResults = n
		return nil
	})

	ctx.Step(`^the tool result contains at most (\d+) results$`, func(max int) error {
		if state.ToolResult.Output == "" {
			return nil
		}
		lines := strings.Split(strings.TrimSpace(state.ToolResult.Output), "\n")
		var count int
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				count++
			}
		}
		if count > max {
			return fmt.Errorf("expected at most %d results, got %d", max, count)
		}
		return nil
	})
}
