package tool

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/witlox/ghyll/internal/safefile"
	"github.com/witlox/ghyll/types"
)

// WebFetch retrieves a URL and returns content as markdown.
// Invariant 44: retries 3 times with exponential backoff on transient errors.
// Invariant 45: response truncated to maxTokens with "[truncated]" marker.
func WebFetch(ctx context.Context, url string, maxTokens int, timeout time.Duration) types.ToolResult {
	start := time.Now()

	done := make(chan types.ToolResult, 1)
	go func() {
		done <- webFetchImpl(ctx, url, maxTokens, timeout)
	}()

	select {
	case result := <-done:
		result.Duration = time.Since(start)
		return result
	case <-time.After(timeout):
		return types.ToolResult{
			Error:    fmt.Sprintf("web fetch timed out after %s", timeout),
			TimedOut: true,
			Duration: time.Since(start),
		}
	case <-ctx.Done():
		return types.ToolResult{
			Error:    fmt.Sprintf("web fetch cancelled: %v", ctx.Err()),
			TimedOut: true,
			Duration: time.Since(start),
		}
	}
}

func webFetchImpl(ctx context.Context, url string, maxTokens int, timeout time.Duration) types.ToolResult {
	client := &http.Client{Timeout: timeout}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return types.ToolResult{Error: fmt.Sprintf("web fetch cancelled: %v", ctx.Err()), TimedOut: true}
			}
		}

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return types.ToolResult{Error: fmt.Sprintf("invalid URL: %v", err)}
		}
		req.Header.Set("User-Agent", "ghyll/1.0")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue // Retry on connection error
		}

		// 4xx: non-retryable
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			_ = resp.Body.Close()
			return types.ToolResult{Error: fmt.Sprintf("HTTP %d", resp.StatusCode)}
		}

		// 5xx: retryable
		if resp.StatusCode >= 500 {
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}

		// Check content type — reject binary
		contentType := resp.Header.Get("Content-Type")
		if isBinaryContentType(contentType) {
			_ = resp.Body.Close()
			return types.ToolResult{Error: "binary content not supported"}
		}

		// Limit read to prevent OOM on large responses.
		// Read up to 2x the token budget in chars to allow for HTML overhead.
		maxReadBytes := int64(maxTokens*4*2) + 4096
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxReadBytes))
		_ = resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		// Convert HTML to markdown
		markdown := htmlToMarkdown(string(body))

		// Truncate if needed (invariant 45)
		// Approximate: 1 token ≈ 4 chars
		maxChars := maxTokens * 4
		if len(markdown) > maxChars {
			markdown = markdown[:maxChars] + "\n[truncated]"
		}

		return types.ToolResult{Output: markdown}
	}

	if lastErr != nil {
		return types.ToolResult{Error: fmt.Sprintf("domain not reachable after 3 attempts: %v", lastErr)}
	}
	return types.ToolResult{Error: "web fetch failed"}
}

// WebSearch queries a search backend and returns structured results.
// Invariant 44: retries on transient failure.
// Invariant 45: limited to maxResults.
func WebSearch(ctx context.Context, query, backendURL string, maxResults int, timeout time.Duration) types.ToolResult {
	start := time.Now()

	done := make(chan types.ToolResult, 1)
	go func() {
		done <- webSearchImpl(ctx, query, backendURL, maxResults, timeout)
	}()

	select {
	case result := <-done:
		result.Duration = time.Since(start)
		return result
	case <-time.After(timeout):
		return types.ToolResult{
			Error:    fmt.Sprintf("web search timed out after %s", timeout),
			TimedOut: true,
			Duration: time.Since(start),
		}
	case <-ctx.Done():
		return types.ToolResult{
			Error:    fmt.Sprintf("web search cancelled: %v", ctx.Err()),
			TimedOut: true,
			Duration: time.Since(start),
		}
	}
}

// maxSearchResponseBytes caps WebSearch's body read at 1 MiB.
// Tier 3 / SR C-7: previously unbounded io.ReadAll.
const maxSearchResponseBytes int64 = 1 * 1024 * 1024

func webSearchImpl(ctx context.Context, query, backendURL string, maxResults int, timeout time.Duration) types.ToolResult {
	client := &http.Client{Timeout: timeout}

	searchURL := fmt.Sprintf("%s/?q=%s&format=json", backendURL, url.QueryEscape(query))

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return types.ToolResult{Error: fmt.Sprintf("web search cancelled: %v", ctx.Err()), TimedOut: true}
			}
		}

		req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
		if err != nil {
			return types.ToolResult{Error: fmt.Sprintf("invalid search URL: %v", err)}
		}
		req.Header.Set("User-Agent", "ghyll/1.0")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode >= 500 {
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}

		if resp.StatusCode >= 400 {
			_ = resp.Body.Close()
			return types.ToolResult{Error: fmt.Sprintf("search HTTP %d", resp.StatusCode)}
		}

		// Tier 3 / SR C-7: cap response body at
		// maxSearchResponseBytes (1 MiB). Previously unbounded
		// io.ReadAll could OOM the agent on a hostile / DNS-
		// hijacked search backend returning a multi-GB payload.
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchResponseBytes))
		_ = resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		// Parse results from HTML (simple extraction)
		results := parseSearchResults(string(body), maxResults)
		return types.ToolResult{Output: results}
	}

	if lastErr != nil {
		return types.ToolResult{Error: fmt.Sprintf("search backend not reachable after 3 attempts: %v", lastErr)}
	}
	return types.ToolResult{Error: "web search failed"}
}

