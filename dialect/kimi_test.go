package dialect

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/witlox/ghyll/memory"
	"github.com/witlox/ghyll/types"
)

// Test Kimi dialect functions (mirror of TestMinimax_* — lines 14-94
// in dialect_test.go — plus Kimi-specific units for the K2.6 wire
// contract: reasoning_content round-trip + functions.<name>:<idx>
// tool-call id contract).

func TestKimi_SystemPrompt(t *testing.T) {
	prompt := KimiSystemPrompt("/home/dev/project")
	if prompt == "" {
		t.Fatal("expected non-empty system prompt")
	}
	if !strings.Contains(prompt, "/home/dev/project") {
		t.Error("prompt should include workdir")
	}
}

func TestKimi_BuildMessages(t *testing.T) {
	msgs := []types.Message{
		{Role: "user", Content: "hello"},
	}
	built := KimiBuildMessages(msgs, "You are a coding assistant.")
	if len(built) != 2 { // system + user
		t.Fatalf("expected 2 messages, got %d", len(built))
	}
	if built[0]["role"] != "system" {
		t.Errorf("first message role = %q", built[0]["role"])
	}
}

// TestKimi_BuildMessages_PreservesReasoningContent — TDD unit for the
// ReasoningContent round-trip on assistant turns. Asserts that the
// reasoning_content field is emitted on the wire for assistant turns
// AND only on assistant turns (user turn with the same field set is
// NOT emitted).
func TestKimi_BuildMessages_PreservesReasoningContent(t *testing.T) {
	msgs := []types.Message{
		{Role: "user", Content: "hello", ReasoningContent: "should not appear"},
		{Role: "assistant", Content: "hi", ReasoningContent: "I should call bash"},
	}
	built := KimiBuildMessages(msgs, "sys")
	// built[0] = system; built[1] = user; built[2] = assistant
	if len(built) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(built))
	}
	if _, ok := built[1]["reasoning_content"]; ok {
		t.Error("user turn must NOT emit reasoning_content (assistant-only round-trip)")
	}
	rc, ok := built[2]["reasoning_content"]
	if !ok {
		t.Fatal("assistant turn missing reasoning_content key")
	}
	if rc != "I should call bash" {
		t.Errorf("reasoning_content = %v, want %q", rc, "I should call bash")
	}
}

func TestKimi_ParseToolCalls(t *testing.T) {
	raw := json.RawMessage(`[{"index":0,"id":"functions.bash:0","type":"function","function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}]`)
	calls, err := KimiParseToolCalls(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].Function.Name != "bash" {
		t.Errorf("name = %q", calls[0].Function.Name)
	}
	if calls[0].ID != "functions.bash:0" {
		t.Errorf("id = %q, want functions.bash:0", calls[0].ID)
	}
}

// TestKimi_ParseToolCalls_EnforcesIDContract — TDD unit for the
// Kimi-specific id contract. The JSON itself is valid; the assertion
// is a contract check on top of parseOpenAIToolCalls — a random UUID
// in the id slot must return errors.Is(err, ErrParseToolCall) so the
// runner can pattern-match it loudly rather than silently dispatching
// against an unparseable id shape.
func TestKimi_ParseToolCalls_EnforcesIDContract(t *testing.T) {
	// Positive: well-formed Kimi id
	good := json.RawMessage(`[{"index":0,"id":"functions.bash:0","type":"function","function":{"name":"bash","arguments":"{}"}}]`)
	calls, err := KimiParseToolCalls(good)
	if err != nil {
		t.Fatalf("good id rejected: %v", err)
	}
	if calls[0].ID != "functions.bash:0" {
		t.Errorf("good id = %q", calls[0].ID)
	}

	// Negative: UUID-shaped id is the documented sentinel of a
	// non-conformant Kimi backend. Surface ErrParseToolCall so the
	// session loop can name the offending shape in the operator
	// diagnostic.
	uuid := json.RawMessage(`[{"index":0,"id":"550e8400-e29b-41d4-a716-446655440000","type":"function","function":{"name":"bash","arguments":"{}"}}]`)
	_, err = KimiParseToolCalls(uuid)
	if err == nil {
		t.Fatal("UUID id must return ErrParseToolCall, got nil")
	}
	if !errors.Is(err, ErrParseToolCall) {
		t.Errorf("expected ErrParseToolCall, got %v", err)
	}
}

func TestKimi_CompactionPrompt(t *testing.T) {
	prompt := KimiCompactionPrompt()
	if prompt == "" {
		t.Fatal("expected non-empty compaction prompt")
	}
}

func TestKimi_TokenCount(t *testing.T) {
	msgs := []types.Message{
		{Role: "user", Content: "hello world"},
		{Role: "assistant", Content: "hi there"},
	}
	count := KimiTokenCount(msgs)
	if count <= 0 {
		t.Errorf("expected positive token count, got %d", count)
	}
}

