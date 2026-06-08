package stream

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/witlox/ghyll/types"
)

var (
	ErrStreamInterrupted = errors.New("stream: connection dropped mid-response")
	ErrAllTiersDown      = errors.New("stream: all model endpoints unreachable")
	ErrModelLocked       = errors.New("stream: locked model endpoint unreachable")
	ErrRateLimited       = errors.New("stream: rate limited")
)

// StreamError includes retry/fallback classification.
type StreamError struct {
	StatusCode     int
	Retryable      bool
	RetryAfter     int // seconds, from Retry-After header
	ContextTooLong bool
	Message        string
	Err            error
}

func (e *StreamError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("stream: HTTP %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("stream: HTTP %d", e.StatusCode)
}

func (e *StreamError) Unwrap() error {
	return e.Err
}

// AsStreamError extracts a *StreamError from an error chain.
func AsStreamError(err error, target **StreamError) bool {
	return errors.As(err, target)
}

// Response is the assembled result of a streaming API call.
//
// ReasoningContent accumulates dialect-side `reasoning_content`
// chunks (Kimi 2.5/2.6) so the session loop can populate the
// matching field on the appended assistant `types.Message`. Without
// this, ADR-v4-009's reasoning round-trip would be one-way only:
// outbound assistant turns would have to re-fetch the trace on
// every cycle.
type Response struct {
	Content          string
	ReasoningContent string
	ToolCalls        []types.ToolCall
	Usage            Usage
	FinishReason     string
	Partial          bool
	RawToolCalls     json.RawMessage
}

// Usage tracks token counts from the API response.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// ClientOptions configures retry behavior.
type ClientOptions struct {
	MaxRetries    int    // default 3
	BaseBackoffMs int    // default 1000
	ModelName     string // optional, sent as "model" in API request

	// ExtraHeaders are merged onto every outgoing request. Tier 3
	// live-endpoint tests use this to set Authorization: Bearer
	// <key>; production session wiring leaves it nil (the endpoint
	// config supplies credentials elsewhere).
	ExtraHeaders http.Header
}

// Client is the SSE streaming client for OpenAI-compatible endpoints.
type Client struct {
	endpoint   string
	httpClient *http.Client
	opts       ClientOptions
}

// NewClient creates a streaming client for the given endpoint.
func NewClient(endpoint string, opts *ClientOptions) *Client {
	c := &Client{
		endpoint:   endpoint,
		httpClient: &http.Client{Timeout: 0}, // no timeout — streaming
		opts: ClientOptions{
			MaxRetries:    3,
			BaseBackoffMs: 1000,
		},
	}
	if opts != nil {
		if opts.MaxRetries >= 0 {
			c.opts.MaxRetries = opts.MaxRetries
		}
		if opts.BaseBackoffMs > 0 {
			c.opts.BaseBackoffMs = opts.BaseBackoffMs
		}
		if opts.ModelName != "" {
			c.opts.ModelName = opts.ModelName
		}
		if opts.ExtraHeaders != nil {
			c.opts.ExtraHeaders = opts.ExtraHeaders.Clone()
		}
	}
	return c
}

// OnDelta is called for each content delta during streaming.
// Allows real-time terminal rendering as tokens arrive.
type OnDelta func(delta string)

// Send sends messages and returns the complete response (no streaming callback).
// Used for compaction calls and testing.
func (c *Client) Send(messages []map[string]any) (*Response, error) {
	return c.SendStream(messages, nil)
}

// SendStream sends messages with real-time delta streaming.
// The onDelta callback is invoked for each content token as it arrives.
// Retries on 5xx with exponential backoff (invariant 18).
func (c *Client) SendStream(messages []map[string]any, onDelta OnDelta) (*Response, error) {
	var lastErr error

	for attempt := 0; attempt <= c.opts.MaxRetries; attempt++ {
		if attempt > 0 {
			// Tier 3 / SR L-6: clamp shift count at 6 so
			// attempt-1 ≤ 6 → 1 << ≤ 64. Without the cap,
			// MaxRetries > 31 overflowed the shift to a
			// negative duration → time.Sleep no-op → hot
			// retry loop against a persistently-erroring
			// endpoint.
			shift := attempt - 1
			if shift > 6 {
				shift = 6
			}
			backoff := c.opts.BaseBackoffMs * (1 << shift)
			time.Sleep(time.Duration(backoff) * time.Millisecond)
		}

		resp, err := c.doRequest(messages, onDelta)
		if err == nil {
			return resp, nil
		}

		var sErr *StreamError
		if errors.As(err, &sErr) {
			if sErr.StatusCode == 429 && sErr.RetryAfter > 0 {
				time.Sleep(time.Duration(sErr.RetryAfter) * time.Second)
				lastErr = err
				continue
			}
			if sErr.ContextTooLong {
				return nil, err
			}
			if sErr.Retryable {
				lastErr = err
				continue
			}
			return nil, err
		}

		lastErr = err
	}

	return nil, lastErr
}

func (c *Client) doRequest(messages []map[string]any, onDelta OnDelta) (*Response, error) {
	modelName := c.opts.ModelName
	if modelName == "" {
		modelName = "default"
	}
	body := map[string]any{
		"model":    modelName,
		"messages": messages,
		"stream":   true,
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("stream: marshal request: %w", err)
	}

	// Debug-level breadcrumb: surface the request size so operators
	// hitting gateway 413s can correlate "ghyll sent N bytes →
	// gateway rejected". GHYLL_LOG_LEVEL=debug to see it. Cheap
	// (no body content logged — just the size + message count).
	slog.Debug("stream: outbound request",
		"endpoint", c.endpoint,
		"model", modelName,
		"body_bytes", len(bodyBytes),
		"message_count", len(messages),
	)

	url := strings.TrimRight(c.endpoint, "/") + "/chat/completions"
	req, err := http.NewRequest("POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("stream: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	// Tier 3: merge caller-supplied headers (e.g. Authorization
	// for live-endpoint tests). Caller headers do NOT overwrite
	// the protocol headers above; the for-Add loop appends.
	for k, vs := range c.opts.ExtraHeaders {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &StreamError{
			Retryable: true,
			Message:   err.Error(),
			Err:       err,
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return nil, classifyHTTPError(resp)
	}

	return parseSSEStream(resp.Body, onDelta)
}

// bearerEchoPattern matches any substring that LOOKS like a Bearer
// token or Authorization header value echoed in an upstream error
// body. AUTH-4 / AUTH-W-001 / ADV-AUTH-001: a non-401/403 upstream
// (400 Bad Request, 402, 407, 422, 500/502/503) MAY also echo the
// inbound Authorization header in its error JSON. We strip those
// substrings before surfacing the message so the redaction guard is
// not limited to the 401/403 status codes.
var bearerEchoPattern = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*\S+|bearer\s+\S+|sk-[A-Za-z0-9_\-]{8,})`)

// sanitizeUpstreamMessage strips substrings that resemble a Bearer
// token (Authorization: ..., Bearer ..., sk-...). Used before any
// upstream-controlled body or header is surfaced via StreamError.
func sanitizeUpstreamMessage(s string) string {
	return bearerEchoPattern.ReplaceAllString(s, "<redacted>")
}

// maxRequestIDLen caps the operator-facing X-Request-ID length.
// AUTH-4 / ADV-AUTH-006: upstream-controlled — a malicious or
// debug-happy gateway can stuff arbitrary bytes (or the inbound
// token) into this header. Cap + whitelist printable-ish characters.
const maxRequestIDLen = 64

// safeRequestIDPattern is the only character class accepted in an
// echoed X-Request-ID value. Anything else gets the rid dropped.
var safeRequestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._\-:]+$`)

// sanitizeRequestID applies the length cap + whitelist. Returns ""
// when the input is empty or fails the whitelist.
func sanitizeRequestID(rid string) string {
	if rid == "" {
		return ""
	}
	if len(rid) > maxRequestIDLen {
		rid = rid[:maxRequestIDLen]
	}
	if !safeRequestIDPattern.MatchString(rid) {
		return ""
	}
	return rid
}

func classifyHTTPError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)

	sErr := &StreamError{
		StatusCode: resp.StatusCode,
		Retryable:  resp.StatusCode >= 500,
	}

	// Parse Retry-After for 429
	if resp.StatusCode == 429 {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if sec, err := strconv.Atoi(ra); err == nil {
				sErr.RetryAfter = sec
			}
		}
		sErr.Retryable = true
		sErr.Err = ErrRateLimited
	}

	// Auth-redaction guard: a 401/403 response body MAY echo the
	// Bearer token (some gateways quote the offending header in
	// their error JSON). Replace the body-derived message with a
	// fixed string BEFORE the json.Unmarshal pass below so the
	// surfaced StreamError.Message can never carry the secret to
	// logs, error chains, or the operator UI. The X-Request-ID
	// header (if present) is preserved as a diagnostic — request
	// IDs are operator-set, not token-bearing — but is length-
	// capped + character-whitelisted (AUTH-4 / ADV-AUTH-006).
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		sErr.Message = "authentication failed"
		if rid := sanitizeRequestID(resp.Header.Get("X-Request-ID")); rid != "" {
			sErr.Message = "authentication failed (request-id=" + rid + ")"
		}
		sErr.Retryable = false
		return sErr
	}

	// Check for context_length_exceeded in error body
	var errBody struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &errBody) == nil {
		// AUTH-W-001 / ADV-AUTH-001: scrub Bearer-token-shaped
		// substrings from non-401/403 bodies before surfacing.
		// Some gateways (Cloudflare, internal reverse-proxies) put
		// a malformed Authorization header into 400/422/500/502
		// bodies for "debugging" — we MUST NOT echo it to the
		// operator UI, log, or sub-agent context.
		sErr.Message = sanitizeUpstreamMessage(errBody.Error.Message)
		if strings.Contains(errBody.Error.Message, "context_length_exceeded") {
			sErr.ContextTooLong = true
			sErr.Retryable = false
		}
	}

	// Fallback: if the JSON parse produced no usable message (non-
	// JSON body, plain text, HTML, empty), surface a sanitized
	// excerpt of the raw body. Critical for gateway 413s, 502s,
	// and other plain-text/HTML errors where the canonical OpenAI
	// error envelope isn't present. Capped at 2 KiB to keep error
	// chains bounded.
	if sErr.Message == "" && len(body) > 0 {
		excerpt := string(body)
		if len(excerpt) > 2048 {
			excerpt = excerpt[:2048] + "…(truncated)"
		}
		sErr.Message = sanitizeUpstreamMessage(strings.TrimSpace(excerpt))
	}

	// 413 specifically: prefix with the universal meaning. The
	// body excerpt (above) often just says "413 Request Entity
	// Too Large" or includes the gateway's max byte limit. Either
	// way the operator needs to drop max_context.
	if resp.StatusCode == 413 {
		hint := "gateway rejected request body as too large; lower [models.*].max_context"
		if sErr.Message != "" {
			sErr.Message = hint + " — gateway said: " + sErr.Message
		} else {
			sErr.Message = hint
		}
		// Debug log includes response headers — gateways
		// sometimes return X-RateLimit-Max-Body or similar that
		// pins the exact byte limit.
		slog.Debug("stream: 413 from gateway",
			"body_bytes", len(body),
			"headers", resp.Header,
		)
	}

	return sErr
}

