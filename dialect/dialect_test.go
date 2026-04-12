package dialect

import (
	"encoding/json"
	"testing"

	"github.com/witlox/ghyll/memory"
	"github.com/witlox/ghyll/types"
)

// Test M2.5 dialect functions

func TestM25_SystemPrompt(t *testing.T) {
	prompt := M25SystemPrompt("/home/dev/project")
	if prompt == "" {
		t.Fatal("expected non-empty system prompt")
	}
}

func TestM25_BuildMessages(t *testing.T) {
	msgs := []types.Message{
		{Role: "user", Content: "hello"},
	}
	built := M25BuildMessages(msgs, "You are a coding assistant.")
	if len(built) != 2 { // system + user
		t.Fatalf("expected 2 messages, got %d", len(built))
	}
	if built[0]["role"] != "system" {
		t.Errorf("first message role = %q", built[0]["role"])
	}
}

func TestM25_ParseToolCalls(t *testing.T) {
	raw := json.RawMessage(`[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}]`)
	calls, err := M25ParseToolCalls(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].Function.Name != "bash" {
		t.Errorf("name = %q", calls[0].Function.Name)
	}
}

func TestM25_CompactionPrompt(t *testing.T) {
	prompt := M25CompactionPrompt()
	if prompt == "" {
		t.Fatal("expected non-empty compaction prompt")
	}
}

func TestM25_TokenCount(t *testing.T) {
	msgs := []types.Message{
		{Role: "user", Content: "hello world"},
		{Role: "assistant", Content: "hi there"},
	}
	count := M25TokenCount(msgs)
	if count <= 0 {
		t.Errorf("expected positive token count, got %d", count)
	}
}

func TestM25_HandoffSummary(t *testing.T) {
	cp := memory.Checkpoint{
		Summary:     "Working on auth module refactor",
		ActiveModel: "m25",
		Turn:        10,
	}
	recent := []types.Message{
		{Role: "user", Content: "fix the race condition"},
		{Role: "assistant", Content: "I'll look at session.go"},
	}
	result := M25HandoffSummary(cp, recent)
	if len(result) == 0 {
		t.Fatal("expected non-empty handoff summary")
	}
	// First message should be system with checkpoint context
	if result[0].Role != "system" {
		t.Errorf("first message role = %q, want system", result[0].Role)
	}
}

// Test GLM-5 dialect functions

func TestGLM5_SystemPrompt(t *testing.T) {
	prompt := GLM5SystemPrompt("/home/dev/project")
	if prompt == "" {
		t.Fatal("expected non-empty system prompt")
	}
}

func TestGLM5_BuildMessages(t *testing.T) {
	msgs := []types.Message{
		{Role: "user", Content: "explain this code"},
	}
	built := GLM5BuildMessages(msgs, "You are a coding assistant.")
	if len(built) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(built))
	}
}

func TestGLM5_ParseToolCalls(t *testing.T) {
	raw := json.RawMessage(`[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"main.go\"}"}}]`)
	calls, err := GLM5ParseToolCalls(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
}

func TestGLM5_TokenCount(t *testing.T) {
	msgs := []types.Message{
		{Role: "user", Content: "hello world this is a longer message"},
	}
	count := GLM5TokenCount(msgs)
	if count <= 0 {
		t.Errorf("expected positive count, got %d", count)
	}
}

func TestGLM5_HandoffSummary(t *testing.T) {
	cp := memory.Checkpoint{
		Summary:     "Debugging auth race condition",
		ActiveModel: "glm5",
		Turn:        5,
	}
	recent := []types.Message{
		{Role: "user", Content: "what about the lock?"},
	}
	result := GLM5HandoffSummary(cp, recent)
	if len(result) == 0 {
		t.Fatal("expected non-empty handoff summary")
	}
}
