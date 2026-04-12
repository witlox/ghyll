package stream

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
type Response struct {
	Content      string
	ToolCalls    []types.ToolCall
	Usage        Usage
	FinishReason string
	Partial      bool
	RawToolCalls json.RawMessage
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
	}
	return c
}

// Send sends messages to the chat completions endpoint with streaming.
// Retries on 5xx with exponential backoff (invariant 18).
// Returns a StreamError with classification on failure.
func (c *Client) Send(messages []map[string]any) (*Response, error) {
	var lastErr error

	for attempt := 0; attempt <= c.opts.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := c.opts.BaseBackoffMs * (1 << (attempt - 1))
			time.Sleep(time.Duration(backoff) * time.Millisecond)
		}

		resp, err := c.doRequest(messages)
		if err == nil {
			return resp, nil
		}

		var sErr *StreamError
		if errors.As(err, &sErr) {
			// Rate limit: use Retry-After if available
			if sErr.StatusCode == 429 && sErr.RetryAfter > 0 {
				time.Sleep(time.Duration(sErr.RetryAfter) * time.Second)
				lastErr = err
				continue
			}
			// Context too long: don't retry, surface immediately
			if sErr.ContextTooLong {
				return nil, err
			}
			// 5xx: retry
			if sErr.Retryable {
				lastErr = err
				continue
			}
			// 4xx (non-retryable): surface immediately
			return nil, err
		}

		lastErr = err
	}

	return nil, lastErr
}

func (c *Client) doRequest(messages []map[string]any) (*Response, error) {
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

	url := strings.TrimRight(c.endpoint, "/") + "/chat/completions"
	req, err := http.NewRequest("POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("stream: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

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

	return parseSSEStream(resp.Body)
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

	// Check for context_length_exceeded in error body
	var errBody struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &errBody) == nil {
		sErr.Message = errBody.Error.Message
		if strings.Contains(errBody.Error.Message, "context_length_exceeded") {
			sErr.ContextTooLong = true
			sErr.Retryable = false
		}
	}

	return sErr
}

// sseEvent represents a parsed SSE delta.
type sseEvent struct {
	Choices []struct {
		Delta struct {
			Content   string             `json:"content"`
			ToolCalls []sseToolCallDelta `json:"tool_calls"`
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

func parseSSEStream(body io.Reader) (*Response, error) {
	result := &Response{}
	var contentBuilder strings.Builder
	toolCallMap := map[int]*types.ToolCall{}
	gotDone := false

	scanner := bufio.NewScanner(body)
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

		// Accumulate content
		if choice.Delta.Content != "" {
			contentBuilder.WriteString(choice.Delta.Content)
		}

		// Accumulate tool calls
		for _, tc := range choice.Delta.ToolCalls {
			existing, ok := toolCallMap[tc.Index]
			if !ok {
				existing = &types.ToolCall{
					ID:   tc.ID,
					Type: tc.Type,
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
				if tc.Function.Name != "" {
					existing.Function.Name = tc.Function.Name
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

	result.Content = contentBuilder.String()

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
