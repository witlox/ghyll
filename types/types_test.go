package types

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMessage_JSONRoundTrip(t *testing.T) {
	msg := Message{
		Role:    "assistant",
		Content: "hello",
		ToolCalls: []ToolCall{
			{
				ID:   "call_1",
				Type: "function",
				Function: ToolFunction{
					Name:      "bash",
					Arguments: `{"command":"ls"}`,
				},
			},
		},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}

	var got Message
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	if got.Role != msg.Role {
		t.Errorf("Role = %q, want %q", got.Role, msg.Role)
	}
	if len(got.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(got.ToolCalls))
	}
	if got.ToolCalls[0].Function.Name != "bash" {
		t.Errorf("Function.Name = %q, want %q", got.ToolCalls[0].Function.Name, "bash")
	}
}

func TestMessage_OmitsEmptyToolCalls(t *testing.T) {
	msg := Message{Role: "user", Content: "hi"}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	// tool_calls should be omitted when empty
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["tool_calls"]; ok {
		t.Error("expected tool_calls to be omitted when empty")
	}
}

func TestToolResult_Fields(t *testing.T) {
	r := ToolResult{
		Output:   "file.go",
		TimedOut: false,
		Duration: 100 * time.Millisecond,
	}
	if r.Output != "file.go" {
		t.Errorf("Output = %q", r.Output)
	}
	if r.Duration != 100*time.Millisecond {
		t.Errorf("Duration = %v", r.Duration)
	}
}
