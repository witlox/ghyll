package types

import "time"

// Message is a single entry in the context window.
type Message struct {
	Role       string     `json:"role"` // "system", "user", "assistant", "tool"
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
	// ReasoningContent carries the model's reasoning trace
	// for dialects that surface it (Kimi 2.5/2.6). Assistant turns
	// ONLY — user/tool/system turns never carry this field.
	// Per ADR-v4-009 this field is EXCLUDED from canonical hashing
	// for the same reason ADR-003 excluded Embedding: cross-platform
	// / cross-tokenizer serialization variance.
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

// ToolCall is a structured tool invocation parsed from model output.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // always "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction holds the name and arguments of a tool call.
type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ToolResult is returned from any tool execution.
type ToolResult struct {
	Output   string
	Error    string
	TimedOut bool
	Duration time.Duration
}