func isBinaryContentType(ct string) bool {
	ct = strings.ToLower(ct)
	textTypes := []string{"text/", "application/json", "application/xml", "application/xhtml"}
	for _, t := range textTypes {
		if strings.Contains(ct, t) {
			return false
		}
	}
	// If no content type, assume text
	if ct == "" {
		return false
	}
	return true
}

var (
	reTag       = regexp.MustCompile(`<[^>]+>`)
	reMultiNL   = regexp.MustCompile(`\n{3,}`)
	reMultiSp   = regexp.MustCompile(`[ \t]{2,}`)
	reScript    = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle     = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reComment   = regexp.MustCompile(`(?s)<!--.*?-->`)
	reHeading   = regexp.MustCompile(`(?i)<h([1-6])[^>]*>(.*?)</h[1-6]>`)
	rePara      = regexp.MustCompile(`(?is)<p[^>]*>(.*?)</p>`)
	reLink      = regexp.MustCompile(`(?i)<a[^>]+href="([^"]*)"[^>]*>(.*?)</a>`)
	reLi        = regexp.MustCompile(`(?is)<li[^>]*>(.*?)</li>`)
	reSearchURL = regexp.MustCompile(`(?i)href="(https?://[^"]*)"`)
)

// maxHTMLBytesForRegex caps the input to the regex-based HTML
// stripper at 256 KiB. Tier 3 / SR M-2: <script[^>]*>.*?</script>
// non-greedy regex over MiB-class buffers can CPU-stall via
// backtracking on hostile input (missing close tag). Truncate
// before passing; the LimitReader at the caller already caps at
// maxReadBytes, but defense-in-depth.
const maxHTMLBytesForRegex = 256 * 1024

// htmlToMarkdown converts HTML to a basic markdown representation.
func htmlToMarkdown(htmlIn string) string {
	s := htmlIn
	if len(s) > maxHTMLBytesForRegex {
		s = s[:maxHTMLBytesForRegex]
	}
	// Remove scripts, styles, comments
	s = reScript.ReplaceAllString(s, "")
	s = reStyle.ReplaceAllString(s, "")
	s = reComment.ReplaceAllString(s, "")

	// Convert headings
	s = reHeading.ReplaceAllStringFunc(s, func(match string) string {
		sub := reHeading.FindStringSubmatch(match)
		if len(sub) < 3 {
			return match
		}
		level := sub[1]
		text := reTag.ReplaceAllString(sub[2], "")
		prefix := strings.Repeat("#", int(level[0]-'0'))
		return "\n" + prefix + " " + strings.TrimSpace(text) + "\n"
	})

	// Convert links
	s = reLink.ReplaceAllStringFunc(s, func(match string) string {
		sub := reLink.FindStringSubmatch(match)
		if len(sub) < 3 {
			return match
		}
		text := reTag.ReplaceAllString(sub[2], "")
		return fmt.Sprintf("[%s](%s)", strings.TrimSpace(text), sub[1])
	})

	// Convert paragraphs
	s = rePara.ReplaceAllStringFunc(s, func(match string) string {
		sub := rePara.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		text := reTag.ReplaceAllString(sub[1], "")
		return "\n" + strings.TrimSpace(text) + "\n"
	})

	// Convert list items
	s = reLi.ReplaceAllStringFunc(s, func(match string) string {
		sub := reLi.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		text := reTag.ReplaceAllString(sub[1], "")
		return "- " + strings.TrimSpace(text) + "\n"
	})

	// Strip remaining tags
	s = reTag.ReplaceAllString(s, "")

	// Clean up whitespace. Tier 3 / SR M-1: full html.UnescapeString
	// covers numeric entities (&#x202E; RTL override, &#xfeff; BOM)
	// the hand-rolled list missed. Strip any control-class runes
	// AND U+202D/U+202E afterward — RTL-override smuggling into the
	// model's context would otherwise reorder the prompt text
	// invisibly.
	s = html.UnescapeString(s)
	s = stripDangerousRunes(s)
	s = reMultiSp.ReplaceAllString(s, " ")
	s = reMultiNL.ReplaceAllString(s, "\n\n")
	s = strings.TrimSpace(s)

	return s
}

// stripDangerousRunes drops C0/C1 controls (except \n/\r/\t) and
// Unicode format characters (Cf class — RTL/LTR overrides, ZWSP,
// ZWJ). Tier 3 / SR M-1 helper.
func stripDangerousRunes(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7F:
			// drop
		case unicode.Is(unicode.Cf, r):
			// drop format runes (RTL override, ZWSP, ZWJ, BOM, …)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// parseSearchResults extracts search results from HTML response.
func parseSearchResults(html string, maxResults int) string {
	// Extract URLs from result links
	matches := reSearchURL.FindAllStringSubmatch(html, -1)

	var results []string
	seen := make(map[string]bool)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		u := m[1]
		// Skip internal/navigation links
		if strings.Contains(u, "duckduckgo") || strings.Contains(u, "javascript:") {
			continue
		}
		// Tier 3 / SR L-7: scheme allowlist — refuse data://,
		// file://, ext::, etc. that could otherwise flow into
		// a later WebFetch call.
		if err := safefile.ValidateURLScheme(u, "http", "https"); err != nil {
			continue
		}
		if seen[u] {
			continue
		}
		seen[u] = true
		results = append(results, u)
		if len(results) >= maxResults {
			break
		}
	}

	if len(results) == 0 {
		return ""
	}

	var lines []string
	for i, u := range results {
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, u))
	}
	return strings.Join(lines, "\n")
}