// sseEvent represents a parsed SSE delta.
//
// ReasoningContent is the dialect-agnostic field name for model-side
// reasoning traces emitted on streaming deltas. Kimi 2.5/2.6 ships
// `delta.reasoning_content` on every assistant chunk; other dialects
// either ignore the field or surface it under the same name. The
// inbound half of ADR-v4-009 reads it here; the outbound half lives
// in dialect/helpers.go's buildOpenAIMessages.
//
// Reasoning is the alternative field name some OpenAI-compatible
// gateways and inference backends emit instead of `reasoning_content`
// — confirmed on the CSCS Envoy AI Gateway fronting vLLM (2026-06-08
// probe). When both fields are present on a single delta (unlikely
// but legal JSON), `reasoning_content` wins because it's the spec-
// correct name; when only `reasoning` is set we merge it into the
// same accumulator. Operator-visible behavior is identical either
// way.
type sseEvent struct {
	Choices []struct {
		Delta struct {
			Content          string             `json:"content"`
			ReasoningContent string             `json:"reasoning_content"`
			Reasoning        string             `json:"reasoning"`
			ToolCalls        []sseToolCallDelta `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type sseToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Tier 3 / SR C-5 stream-protection caps:
//
//   - maxSSELineBytes: per-line buffer (bufio.Scanner). Default
//     64 KiB is too small for some events; bumped to 1 MiB.
//   - maxStreamContentBytes: total response content budget. A
//     malicious endpoint emitting an infinite SSE stream of small
//     deltas previously OOMed via the unbounded contentBuilder.
//   - maxToolCallArgsBytes: per-tool-call Arguments accumulation
//     cap. Same OOM class via existing.Function.Arguments += ...
const (
	maxSSELineBytes       = 1 << 20  // 1 MiB
	maxStreamContentBytes = 16 << 20 // 16 MiB
	maxToolCallArgsBytes  = 1 << 20  // 1 MiB per tool call
)

// ErrStreamSizeCap is returned by parseSSEStream when the
// response exceeds the configured byte budget.
var ErrStreamSizeCap = errors.New("stream: response exceeds size cap")

func parseSSEStream(body io.Reader, onDelta OnDelta) (*Response, error) {
	result := &Response{}
	var contentBuilder strings.Builder
	var reasoningBuilder strings.Builder
	toolCallMap := map[int]*types.ToolCall{}
	gotDone := false

	scanner := bufio.NewScanner(body)
	// Tier 3 / SR C-5: bigger buffer for fat SSE frames.
	scanner.Buffer(make([]byte, 0, 64<<10), maxSSELineBytes)
	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			gotDone = true
			break
		}

		var event sseEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			// Malformed frame — skip and continue (invariant: malformed SSE scenario)
			continue
		}

		if len(event.Choices) == 0 {
			continue
		}

		choice := event.Choices[0]

		// Accumulate content and stream delta. Tier 3 / SR C-5:
		// abort if total exceeds budget.
		if choice.Delta.Content != "" {
			if contentBuilder.Len()+len(choice.Delta.Content) > maxStreamContentBytes {
				return nil, &StreamError{
					Retryable: false,
					Message:   "stream content exceeds cap",
					Err:       ErrStreamSizeCap,
				}
			}
			contentBuilder.WriteString(choice.Delta.Content)
			if onDelta != nil {
				onDelta(choice.Delta.Content)
			}
		}

		// ADR-v4-009 inbound half: accumulate dialect-side
		// reasoning. Cap with the same size budget as content — a
		// malicious endpoint emitting an infinite reasoning stream
		// would otherwise OOM via the reasoningBuilder. We do NOT
		// call onDelta for reasoning because the terminal renderer
		// renders model OUTPUT (content), not the model's chain-
		// of-thought.
		//
		// Accept BOTH `reasoning_content` (the spec-correct name
		// Kimi M-flavor emits) AND `reasoning` (the alternative
		// some OpenAI-compatible gateways emit — confirmed on CSCS
		// Envoy AI Gateway 2026-06-08). Spec-correct wins on the
		// rare case both are populated in one delta.
		reasoningDelta := choice.Delta.ReasoningContent
		if reasoningDelta == "" {
			reasoningDelta = choice.Delta.Reasoning
		}
		if reasoningDelta != "" {
			if reasoningBuilder.Len()+len(reasoningDelta) > maxStreamContentBytes {
				return nil, &StreamError{
					Retryable: false,
					Message:   "stream reasoning_content exceeds cap",
					Err:       ErrStreamSizeCap,
				}
			}
			reasoningBuilder.WriteString(reasoningDelta)
		}

		// Accumulate tool calls
		for _, tc := range choice.Delta.ToolCalls {
			existing, ok := toolCallMap[tc.Index]
			if !ok {
				// Some backends (vLLM 0.6 and earlier, plus
				// quantized derivatives) omit `type` on the FIRST
				// chunk of a streamed tool_call. Default to
				// "function" so the accumulated ToolCall is
				// non-empty for downstream `tc.Type == "function"`
				// consumers. Explicit non-empty types pass through
				// unchanged. The merge path below (else branch)
				// honours later non-empty Type values so a backend
				// emitting `type: ""` on chunk 1 followed by
				// `type: "function"` on chunk 2 also converges.
				tcType := tc.Type
				if tcType == "" {
					tcType = "function"
				}
				existing = &types.ToolCall{
					ID:   tc.ID,
					Type: tcType,
					Function: types.ToolFunction{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
				toolCallMap[tc.Index] = existing
			} else {
				if tc.ID != "" {
					existing.ID = tc.ID
				}
				// K-ADV-7 / WIRE-3: a backend that emits an empty
				// `type` on chunk 1 (defaulted above to "function")
				// and then an explicit `type` on a later chunk
				// previously had its later `type` silently dropped.
				// Symmetric with the ID / Name merge policy. The
				// default value of "function" is preserved when the
				// later chunk also carries an empty Type.
				if tc.Type != "" {
					existing.Type = tc.Type
				}
				if tc.Function.Name != "" {
					existing.Function.Name = tc.Function.Name
				}
				// Tier 3 / SR C-5: cap per-tool-call args.
				if len(existing.Function.Arguments)+len(tc.Function.Arguments) > maxToolCallArgsBytes {
					return nil, &StreamError{
						Retryable: false,
						Message:   "tool call arguments exceed cap",
						Err:       ErrStreamSizeCap,
					}
				}
				existing.Function.Arguments += tc.Function.Arguments
			}
		}

		// Capture finish reason
		if choice.FinishReason != nil {
			result.FinishReason = *choice.FinishReason
		}

		// Capture usage
		if event.Usage != nil {
			result.Usage = Usage{
				PromptTokens:     event.Usage.PromptTokens,
				CompletionTokens: event.Usage.CompletionTokens,
				TotalTokens:      event.Usage.TotalTokens,
			}
		}
	}
	// Tier 3 / SR C-5: scanner.Err must be inspected — silent
	// truncation on bufio.ErrTooLong used to disappear into a
	// short response with no signal.
	if err := scanner.Err(); err != nil {
		return nil, &StreamError{
			Retryable: false,
			Message:   "stream scanner: " + err.Error(),
			Err:       err,
		}
	}

	result.Content = contentBuilder.String()
	result.ReasoningContent = reasoningBuilder.String()

	// Collect tool calls in order
	for i := 0; i < len(toolCallMap); i++ {
		if tc, ok := toolCallMap[i]; ok {
			result.ToolCalls = append(result.ToolCalls, *tc)
		}
	}

	// If we didn't get [DONE], this is a partial response (invariant 20)
	if !gotDone && (result.Content != "" || len(result.ToolCalls) > 0) {
		result.Partial = true
	}

	return result, nil
}
