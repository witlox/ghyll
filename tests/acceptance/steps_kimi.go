package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/cucumber/godog"
	"github.com/witlox/ghyll/config"
	"github.com/witlox/ghyll/dialect"
	"github.com/witlox/ghyll/stream"
	"github.com/witlox/ghyll/types"
)

// makeTempDir / writeFile / configLoad are tiny shims so the
// KIMI-CFG-5 BDD step bindings don't have to re-import the same
// helpers in three places. They're intentionally minimal.
func makeTempDir() (string, error) {
	return os.MkdirTemp("", "ghyll-kimi-cfg-")
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

func configLoad(path string) (*config.Config, error) {
	return config.Load(path)
}

// registerKimiSteps wires the Kimi 2.5/2.6 BDD scenarios.
//
// State is held in step-local closures (per-scenario, not in
// ScenarioState) so steps_kimi.go cannot stomp the cross-cutting
// stream/server mock that steps_stream.go owns. Each scenario gets
// its own httptest server lifecycle via ctx.Before / ctx.After.
func registerKimiSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	_ = state // ScenarioState reserved for future cross-step plumbing
	var (
		server         *httptest.Server
		client         *stream.Client
		capturedBodies [][]byte
		modelID        string
		ctxMsgs        []types.Message
		lastAssistMsg  types.Message
		lastToolCalls  []types.ToolCall
		nextResponses  [][]byte // queued SSE bodies, FIFO per request
		parseErr       error
	)

	resetServer := func() {
		if server != nil {
			server.Close()
			server = nil
		}
		client = nil
		capturedBodies = nil
		nextResponses = nil
		ctxMsgs = nil
		lastAssistMsg = types.Message{}
		lastToolCalls = nil
		parseErr = nil
	}

	ctx.Before(func(ctx2 context.Context, sc *godog.Scenario) (context.Context, error) {
		resetServer()
		return ctx2, nil
	})
	ctx.After(func(ctx2 context.Context, sc *godog.Scenario, err error) (context.Context, error) {
		resetServer()
		return ctx2, nil
	})

	startServer := func() {
		if server != nil {
			server.Close()
		}
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			capturedBodies = append(capturedBodies, body)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			if len(nextResponses) == 0 {
				// nothing queued → terminator only
				_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
				return
			}
			resp := nextResponses[0]
			nextResponses = nextResponses[1:]
			_, _ = w.Write(resp)
		}))
		client = stream.NewClient(server.URL, &stream.ClientOptions{
			MaxRetries:    1,
			BaseBackoffMs: 1,
			ModelName:     modelID,
		})
	}

	queueAssistantSSE := func(content, reasoning, toolID, toolName, toolArgs string) {
		var b bytes.Buffer
		if reasoning != "" || toolID != "" {
			// Emit a single combined chunk with content + reasoning_content +
			// (optionally) a tool_calls entry. Kimi's SSE merges these on the
			// same delta in the K2.6 wire form.
			delta := map[string]any{}
			if content != "" {
				delta["content"] = content
			}
			if reasoning != "" {
				delta["reasoning_content"] = reasoning
			}
			if toolID != "" {
				delta["tool_calls"] = []map[string]any{{
					"index": 0,
					"id":    toolID,
					"type":  "function",
					"function": map[string]any{
						"name":      toolName,
						"arguments": toolArgs,
					},
				}}
			}
			frame := map[string]any{
				"choices": []map[string]any{{
					"delta":         delta,
					"finish_reason": nil,
				}},
			}
			frameBytes, _ := json.Marshal(frame)
			fmt.Fprintf(&b, "data: %s\n\n", frameBytes)
			if toolID != "" {
				fmt.Fprint(&b, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
			} else {
				fmt.Fprint(&b, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
			}
		} else if content != "" {
			fmt.Fprintf(&b, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":null}]}\n\n", content)
			fmt.Fprint(&b, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		}
		fmt.Fprint(&b, "data: [DONE]\n\n")
		nextResponses = append(nextResponses, b.Bytes())
	}

	ctx.Step(`^a Kimi mock SSE endpoint is configured$`, func() error {
		modelID = "moonshotai/Kimi-K2.6"
		startServer()
		return nil
	})

	ctx.Step(`^the session uses dialect "([^"]*)" and model id "([^"]*)"$`, func(d, id string) error {
		_ = d
		modelID = id
		// Re-init the client with the new model id stamped on requests.
		if server != nil {
			client = stream.NewClient(server.URL, &stream.ClientOptions{
				MaxRetries:    1,
				BaseBackoffMs: 1,
				ModelName:     modelID,
			})
		}
		return nil
	})

	ctx.Step(`^the model emits an assistant turn with reasoning_content "([^"]*)" and tool_call id "([^"]*)" calling "([^"]*)"$`,
		func(reasoning, toolID, toolName string) error {
			args := `{"command":"ls"}`
			// Queue the assistant turn for the FIRST request.
			queueAssistantSSE("", reasoning, toolID, toolName, args)
			// Drive the first model call: the test session sends an
			// initial user prompt.
			ctxMsgs = []types.Message{{Role: "user", Content: "list the test files"}}
			built := dialect.KimiBuildMessages(ctxMsgs, dialect.KimiSystemPrompt("/tmp/kimi-test"))
			resp, err := client.Send(built)
			if err != nil {
				return fmt.Errorf("first model call failed: %v", err)
			}
			lastToolCalls = resp.ToolCalls
			// K-ADV-6 / WIRE-1 remediation: read ReasoningContent
			// from the parsed stream.Response (the actual wire
			// surface), NOT from the step argument. This is the
			// load-bearing assertion — if parseSSEStream silently
			// dropped reasoning_content, lastAssistMsg.ReasoningContent
			// would be empty and the downstream Then-step would fail.
			lastAssistMsg = types.Message{
				Role:             "assistant",
				Content:          resp.Content,
				ToolCalls:        resp.ToolCalls,
				ReasoningContent: resp.ReasoningContent,
			}
			ctxMsgs = append(ctxMsgs, lastAssistMsg)
			return nil
		})

	ctx.Step(`^the session executes the bash tool and the mock returns a follow-up assistant turn with content "([^"]*)"$`,
		func(content string) error {
			// Append the tool result and dispatch the SECOND model call.
			if len(lastToolCalls) == 0 {
				return fmt.Errorf("no tool calls captured from first model call")
			}
			ctxMsgs = append(ctxMsgs, types.Message{
				Role:       "tool",
				Content:    "tests/\n",
				ToolCallID: lastToolCalls[0].ID,
				Name:       lastToolCalls[0].Function.Name,
			})
			queueAssistantSSE(content, "", "", "", "")
			built := dialect.KimiBuildMessages(ctxMsgs, dialect.KimiSystemPrompt("/tmp/kimi-test"))
			_, err := client.Send(built)
			if err != nil {
				return fmt.Errorf("second model call failed: %v", err)
			}
			return nil
		})

	ctx.Step(`^the captured request body for the SECOND model call contains an assistant message with field "([^"]*)" equal to "([^"]*)"$`,
		func(field, want string) error {
			if len(capturedBodies) < 2 {
				return fmt.Errorf("expected >= 2 captured bodies, got %d", len(capturedBodies))
			}
			body := capturedBodies[1]
			var payload struct {
				Messages []map[string]any `json:"messages"`
			}
			if err := json.Unmarshal(body, &payload); err != nil {
				return fmt.Errorf("unmarshal second body: %v\nbody=%s", err, body)
			}
			for _, m := range payload.Messages {
				if m["role"] == "assistant" {
					v, ok := m[field]
					if !ok {
						return fmt.Errorf("assistant message missing field %q (msg=%v)", field, m)
					}
					if v != want {
						return fmt.Errorf("assistant.%s = %v, want %q", field, v, want)
					}
					return nil
				}
			}
			return fmt.Errorf("no assistant message in second body")
		})

	ctx.Step(`^the captured request body for the SECOND model call contains an assistant message with a tool_calls entry whose id equals "([^"]*)"$`,
		func(want string) error {
			if len(capturedBodies) < 2 {
				return fmt.Errorf("expected >= 2 captured bodies, got %d", len(capturedBodies))
			}
			body := capturedBodies[1]
			var payload struct {
				Messages []map[string]any `json:"messages"`
			}
			if err := json.Unmarshal(body, &payload); err != nil {
				return fmt.Errorf("unmarshal second body: %v", err)
			}
			for _, m := range payload.Messages {
				if m["role"] != "assistant" {
					continue
				}
				rawTCs, ok := m["tool_calls"]
				if !ok {
					continue
				}
				tcs, ok := rawTCs.([]any)
				if !ok || len(tcs) == 0 {
					continue
				}
				first, _ := tcs[0].(map[string]any)
				if id, _ := first["id"].(string); id == want {
					return nil
				}
				return fmt.Errorf("first tool_calls.id = %v, want %q", first["id"], want)
			}
			return fmt.Errorf("no assistant tool_calls entry in second body")
		})

	ctx.Step(`^the parsed assistant Message in the session context has ReasoningContent "([^"]*)"$`, func(want string) error {
		if lastAssistMsg.ReasoningContent != want {
			return fmt.Errorf("assistant ReasoningContent = %q, want %q", lastAssistMsg.ReasoningContent, want)
		}
		return nil
	})

	ctx.Step(`^the parsed ToolCall has ID "([^"]*)" and Function\.Name "([^"]*)"$`, func(wantID, wantName string) error {
		if len(lastToolCalls) == 0 {
			return fmt.Errorf("no tool calls parsed")
		}
		if lastToolCalls[0].ID != wantID {
			return fmt.Errorf("ToolCall.ID = %q, want %q", lastToolCalls[0].ID, wantID)
		}
		if lastToolCalls[0].Function.Name != wantName {
			return fmt.Errorf("ToolCall.Function.Name = %q, want %q", lastToolCalls[0].Function.Name, wantName)
		}
		return nil
	})

	// --- Negative scenario: non-conformant tool-call id ---

	ctx.Step(`^the parser receives an assistant tool_call with id "([^"]*)"$`, func(badID string) error {
		raw := json.RawMessage(fmt.Sprintf(`[{"index":0,"id":%q,"type":"function","function":{"name":"bash","arguments":"{}"}}]`, badID))
		_, parseErr = dialect.KimiParseToolCalls(raw)
		// Record the offending id for the diagnostic-content step.
		_ = badID
		return nil
	})

	ctx.Step(`^KimiParseToolCalls returns a wrapped ErrParseToolCall sentinel$`, func() error {
		if parseErr == nil {
			return fmt.Errorf("KimiParseToolCalls did not return an error")
		}
		if !errors.Is(parseErr, dialect.ErrParseToolCall) {
			return fmt.Errorf("error %v is not ErrParseToolCall", parseErr)
		}
		return nil
	})

	ctx.Step(`^the operator-facing diagnostic names the offending id shape$`, func() error {
		if parseErr == nil {
			return fmt.Errorf("no parse error to inspect")
		}
		msg := parseErr.Error()
		if !strings.Contains(msg, "functions.<name>:<index>") {
			return fmt.Errorf("diagnostic %q does not name the required shape", msg)
		}
		return nil
	})

	// --- K-ADV-2 / WIRE-1: inbound reasoning_content round-trip ---

	var streamResp *stream.Response
	ctx.Step(`^the mock emits an assistant turn with reasoning_content "([^"]*)" and content "([^"]*)"$`,
		func(reasoning, content string) error {
			queueAssistantSSE(content, reasoning, "", "", "")
			ctxMsgs = []types.Message{{Role: "user", Content: "go"}}
			built := dialect.KimiBuildMessages(ctxMsgs, dialect.KimiSystemPrompt("/tmp/kimi-test"))
			r, err := client.Send(built)
			if err != nil {
				return fmt.Errorf("send: %v", err)
			}
			streamResp = r
			return nil
		})

	ctx.Step(`^the parsed stream\.Response carries ReasoningContent equal to "([^"]*)"$`, func(want string) error {
		if streamResp == nil {
			return fmt.Errorf("no stream.Response captured")
		}
		if streamResp.ReasoningContent != want {
			return fmt.Errorf("stream.Response.ReasoningContent = %q, want %q (the SSE parser silently dropped reasoning_content)",
				streamResp.ReasoningContent, want)
		}
		return nil
	})

	ctx.Step(`^the parsed stream\.Response carries Content equal to "([^"]*)"$`, func(want string) error {
		if streamResp == nil {
			return fmt.Errorf("no stream.Response captured")
		}
		if streamResp.Content != want {
			return fmt.Errorf("stream.Response.Content = %q, want %q", streamResp.Content, want)
		}
		return nil
	})

	// --- KIMI-CFG-5: drive config.Load against the docs-example
	// Kimi block, asserting the canonical mixed-case literal id
	// is accepted AND lands on the wire model field.

	var (
		kimiCfgPath    string
		kimiCfgLoaded  *kimiCfgFacade
		kimiCfgLoadErr error
	)
	ctx.Step(`^a config\.toml carries the canonical Kimi block with dialect "([^"]*)" and model "([^"]*)"$`,
		func(dialectField, modelField string) error {
			dir, err := makeTempDir()
			if err != nil {
				return err
			}
			kimiCfgPath = dir + "/config.toml"
			body := fmt.Sprintf(`
[models.kimi]
endpoint = "https://ai-gateway.svc.cscs.ch/v1"
dialect = %q
model = %q
max_context = 200000

[routing]
default_model = "kimi"
`, dialectField, modelField)
			return writeFile(kimiCfgPath, []byte(body))
		})

	ctx.Step(`^config\.Load is invoked on the file$`, func() error {
		c, err := loadKimiConfigFacade(kimiCfgPath)
		kimiCfgLoaded = c
		kimiCfgLoadErr = err
		return nil
	})

	ctx.Step(`^no validation error is returned$`, func() error {
		if kimiCfgLoadErr != nil {
			return fmt.Errorf("config.Load returned error: %v", kimiCfgLoadErr)
		}
		return nil
	})

	ctx.Step(`^the loaded model's wire model id equals "([^"]*)"$`, func(want string) error {
		if kimiCfgLoaded == nil {
			return fmt.Errorf("no loaded config to inspect")
		}
		if kimiCfgLoaded.WireModel != want {
			return fmt.Errorf("loaded wire model id = %q, want %q (KIMI-CFG-4 / KIMI-CFG-5)", kimiCfgLoaded.WireModel, want)
		}
		return nil
	})
}

// kimiCfgFacade is a thin view over the loaded model's wire model
// id, isolated so the Kimi BDD scenario does not have to depend on
// the full config.Config shape.
type kimiCfgFacade struct {
	WireModel string
}

func loadKimiConfigFacade(path string) (*kimiCfgFacade, error) {
	cfg, err := configLoad(path)
	if err != nil {
		return nil, err
	}
	mc, ok := cfg.Models["kimi"]
	if !ok {
		return nil, fmt.Errorf("no [models.kimi] block in loaded config")
	}
	wm := mc.Model
	if wm == "" {
		wm = mc.Dialect
	}
	return &kimiCfgFacade{WireModel: wm}, nil
}
