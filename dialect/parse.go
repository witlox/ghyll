package dialect

import (
	"encoding/json"
	"errors"

	"github.com/witlox/ghyll/types"
)

var ErrParseToolCall = errors.New("dialect: failed to parse tool call from response")

// parseOpenAIToolCalls parses the standard OpenAI tool_calls format.
// Shared between dialects that use the same format via SGLang.
func parseOpenAIToolCalls(raw json.RawMessage) ([]types.ToolCall, error) {
	var calls []types.ToolCall
	if err := json.Unmarshal(raw, &calls); err != nil {
		return nil, ErrParseToolCall
	}
	return calls, nil
}