// TestKimi_TokenCount_CJKHeavy — conservative under-count guard for
// CJK-heavy content. Kimi's tighter BPE (3 chars/token for ASCII)
// must still emit >= 1 token per CJK rune; the drift detector
// tolerates under-count but never silent zero.
func TestKimi_TokenCount_CJKHeavy(t *testing.T) {
	// 10 zh-CN runes repeated 100 times = 1000 runes; non-ASCII
	// runes yield >= 1 token each.
	cjk := strings.Repeat("你好世界你好世界你好", 100)
	msgs := []types.Message{{Role: "user", Content: cjk}}
	got := KimiTokenCount(msgs)
	if got < 1000 {
		t.Errorf("CJK 1000 runes → KimiTokenCount = %d; want >= 1000", got)
	}
}

func TestKimi_HandoffSummary(t *testing.T) {
	cp := memory.Checkpoint{
		Summary:     "Working on Kimi dialect",
		ActiveModel: "kimi",
		Turn:        10,
	}
	recent := []types.Message{
		{Role: "user", Content: "continue with the build"},
	}
	result := KimiHandoffSummary(cp, recent)
	if len(result) == 0 {
		t.Fatal("expected non-empty handoff summary")
	}
	if result[0].Role != "system" {
		t.Errorf("first message role = %q, want system", result[0].Role)
	}
}

// TestKimi_HandoffSummary_ZeroCheckpointGuard — D7 guard. A fresh
// session with a zero-value Checkpoint must NOT produce the
// "Continuing from checkpoint (turn 0...)" framing.
func TestKimi_HandoffSummary_ZeroCheckpointGuard(t *testing.T) {
	zero := memory.Checkpoint{}
	recent := []types.Message{{Role: "user", Content: "hi"}}
	out := KimiHandoffSummary(zero, recent)
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

func TestKimi_PlanModePrompt(t *testing.T) {
	prompt := KimiPlanModePrompt()
	if prompt == "" {
		t.Fatal("expected non-empty plan mode prompt")
	}
	if !strings.Contains(prompt, "PLAN MODE") {
		t.Error("plan mode prompt should mention PLAN MODE")
	}
}

// TestKimi_BuildMessages_AssistantToolCallsEmptyContentEmitsNull —
// K-ADV-8 fix: an assistant turn that carries ONLY tool_calls (no
// content) must emit `content: null` on the wire rather than
// `content: ""`. The OpenAI Chat Completions spec says assistant
// content MUST be a string OR null; vLLM strict mode and stricter
// Kimi backends reject empty-string content when tool_calls is also
// present, expecting null.
func TestKimi_BuildMessages_AssistantToolCallsEmptyContentEmitsNull(t *testing.T) {
	msgs := []types.Message{
		{Role: "user", Content: "list files"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []types.ToolCall{
				{ID: "functions.bash:0", Type: "function", Function: types.ToolFunction{Name: "bash", Arguments: `{"command":"ls"}`}},
			},
		},
	}
	built := KimiBuildMessages(msgs, "sys")
	// built[0] = system; built[1] = user; built[2] = assistant
	if len(built) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(built))
	}
	assist := built[2]
	if assist["role"] != "assistant" {
		t.Fatalf("expected assistant in [2], got role=%v", assist["role"])
	}
	content, ok := assist["content"]
	if !ok {
		t.Fatal("assistant turn must carry a content key (possibly null)")
	}
	if content != nil {
		t.Errorf("assistant turn with empty content + tool_calls must emit content: null; got %#v (this rejects on strict Kimi backends)", content)
	}
	// Sanity: a non-empty content stays a string.
	msgs[1].Content = "okay"
	built = KimiBuildMessages(msgs, "sys")
	if got := built[2]["content"]; got != "okay" {
		t.Errorf("non-empty content must stay a string; got %#v", got)
	}
	// Sanity: a NORMAL assistant turn with content but no tool_calls
	// continues to emit content as a string (not null).
	msgs2 := []types.Message{
		{Role: "assistant", Content: "hello"},
	}
	built = KimiBuildMessages(msgs2, "sys")
	if got := built[1]["content"]; got != "hello" {
		t.Errorf("plain assistant content must stay a string; got %#v", got)
	}
	// Sanity: an assistant turn with empty content AND no tool_calls
	// still emits empty-string content (unchanged behaviour). The
	// null-content rule applies ONLY when tool_calls is present.
	msgs3 := []types.Message{
		{Role: "assistant", Content: ""},
	}
	built = KimiBuildMessages(msgs3, "sys")
	if got := built[1]["content"]; got != "" {
		t.Errorf("assistant with no content AND no tool_calls must keep empty string; got %#v", got)
	}
}
