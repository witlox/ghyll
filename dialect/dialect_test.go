package dialect

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/witlox/ghyll/memory"
	"github.com/witlox/ghyll/types"
)

// Test Minimax dialect functions

func TestMinimax_SystemPrompt(t *testing.T) {
	prompt := MinimaxSystemPrompt("/home/dev/project")
	if prompt == "" {
		t.Fatal("expected non-empty system prompt")
	}
}

func TestMinimax_BuildMessages(t *testing.T) {
	msgs := []types.Message{
		{Role: "user", Content: "hello"},
	}
	built := MinimaxBuildMessages(msgs, "You are a coding assistant.")
	if len(built) != 2 { // system + user
		t.Fatalf("expected 2 messages, got %d", len(built))
	}
	if built[0]["role"] != "system" {
		t.Errorf("first message role = %q", built[0]["role"])
	}
}

func TestMinimax_ParseToolCalls(t *testing.T) {
	raw := json.RawMessage(`[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}]`)
	calls, err := MinimaxParseToolCalls(raw)
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

func TestMinimax_CompactionPrompt(t *testing.T) {
	prompt := MinimaxCompactionPrompt()
	if prompt == "" {
		t.Fatal("expected non-empty compaction prompt")
	}
}

func TestMinimax_TokenCount(t *testing.T) {
	msgs := []types.Message{
		{Role: "user", Content: "hello world"},
		{Role: "assistant", Content: "hi there"},
	}
	count := MinimaxTokenCount(msgs)
	if count <= 0 {
		t.Errorf("expected positive token count, got %d", count)
	}
}

func TestMinimax_HandoffSummary(t *testing.T) {
	cp := memory.Checkpoint{
		Summary:     "Working on auth module refactor",
		ActiveModel: "m25",
		Turn:        10,
	}
	recent := []types.Message{
		{Role: "user", Content: "fix the race condition"},
		{Role: "assistant", Content: "I'll look at session.go"},
	}
	result := MinimaxHandoffSummary(cp, recent)
	if len(result) == 0 {
		t.Fatal("expected non-empty handoff summary")
	}
	// First message should be system with checkpoint context
	if result[0].Role != "system" {
		t.Errorf("first message role = %q, want system", result[0].Role)
	}
}

func TestMinimax_PlanModePrompt(t *testing.T) {
	prompt := MinimaxPlanModePrompt()
	if prompt == "" {
		t.Fatal("expected non-empty plan mode prompt")
	}
	if !strings.Contains(prompt, "PLAN MODE") {
		t.Error("plan mode prompt should mention PLAN MODE")
	}
}

// Test GLM dialect functions

func TestGLM_SystemPrompt(t *testing.T) {
	prompt := GLMSystemPrompt("/home/dev/project")
	if prompt == "" {
		t.Fatal("expected non-empty system prompt")
	}
}

func TestGLM_BuildMessages(t *testing.T) {
	msgs := []types.Message{
		{Role: "user", Content: "explain this code"},
	}
	built := GLMBuildMessages(msgs, "You are a coding assistant.")
	if len(built) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(built))
	}
}

func TestGLM_ParseToolCalls(t *testing.T) {
	raw := json.RawMessage(`[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"main.go\"}"}}]`)
	calls, err := GLMParseToolCalls(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
}

func TestGLM_TokenCount(t *testing.T) {
	msgs := []types.Message{
		{Role: "user", Content: "hello world this is a longer message"},
	}
	count := GLMTokenCount(msgs)
	if count <= 0 {
		t.Errorf("expected positive count, got %d", count)
	}
}

func TestGLM_PlanModePrompt(t *testing.T) {
	prompt := GLMPlanModePrompt()
	if prompt == "" {
		t.Fatal("expected non-empty plan mode prompt")
	}
	if !strings.Contains(prompt, "PLAN MODE") {
		t.Error("plan mode prompt should mention PLAN MODE")
	}
	// GLM plan mode should be more detailed than Minimax
	if len(prompt) <= len(MinimaxPlanModePrompt()) {
		t.Error("GLM plan mode should be at least as detailed as Minimax")
	}
}

func TestGLM_HandoffSummary(t *testing.T) {
	cp := memory.Checkpoint{
		Summary:     "Debugging auth race condition",
		ActiveModel: "glm5",
		Turn:        5,
	}
	recent := []types.Message{
		{Role: "user", Content: "what about the lock?"},
	}
	result := GLMHandoffSummary(cp, recent)
	if len(result) == 0 {
		t.Fatal("expected non-empty handoff summary")
	}
}
