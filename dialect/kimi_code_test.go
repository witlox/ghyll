package dialect

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/witlox/ghyll/memory"
	"github.com/witlox/ghyll/types"
)

// Test KimiCode dialect functions. Mirrors kimi_test.go structure but
// asserts the key difference: KimiCodeParseToolCalls accepts any
// OpenAI-compatible tool-call ID (UUID, opaque string, etc.) without
// enforcing the `functions.<name>:<index>` shape.

func TestKimiCode_SystemPrompt(t *testing.T) {
	prompt := KimiCodeSystemPrompt("/home/dev/project")
	if prompt == "" {
		t.Fatal("expected non-empty system prompt")
	}
	if !strings.Contains(prompt, "/home/dev/project") {
		t.Error("prompt should include workdir")
	}
}

func TestKimiCode_BuildMessages(t *testing.T) {
	msgs := []types.Message{
		{Role: "user", Content: "hello"},
	}
	built := KimiCodeBuildMessages(msgs, "You are a coding assistant.")
	if len(built) != 2 { // system + user
		t.Fatalf("expected 2 messages, got %d", len(built))
	}
	if built[0]["role"] != "system" {
		t.Errorf("first message role = %q", built[0]["role"])
	}
}

func TestKimiCode_BuildMessages_PreservesReasoningContent(t *testing.T) {
	msgs := []types.Message{
		{Role: "user", Content: "hello", ReasoningContent: "should not appear"},
		{Role: "assistant", Content: "hi", ReasoningContent: "I should call bash"},
	}
	built := KimiCodeBuildMessages(msgs, "sys")
	// built[0] = system; built[1] = user; built[2] = assistant
	if len(built) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(built))
	}
	if _, ok := built[1]["reasoning_content"]; ok {
		t.Error("user turn must NOT emit reasoning_content")
	}
	rc, ok := built[2]["reasoning_content"]
	if !ok {
		t.Fatal("assistant turn missing reasoning_content key")
	}
	if rc != "I should call bash" {
		t.Errorf("reasoning_content = %v, want %q", rc, "I should call bash")
	}
}

// TestKimiCode_ParseToolCalls_AcceptsUUID is the critical contract test
// for the kimi-code dialect: unlike the kimi dialect (which enforces
// `functions.<name>:<index>`), the kimi-code dialect MUST accept any
// OpenAI-compatible tool-call ID including plain UUIDs, because the
// Moonshot Cloud API returns opaque IDs rather than the shaped IDs
// emitted by self-hosted K2 (vLLM/SGLang).
func TestKimiCode_ParseToolCalls_AcceptsUUID(t *testing.T) {
	// UUID-shaped id — rejected by KimiParseToolCalls but MUST be
	// accepted by KimiCodeParseToolCalls.
	uuid := json.RawMessage(`[{"index":0,"id":"550e8400-e29b-41d4-a716-446655440000","type":"function","function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}]`)
	calls, err := KimiCodeParseToolCalls(uuid)
	if err != nil {
		t.Fatalf("UUID id must be accepted by KimiCodeParseToolCalls; got error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].ID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("id = %q", calls[0].ID)
	}
	if calls[0].Function.Name != "bash" {
		t.Errorf("name = %q", calls[0].Function.Name)
	}
}

// TestKimiCode_ParseToolCalls_AlsoAcceptsFunctionsShape ensures that
// the kimi-code dialect is backward-compatible with responses that
// happen to carry the `functions.<name>:<index>` id format (e.g. when
// routing through a gateway that adds the shape, or during a migration).
func TestKimiCode_ParseToolCalls_AlsoAcceptsFunctionsShape(t *testing.T) {
	raw := json.RawMessage(`[{"index":0,"id":"functions.bash:0","type":"function","function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}]`)
	calls, err := KimiCodeParseToolCalls(raw)
	if err != nil {
		t.Fatalf("functions.<name>:<index> id must also be accepted: %v", err)
	}
	if calls[0].ID != "functions.bash:0" {
		t.Errorf("id = %q, want functions.bash:0", calls[0].ID)
	}
}

func TestKimiCode_CompactionPrompt(t *testing.T) {
	prompt := KimiCodeCompactionPrompt()
	if prompt == "" {
		t.Fatal("expected non-empty compaction prompt")
	}
}

func TestKimiCode_TokenCount(t *testing.T) {
	msgs := []types.Message{
		{Role: "user", Content: "hello world"},
		{Role: "assistant", Content: "hi there"},
	}
	count := KimiCodeTokenCount(msgs)
	if count <= 0 {
		t.Errorf("expected positive token count, got %d", count)
	}
}

func TestKimiCode_TokenCount_CJKHeavy(t *testing.T) {
	cjk := strings.Repeat("你好世界你好世界你好", 100)
	msgs := []types.Message{{Role: "user", Content: cjk}}
	got := KimiCodeTokenCount(msgs)
	if got < 1000 {
		t.Errorf("CJK 1000 runes → KimiCodeTokenCount = %d; want >= 1000", got)
	}
}

func TestKimiCode_HandoffSummary(t *testing.T) {
	cp := memory.Checkpoint{
		Summary:     "Working on Kimi Code dialect",
		ActiveModel: "kimi-for-coding",
		Turn:        5,
	}
	recent := []types.Message{
		{Role: "user", Content: "continue with the build"},
	}
	result := KimiCodeHandoffSummary(cp, recent)
	if len(result) == 0 {
		t.Fatal("expected non-empty handoff summary")
	}
	if result[0].Role != "system" {
		t.Errorf("first message role = %q, want system", result[0].Role)
	}
}

func TestKimiCode_HandoffSummary_ZeroCheckpointGuard(t *testing.T) {
	zero := memory.Checkpoint{}
	recent := []types.Message{{Role: "user", Content: "hi"}}
	out := KimiCodeHandoffSummary(zero, recent)
	if len(out) != 1 {
		t.Fatalf("zero checkpoint must return recent as-is; got %d msgs", len(out))
	}
	if out[0].Role != "user" {
		t.Errorf("first msg role = %q, want user (no system framing)", out[0].Role)
	}
	for _, m := range out {
		if strings.Contains(m.Content, "Continuing from checkpoint") {
			t.Errorf("zero checkpoint produced framing: %q", m.Content)
		}
	}
}

func TestKimiCode_PlanModePrompt(t *testing.T) {
	prompt := KimiCodePlanModePrompt()
	if prompt == "" {
		t.Fatal("expected non-empty plan mode prompt")
	}
	if !strings.Contains(prompt, "PLAN MODE") {
		t.Error("plan mode prompt should mention PLAN MODE")
	}
}

// TestKimiCode_BuildMessages_AssistantToolCallsEmptyContentEmitsNull
// mirrors the kimi family's K-ADV-8 fix: assistant turns with only
// tool_calls must emit content:null on the wire.
func TestKimiCode_BuildMessages_AssistantToolCallsEmptyContentEmitsNull(t *testing.T) {
	msgs := []types.Message{
		{Role: "user", Content: "list files"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []types.ToolCall{
				{ID: "call_abc123", Type: "function", Function: types.ToolFunction{Name: "bash", Arguments: `{"command":"ls"}`}},
			},
		},
	}
	built := KimiCodeBuildMessages(msgs, "sys")
	// built[0] = system; built[1] = user; built[2] = assistant
	if len(built) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(built))
	}
	assist := built[2]
	content, ok := assist["content"]
	if !ok {
		t.Fatal("assistant turn must carry a content key (possibly null)")
	}
	if content != nil {
		t.Errorf("assistant turn with empty content + tool_calls must emit content:null; got %#v", content)
	}
}
